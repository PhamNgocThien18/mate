package console

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// The work board sheet (CON-158): `w` on a Mate or Project row lists what
// the captain asked the Mate for and what the Mate proposed, grouped by
// where each stands. A headless Mate shows the board as a page in its own
// pane instead (CON-198): the sheet is 40 columns, too narrow to read, so
// it is only for a Mate with no REPL. The only thing the captain does here is tick a done
// row (Space), which is final and takes it off the board; everything else
// is for looking. A row that waits on a topic names it, and `t` opens the
// topic in the Mate the way Enter on a news does.

// workSheet is the sheet's state.
type workSheet struct {
	open bool
	// project is the Project whose board is shown.
	project string
	// sel is the row under the cursor, as an index into workVisible.
	sel int
	// all shows every okayed row; otherwise only the latest workOkayedShown.
	all bool
	// note is the last result or refusal, drawn under the rows.
	note string
	// expanded is the rows whose note history is shown, by id.
	expanded map[int]bool
}

// workOkayedShown is how many okayed rows the sheet lists until `a`.
const workOkayedShown = 10

// workGroups is the order the sheet lists the statuses in, with each
// group's heading.
var workGroups = []struct{ status, heading string }{
	{"done", "for you to OK"},
	{"working", "in progress"},
	{"waiting", "waiting on a topic"},
	{"okayed", "okayed"},
}

// ErrNoMateREPL is an open-work request for a Mate that runs without the
// REPL: the console shows the board in its own sheet.
var ErrNoMateREPL = errors.New("the Mate runs without the REPL")

// workOpenedMsg is the REPL's answer to an open-work request.
type workOpenedMsg struct {
	project string
	row     row
	err     error
}

// beginWork is `w`: the board of the selected row's Mate, as a page in the
// Mate's pane when it runs headless, else in the sheet.
func (m Model) beginWork() (Model, tea.Cmd) {
	r, ok := m.selectedRow()
	if !ok {
		return m, nil
	}
	project := ""
	switch r.kind {
	case rowMate:
		project = m.mateProject(r).ProjectID
	case rowProject:
		project = r.id
	}
	if project == "" {
		return m, nil
	}
	if m.action == nil {
		return m.openWorkSheet(project), nil
	}
	action, ctx := m.action, m.baseCtx()
	req := ActionRequest{Action: ActionOpenWork, Target: project, TargetKind: "project"}
	return m, func() tea.Msg {
		_, err := action(ctx, req)
		return workOpenedMsg{project: project, row: r, err: err}
	}
}

// onWorkOpened shows the Mate's pane once its REPL has the page, or the
// sheet when the Mate has no REPL.
func (m Model) onWorkOpened(msg workOpenedMsg) (Model, tea.Cmd) {
	switch {
	case errors.Is(msg.err, ErrNoMateREPL):
		return m.openWorkSheet(msg.project), nil
	case msg.err != nil:
		m.msg = errMsg("Open work refused: " + msg.err.Error())
		return m, nil
	case msg.row.kind == rowMate:
		m, stage := m.stageRow(msg.row)
		m.msg = infoMsg(m.g.Arrow + " work board opened in the Mate's pane")
		return m, stage
	}
	m.msg = infoMsg(m.g.Arrow + " work board opened in the Mate's pane")
	return m, nil
}

// openWorkSheet is the board of project in the console's own sheet.
func (m Model) openWorkSheet(project string) Model {
	m = m.closeActions()
	m.work = workSheet{open: true, project: project, expanded: map[int]bool{}}
	m.msg = footerMsg{}
	return m.relayout()
}

func (m Model) closeWork() Model {
	m.work = workSheet{}
	return m.relayout()
}

// workMate is the Mate whose board the sheet shows.
func (m Model) workMate() query.MateNode {
	p, _ := m.projectByID(m.work.project)
	return p.Mate
}

// workProjectName is the Project's name for the title.
func (m Model) workProjectName() string {
	if p, ok := m.projectByID(m.work.project); ok {
		return p.Name
	}
	return m.work.project
}

// workVisible is the rows the sheet lists, in group order: done, working,
// waiting, then the okayed ones (all of them, or the latest few).
func (m Model) workVisible() []query.WorkItem {
	mate := m.workMate()
	if !mate.Work.IsKnown() {
		return nil
	}
	var out []query.WorkItem
	for _, g := range workGroups {
		n := 0
		for _, w := range mate.Work.Value {
			if w.Status != g.status {
				continue
			}
			if g.status == "okayed" && !m.work.all && n >= workOkayedShown {
				break
			}
			out = append(out, w)
			n++
		}
	}
	return out
}

// workSelected is the row under the cursor.
func (m Model) workSelected() (query.WorkItem, bool) {
	rows := m.workVisible()
	if len(rows) == 0 {
		return query.WorkItem{}, false
	}
	return rows[clampInt(m.work.sel, 0, len(rows)-1)], true
}

// onWorkKey is a key while the work sheet is open.
func (m Model) onWorkKey(key string) (tea.Model, tea.Cmd) {
	rows := m.workVisible()
	switch key {
	case "esc", "backspace":
		return m.closeWork(), nil
	case "up", "k":
		m.work.sel = clampInt(m.work.sel-1, 0, max0(len(rows)-1))
		return m, nil
	case "down", "j":
		m.work.sel = clampInt(m.work.sel+1, 0, max0(len(rows)-1))
		return m, nil
	case "a":
		m.work.all = !m.work.all
		m.work.sel = clampInt(m.work.sel, 0, max0(len(m.workVisible())-1))
		return m.relayout(), nil
	case "enter":
		if w, ok := m.workSelected(); ok {
			m.work.expanded[w.ID] = !m.work.expanded[w.ID]
		}
		return m.relayout(), nil
	case "t":
		w, ok := m.workSelected()
		if !ok || w.Status != "waiting" || w.Topic <= 0 {
			m.work.note = "only a waiting row names a topic"
			return m, nil
		}
		m.work.note = fmt.Sprintf("topic #%d opened in the Mate", w.Topic)
		return m, m.openNews(m.work.project, w.Topic)
	case " ":
		return m.okayWork()
	}
	return m, nil
}

// okayWork is Space on a row: the tick, when the row is done.
func (m Model) okayWork() (tea.Model, tea.Cmd) {
	w, ok := m.workSelected()
	if !ok || w.Status != "done" {
		m.work.note = "only a done row can be okayed"
		return m, nil
	}
	c := actionChoice{
		action:  ActionOkayWork,
		desc:    "Tick a done row off the work board",
		enabled: true,
		req:     ActionRequest{Action: ActionOkayWork, Target: m.work.project, TargetKind: "project", Input: strconv.Itoa(w.ID)},
	}
	return m.runAction(c)
}

// onWorkDone is an okay's return: the sheet stays open with the outcome
// under the rows, and the re-read moves the row to the okayed group.
func (m Model) onWorkDone(msg actionDoneMsg) Model {
	if msg.err != nil {
		m.work.note = "okay refused: " + msg.err.Error()
		return m
	}
	m.work.note = msg.text
	if m.work.note == "" {
		m.work.note = "okayed work #" + msg.choice.req.Input
	}
	return m
}

// workSheetLines draws the board; rowAt[i] is the index into workVisible
// of the row line i belongs to, or -1 for a heading, note or blank line.
func (m Model) workSheetLines(p framePlan) ([]gline, []int) {
	var out []gline
	var rowAt []int
	put := func(l gline, row int) {
		out = append(out, l.padTo(p.w, m.g))
		rowAt = append(rowAt, row)
	}
	mate := m.workMate()
	switch {
	case mate.Work.State == query.Unknown:
		for _, l := range fieldState(mate.Work, p.w-2) {
			put(gl().pad(2).join(l), -1)
		}
	case !mate.Work.IsKnown():
		put(gl().pad(2).add("the Mate has no work board yet", tDim), -1)
	}
	rows := m.workVisible()
	sel := clampInt(m.work.sel, 0, max0(len(rows)-1))
	if mate.Work.IsKnown() && len(rows) == 0 {
		put(gl().pad(2).add("nothing on the board", tDim), -1)
	}
	_, _, _, okayed := mate.WorkCounts()
	i := 0
	for _, g := range workGroups {
		first := true
		for i < len(rows) && rows[i].Status == g.status {
			if first {
				heading := g.heading
				if g.status == "okayed" {
					heading = fmt.Sprintf("%s %s %d", heading, m.g.Dot, okayed)
					if !m.work.all && okayed > workOkayedShown {
						heading += fmt.Sprintf(" %s a shows all", m.g.Dot)
					}
				}
				put(gl().pad(2).add(heading, tDim), -1)
				first = false
			}
			for _, l := range m.workRowLines(rows[i], i == sel, p.w) {
				put(l, i)
			}
			i++
		}
	}
	if m.work.note != "" {
		put(gl(), -1)
		for _, part := range wrapWords(m.work.note, p.w-4) {
			put(gl().pad(2).add(part, tFg), -1)
		}
	}
	return out, rowAt
}

// workRowLines is one row: the tick cell, the title and its status on the
// first line, the Mate's last line under it, and the note history when the
// row is expanded.
func (m Model) workRowLines(w query.WorkItem, on bool, width int) []gline {
	lead := gl().pad(1)
	if on {
		lead = gl().add(m.g.Selected, tAcc)
	}
	var box gline
	switch w.Status {
	case "done":
		box = gl().add("[ ]", tGreen)
	case "okayed":
		check := "✓"
		if m.g.Name == "ascii" {
			check = "x"
		}
		box = gl().add(" "+check+" ", tGreen)
	default:
		box = gl().add(" "+m.g.Dot+" ", tDim)
	}
	right := gl()
	if w.Kind == "proposed" {
		right = right.add("proposed", tAmber).pad(1)
	}
	switch w.Status {
	case "done":
		right = right.add("done", tGreen)
	case "working":
		right = right.add("working", tAcc)
	case "waiting":
		right = right.add(fmt.Sprintf("topic #%d", w.Topic), tAmber)
	case "okayed":
		right = right.add("okayed", tDim)
	}
	right = right.pad(1)
	// Nothing is cut (CON-163): the console is 40 columns and a title or
	// the Mate's last line rarely fits one. The title wraps beside its
	// status and runs on under itself; the last line and the history wrap
	// at the same indent, a note's text hanging under its first word.
	left := gl().pad(1).join(box).pad(1)
	indent := left.width() + 1
	text := max0(width - indent - 1)
	title := wrapHanging(w.Title, max0(text-right.width()-1), text)
	if len(title) == 0 {
		title = []string{""}
	}
	out := []gline{lead.join(spread(left.add(title[0], tFg), right, width-1, m.g)).selected(on)}
	for _, part := range title[1:] {
		out = append(out, gl().pad(indent).add(part, tFg).selected(on))
	}
	for _, part := range wrapWords(w.Last, text) {
		out = append(out, gl().pad(indent).add(part, tDim).selected(on))
	}
	if m.work.expanded[w.ID] {
		for _, n := range w.Notes {
			l := gl().pad(indent).add(n.Op, tDim)
			if n.Topic > 0 {
				l = l.add(fmt.Sprintf(" #%d", n.Topic), tAmber)
			}
			if since := m.since(n.At); since != "" {
				l = l.add(" "+m.g.Dot+" "+since, tDim)
			}
			if strings.TrimSpace(n.Text) == "" {
				out = append(out, l.cut(width, m.g).selected(on))
				continue
			}
			l = l.add(" "+m.g.Dot+" ", tDim)
			parts := wrapHanging(n.Text, max0(width-l.width()-1), max0(text-2))
			out = append(out, l.add(parts[0], tFg).selected(on))
			for _, part := range parts[1:] {
				out = append(out, gl().pad(indent+2).add(part, tFg).selected(on))
			}
		}
	}
	return out
}

// workWindow is the sheet body at h rows, scrolled so the selected row is
// in view: a wrapped board is often taller than the pane under the list.
func (m Model) workWindow(p framePlan, h int) []gline {
	lines, rowAt := m.workSheetLines(p)
	top := m.workTop(rowAt, h)
	return lines[top:minInt(len(lines), top+h)]
}

// workTop is the first sheet line drawn; the mouse reads it too. It stays
// at the top, headings included, until the selected row would fall below
// the pane, then scrolls just enough to show the whole row.
func (m Model) workTop(rowAt []int, h int) int {
	if len(rowAt) <= h || h <= 0 {
		return 0
	}
	sel := clampInt(m.work.sel, 0, max0(len(m.workVisible())-1))
	first, last := -1, -1
	for i, r := range rowAt {
		if r == sel {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		return 0
	}
	top := clampInt(last-h+1, 0, len(rowAt)-h)
	return minInt(top, first)
}

// workCountsLine is the detail pane's one-line summary of a Mate's board:
// how many rows wait on the captain's tick, run, wait on a topic, and are
// okayed. forYou is amber while there is one.
func (m Model) workCountsLine(mate query.MateNode) gline {
	forYou, working, waiting, okayed := mate.WorkCounts()
	tok := tDim
	if forYou > 0 {
		tok = tAmber
	}
	sep := " " + m.g.Dot + " "
	return gl().add(fmt.Sprintf("%d for you", forYou), tok).
		add(sep+fmt.Sprintf("%d working", working), tDim).
		add(sep+fmt.Sprintf("%d waiting", waiting), tDim).
		add(sep+fmt.Sprintf("%d okayed", okayed), tDim).
		add(sep+"w opens the board", tDim)
}
