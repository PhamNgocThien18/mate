package spawn_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// shortWorkspace is newWorkspace under /tmp: the host's socket lives in the
// Mate directory, and a t.TempDir path on macOS is longer than a unix
// socket path may be.
func shortWorkspace(t *testing.T, project string) *store.Workspace {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "mh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	w, err := store.Init(root, store.Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(w.ProjectHome(project), project)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, repo)
	if err := w.AddProject(project, store.ProjectConfig{Repos: []store.RepoConfig{{Path: repo, DefaultBranch: "main"}}}); err != nil {
		t.Fatal(err)
	}
	return w
}

// playHost makes the fake runtime play `mate mate host`: when the start
// runs it in the pane, a host answers on the Mate's socket and Herdr lists
// it under the reserved name; when the host is asked to stop, it leaves.
func playHost(t *testing.T, w *store.Workspace, rt *runtime.Fake, project string, onHost ...func(*matehost.Host)) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	rt.OnRunInPane = func(tab runtime.TabHandle, argv []string) error {
		mateDir := w.MateDir(project)
		l, err := matehost.ReadLaunch(mateDir)
		if err != nil {
			return err
		}
		host := matehost.New(&matehost.Runner{Launch: l, Turns: claude.Claude{}}, matehost.Options{})
		for _, fn := range onHost {
			fn(host)
		}
		handle := runtime.AgentHandle{Session: tab.Session, Name: l.Agent, RawID: project, Kind: claude.KindClaude, Tab: tab}
		rt.SeedAgent(handle, runtime.AgentIdle)
		go func() {
			_ = host.Serve(ctx, matehost.SocketPath(mateDir), func() {
				_ = rt.StopAgent(context.Background(), handle, runtime.StopForce)
			})
		}()
		return nil
	}
}

func TestStartMateHeadlessRunsTheHostAndRecordsTheMode(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(claude.ClaudeConfigDirEnv, "")
	w := shortWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	// The played host leaves after it answers its stop, as the real one
	// exits; the stop's wait for that has to take real time.
	deps.Sleep = func(ctx context.Context, d time.Duration) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
			return nil
		}
	}
	playHost(t, w, rt, "shop")

	res, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", Mode: spawn.ModeHeadless})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	if !res.Headless || res.Agent != "mate-shop" || res.SessionID != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("result = %+v", res)
	}
	if len(rt.PaneRuns) != 1 {
		t.Fatalf("pane runs = %q", rt.PaneRuns)
	}
	want := []string{deps.Binary, "mate", "host", "shop", "--workspace", w.Root()}
	if !slices.Equal(rt.PaneRuns[0], want) {
		t.Fatalf("pane ran %q, want %q", rt.PaneRuns[0], want)
	}
	if slices.Contains(rt.Calls, "StartAgent") {
		t.Fatal("a headless start launched the harness's TUI")
	}
	meta, err := w.ReadMateMeta("shop")
	if err != nil {
		t.Fatal(err)
	}
	if meta[spawn.MetaMode] != spawn.ModeHeadless || meta[spawn.MetaAgent] != "mate-shop" || !w.MateHeadless("shop") {
		t.Fatalf("meta = %v", meta)
	}
	l, err := matehost.ReadLaunch(w.MateDir("shop"))
	if err != nil {
		t.Fatal(err)
	}
	if l.Program != "claude" || l.Agent != "mate-shop" || l.SessionID != res.SessionID || claude.LaunchSessionID(l.Args) != res.SessionID {
		t.Fatalf("launch = %+v", l)
	}
	for _, flag := range []string{"--setting-sources", "--settings", "--tools"} {
		if !slices.Contains(l.Args, flag) {
			t.Errorf("launch args %q lack the Mate's %s", l.Args, flag)
		}
	}
	manual, err := os.ReadFile(filepath.Join(w.MateDir("shop"), "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manual), "You run headless") {
		t.Fatal("the headless Mate's manual lacks the headless rules")
	}

	stop, err := spawn.StopMate(context.Background(), w, deps, "shop")
	if err != nil {
		t.Fatalf("StopMate: %v", err)
	}
	if !stop.Confirmed || stop.Forced != "" {
		t.Fatalf("stop = %+v, want the host's own clean exit", stop)
	}
	if len(rt.ExitPrompts) != 0 {
		t.Fatalf("the stop typed %q into the host", rt.ExitPrompts)
	}
	meta, _ = w.ReadMateMeta("shop")
	if meta[spawn.MetaMode] != spawn.ModeHeadless || meta[spawn.MetaAgent] != "" || w.MateHeadless("shop") {
		t.Fatalf("meta after stop = %v, want the mode kept and the agent dropped", meta)
	}
}

func TestStartMateKeepsTheModeOfTheLastRun(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(claude.ClaudeConfigDirEnv, "")
	w := shortWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	playHost(t, w, rt, "shop")
	if err := w.WriteMateMeta("shop", map[string]string{spawn.MetaHarness: "claude", spawn.MetaMode: spawn.ModeHeadless, spawn.MetaSessionID: "99999999-2222-3333-4444-555555555555"}); err != nil {
		t.Fatal(err)
	}
	res, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", Resume: true})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	if !res.Headless || !res.Resumed || res.SessionID != "99999999-2222-3333-4444-555555555555" {
		t.Fatalf("result = %+v, want a headless resume", res)
	}
	l, _ := matehost.ReadLaunch(w.MateDir("shop"))
	if l.SessionID != res.SessionID {
		t.Fatalf("launch session = %q", l.SessionID)
	}
}

// setMateMode writes project.yaml's `mate.mode` for project.
func setMateMode(t *testing.T, w *store.Workspace, project, mode string) {
	t.Helper()
	cfg, err := w.LoadProject(project)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Mate.Mode = mode
	if err := w.SaveProject(project, cfg); err != nil {
		t.Fatal(err)
	}
}

// project.yaml's `mate.mode: pane` wins over the headless mode the last
// run recorded (CON-220): a Mate that once ran headless comes back in its
// pane at the next start that names no mode.
func TestStartMateProjectConfigBeatsTheModeOfTheLastRun(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(claude.ClaudeConfigDirEnv, "")
	w := shortWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	deps.DefaultMode = spawn.ModeHeadless
	playHost(t, w, rt, "shop")
	if err := w.WriteMateMeta("shop", map[string]string{spawn.MetaHarness: "claude", spawn.MetaMode: spawn.ModeHeadless}); err != nil {
		t.Fatal(err)
	}
	setMateMode(t, w, "shop", spawn.ModePane)

	res, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", Resume: true})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	if res.Headless || res.ModeNote != "" || len(rt.PaneRuns) != 0 || !slices.Contains(rt.Calls, "StartAgent") {
		t.Fatalf("result = %+v, pane runs %q; want the harness's TUI in its pane", res, rt.PaneRuns)
	}
	meta, _ := w.ReadMateMeta("shop")
	if meta[spawn.MetaMode] == spawn.ModeHeadless || w.MateHeadless("shop") {
		t.Fatalf("meta = %v, want the pane mode recorded", meta)
	}
}

// A start from a UI that cannot show a pane runs headless over
// project.yaml's `mate.mode: pane`, and its result says why (CON-220).
func TestStartMateHeadlessOnlyOverConfigPaneSaysSo(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(claude.ClaudeConfigDirEnv, "")
	w := shortWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	playHost(t, w, rt, "shop")
	setMateMode(t, w, "shop", spawn.ModePane)

	res, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", HeadlessOnly: true})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	if !res.Headless || !w.MateHeadless("shop") {
		t.Fatalf("result = %+v, want a headless Mate", res)
	}
	if !strings.Contains(res.ModeNote, "mate.mode is pane") || !strings.Contains(res.ModeNote, "runs headless") {
		t.Fatalf("mode note = %q, want it to say project.yaml's pane was overridden", res.ModeNote)
	}
}

func TestStartMateHeadlessRefusesOtherHarnesses(t *testing.T) {
	w := shortWorkspace(t, "shop")
	rt := runtime.NewFake()
	_, err := spawn.StartMate(context.Background(), w, fakeDeps(t, rt), spawn.StartRequest{Project: "shop", Harness: "codex", Mode: spawn.ModeHeadless})
	if err == nil || !strings.Contains(err.Error(), "cannot run a headless Mate") {
		t.Fatalf("err = %v", err)
	}
}

func TestReloadMateKeepsThePaneAndStartsAFreshSession(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(claude.ClaudeConfigDirEnv, "")
	w := shortWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	deps.NewSessionID = idSequence(t, "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222")
	notes := make(chan string, 4)
	playHost(t, w, rt, "shop", func(h *matehost.Host) {
		h.Subscribe(func(ev matehost.Event) {
			if ev.Note != "" {
				notes <- ev.Note
			}
		})
	})
	start, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", Mode: spawn.ModeHeadless})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	before, _ := w.ReadMateMeta("shop")
	calls := len(rt.Calls)

	res, err := spawn.ReloadMate(context.Background(), w, deps, "shop")
	if err != nil {
		t.Fatalf("ReloadMate: %v", err)
	}
	const fresh = "22222222-2222-2222-2222-222222222222"
	if res.SessionID != fresh || res.SessionID == start.SessionID || res.Pane != start.Pane || res.Agent != start.Agent || res.Resumed {
		t.Fatalf("reload = %+v, want a fresh session in pane %s", res, start.Pane)
	}
	if len(rt.PaneRuns) != 1 {
		t.Fatalf("pane runs = %q, want the host started once", rt.PaneRuns)
	}
	for _, c := range rt.Calls[calls:] {
		if c == "RemoveTab" || c == "CreateAgentTab" || strings.HasPrefix(c, "StopAgent") || c == "RunInPane" {
			t.Fatalf("the reload called %s: %q", c, rt.Calls[calls:])
		}
	}
	meta, _ := w.ReadMateMeta("shop")
	for _, key := range []string{spawn.MetaAgent, spawn.MetaPane, spawn.MetaTab, spawn.MetaWorkspace, spawn.MetaSession, spawn.MetaMode} {
		if meta[key] != before[key] {
			t.Errorf("meta %s = %q, was %q", key, meta[key], before[key])
		}
	}
	if meta[spawn.MetaSessionID] != fresh {
		t.Fatalf("meta session_id = %q, want %s", meta[spawn.MetaSessionID], fresh)
	}
	l, err := matehost.ReadLaunch(w.MateDir("shop"))
	if err != nil {
		t.Fatal(err)
	}
	if l.SessionID != fresh || claude.LaunchSessionID(l.Args) != fresh || l.Agent != start.Agent {
		t.Fatalf("launch = %+v", l)
	}
	select {
	case note := <-notes:
		if !strings.Contains(note, fresh) {
			t.Fatalf("host note = %q", note)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the host was not switched")
	}
}

func TestReloadMateRefusesWhatItCannotSwitch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	w := shortWorkspace(t, "shop")
	deps := fakeDeps(t, runtime.NewFake())
	for name, meta := range map[string]map[string]string{
		"pane mode":      {spawn.MetaHarness: "claude", spawn.MetaAgent: "mate-shop", spawn.MetaPane: "w1:p1", spawn.MetaSessionID: "s"},
		"stopped":        {spawn.MetaHarness: "claude", spawn.MetaMode: spawn.ModeHeadless, spawn.MetaSessionID: "s"},
		"host not there": {spawn.MetaHarness: "claude", spawn.MetaMode: spawn.ModeHeadless, spawn.MetaAgent: "mate-shop", spawn.MetaPane: "w1:p1", spawn.MetaSessionID: "s"},
	} {
		if err := w.WriteMateMeta("shop", meta); err != nil {
			t.Fatal(err)
		}
		if _, err := spawn.ReloadMate(context.Background(), w, deps, "shop"); !errors.Is(err, spawn.ErrNoReload) {
			t.Errorf("%s: reload = %v, want ErrNoReload", name, err)
		}
	}
}
