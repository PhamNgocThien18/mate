package core

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// testHarnesses is a harness catalog for a snapshot: core knows no harness
// by name, cmd/mate hands it the binary's.
var testHarnesses = query.Harnesses{
	List:        []query.Harness{{Kind: "claude", Mate: true}, {Kind: "codex"}},
	MateDefault: "claude",
}

// shopWorkspace is a workspace with one project, shop, and no repo yet.
func shopWorkspace(t *testing.T) *store.Workspace {
	t.Helper()
	w, err := store.Init(t.TempDir(), store.Defaults{MateHarness: "claude", CrewHarness: "codex"})
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	if err := w.AddProject("shop", store.ProjectConfig{}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	return w
}

// writeFile writes text to path, as another process would.
func writeFile(path, text string) error { return os.WriteFile(path, []byte(text), 0o644) }

// withTimelineDB creates `.mate/mate.db` for w, as the observer does at a
// console's start, and returns the writer for a test to seed; the test's
// end closes it.
func withTimelineDB(t *testing.T, w *store.Workspace) *db.DB {
	t.Helper()
	handle, err := db.Open(w)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = handle.Close() })
	return handle
}

// seedActorTurnAndPricing writes just enough of the schema by hand - an
// actor, one turn, and a pricing row with the context window filled in but
// no price yet - to exercise crewTokens/mateTokens without a transcript
// fixture. Tests of the SQL the ingest itself writes live in
// internal/timeline; this is the snapshot on top of it. (cmd/mate keeps a
// copy for its `mate usage` tests.)
func seedActorTurnAndPricing(t *testing.T, handle *db.DB, actorID, project, kind, model string,
	input, cacheRead, cacheWrite, output int64, contextWindow int64) {
	t.Helper()
	now := db.FormatTime(time.Now())
	// name is a plausible actor.name for the kind: `v_task_ledger.crew`
	// keys a crew by its short id, never by the `crew:<project>:<crew>`
	// actor id.
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
