package usage

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/catalog"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/timeline"
)

// newWorkspace, usageWorkspace and seedActorTurnAndPricing are cmd/mate's
// liveCrewWorkspace, usageWorkspace and seedActorTurnAndPricing, which the
// tests here ran on before the usage pages moved out of cmd/mate
// (CON-168); writePage stands where `runCLI(t, "usage", ...)` stood.

// newWorkspace builds a workspace with one registered git project, the way
// `mate init` and `mate project add` leave it.
func newWorkspace(t *testing.T, project string) *store.Workspace {
	t.Helper()
	reg := catalog.Default()
	var defaults store.Defaults
	if k, err := reg.Default(harness.RoleMate); err == nil {
		defaults.MateHarness = string(k)
	}
	if k, err := reg.Default(harness.RoleCrew); err == nil {
		defaults.CrewHarness = string(k)
	}
	w, err := store.Init(t.TempDir(), defaults)
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	repo := filepath.Join(w.ProjectHome(project), project)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "mate-test@example.com"},
		{"config", "user.name", "mate test"},
		{"commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := w.AddProject(project, store.ProjectConfig{Repos: []store.RepoConfig{{Path: repo, DefaultBranch: "main"}}}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	return w
}

// usageWorkspace builds a workspace with a Mate, one crew declared
// `working`, and a hand-seeded ledger: the crew's turns total
// 90000+6000+300+1200 = 97500 tokens on a model that is not yet priced. The
// Mate gets a smaller total on the same unpriced model.
func usageWorkspace(t *testing.T) *store.Workspace {
	t.Helper()
	w := newWorkspace(t, "shop")
	if err := w.WriteMateMeta("shop", map[string]string{"harness": "claude"}); err != nil {
		t.Fatalf("WriteMateMeta: %v", err)
	}
	if err := w.WriteCrewMeta("shop", "k3", map[string]string{
		"harness": "codex", "task": "add a Buy button", "state": "spawned",
	}); err != nil {
		t.Fatalf("WriteCrewMeta: %v", err)
	}
	if err := w.AppendStatus("shop", "k3", "working: writing the button"); err != nil {
		t.Fatalf("AppendStatus: %v", err)
	}

	handle, err := db.Open(w)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer handle.Close()

	crewActor := timeline.CrewActorID("shop", "k3")
	mateActor := timeline.MateActorID("shop")
	// seedActorTurnAndPricing writes the crew's `task` row itself (it must,
	// since `v_task_ledger` is a join against `task`); the Mate gets none,
	// which matches production - a Mate's turns belong to the project, not
	// to any one task (docs/timeline.md).
	seedActorTurnAndPricing(t, handle, crewActor, "shop", "crew", "unpriced-model", 90_000, 6_000, 0, 1_200, 200_000)
	seedActorTurnAndPricing(t, handle, mateActor, "shop", "mate", "unpriced-model", 400, 0, 0, 100, 200_000)
	return w
}

// seedActorTurnAndPricing writes just enough of the schema by hand - an
// actor, one turn, and a pricing row with the context window filled in but
// no price yet - to exercise the ledger without a transcript fixture. Tests
// of the SQL the ingest itself writes live in internal/timeline.
func seedActorTurnAndPricing(t *testing.T, handle *db.DB, actorID, project, kind, model string,
	input, cacheRead, cacheWrite, output int64, contextWindow int64) {
	t.Helper()
	now := db.FormatTime(time.Now())
	// name is a plausible actor.name for the kind: `v_task_ledger.crew`
	// (and `spawn.ListCrews`) key a crew by its short id, never by the
	// `crew:<project>:<crew>` actor id.
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

// writePage is what `mate usage` prints for req, over a read-only handle
// as cmdUsage opens it.
func writePage(t *testing.T, w *store.Workspace, req Request) string {
	t.Helper()
	out, err := tryPage(w, req)
	if err != nil {
		t.Fatalf("usage %+v: %v", req, err)
	}
	return out
}

func tryPage(w *store.Workspace, req Request) (string, error) {
	handle, err := db.OpenRead(w)
	if err != nil {
		return "", err
	}
	defer handle.Close()
	var out strings.Builder
	err = Write(context.Background(), &out, w, handle, Deps{Harnesses: catalog.Default()}, req)
	return out.String(), err
}
