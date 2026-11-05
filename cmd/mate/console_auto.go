package main

import (
	"context"

	"github.com/nguyenngocanh94/mate/internal/autopilot"
	"github.com/nguyenngocanh94/mate/internal/core"
	"github.com/nguyenngocanh94/mate/internal/outbox"
	"github.com/nguyenngocanh94/mate/internal/prwatch"
	"github.com/nguyenngocanh94/mate/internal/recovery"
	"github.com/nguyenngocanh94/mate/internal/refresh"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// The Mate's delivery wiring (mvp.md tasks 19 and 30), and engineOptions,
// which hands it, the observer and recovery to internal/core's engine
// (CON-207). They sit beside the observer's wiring in console_watch.go, and
// for the same reason: internal/query is file-only and internal/ui/console
// may reach neither Herdr, internal/outbox nor internal/autopilot (its
// boundary test), so this package is the one place that can hold both a
// live runtime and a snapshot.

// engineOptions builds the parts internal/core's engine runs for an open
// workspace (CON-207): the observer with the timeline it records, the outbox
// sender and the auto daemon, and recovery; and what its snapshot asks Herdr
// through (CON-208). The console runs them under an engine for as long as
// it is open, adding the dashboard and the notifications; `mate core` is to
// run the same ones (CON-211). The builders stay in this package, beside
// the commands that build the same parts for one shot of their own
// (spawn.MateOutbox, consoleSessionRef, mateRefresher).
//
// Each loop opens its own *store.Workspace over ws's directory, for the
// reason consoleWatcher gives; recovery repairs ws itself, and the snapshot
// and the dashboard read it.
func engineOptions(ws *store.Workspace, deps spawn.Deps) (core.Options, error) {
	// The observer of mvp.md section 4b runs for as long as the workspace is
	// open, and only then: it lives in this process, so quitting the console
	// means nobody is watching the crews and no new incident is opened. What
	// it already wrote stays in `incidents.log`.
	//
	// It is also the single writer of `.mate/mate.db` (mvp.md M5): the
	// timeline is recorded at the end of each of its polls, and the database
	// handle's advisory lock is released when the engine stops.
	watcher, timelineDB, ingest, err := consoleWatcherWithTimeline(ws.Root(), deps)
	if err != nil {
		return core.Options{}, err
	}
	// The auto daemon of mvp.md section 5 runs beside it, and like it only
	// while the workspace is open: a closed console sends nothing, which is
	// the honest shape of a feature whose whole point is that it types into
	// somebody's composer. It sends only for the projects whose `mate/.auto`
	// exists, and it re-reads that flag every tick.
	delivery, err := consolePilot(ws.Root(), deps)
	if err != nil {
		if timelineDB != nil {
			_ = timelineDB.Close()
		}
		return core.Options{}, err
	}
	opts := core.Options{
		Workspace: ws,
		Watcher:   core.WatchLoop(watcher),
		Ingest:    ingest,
		Sender:    core.SenderLoop(delivery.sender),
		Pilot:     core.PilotLoop(delivery.pilot),
		// What a machine restart or a copied workspace left behind is put
		// right in the background, without asking: the console is usable
		// meanwhile and its status line says where recovery is (docs/mvp.md
		// M17).
		Recover: func(ctx context.Context, ws *store.Workspace, progress func(recovery.Progress)) recovery.Result {
			return recovery.Run(ctx, ws, deps, progress)
		},
		// The snapshot every UI reads asks the same Herdr which agents are
		// up, and carries the harnesses this binary launches (CON-208).
		Spawn:     deps,
		Harnesses: consoleHarnesses(),
	}
	// No handle - another process writes the timeline - is no timeline to
	// close, which a nil *db.DB in the interface would not say.
	if timelineDB != nil {
		opts.Timeline = timelineDB
	}
	return opts, nil
}

// consoleDelivery is everything that types into a Mate's composer without a
// keystroke of the captain's, for as long as the console is open: the outbox
// sender loop (task 30), which delivers every queued `[assign]` and digest,
// and the auto daemon (task 19), which queues digests. They start and stop
// together because neither is any use without the other: a daemon with no
// sender queues digests nobody types, and a console without the sender
// leaves every `[assign]` the Mate was too busy to take waiting forever.
//
// The engine runs the two as loops of its own, in this same order
// (engineOptions); Start and Stop are for a run with no engine, such as the
// live tests'.
type consoleDelivery struct {
	sender *outbox.Sender
	pilot  *autopilot.Pilot
}

// Start runs the sender loop and the daemon, each on its own goroutine.
func (d *consoleDelivery) Start(ctx context.Context) {
	d.sender.Start(ctx)
	d.pilot.Start(ctx)
}

// Stop ends both and waits for what is in flight; the daemon first, so it
// cannot queue a digest after the sender has gone.
func (d *consoleDelivery) Stop() {
	d.pilot.Stop()
	d.sender.Stop()
}

// Snapshot is the daemon's per-project state, for the MODE cell and footer.
func (d *consoleDelivery) Snapshot() map[string]autopilot.Status { return d.pilot.Snapshot() }

// consolePilot builds the delivery for an open workspace.
//
// The sender loop and the daemon each open their own *store.Workspace over
// the same directory, exactly as consoleWatcher does: a Workspace caches
// workspace.yaml and rewrites that cache on LoadConfig, which both loops and
// query.Load (on the UI goroutine) call on their own schedules. The files
// themselves are coordinated by the outbox's own lock and by flock on the
// append-only logs.
func consolePilot(dir string, deps spawn.Deps) (*consoleDelivery, error) {
	senderWS, err := store.Open(dir)
	if err != nil {
		return nil, err
	}
	pilotWS, err := store.Open(dir)
	if err != nil {
		return nil, err
	}
	return &consoleDelivery{
		sender: spawn.MateOutbox(senderWS, deps),
		pilot:  consoleAutoPilot(pilotWS, deps),
	}, nil
}

// consoleAutoPilot is the auto daemon over ws, queueing into ws's outbox and
// making its one immediate attempt through a sender over the same handle.
func consoleAutoPilot(ws *store.Workspace, deps spawn.Deps) *autopilot.Pilot {
	// What closed tasks left behind is collected once at start and every
	// GCEvery after (CON-114) - by a console with the real teardown only,
	// so a test's fake deps never reach lsof or signal a process.
	var gc *gcSchedule
	if deps.Cleaner != nil {
		gc = consoleGC(ws, liveGCDeps())
	}
	return autopilot.New(ws, autopilot.Deps{
		Outbox: spawn.MateOutbox(ws, deps),
		Maintain: func(ctx context.Context, project string) (bool, error) {
			return mateRefresher(ws, deps).Run(ctx, project, true)
		},
		Sweep: func(ctx context.Context) {
			prwatch.Sweep(ws, prStarter)
			gc.Tick(ctx)
		},
		Budget: consoleBudgetActions(ws, deps),
	})
}

// mateRefresher is the Mate's context refresh (internal/refresh) over ws,
// handed what only this package holds: the receipt protocol of `mate
// checkpoint` (checkpoint.go) and the outbox sender over deps' runtime.
// `mate mate refresh` and the auto daemon build one for each refresh.
func mateRefresher(ws *store.Workspace, deps spawn.Deps) *refresh.Refresher {
	return refresh.New(ws, refresh.Deps{
		Spawn:  deps,
		Outbox: spawn.MateOutbox(ws, deps),
		RequestPath: func(project string) (string, error) {
			return refreshMeta(ws, project, "refresh-request")
		},
		VerifyCheckpoint: func(project, nonce, session string) error {
			return verifyCheckpoint(ws, project, nonce, session)
		},
	})
}
