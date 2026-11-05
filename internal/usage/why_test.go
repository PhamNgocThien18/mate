package usage

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/telemetry"
	"github.com/nguyenngocanh94/mate/internal/timeline"
)

// seedSmallerCrews adds two more tasks to usageWorkspace's ledger, both
// smaller than k3's 97.2k: k1 at 10.5k and k2 at 50.5k.
func seedSmallerCrews(t *testing.T, w *store.Workspace) {
	t.Helper()
	handle, err := db.Open(w)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer handle.Close()
	seedActorTurnAndPricing(t, handle, timeline.CrewActorID("shop", "k1"), "shop", "crew", "unpriced-model", 10_000, 0, 0, 500, 200_000)
	seedActorTurnAndPricing(t, handle, timeline.CrewActorID("shop", "k2"), "shop", "crew", "unpriced-model", 50_000, 0, 0, 500, 200_000)
}

func TestUsageTopKeepsTheMateAndTheLargestTasksAndTotalsEverything(t *testing.T) {
	w := usageWorkspace(t)
	seedSmallerCrews(t, w)
	out := writePage(t, w, Request{Project: "shop", Top: 2})
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 8 {
		t.Fatalf("want header, mate, two tasks, total, the cut notice and the by-model lines; got %d line(s):\n%s", len(lines), out)
	}
	if !strings.HasPrefix(lines[1], "mate") || !strings.HasPrefix(lines[2], "k3") || !strings.HasPrefix(lines[3], "k2") {
		t.Fatalf("want the mate, then k3 and k2 largest first:\n%s", out)
	}
	if strings.Contains(out, "k1") {
		t.Fatalf("the smallest task must be cut:\n%s", out)
	}
	// 97.2k + 50.5k + 10.5k + the Mate's 500: the cut task still counts.
	if !strings.HasPrefix(lines[4], "total:") || !strings.Contains(lines[4], "158.7k total") {
		t.Fatalf("the total line must count every task, shown or not:\n%s", lines[4])
	}
	if !strings.Contains(lines[5], "2 largest of 3 task(s)") {
		t.Fatalf("a cut table must say what it left out:\n%s", lines[5])
	}
}

func TestUsageTopLargerThanTheLedgerChangesNothing(t *testing.T) {
	w := usageWorkspace(t)
	seedSmallerCrews(t, w)
	plain := writePage(t, w, Request{Project: "shop"})
	if top := writePage(t, w, Request{Project: "shop", Top: 10}); top != plain {
		t.Fatalf("--top above the task count must print the plain ledger:\n%s\n--- plain ---\n%s", top, plain)
	}
}

func TestUsageWhyExplainsOneTask(t *testing.T) {
	w := usageWorkspace(t)
	handle, err := db.Open(w)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	actor := timeline.CrewActorID("shop", "k3")
	end := time.Now().UTC()
	start := end.Add(-10 * time.Second)
	exit := 1
	fact := telemetry.Fact{Version: 1, ID: "failed-test", ExecutionID: "failed-test", Kind: "execution", SessionID: actor + "#s",
		Command: "go test ./cart", Tool: "exec_command", StartedAt: &start, CompletedAt: &end, Status: "failed", ExitCode: &exit,
		OccurredAt: end, ObservedAt: end, MeasurementKind: "native", SourcePath: "rollout.jsonl", SourceOffset: 7,
		Output: &telemetry.Output{Bytes: 9, SHA256: "failed-output", Preview: "FAIL cart"}}
	payload, err := json.Marshal(fact)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handle.SQL().Exec(`INSERT INTO event(dedup,project,at,actor_id,kind,payload,ref_path,ref_offset) VALUES(?,?,?,?,?,?,?,?)`,
		"why-test#failed-test", "shop", db.FormatTime(end), actor, "telemetry.execution", string(payload), fact.SourcePath, fact.SourceOffset); err != nil {
		handle.Close()
		t.Fatalf("seed telemetry: %v", err)
	}
	handle.Close()

	out := writePage(t, w, Request{Project: "shop", Crew: "k3", Why: true})
	for _, want := range []string{
		"usage why shop k3",
		// The ledger's own numbers, bucket by bucket; an unpriced model is "?".
		"tokens: 97.2k total = 90k in + 6k cache-read + 0 cache-write + 1.2k out · cost ?",
		"1 model call(s)",
		"tool elapsed 10s",
		"tool invocation sum 10s",
		"unallocated is not model latency",
		"? is unknown",
		"context peak 96k, last 96k",
		// The failed command is named with its exit code and the review hint
		// that says where a fix would live.
		"execution_failure",
		"go test ./cart · exit 1",
		"review: ",
		// Findings overlap, and the digest has to say so where they are read.
		"never add them up",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("--why output is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "TURN ") {
		t.Errorf("--why must replace the per-call table, not add to it:\n%s", out)
	}
}

func TestUsageWhyOnAnUnknownCrewNamesTheCrew(t *testing.T) {
	w := usageWorkspace(t)
	_, err := tryPage(w, Request{Project: "shop", Crew: "nope", Why: true})
	if err == nil || !strings.Contains(err.Error(), `"nope"`) {
		t.Fatalf("want an error naming the crew, got %v", err)
	}
}
