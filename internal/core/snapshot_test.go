package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nguyenngocanh94/mate/internal/autopilot"
	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/dashboard"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/recovery"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/timeline"
	"github.com/nguyenngocanh94/mate/internal/watch"
)

// computations is a snapshot computation a test drives: the n-th call (from
// 1) returns a snapshot whose WorkspaceID is n, once its gate is opened, and
// every call's context is kept.
type computations struct {
	mu    sync.Mutex
	calls int
	ctxs  []context.Context
	gates map[int]chan struct{}
	errs  map[int]error
}

func newComputations() *computations {
	return &computations{gates: map[int]chan struct{}{}, errs: map[int]error{}}
}

// gate makes the n-th call wait until open(n).
func (c *computations) gate(n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gates[n] = make(chan struct{})
}

func (c *computations) open(n int) { close(c.gates[n]) }

func (c *computations) fail(n int, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.errs[n] = err
}

func (c *computations) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *computations) ctx(n int) context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ctxs[n-1]
}

func (c *computations) compute(ctx context.Context) (query.Snapshot, error) {
	c.mu.Lock()
	c.calls++
	n := c.calls
	c.ctxs = append(c.ctxs, ctx)
	gate, err := c.gates[n], c.errs[n]
	c.mu.Unlock()
	if gate != nil {
		<-gate
	}
	if err != nil {
		return query.Snapshot{}, err
	}
	return query.Snapshot{WorkspaceID: fmt.Sprint(n)}, nil
}

// read is one Snapshot call on a goroutine of its own.
type read struct {
	done chan struct{}
	snap query.Snapshot
	err  error
}

func readAsync(e *Engine, ctx context.Context) *read {
	r := &read{done: make(chan struct{})}
	go func() {
		defer close(r.done)
		r.snap, r.err = e.Snapshot(ctx)
	}()
	return r
}

func (r *read) returned() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}

// cacheEngine is an engine with no parts whose computation c drives.
func cacheEngine(c *computations) *Engine {
	e := New(Options{})
	e.snaps.compute = c.compute
	return e
}

// Readers that come while a snapshot is computed wait for that one. The
// computation runs under the engine's context: the first reader giving up
// stops only its own wait, and the others get the snapshot. It is then
// handed out for two seconds, and the next read after them computes anew.
func TestSnapshotReadersShareOneComputationAndOutliveTheFirstOne(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newComputations()
		c.gate(1)
		e := cacheEngine(c)
		defer e.Stop()

		first, leave := context.WithCancel(context.Background())
		a := readAsync(e, first)
		synctest.Wait()
		b, d := readAsync(e, context.Background()), readAsync(e, context.Background())
		synctest.Wait()
		if c.count() != 1 {
			t.Fatalf("three readers started %d computations, want 1", c.count())
		}

		leave()
		synctest.Wait()
		if !a.returned() || !errors.Is(a.err, context.Canceled) {
			t.Fatalf("the reader that left: returned %v, err %v; want context.Canceled", a.returned(), a.err)
		}
		if err := c.ctx(1).Err(); err != nil {
			t.Fatalf("the computation's context ended with the first reader's: %v", err)
		}
		if b.returned() || d.returned() {
			t.Fatal("the other readers stopped waiting with the first")
		}

		c.open(1)
		synctest.Wait()
		for _, r := range []*read{b, d} {
			if r.err != nil || r.snap.WorkspaceID != "1" {
				t.Fatalf("a reader that stayed got %q, %v; want the computed snapshot", r.snap.WorkspaceID, r.err)
			}
		}

		time.Sleep(snapshotMaxAge - time.Millisecond)
		if snap, err := e.Snapshot(context.Background()); err != nil || snap.WorkspaceID != "1" || c.count() != 1 {
			t.Fatalf("a read within two seconds got %q, %v after %d computations; want the kept one", snap.WorkspaceID, err, c.count())
		}
		time.Sleep(time.Millisecond)
		if snap, err := e.Snapshot(context.Background()); err != nil || snap.WorkspaceID != "2" {
			t.Fatalf("a read two seconds on got %q, %v; want a new computation", snap.WorkspaceID, err)
		}
	})
}

// An Invalidate while a snapshot is computed: the computation still answers
// the readers waiting on it, but is not kept, and a reader after the
// Invalidate waits for a computation that started after it.
func TestInvalidateWhileComputingKeepsNothingOfThatComputation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newComputations()
		c.gate(1)
		c.gate(2)
		e := cacheEngine(c)
		defer e.Stop()

		before := readAsync(e, context.Background())
		synctest.Wait()
		e.Invalidate()
		after := readAsync(e, context.Background())
		synctest.Wait()
		if c.count() != 2 {
			t.Fatalf("a read after Invalidate joined the older computation: %d computations, want 2", c.count())
		}

		c.open(1)
		synctest.Wait()
		if before.err != nil || before.snap.WorkspaceID != "1" {
			t.Fatalf("the reader before Invalidate got %q, %v; want the computation it waited on", before.snap.WorkspaceID, before.err)
		}
		if after.returned() {
			t.Fatalf("the reader after Invalidate got %q from the older computation", after.snap.WorkspaceID)
		}
		// Nothing of the first computation was kept: the next read waits for
		// the second.
		next := readAsync(e, context.Background())
		synctest.Wait()
		if next.returned() || c.count() != 2 {
			t.Fatalf("a read got %q from the computation Invalidate dropped (%d computations)", next.snap.WorkspaceID, c.count())
		}

		c.open(2)
		synctest.Wait()
		for _, r := range []*read{after, next} {
			if r.err != nil || r.snap.WorkspaceID != "2" {
				t.Fatalf("got %q, %v; want the computation after Invalidate", r.snap.WorkspaceID, r.err)
			}
		}
		if snap, _ := e.Snapshot(context.Background()); snap.WorkspaceID != "2" || c.count() != 2 {
			t.Fatalf("read %q after %d computations; want the second one kept", snap.WorkspaceID, c.count())
		}

		// And an Invalidate with nothing running drops what is kept.
		e.Invalidate()
		if snap, _ := e.Snapshot(context.Background()); snap.WorkspaceID != "3" {
			t.Fatalf("read %q after Invalidate, want a new computation", snap.WorkspaceID)
		}
	})
}

// A computation that fails fails every reader waiting on it and is not
// kept: the next read computes again.
func TestAFailedSnapshotIsNotKept(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := newComputations()
		c.gate(1)
		broken := errors.New("store: workspace.yaml: yaml: did not find expected key")
		c.fail(1, broken)
		e := cacheEngine(c)
		defer e.Stop()

		a, b := readAsync(e, context.Background()), readAsync(e, context.Background())
		synctest.Wait()
		c.open(1)
		synctest.Wait()
		for _, r := range []*read{a, b} {
			if !errors.Is(r.err, broken) {
				t.Fatalf("a waiting reader got %v, want the computation's error", r.err)
			}
		}
		if snap, err := e.Snapshot(context.Background()); err != nil || snap.WorkspaceID != "2" || c.count() != 2 {
			t.Fatalf("the read after a failure got %q, %v after %d computations; want a new one", snap.WorkspaceID, err, c.count())
		}
	})
}

// FreshSnapshot computes every time, whatever is kept, and keeps nothing.
func TestFreshSnapshotAlwaysComputes(t *testing.T) {
	c := newComputations()
	e := cacheEngine(c)
	defer e.Stop()
	ctx := context.Background()
	if snap, _ := e.Snapshot(ctx); snap.WorkspaceID != "1" {
		t.Fatalf("first read %q", snap.WorkspaceID)
	}
	for want := 2; want <= 3; want++ {
		if snap, err := e.FreshSnapshot(ctx); err != nil || snap.WorkspaceID != fmt.Sprint(want) {
			t.Fatalf("FreshSnapshot = %q, %v; want computation %d", snap.WorkspaceID, err, want)
		}
	}
	if snap, _ := e.Snapshot(ctx); snap.WorkspaceID != "1" || c.count() != 3 {
		t.Fatalf("read %q after %d computations, want the kept first one", snap.WorkspaceID, c.count())
	}
}

// A computation gets ten seconds: one that hangs fails its readers then,
// under the engine's context and not theirs.
func TestASnapshotComputationIsBoundedByTenSeconds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := New(Options{})
		defer e.Stop()
		e.snaps.compute = func(ctx context.Context) (query.Snapshot, error) {
			<-ctx.Done()
			return query.Snapshot{}, ctx.Err()
		}
		start := time.Now()
		if _, err := e.Snapshot(context.Background()); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v, want the deadline", err)
		}
		if waited := time.Since(start); waited != snapshotLimit {
			t.Fatalf("waited %s, want %s", waited, snapshotLimit)
		}
	})
}

// Stop ends the computation in flight - the engine's context is its - waits
// for it, and no read computes afterwards.
func TestStopEndsTheSnapshotComputation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := New(Options{})
		e.snaps.compute = func(ctx context.Context) (query.Snapshot, error) {
			<-ctx.Done()
			return query.Snapshot{}, ctx.Err()
		}
		if err := e.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		r := readAsync(e, context.Background())
		synctest.Wait()
		e.Stop()
		synctest.Wait()
		if !r.returned() || !errors.Is(r.err, context.Canceled) {
			t.Fatalf("the reader during Stop: returned %v, err %v; want context.Canceled", r.returned(), r.err)
		}
		if _, err := e.Snapshot(context.Background()); !errors.Is(err, errStopped) {
			t.Fatalf("a read after Stop: %v, want errStopped", err)
		}
	})
}

// Cancelling the context Start was given ends the engine's context too, so
// a computation does not outlive the caller that ran the engine.
func TestTheStartContextEndsTheSnapshotContext(t *testing.T) {
	e := New(Options{})
	defer e.Stop()
	ctx, cancel := context.WithCancel(context.Background())
	if err := e.Start(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	waitFor(t, "the engine's context to end", func() bool { return e.ctx.Err() != nil })
}

// The console and Mate Office's two GET routes that read a snapshot
// (`/api/projects/{p}/actions`, `/api/actions`), within two seconds: one
// computation between them. A press (POST /api/actions) is checked against
// a fresh one every time, and its action drops what is kept.
func TestTheConsoleAndBothGETRoutesShareOneSnapshot(t *testing.T) {
	c := newComputations()
	var mu sync.Mutex
	var acted []coreapi.ActionRequest
	act := func(_ context.Context, req coreapi.ActionRequest) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		acted = append(acted, req)
		return "mode changed", nil
	}
	// shop's Mate offers its mode, which is what a press asks for.
	shop := func(ctx context.Context) (query.Snapshot, error) {
		snap, err := c.compute(ctx)
		snap.Projects = []query.ProjectNode{{ProjectID: "shop", Name: "shop"}}
		return snap, err
	}
	e := dashboardEngine(t, DashboardOptions{Addr: freeAddr, Deps: dashboard.Deps{Act: act}}, shop)
	served, err := e.Dashboard()
	if err != nil {
		t.Fatal(err)
	}
	base := strings.Fields(served)[0]

	if _, err := e.Snapshot(context.Background()); err != nil {
		t.Fatalf("the console's read: %v", err)
	}
	for _, path := range []string{"/api/projects/shop/actions", "/api/actions"} {
		if status, body := get(t, base, path); status != http.StatusOK {
			t.Fatalf("GET %s = %d %s", path, status, body)
		}
	}
	if got := c.count(); got != 1 {
		t.Fatalf("the console and two GET routes computed %d snapshots, want 1", got)
	}

	resp, err := http.Post(strings.TrimRight(base, "/")+"/api/actions", "application/json", strings.NewReader(`{"action":"mode","project":"shop"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST mode = %d", resp.StatusCode)
	}
	mu.Lock()
	if len(acted) != 1 {
		t.Fatalf("acted %+v, want the one press", acted)
	}
	mu.Unlock()
	if got := c.count(); got != 2 {
		t.Fatalf("the press computed %d snapshots in all, want 2: one fresh for its check", got)
	}
	if status, _ := get(t, base, "/api/actions"); status != http.StatusOK || c.count() != 3 {
		t.Fatalf("a GET after the press: %d, %d computations in all; want a new one", status, c.count())
	}
}

// On the same files, the engine's snapshot with the console's keys put in is
// what the console's loadFrom built before CON-208: query.LoadLive with the
// keys and Herdr's word, then recovery, the crews' health, the runtime
// notice, the stale timelines, the daemon's state and the tokens, in that
// order. The reference below is that function's body as it was.
func TestSnapshotWithTheConsolesKeysIsWhatTheConsoleLoadedBefore(t *testing.T) {
	w := shopWorkspace(t)
	if err := w.WriteMateMeta("shop", map[string]string{"harness": "claude", "agent": "mate-shop", "pane": "p1"}); err != nil {
		t.Fatal(err)
	}
	for _, crew := range []string{"k1", "k3"} {
		if err := w.WriteCrewMeta("shop", crew, map[string]string{"harness": "codex", "state": "spawned", "agent": "crew-" + crew}); err != nil {
			t.Fatal(err)
		}
	}
	handle := withTimelineDB(t, w)
	seedActorTurnAndPricing(t, handle, timeline.CrewActorID("shop", "k3"), "shop", "crew", "test-model", 1000, 0, 0, 300, 400_000)
	seedActorTurnAndPricing(t, handle, timeline.MateActorID("shop"), "shop", "mate", "test-model", 500, 0, 0, 50, 400_000)

	rec := &recorder{}
	watcher := &fakeWatcher{fakeLoop: fakeLoop{name: "watcher", rec: rec},
		health: map[watch.CrewRef]watch.Health{{Project: "shop", Crew: "k3"}: {AgentPresent: true, Composer: send.StateBusy}},
		notice: "herdr is not running", at: time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)}
	pilot := &fakePilot{fakeLoop: fakeLoop{name: "pilot", rec: rec},
		status: map[string]autopilot.Status{"shop": {Sends: 2, Notice: "not delivered"}}}
	rt := runtime.NewFake()
	deps := spawn.Deps{Runtime: rt, Names: rt.Names, ConfigHome: t.TempDir()}
	e := New(Options{
		Workspace: w, Watcher: watcher, Pilot: pilot, Ingest: timeline.New(nil, nil, timeline.Deps{}),
		Spawn: deps, Harnesses: testHarnesses,
		Recover: func(context.Context, *store.Workspace, func(recovery.Progress)) recovery.Result {
			return recovery.Result{Items: []recovery.Item{{Project: "shop", Crew: "k1", Err: errors.New("the worktree of crew k1 is gone")}}}
		},
	})
	now := time.Now()
	e.now = func() time.Time { return now }
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	waitFor(t, "recovery to finish", func() bool { return e.Recovery().Done })
	commands := []query.CommandBinding{{Key: "e", Label: "report", Scope: "crew"}}

	// cmd/mate's loadFrom, as it was: watcher, ingest and pilot were the
	// engine's, recovery.apply was withRecovery at the console's clock.
	ctx := context.Background()
	loadFrom := func(ws *store.Workspace) coreapi.LoadFunc {
		return func(loadCtx context.Context) (query.Snapshot, error) {
			bindings := commands
			snap, err := query.LoadLive(loadCtx, ws, testHarnesses, bindings, liveness(loadCtx, ws, deps))
			if err != nil {
				return snap, err
			}
			snap = withRecovery(snap, e.Recovery(), now)
			snap = withCrewHealth(snap, e.CrewHealth().Snapshot())
			snap = withRuntimeNotice(snap, e.CrewHealth())
			snap = withTimelineStale(snap, e.Ingest())
			snap = withAutoStatus(snap, e.AutoStatus().Snapshot())
			return withTokens(snap, ws), nil
		}
	}
	want, err := loadFrom(w)(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Commands) != 0 {
		t.Fatalf("the engine's snapshot carries commands %+v; they are the console's", got.Commands)
	}
	got.Commands = append([]query.CommandBinding(nil), commands...)
	got.AsOf = want.AsOf
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the engine's snapshot differs from the console's old one:\n got %+v\nwant %+v", got, want)
	}
	// And it is not a vacuous match: every part put something in.
	crews := got.Projects[0].Crews
	if !crews[1].Health.IsKnown() || !crews[0].Error.IsKnown() || got.Runtime.Notice == "" ||
		got.Projects[0].Daemon.Sends != 2 || !crews[1].Tokens.IsKnown() || !got.Projects[0].Mate.Tokens.IsKnown() || got.Recovery.Line == "" {
		t.Fatalf("a part put nothing in: %+v", got)
	}
}

// Nobody reads a snapshot, and the workspace the dashboard shares is still
// read back from workspace.yaml every two seconds: a project another
// process registered is in Projects() by then, and not before.
func TestTheSharedWorkspaceFollowsWorkspaceYAMLWithNoReader(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := shopWorkspace(t)
		e := New(Options{Workspace: w})
		e.snaps.compute = func(context.Context) (query.Snapshot, error) {
			t.Error("a snapshot was computed with nobody reading")
			return query.Snapshot{}, nil
		}
		if err := e.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		defer e.Stop()

		other, err := store.Open(w.Root())
		if err != nil {
			t.Fatal(err)
		}
		if err := other.AddProject("blog", store.ProjectConfig{}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(configEvery - time.Millisecond)
		synctest.Wait()
		if _, ok := w.Project("blog"); ok {
			t.Fatal("blog is in the shared workspace before the engine read the file")
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		if _, ok := w.Project("blog"); !ok {
			t.Fatalf("projects %+v two seconds after blog was added; want blog", w.Projects())
		}

		// A file that does not read keeps what was loaded before.
		if err := writeFile(w.WorkspaceFile(), "projects: [\n"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(configEvery)
		synctest.Wait()
		if _, ok := w.Project("blog"); !ok {
			t.Fatalf("projects %+v after workspace.yaml broke; want the configuration before", w.Projects())
		}
	})
}

// The same through the dashboard: a route that reads no snapshot and keeps
// no cache of its own (`/api/board`) lists a project added to
// workspace.yaml within two seconds, with no console and no reader.
// (`/api/workspace` keeps its answer until the timeline's next event, the
// dashboard's own cache.)
func TestTheDashboardListsANewProjectWithNoConsole(t *testing.T) {
	e := dashboardEngine(t, DashboardOptions{Addr: freeAddr})
	served, err := e.Dashboard()
	if err != nil {
		t.Fatal(err)
	}
	base := strings.Fields(served)[0]
	for _, path := range []string{"/api/board"} {
		if _, body := get(t, base, path); strings.Contains(body, `"blog"`) {
			t.Fatalf("GET %s lists blog before it was added: %s", path, body)
		}
	}
	other, err := store.Open(e.o.Workspace.Root())
	if err != nil {
		t.Fatal(err)
	}
	if err := other.AddProject("blog", store.ProjectConfig{}); err != nil {
		t.Fatal(err)
	}
	added := time.Now()
	// Two seconds, and some slack for a loaded machine running -race.
	deadline := added.Add(configEvery + 2*time.Second)
	for _, path := range []string{"/api/board"} {
		for {
			_, body := get(t, base, path)
			if strings.Contains(body, `"blog"`) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("GET %s does not list blog %s after it was added: %s", path, time.Since(added), body)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	if _, ok := e.o.Workspace.Project("blog"); !ok {
		t.Fatal("the server's workspace has no blog")
	}
}
