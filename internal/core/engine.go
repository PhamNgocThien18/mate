package core

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/nguyenngocanh94/mate/internal/autopilot"
	"github.com/nguyenngocanh94/mate/internal/notifications"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/recovery"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/timeline"
	"github.com/nguyenngocanh94/mate/internal/watch"
)

// Loop is one of the engine's background loops. Start begins it on its own
// goroutine and returns at once; an error means it did not start and there
// is nothing of it to stop. Stop ends it and waits for the work in flight.
type Loop interface {
	Start(ctx context.Context) error
	Stop()
}

// CrewHealth is what the observer has seen, safe to read from any
// goroutine while it polls.
type CrewHealth interface {
	// Snapshot is the latest reading of every crew it has observed.
	Snapshot() map[watch.CrewRef]watch.Health
	// RuntimeNotice is its standing word about a Herdr it could not reach,
	// and when it last failed to; empty once Herdr answers again.
	RuntimeNotice() (string, time.Time)
}

// Watcher is the observer of mvp.md section 4b (internal/watch) as the
// engine runs it.
type Watcher interface {
	Loop
	CrewHealth
}

// AutoStatus is the auto daemon's per-project state, safe to read from any
// goroutine while it ticks.
type AutoStatus interface {
	Snapshot() map[string]autopilot.Status
}

// Pilot is the auto daemon of mvp.md section 5 (internal/autopilot) as the
// engine runs it.
type Pilot interface {
	Loop
	AutoStatus
}

// RecoverFunc recovers ws once (internal/recovery.Run over the runtime only
// the caller holds), telling progress of each step, and returns what it did.
// A cancelled ctx makes it stop between two agents.
type RecoverFunc func(ctx context.Context, ws *store.Workspace, progress func(recovery.Progress)) recovery.Result

// Options is the engine's parts, built by the caller. A nil part is not run.
type Options struct {
	// Workspace is the open workspace the engine runs for. Recovery
	// repairs it.
	Workspace *store.Workspace
	// Watcher is the observer: it reads every open crew's pane, appends
	// what it concludes to incidents.log and, while it has the timeline,
	// records the timeline at the end of each poll.
	Watcher Watcher
	// Timeline is the observer's writable handle on `.mate/mate.db`. The
	// engine closes it last, once nothing that writes it runs, and that
	// releases its advisory lock. It is nil when another process writes
	// the timeline (internal/db.ErrLocked): the observer then observes
	// without recording, and the other process records once rather than
	// twice.
	Timeline io.Closer
	// Ingest is the timeline ingest the watcher runs at the end of each
	// poll; nil with Timeline. The engine only hands it out (Engine.Ingest).
	Ingest *timeline.Ingester
	// Sender is the outbox sender loop (internal/outbox): it delivers every
	// queued `[assign]` and digest into the Mate's composer.
	Sender Loop
	// Pilot is the auto daemon: it queues digests for the projects in auto
	// mode, refreshes the Mates' context, sweeps pull requests and token
	// budgets.
	Pilot Pilot
	// Recover is recovery (docs/mvp.md M17): what a machine restart or a
	// copied workspace left behind, put right once in the background.
	Recover RecoverFunc

	// Spawn is the live dependencies a snapshot asks Herdr through, for
	// which agents are up (query.LoadLive). With no Runtime it asks nothing
	// and every row keeps its recorded state.
	Spawn spawn.Deps
	// Harnesses is the binary's harness catalog, which every snapshot and
	// every notification read carries.
	Harnesses query.Harnesses
	// Dashboard is Mate Office and Agent City, served for as long as the
	// engine runs; nil serves nothing.
	Dashboard *DashboardOptions
	// Notifier sends the captain a native notification for news waiting on
	// them, for as long as the engine runs (NativeNotifier); nil sends none.
	Notifier notifications.Sender
}

// Engine runs a workspace's background loops for as long as it is open, and
// computes the snapshot every UI reads (Snapshot).
//
// Start starts the reading of workspace.yaml, the watcher, then the sender,
// then the pilot - so the daemon never queues a digest no sender would type
// - then recovery, and last what serves the captain: the dashboard, then
// the notifications, which link to it. Stop stops them in the reverse order
// and closes the timeline once nothing that writes it runs. The order is
// the one runConsole kept in its own body before CON-207 and CON-208.
type Engine struct {
	o         Options
	recovery  *recoveryRun
	reload    *tickLoop
	dashboard *dashboardPart
	notify    *notifyLoop
	snaps     *snapshotCache
	now       func() time.Time

	// ctx is the engine's own: the shared snapshot computations run under
	// it. cancel ends it when the engine stops, and so does the end of the
	// context Start was given, through unhook's AfterFunc.
	ctx    context.Context
	cancel context.CancelFunc
	unhook func() bool

	// mu serializes Start and Stop; a Stop that comes while another runs
	// waits for it and then finds the engine stopped.
	mu      sync.Mutex
	state   engineState
	running []Loop
}

type engineState int

const (
	idle engineState = iota
	started
	stopped
)

var (
	errStarted = errors.New("core: the engine is already running")
	errStopped = errors.New("core: the engine has been stopped")
)

// New builds an engine over its parts. It starts nothing until Start, and
// from here on it owns the parts: Stop, or a failed Start, releases them.
func New(o Options) *Engine {
	e := &Engine{o: o, now: time.Now}
	e.ctx, e.cancel = context.WithCancel(context.Background())
	e.snaps = &snapshotCache{compute: e.assemble, ctx: e.ctx, maxAge: snapshotMaxAge, limit: snapshotLimit, now: time.Now}
	if o.Recover != nil {
		e.recovery = &recoveryRun{run: o.Recover, ws: o.Workspace, wait: recoveryWait, now: time.Now}
	}
	if o.Workspace != nil {
		e.reload = configReload(o.Workspace)
		if o.Dashboard != nil {
			e.dashboard = &dashboardPart{ws: o.Workspace, o: *o.Dashboard, deps: e.dashboardDeps}
		}
		if o.Notifier != nil {
			e.notify = &notifyLoop{root: o.Workspace.Root(), harnesses: o.Harnesses, sender: o.Notifier, url: e.dashboardURL}
		}
	}
	return e
}

// Start starts the loops in order under ctx, the caller's: cancelling it
// ends them too, though Stop needs nothing of it. When a part fails to
// start, Start stops the ones it had started, closes the timeline and
// returns that part's error unchanged; the engine is then stopped. The
// dashboard and the notifications never fail it: what keeps either from
// starting is said by Dashboard and Notifications.
func (e *Engine) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch e.state {
	case started:
		return errStarted
	case stopped:
		return errStopped
	}
	e.unhook = context.AfterFunc(ctx, e.cancel)
	for _, part := range e.parts() {
		if err := part.Start(ctx); err != nil {
			e.stop()
			return err
		}
		e.running = append(e.running, part)
	}
	e.state = started
	return nil
}

// parts is every part that runs, in the order Start starts them.
func (e *Engine) parts() []Loop {
	var out []Loop
	if e.reload != nil {
		out = append(out, e.reload)
	}
	for _, part := range []Loop{e.o.Watcher, e.o.Sender, e.o.Pilot} {
		if part != nil {
			out = append(out, part)
		}
	}
	if e.recovery != nil {
		out = append(out, e.recovery)
	}
	if e.dashboard != nil {
		out = append(out, e.dashboard)
	}
	if e.notify != nil {
		out = append(out, e.notify)
	}
	return out
}

// Stop stops the loops in the reverse of the order they started in - the
// dashboard waits at most 5 seconds for its requests, a recovery that will
// not stop gets 45 - and then closes the timeline. A snapshot being
// computed is cancelled and waited for, and none is computed after. It is
// safe to call more than once, and on an engine that never started: that
// one only closes the timeline it was given.
func (e *Engine) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stop()
}

func (e *Engine) stop() {
	if e.state == stopped {
		return
	}
	e.state = stopped
	e.cancel()
	if e.unhook != nil {
		e.unhook()
	}
	for i := len(e.running) - 1; i >= 0; i-- {
		e.running[i].Stop()
	}
	e.running = nil
	e.snaps.close()
	if e.o.Timeline != nil {
		// As the console's deferred Close always has: nothing is left to
		// tell about a database that would not close at exit.
		_ = e.o.Timeline.Close()
	}
}

// CrewHealth is the observer's readings: each crew's health, and its word
// about a Herdr it could not reach.
func (e *Engine) CrewHealth() CrewHealth { return e.o.Watcher }

// AutoStatus is the auto daemon's per-project state: when it last sent, and
// why it last could not.
func (e *Engine) AutoStatus() AutoStatus { return e.o.Pilot }

// Ingest is the timeline ingest, for which projects' timeline has stopped
// moving (Ingester.Stale). It is nil while another process writes the
// timeline: that process's ingest is not this one's to report.
func (e *Engine) Ingest() *timeline.Ingester { return e.o.Ingest }

// Recovery is where recovery stands: zero until it reports, then its latest
// progress, then what it did.
func (e *Engine) Recovery() Recovery {
	if e.recovery == nil {
		return Recovery{}
	}
	return e.recovery.read()
}

// Dashboard is what the dashboard serves at, once Start has returned: its
// loopback URL, followed by the LAN one when the bind is reachable from the
// LAN; or why it did not start. Both are empty for an engine that serves no
// dashboard.
func (e *Engine) Dashboard() (string, error) {
	if e.dashboard == nil {
		return "", nil
	}
	return e.dashboard.read()
}

func (e *Engine) dashboardURL() string {
	url, _ := e.Dashboard()
	return url
}

// Notifications is the notification loop's latest word - a line for the
// status bar while its last tick failed, "" while it sends - and, once
// Start has returned, why it did not start. Both are empty for an engine
// that sends no notification.
func (e *Engine) Notifications() (string, error) {
	if e.notify == nil {
		return "", nil
	}
	return e.notify.read()
}
