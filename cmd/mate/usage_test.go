package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/core"
	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/timeline"
)

// The pages `mate usage` prints are internal/usage's, and so are the tests
// of what they say (CON-168). These test what cmdUsage owns: reading the
// flags, refusing a wrong combination, and handing each form to the page
// it names.

// usageWorkspace builds a workspace with a Mate, one crew declared
// `working`, and a hand-seeded ledger: the crew's turns total
// 90000+6000+300+1200 = 97500 tokens on a model that is not yet priced. The
// Mate gets a smaller total on the same unpriced model.
func usageWorkspace(t *testing.T) *store.Workspace {
	t.Helper()
	w := liveCrewWorkspace(t, "shop")
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

// Each form of the command reaches its own page: the first line names it,
// and --top reaches the ledger as a cut (a second task makes one).
func TestUsageHandsEachFormToItsPage(t *testing.T) {
	w := usageWorkspace(t)
	handle, err := db.Open(w)
	if err != nil {
		t.Fatal(err)
	}
	seedActorTurnAndPricing(t, handle, timeline.CrewActorID("shop", "k1"), "shop", "crew", "unpriced-model", 10_000, 0, 0, 500, 200_000)
	handle.Close()
	for name, tc := range map[string]struct {
		args        []string
		first, also string
	}{
		"ledger":   {[]string{"usage", "shop"}, "ID ", "k1"},
		"top":      {[]string{"usage", "shop", "--top", "1"}, "ID ", "showing the mate and the 1 largest of 2 task(s)"},
		"turns":    {[]string{"usage", "shop", "k3"}, "TURN ", ""},
		"sessions": {[]string{"usage", "shop", "k3", "--sessions"}, "SESSION ", ""},
		"why":      {[]string{"usage", "shop", "k3", "--why"}, "usage why shop k3", ""},
	} {
		t.Run(name, func(t *testing.T) {
			out := runCLI(t, append(tc.args, "--workspace", w.Root())...)
			if !strings.HasPrefix(out, tc.first) || !strings.Contains(out, tc.also) {
				t.Fatalf("mate %s printed, want a first line starting %q and %q:\n%s", strings.Join(tc.args, " "), tc.first, tc.also, out)
			}
		})
	}
}

// A workspace whose console never ran has no mate.db: the command says so
// and prints nothing.
func TestUsageWithoutATimelineSaysHowToMakeOne(t *testing.T) {
	w := liveCrewWorkspace(t, "shop")
	var stdout, stderr strings.Builder
	err := testCLI().run([]string{"usage", "shop", "--workspace", w.Root()}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "no timeline at") || !strings.Contains(err.Error(), "mate reindex") {
		t.Fatalf("want the no-timeline error, got %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("a failed usage printed:\n%s", stdout.String())
	}
}

func TestUsageUnknownProjectIsAUsageError(t *testing.T) {
	w := usageWorkspace(t)
	var stdout, stderr strings.Builder
	err := testCLI().run([]string{"usage", "nope", "--workspace", w.Root()}, &stdout, &stderr)
	if err == nil {
		t.Fatal("usage on an unregistered project must fail")
	}
}

func TestUsageWhyAndTopRefuseTheWrongShape(t *testing.T) {
	w := usageWorkspace(t)
	for name, args := range map[string][]string{
		"why without a crew":      {"usage", "shop", "--why", "--workspace", w.Root()},
		"top with a crew":         {"usage", "shop", "k3", "--top", "2", "--workspace", w.Root()},
		"negative top":            {"usage", "shop", "--top", "-1", "--workspace", w.Root()},
		"sessions without a crew": {"usage", "shop", "--sessions", "--workspace", w.Root()},
		"sessions and why":        {"usage", "shop", "k3", "--sessions", "--why", "--workspace", w.Root()},
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr strings.Builder
			err := testCLI().run(args, &stdout, &stderr)
			var ue *usageError
			if !errors.As(err, &ue) {
				t.Fatalf("want a usage error, got %v", err)
			}
			if stdout.Len() != 0 {
				t.Fatalf("a refused command printed:\n%s", stdout.String())
			}
		})
	}
}

// The console keeps the Mate's absolute context when no model window is
// known. internal/usage's TestUsageKeepsAbsoluteContextWithoutAModelWindow
// is the ledger's half of what was one test before CON-168.
func TestConsoleKeepsAbsoluteContextWithoutAModelWindow(t *testing.T) {
	w := usageWorkspace(t)
	handle, err := db.Open(w)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	if _, err := handle.SQL().Exec(`DELETE FROM pricing`); err != nil {
		t.Fatal(err)
	}
	// The console reads the engine's snapshot (internal/core).
	engine := core.New(core.Options{Workspace: w, Harnesses: consoleHarnesses()})
	defer engine.Stop()
	snap, err := engine.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tokens := snap.Projects[0].Mate.Tokens
	if !tokens.IsKnown() || tokens.Value.ContextTokens == nil || *tokens.Value.ContextTokens != 400 || tokens.Value.ContextPct != nil {
		t.Fatalf("console lost absolute context: %+v", tokens)
	}
}
