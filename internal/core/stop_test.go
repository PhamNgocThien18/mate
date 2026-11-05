package core

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nguyenngocanh94/mate/internal/autopilot"
	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/recovery"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/watch"
)

// The engine stops within a bound it knows (CON-210). These tests run in a
// synctest bubble, whose clock moves only when every goroutine in it waits:
// Stop's 45 seconds pass at once, and are measured exactly.

// deafLoop is a part that does not stop when asked: its Stop returns once
// release is closed, whatever its context says.
type deafLoop struct{ release <-chan struct{} }

func (l deafLoop) Start(context.Context) error { return nil }
func (l deafLoop) Stop()                       { <-l.release }

type deafWatcher struct{ deafLoop }

func (deafWatcher) Snapshot() map[watch.CrewRef]watch.Health { return nil }
func (deafWatcher) RuntimeNotice() (string, time.Time)       { return "", time.Time{} }

type deafPilot struct{ deafLoop }

func (deafPilot) Snapshot() map[string]autopilot.Status { return nil }

// deafRecovery is a recovery that returns once release is closed, deaf to
// its context, like a restart stuck in Herdr.
func deafRecovery(release <-chan struct{}) RecoverFunc {
	return func(context.Context, *store.Workspace, func(recovery.Progress)) recovery.Result {
		<-release
		return recovery.Result{}
	}
}

// Every part, a snapshot computation and an action all wait longer than the
// bound: Stop returns once the bound has passed, under 50 seconds, names
// each of them on Stderr in the order they started, and leaves the timeline
// open for the process exit.
func TestEngineStopReturnsWithinItsBoundAndNamesWhatDidNotStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{}
		release := make(chan struct{})
		var stderr strings.Builder
		e := locksOnly(New(Options{
			Watcher:  deafWatcher{deafLoop{release}},
			Timeline: fakeTimeline{rec},
			Sender:   deafLoop{release},
			Pilot:    deafPilot{deafLoop{release}},
			Recover:  deafRecovery(release),
			Act: func(context.Context, coreapi.ActionRequest) (string, error) {
				<-release
				return "stopped at last", nil
			},
			Stderr: &stderr,
		}))
		e.snaps.compute = func(context.Context) (query.Snapshot, error) {
			<-release
			return query.Snapshot{}, nil
		}
		if err := e.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		acted := make(chan error, 1)
		go func() {
			_, err := e.Act(context.Background(), stopMate)
			acted <- err
		}()
		read, cancelRead := context.WithCancel(context.Background())
		go func() { _, _ = e.Snapshot(read) }()
		synctest.Wait()
		cancelRead() // the computation goes on without its reader

		start := time.Now()
		e.Stop()
		waited := time.Since(start)
		if waited != StopWait || waited >= 50*time.Second {
			t.Errorf("Stop returned after %s, want its bound %s, under 50s", waited, StopWait)
		}
		want := "mate core: observer, outbox, autopilot, recovery, snapshot, action stop on shop had not stopped 45s after the stop began; mate.db is left open for the process exit to release\n"
		if got := stderr.String(); got != want {
			t.Errorf("stderr\n%q\nwant\n%q", got, want)
		}
		if got := rec.entries(); len(got) != 0 {
			t.Errorf("log %q: the timeline was closed while its writers may still run", got)
		}

		// A second Stop does not stop again, nor wait the bound again.
		start = time.Now()
		e.Stop()
		if waited := time.Since(start); waited != 0 {
			t.Errorf("a second Stop waited %s", waited)
		}

		close(release)
		synctest.Wait()
		if err := <-acted; err != nil {
			t.Errorf("the action that returned after Stop: %v", err)
		}
	})
}

// A part that ends with its context stops at once, and the timeline closes:
// Stop waits the bound only for what will not stop.
func TestEngineStopReturnsAsSoonAsEverythingStopped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{}
		opts, _, _, _ := fakeParts(rec)
		var stderr strings.Builder
		opts.Stderr = &stderr
		e := New(opts)
		if err := e.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		synctest.Wait()
		start := time.Now()
		e.Stop()
		if waited := time.Since(start); waited != 0 {
			t.Errorf("Stop waited %s with every part stopping at once", waited)
		}
		if stderr.Len() != 0 {
			t.Errorf("stderr %q, want nothing", stderr.String())
		}
		if got := rec.entries(); len(got) == 0 || got[len(got)-1] != "close timeline" {
			t.Errorf("log %q, want the timeline closed last", got)
		}
	})
}

// tickingPilot is an auto daemon that ticks every second until its context
// ends, as autopilot.Pilot does, counting its ticks.
type tickingPilot struct {
	ticks   atomic.Int32
	stopped chan struct{}
	ctx     context.Context
}

func (p *tickingPilot) Start(ctx context.Context) error {
	p.ctx, p.stopped = ctx, make(chan struct{})
	stopped := p.stopped
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if ctx.Err() != nil {
					return
				}
				p.ticks.Add(1)
			}
		}
	}()
	return nil
}

func (p *tickingPilot) Stop() { <-p.stopped }

func (*tickingPilot) Snapshot() map[string]autopilot.Status { return nil }

// Stop cancels the auto daemon before it waits for anything, here a
// recovery that will not stop: the daemon ticks no more from the moment
// Stop begins, so no refresh of a Mate's context starts while the engine
// waits for recovery. Before CON-210 it ran on until recovery's 45 seconds
// were up.
func TestEngineStopEndsThePilotsTicksAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		pilot := &tickingPilot{}
		e := New(Options{Pilot: pilot, Recover: deafRecovery(release), Stderr: &strings.Builder{}})
		if err := e.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		time.Sleep(3*time.Second + time.Millisecond)
		before := pilot.ticks.Load()
		if before != 3 {
			t.Fatalf("the pilot ticked %d times in 3s, want 3", before)
		}

		stopped := make(chan struct{})
		go func() {
			defer close(stopped)
			e.Stop()
		}()
		synctest.Wait()
		if pilot.ctx.Err() == nil {
			t.Fatal("the pilot's context is live while Stop waits for recovery")
		}
		time.Sleep(30 * time.Second)
		if after := pilot.ticks.Load(); after != before {
			t.Errorf("the pilot ticked %d times after Stop began", after-before)
		}
		close(release)
		<-stopped
	})
}

// An action asked for once Stop has begun is refused in the core's own
// words, and nothing of it runs.
func TestAnActionWhileStoppingIsRefused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		h := newHeldActs()
		e := New(Options{Act: h.act, Recover: deafRecovery(release), Stderr: &strings.Builder{}})
		if err := e.Start(context.Background()); err != nil {
			t.Fatalf("Start: %v", err)
		}
		stopped := make(chan struct{})
		go func() {
			defer close(stopped)
			e.Stop()
		}()
		synctest.Wait()
		_, err := e.Act(context.Background(), mergeCrew)
		if !errors.Is(err, coreapi.ErrStopping) || err.Error() != "mate core is stopping · nothing started" {
			t.Errorf("an action during Stop = %v, want %q", err, "mate core is stopping · nothing started")
		}
		close(release)
		<-stopped
		if _, err := e.Act(context.Background(), mergeCrew); !errors.Is(err, coreapi.ErrStopping) {
			t.Errorf("an action after Stop = %v, want ErrStopping", err)
		}
		if got := h.count(); got != 0 {
			t.Errorf("%d actions ran, want none", got)
		}
	})
}

// A press on a page runs with a context its request does not end
// (dashboard's handleAct, context.WithoutCancel), so nobody cancelled it
// when the console quit, and the server gave up on it after five seconds.
// Stop cancels it now, and waits for it like any other action.
func TestStopCancelsAPagesActionInFlight(t *testing.T) {
	entered := make(chan struct{})
	ended := make(chan error, 1)
	act := func(ctx context.Context, _ coreapi.ActionRequest) (string, error) {
		close(entered)
		<-ctx.Done()
		ended <- ctx.Err()
		return "", ctx.Err()
	}
	shop := func(context.Context) (query.Snapshot, error) {
		return query.Snapshot{Projects: []query.ProjectNode{{ProjectID: "shop", Name: "shop"}}}, nil
	}
	e := dashboardEngine(t, DashboardOptions{Addr: freeAddr}, act, shop)
	served, err := e.Dashboard()
	if err != nil {
		t.Fatal(err)
	}
	base := strings.TrimRight(strings.Fields(served)[0], "/")
	go func() {
		resp, err := http.Post(base+"/api/actions", "application/json", strings.NewReader(`{"action":"mode","project":"shop"}`))
		if err == nil {
			resp.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the page's press never reached the action")
	}

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		e.Stop()
	}()
	select {
	case err := <-ended:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("the page's action ended with %v, want its context cancelled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not cancel the page's action")
	}
	select {
	case <-stopped:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop did not return once the page's action had ended")
	}
}
