package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/core"
	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/host"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// Whether a Mate runs headless is asked by the UI a start comes from
// (CON-205): a console that cannot split its terminal and a dashboard page
// cannot show a pane Mate, and a console that can split leaves the mode to
// project.yaml and the last run. These tests press actions the way the
// console's TUI and its dashboard do (consoleUIs), over the fake Herdr
// adapter, and read the mode StartMate recorded in mate.meta.

// splitTerminal is a terminal the console can split. Any host will do:
// tuiAction only asks whether there is one.
var splitTerminal host.Host = layoutFunc(func() {})

// consoleUIs is how the console's TUI and its pages press an action: both
// through one engine's Act (CON-209), which runs every action itself
// (CON-232), the TUI's HeadlessOnly where it cannot split (h nil). The
// engine is not started: Act needs none of its loops.
func consoleUIs(t *testing.T, w *store.Workspace, deps spawn.Deps, h host.Host) (tui, web coreapi.ActionFunc) {
	t.Helper()
	engine := testEngine(t, core.Options{Workspace: w, Spawn: deps, Harnesses: consoleHarnesses()})
	t.Cleanup(engine.Stop)
	return tuiAction(engine.Act, h), engine.Act
}

// headlessFixture is consoleFixture's shop in a workspace under /tmp, with
// playHosts playing `mate mate host`: a start that runs headless gets a
// host answering on the Mate's socket, outside Herdr (CON-225). The
// socket lives in the Mate directory, and a t.TempDir path on macOS is
// longer than a unix socket path may be. Nothing asks for a mode by
// default (consoleFixture's DefaultMode is empty), so a fresh Mate runs in
// its pane.
func headlessFixture(t *testing.T) (*store.Workspace, spawn.Deps, *runtime.Fake) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv(claude.ClaudeConfigDirEnv, "")
	_, deps := consoleFixture(t, "shop")
	w := tmpShopWorkspace(t)
	// A host that never answers fails the start within the readiness
	// timeout, rather than spinning on a sleep that ignores its context.
	deps.Sleep = func(ctx context.Context, _ time.Duration) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Millisecond):
			return nil
		}
	}
	rt := deps.Runtime.(*runtime.Fake)
	playHosts(t, w, &deps, "shop", func(l matehost.Launch, pid int) *matehost.Host {
		return matehost.New(&matehost.Runner{
			Launch: l,
			Turns:  claude.Claude{},
			Lock:   func() (func(), bool, error) { return func() {}, true, nil },
			Opened: func(string, string) bool { return false },
		}, matehost.Options{PID: pid})
	})
	return w, deps, rt
}

// playedHosts counts the hosts playHosts started.
type playedHosts struct {
	mu     sync.Mutex
	starts int
}

func (p *playedHosts) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.starts
}

// playHosts plays `mate mate host` for deps (CON-225): a host start runs
// mk's host in-process on the Mate's socket, holding the host lock as the
// real process does, and it leaves, lock and socket, once it has answered
// its stop or is signalled.
func playHosts(t *testing.T, w *store.Workspace, deps *spawn.Deps, project string, mk func(l matehost.Launch, pid int) *matehost.Host) *playedHosts {
	t.Helper()
	played := &playedHosts{}
	ctx, cancel := context.WithCancel(context.Background())
	var serving sync.WaitGroup
	var mu sync.Mutex
	ends := map[int]context.CancelFunc{}
	// Before the workspace is removed: the hosts stop serving and take
	// their sockets with them.
	t.Cleanup(func() { cancel(); serving.Wait() })
	deps.Hosts = spawn.HostFuncs{
		Start: func(spec spawn.HostSpec) (int, <-chan struct{}, error) {
			l, err := matehost.ReadLaunch(spec.Dir)
			if err != nil {
				return 0, nil, err
			}
			release, ok, err := w.TryMateLock(project, matehost.HostLock)
			if err != nil || !ok {
				return 0, nil, fmt.Errorf("the host lock is held (%v)", err)
			}
			played.mu.Lock()
			played.starts++
			pid := 5000 + played.starts
			played.mu.Unlock()
			mateHost := mk(l, pid)
			hostCtx, end := context.WithCancel(ctx)
			mu.Lock()
			ends[pid] = end
			mu.Unlock()
			exited := make(chan struct{})
			serving.Add(1)
			go func() {
				defer serving.Done()
				defer close(exited)
				defer release()
				_ = mateHost.Serve(hostCtx, matehost.SocketPath(spec.Dir), end)
			}()
			return pid, exited, nil
		},
		Signal: func(pid int, _ syscall.Signal) error {
			mu.Lock()
			end, ok := ends[pid]
			mu.Unlock()
			if !ok {
				return syscall.ESRCH
			}
			end()
			return nil
		},
	}
	return played
}

// tmpShopWorkspace is a workspace under /tmp with the Project shop and its
// repo. A Mate's socket lives in the Mate directory, and a t.TempDir path
// on macOS is longer than a unix socket path may be.
func tmpShopWorkspace(t *testing.T) *store.Workspace {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "ho")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	w, err := store.Init(root, workspaceDefaults())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(w.ProjectHome("shop"), "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)
	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: repo, DefaultBranch: "main"}}}); err != nil {
		t.Fatal(err)
	}
	return w
}

// mateMode is how shop's Mate runs now, by mate.meta: headless, pane, or
// stopped when it names no agent.
func mateMode(t *testing.T, w *store.Workspace) string {
	t.Helper()
	meta, err := w.ReadMateMeta("shop")
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case meta[spawn.MetaAgent] == "":
		return "stopped"
	case meta[spawn.MetaMode] == spawn.ModeHeadless:
		return spawn.ModeHeadless
	}
	return spawn.ModePane
}

// mateAgent is the agent mate.meta names for shop's Mate.
func mateAgent(t *testing.T, w *store.Workspace) runtime.AgentHandle {
	t.Helper()
	meta, err := w.ReadMateMeta("shop")
	if err != nil {
		t.Fatal(err)
	}
	return runtime.AgentHandle{Session: runtime.SessionHandle{Name: meta[spawn.MetaSession]}, Name: meta[spawn.MetaAgent]}
}

// paneMate leaves shop's last run in its pane, as the TUI of a console
// that can split starts it: stopped, or still running with an empty
// composer, so that a restart's stow goes in.
func paneMate(t *testing.T, w *store.Workspace, deps spawn.Deps, rt *runtime.Fake, running bool) {
	t.Helper()
	ctx := context.Background()
	tui, _ := consoleUIs(t, w, deps, splitTerminal)
	if _, err := tui(ctx, coreapi.ActionRequest{Action: coreapi.ActionStart, Target: "shop", TargetKind: "mate"}); err != nil {
		t.Fatalf("start shop's Mate in its pane: %v", err)
	}
	if got := mateMode(t, w); got != spawn.ModePane {
		t.Fatalf("setup: shop's Mate runs %s, want its pane", got)
	}
	if running {
		rt.SetReadOutput(mateAgent(t, w), claudeEmptyScreen)
		return
	}
	if _, err := tui(ctx, coreapi.ActionRequest{Action: coreapi.ActionStop, Target: "shop", TargetKind: "mate"}); err != nil {
		t.Fatalf("stop shop's Mate: %v", err)
	}
}

// The asking UI decides whether the Mate it brings up runs headless. A
// console in a terminal it cannot split (neither WezTerm nor Ghostty) has
// no column to show a pane Mate in, so what its TUI starts, resumes,
// onboards or restarts runs headless, though the last run was in a pane
// (CON-150). A console that can split asks nothing, and the last run keeps
// its pane (project.yaml names no mode here). Mate Office and Agent City
// cannot show a pane Mate either, so a page's press runs headless even
// where its console can split (CON-205).
// Not parallel: it sets HOME, CLAUDE_CONFIG_DIR in the process environment through headlessFixture.
func TestTheAskingUIDecidesWhetherTheMateRunsHeadless(t *testing.T) {
	for _, ui := range []struct {
		name     string
		terminal host.Host
		page     bool
		want     string
	}{
		{name: "console that cannot split", terminal: nil, want: spawn.ModeHeadless},
		{name: "console that can split", terminal: splitTerminal, want: spawn.ModePane},
		{name: "page", terminal: splitTerminal, page: true, want: spawn.ModeHeadless},
	} {
		for _, req := range []coreapi.ActionRequest{
			// The Mate-row presses that bring a Mate up, as the sheet
			// builds them.
			{Action: coreapi.ActionStart, Target: "shop", TargetKind: "mate"},
			{Action: coreapi.ActionResume, Target: "shop", TargetKind: "mate"},
			{Action: coreapi.ActionOnboard, Target: "shop", TargetKind: "project-mate"},
			{Action: coreapi.ActionRestartMate, Target: "shop", TargetKind: "project"},
		} {
			t.Run(ui.name+"/"+string(req.Action), func(t *testing.T) {
				w, deps, rt := headlessFixture(t)
				paneMate(t, w, deps, rt, req.Action == coreapi.ActionRestartMate)
				tui, web := consoleUIs(t, w, deps, ui.terminal)

				var out string
				var err error
				if ui.page {
					// A page's press is the sheet's request, which the
					// dashboard says is HeadlessOnly (its coreRequest,
					// pinned by internal/dashboard's tests).
					page := req
					page.HeadlessOnly = true
					out, err = web(context.Background(), page)
				} else {
					out, err = tui(context.Background(), req)
				}
				if err != nil {
					t.Fatalf("%s: %v", req.Action, err)
				}
				if got := mateMode(t, w); got != ui.want {
					t.Fatalf("%s pressed in a %s: the Mate runs %s, want %s (%s)", req.Action, ui.name, got, ui.want, out)
				}
			})
		}
	}
}

// project.yaml's `mate.mode: pane` asks for the pane, but a console that
// cannot split and a page have nowhere to show one: the Mate runs
// headless, and the line the console's status line and the page show says
// why (CON-220).
// Not parallel: it sets HOME, CLAUDE_CONFIG_DIR in the process environment through headlessFixture.
func TestAHeadlessOnlyStartOverConfigPaneSaysSo(t *testing.T) {
	for _, ui := range []struct {
		name     string
		terminal host.Host
		page     bool
	}{
		{name: "console that cannot split", terminal: nil},
		{name: "page", terminal: splitTerminal, page: true},
	} {
		for _, req := range []coreapi.ActionRequest{
			{Action: coreapi.ActionStart, Target: "shop", TargetKind: "mate"},
			{Action: coreapi.ActionRestartMate, Target: "shop", TargetKind: "project"},
		} {
			t.Run(ui.name+"/"+string(req.Action), func(t *testing.T) {
				w, deps, rt := headlessFixture(t)
				paneMate(t, w, deps, rt, req.Action == coreapi.ActionRestartMate)
				cfg, err := w.LoadProject("shop")
				if err != nil {
					t.Fatal(err)
				}
				cfg.Mate.Mode = spawn.ModePane
				if err := w.SaveProject("shop", cfg); err != nil {
					t.Fatal(err)
				}
				tui, web := consoleUIs(t, w, deps, ui.terminal)
				var out string
				if ui.page {
					page := req
					page.HeadlessOnly = true
					out, err = web(context.Background(), page)
				} else {
					out, err = tui(context.Background(), req)
				}
				if err != nil {
					t.Fatalf("%s: %v", req.Action, err)
				}
				if got := mateMode(t, w); got != spawn.ModeHeadless {
					t.Fatalf("the Mate runs %s, want headless (%s)", got, out)
				}
				if !strings.Contains(out, "project.yaml's mate.mode is pane") {
					t.Fatalf("result = %q, want it to say project.yaml's pane was overridden", out)
				}
			})
		}
	}
}

// A harness without headless turns still starts from a page: in its pane,
// without an error.
// Not parallel: it sets HOME, CLAUDE_CONFIG_DIR in the process environment through headlessFixture.
func TestAPageStartsAPaneOnlyHarnessInItsPane(t *testing.T) {
	w, deps, rt := headlessFixture(t)
	_, web := consoleUIs(t, w, deps, splitTerminal)
	out, err := web(context.Background(), coreapi.ActionRequest{Action: coreapi.ActionOnboard, Target: "shop", TargetKind: "project-mate", Harness: "codex", HeadlessOnly: true})
	if err != nil {
		t.Fatalf("onboard a Codex Mate from a page: %v", err)
	}
	meta, err := w.ReadMateMeta("shop")
	if err != nil {
		t.Fatal(err)
	}
	if got := mateMode(t, w); got != spawn.ModePane || meta[spawn.MetaHarness] != "codex" || len(rt.PaneRuns) != 0 || meta[spawn.MetaPID] != "" {
		t.Fatalf("the Mate runs %s on %q, pane runs %q, meta %v; want Codex in its pane (%s)", got, meta[spawn.MetaHarness], rt.PaneRuns, meta, out)
	}
}

// The console's TUI and its pages press one engine's Act, so they share
// its restartHolds (CON-231): a restart the console held because the Mate's
// composer holds unsent text is confirmed by the same restart pressed on a
// page within the window, as a second press in the console would be
// (CON-141, CON-205). The page's restart runs the Mate headless.
// Not parallel: it sets HOME, CLAUDE_CONFIG_DIR in the process environment through headlessFixture.
func TestARestartHeldInTheConsoleIsConfirmedOnAPage(t *testing.T) {
	w, deps, rt := headlessFixture(t)
	paneMate(t, w, deps, rt, true)
	rt.SetReadOutput(mateAgent(t, w), claudePendingScreen)
	tui, web := consoleUIs(t, w, deps, splitTerminal)
	ctx := context.Background()

	out, err := tui(ctx, coreapi.ActionRequest{Action: coreapi.ActionRestartMate, Target: "shop", TargetKind: "project"})
	if err != nil || !strings.HasPrefix(out, "held: ") {
		t.Fatalf("the console's press = %q, %v; want it held for the unsent text", out, err)
	}
	out, err = web(ctx, coreapi.ActionRequest{Action: coreapi.ActionRestartMate, Target: "shop", TargetKind: "project", HeadlessOnly: true})
	if err != nil {
		t.Fatalf("the page's press: %v", err)
	}
	if !strings.HasPrefix(out, "not stowed: the composer held unsent text; restarted on your confirmation; stopped ") {
		t.Fatalf("the page's press = %q, want the console's hold taken as the confirmation", out)
	}
	if len(rt.SentText) != 0 {
		t.Fatalf("the confirmed restart typed over the captain's text: %+v", rt.SentText)
	}
	if got := mateMode(t, w); got != spawn.ModeHeadless {
		t.Fatalf("the Mate restarted from a page runs %s, want headless", got)
	}
}

// The other way round: a restart a page held is confirmed by the console's
// press within the window, since the hold is the engine's, not the UI's
// (CON-231).
// Not parallel: it sets HOME, CLAUDE_CONFIG_DIR in the process environment through headlessFixture.
func TestARestartHeldOnAPageIsConfirmedInTheConsole(t *testing.T) {
	w, deps, rt := headlessFixture(t)
	paneMate(t, w, deps, rt, true)
	rt.SetReadOutput(mateAgent(t, w), claudePendingScreen)
	tui, web := consoleUIs(t, w, deps, splitTerminal)
	ctx := context.Background()

	out, err := web(ctx, coreapi.ActionRequest{Action: coreapi.ActionRestartMate, Target: "shop", TargetKind: "project", HeadlessOnly: true})
	if err != nil || !strings.HasPrefix(out, "held: ") {
		t.Fatalf("the page's press = %q, %v; want it held for the unsent text", out, err)
	}
	out, err = tui(ctx, coreapi.ActionRequest{Action: coreapi.ActionRestartMate, Target: "shop", TargetKind: "project"})
	if err != nil {
		t.Fatalf("the console's press: %v", err)
	}
	if !strings.HasPrefix(out, "not stowed: the composer held unsent text; restarted on your confirmation; stopped ") {
		t.Fatalf("the console's press = %q, want the page's hold taken as the confirmation", out)
	}
	if len(rt.SentText) != 0 {
		t.Fatalf("the confirmed restart typed over the captain's text: %+v", rt.SentText)
	}
}
