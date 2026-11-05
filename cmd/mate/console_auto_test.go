package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/autopilot"
	"github.com/nguyenngocanh94/mate/internal/core"
	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/outbox"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/watch"
)

// The console's auto-daemon wiring (mvp.md task 19): internal/autopilot over
// the real spawn.MateHandle, and its state put into the snapshot the Console
// draws. Nothing here stubs internal/autopilot - the point is that a Mate
// started through the fake Herdr really is digested to, and that the row the
// Console gets says so.

func TestConsolePilotDigestsToAStartedMate(t *testing.T) {
	w, deps := consoleFixture(t, "shop")
	res, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	rt := deps.Runtime.(*runtime.Fake)
	handle, _, err := spawn.MateHandle(context.Background(), w, deps, "shop")
	if err != nil {
		t.Fatalf("MateHandle: %v", err)
	}
	rt.SetReadOutput(handle, claudeEmptyScreen)

	if err := w.SetMode("shop", true); err != nil {
		t.Fatalf("SetAuto: %v", err)
	}
	if err := w.AppendStatus("shop", "k3", "needs-decision: pick A or B"); err != nil {
		t.Fatalf("AppendStatus: %v", err)
	}

	pilot := autopilot.New(w, autopilot.Deps{
		Outbox: outbox.New(w, outbox.Deps{
			Harnesses: harnesses,
			Runtime:   deps.Runtime,
			Handle:    consoleMateHandle(w, deps),
			Sleeper:   noSleep{},
		}),
		Sleeper: noSleep{},
	})
	if err := pilot.Tick(context.Background()); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if len(rt.SentText) != 1 || !strings.HasPrefix(rt.SentText[0].Text, send.Marker+"digest: 1 item(s)") {
		t.Fatalf("typed %+v into %s, want one marked digest", rt.SentText, res.Agent)
	}

	// And the snapshot the Console draws carries the daemon's own state.
	snap, err := query.Load(context.Background(), w, consoleHarnesses(), nil)
	if err != nil {
		t.Fatalf("query.Load: %v", err)
	}
	if got := snap.Projects[0].Daemon; got.Sends != 0 {
		t.Fatalf("query.Load filled Daemon = %+v; it reads files and cannot know this", got)
	}
	engine := core.New(core.Options{Workspace: w, Pilot: core.PilotLoop(pilot), Spawn: deps, Harnesses: consoleHarnesses()})
	defer engine.Stop()
	merged, err := engine.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("engine.Snapshot: %v", err)
	}
	daemon := merged.Projects[0].Daemon
	if daemon.Sends != 1 || daemon.LastSentAt.IsZero() {
		t.Fatalf("Daemon = %+v, want one send with a time", daemon)
	}
	if daemon.Notice != "" {
		t.Fatalf("Daemon.Notice = %q after a delivered digest", daemon.Notice)
	}
}

// noSleep runs the daemon's real settle and retry durations at memory speed.
type noSleep struct{}

func (noSleep) Sleep(ctx context.Context, _ time.Duration) error { return ctx.Err() }

// engineFixture is a workspace with one crew the fake Herdr shows at an
// empty composer, and deps for an engine over it. The live deps set no
// Sleep; the fixture's instant one would make the sender loop, which sleeps
// its interval through it, spin.
func engineFixture(t *testing.T) (*store.Workspace, spawn.Deps, watch.CrewRef) {
	t.Helper()
	w, deps := consoleFixture(t, "shop")
	res := spawnFakeCrew(t, w, deps, "shop", "k3")
	deps.Runtime.(*runtime.Fake).SetReadOutput(spawnedHandle(res), codexEmptyScreen)
	deps.Sleep = nil
	return w, deps, watch.CrewRef{Project: "shop", Crew: "k3"}
}

// startEngine runs the console's own parts under an engine, as runConsole
// does, until the test ends.
func startEngine(t *testing.T, w *store.Workspace, deps spawn.Deps) (*core.Engine, core.Options) {
	t.Helper()
	opts, err := engineOptions(w, deps)
	if err != nil {
		t.Fatalf("engineOptions: %v", err)
	}
	engine := core.New(opts)
	if err := engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(engine.Stop)
	return engine, opts
}

// engineOptions hands the engine every part the console ran itself before
// CON-207. Run over the fake Herdr, the observer reads the crew, recovery
// finishes, and Stop closes the timeline: the next writer can open it.
func TestEngineOptionsRunsTheConsoleLoops(t *testing.T) {
	w, deps, ref := engineFixture(t)
	engine, opts := startEngine(t, w, deps)
	if opts.Workspace != w || opts.Watcher == nil || opts.Timeline == nil || opts.Ingest == nil ||
		opts.Sender == nil || opts.Pilot == nil || opts.Recover == nil ||
		opts.Spawn.Runtime != deps.Runtime || len(opts.Harnesses.List) == 0 {
		t.Fatalf("engineOptions left a part out: %+v", opts)
	}
	// The console adds what serves the captain; engineOptions does not.
	if opts.Dashboard != nil || opts.Notifier != nil {
		t.Fatalf("engineOptions serves a dashboard (%v) or notifies (%v); only the console does", opts.Dashboard, opts.Notifier)
	}

	waitFor(t, func() bool {
		h, ok := engine.CrewHealth().Snapshot()[ref]
		return ok && h.AgentPresent
	})
	waitFor(t, func() bool { return engine.Recovery().Done })
	if engine.Ingest() != opts.Ingest {
		t.Error("the engine reads another ingest than the one the observer runs")
	}

	engine.Stop()
	handle, err := db.Open(w)
	if err != nil {
		t.Fatalf("opening mate.db after Stop: %v; the engine must release the writer's lock", err)
	}
	_ = handle.Close()
}

// Another process holds `.mate/mate.db` - `mate reindex`, say. The console
// observes all the same, records no timeline and says nothing about one
// (internal/db.ErrLocked); quitting leaves the other writer's database and
// lock alone.
func TestEngineObservesWithoutTheTimelineWhileAnotherProcessWritesIt(t *testing.T) {
	w, deps, ref := engineFixture(t)
	other, err := db.Open(w)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer other.Close()

	engine, opts := startEngine(t, w, deps)
	if opts.Timeline != nil || opts.Ingest != nil {
		t.Fatalf("engineOptions with mate.db held elsewhere: timeline %v, ingest %v; want neither", opts.Timeline, opts.Ingest)
	}
	waitFor(t, func() bool {
		_, ok := engine.CrewHealth().Snapshot()[ref]
		return ok
	})
	snap, err := engine.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("engine.Snapshot: %v", err)
	}
	if strings.Contains(snap.Runtime.Notice, "timeline") {
		t.Errorf("status line %q, want nothing about a timeline this console does not write", snap.Runtime.Notice)
	}

	engine.Stop()
	if handle, err := db.Open(w); !errors.Is(err, db.ErrLocked) {
		if handle != nil {
			_ = handle.Close()
		}
		t.Fatalf("a writer after Stop got %v, want ErrLocked: the other process still holds mate.db", err)
	}
	if err := other.SQL().Ping(); err != nil {
		t.Fatalf("the other writer's database after Stop: %v", err)
	}
}

// A Jev setting that asks for what it cannot have leaves no observer
// configured: runConsole puts the reason on the status line, and the engine
// runs on with the fixture observer reading the panes.
func TestEngineObservesWithTheFixtureWhenJevIsMisconfigured(t *testing.T) {
	w, deps, ref := engineFixture(t)
	writeEnv(t, w, "MATE_JEV=on\n")
	observer, err := configuredObserver(w)
	if err == nil || observer != nil {
		t.Fatalf("configuredObserver = %v, %v; want no observer and the reason", observer, err)
	}
	deps.Observer = observer

	engine, _ := startEngine(t, w, deps)
	waitFor(t, func() bool {
		h, ok := engine.CrewHealth().Snapshot()[ref]
		return ok && h.AgentPresent && h.Composer == send.StateEmpty
	})
}
