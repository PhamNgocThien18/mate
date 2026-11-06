package core

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/nguyenngocanh94/mate/internal/query"
)

const (
	// snapshotMaxAge is how long a snapshot is handed out again before the
	// next read computes a new one: the console's own refresh period, so a
	// console, Mate Office and Agent City open together cost one computation
	// every two seconds rather than one each.
	snapshotMaxAge = 2 * time.Second
	// snapshotLimit bounds one computation: two Herdr questions, the files
	// and the ledger. One that is still running past it fails every reader
	// waiting on it, and the next read starts another.
	snapshotLimit = 10 * time.Second
)

// Snapshot is the workspace as every UI shows it: query.LoadLive with
// Herdr's word about which agents are up, and what the engine's loops know
// put in - recovery, the crews' health, the observer's word about Herdr,
// the stale timelines, the daemon's state, and the token ledger. Its
// Commands are empty: they are the console's own keys, which it adds itself.
//
// A snapshot is handed out again for up to two seconds. Readers that come
// while one is being computed wait for that one, which runs under the
// engine's context rather than any reader's: a reader whose ctx ends stops
// waiting and gets ctx's error, and the others still get the snapshot.
// A failed computation is not kept; the next read computes again. Nothing
// is computed while nobody reads.
//
// The snapshot is shared by every reader of it: it is read-only, and a
// reader that changes a field changes it under the others.
func (e *Engine) Snapshot(ctx context.Context) (query.Snapshot, error) {
	return e.snaps.get(ctx)
}

// FreshSnapshot computes a new snapshot for this reader alone, under ctx and
// within the same ten seconds, whatever is cached, and keeps nothing: for
// the check that a pressed button is still offered before its action runs
// (the dashboard's POST /api/actions). It runs on the reader's goroutine,
// so Stop neither cancels nor waits for it; once the engine has stopped it
// computes nothing.
func (e *Engine) FreshSnapshot(ctx context.Context) (query.Snapshot, error) {
	if e.snaps.isClosed() {
		return query.Snapshot{}, errStopped
	}
	ctx, cancel := context.WithTimeout(ctx, snapshotLimit)
	defer cancel()
	return e.snaps.safeCompute(ctx)
}

// Invalidate drops the cached snapshot, so the next read computes one that
// sees what an action just changed. A computation already running when it
// is called still answers the readers waiting on it, and is not kept; a
// reader that comes after Invalidate waits for a computation that started
// after it.
func (e *Engine) Invalidate() { e.snaps.invalidate() }

// snapshotCache is the engine's one snapshot: at most one computation per
// generation at a time, its result kept for maxAge.
type snapshotCache struct {
	compute func(context.Context) (query.Snapshot, error)
	// ctx is the engine's, under which a shared computation runs.
	ctx    context.Context
	maxAge time.Duration
	limit  time.Duration
	now    func() time.Time

	mu sync.Mutex
	// gen counts the Invalidates: a computation that started in an older
	// generation answers its readers and is not kept.
	gen    uint64
	held   *heldSnapshot
	flight *flight
	// closed is set when the engine stops: no computation starts after it,
	// and running counts the ones that started before, for Stop to wait on.
	closed  bool
	running sync.WaitGroup
}

// heldSnapshot is a computed snapshot and when its computation started.
type heldSnapshot struct {
	snap query.Snapshot
	at   time.Time
}

// flight is one shared computation; done closes once snap and err are set.
type flight struct {
	gen  uint64
	done chan struct{}
	snap query.Snapshot
	err  error
}

func (c *snapshotCache) get(ctx context.Context) (query.Snapshot, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return query.Snapshot{}, errStopped
	}
	if c.held != nil && c.now().Sub(c.held.at) < c.maxAge {
		snap := c.held.snap
		c.mu.Unlock()
		return snap, nil
	}
	f := c.flight
	if f == nil || f.gen != c.gen {
		f = &flight{gen: c.gen, done: make(chan struct{})}
		c.flight = f
		c.running.Add(1)
		go c.run(f, c.now())
	}
	c.mu.Unlock()
	select {
	case <-f.done:
		return f.snap, f.err
	case <-ctx.Done():
		return query.Snapshot{}, ctx.Err()
	}
}

// run computes f under the engine's context and keeps the result, unless it
// failed or an Invalidate came while it ran.
func (c *snapshotCache) run(f *flight, started time.Time) {
	defer c.running.Done()
	ctx, cancel := context.WithTimeout(c.ctx, c.limit)
	snap, err := c.safeCompute(ctx)
	cancel()
	c.mu.Lock()
	if c.flight == f {
		c.flight = nil
	}
	if err == nil && f.gen == c.gen {
		c.held = &heldSnapshot{snap: snap, at: started}
	}
	f.snap, f.err = snap, err
	c.mu.Unlock()
	close(f.done)
}

// safeCompute is one computation, with a panic in it turned into its error:
// a shared computation runs on a goroutine of its own, where a panic would
// end the process, and before CON-208 a dashboard's read ran inside an HTTP
// handler, which survives one.
func (c *snapshotCache) safeCompute(ctx context.Context) (snap query.Snapshot, err error) {
	defer func() {
		if p := recover(); p != nil {
			snap, err = query.Snapshot{}, fmt.Errorf("core: computing the snapshot panicked: %v", p)
		}
	}()
	return c.compute(ctx)
}

func (c *snapshotCache) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *snapshotCache) invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen++
	c.held = nil
}

// close starts no computation from here on and waits for the ones running,
// which the engine's context, cancelled by then, ends.
func (c *snapshotCache) close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	c.running.Wait()
}

// assemble computes one snapshot over the engine's workspace: the files and
// Herdr's word through query.LoadLive, then what the loops know. The order is
// the one the console's load kept before CON-208.
func (e *Engine) assemble(ctx context.Context) (query.Snapshot, error) {
	ws := e.o.Workspace
	snap, err := query.LoadLive(ctx, ws, e.o.Harnesses, nil, liveness(ctx, ws, e.o.Spawn))
	if err != nil {
		return snap, err
	}
	snap = withRecovery(snap, e.Recovery(), e.now())
	snap = withHostWatch(snap, e.hosts.read())
	if e.o.Watcher != nil {
		snap = withCrewHealth(snap, e.o.Watcher.Snapshot())
		snap = withRuntimeNotice(snap, e.o.Watcher)
	}
	snap = withTimelineStale(snap, e.o.Ingest)
	if e.o.Pilot != nil {
		snap = withAutoStatus(snap, e.o.Pilot.Snapshot())
	}
	return withTokens(snap, ws), nil
}
