package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/nguyenngocanh94/mate/internal/autopilot"
	"github.com/nguyenngocanh94/mate/internal/coreapi"
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
	// and every row keeps its recorded state. The engine's own actions
	// start, stop and read agents through it, and parse a harness against
	// its Harnesses; with no Runtime they refuse in spawn's words.
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
	// Notice is Jev, which explain_notice asks about a screen (CON-141);
	// nil when Jev is off, and the engine then has no explain_notice: it
	// refuses one as not wired, and its pages do not offer it. A caller
	// with a *notice.Client sets it only when the client is not nil: a nil
	// pointer in this interface is not a nil Notice (CON-232).
	Notice NoticeClassifier
	// Act is a test's stand-in for the engine's own actions, which a real
	// engine leaves nil: the engine runs every action itself (CON-232).
	// When set, it runs every action but the chat and a tick the Mate's
	// host takes, in place of the engine's own bodies, so a test of the
	// locks can hold any action it likes. Engine.Act is its only caller,
	// under the lock on the action's target.
	Act coreapi.ActionFunc
	// Stderr is where Stop names the parts that had not stopped within its
	// bound; os.Stderr when nil.
	Stderr io.Writer

	// Owner is who runs the engine, as core.lock records it: OwnerCore for
	// `mate core run`, OwnerConsole (the zero value) for a console that
	// runs the engine itself, which only a console built before CON-214
	// does. Unused with Claim.
	Owner string
	// Claim is core.lock taken ahead (Lock) by a caller that must hold it
	// before it builds the parts; the engine owns it from New on, and
	// releases it when it stops, a failed Start included. Nil makes Start
	// take the lock itself.
	Claim *Claim
	// ConsolePresent says whether a console is open on the workspace (CON-203,
	// decided 2026-11-05): a Manual project gets nothing typed into its Mate
	// by the outbox loop while it says false; an Auto project gets its
	// deliveries either way. The caller hands the same function to the
	// outbox sender it builds (cmd/mate's engineOptions); the engine reports
	// it (Engine.ConsolePresent). A console's engine says true; `mate core
	// run` hands in ConsoleSeen's Present. Nil is true.
	ConsolePresent func() bool
	// ConsoleSeen is where the engine records each request a console makes
	// on its socket (coreapi.ClientHeader, CON-212). It only records:
	// `mate core run` hands its Present to ConsolePresent and to the outbox
	// alike. Nil records nothing.
	ConsoleSeen *ConsoleSeen
	// Notices are what the caller knows is wrong and a console's status
	// line says - Jev's and the observer's configuration errors. The engine
	// only reports them (Status), after its own.
	Notices []string
}

// StopWait is how long Stop waits for everything it has cancelled: the
// loops, recovery, the Office/City server, the notifications, a snapshot
// being computed, and every action in flight from any UI, all at once under
// this one bound (CON-210). It is longer than the spawn package's own 30s
// cleanup bound, so an action or a recovery cancelled halfway through
// starting an agent gets the chance to undo it. Stop returns within it and
// the closing of mate.db, well inside 50 seconds; `mate core stop`
// (CON-213) waits longer than that before it kills.
const StopWait = 45 * time.Second

// Engine runs a workspace's background loops for as long as it is open,
// computes the snapshot every UI reads (Snapshot), and runs every action a
// UI asks for (Act).
//
// Start starts the reading of workspace.yaml, the watcher, then the sender,
// then the pilot - so the daemon never queues a digest no sender would type
// - then recovery and the watch over the headless hosts (CON-226), and last
// what serves the captain: the dashboard, then the notifications, which
// link to it. The order is the one runConsole kept in its own body before
// CON-207 and CON-208, with the host watch added after recovery. Stop
// cancels them all at once, waits for them together within StopWait, and
// closes the timeline once they have all stopped.
type Engine struct {
	o        Options
	recovery *recoveryRun
	// hosts watches each headless Mate's host of its own (CON-226); nil
	// without a workspace or a Herdr runtime to start a Mate through.
	hosts     *hostWatch
	reload    *tickLoop
	dashboard *dashboardPart
	notify    *notifyLoop
	snaps     *snapshotCache
	locks     actionLocks
	acts      actionRuns
	// holds is every restart held for the captain's second press, from
	// whichever UI (restartMate).
	holds restartHolds
	now   func() time.Time
	// stopWait is StopWait; a test shortens it.
	stopWait time.Duration

	// claim is the workspace's core.lock while the engine runs; Start
	// takes it first (or the one Options.Claim handed in) and Stop
	// releases it last.
	claim *Claim
	// holder is what core.lock recorded of this engine when Start took
	// it, before any part could read it; zero without one.
	holder store.CoreHolder

	// ctx is the engine's own: the shared snapshot computations run under
	// it. cancel ends it when the engine stops, and so does the end of the
	// context Start was given, through unhook's AfterFunc.
	ctx    context.Context
	cancel context.CancelFunc
	unhook func() bool
	// cancelRun ends the context every part was started under, the
	// caller's with this cancel added. Stop calls it before it waits for
	// any part, so none starts new work while another is still stopping.
	cancelRun context.CancelFunc

	// mu serializes Start and Stop; a Stop that comes while another runs
	// waits for it and then finds the engine stopped.
	mu      sync.Mutex
	state   engineState
	running []part
}

// part is one of the engine's loops, with the name Stop gives it when it
// has not stopped in time.
type part struct {
	name string
	Loop
}

type engineState int

const (
	idle engineState = iota
	started
	stopped
)

var (
	errStarted = errors.New("core: the engine is already running")
	// errStopped is also what a snapshot read gets once Stop has begun, so
	// it is coreapi.ErrStopping too: a page's press, checked against a
	// fresh snapshot before it reaches Act, is refused in the same words.
	errStopped = fmt.Errorf("core: the engine has been stopped: %w", coreapi.ErrStopping)
)

// New builds an engine over its parts. It starts nothing until Start, and
// from here on it owns the parts: Stop, or a failed Start, releases them.
func New(o Options) *Engine {
	e := &Engine{o: o, now: time.Now, stopWait: StopWait}
	e.ctx, e.cancel = context.WithCancel(context.Background())
	e.snaps = &snapshotCache{compute: e.assemble, ctx: e.ctx, maxAge: snapshotMaxAge, limit: snapshotLimit, now: time.Now}
	if o.Recover != nil {
		e.recovery = &recoveryRun{run: o.Recover, ws: o.Workspace, now: time.Now}
	}
	if o.Workspace != nil {
		e.reload = configReload(o.Workspace)
		if o.Spawn.Runtime != nil {
			// spawn.StartMate, which the restart runs, refuses without one.
			e.hosts = e.newHostWatch()
		}
		if o.Dashboard != nil {
			e.dashboard = &dashboardPart{ws: o.Workspace, o: *o.Dashboard, deps: e.dashboardDeps}
		}
		if o.Notifier != nil {
			e.notify = &notifyLoop{root: o.Workspace.Root(), harnesses: o.Harnesses, sender: o.Notifier, url: e.dashboardURL}
		}
	}
	return e
}

// Start first takes the workspace's `.mate/core.lock` (CON-211) and is
// refused with a *RunningError while another engine holds it, in `mate
// core` or in a console; it then starts the loops in order under ctx, the
// caller's: cancelling it ends them too, though Stop needs nothing of it.
// Once they run it records in core.lock where the dashboard serves. When a
// part fails to start, Start stops the ones it had started as Stop does -
// within StopWait, closing the timeline and releasing core.lock once they
// have all stopped - and returns that part's error unchanged; the engine
// is then stopped. The notifications never fail it, nor does the dashboard
// over TCP: what keeps either from starting is said by Dashboard and
// Notifications. A socket asked for (DashboardOptions.Socket) that cannot
// be served does.
func (e *Engine) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch e.state {
	case started:
		return errStarted
	case stopped:
		return errStopped
	}
	// One workspace runs one engine, in `mate core` or in a console
	// (CON-211): the lock comes before any part, so a refused engine has
	// started nothing.
	if e.o.Workspace != nil {
		if err := e.lock(); err != nil {
			e.stop()
			return err
		}
	}
	// The socket of `mate core` next, before anything acts: a core that
	// cannot be reached on it does not run.
	if e.dashboard != nil {
		if err := e.dashboard.bindSocket(); err != nil {
			e.stop()
			return err
		}
	}
	e.unhook = context.AfterFunc(ctx, e.cancel)
	runCtx, cancelRun := context.WithCancel(ctx)
	e.cancelRun = cancelRun
	for _, p := range e.parts() {
		if err := p.Start(runCtx); err != nil {
			e.stop()
			return err
		}
		e.running = append(e.running, p)
	}
	e.state = started
	e.recordServing()
	return nil
}

// parts is every part that runs, in the order Start starts them.
func (e *Engine) parts() []part {
	var out []part
	if e.reload != nil {
		out = append(out, part{"workspace.yaml reload", e.reload})
	}
	if e.o.Watcher != nil {
		out = append(out, part{"observer", e.o.Watcher})
	}
	if e.o.Sender != nil {
		out = append(out, part{"outbox", e.o.Sender})
	}
	if e.o.Pilot != nil {
		out = append(out, part{"autopilot", e.o.Pilot})
	}
	if e.recovery != nil {
		out = append(out, part{"recovery", e.recovery})
	}
	if e.hosts != nil {
		out = append(out, part{"host watch", e.hosts})
	}
	if e.dashboard != nil {
		out = append(out, part{"Office/City server", e.dashboard})
	}
	if e.notify != nil {
		out = append(out, part{"notifications", e.notify})
	}
	return out
}

// Stop stops the engine within StopWait (CON-210). From its first moment
// every new action is refused (coreapi.ErrStopping). Then it cancels, all
// at once, every part - the loops, recovery, the Office/City server, the
// notifications -, a shared snapshot being computed and every action in
// flight, the console's and the pages' alike, so nothing starts new work
// while something else is still stopping. It waits for all of them
// together, StopWait at most; the server gives its requests 5 seconds of
// that (dashboard.Server.Serve). Once they have all stopped it closes the
// timeline, then releases core.lock. Past the bound it names on Stderr what
// has not stopped and returns, leaving mate.db open and core.lock held:
// what still runs may write the one and must not run beside another
// engine, and the process exit releases both. A restart cancelled halfway
// is put right by the next recovery, as when the console was killed
// halfway before.
//
// After Stop no snapshot is handed out or computed (a FreshSnapshot already
// running is its reader's to wait for). It is safe to call more than once:
// a second Stop waits for the first and stops nothing again. On an engine
// that never started it only closes the timeline it was given.
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
	// Refuse, then cancel everything, and only then wait.
	actions := e.acts.close()
	if e.cancelRun != nil {
		e.cancelRun()
	}
	e.cancel()
	if e.unhook != nil {
		e.unhook()
	}
	var waits []stopping
	for _, p := range e.running {
		waits = append(waits, stopping{p.name, inBackground(p.Stop)})
	}
	e.running = nil
	if e.dashboard != nil {
		// A socket bound for a dashboard that never started.
		e.dashboard.release()
	}
	waits = append(waits, stopping{"snapshot", inBackground(e.snaps.close)})
	for _, a := range actions {
		waits = append(waits, stopping{a.name, a.done})
	}
	if late := awaitAll(waits, e.stopWait); len(late) > 0 {
		line := fmt.Sprintf("mate core: %s had not stopped %s after the stop began", strings.Join(late, ", "), e.stopWait)
		switch {
		case e.o.Timeline != nil && e.claim != nil:
			line += "; mate.db and core.lock are left for the process exit to release"
		case e.o.Timeline != nil:
			line += "; mate.db is left open for the process exit to release"
		case e.claim != nil:
			line += "; core.lock is left for the process exit to release"
		}
		fmt.Fprintln(e.stderr(), line)
		return
	}
	if e.o.Timeline != nil {
		// As the console's deferred Close always has: nothing is left to
		// tell about a database that would not close at exit.
		_ = e.o.Timeline.Close()
	}
	// Last: once it is released another engine may start, and nothing of
	// this one is left to run beside it.
	e.claim.Release()
	if e.claim == nil {
		// A Start that never came: the claim handed in is still the
		// engine's to release.
		e.o.Claim.Release()
	}
}

// ConsolePresent is Options.ConsolePresent's answer: whether a console is
// open on the workspace. True when the caller gave no such function.
func (e *Engine) ConsolePresent() bool {
	if e.o.ConsolePresent == nil {
		return true
	}
	return e.o.ConsolePresent()
}

// stopping is something Stop waits for: done closes once it has stopped.
type stopping struct {
	name string
	done <-chan struct{}
}

// inBackground runs stop on a goroutine of its own and returns the channel
// that closes once it has returned.
func inBackground(stop func()) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		stop()
	}()
	return done
}

// awaitAll waits for every one of waits, all of them within one bound, and
// returns the names of those that had not stopped by then, in their order.
func awaitAll(waits []stopping, bound time.Duration) []string {
	timer := time.NewTimer(bound)
	defer timer.Stop()
	expired := false
	var late []string
	for _, w := range waits {
		if !expired {
			select {
			case <-w.done:
				continue
			case <-timer.C:
				expired = true
			}
		}
		select {
		case <-w.done:
		default:
			late = append(late, w.name)
		}
	}
	return late
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
	s := e.dashboard.read()
	url := s.URL
	if s.LANURL != "" {
		url += " " + s.LANURL
	}
	return url, s.Err
}

// Serving is where the dashboard serves, once Start has returned: over
// TCP, and on the socket of `mate core`. It is zero for an engine that
// serves no dashboard.
func (e *Engine) Serving() Serving {
	if e.dashboard == nil {
		return Serving{}
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
