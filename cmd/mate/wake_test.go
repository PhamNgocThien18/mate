package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/box"
	"github.com/nguyenngocanh94/mate/internal/crewstate"
	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// wakeFixture is a project with a ship that handed back on a branch one
// commit ahead (k3), its reviewer, a Crew asking a question, and one still
// working.
func wakeFixture(t *testing.T) *store.Workspace {
	t.Helper()
	f := newDiffFixture(t)
	w := f.w
	commitInWorktree(t, f.worktree, "README.md", "shop\nmore\n", "add a line")
	if err := os.MkdirAll(w.CrewDir("shop", "k3"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.CrewHandback("shop", "k3"), []byte("## For the next crew\nREADME now has two lines.\nThe build is `make`.\n\n| criterion | pass/fail | command | output |\n| --- | --- | --- | --- |\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendStatus("shop", "k3", "working: adding the line"); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendStatus("shop", "k3", "wait-mate: ready in branch mate/k3"); err != nil {
		t.Fatal(err)
	}
	for crew, meta := range map[string]map[string]string{
		"rev-k3":   {spawn.MetaHarness: "claude", spawn.MetaModel: "sonnet", spawn.MetaEffort: "medium", spawn.MetaKind: "scout", spawn.MetaState: spawn.CrewStateSpawned, spawn.MetaTask: "Review k3"},
		"ask-k4":   {spawn.MetaHarness: "codex", spawn.MetaState: spawn.CrewStateSpawned, spawn.MetaTask: "ask"},
		"busy-k5":  {spawn.MetaHarness: "codex", spawn.MetaState: spawn.CrewStateSpawned, spawn.MetaTask: "busy"},
		"done-old": {spawn.MetaHarness: "codex", spawn.MetaState: "finished", spawn.MetaTask: "old"},
	} {
		if err := w.WriteCrewMeta("shop", crew, meta); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(w.CrewDir("shop", "rev-k3"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteMeta(w.CrewReviewMeta("shop", "rev-k3"), map[string]string{"crew": "k3"}); err != nil {
		t.Fatal(err)
	}
	for crew, line := range map[string]string{
		"rev-k3":   "working: reading the diff",
		"ask-k4":   "needs-decision: keep the old endpoint, or remove it now that nothing calls it? Removing it breaks the mobile app v1.2.",
		"busy-k5":  "working: running the test suite",
		"done-old": "wait-mate: long done",
	} {
		if err := w.AppendStatus("shop", crew, line); err != nil {
			t.Fatal(err)
		}
	}
	return w
}

func runWake(t *testing.T, w *store.Workspace) string {
	t.Helper()
	return runWakeAdvancing(t, w, true)
}

func runWakeAdvancing(t *testing.T, w *store.Workspace, advance bool) string {
	t.Helper()
	text, err := wake(context.Background(), w, gitx.New(), "shop", time.Date(2026, 10, 14, 22, 0, 0, 0, time.UTC), advance)
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	return text
}

// One wake holds what the Mate would otherwise learn with tail, cat, peek
// and crew list: the question in full before the hand-back, the hand-back's
// branch, its section for the next crew and its review, then the new status
// lines and every open Crew. A closed Crew is in none of it.
func TestWakePrintsWhatToActOnInOrder(t *testing.T) {
	text := runWake(t, wakeFixture(t))
	for _, want := range []string{
		"== 1. To act on (2) ==\n",
		"needs-decision ask-k4: keep the old endpoint, or remove it now that nothing calls it? Removing it breaks the mobile app v1.2.\n",
		"wait-mate k3: ready in branch mate/k3\n",
		"  branch mate/k3: 1 commit(s) beyond main; 1 file changed, 1 insertion(+)\n",
		"(6 lines), ## For the next crew:\n    README now has two lines.\n    The build is `make`.\n  acceptance table:\n    | criterion | pass/fail | command | output |\n    | --- | --- | --- | --- |\n  ## Deviations from Build: missing\n  ## Still open: missing\n  reviews:",
		"  reviews: rev-k3 pending\n",
		"== 2. New status since your last wake (4) ==\n",
		"busy-k5 working: running the test suite\n",
		"== 3. Open crews (4) ==\n",
		"rev-k3 working · claude sonnet/medium · reading the diff\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("wake lacks %q:\n%s", want, text)
		}
	}
	if strings.Index(text, "needs-decision ask-k4") > strings.Index(text, "wait-mate k3") {
		t.Errorf("a question must come before a hand-back:\n%s", text)
	}
	if strings.Contains(text, "done-old") {
		t.Errorf("a closed Crew was shown:\n%s", text)
	}
}

// A budget incident is printed with the tokens used and the threshold it
// was filed at (CON-76): "budget 50%" for the half one, with what to do
// next, and "over budget" for the full one with the Mate's three choices
// (CON-94); both after the hand-backs.
func TestWakePrintsBudgetIncidentsWithTheirThreshold(t *testing.T) {
	w := wakeFixture(t)
	for crew, text := range map[string]string{
		"busy-k5": box.BudgetHalfPrefix + "1.1M tokens of 2M tokens, no hand-back yet",
		"ask-k4":  "2.3M tokens of 2M tokens",
	} {
		if err := w.AppendIncident("shop", store.IncidentEntry{
			Time: time.Date(2026, 10, 14, 21, 0, 0, 0, time.UTC), Crew: crew,
			Kind: string(box.IncidentBudget), State: store.IncidentOpen, Text: text,
		}); err != nil {
			t.Fatal(err)
		}
	}
	text := runWake(t, w)
	for _, want := range []string{
		"== 1. To act on (4) ==\n",
		"budget 50% busy-k5: 1.1M tokens of 2M tokens, no hand-back yet\n  next: mate peek busy-k5 once; if it is looping or polling, send it one line\n",
		"over budget ask-k4: 2.3M tokens of 2M tokens\n  next: skill wake, Budget; the app told the Crew to commit, hand back and stop - at its hand-back extend it once (mate crew budget shop ask-k4 --extend <tokens> --reason \"<why>\"), narrow it, or drop it\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("wake lacks %q:\n%s", want, text)
		}
	}
	if strings.Index(text, "budget 50% busy-k5") < strings.Index(text, "wait-mate k3") {
		t.Errorf("a budget item must come after the hand-backs:\n%s", text)
	}
}

// The hard ceiling (CON-94) reads as the crew the app stopped, not as one
// more over-budget line.
func TestWakePrintsTheBudgetHardCeiling(t *testing.T) {
	w := wakeFixture(t)
	if err := w.AppendIncident("shop", store.IncidentEntry{
		Time: time.Date(2026, 10, 14, 21, 0, 0, 0, time.UTC), Crew: "busy-k5",
		Kind: string(box.IncidentBudget), State: store.IncidentOpen,
		Text: box.BudgetHardPrefix + "4.1M tokens of 2M tokens, over twice the budget; the app stops the crew",
	}); err != nil {
		t.Fatal(err)
	}
	text := runWake(t, w)
	want := "budget hard ceiling busy-k5: 4.1M tokens of 2M tokens, over twice the budget; the app stops the crew\n  next: skill wake, Budget; the app stopped the Crew"
	if !strings.Contains(text, want) {
		t.Fatalf("wake lacks %q:\n%s", want, text)
	}
	if strings.Contains(text, "over budget busy-k5") {
		t.Fatalf("the hard ceiling was printed as a plain over-budget item:\n%s", text)
	}
}

// Part 2 shows only what is new since the last wake; part 1 keeps an item
// until it is dealt with, however many wakes have shown it.
func TestWakeShowsOnlyNewStatusButKeepsOpenItems(t *testing.T) {
	w := wakeFixture(t)
	runWake(t, w)
	if err := w.AppendStatus("shop", "busy-k5", "working: fixing a failing test"); err != nil {
		t.Fatal(err)
	}
	text := runWake(t, w)
	if !strings.Contains(text, "== 2. New status since your last wake (1) ==\nbusy-k5 working: fixing a failing test\n") {
		t.Errorf("second wake did not show exactly the new line:\n%s", text)
	}
	if !strings.Contains(text, "== 1. To act on (2) ==") {
		t.Errorf("open items vanished after one wake:\n%s", text)
	}
}

// A --peek shows the same wake and leaves part 2's lines for the Mate's
// next one.
func TestWakePeekLeavesTheCursor(t *testing.T) {
	w := wakeFixture(t)
	peeked := runWakeAdvancing(t, w, false)
	if !strings.Contains(peeked, "== 2. New status since your last wake (4) ==") {
		t.Fatalf("peek did not show the new lines:\n%s", peeked)
	}
	if text := runWake(t, w); text != peeked {
		t.Errorf("the wake after a peek differs from it:\n%s\n--- peek ---\n%s", text, peeked)
	}
}

// A review Crew's hand-back is its verdict, and the reviewed Crew's line
// says the same.
func TestWakeShowsAReviewsVerdict(t *testing.T) {
	w := wakeFixture(t)
	if err := os.WriteFile(w.CrewReport("shop", "rev-k3"), []byte("## Summary\nok\n## Verdict\npass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendStatus("shop", "rev-k3", "wait-mate: review done"); err != nil {
		t.Fatal(err)
	}
	text := runWake(t, w)
	for _, want := range []string{
		"wait-mate rev-k3: review done\n  review of k3: verdict pass",
		"  reviews: rev-k3 pass\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("wake lacks %q:\n%s", want, text)
		}
	}
}

func TestMarkdownSectionStopsAtTheNextHeadingAndBounds(t *testing.T) {
	text := "# T\n## Summary\nx\n## For the next crew\na\nb\nc\n## Other\nz\n"
	if got := markdownSection("## For the next crew\na\n\n| c | d |\n", "For the next crew", 10); got != "a" {
		t.Errorf("section before a table = %q", got)
	}
	if got := markdownSection(text, "For the next crew", 10); got != "a\nb\nc" {
		t.Errorf("section = %q", got)
	}
	if got := markdownSection(text, "For the next crew", 2); got != "a\nb\n(+1 more lines in the section)" {
		t.Errorf("bounded section = %q", got)
	}
	if got := markdownSection(text, "Missing", 10); got != "" {
		t.Errorf("missing section = %q", got)
	}
}

// A `no_turn` incident (CON-74) is listed with the blocked items, before
// the questions, with the one next step: peek, and relaunch if the pane
// never got going.
func TestWakePrintsANoTurnIncidentWithTheBlocked(t *testing.T) {
	w := wakeFixture(t)
	if err := w.AppendIncident("shop", store.IncidentEntry{
		Time: time.Date(2026, 10, 14, 21, 54, 0, 0, time.UTC), Crew: "busy-k5",
		Kind: string(box.IncidentNoTurn), State: store.IncidentOpen, Text: "no turn in the ledger 6m0s after launch",
	}); err != nil {
		t.Fatal(err)
	}
	text := runWake(t, w)
	want := "no turn busy-k5: no turn in the ledger 6m0s after launch, open for 6m\n  next: mate peek busy-k5; if the pane is empty or at a sign-in screen, mate crew relaunch busy-k5\n"
	if !strings.Contains(text, want) {
		t.Fatalf("wake lacks %q:\n%s", want, text)
	}
	if strings.Index(text, "no turn busy-k5") > strings.Index(text, "needs-decision ask-k4") {
		t.Errorf("a no-turn item must come with the blocked ones, before the questions:\n%s", text)
	}
	if !strings.Contains(text, "busy-k5 working · codex · running the test suite\n") {
		t.Errorf("a no-turn item changed the crew's state:\n%s", text)
	}
}

func writeLearning(t *testing.T, w *store.Workspace, crew, body string) {
	t.Helper()
	path := w.CrewLearning("shop", crew)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Under a hand-back the wake says whether the Mate's learning record covers
// the crew's revision (CON-129): the check is in the tool output, not only
// in a skill. A record that names an older revision counts as missing for
// this one.
func TestWakeSaysWhetherLearningIsRecorded(t *testing.T) {
	w := wakeFixture(t)
	head := strings.TrimSpace(gitOutput(t, w.WorktreeDir("shop", "k3"), "rev-parse", "--short=7", "HEAD"))
	path := w.CrewLearning("shop", "k3")
	for _, step := range []struct{ record, want string }{
		{"", "  learning: none recorded; next: skill task-learning (writes " + path + "), then review-delivery's next step\n"},
		{"# k3\nSource: mate/k3 @ 0000000\nLearning: none\n", "  learning: recorded, but not for " + head + " (" + path + "); next: skill task-learning for this revision, then review-delivery's next step\n"},
		{"# k3\nSource: mate/k3 @ " + head + "\nLearning: captured\n", "  learning: recorded for " + head + " (" + path + "); next: review-delivery's next step\n"},
	} {
		if step.record != "" {
			writeLearning(t, w, "k3", step.record)
		}
		text := runWake(t, w)
		if !strings.Contains(text, step.want) {
			t.Errorf("wake lacks %q:\n%s", step.want, text)
		}
		if strings.Contains(text, "wait-mate rev-k3") && strings.Contains(text, "learning: none recorded; next: skill task-learning (writes "+w.CrewLearning("shop", "rev-k3")) {
			t.Errorf("a review crew's hand-back got a learning line:\n%s", text)
		}
	}
}

// A crew whose pull request merged while it is still open is a part-1 item
// until `crew stop` closes it, with the learning check made against the
// merge commit rather than the branch head (a squash merge is not on the
// branch at all).
func TestWakeListsAMergedCrewStillOpen(t *testing.T) {
	w := wakeFixture(t)
	meta, err := w.ReadCrewMeta("shop", "k3")
	if err != nil {
		t.Fatal(err)
	}
	meta[crewstate.MetaPRURL] = "https://github.com/x/shop/pull/9"
	meta[crewstate.MetaPRState] = crewstate.PRStateMerged
	meta[crewstate.MetaMergeCommit] = "abcdef0123456789abcdef0123456789abcdef01"
	if err := w.WriteCrewMeta("shop", "k3", meta); err != nil {
		t.Fatal(err)
	}
	path := w.CrewLearning("shop", "k3")
	text := runWake(t, w)
	for _, want := range []string{
		"== 1. To act on (3) ==\n",
		"landed k3: pull request merged (https://github.com/x/shop/pull/9), crew still open\n  learning: none recorded; next: skill task-learning (writes " + path + "), then mate crew stop shop k3\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("wake lacks %q:\n%s", want, text)
		}
	}
	writeLearning(t, w, "k3", "Source: PR #9 @ abcdef0, review none\nLearning: none\n")
	text = runWake(t, w)
	if want := "  learning: recorded for abcdef0 (" + path + "); next: mate crew stop shop k3\n"; !strings.Contains(text, want) {
		t.Errorf("wake lacks %q:\n%s", want, text)
	}
	meta[spawn.MetaState] = "finished"
	if err := w.WriteCrewMeta("shop", "k3", meta); err != nil {
		t.Fatal(err)
	}
	if text := runWake(t, w); strings.Contains(text, "landed k3") {
		t.Errorf("a closed crew is still listed as landed:\n%s", text)
	}
}

func TestWakePrintsTheHandBackChecksAfterForTheNextCrew(t *testing.T) {
	var out strings.Builder
	data := "## For the next crew\na\n\n| criterion | pass/fail | command | output |\n| --- | --- | --- | --- |\n| builds | pass | `make` | ok |\n\n## Deviations from Build\nNone.\n\n## Still open\n- the icon\n"
	writeHandBackChecks(&out, data)
	want := "  acceptance table:\n    | criterion | pass/fail | command | output |\n    | --- | --- | --- | --- |\n    | builds | pass | `make` | ok |\n  ## Deviations from Build:\n    None.\n  ## Still open:\n    - the icon\n"
	if out.String() != want {
		t.Fatalf("got\n%s\nwant\n%s", out.String(), want)
	}
	if rows := markdownTable("| a |\n| b |\n| c |\n", 2); len(rows) != 3 || rows[2] != "(+1 more rows in the hand-back)" {
		t.Fatalf("markdownTable bound: %q", rows)
	}
}
