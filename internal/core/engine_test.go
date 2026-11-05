package core

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nguyenngocanh94/mate/internal/autopilot"
	"github.com/nguyenngocanh94/mate/internal/recovery"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/timeline"
	"github.com/nguyenngocanh94/mate/internal/watch"
)

// recorder is the one log every fake part writes to, so a test reads the
// order the engine drove them in.
type recorder struct {
	mu  sync.Mutex
	log []string
}

func (r *recorder) add(entry string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.log = append(r.log, entry)
}

func (r *recorder) entries() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.log)
}

type ctxKey struct{}

// fakeLoop is a loop that records its Start and Stop. A startErr makes its
// Start fail; ctxValue is what the context it was started with carries.
// cancelledAtStop is whether that context had been cancelled by the time
// Stop was called.
type fakeLoop struct {
	name            string
	rec             *recorder
	startErr        error
	ctxValue        any
	ctx             context.Context
	cancelledAtStop bool
}

func (l *fakeLoop) Start(ctx context.Context) error {
	l.ctx = ctx
	l.ctxValue = ctx.Value(ctxKey{})
	l.rec.add("start " + l.name)
	return l.startErr
}

func (l *fakeLoop) Stop() {
	l.cancelledAtStop = l.ctx != nil && l.ctx.Err() != nil
	l.rec.add("stop " + l.name)
}

type fakeWatcher struct {
	fakeLoop
	health map[watch.CrewRef]watch.Health
	notice string
	at     time.Time
}

func (w *fakeWatcher) Snapshot() map[watch.CrewRef]watch.Health { return w.health }
func (w *fakeWatcher) RuntimeNotice() (string, time.Time)       { return w.notice, w.at }

type fakePilot struct {
	fakeLoop
	status map[string]autopilot.Status
}

func (p *fakePilot) Snapshot() map[string]autopilot.Status { return p.status }

type fakeTimeline struct{ rec *recorder }

func (d fakeTimeline) Close() error {
	d.rec.add("close timeline")
	return nil
}

// fakeParts is one of each part over one recorder. Recover reports that it
// runs, then holds until it is cancelled, the way recovery.Run stops between
// two agents.
func fakeParts(rec *recorder) (Options, *fakeWatcher, *fakeLoop, *fakePilot) {
	w := &fakeWatcher{fakeLoop: fakeLoop{name: "watcher", rec: rec}}
	s := &fakeLoop{name: "sender", rec: rec}
	p := &fakePilot{fakeLoop: fakeLoop{name: "pilot", rec: rec}}
	return Options{
		Watcher:  w,
		Timeline: fakeTimeline{rec},
		Sender:   s,
		Pilot:    p,
		Recover: func(ctx context.Context, _ *store.Workspace, _ func(recovery.Progress)) recovery.Result {
			rec.add("recover")
			<-ctx.Done()
			rec.add("recover cancelled")
			return recovery.Result{Err: ctx.Err()}
		},
	}, w, s, p
}

// waitFor polls cond, for what a part does on a goroutine of its own.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// logIs checks a recorder's log: first, in this order, then every one of
// anyOrder, in whichever order the parts stopping together wrote them, then
// last, in this order.
func logIs(t *testing.T, got, first, anyOrder, last []string) {
	t.Helper()
	ok := len(got) == len(first)+len(anyOrder)+len(last)
	if ok {
		middle := slices.Clone(got[len(first) : len(first)+len(anyOrder)])
		want := slices.Clone(anyOrder)
		slices.Sort(middle)
		slices.Sort(want)
		ok = slices.Equal(got[:len(first)], first) && slices.Equal(middle, want) &&
			slices.Equal(got[len(first)+len(anyOrder):], last)
	}
	if !ok {
		t.Fatalf("log %q, want %q, then %q in any order, then %q", got, first, anyOrder, last)
	}
}

// The order runConsole had before CON-207: the observer, then the outbox
// sender and the auto daemon, then recovery. Stop cancels them all before
// it waits for any (CON-210), stops them together, and closes the timeline
// database last, once nothing that writes it runs.
func TestEngineStartsInOrderAndStopsEverythingAtOnce(t *testing.T) {
	rec := &recorder{}
	opts, w, s, p := fakeParts(rec)
	e := New(opts)

	ctx := context.WithValue(context.Background(), ctxKey{}, "caller")
	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, "recovery to run", func() bool { return slices.Contains(rec.entries(), "recover") })
	if got, want := rec.entries(), []string{"start watcher", "start sender", "start pilot", "recover"}; !slices.Equal(got, want) {
		t.Fatalf("after Start: %q, want %q", got, want)
	}
	for _, l := range []*fakeLoop{&w.fakeLoop, s, &p.fakeLoop} {
		if l.ctxValue != "caller" {
			t.Errorf("%s started under a context without the caller's value: %v", l.name, l.ctxValue)
		}
	}

	e.Stop()
	logIs(t, rec.entries(),
		[]string{"start watcher", "start sender", "start pilot", "recover"},
		[]string{"recover cancelled", "stop pilot", "stop sender", "stop watcher"},
		[]string{"close timeline"})
	for _, l := range []*fakeLoop{&w.fakeLoop, s, &p.fakeLoop} {
		if !l.cancelledAtStop {
			t.Errorf("%s was asked to stop before its context was cancelled", l.name)
		}
	}
}

// Nothing fails to start today; core.lock will (CON-211). A part that does
// not start is not stopped, every part before it is, together, and the
// timeline is closed: the engine holds nothing once Start has failed.
func TestEngineStartFailureStopsWhatStartedAndReturnsTheError(t *testing.T) {
	errBoom := errors.New("boom")
	for _, tc := range []struct {
		failing          string
		started, stopped []string
	}{
		{"watcher", []string{"start watcher"}, nil},
		{"sender", []string{"start watcher", "start sender"}, []string{"stop watcher"}},
		{"pilot", []string{"start watcher", "start sender", "start pilot"}, []string{"stop sender", "stop watcher"}},
	} {
		t.Run(tc.failing, func(t *testing.T) {
			rec := &recorder{}
			opts, w, s, p := fakeParts(rec)
			var ran atomic.Bool
			opts.Recover = func(context.Context, *store.Workspace, func(recovery.Progress)) recovery.Result {
				ran.Store(true)
				return recovery.Result{}
			}
			loops := map[string]*fakeLoop{"watcher": &w.fakeLoop, "sender": s, "pilot": &p.fakeLoop}
			loops[tc.failing].startErr = errBoom
			e := New(opts)

			if err := e.Start(context.Background()); err != errBoom {
				t.Fatalf("Start = %v, want the part's own error", err)
			}
			logIs(t, rec.entries(), tc.started, tc.stopped, []string{"close timeline"})
			e.Stop()
			logIs(t, rec.entries(), tc.started, tc.stopped, []string{"close timeline"})
			if ran.Load() {
				t.Fatal("recovery ran although Start failed")
			}
		})
	}
}

// A cancelled recovery gets the engine's 45 seconds to undo an agent it had
// half started, longer than spawn's own 30s cleanup. Past that, Stop names
// it and returns, the rest stopped, and leaves the timeline to the process
// exit: a recovery still running may yet write it.
func TestEngineStopWaitsForRecoveryAtMostItsBound(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{}
		opts, _, _, _ := fakeParts(rec)
		release := make(chan struct{})
		opts.Recover = func(context.Context, *store.Workspace, func(recovery.Progress)) recovery.Result {
			<-release // deaf to its context, like a restart stuck in Herdr
			rec.add("recover returned")
			return recovery.Result{}
		}
		var stderr strings.Builder
		opts.Stderr = &stderr
		e := New(opts)
		if err := e.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}

		start := time.Now()
		e.Stop()
		if waited := time.Since(start); waited != 45*time.Second {
			t.Errorf("Stop waited %s for recovery, want 45s", waited)
		}
		logIs(t, rec.entries(),
			[]string{"start watcher", "start sender", "start pilot"},
			[]string{"stop pilot", "stop sender", "stop watcher"}, nil)
		if want := "mate core: recovery had not stopped 45s after the stop began; mate.db is left open for the process exit to release\n"; stderr.String() != want {
			t.Errorf("stderr %q, want %q", stderr.String(), want)
		}
		close(release)
		synctest.Wait()
		if !e.Recovery().Done {
			t.Fatal("recovery that returned after Stop is not reported done")
		}
	})
}

// What recovery has said so far is readable while it runs, and what it did
// once it returns: the console's status line is drawn from it.
func TestEngineRecoveryReportsProgressThenItsResult(t *testing.T) {
	ws := &store.Workspace{}
	step, release := make(chan struct{}), make(chan struct{})
	var ranOver *store.Workspace
	e := New(Options{
		Workspace: ws,
		Recover: func(_ context.Context, w *store.Workspace, progress func(recovery.Progress)) recovery.Result {
			ranOver = w
			progress(recovery.Progress{Total: 2})
			progress(recovery.Progress{Done: 1, Total: 2})
			close(step)
			<-release
			return recovery.Result{Items: []recovery.Item{{Project: "shop", Resumed: true}}}
		},
	})
	doneAt := time.Date(2026, 11, 5, 9, 30, 0, 0, time.UTC)
	e.recovery.now = func() time.Time { return doneAt }

	if st := e.Recovery(); st.Reported || st.Done {
		t.Fatalf("before Start: %+v, want nothing reported", st)
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer e.Stop()
	<-step
	if ranOver != ws {
		t.Errorf("recovery ran over %p, want the engine's workspace %p", ranOver, ws)
	}
	st := e.Recovery()
	if !st.Reported || st.Progress != (recovery.Progress{Done: 1, Total: 2}) || st.Done {
		t.Fatalf("while running: %+v, want the latest progress and not done", st)
	}

	close(release)
	waitFor(t, "recovery to finish", func() bool { return e.Recovery().Done })
	st = e.Recovery()
	if len(st.Result.Items) != 1 || st.Result.Items[0].Project != "shop" || !st.DoneAt.Equal(doneAt) {
		t.Fatalf("after: %+v, want the result at %s", st, doneAt)
	}
}

// The snapshot reads the parts the engine runs through it: the crews'
// health and the runtime notice, the timeline's ingest, the daemon's state.
func TestEngineReadsThePartsItRuns(t *testing.T) {
	rec := &recorder{}
	opts, w, _, p := fakeParts(rec)
	ref := watch.CrewRef{Project: "shop", Crew: "k3"}
	at := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	w.health = map[watch.CrewRef]watch.Health{ref: {AgentPresent: true}}
	w.notice, w.at = "herdr is not running", at
	p.status = map[string]autopilot.Status{"shop": {Sends: 2}}
	ingest := timeline.New(nil, nil, timeline.Deps{})
	opts.Ingest = ingest
	e := New(opts)

	if h := e.CrewHealth().Snapshot(); !h[ref].AgentPresent {
		t.Errorf("crew health %+v, want the watcher's reading", h)
	}
	if notice, when := e.CrewHealth().RuntimeNotice(); notice != "herdr is not running" || !when.Equal(at) {
		t.Errorf("runtime notice %q at %s, want the watcher's", notice, when)
	}
	if got := e.AutoStatus().Snapshot()["shop"].Sends; got != 2 {
		t.Errorf("daemon sends = %d, want 2", got)
	}
	if e.Ingest() != ingest {
		t.Error("Ingest is not the ingest the engine was given")
	}
}

// Another process holds `.mate/mate.db` (internal/db.ErrLocked): there is no
// timeline to close and no ingest to read, and the observer runs on.
func TestEngineRunsWithoutATimeline(t *testing.T) {
	rec := &recorder{}
	opts, _, _, _ := fakeParts(rec)
	opts.Timeline, opts.Ingest = nil, nil
	e := New(opts)
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if e.Ingest() != nil {
		t.Error("an engine with no timeline reads an ingest")
	}
	e.Stop()
	if got := rec.entries(); slices.Contains(got, "close timeline") || !slices.Contains(got, "stop watcher") {
		t.Fatalf("log %q, want the watcher stopped and nothing closed", got)
	}
}

// Start runs once and Stop stops once: a second Stop waits for nothing and
// closes nothing again, and an engine stopped before it started still
// releases the timeline it was given and will not start afterwards.
func TestEngineStartsOnceAndStopsOnce(t *testing.T) {
	rec := &recorder{}
	opts, _, _, _ := fakeParts(rec)
	opts.Recover = nil
	e := New(opts)
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := e.Start(context.Background()); err == nil {
		t.Error("a second Start succeeded")
	}
	e.Stop()
	e.Stop()
	logIs(t, rec.entries(),
		[]string{"start watcher", "start sender", "start pilot"},
		[]string{"stop pilot", "stop sender", "stop watcher"},
		[]string{"close timeline"})
	if err := e.Start(context.Background()); err == nil {
		t.Error("Start after Stop succeeded")
	}

	rec = &recorder{}
	opts, _, _, _ = fakeParts(rec)
	never := New(opts)
	never.Stop()
	if got := rec.entries(); !slices.Equal(got, []string{"close timeline"}) {
		t.Fatalf("Stop before Start: %q, want only the timeline closed", got)
	}
	if err := never.Start(context.Background()); err == nil {
		t.Error("Start after Stop succeeded")
	}
}
