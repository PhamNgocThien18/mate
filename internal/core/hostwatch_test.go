package core

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/harness/catalog"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/notifications"
	"github.com/nguyenngocanh94/mate/internal/recovery"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// recordHost writes the mate.meta of a headless Mate whose host of its own
// runs as pid, as spawn.StartMate records it.
func recordHost(t *testing.T, w *store.Workspace, project string, pid int) {
	t.Helper()
	if err := w.WriteMateMeta(project, map[string]string{
		spawn.MetaHarness: "claude", spawn.MetaAgent: "mate-" + project, spawn.MetaMode: spawn.ModeHeadless,
		spawn.MetaPID: strconv.Itoa(pid), spawn.MetaSocket: matehost.SocketPath(w.MateDir(project)),
		spawn.MetaSessionID: "11111111-2222-3333-4444-555555555555",
	}); err != nil {
		t.Fatal(err)
	}
}

// fakeHosts plays the hosts and the restart for a hostWatch: alive is
// whether the host now recorded answers, and a restart records a new pid,
// whose host answers unless keepDying.
type fakeHosts struct {
	clock     time.Time
	alive     bool
	asked     bool
	keepDying bool
	fail      error
	restarts  []string
	told      []string
	changed   int
	log       bytes.Buffer
}

func newFakeHosts(t *testing.T, w *store.Workspace) (*fakeHosts, *hostWatch) {
	f := &fakeHosts{clock: time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC), asked: true}
	h := &hostWatch{
		ws:    w,
		every: time.Hour,
		now:   func() time.Time { return f.clock },
		alive: func(context.Context, string) (bool, bool) { return f.alive, f.asked },
		restart: func(_ context.Context, project, pid string) (recovery.Item, bool) {
			f.restarts = append(f.restarts, project+" "+pid)
			if f.fail != nil {
				return recovery.Item{Project: project, Err: f.fail}, true
			}
			n, _ := strconv.Atoi(pid)
			recordHost(t, w, project, n+1)
			f.alive = !f.keepDying
			return recovery.Item{Project: project, Resumed: true}, true
		},
		changed: func() { f.changed++ },
		tell:    func(_ context.Context, project, line string) { f.told = append(f.told, line) },
	}
	h.log = &f.log
	return f, h
}

// A host found dead twice in a row, under the same pid, is started again
// once; one that answers again in between is left alone.
func TestHostWatchRestartsAHostFoundDeadTwiceInARow(t *testing.T) {
	w := shopWorkspace(t)
	recordHost(t, w, "shop", 100)
	f, h := newFakeHosts(t, w)
	ctx := context.Background()

	h.check(ctx)
	if len(f.restarts) != 0 {
		t.Fatalf("restarted after one look: %v", f.restarts)
	}
	f.alive = true
	h.check(ctx)
	f.alive = false
	h.check(ctx)
	if len(f.restarts) != 0 {
		t.Fatalf("restarted a host that answered in between: %v", f.restarts)
	}
	h.check(ctx)
	if len(f.restarts) != 1 || f.restarts[0] != "shop 100" {
		t.Fatalf("restarts = %v, want shop's host pid 100 once", f.restarts)
	}
	h.check(ctx)
	h.check(ctx)
	if len(f.restarts) != 1 {
		t.Fatalf("restarts = %v, want none while the new host answers", f.restarts)
	}
	if !strings.Contains(f.log.String(), "mate core: Mate shop: host pid 100 died; started again (1 of 3 in 10m), resumed its session") {
		t.Fatalf("log = %q", f.log.String())
	}
	if n := h.read(); len(n) != 0 {
		t.Fatalf("notices = %v, want none after a restart that worked", n)
	}
}

// A stop drops the host from mate.meta (spawn.StopMate): a host whose
// record went between the two looks is not started again, nor is one that
// was never a host of its own, nor one a question could not reach.
func TestHostWatchLeavesAStoppedMateAlone(t *testing.T) {
	w := shopWorkspace(t)
	recordHost(t, w, "shop", 100)
	f, h := newFakeHosts(t, w)
	ctx := context.Background()

	h.check(ctx)
	if err := w.WriteMateMeta("shop", map[string]string{spawn.MetaHarness: "claude", spawn.MetaMode: spawn.ModeHeadless, spawn.MetaStoppedAt: "2026-11-06T09:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	h.check(ctx)
	h.check(ctx)
	if len(f.restarts) != 0 || len(h.mates) != 0 {
		t.Fatalf("restarts = %v, watched = %v; want a stopped Mate left alone", f.restarts, h.mates)
	}

	// A Mate in a Herdr pane is Herdr's to report.
	if err := w.WriteMateMeta("shop", map[string]string{spawn.MetaHarness: "claude", spawn.MetaAgent: "mate-shop", spawn.MetaPane: "w1:p1", spawn.MetaMode: spawn.ModeHeadless}); err != nil {
		t.Fatal(err)
	}
	h.check(ctx)
	h.check(ctx)
	if len(f.restarts) != 0 {
		t.Fatalf("restarted a Mate in a pane: %v", f.restarts)
	}

	// A question cut short says nothing about the host.
	recordHost(t, w, "shop", 100)
	f.asked = false
	h.check(ctx)
	h.check(ctx)
	if len(f.restarts) != 0 {
		t.Fatalf("restarted on unasked questions: %v", f.restarts)
	}
}

// While an action of a UI holds the Mate (a stop, a start, a restart), the
// watch starts nothing: that action decides.
func TestHostWatchWaitsForAnActionOnTheMate(t *testing.T) {
	l := newLiveHosts(t)
	recordHost(t, l.w, "shop", 4242)
	e := New(Options{Workspace: l.w, Spawn: l.deps, Harnesses: testHarnesses, Stderr: &bytes.Buffer{}})
	defer e.Stop()
	e.hosts.every = 20 * time.Millisecond
	// A UI's stop on the Mate holds it, as Act takes it.
	key, shown := lockOf(coreapi.ActionRequest{Action: coreapi.ActionStop, Target: "shop", TargetKind: "mate"})
	if err := e.locks.take(key, shown, coreapi.ActionStop); err != nil {
		t.Fatal(err)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if starts, _ := l.counts(); starts != 0 {
		t.Fatalf("started %d hosts while an action held the Mate", starts)
	}
	e.locks.release(key)
	l.waitPID(t, "4301")
}

// A host that keeps dying is started again three times within ten
// minutes; the fourth death is the captain's: one notification, one
// notice on the Mate's row, the status line and core status, and nothing
// started. A start of the captain's begins a new count.
func TestHostWatchGivesUpAfterThreeRestartsInTenMinutes(t *testing.T) {
	w := shopWorkspace(t)
	recordHost(t, w, "shop", 100)
	f, h := newFakeHosts(t, w)
	f.keepDying = true
	ctx := context.Background()

	for range 12 {
		h.check(ctx)
		f.clock = f.clock.Add(HostWatchEvery)
	}
	if len(f.restarts) != 3 {
		t.Fatalf("restarts = %v, want 3", f.restarts)
	}
	const want = "Mate shop: host died 4 times in 10m; not restarting"
	if len(f.told) != 1 || !strings.HasPrefix(f.told[0], want) {
		t.Fatalf("told = %q, want one notification %q", f.told, want)
	}
	if !strings.Contains(f.log.String(), want) {
		t.Fatalf("log = %q, want %q", f.log.String(), want)
	}
	if f.changed == 0 {
		t.Fatal("the cached snapshot was never dropped")
	}
	// The snapshot every UI reads says it, beside the observer's word
	// about Herdr, which replaces the status line before it.
	e := New(Options{Workspace: w, Harnesses: testHarnesses, Watcher: &fakeWatcher{notice: "herdr is down", at: time.Now()}})
	defer e.Stop()
	e.hosts = h
	snap, err := e.FreshSnapshot(ctx)
	if err != nil {
		t.Fatalf("FreshSnapshot: %v", err)
	}
	if e := snap.Projects[0].Mate.Error; !e.IsKnown() || string(e.Value) != want {
		t.Fatalf("Mate error = %+v, want %q", e, want)
	}
	if snap.Runtime.Notice != "herdr is down; "+want {
		t.Fatalf("status line = %q", snap.Runtime.Notice)
	}
	if got := e.Status().Notices; len(got) != 1 || got[0] != want {
		t.Fatalf("core status notices = %q", got)
	}

	// The captain starts it again by hand: a new host, a new count.
	recordHost(t, w, "shop", 900)
	h.check(ctx)
	if len(h.read()) != 0 {
		t.Fatalf("notices = %v after the captain's start", h.read())
	}
	h.check(ctx)
	if len(f.restarts) != 4 || f.restarts[3] != "shop 900" {
		t.Fatalf("restarts = %v, want the captain's host started again", f.restarts)
	}
	if len(f.told) != 1 {
		t.Fatalf("told = %q, want no second notification", f.told)
	}
}

// A host the loop did not start - the captain's, a UI's restart - begins a
// new count even before the loop has given up.
func TestHostWatchCountsAgainAfterSomeoneElseStartsTheMate(t *testing.T) {
	w := shopWorkspace(t)
	recordHost(t, w, "shop", 100)
	f, h := newFakeHosts(t, w)
	f.keepDying = true
	ctx := context.Background()
	for range 4 {
		h.check(ctx)
	}
	if len(f.restarts) != 2 {
		t.Fatalf("restarts = %v, want 2", f.restarts)
	}
	recordHost(t, w, "shop", 900)
	for range 6 {
		h.check(ctx)
	}
	if len(f.restarts) != 5 || len(f.told) != 0 {
		t.Fatalf("restarts = %v, told = %q; want three more after the captain's start", f.restarts, f.told)
	}
	h.check(ctx)
	h.check(ctx)
	if len(f.restarts) != 5 || len(f.told) != 1 {
		t.Fatalf("restarts = %v, told = %q; want the give-up", f.restarts, f.told)
	}
}

// Only the restarts of the last ten minutes count: a host that dies now and
// then is started again every time.
func TestHostWatchCountsOnlyTheLastTenMinutes(t *testing.T) {
	w := shopWorkspace(t)
	recordHost(t, w, "shop", 100)
	f, h := newFakeHosts(t, w)
	f.keepDying = true
	for range 10 {
		h.check(context.Background())
		h.check(context.Background())
		f.clock = f.clock.Add(4 * time.Minute)
	}
	if len(f.restarts) != 10 || len(f.told) != 0 {
		t.Fatalf("restarts = %d, told = %q; want every death restarted", len(f.restarts), f.told)
	}
}

// A restart that fails counts toward the limit and is said on the Mate's
// row until the host comes back.
func TestHostWatchSaysWhyARestartFailed(t *testing.T) {
	w := shopWorkspace(t)
	recordHost(t, w, "shop", 100)
	f, h := newFakeHosts(t, w)
	f.fail = errors.New("the headless Mate's host did not answer on its socket within 1s\nlog tail")
	for range 8 {
		h.check(context.Background())
	}
	if len(f.restarts) != 3 {
		t.Fatalf("restarts = %v, want 3 failed attempts and then none", f.restarts)
	}
	if got := hostNotices(h.read()); len(got) != 1 || !strings.HasPrefix(got[0], "Mate shop: host died 4 times in 10m") {
		t.Fatalf("notices = %q", got)
	}
	if !strings.Contains(f.log.String(), "Mate shop: host pid 100 died; restart failed: the headless Mate's host did not answer on its socket within 1s\n") {
		t.Fatalf("log = %q", f.log.String())
	}
}

// liveHosts is a workspace in a short directory (a unix socket path is
// short), the spawn deps a headless Mate starts through, and hosts played
// in this process: start serves a host on the Mate's socket under pid.
type liveHosts struct {
	t    *testing.T
	w    *store.Workspace
	deps spawn.Deps

	mu      sync.Mutex
	starts  int
	signals int
	stops   []context.CancelFunc
	wg      sync.WaitGroup
}

func newLiveHosts(t *testing.T) *liveHosts {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(claude.ClaudeConfigDirEnv, "")
	root, err := os.MkdirTemp("/tmp", "hw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	w, err := store.Init(filepath.Join(root, "ws"), store.Defaults{MateHarness: "claude", CrewHarness: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddProject("shop", store.ProjectConfig{}); err != nil {
		t.Fatal(err)
	}
	fake := runtime.NewFake()
	l := &liveHosts{t: t, w: w}
	l.deps = spawn.Deps{
		Harnesses:            catalog.Default(),
		Runtime:              fake,
		Names:                fake.Names,
		ConfigHome:           t.TempDir(),
		Binary:               filepath.Join(t.TempDir(), "mate"),
		ReadinessTimeout:     2 * time.Second,
		StartupPromptTimeout: 50 * time.Millisecond,
		StartTimeout:         10 * time.Second,
		NewSessionID:         func() string { return "99999999-8888-7777-6666-555555555555" },
	}
	l.deps.Hosts = spawn.HostFuncs{
		Start: func(spec spawn.HostSpec) (int, <-chan struct{}, error) {
			l.mu.Lock()
			l.starts++
			pid := 4300 + l.starts
			l.mu.Unlock()
			launch, err := matehost.ReadLaunch(spec.Dir)
			if err != nil {
				return 0, nil, err
			}
			l.serve(launch, pid)
			return pid, nil, nil
		},
		Signal: func(int, syscall.Signal) error {
			l.mu.Lock()
			l.signals++
			l.mu.Unlock()
			return nil
		},
	}
	t.Cleanup(l.killAll)
	return l
}

// serve runs a host on shop's socket as pid until kill.
func (l *liveHosts) serve(launch matehost.Launch, pid int) {
	ctx, cancel := context.WithCancel(context.Background())
	free := func() (func(), bool, error) { return func() {}, true, nil }
	host := matehost.New(&matehost.Runner{Launch: launch, Turns: claude.Claude{}, Lock: free},
		matehost.Options{PID: pid, History: matehost.HistoryPath(l.w.MateDir("shop"))})
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		_ = host.Serve(ctx, matehost.SocketPath(l.w.MateDir("shop")), nil)
	}()
	l.mu.Lock()
	l.stops = append(l.stops, cancel)
	l.mu.Unlock()
	client := matehost.Dial(l.w.MateDir("shop"))
	for deadline := time.Now().Add(5 * time.Second); ; {
		if st, err := client.State(context.Background()); err == nil && st.PID == pid {
			return
		}
		if time.Now().After(deadline) {
			l.t.Error("the fake host did not come up")
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// killAll ends every host, as a kill of their processes would.
func (l *liveHosts) killAll() {
	l.mu.Lock()
	stops := l.stops
	l.stops = nil
	l.mu.Unlock()
	for _, stop := range stops {
		stop()
	}
	l.wg.Wait()
}

func (l *liveHosts) counts() (starts, signals int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.starts, l.signals
}

// waitPID waits for mate.meta to record the host pid, on the Mate's own
// session.
func (l *liveHosts) waitPID(t *testing.T, pid string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; {
		meta, _ := l.w.ReadMateMeta("shop")
		if meta[spawn.MetaPID] == pid {
			if meta[spawn.MetaSessionID] != "11111111-2222-3333-4444-555555555555" {
				t.Fatalf("meta = %v, want the Mate resumed on its session", meta)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("meta = %v; host %s was never recorded", meta, pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (l *liveHosts) engine(t *testing.T) *Engine {
	e := New(Options{Workspace: l.w, Spawn: l.deps, Harnesses: testHarnesses, Stderr: &bytes.Buffer{}})
	e.hosts.every = 20 * time.Millisecond
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return e
}

// The core starts a killed host again, on the Mate's own session, through
// recovery and the spawn seam a start uses (CON-226).
func TestTheCoreRestartsAKilledHostOnItsSession(t *testing.T) {
	l := newLiveHosts(t)
	recordHost(t, l.w, "shop", 4242)
	launch := matehost.Launch{Dir: l.w.MateDir("shop"), SessionID: "11111111-2222-3333-4444-555555555555"}
	l.serve(launch, 4242)

	e := l.engine(t)
	defer e.Stop()
	time.Sleep(150 * time.Millisecond)
	if starts, _ := l.counts(); starts != 0 {
		t.Fatalf("started %d hosts beside one that answers", starts)
	}

	l.killAll()
	l.waitPID(t, "4301")
	client := matehost.Dial(l.w.MateDir("shop"))
	if st, err := client.State(context.Background()); err != nil || st.PID != 4301 {
		t.Fatalf("State = %+v, %v; want the new host answering", st, err)
	}
	if starts, _ := l.counts(); starts != 1 {
		t.Fatalf("starts = %d, want 1", starts)
	}
}

// slowTurn plays `claude -p`: it waits for a file named release beside it,
// then answers like stream-json.
const slowTurn = `#!/bin/sh
cat >/dev/null
while [ ! -f "$(dirname "$0")/release" ]; do sleep 0.02; done
echo '{"type":"result","subtype":"success","result":"turn done","session_id":"11111111-2222-3333-4444-555555555555","is_error":false}'
`

// Stopping the core sends a host nothing - no stop, no signal - so a turn
// in the host's process runs on across a restart of the core and finishes;
// the next core finds the host alive on its socket and starts nothing
// beside it.
func TestRestartingTheCoreMidTurnLetsTheTurnFinish(t *testing.T) {
	l := newLiveHosts(t)
	recordHost(t, l.w, "shop", 4242)
	bin := t.TempDir()
	program := filepath.Join(bin, "claude")
	if err := os.WriteFile(program, []byte(slowTurn), 0o755); err != nil {
		t.Fatal(err)
	}
	l.serve(matehost.Launch{Dir: l.w.MateDir("shop"), Program: program, Harness: "claude", SessionID: "11111111-2222-3333-4444-555555555555"}, 4242)
	client := matehost.Dial(l.w.MateDir("shop"))
	if err := client.Deliver(context.Background(), "a line for the Mate"); err != nil {
		t.Fatalf("Deliver: %v", err)
	}

	first := l.engine(t)
	time.Sleep(100 * time.Millisecond)
	first.Stop()
	if st, err := client.State(context.Background()); err != nil || st.PID != 4242 || !st.Busy || st.Stopping {
		t.Fatalf("after the core stopped: State = %+v, %v; want host 4242 still in its turn", st, err)
	}
	second := l.engine(t)
	defer second.Stop()
	time.Sleep(100 * time.Millisecond)

	if err := os.WriteFile(filepath.Join(bin, "release"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); ; {
		items, err := client.History(context.Background(), 5)
		if err == nil && len(items) > 0 && items[len(items)-1].Done {
			if last := items[len(items)-1]; last.Reply != "turn done" || last.Error != "" {
				t.Fatalf("the turn ended %+v, want its reply", last)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("History = %+v, %v; the turn never finished", items, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if starts, signals := l.counts(); starts != 0 || signals != 0 {
		t.Fatalf("starts = %d, signals = %d; want the host left alone", starts, signals)
	}
}

// The give-up's notification goes through the engine's notifier, with a
// link to the captain's desk, unless the workspace turned notifications off.
func TestTheGiveUpIsANotification(t *testing.T) {
	w := shopWorkspace(t)
	sent := &recordingSender{}
	e := New(Options{Workspace: w, Notifier: sent})
	e.tellHostGivenUp(context.Background(), "shop", "Mate shop: host died 4 times in 10m; not restarting")
	if len(sent.msgs) != 1 || sent.msgs[0].Title != "Mate shop" || !strings.Contains(sent.msgs[0].Body, "not restarting") {
		t.Fatalf("sent = %+v", sent.msgs)
	}
	if err := writeFile(w.EnvFile(), "MATE_NOTIFICATIONS=off\n"); err != nil {
		t.Fatal(err)
	}
	e.tellHostGivenUp(context.Background(), "shop", "again")
	if len(sent.msgs) != 1 {
		t.Fatalf("sent = %+v with notifications off", sent.msgs)
	}
}

type recordingSender struct{ msgs []notifications.Message }

func (r *recordingSender) Send(_ context.Context, m notifications.Message) error {
	r.msgs = append(r.msgs, m)
	return nil
}
