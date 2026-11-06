package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/core"
	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/watch"
)

// spawnedHandle is the live Herdr handle of a crew the fake adapter just
// spawned. Only the session and the agent name identify a pane to the fake,
// which is the same pair spawn.CrewHandle resolves out of the meta.
func spawnedHandle(res spawn.CrewResult) runtime.AgentHandle {
	return runtime.AgentHandle{
		Session: runtime.SessionHandle{Name: res.Session},
		Name:    res.Agent,
		RawID:   res.Crew,
		Kind:    res.Harness,
	}
}

// The console's observer wiring (mvp.md task 18): internal/watch over the
// real spawn.CrewHandle, and its readings put into the snapshot the Console
// draws. Nothing here stubs internal/watch - the point is that a crew
// spawned through the fake Herdr really is observed, and that the row the
// Console gets carries what was observed.

func TestConsoleWatcherObservesASpawnedCrew(t *testing.T) {
	t.Parallel()
	w, deps := consoleFixture(t, "shop")
	res := spawnFakeCrew(t, w, deps, "shop", "k3")
	rt := deps.Runtime.(*runtime.Fake)
	rt.SetReadOutput(spawnedHandle(res), codexEmptyScreen)

	watcher := watch.New(w, watch.Deps{
		Runtime: deps.Runtime,
		Handle:  consoleCrewHandle(w, deps),
	})
	if err := watcher.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	h, ok := watcher.Health("shop", "k3")
	if !ok {
		t.Fatal("the observer recorded no health for the spawned crew")
	}
	if !h.AgentPresent || h.Composer != send.StateEmpty {
		t.Fatalf("health = %+v, want a present agent with an empty composer", h)
	}

	// And the snapshot the Console draws, the engine's (internal/core),
	// carries it.
	snap, err := query.Load(context.Background(), w, consoleHarnesses(), nil)
	if err != nil {
		t.Fatalf("query.Load: %v", err)
	}
	if got := snap.Projects[0].Crews[0].Health; got.State != query.Absent {
		t.Fatalf("health straight out of query.Load = %+v, want Absent: query reads files only", got)
	}
	engine := testEngine(t, core.Options{Workspace: w, Watcher: core.WatchLoop(watcher), Spawn: deps, Harnesses: consoleHarnesses()})
	defer engine.Stop()
	snap, err = engine.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("engine.Snapshot: %v", err)
	}
	crew := snap.Projects[0].Crews[0]
	if !crew.Health.IsKnown() {
		t.Fatalf("crew health = %+v, want the observation filled in", crew.Health)
	}
	if !crew.Health.Value.AgentPresent || crew.Health.Value.Composer != query.ComposerEmpty {
		t.Fatalf("crew health = %+v, want a present agent with an empty composer", crew.Health.Value)
	}
}

func TestConsoleWatcherOpensRuntimeLostForAKilledAgent(t *testing.T) {
	t.Parallel()
	w, deps := consoleFixture(t, "shop")
	res := spawnFakeCrew(t, w, deps, "shop", "k3")
	rt := deps.Runtime.(*runtime.Fake)
	rt.SetReadOutput(spawnedHandle(res), codexEmptyScreen)

	watcher := watch.New(w, watch.Deps{Runtime: deps.Runtime, Handle: consoleCrewHandle(w, deps)})
	if err := watcher.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	rt.DropAgent(spawnedHandle(res))
	if err := watcher.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	entries, _, err := w.ReadIncidents("shop", 0)
	if err != nil {
		t.Fatalf("ReadIncidents: %v", err)
	}
	if len(entries) != 1 || entries[0].Kind != "runtime_lost" || !entries[0].Open() {
		t.Fatalf("incidents = %+v, want one open runtime_lost", entries)
	}

	// The box the Console draws reads the same file, so the crew's row is
	// `blocked` and the item is in the inbox (mvp.md section 4b).
	box := query.LoadBox(w, "shop")
	if !box.IsKnown() {
		t.Fatalf("box = %+v, want it readable", box)
	}
	if box.Value.ToResolve() != 1 {
		t.Fatalf("inbox = %+v, want the incident in it", box.Value.Inbox)
	}
	if item := box.Value.Inbox[0]; item.Kind != query.BoxIncident || item.Verb != "runtime_lost" || item.Crew != "k3" {
		t.Fatalf("inbox item = %+v, want the k3 runtime_lost incident", item)
	}
	if !strings.Contains(box.Value.Inbox[0].Resolve, "runtime_lost") {
		t.Fatalf("resolve line = %q, want it to name the incident kind", box.Value.Inbox[0].Resolve)
	}
}

// TestConsoleWatcherOpensItsOwnWorkspaceHandle: the observer polls on its own
// goroutine while query.Load re-reads workspace.yaml on the UI's, and a
// *store.Workspace caches that file. Sharing one handle would be a data race
// on the cache, so the wiring opens a second one over the same directory.
func TestConsoleWatcherOpensItsOwnWorkspaceHandle(t *testing.T) {
	t.Parallel()
	w, deps := consoleFixture(t, "shop")
	watcher, err := consoleWatcher(w.Root(), deps)
	if err != nil {
		t.Fatalf("consoleWatcher: %v", err)
	}
	if watcher == nil {
		t.Fatal("consoleWatcher returned no watcher")
	}
	// It sees the same workspace: a crew spawned through the console's
	// handle is observed through the watcher's.
	res := spawnFakeCrew(t, w, deps, "shop", "k3")
	deps.Runtime.(*runtime.Fake).SetReadOutput(spawnedHandle(res), codexEmptyScreen)
	if err := watcher.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if _, ok := watcher.Health("shop", "k3"); !ok {
		t.Fatal("the watcher's own workspace handle does not see the crew")
	}

	if _, err := consoleWatcher(t.TempDir(), deps); err == nil {
		t.Fatal("consoleWatcher accepted a directory that is not a workspace")
	}
}

// seedActorTurnAndPricing writes just enough of the schema by hand - an
// actor, one turn, and a pricing row with the context window filled in but
// no price yet - to exercise crewTokens/mateTokens without a transcript
// fixture. Tests of the SQL the ingest itself writes live in
// internal/timeline; this is the console wiring on top of it.
func seedActorTurnAndPricing(t *testing.T, handle *db.DB, actorID, project, kind, model string,
	input, cacheRead, cacheWrite, output int64, contextWindow int64) {
	t.Helper()
	now := db.FormatTime(time.Now())
	// name is a plausible actor.name for the kind: `v_task_ledger.crew`
	// (and `spawn.ListCrews`) key a crew by its short id, never by the
	// `crew:<project>:<crew>` actor id, so a test that used the actor id as
	// the name here would silently fail every lookup keyed by crew id.
	name := actorID
	if kind == "crew" {
		name = strings.TrimPrefix(actorID, "crew:"+project+":")
	}
	if _, err := handle.SQL().Exec(
		`INSERT INTO actor(id, project, kind, name) VALUES (?, ?, ?, ?)`,
		actorID, project, kind, name); err != nil {
		t.Fatalf("seed actor: %v", err)
	}
	if _, err := handle.SQL().Exec(
		`INSERT INTO session(id, actor_id, started_at) VALUES (?, ?, ?)`,
		actorID+"#s", actorID, now); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if _, err := handle.SQL().Exec(
		`INSERT INTO turn(id, actor_id, session_id, started_at, ended_at, model, input_tokens, cache_read_tokens, cache_write_tokens, output_tokens, context_tokens_after)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		actorID+"#t", actorID, actorID+"#s", now, now, model, input, cacheRead, cacheWrite, output, input+cacheRead+cacheWrite); err != nil {
		t.Fatalf("seed turn: %v", err)
	}
	if _, err := handle.SQL().Exec(
		`INSERT INTO pricing(model, context_window) VALUES (?, ?)
		 ON CONFLICT(model) DO UPDATE SET context_window = excluded.context_window`,
		model, contextWindow); err != nil {
		t.Fatalf("seed pricing: %v", err)
	}
	if kind == "crew" {
		if _, err := handle.SQL().Exec(
			`INSERT INTO task(crew_actor_id, project, spawned_at) VALUES (?, ?, ?)`,
			actorID, project, now); err != nil {
			t.Fatalf("seed task: %v", err)
		}
	}
}
