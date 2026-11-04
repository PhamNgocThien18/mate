package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/dashboard"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

// Whether a Mate runs headless is asked by the UI a start comes from
// (CON-205): a console that cannot split its terminal and a dashboard page
// cannot show a pane Mate, and a console that can leaves the mode to the
// last run. These tests press the console's one action function the way
// its TUI (headlessOnlyAction) and its pages (dashboardActionRequest) do,
// over the fake Herdr adapter, and read the mode StartMate recorded in
// mate.meta.

// headlessFixture is consoleFixture in a workspace under /tmp, with the
// fake runtime playing `mate mate host`: a start that runs headless gets a
// host answering on the Mate's socket, and Herdr lists it. The socket lives
// in the Mate directory, and a t.TempDir path on macOS is longer than a
// unix socket path may be. Nothing asks for a mode by default
// (consoleFixture's DefaultMode is empty), so a fresh Mate runs in its pane.
func headlessFixture(t *testing.T, project string) (*store.Workspace, spawn.Deps, *runtime.Fake) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv(claude.ClaudeConfigDirEnv, "")
	_, deps := consoleFixture(t, project)
	root, err := os.MkdirTemp("/tmp", "ho")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	w, err := store.Init(root, workspaceDefaults())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(w.ProjectHome(project), project)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)
	if err := w.AddProject(project, store.ProjectConfig{Repos: []store.RepoConfig{{Path: repo, DefaultBranch: "main"}}}); err != nil {
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
	t.Cleanup(cancel)
	rt.OnRunInPane = func(tab runtime.TabHandle, argv []string) error {
		mateDir := w.MateDir(project)
		l, err := matehost.ReadLaunch(mateDir)
		if err != nil {
			return err
		}
		host := matehost.New(&matehost.Runner{
			Launch: l,
			Turns:  claude.Claude{},
			Lock:   func() (func(), bool, error) { return func() {}, true, nil },
			Opened: func(string, string) bool { return false },
		}, matehost.Options{})
		handle := runtime.AgentHandle{Session: tab.Session, Name: l.Agent, RawID: project, Kind: claude.KindClaude, Tab: tab}
		rt.SeedAgent(handle, runtime.AgentIdle)
		go func() {
			_ = host.Serve(ctx, matehost.SocketPath(mateDir), func() {
				_ = rt.StopAgent(context.Background(), handle, runtime.StopForce)
			})
		}()
		return nil
	}
	return w, deps, rt
}

// mateMode is how the project's Mate runs now, by mate.meta: headless,
// pane, or stopped when it names no agent.
func mateMode(t *testing.T, w *store.Workspace, project string) string {
	t.Helper()
	meta, err := w.ReadMateMeta(project)
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

// paneMate leaves shop's last run in its pane, as a console that can split
// starts it: stopped, or still running with an empty composer, so that a
// restart's stow goes in.
func paneMate(t *testing.T, w *store.Workspace, rt *runtime.Fake, action console.ActionFunc, running bool) {
	t.Helper()
	ctx := context.Background()
	if _, err := headlessOnlyAction(action, false)(ctx, console.ActionRequest{Action: console.ActionStart, Target: "shop", TargetKind: "mate"}); err != nil {
		t.Fatalf("start shop's Mate in its pane: %v", err)
	}
	if got := mateMode(t, w, "shop"); got != spawn.ModePane {
		t.Fatalf("setup: shop's Mate runs %s, want its pane", got)
	}
	if running {
		rt.SetReadOutput(mateAgent(t, w), claudeEmptyScreen)
		return
	}
	if _, err := action(ctx, console.ActionRequest{Action: console.ActionStop, Target: "shop", TargetKind: "mate"}); err != nil {
		t.Fatalf("stop shop's Mate: %v", err)
	}
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

// The Mate-row presses that bring a Mate up, as the TUI's sheet builds
// them.
var mateUpRequests = []console.ActionRequest{
	{Action: console.ActionStart, Target: "shop", TargetKind: "mate"},
	{Action: console.ActionResume, Target: "shop", TargetKind: "mate"},
	{Action: console.ActionOnboard, Target: "shop", TargetKind: "project-mate"},
	{Action: console.ActionRestartMate, Target: "shop", TargetKind: "project"},
}

// A console in a terminal it cannot split (neither WezTerm nor Ghostty)
// has no column to show a pane Mate in, so what its TUI starts, resumes,
// onboards or restarts runs headless, though the Mate's last run was in a
// pane (CON-150). A console that can split asks nothing, and the last run
// keeps its pane.
func TestTheConsoleStartsHeadlessOnlyWhenItCannotSplit(t *testing.T) {
	for _, req := range mateUpRequests {
		for _, split := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/split=%v", req.Action, split), func(t *testing.T) {
				w, deps, rt := headlessFixture(t, "shop")
				action := consoleAction(w, deps)
				paneMate(t, w, rt, action, req.Action == console.ActionRestartMate)

				out, err := headlessOnlyAction(action, !split)(context.Background(), req)
				if err != nil {
					t.Fatalf("%s: %v", req.Action, err)
				}
				want := spawn.ModeHeadless
				if split {
					want = spawn.ModePane
				}
				if got := mateMode(t, w, "shop"); got != want {
					t.Fatalf("%s from a console that can split=%v: the Mate runs %s, want %s (%s)", req.Action, split, got, want, out)
				}
			})
		}
	}
}

// Mate Office and Agent City cannot show a pane Mate either: what a page
// starts, resumes, onboards or restarts runs headless when the harness
// can, though the last run was in a pane and whatever the console serving
// the page can split (CON-205).
func TestAPageStartsTheMateHeadless(t *testing.T) {
	for _, req := range mateUpRequests {
		t.Run(string(req.Action), func(t *testing.T) {
			w, deps, rt := headlessFixture(t, "shop")
			action := consoleAction(w, deps)
			paneMate(t, w, rt, action, req.Action == console.ActionRestartMate)

			out, err := action(context.Background(), dashboardActionRequest(dashboard.ActionRequest{Action: string(req.Action), Project: "shop"}))
			if err != nil {
				t.Fatalf("%s pressed on a page: %v", req.Action, err)
			}
			if got := mateMode(t, w, "shop"); got != spawn.ModeHeadless {
				t.Fatalf("%s pressed on a page: the Mate runs %s, want headless (%s)", req.Action, got, out)
			}
		})
	}
}

// A harness without headless turns still starts from a page: in its pane,
// without an error.
func TestAPageStartsAPaneOnlyHarnessInItsPane(t *testing.T) {
	w, deps, rt := headlessFixture(t, "shop")
	out, err := consoleAction(w, deps)(context.Background(), dashboardActionRequest(dashboard.ActionRequest{Action: "onboard", Project: "shop", Harness: "codex"}))
	if err != nil {
		t.Fatalf("onboard a Codex Mate from a page: %v", err)
	}
	meta, err := w.ReadMateMeta("shop")
	if err != nil {
		t.Fatal(err)
	}
	if got := mateMode(t, w, "shop"); got != spawn.ModePane || meta[spawn.MetaHarness] != "codex" || len(rt.PaneRuns) != 0 {
		t.Fatalf("the Mate runs %s on %q, pane runs %q; want Codex in its pane (%s)", got, meta[spawn.MetaHarness], rt.PaneRuns, out)
	}
}

// The console's TUI and its pages press one action function, so they
// share one restartHolds: a restart the console held because the Mate's
// composer holds unsent text is confirmed by the same restart pressed on a
// page within the window, as a second press in the console would be
// (CON-141, CON-205). The page's restart runs the Mate headless.
func TestARestartHeldInTheConsoleIsConfirmedOnAPage(t *testing.T) {
	w, deps, rt := headlessFixture(t, "shop")
	action := consoleAction(w, deps)
	paneMate(t, w, rt, action, true)
	rt.SetReadOutput(mateAgent(t, w), claudePendingScreen)
	ctx := context.Background()

	out, err := headlessOnlyAction(action, false)(ctx, console.ActionRequest{Action: console.ActionRestartMate, Target: "shop", TargetKind: "project"})
	if err != nil || !strings.HasPrefix(out, "held: ") {
		t.Fatalf("the console's press = %q, %v; want it held for the unsent text", out, err)
	}
	out, err = action(ctx, dashboardActionRequest(dashboard.ActionRequest{Action: "restart_mate", Project: "shop"}))
	if err != nil {
		t.Fatalf("the page's press: %v", err)
	}
	if !strings.HasPrefix(out, "not stowed: the composer held unsent text; restarted on your confirmation; stopped ") {
		t.Fatalf("the page's press = %q, want the console's hold taken as the confirmation", out)
	}
	if len(rt.SentText) != 0 {
		t.Fatalf("the confirmed restart typed over the captain's text: %+v", rt.SentText)
	}
	if got := mateMode(t, w, "shop"); got != spawn.ModeHeadless {
		t.Fatalf("the Mate restarted from a page runs %s, want headless", got)
	}
}
