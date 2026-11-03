package console

import (
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// bxDuty puts a duty board on payments-api's Mate and opens that Project:
// #3 done (for the captain), #2 working, #4 proposed and waiting on topic
// 7, #1 okayed.
func bxDuty(t *testing.T) (Model, *bxAction) {
	t.Helper()
	m, act, _ := bxPayments(t, 60, 40)
	m.tree.Projects[7].Mate.Duty = query.KnownField([]query.DutyItem{
		{ID: 4, Kind: "proposed", Title: "Tidy the docs", Status: "waiting", Topic: 7, Last: "which section first?", At: bxAt(15, 0),
			Notes: []query.DutyNote{{At: bxAt(14, 0), Op: "add"}, {At: bxAt(15, 0), Op: "wait", Topic: 7, Text: "which section first?"}}},
		{ID: 3, Kind: "assigned", Title: "Epic 3 ledger", Status: "done", Last: "PR #12 merged", At: bxAt(14, 30)},
		{ID: 2, Kind: "assigned", Title: "Epic 2 billing", Status: "working", Last: "two crews on it", At: bxAt(14, 0)},
		{ID: 1, Kind: "assigned", Title: "Epic 1", Status: "okayed", At: bxAt(13, 0)},
	})
	return m, act
}

// bxSheet is the open sheet's rendered lines, trimmed.
func bxSheet(t *testing.T, m Model) string {
	t.Helper()
	var b strings.Builder
	for _, l := range m.sheetLines(m.plan()) {
		b.WriteString(strings.TrimRight(l.render(m.w, plainPalette()), " "))
		b.WriteByte('\n')
	}
	return b.String()
}

// `w` on the Mate row opens the board grouped by where each row stands;
// the proposed row carries its tag and the waiting one its topic.
func TestDOpensTheDutyBoard(t *testing.T) {
	m, _ := bxDuty(t)
	m, _ = send(t, m, key("d"))
	if m.sheetOpen() != sheetDuty || m.duty.project != "payments-api" {
		t.Fatalf("w opened %v for %q, want the duty sheet of payments-api", m.sheetOpen(), m.duty.project)
	}
	got := bxSheet(t, m)
	bxWants(t, "duty sheet", got,
		"for you to OK", "[ ] Epic 3 ledger", "done", "PR #12 merged",
		"in progress", "·  Epic 2 billing", "working",
		"waiting on a topic", "Tidy the docs", "proposed topic #7",
		"okayed · 1", "✓  Epic 1")
	for i, want := range []string{"for you to OK", "in progress", "waiting on a topic", "okayed"} {
		if i > 0 && strings.Index(got, want) < strings.Index(got, "for you to OK") {
			t.Errorf("group %q is out of order:\n%s", want, got)
		}
	}
	frame := renderFrame(t, m)
	bxWants(t, "frame", frame, "Duty · payments-api", "space OK", "enter history", "t topic")
	m, _ = send(t, m, key("esc"))
	if m.sheetOpen() != sheetNone {
		t.Fatalf("esc left the sheet %v open", m.sheetOpen())
	}
}

// Space on the done row runs the tick for that Project and row; on a
// working row it only says why not. Enter shows a row's history.
func TestSpaceTicksADoneRow(t *testing.T) {
	m, act := bxDuty(t)
	m, _ = send(t, m, key("d"))
	m, cmd := send(t, m, key(" "))
	if cmd == nil || !m.actionBusy {
		t.Fatalf("Space on the done row ran nothing: busy %v, note %q", m.actionBusy, m.duty.note)
	}
	done := cmd()
	m, _ = send(t, m, done)
	if len(act.reqs) != 1 || act.reqs[0].Action != ActionOkayDuty || act.reqs[0].Target != "payments-api" || act.reqs[0].TargetKind != "project" || act.reqs[0].Input != "3" {
		t.Fatalf("requests = %+v, want okay_duty #3 of payments-api", act.reqs)
	}
	if !m.duty.open || m.duty.note != "done" {
		t.Fatalf("after the tick: open %v, note %q, want the sheet open with the action's line", m.duty.open, m.duty.note)
	}

	m, _ = send(t, m, key("j")) // the working row
	m, cmd = send(t, m, key(" "))
	if cmd != nil || len(act.reqs) != 1 || m.duty.note != "only a done row can be okayed" {
		t.Fatalf("Space on a working row: cmd %v, reqs %d, note %q", cmd != nil, len(act.reqs), m.duty.note)
	}

	m, _ = send(t, m, key("j")) // the waiting row
	m, _ = send(t, m, key("enter"))
	bxWants(t, "expanded row", bxSheet(t, m), "wait #7", "add")
	m, cmd = send(t, m, key("t"))
	if cmd == nil {
		t.Fatal("t on a waiting row opened no topic")
	}
	cmd()
	if len(act.reqs) != 2 || act.reqs[1].Action != ActionOpenNews || act.reqs[1].Input != "7" {
		t.Fatalf("requests = %+v, want open_news #7", act.reqs)
	}
}

// `w` on a Project row on the workspace level opens its Mate's board.
func TestWOnAProjectRowOpensItsMatesBoard(t *testing.T) {
	m, _ := bxDuty(t)
	m, _ = send(t, m, key("esc"))
	if m.cur().kind != frameWorkspace {
		t.Fatalf("setup: not on the workspace: %+v", m.cur())
	}
	m, _ = send(t, m, key("d"))
	if m.sheetOpen() != sheetDuty || m.duty.project != "payments-api" {
		t.Fatalf("w opened %v for %q", m.sheetOpen(), m.duty.project)
	}
	bxWants(t, "duty sheet", bxSheet(t, m), "Epic 3 ledger")
}

// The Mate's detail sums the board up in one line; the Project's peek
// says how many rows are for the captain, and only then. An unreadable
// board says why; none says nothing.
func TestMateDetailDutyStates(t *testing.T) {
	m, _ := bxDuty(t)
	d := bxDetail(t, m)
	bxWants(t, "mate detail", d, "duty     1 for you · 1 working · 1 waiting · 1 okayed")
	d = bxDetail(t, bxMateDuty(t, query.AbsentField[[]query.DutyItem]("no board")))
	bxDenies(t, "absent board", d, "duty")
	d = bxDetail(t, bxMateDuty(t, query.UnknownField[[]query.DutyItem]("permission denied")))
	bxWants(t, "unreadable board", d, "duty     unknown", "permission denied")

	tree := designTree()
	tree.Projects[7].Mate.Duty = query.KnownField([]query.DutyItem{{ID: 3, Title: "Epic 3", Status: "done"}, {ID: 2, Title: "Epic 2", Status: "working"}})
	m = newFixture(t, tree, 40, 36, unicodeGlyphs)
	for i := 0; i < 7; i++ {
		m, _ = send(t, m, key("down"))
	}
	bxWants(t, "project peek", bxDetail(t, m), "duty      1 for you")
	tree.Projects[7].Mate.Duty = query.KnownField([]query.DutyItem{{ID: 2, Title: "Epic 2", Status: "working"}})
	m = newFixture(t, tree, 40, 36, unicodeGlyphs)
	for i := 0; i < 7; i++ {
		m, _ = send(t, m, key("down"))
	}
	bxDenies(t, "project peek with nothing for the captain", bxDetail(t, m), "for you")
}

// bxMateDuty puts a board on payments-api's Mate and opens that Project.
func bxMateDuty(t *testing.T, duty query.Field[[]query.DutyItem]) Model {
	t.Helper()
	tree := designTree()
	tree.Projects[7].Mate.Duty = duty
	m := newFixture(t, tree, 40, 36, unicodeGlyphs)
	for i := 0; i < 7; i++ {
		m, _ = send(t, m, key("down"))
	}
	m, _ = send(t, m, key("enter"))
	return m
}
