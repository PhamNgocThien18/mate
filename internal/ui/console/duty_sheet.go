package console

import (
	"fmt"
	"strconv"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// The duty board sheet (CON-158): `d` on a Mate or Project row lists what
// the captain asked the Mate for and what the Mate proposed, grouped by
// where each stands. The only thing the captain does here is tick a done
// row (Space), which is final and takes it off the board; everything else
// is for looking. A row that waits on a topic names it, and `t` opens the
// topic in the Mate the way Enter on a news does.

// dutySheet is the sheet's state.
type dutySheet struct {
	open bool
	// project is the Project whose board is shown.
	project string
	// sel is the row under the cursor, as an index into dutyVisible.
	sel int
	// all shows every okayed row; otherwise only the latest dutyOkayedShown.
	all bool
	// note is the last result or refusal, drawn under the rows.
	note string
	// expanded is the rows whose note history is shown, by id.
	expanded map[int]bool
}

// dutyOkayedShown is how many okayed rows the sheet lists until `a`.
const dutyOkayedShown = 10

// dutyGroups is the order the sheet lists the statuses in, with each
// group's heading.
var dutyGroups = []struct{ status, heading string }{
	{"done", "for you to OK"},
	{"working", "in progress"},
	{"waiting", "waiting on a topic"},
	{"okayed", "okayed"},
}

// beginDuty is `d`: the board of the selected row's Mate.
func (m Model) beginDuty() Model {
	r, ok := m.selectedRow()
	if !ok {
		return m
	}
	project := ""
	switch r.kind {
	case rowMate:
		project = m.mateProject(r).ProjectID
	case rowProject:
		project = r.id
	}
	if project == "" {
		return m
	}
	m = m.closeActions()
	m.duty = dutySheet{open: true, project: project, expanded: map[int]bool{}}
	m.msg = footerMsg{}
	return m.relayout()
}

func (m Model) closeDuty() Model {
	m.duty = dutySheet{}
	return m.relayout()
}

// dutyMate is the Mate whose board the sheet shows.
func (m Model) dutyMate() query.MateNode {
	p, _ := m.projectByID(m.duty.project)
	return p.Mate
}

// dutyProjectName is the Project's name for the title.
func (m Model) dutyProjectName() string {
	if p, ok := m.projectByID(m.duty.project); ok {
		return p.Name
	}
	return m.duty.project
}

// dutyVisible is the rows the sheet lists, in group order: done, working,
// waiting, then the okayed ones (all of them, or the latest few).
func (m Model) dutyVisible() []query.DutyItem {
	mate := m.dutyMate()
	if !mate.Duty.IsKnown() {
		return nil
	}
	var out []query.DutyItem
	for _, g := range dutyGroups {
		n := 0
		for _, w := range mate.Duty.Value {
			if w.Status != g.status {
				continue
			}
			if g.status == "okayed" && !m.duty.all && n >= dutyOkayedShown {
				break
			}
			out = append(out, w)
			n++
		}
	}
	return out
}

// dutySelected is the row under the cursor.
func (m Model) dutySelected() (query.DutyItem, bool) {
	rows := m.dutyVisible()
	if len(rows) == 0 {
		return query.DutyItem{}, false
	}
	return rows[clampInt(m.duty.sel, 0, len(rows)-1)], true
}

// onDutyKey is a key while the duty sheet is open.
func (m Model) onDutyKey(key string) (tea.Model, tea.Cmd) {
	rows := m.dutyVisible()
	switch key {
	case "esc", "backspace":
		return m.closeDuty(), nil
	case "up", "k":
		m.duty.sel = clampInt(m.duty.sel-1, 0, max0(len(rows)-1))
		return m, nil
	case "down", "j":
		m.duty.sel = clampInt(m.duty.sel+1, 0, max0(len(rows)-1))
		return m, nil
	case "a":
		m.duty.all = !m.duty.all
		m.duty.sel = clampInt(m.duty.sel, 0, max0(len(m.dutyVisible())-1))
		return m.relayout(), nil
	case "enter":
		if w, ok := m.dutySelected(); ok {
			m.duty.expanded[w.ID] = !m.duty.expanded[w.ID]
		}
		return m.relayout(), nil
	case "t":
		w, ok := m.dutySelected()
		if !ok || w.Status != "waiting" || w.Topic <= 0 {
			m.duty.note = "only a waiting row names a topic"
			return m, nil
		}
		m.duty.note = fmt.Sprintf("topic #%d opened in the Mate", w.Topic)
		return m, m.openNews(m.duty.project, w.Topic)
	case " ":
		return m.okayDuty()
	}
	return m, nil
}

// okayDuty is Space on a row: the tick, when the row is done.
func (m Model) okayDuty() (tea.Model, tea.Cmd) {
	w, ok := m.dutySelected()
	if !ok || w.Status != "done" {
		m.duty.note = "only a done row can be okayed"
		return m, nil
	}
	c := actionChoice{
		action:  ActionOkayDuty,
		desc:    "Tick a done row off the duty board",
		enabled: true,
		req:     ActionRequest{Action: ActionOkayDuty, Target: m.duty.project, TargetKind: "project", Input: strconv.Itoa(w.ID)},
	}
	return m.runAction(c)
}

// onDutyDone is an okay's return: the sheet stays open with the outcome
// under the rows, and the re-read moves the row to the okayed group.
func (m Model) onDutyDone(msg actionDoneMsg) Model {
	if msg.err != nil {
		m.duty.note = "okay refused: " + msg.err.Error()
		return m
	}
	m.duty.note = msg.text
	if m.duty.note == "" {
		m.duty.note = "okayed duty #" + msg.choice.req.Input
	}
	return m
}

// dutySheetLines draws the board; rowAt[i] is the index into dutyVisible
// of the row line i belongs to, or -1 for a heading, note or blank line.
func (m Model) dutySheetLines(p framePlan) ([]gline, []int) {
	var out []gline
	var rowAt []int
	put := func(l gline, row int) {
		out = append(out, l.padTo(p.w, m.g))
		rowAt = append(rowAt, row)
	}
	mate := m.dutyMate()
	switch {
	case mate.Duty.State == query.Unknown:
		for _, l := range fieldState(mate.Duty, p.w-2) {
			put(gl().pad(2).join(l), -1)
		}
	case !mate.Duty.IsKnown():
		put(gl().pad(2).add("the Mate has no duty board yet", tDim), -1)
	}
	rows := m.dutyVisible()
	sel := clampInt(m.duty.sel, 0, max0(len(rows)-1))
	if mate.Duty.IsKnown() && len(rows) == 0 {
		put(gl().pad(2).add("nothing on the board", tDim), -1)
	}
	_, _, _, okayed := mate.DutyCounts()
	i := 0
	for _, g := range dutyGroups {
		first := true
		for i < len(rows) && rows[i].Status == g.status {
			if first {
				heading := g.heading
				if g.status == "okayed" {
					heading = fmt.Sprintf("%s %s %d", heading, m.g.Dot, okayed)
					if !m.duty.all && okayed > dutyOkayedShown {
						heading += fmt.Sprintf(" %s a shows all", m.g.Dot)
					}
				}
				put(gl().pad(2).add(heading, tDim), -1)
				first = false
			}
			for _, l := range m.dutyRowLines(rows[i], i == sel, p.w) {
				put(l, i)
			}
			i++
		}
	}
	if m.duty.note != "" {
		put(gl(), -1)
		for _, part := range wrapWords(m.duty.note, p.w-4) {
			put(gl().pad(2).add(part, tFg), -1)
		}
	}
	return out, rowAt
}

// dutyRowLines is one row: the tick cell, the title and its status on the
// first line, the Mate's last line under it, and the note history when the
// row is expanded.
func (m Model) dutyRowLines(w query.DutyItem, on bool, width int) []gline {
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
	left := gl().pad(1).join(box).pad(1).add(w.Title, tFg)
	first := lead.join(spread(left, right, width-1, m.g)).selected(on)
	out := []gline{first}
	indent := 6
	if w.Last != "" {
		out = append(out, gl().pad(indent).add(truncateEnd(w.Last, max0(width-indent-1), m.g), tDim).selected(on))
	}
	if m.duty.expanded[w.ID] {
		for _, n := range w.Notes {
			l := gl().pad(indent).add(n.Op, tDim)
			if n.Topic > 0 {
				l = l.add(fmt.Sprintf(" #%d", n.Topic), tAmber)
			}
			if since := m.since(n.At); since != "" {
				l = l.add(" "+m.g.Dot+" "+since, tDim)
			}
			if n.Text != "" {
				l = l.add(" "+m.g.Dot+" ", tDim).add(n.Text, tFg)
			}
			out = append(out, l.cut(width, m.g).selected(on))
		}
	}
	return out
}

// dutyCountsLine is the detail pane's one-line summary of a Mate's board:
// how many rows wait on the captain's tick, run, wait on a topic, and are
// okayed. forYou is amber while there is one.
func (m Model) dutyCountsLine(mate query.MateNode) gline {
	forYou, working, waiting, okayed := mate.DutyCounts()
	tok := tDim
	if forYou > 0 {
		tok = tAmber
	}
	sep := " " + m.g.Dot + " "
	return gl().add(fmt.Sprintf("%d for you", forYou), tok).
		add(sep+fmt.Sprintf("%d working", working), tDim).
		add(sep+fmt.Sprintf("%d waiting", waiting), tDim).
		add(sep+fmt.Sprintf("%d okayed", okayed), tDim).
		add(sep+"d opens the board", tDim)
}
