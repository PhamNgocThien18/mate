package usage

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/timeline"
)

func TestUsagePrintsTheLedgerWithMateFirstAndATotalsFooter(t *testing.T) {
	w := usageWorkspace(t)
	out := writePage(t, w, Request{Project: "shop"})

	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) < 4 {
		t.Fatalf("usage printed %d line(s):\n%s", len(lines), out)
	}
	header := lines[0]
	for _, col := range []string{"ID", "STATE", "MODEL", "EFFORT", "TURNS", "IN", "CACHE-READ", "CACHE-WRITE", "OUT", "TOTAL", "COST", "CTX", "CTX-AVG", "CTX%", "ASKED", "WAITED"} {
		if !strings.Contains(header, col) {
			t.Fatalf("header %q is missing column %q", header, col)
		}
	}
	if !strings.Contains(lines[1], "mate") {
		t.Fatalf("mate row is not first:\n%s", out)
	}
	if !strings.Contains(lines[2], "k3") || !strings.Contains(lines[2], "working") {
		t.Fatalf("crew row missing or wrong state:\n%s", out)
	}
	// The model is unpriced: every cost cell, including the footer's, reads
	// "?" rather than a fabricated $0.00 (mvp.md M5: a missing price is not
	// a price of zero).
	if !strings.Contains(lines[1], "?") || !strings.Contains(lines[2], "?") {
		t.Fatalf("an unpriced model must show cost as ?:\n%s", out)
	}
	footer := lines[3]
	if !strings.Contains(footer, "total:") || !strings.Contains(footer, "? cost") {
		t.Fatalf("footer = %q, want a totals line with ? cost", footer)
	}
	// Numbers humanised: 90000+6000+0+1200 = 97200 -> 97.2k.
	if !strings.Contains(lines[2], "97.2k") {
		t.Fatalf("crew TOTAL is not humanised:\n%s", lines[2])
	}
}

func TestUsageShowsARealCostOnceTheModelIsPriced(t *testing.T) {
	w := usageWorkspace(t)
	if err := w.SavePricing(store.PricingConfig{Models: []store.PricingModel{
		{Model: "unpriced-model", InputPerM: 10, OutputPerM: 20, ContextWindow: 200_000},
	}}); err != nil {
		t.Fatalf("SavePricing: %v", err)
	}
	// Reload the price into the already-seeded pricing row: this test seeds
	// the ledger by hand rather than through the ingest, so it upserts the
	// price the same way `timeline.Ingester.ingestPricing` would.
	handle, err := db.Open(w)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if _, err := handle.SQL().Exec(
		`UPDATE pricing SET input_per_m = 10, output_per_m = 20 WHERE model = 'unpriced-model'`); err != nil {
		handle.Close()
		t.Fatalf("price the model: %v", err)
	}
	handle.Close()

	out := writePage(t, w, Request{Project: "shop"})
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	// Crew: 90000 input @ $10/M + 1200 output @ $20/M = 0.90 + 0.024 = $0.924.
	if !strings.Contains(lines[2], "$0.92") {
		t.Fatalf("crew row does not show the priced cost:\n%s", lines[2])
	}
	footer := lines[len(lines)-1]
	if strings.Contains(footer, "? cost") {
		t.Fatalf("footer still says unpriced once the model is priced:\n%s", footer)
	}
}

func TestUsageWithACrewArgumentPrintsPerTurnRows(t *testing.T) {
	w := usageWorkspace(t)
	out := writePage(t, w, Request{Project: "shop", Crew: "k3"})
	if !strings.Contains(out, "TURN") || !strings.Contains(out, "MODEL") || !strings.Contains(out, "unpriced-model") {
		t.Fatalf("per-turn output missing expected columns/model:\n%s", out)
	}
	if strings.Contains(out, "mate") {
		t.Fatalf("per-turn output for k3 must not include the mate's turns:\n%s", out)
	}
}

// The Mate's row keeps its absolute context when no model window is known.
// cmd/mate's TestConsoleKeepsAbsoluteContextWithoutAModelWindow is the
// console's half of what was one test before CON-168.
func TestUsageKeepsAbsoluteContextWithoutAModelWindow(t *testing.T) {
	w := usageWorkspace(t)
	handle, err := db.Open(w)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	if _, err := handle.SQL().Exec(`DELETE FROM pricing`); err != nil {
		t.Fatal(err)
	}
	row, err := mateLedgerRow(context.Background(), handle, w, "shop")
	if err != nil {
		t.Fatal(err)
	}
	if !row.Context.Valid || row.Context.Int64 != 400 || row.CtxPct.Valid {
		t.Fatalf("absolute context requires no pricing: %+v", row)
	}
	var out strings.Builder
	printLedgerTable(&out, []ledgerRow{row})
	if !strings.Contains(out.String(), "CACHE-READ") || !strings.Contains(out.String(), "CACHE-WRITE") || !strings.Contains(out.String(), "CTX") {
		t.Fatalf("usage must distinguish cache buckets and context:\n%s", out.String())
	}
}

// Below the totals, one line per model gives calls per task and context
// per call over every task, and a Claude Crew whose context passed the
// workspace's crew autocompact threshold is named (CON-68). A Codex Crew
// above it is not: the threshold is Claude's flag.
func TestUsagePrintsCallsPerTaskAndWarnsAboveTheCompactThreshold(t *testing.T) {
	w := usageWorkspace(t)
	for crew, h := range map[string]string{"big": "claude", "wide": "codex"} {
		if err := w.WriteCrewMeta("shop", crew, map[string]string{"harness": h, "task": crew, "state": "spawned"}); err != nil {
			t.Fatal(err)
		}
	}
	handle, err := db.Open(w)
	if err != nil {
		t.Fatal(err)
	}
	seedActorTurnAndPricing(t, handle, timeline.CrewActorID("shop", "big"), "shop", "crew", "claude-opus-5-5", 1_000, 250_000, 0, 500, 1_000_000)
	seedActorTurnAndPricing(t, handle, timeline.CrewActorID("shop", "wide"), "shop", "crew", "gpt-6-luna", 1_000, 200_000, 0, 500, 272_000)
	handle.Close()

	out := writePage(t, w, Request{Project: "shop"})
	for _, want := range []string{
		"by model and effort (each task's last call):",
		"  claude-opus-5-5: 1 task(s), 1.0 calls/task, 251k context/call",
		"  unpriced-model: 1 task(s), 1.0 calls/task, 96k context/call",
		"warning: 1 crew(s) passed the 200k crew autocompact threshold (crew.autocompact in .mate/workspace.yaml): big 251k\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("usage output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "wide 201k") {
		t.Errorf("a Codex Crew was warned about Claude's threshold:\n%s", out)
	}
}

// Context per call is a mean over the calls that recorded a context: a task
// whose other calls recorded none must not weigh its mean by them.
func TestUsageContextPerCallCountsOnlyCallsThatRecordedOne(t *testing.T) {
	w := usageWorkspace(t)
	for _, crew := range []string{"lean", "full"} {
		if err := w.WriteCrewMeta("shop", crew, map[string]string{"harness": "claude", "task": crew, "state": "spawned"}); err != nil {
			t.Fatal(err)
		}
	}
	handle, err := db.Open(w)
	if err != nil {
		t.Fatal(err)
	}
	seedActorTurnAndPricing(t, handle, timeline.CrewActorID("shop", "lean"), "shop", "crew", "claude-sonnet-5-5", 0, 100_000, 0, 0, 1_000_000)
	seedActorTurnAndPricing(t, handle, timeline.CrewActorID("shop", "full"), "shop", "crew", "claude-sonnet-5-5", 0, 300_000, 0, 0, 1_000_000)
	now := db.FormatTime(time.Now())
	for i := range 3 {
		a := timeline.CrewActorID("shop", "lean")
		if _, err := handle.SQL().Exec(
			`INSERT INTO turn(id, actor_id, session_id, started_at, ended_at, model, input_tokens, cache_read_tokens, cache_write_tokens, output_tokens, context_tokens_after)
			 VALUES (?, ?, ?, ?, ?, 'claude-sonnet-5-5', 0, 0, 0, 10, 0)`,
			fmt.Sprintf("%s#t%d", a, i), a, a+"#s", now, now); err != nil {
			t.Fatal(err)
		}
	}
	handle.Close()

	out := writePage(t, w, Request{Project: "shop"})
	if want := "  claude-sonnet-5-5: 2 task(s), 2.5 calls/task, 200k context/call"; !strings.Contains(out, want) {
		t.Errorf("usage output lacks %q:\n%s", want, out)
	}
}

// The ledger names each row's model and effort (CON-75): the last call's
// when the turn recorded them, else the launch profile in `.meta` - which
// is where a Claude crew's effort always comes from, since its transcript
// never says. The per-model line keys on both, so Opus high and Sonnet
// medium are two lines.
func TestUsageShowsModelAndEffortFromTheTurnOrTheLaunchProfile(t *testing.T) {
	w := usageWorkspace(t)
	if err := w.UpdateCrewMeta("shop", "k3", map[string]string{"model": "claude-opus-5-5", "effort": "high"}); err != nil {
		t.Fatal(err)
	}
	out := writePage(t, w, Request{Project: "shop"})
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	fields := strings.Fields(lines[2])
	if fields[0] != "k3" || fields[2] != "unpriced-model" || fields[3] != "high" {
		t.Fatalf("crew row = %q, want the turn's model and the meta's effort", lines[2])
	}
	if !strings.Contains(out, "unpriced-model high: 1 task(s)") {
		t.Fatalf("the per-model line does not key on effort:\n%s", out)
	}
	turns := writePage(t, w, Request{Project: "shop", Crew: "k3"})
	if !strings.Contains(strings.Split(turns, "\n")[0], "EFFORT") {
		t.Fatalf("per-turn header lacks EFFORT:\n%s", turns)
	}
}

// A task's reviewers roll into it (CON-75): TASK-TOTAL is the task plus
// every reviewer mate review started for it, REVIEWS counts them, and a
// reviewer row says which task it reviewed. --top ranks by TASK-TOTAL and
// keeps each shown task's reviewers under it; the footer still counts
// every row once.
func TestUsageRollsReviewersIntoTheirTask(t *testing.T) {
	w := usageWorkspace(t)
	for id, meta := range map[string]map[string]string{
		"rev": {"harness": "claude", "task": "Review k3 at abc", "state": "spawned", "budget": "3000000"},
		"big": {"harness": "claude", "task": "unrelated", "state": "spawned", "budget": "400000"},
		"old": {"harness": "claude", "task": "Review k3 at 000", "state": "finished"},
	} {
		if err := w.WriteCrewMeta("shop", id, meta); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.AddCrewReview("shop", "k3", "rev"); err != nil {
		t.Fatal(err)
	}
	// A reviewer from before reviews= existed links through its review.meta.
	if err := store.WriteMeta(w.CrewReviewMeta("shop", "old"), map[string]string{"crew": "k3", "head": "000"}); err != nil {
		t.Fatal(err)
	}
	handle, err := db.Open(w)
	if err != nil {
		t.Fatal(err)
	}
	seedActorTurnAndPricing(t, handle, timeline.CrewActorID("shop", "rev"), "shop", "crew", "unpriced-model", 10_000, 0, 0, 1_000, 200_000)
	seedActorTurnAndPricing(t, handle, timeline.CrewActorID("shop", "old"), "shop", "crew", "unpriced-model", 2_000, 0, 0, 0, 200_000)
	seedActorTurnAndPricing(t, handle, timeline.CrewActorID("shop", "big"), "shop", "crew", "unpriced-model", 100_000, 0, 0, 0, 200_000)
	handle.Close()

	out := writePage(t, w, Request{Project: "shop"})
	row := func(id string) []string {
		t.Helper()
		for _, line := range strings.Split(out, "\n") {
			if f := strings.Fields(line); len(f) > 0 && f[0] == id {
				return f
			}
		}
		t.Fatalf("no row for %s:\n%s", id, out)
		return nil
	}
	// k3: 97.2k own + rev 11k + old 2k = 110.2k with 2 reviews.
	// Columns 11 and 12 are BUDGET and % (CON-94): none of these crews has one.
	if f := row("k3"); f[9] != "97.2k" || f[10] != "110.2k" || f[11] != "-" || f[12] != "-" || f[13] != "2" {
		t.Fatalf("k3 row = %v, want TOTAL 97.2k, TASK-TOTAL 110.2k, no BUDGET, REVIEWS 2", f)
	}
	if f := row("rev"); f[10] != "-" || f[13] != "for" || f[14] != "k3" {
		t.Fatalf("rev row = %v, want no TASK-TOTAL and 'for k3'", f)
	}
	if f := row("big"); f[10] != "100k" || f[11] != "400k" || f[12] != "25%" || f[13] != "-" {
		t.Fatalf("big row = %v, want TASK-TOTAL equal to its own total, 25%% of its 400k BUDGET, and no reviews", f)
	}
	// A reviewer's own budget= is not a pocket of its own: its tokens are
	// in its task's TASK-TOTAL, and its row shows no BUDGET.
	if f := row("rev"); f[11] != "-" || f[12] != "-" {
		t.Fatalf("rev row = %v, want no BUDGET of its own", f)
	}
	if !strings.Contains(out, "total: 5 turn(s)") {
		t.Fatalf("footer does not count every row once:\n%s", out)
	}

	top := writePage(t, w, Request{Project: "shop", Top: 1})
	var ids []string
	for _, line := range strings.Split(top, "\n")[1:] {
		if f := strings.Fields(line); len(f) > 0 && !strings.HasPrefix(line, "total") && !strings.HasPrefix(line, "showing") && !strings.HasPrefix(line, "by ") && !strings.HasPrefix(line, " ") {
			ids = append(ids, f[0])
		}
	}
	if len(ids) != 4 || ids[0] != "mate" || ids[1] != "k3" {
		t.Fatalf("--top 1 rows = %v, want the mate, then the largest task by TASK-TOTAL", ids)
	}
	reviewers := append([]string(nil), ids[2:]...)
	sort.Strings(reviewers)
	if strings.Join(reviewers, ",") != "old,rev" {
		t.Fatalf("--top 1 rows = %v, want k3's two reviewers under it", ids)
	}
	if !strings.Contains(top, "largest of 2 task(s) by TASK-TOTAL") {
		t.Fatalf("--top note:\n%s", top)
	}
}

// `mate usage <project> <crew> --sessions` is one row per session of the
// task (CON-73), oldest first, with the session's calls and tokens.
func TestUsageSessionsSplitsACrewsTokensBySession(t *testing.T) {
	w := usageWorkspace(t)
	handle, err := db.Open(w)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	crewActor := timeline.CrewActorID("shop", "k3")
	later := db.FormatTime(time.Now().Add(time.Hour))
	for _, stmt := range []string{
		`UPDATE session SET crew_session = 1 WHERE id = '` + crewActor + `#s'`,
		`INSERT INTO session(id, actor_id, started_at, crew_session) VALUES ('` + crewActor + `#s2', '` + crewActor + `', '` + later + `', 2)`,
		`INSERT INTO turn(id, actor_id, session_id, started_at, ended_at, model, input_tokens, cache_read_tokens, cache_write_tokens, output_tokens, context_tokens_after)
		 VALUES ('` + crewActor + `#t2', '` + crewActor + `', '` + crewActor + `#s2', '` + later + `', '` + later + `', 'unpriced-model', 1000, 500, 0, 100, 1500)`,
	} {
		if _, err := handle.SQL().Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	handle.Close()

	out := writePage(t, w, Request{Project: "shop", Crew: "k3", Sessions: true})
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("usage --sessions printed %d line(s), want header, two sessions and a total:\n%s", len(lines), out)
	}
	for _, col := range []string{"SESSION", "TURNS", "IN", "CACHED", "OUT", "TOTAL", "CTX-MAX", "STARTED", "ENDED"} {
		if !strings.Contains(lines[0], col) {
			t.Fatalf("header %q lacks %s", lines[0], col)
		}
	}
	if f := strings.Fields(lines[1]); len(f) < 7 || f[0] != "1" || f[1] != "1" || f[5] != "97.2k" || f[6] != "96k" {
		t.Fatalf("session 1 row = %q, want ordinal 1, one turn, 97.2k total, 96k context", lines[1])
	}
	if f := strings.Fields(lines[2]); len(f) < 7 || f[0] != "2" || f[1] != "1" || f[5] != "1.6k" {
		t.Fatalf("session 2 row = %q, want ordinal 2, one turn, 1.6k total", lines[2])
	}
	if lines[3] != "total: 2 session(s), 98.8k tokens" {
		t.Fatalf("total line = %q", lines[3])
	}
}
