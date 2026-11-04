package console

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// bxWork puts a work board on payments-api's Mate and opens that Project:
// #3 done (for the captain), #2 working, #4 proposed and waiting on topic
// 7, #1 okayed.
func bxWork(t *testing.T) (Model, *bxAction) {
	t.Helper()
	m, act, _ := bxPayments(t, 60, 40)
	m.tree.Projects[7].Mate.Work = query.KnownField([]query.WorkItem{
		{ID: 4, Kind: "proposed", Title: "Tidy the docs", Status: "waiting", Topic: 7, Last: "which section first?", At: bxAt(15, 0),
			Notes: []query.WorkNote{{At: bxAt(14, 0), Op: "add"}, {At: bxAt(15, 0), Op: "wait", Topic: 7, Text: "which section first?"}}},
		{ID: 3, Kind: "assigned", Title: "Epic 3 ledger", Status: "done", Last: "PR #12 merged", At: bxAt(14, 30)},
		{ID: 2, Kind: "assigned", Title: "Epic 2 billing", Status: "working", Last: "two crews on it", At: bxAt(14, 0)},
		{ID: 1, Kind: "assigned", Title: "Epic 1", Status: "okayed", At: bxAt(13, 0)},
	})
	return m, act
}

// bxOpenSheet is `w` for a Mate with no REPL: the open-work request is
// refused with ErrNoMateREPL and the console shows its own sheet. The
// request is not kept in act.
func bxOpenSheet(t *testing.T, m Model, act *bxAction) Model {
	t.Helper()
	act.err = ErrNoMateREPL
	m, cmd := send(t, m, key("w"))
	if cmd == nil {
		t.Fatal("w sent no open-work request")
	}
	m, _ = send(t, m, cmd())
	act.err, act.reqs = nil, nil
	return m
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
func TestWOpensTheWorkBoard(t *testing.T) {
	m, act := bxWork(t)
	m = bxOpenSheet(t, m, act)
	if m.sheetOpen() != sheetWork || m.work.project != "payments-api" {
		t.Fatalf("w opened %v for %q, want the work sheet of payments-api", m.sheetOpen(), m.work.project)
	}
	got := bxSheet(t, m)
	bxWants(t, "work sheet", got,
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
	bxWants(t, "frame", frame, "Work · payments-api", "space OK", "enter history", "t topic")
	m, _ = send(t, m, key("esc"))
	if m.sheetOpen() != sheetNone {
		t.Fatalf("esc left the sheet %v open", m.sheetOpen())
	}
}

// Space on the done row runs the tick for that Project and row; on a
// working row it only says why not. Enter shows a row's history.
func TestSpaceTicksADoneRow(t *testing.T) {
	m, act := bxWork(t)
	m = bxOpenSheet(t, m, act)
	m, cmd := send(t, m, key(" "))
	if cmd == nil || !m.actionBusy {
		t.Fatalf("Space on the done row ran nothing: busy %v, note %q", m.actionBusy, m.work.note)
	}
	done := cmd()
	m, _ = send(t, m, done)
	if len(act.reqs) != 1 || act.reqs[0].Action != ActionOkayWork || act.reqs[0].Target != "payments-api" || act.reqs[0].TargetKind != "project" || act.reqs[0].Input != "3" {
		t.Fatalf("requests = %+v, want okay_work #3 of payments-api", act.reqs)
	}
	if !m.work.open || m.work.note != "done" {
		t.Fatalf("after the tick: open %v, note %q, want the sheet open with the action's line", m.work.open, m.work.note)
	}

	m, _ = send(t, m, key("j")) // the working row
	m, cmd = send(t, m, key(" "))
	if cmd != nil || len(act.reqs) != 1 || m.work.note != "only a done row can be okayed" {
		t.Fatalf("Space on a working row: cmd %v, reqs %d, note %q", cmd != nil, len(act.reqs), m.work.note)
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
	m, act := bxWork(t)
	m, _ = send(t, m, key("esc"))
	if m.cur().kind != frameWorkspace {
		t.Fatalf("setup: not on the workspace: %+v", m.cur())
	}
	m = bxOpenSheet(t, m, act)
	if m.sheetOpen() != sheetWork || m.work.project != "payments-api" {
		t.Fatalf("w opened %v for %q", m.sheetOpen(), m.work.project)
	}
	bxWants(t, "work sheet", bxSheet(t, m), "Epic 3 ledger")
}

// The Mate's detail sums the board up in one line; the Project's peek
// says how many rows are for the captain, and only then. An unreadable
// board says why; none says nothing.
func TestMateDetailWorkStates(t *testing.T) {
	m, _ := bxWork(t)
	d := bxDetail(t, m)
	bxWants(t, "mate detail", d, "work     1 for you · 1 working · 1 waiting · 1 okayed")
	d = bxDetail(t, bxMateWork(t, query.AbsentField[[]query.WorkItem]("no board")))
	bxDenies(t, "absent board", d, "work")
	d = bxDetail(t, bxMateWork(t, query.UnknownField[[]query.WorkItem]("permission denied")))
	bxWants(t, "unreadable board", d, "work     unknown", "permission denied")

	tree := designTree()
	tree.Projects[7].Mate.Work = query.KnownField([]query.WorkItem{{ID: 3, Title: "Epic 3", Status: "done"}, {ID: 2, Title: "Epic 2", Status: "working"}})
	m = newFixture(t, tree, 40, 36, unicodeGlyphs)
	for i := 0; i < 7; i++ {
		m, _ = send(t, m, key("down"))
	}
	bxWants(t, "project peek", bxDetail(t, m), "work      1 for you")
	tree.Projects[7].Mate.Work = query.KnownField([]query.WorkItem{{ID: 2, Title: "Epic 2", Status: "working"}})
	m = newFixture(t, tree, 40, 36, unicodeGlyphs)
	for i := 0; i < 7; i++ {
		m, _ = send(t, m, key("down"))
	}
	bxDenies(t, "project peek with nothing for the captain", bxDetail(t, m), "for you")
}

// bxMateWork puts a board on payments-api's Mate and opens that Project.
func bxMateWork(t *testing.T, work query.Field[[]query.WorkItem]) Model {
	t.Helper()
	tree := designTree()
	tree.Projects[7].Mate.Work = work
	m := newFixture(t, tree, 40, 36, unicodeGlyphs)
	for i := 0; i < 7; i++ {
		m, _ = send(t, m, key("down"))
	}
	m, _ = send(t, m, key("enter"))
	return m
}

// bxLongWork is the board captain saw cut on 2026-11-04 (CON-163): a long
// title and a long last line in a 40-column console, plus enough rows that
// a wrapped board is taller than the pane under the list.
func bxLongWork(t *testing.T, w, h int) Model {
	t.Helper()
	m, act, _ := bxPayments(t, w, h)
	items := []query.WorkItem{
		{ID: 1, Kind: "assigned", Status: "done", At: bxAt(17, 0),
			Title: "HU-387 khay trang trí trong màn soạn (HU-388..394)",
			Last:  "HU-388..394 đều đã vào main (PR #334-#340); HU-387 Done trên Linear",
			Notes: []query.WorkNote{{At: bxAt(17, 0), Op: "done", Text: "HU-388..394 đều đã vào main (PR #334-#340); HU-387 Done trên Linear, chờ anh thử app"}}},
	}
	for i := 2; i <= 9; i++ {
		items = append(items, query.WorkItem{ID: i, Kind: "assigned", Status: "okayed", At: bxAt(13, 0),
			Title: "việc cũ số " + strings.Repeat("x", i) + " đã xong từ trước", Last: "đã merge"})
	}
	m.tree.Projects[7].Mate.Work = query.KnownField(items)
	return bxOpenSheet(t, m, act)
}

// `w` on a headless Mate asks its REPL for the Work page and shows the
// Mate in the next pane; the console opens no sheet of its own.
func TestWAsksAHeadlessMateForTheWorkPage(t *testing.T) {
	m, act := bxWork(t)
	m, cmd := send(t, m, key("w"))
	if cmd == nil {
		t.Fatal("w sent no open-work request")
	}
	if len(act.reqs) != 0 {
		t.Fatalf("w ran %+v before its command", act.reqs)
	}
	m, stage := send(t, m, cmd())
	if len(act.reqs) != 1 || act.reqs[0].Action != ActionOpenWork || act.reqs[0].Target != "payments-api" {
		t.Fatalf("requests = %+v, want open_work of payments-api", act.reqs)
	}
	if m.sheetOpen() != sheetNone {
		t.Fatalf("w opened the sheet %v for a headless Mate", m.sheetOpen())
	}
	if stage == nil {
		t.Fatal("the Mate row was not shown in the next pane")
	}
	if !strings.Contains(m.msg.text, "work board opened in the Mate's pane") {
		t.Fatalf("footer = %q", m.msg.text)
	}
}

// Nothing on the board is cut: the title and the Mate's last line wrap,
// and so does a note's text in the history.
func TestTheWorkBoardWrapsInsteadOfCutting(t *testing.T) {
	m := bxLongWork(t, 40, 60)
	m, _ = send(t, m, key("enter"))
	got := bxSheet(t, m)
	if strings.Contains(got, "…") {
		t.Fatalf("the board still cuts a line:\n%s", got)
	}
	flat := strings.Join(strings.Fields(got), " ")
	// The status sits at the end of the title's first line, so the title is
	// read with it taken out.
	flat = strings.Replace(flat, " done ", " ", 1)
	for _, want := range []string{
		"HU-387 khay trang trí trong màn soạn (HU-388..394)",
		"HU-388..394 đều đã vào main (PR #334-#340); HU-387 Done trên Linear",
		"chờ anh thử app",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("the board lacks %q:\n%s", want, got)
		}
	}
	for _, l := range strings.Split(got, "\n") {
		if cells(l) > 40 {
			t.Errorf("line wider than the console: %q", l)
		}
	}
	first := strings.Split(got, "\n")[1]
	if !strings.HasSuffix(first, "done") {
		t.Errorf("the status left the title's first line: %q", first)
	}
}

// A board taller than its pane scrolls to keep the selected row whole,
// and a click lands on the row drawn under the pointer.
func TestATallWorkBoardScrollsToTheSelectedRow(t *testing.T) {
	m := bxLongWork(t, 40, 24)
	sl, ok := m.plan().slot(slotSheet)
	if !ok {
		t.Fatal("no sheet slot")
	}
	_, h := sl.body()
	_, rowAt := m.workSheetLines(m.plan())
	if len(rowAt) <= h {
		t.Fatalf("setup: board of %d lines fits the %d-row pane", len(rowAt), h)
	}
	for i := 0; i < 8; i++ {
		m, _ = send(t, m, key("j"))
	}
	if m.work.sel != 8 {
		t.Fatalf("sel = %d, want the last row", m.work.sel)
	}
	frame := renderFrame(t, m)
	if !strings.Contains(frame, "việc cũ số "+strings.Repeat("x", 9)) {
		t.Fatalf("the selected last row is not on screen:\n%s", frame)
	}
	sl, _ = m.plan().slot(slotSheet)
	top, h := sl.body()
	_, rowAt = m.workSheetLines(m.plan())
	off := m.workTop(rowAt, h)
	if off == 0 {
		t.Fatal("the board did not scroll")
	}
	y := -1
	for i := off; i < off+h && i < len(rowAt); i++ {
		if rowAt[i] == 7 {
			y = top + i - off
			break
		}
	}
	if y < 0 {
		t.Fatal("row 7 is not drawn")
	}
	m, _ = send(t, m, tea.MouseMsg{X: 4, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.work.sel != 7 {
		t.Fatalf("a click on row 7 selected %d", m.work.sel)
	}
}
