package spawn_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/query"
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

// hostPlay plays `mate mate host` as deps.Hosts (CON-225): each start runs
// a host in-process on the Mate's socket, holding the host lock as the
// real process does, and the host leaves - socket and lock - when it has
// answered its stop or is sent SIGTERM or SIGKILL.
type hostPlay struct {
	w       *store.Workspace
	project string
	onHost  []func(*matehost.Host)

	mu sync.Mutex
	// stays makes a host answer its stop and stay: only a signal ends it.
	stays   bool
	starts  []spawn.HostSpec
	signals []string
	hosts   map[int]*playedHost
	next    int
}

type playedHost struct {
	end    context.CancelFunc
	exited chan struct{}
}

// playHost plays the host for deps. Its pauses are real: a stop waits for
// the host to let its lock go.
func playHost(t *testing.T, w *store.Workspace, deps *spawn.Deps, project string, onHost ...func(*matehost.Host)) *hostPlay {
	t.Helper()
	p := &hostPlay{w: w, project: project, onHost: onHost, hosts: map[int]*playedHost{}, next: 4100}
	deps.Hosts = spawn.HostFuncs{Start: p.start, Signal: p.signal}
	deps.Sleep = func(ctx context.Context, d time.Duration) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(min(d, 10*time.Millisecond)):
			return nil
		}
	}
	t.Cleanup(p.endAll)
	return p
}

func (p *hostPlay) start(spec spawn.HostSpec) (int, <-chan struct{}, error) {
	l, err := matehost.ReadLaunch(spec.Dir)
	if err != nil {
		return 0, nil, err
	}
	release, ok, err := p.w.TryMateLock(p.project, matehost.HostLock)
	if err != nil || !ok {
		return 0, nil, fmt.Errorf("the host lock is held (%v)", err)
	}
	p.mu.Lock()
	p.next++
	pid := p.next
	p.starts = append(p.starts, spec)
	stays := p.stays
	p.mu.Unlock()
	host := matehost.New(&matehost.Runner{Launch: l, Turns: claude.Claude{}}, matehost.Options{PID: pid})
	for _, fn := range p.onHost {
		fn(host)
	}
	ctx, cancel := context.WithCancel(context.Background())
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		defer release()
		_ = host.Serve(ctx, matehost.SocketPath(spec.Dir), func() {
			if !stays {
				cancel()
			}
		})
	}()
	p.mu.Lock()
	p.hosts[pid] = &playedHost{end: cancel, exited: exited}
	p.mu.Unlock()
	return pid, exited, nil
}

func (p *hostPlay) signal(pid int, sig syscall.Signal) error {
	p.mu.Lock()
	h, ok := p.hosts[pid]
	p.signals = append(p.signals, fmt.Sprintf("%s %d", sig, pid))
	p.mu.Unlock()
	if !ok {
		return syscall.ESRCH
	}
	h.end()
	return nil
}

// kill ends host pid as a crash would: no stop op, nobody told.
func (p *hostPlay) kill(t *testing.T, pid int) {
	t.Helper()
	p.mu.Lock()
	h := p.hosts[pid]
	p.mu.Unlock()
	if h == nil {
		t.Fatalf("no played host %d", pid)
	}
	h.end()
	<-h.exited
}

func (p *hostPlay) endAll() {
	p.mu.Lock()
	hosts := make([]*playedHost, 0, len(p.hosts))
	for _, h := range p.hosts {
		hosts = append(hosts, h)
	}
	p.mu.Unlock()
	for _, h := range hosts {
		h.end()
		<-h.exited
	}
}

// noHerdr fails when rt was asked for anything a headless Mate outside
// Herdr must not need: a session, a workspace, a tab, a pane or an agent.
func noHerdr(t *testing.T, rt *runtime.Fake, from int) {
	t.Helper()
	for _, c := range rt.Calls[from:] {
		switch strings.Fields(c)[0] {
		case "EnsureSession", "EnsureProjectWorkspace", "LookupProjectWorkspace", "CreateAgentTab", "RunInPane",
			"StartAgent", "WaitAgent", "InspectAgent", "ListAgents", "StopAgent", "RemoveTab", "LookupSession":
			t.Fatalf("Herdr was asked %s: %q", c, rt.Calls[from:])
		}
	}
}

func TestStartMateHeadlessRunsTheHostOutsideHerdrAndRecordsIt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(claude.ClaudeConfigDirEnv, "")
	// A start run from a Herdr pane: the host must not take that pane.
	t.Setenv("HERDR_PANE_ID", "w9:p9")
	t.Setenv("MATE_CREW_ID", "k1")
	w := shortWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	play := playHost(t, w, &deps, "shop")

	res, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", Mode: spawn.ModeHeadless})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	if !res.Headless || res.Agent != "mate-shop" || res.SessionID != "11111111-2222-3333-4444-555555555555" || res.Pane != "" || res.PID != 4101 {
		t.Fatalf("result = %+v", res)
	}
	if res.Place() != "headless, host pid 4101" {
		t.Fatalf("place = %q", res.Place())
	}
	noHerdr(t, rt, 0)
	if len(play.starts) != 1 {
		t.Fatalf("host starts = %+v", play.starts)
	}
	spec := play.starts[0]
	want := []string{deps.Binary, "mate", "host", "shop", "--workspace", w.Root(), "--no-repl"}
	if !slices.Equal(spec.Argv, want) || spec.Dir != w.MateDir("shop") || spec.Log != matehost.LogPath(w.MateDir("shop")) {
		t.Fatalf("host start = %+v, want %q in the Mate directory", spec, want)
	}
	for _, kv := range []string{"MATE_PROJECT_ID=shop", "MATE_AGENT_ROLE=mate", "MATE_CALLER=mate"} {
		if !slices.Contains(spec.Env, kv) {
			t.Errorf("host env lacks %s", kv)
		}
	}
	for _, kv := range spec.Env {
		if strings.HasPrefix(kv, "HERDR_PANE_ID=") || strings.HasPrefix(kv, "MATE_CREW_ID=") {
			t.Errorf("host env carries %s from the process that started it", kv)
		}
	}
	meta, err := w.ReadMateMeta("shop")
	if err != nil {
		t.Fatal(err)
	}
	if meta[spawn.MetaMode] != spawn.ModeHeadless || meta[spawn.MetaAgent] != "mate-shop" || !w.MateHeadless("shop") {
		t.Fatalf("meta = %v", meta)
	}
	if meta[spawn.MetaPID] != "4101" || meta[spawn.MetaSocket] != matehost.SocketPath(w.MateDir("shop")) {
		t.Fatalf("meta = %v, want the host's pid and socket", meta)
	}
	for _, key := range []string{spawn.MetaPane, spawn.MetaTab, spawn.MetaWorkspace} {
		if meta[key] != "" {
			t.Fatalf("meta %s = %q, want none for a host outside Herdr", key, meta[key])
		}
	}
	if !store.MateHostDetached(meta) {
		t.Fatalf("meta = %v, want a host of its own", meta)
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
	if alive := spawn.HostLiveness(context.Background(), w); !alive["shop"] {
		t.Fatalf("host liveness = %v, want shop answering", alive)
	}
	status, err := spawn.MateStatus(context.Background(), w, deps, "shop")
	if err != nil || status.State != spawn.StateRunning {
		t.Fatalf("status = %+v, %v", status, err)
	}

	calls := len(rt.Calls)
	stop, err := spawn.StopMate(context.Background(), w, deps, "shop")
	if err != nil {
		t.Fatalf("StopMate: %v", err)
	}
	if !stop.Confirmed || stop.Forced != "" || stop.AlreadyGone {
		t.Fatalf("stop = %+v, want the host's own clean exit", stop)
	}
	noHerdr(t, rt, calls)
	if len(play.signals) != 0 {
		t.Fatalf("signals = %q, want none after a clean stop", play.signals)
	}
	meta, _ = w.ReadMateMeta("shop")
	if meta[spawn.MetaMode] != spawn.ModeHeadless || meta[spawn.MetaAgent] != "" || meta[spawn.MetaPID] != "" || w.MateHeadless("shop") {
		t.Fatalf("meta after stop = %v, want the mode kept and the agent and pid dropped", meta)
	}
	if meta[spawn.MetaSessionID] != res.SessionID {
		t.Fatalf("meta after stop = %v, want session %s kept for a resume", meta, res.SessionID)
	}
}

// A host that answers its stop but stays is sent SIGTERM, and the stop
// says so.
func TestStopMateSignalsAHostThatDoesNotLeave(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(claude.ClaudeConfigDirEnv, "")
	w := shortWorkspace(t, "shop")
	deps := fakeDeps(t, runtime.NewFake())
	play := playHost(t, w, &deps, "shop")
	play.stays = true
	res, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", Mode: spawn.ModeHeadless})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	stop, err := spawn.StopMate(context.Background(), w, deps, "shop")
	if err != nil {
		t.Fatalf("StopMate: %v", err)
	}
	if !stop.Confirmed || !strings.Contains(stop.Forced, "SIGTERM") {
		t.Fatalf("stop = %+v, want SIGTERM named", stop)
	}
	if want := []string{fmt.Sprintf("%s %d", syscall.SIGTERM, res.PID)}; !slices.Equal(play.signals, want) {
		t.Fatalf("signals = %q, want %q", play.signals, want)
	}
}

// A host that died (CON-225's acceptance: kill the host process) reads as
// stopped at the next snapshot, and a start resumes its session.
func TestAKilledHostReadsAsStoppedAndAStartResumesIt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(claude.ClaudeConfigDirEnv, "")
	w := shortWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	deps.NewSessionID = idSequence(t, "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222")
	play := playHost(t, w, &deps, "shop")
	first, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", Mode: spawn.ModeHeadless})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}

	snapshot := func() query.MateStatus {
		t.Helper()
		live := query.Liveness{Asked: true, Hosts: spawn.HostLiveness(context.Background(), w)}
		snap, err := query.LoadLive(context.Background(), w, query.Harnesses{}, nil, live)
		if err != nil {
			t.Fatal(err)
		}
		return snap.Projects[0].Mate.Designated.Value.Status
	}
	if got := snapshot(); got != query.MateRunning {
		t.Fatalf("status = %q, want running", got)
	}
	play.kill(t, first.PID)
	if got := snapshot(); got != query.MateStopped {
		t.Fatalf("status = %q after the host died, want stopped", got)
	}
	if status, err := spawn.MateStatus(context.Background(), w, deps, "shop"); err != nil || status.State != spawn.StateStale {
		t.Fatalf("mate status = %+v, %v, want stale", status, err)
	}

	calls := len(rt.Calls)
	again, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", Resume: true})
	if err != nil {
		t.Fatalf("StartMate again: %v", err)
	}
	noHerdr(t, rt, calls)
	if !again.StaleMeta || !again.Headless || !again.Resumed || again.SessionID != first.SessionID || again.PID == first.PID {
		t.Fatalf("restart = %+v, want the dead host's session %s resumed in a new host", again, first.SessionID)
	}
	if got := snapshot(); got != query.MateRunning {
		t.Fatalf("status = %q after the start, want running", got)
	}
	// A start over the live one is refused.
	if _, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", Resume: true}); !errors.Is(err, spawn.ErrMateRunning) && !strings.Contains(fmt.Sprint(err), spawn.ErrMateRunning.Error()) {
		t.Fatalf("second start = %v, want ErrMateRunning", err)
	}
}

// A host that exits before it answers fails the start with the end of its
// log, and leaves no meta.
func TestStartMateHeadlessReportsAHostThatExitedAtOnce(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(claude.ClaudeConfigDirEnv, "")
	w := shortWorkspace(t, "shop")
	deps := fakeDeps(t, runtime.NewFake())
	deps.Hosts = spawn.HostFuncs{Start: func(spec spawn.HostSpec) (int, <-chan struct{}, error) {
		if err := os.WriteFile(spec.Log, []byte("mate mate host shop: the launch is missing\n"), 0o644); err != nil {
			return 0, nil, err
		}
		exited := make(chan struct{})
		close(exited)
		return 4321, exited, nil
	}}
	_, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", Mode: spawn.ModeHeadless})
	if err == nil || !strings.Contains(err.Error(), "exited before it answered") || !strings.Contains(err.Error(), "the launch is missing") {
		t.Fatalf("err = %v, want the host's exit and its log", err)
	}
	if _, statErr := os.Stat(w.MateMeta("shop")); !os.IsNotExist(statErr) {
		t.Fatalf("mate.meta after a failed start: %v", statErr)
	}
}

// A headless Mate an older binary runs in its Herdr pane (CON-225) is
// stopped through Herdr, its pane closed, and the next start brings it
// back as a host of its own on the same session.
func TestRestartMovesAPaneHostOutOfHerdrAndKeepsItsSession(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(claude.ClaudeConfigDirEnv, "")
	w := shortWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	const old = "99999999-2222-3333-4444-555555555555"
	ctx := context.Background()
	spec, err := spawn.SessionSpec(deps, w)
	if err != nil {
		t.Fatal(err)
	}
	session, err := rt.EnsureSession(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	tab := runtime.TabHandle{Session: session, WorkspaceID: "w1", TabID: "w1:t1", PaneID: "w1:p1", Label: spawn.MateTabLabel}
	handle := runtime.AgentHandle{Session: session, Name: "mate-shop", RawID: "shop", Kind: claude.KindClaude, Tab: tab}
	rt.SeedAgent(handle, runtime.AgentIdle)
	if err := w.WriteMateMeta("shop", map[string]string{
		spawn.MetaHarness: "claude", spawn.MetaMode: spawn.ModeHeadless, spawn.MetaAgent: "mate-shop", spawn.MetaSession: session.Name,
		spawn.MetaPane: tab.PaneID, spawn.MetaTab: tab.TabID, spawn.MetaWorkspace: tab.WorkspaceID, spawn.MetaSessionID: old,
	}); err != nil {
		t.Fatal(err)
	}
	if err := matehost.WriteLaunch(w.MateDir("shop"), matehost.Launch{Project: "shop", Agent: "mate-shop", Harness: "claude", Program: "claude", Dir: w.MateDir("shop"), SessionID: old}); err != nil {
		t.Fatal(err)
	}
	// The old host: in its pane, no host lock of this binary's making,
	// and Herdr drops its agent once it exits.
	l, _ := matehost.ReadLaunch(w.MateDir("shop"))
	oldHost := matehost.New(&matehost.Runner{Launch: l, Turns: claude.Claude{}}, matehost.Options{})
	serveCtx, endOld := context.WithCancel(ctx)
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = oldHost.Serve(serveCtx, matehost.SocketPath(w.MateDir("shop")), func() {
			endOld()
			_ = rt.StopAgent(context.Background(), handle, runtime.StopForce)
		})
	}()
	t.Cleanup(func() { endOld(); <-served })
	play := playHost(t, w, &deps, "shop")
	waitPing(t, w.MateDir("shop"))
	if live := spawn.HostLiveness(ctx, w); len(live) != 0 {
		t.Fatalf("host liveness = %v, want the pane host left to Herdr", live)
	}

	stop, err := spawn.StopMate(ctx, w, deps, "shop")
	if err != nil {
		t.Fatalf("StopMate: %v", err)
	}
	if !stop.Confirmed || !stop.TabClosed || stop.Forced != "" {
		t.Fatalf("stop = %+v, want the pane host's clean exit and its tab closed", stop)
	}
	if !slices.Contains(rt.Calls, "RemoveTab") {
		t.Fatalf("calls = %q, want the old pane closed", rt.Calls)
	}
	<-served
	calls := len(rt.Calls)
	res, err := spawn.StartMate(ctx, w, deps, spawn.StartRequest{Project: "shop", Resume: true})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	noHerdr(t, rt, calls)
	if !res.Headless || !res.Resumed || res.ResumedFrom != old || res.SessionID != old || res.Pane != "" || len(play.starts) != 1 {
		t.Fatalf("start = %+v, want session %s resumed by a host outside Herdr", res, old)
	}
	meta, _ := w.ReadMateMeta("shop")
	if !store.MateHostDetached(meta) || meta[spawn.MetaSessionID] != old {
		t.Fatalf("meta = %v", meta)
	}
}

func waitPing(t *testing.T, mateDir string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for matehost.Dial(mateDir).Ping(context.Background()) != nil {
		if time.Now().After(deadline) {
			t.Fatal("the host never answered")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestStartMateKeepsTheModeOfTheLastRun(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(claude.ClaudeConfigDirEnv, "")
	w := shortWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	playHost(t, w, &deps, "shop")
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
	play := playHost(t, w, &deps, "shop")
	if err := w.WriteMateMeta("shop", map[string]string{spawn.MetaHarness: "claude", spawn.MetaMode: spawn.ModeHeadless}); err != nil {
		t.Fatal(err)
	}
	setMateMode(t, w, "shop", spawn.ModePane)

	res, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", Resume: true})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	if res.Headless || res.ModeNote != "" || len(play.starts) != 0 || !slices.Contains(rt.Calls, "StartAgent") {
		t.Fatalf("result = %+v, host starts %+v; want the harness's TUI in its pane", res, play.starts)
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
	playHost(t, w, &deps, "shop")
	setMateMode(t, w, "shop", spawn.ModePane)

	res, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", HeadlessOnly: true})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	if !res.Headless || !w.MateHeadless("shop") {
		t.Fatalf("result = %+v, want a headless Mate", res)
	}
	if !strings.Contains(res.ModeNote, "mate.mode is pane") || !strings.Contains(res.ModeNote, "running headless") {
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

func TestReloadMateKeepsTheHostAndStartsAFreshSession(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(claude.ClaudeConfigDirEnv, "")
	w := shortWorkspace(t, "shop")
	rt := runtime.NewFake()
	deps := fakeDeps(t, rt)
	deps.NewSessionID = idSequence(t, "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222")
	notes := make(chan string, 4)
	play := playHost(t, w, &deps, "shop", func(h *matehost.Host) {
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
	if res.SessionID != fresh || res.SessionID == start.SessionID || res.PID != start.PID || res.Agent != start.Agent || res.Resumed {
		t.Fatalf("reload = %+v, want a fresh session in host %d", res, start.PID)
	}
	if len(play.starts) != 1 {
		t.Fatalf("host starts = %+v, want the host started once", play.starts)
	}
	noHerdr(t, rt, calls)
	meta, _ := w.ReadMateMeta("shop")
	for _, key := range []string{spawn.MetaAgent, spawn.MetaPID, spawn.MetaSocket, spawn.MetaPane, spawn.MetaSession, spawn.MetaMode} {
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
		"pane mode":           {spawn.MetaHarness: "claude", spawn.MetaAgent: "mate-shop", spawn.MetaPane: "w1:p1", spawn.MetaSessionID: "s"},
		"stopped":             {spawn.MetaHarness: "claude", spawn.MetaMode: spawn.ModeHeadless, spawn.MetaSessionID: "s"},
		"pane host not there": {spawn.MetaHarness: "claude", spawn.MetaMode: spawn.ModeHeadless, spawn.MetaAgent: "mate-shop", spawn.MetaPane: "w1:p1", spawn.MetaSessionID: "s"},
		"host not there":      {spawn.MetaHarness: "claude", spawn.MetaMode: spawn.ModeHeadless, spawn.MetaAgent: "mate-shop", spawn.MetaPID: "4101", spawn.MetaSessionID: "s"},
	} {
		if err := w.WriteMateMeta("shop", meta); err != nil {
			t.Fatal(err)
		}
		if _, err := spawn.ReloadMate(context.Background(), w, deps, "shop"); !errors.Is(err, spawn.ErrNoReload) {
			t.Errorf("%s: reload = %v, want ErrNoReload", name, err)
		}
	}
}

// A stop cut short (the captain's ^C, a page's deadline) still gives a
// host that stays its SIGTERM, so it can end its turn, before any SIGKILL.
func TestAStopCutShortSendsSIGTERMFirst(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(claude.ClaudeConfigDirEnv, "")
	w := shortWorkspace(t, "shop")
	deps := fakeDeps(t, runtime.NewFake())
	play := playHost(t, w, &deps, "shop")
	play.stays = true
	res, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", Mode: spawn.ModeHeadless})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stop, err := spawn.StopMate(ctx, w, deps, "shop")
	if err != nil {
		t.Fatalf("StopMate: %v", err)
	}
	if want := []string{fmt.Sprintf("%s %d", syscall.SIGTERM, res.PID)}; !stop.Confirmed || !slices.Equal(play.signals, want) {
		t.Fatalf("stop = %+v, signals %q; want %q and a confirmed stop", stop, play.signals, want)
	}
}

// A start whose own host is not the one answering on the socket fails
// rather than record the wrong pid.
func TestStartMateHeadlessWantsItsOwnHostToAnswer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(claude.ClaudeConfigDirEnv, "")
	w := shortWorkspace(t, "shop")
	deps := fakeDeps(t, runtime.NewFake())
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan struct{})
	t.Cleanup(func() { cancel(); <-served })
	deps.Hosts = spawn.HostFuncs{Start: func(spec spawn.HostSpec) (int, <-chan struct{}, error) {
		l, err := matehost.ReadLaunch(spec.Dir)
		if err != nil {
			return 0, nil, err
		}
		other := matehost.New(&matehost.Runner{Launch: l, Turns: claude.Claude{}}, matehost.Options{PID: 99})
		go func() {
			defer close(served)
			_ = other.Serve(ctx, matehost.SocketPath(spec.Dir), nil)
		}()
		return 7, nil, nil
	}}
	deps.Sleep = func(ctx context.Context, d time.Duration) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
			return nil
		}
	}
	_, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", Mode: spawn.ModeHeadless})
	if err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("err = %v, want the start refused", err)
	}
	if _, statErr := os.Stat(w.MateMeta("shop")); !os.IsNotExist(statErr) {
		t.Fatalf("mate.meta after a refused start: %v", statErr)
	}
}

// A host that holds its lock but does not answer is alive, not gone: a
// stop ends it, and recovery must not start a second one beside it. A
// question cut short is not asked.
func TestHostLivenessReadsTheHostLock(t *testing.T) {
	w := shortWorkspace(t, "shop")
	if err := w.WriteMateMeta("shop", map[string]string{
		spawn.MetaHarness: "claude", spawn.MetaAgent: "mate-shop", spawn.MetaMode: spawn.ModeHeadless, spawn.MetaPID: "4242",
	}); err != nil {
		t.Fatal(err)
	}
	if got := spawn.HostLiveness(context.Background(), w); len(got) != 1 || got["shop"] {
		t.Fatalf("liveness = %v, want shop asked and gone", got)
	}
	release, ok, err := w.TryMateLock("shop", matehost.HostLock)
	if err != nil || !ok {
		t.Fatalf("lock: %v %v", ok, err)
	}
	if got := spawn.HostLiveness(context.Background(), w); !got["shop"] {
		t.Fatalf("liveness = %v, want a host that holds its lock alive", got)
	}
	release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := spawn.HostLiveness(ctx, w); len(got) != 0 {
		t.Fatalf("liveness = %v, want nothing asked under a done context", got)
	}
}
