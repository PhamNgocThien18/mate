package learnings

import "testing"

// frictionLog is the shape mihuv2's log had on 2026-11-04: the format's
// example in a fence, one entry above `## Archive`, archive lines, and the
// entries crews appended below the archive.
const frictionLog = "# Harness friction log\n" +
	"\n" +
	"Format:\n" +
	"\n" +
	"```markdown\n" +
	"## YYYY-MM-DD — short title\n" +
	"Friction: what was hard.\n" +
	"Outcome: open | YYYY-MM-DD — what was observed.\n" +
	"```\n" +
	"\n" +
	"```markdown\n" +
	"- YYYY-MM-DD — short title → where the learning lives now.\n" +
	"```\n" +
	"\n" +
	"## 2026-11-03 — above the archive\n" +
	"Friction: one.\n" +
	"Outcome: open\n" +
	"\n" +
	"## Archive\n" +
	"- 2026-10-17 — archived one → verify.sh.\n" +
	"- 2026-10-20 — archived two → Makefile.\n" +
	"\n" +
	"---\n" +
	"\n" +
	"## 2026-10-20 — below the archive, open\n" +
	"Friction: two.\n" +
	"Outcome: open\n" +
	"\n" +
	"## 2026-10-20 — below the archive, closed\n" +
	"Friction: three.\n" +
	"Outcome: 2026-10-28 — recurred: review r1 timed out.\n" +
	"\n" +
	"## 2026-10-27 — still open with evidence\n" +
	"Friction: four.\n" +
	"Outcome: open — recurred once more; next check: the next hand-back.\n" +
	"\n" +
	"## 2026-11-03 — no outcome line yet\n" +
	"Friction: five.\n" +
	"Prediction: none yet.\n"

func TestCountFrictionCountsEntriesBelowTheArchive(t *testing.T) {
	got := CountFriction(frictionLog)
	want := Friction{Entries: 5, Open: 4, Lines: 39}
	if got != want {
		t.Fatalf("CountFriction = %+v, want %+v", got, want)
	}
}

func TestCountFrictionOfAnEmptyLog(t *testing.T) {
	if got := CountFriction("# Harness friction log\n"); got != (Friction{Lines: 1}) {
		t.Fatalf("CountFriction = %+v, want no entries", got)
	}
}

func TestFrictionOverEitherBudget(t *testing.T) {
	b := FrictionBudget{Open: 5, Lines: 120}
	for _, tc := range []struct {
		f    Friction
		over bool
	}{
		{Friction{Open: 5, Lines: 120}, false},
		{Friction{Open: 6, Lines: 40}, true},
		{Friction{Open: 1, Lines: 121}, true},
	} {
		if got := tc.f.Over(b); got != tc.over {
			t.Errorf("%+v.Over(%+v) = %v, want %v", tc.f, b, got, tc.over)
		}
	}
}

func TestParseFrictionBudgetReadsTheBudgetsTable(t *testing.T) {
	review := "## Budgets (defaults — kept at onboarding unless the human changes them)\n\n" +
		"| What | Budget | Why |\n" +
		"| --- | --- | --- |\n" +
		"| Open friction entries | ≤ 8 | more than this and the sweep is overdue |\n" +
		"| Live friction log | ≤ ~200 lines | archive lines cost one line each |\n" +
		"| One feature doc | ≤ 60 lines | the feature template's own budget |\n"
	if got := ParseFrictionBudget(review); got != (FrictionBudget{Open: 8, Lines: 200}) {
		t.Fatalf("ParseFrictionBudget = %+v, want 8 / 200", got)
	}
	if got := ParseFrictionBudget("# Harness review workflow\n"); got != DefaultFrictionBudget {
		t.Fatalf("ParseFrictionBudget of no table = %+v, want the default", got)
	}
}
