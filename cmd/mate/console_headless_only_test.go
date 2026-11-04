package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/dashboard"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/host"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

// Whether a Mate runs headless is asked by the UI a start comes from
// (CON-205): a console that cannot split its terminal and a dashboard page
// cannot show a pane Mate, and a console that can split leaves the mode to
// the last run. These tests press the action functions the console hands
// its TUI and its dashboard (tuiAndWebActions), over the fake Herdr
// adapter, and read the mode StartMate recorded in mate.meta.

// splitTerminal is a terminal the console can split. Any host will do:
// tuiAndWebActions only asks whether there is one.
var splitTerminal host.Host = layoutFunc(func() {})

// headlessFixture is consoleFixture's shop in a workspace under /tmp, with
// the fake runtime playing `mate mate host`: a start that runs headless
// gets a host answering on the Mate's socket, and Herdr lists it. The
// socket lives in the Mate directory, and a t.TempDir path on macOS is
// longer than a unix socket path may be. Nothing asks for a mode by
// default (consoleFixture's DefaultMode is empty), so a fresh Mate runs in
// its pane.
func headlessFixture(t *testing.T) (*store.Workspace, spawn.Deps, *runtime.Fake) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv(claude.ClaudeConfigDirEnv, "")
	_, deps := consoleFixture(t, "shop")
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
	ctx, cancel := context.WithCancel(context.Background())
	var serving sync.WaitGroup
	// Before the workspace is removed: the hosts stop serving and take
	// their sockets with them.
	t.Cleanup(func() { cancel(); serving.Wait() })
	rt.OnRunInPane = func(tab runtime.TabHandle, argv []string) error {
		mateDir := w.MateDir("shop")
		l, err := matehost.ReadLaunch(mateDir)
		if err != nil {
			return err
		}
		mateHost := matehost.New(&matehost.Runner{
			Launch: l,
			Turns:  claude.Claude{},
			Lock:   func() (func(), bool, error) { return func() {}, true, nil },
			Opened: func(string, string) bool { return false },
		}, matehost.Options{})
		rt.SeedAgent(runtime.AgentHandle{Session: tab.Session, Name: l.Agent, RawID: "shop", Kind: claude.KindClaude, Tab: tab}, runtime.AgentIdle)
		serving.Add(1)
		go func() {
			defer serving.Done()
			_ = mateHost.Serve(ctx, matehost.SocketPath(mateDir), nil)
		}()
		return nil
	}
	return w, deps, rt
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
	tui, _ := tuiAndWebActions(w, deps, nil, splitTerminal)
	if _, err := tui(ctx, console.ActionRequest{Action: console.ActionStart, Target: "shop", TargetKind: "mate"}); err != nil {
		t.Fatalf("start shop's Mate in its pane: %v", err)
	}
	if got := mateMode(t, w); got != spawn.ModePane {
		t.Fatalf("setup: shop's Mate runs %s, want its pane", got)
	}
	if running {
		rt.SetReadOutput(mateAgent(t, w), claudeEmptyScreen)
		return
	}
	if _, err := tui(ctx, console.ActionRequest{Action: console.ActionStop, Target: "shop", TargetKind: "mate"}); err != nil {
		t.Fatalf("stop shop's Mate: %v", err)
	}
}

// The asking UI decides whether the Mate it brings up runs headless. A
// console in a terminal it cannot split (neither WezTerm nor Ghostty) has
// no column to show a pane Mate in, so what its TUI starts, resumes,
// onboards or restarts runs headless, though the last run was in a pane
// (CON-150). A console that can split asks nothing, and the last run keeps
// its pane. Mate Office and Agent City cannot show a pane Mate either, so
// a page's press runs headless even where its console can split (CON-205).
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
		for _, req := range []console.ActionRequest{
			// The Mate-row presses that bring a Mate up, as the sheet
			// builds them.
			{Action: console.ActionStart, Target: "shop", TargetKind: "mate"},
			{Action: console.ActionResume, Target: "shop", TargetKind: "mate"},
			{Action: console.ActionOnboard, Target: "shop", TargetKind: "project-mate"},
			{Action: console.ActionRestartMate, Target: "shop", TargetKind: "project"},
		} {
			t.Run(ui.name+"/"+string(req.Action), func(t *testing.T) {
				w, deps, rt := headlessFixture(t)
				paneMate(t, w, deps, rt, req.Action == console.ActionRestartMate)
				tui, web := tuiAndWebActions(w, deps, nil, ui.terminal)

				var out string
				var err error
				if ui.page {
					out, err = web(context.Background(), dashboardActionRequest(dashboard.ActionRequest{Action: string(req.Action), Project: "shop"}))
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

// A harness without headless turns still starts from a page: in its pane,
// without an error.
func TestAPageStartsAPaneOnlyHarnessInItsPane(t *testing.T) {
	w, deps, rt := headlessFixture(t)
	_, web := tuiAndWebActions(w, deps, nil, splitTerminal)
	out, err := web(context.Background(), dashboardActionRequest(dashboard.ActionRequest{Action: "onboard", Project: "shop", Harness: "codex"}))
	if err != nil {
		t.Fatalf("onboard a Codex Mate from a page: %v", err)
	}
	meta, err := w.ReadMateMeta("shop")
	if err != nil {
		t.Fatal(err)
	}
	if got := mateMode(t, w); got != spawn.ModePane || meta[spawn.MetaHarness] != "codex" || len(rt.PaneRuns) != 0 {
		t.Fatalf("the Mate runs %s on %q, pane runs %q; want Codex in its pane (%s)", got, meta[spawn.MetaHarness], rt.PaneRuns, out)
	}
}

// The console's TUI and its pages press one action function, so they
// share one restartHolds: a restart the console held because the Mate's
// composer holds unsent text is confirmed by the same restart pressed on a
// page within the window, as a second press in the console would be
// (CON-141, CON-205). The page's restart runs the Mate headless.
func TestARestartHeldInTheConsoleIsConfirmedOnAPage(t *testing.T) {
	w, deps, rt := headlessFixture(t)
	paneMate(t, w, deps, rt, true)
	rt.SetReadOutput(mateAgent(t, w), claudePendingScreen)
	tui, web := tuiAndWebActions(w, deps, nil, splitTerminal)
	ctx := context.Background()

	out, err := tui(ctx, console.ActionRequest{Action: console.ActionRestartMate, Target: "shop", TargetKind: "project"})
	if err != nil || !strings.HasPrefix(out, "held: ") {
		t.Fatalf("the console's press = %q, %v; want it held for the unsent text", out, err)
	}
	out, err = web(ctx, dashboardActionRequest(dashboard.ActionRequest{Action: "restart_mate", Project: "shop"}))
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
