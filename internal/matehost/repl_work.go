package matehost

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// The Work page (CON-198) is the Mate's work board (CON-158) in the chat's
// place, the way ^T shows the Topic page: the console's sheet is 40 columns
// and the board's titles and lines did not fit it. ^L shows it from the
// chat, and the console's `w` asks for it. Rows are grouped by where they
// stand, each with its title, its status and the Mate's last line. The
// captain moves the cursor, opens a row's history (Enter), ticks a done row
// off the board (Space) and opens the topic a waiting row waits on (t);
// the rest is for reading.

// workOkayedShown is how many okayed rows the page lists until `a`.
const workOkayedShown = 10

// workGroups is the order the page lists the statuses in, with each
// group's heading.
var workGroups = []struct{ status, heading string }{
	{WorkDone, "Chờ anh OK"},
	{WorkWorking, "Đang làm"},
	{WorkWaiting, "Chờ topic"},
	{WorkOkayed, "Đã OK"},
}

// workOps names a history note's op.
var workOps = map[string]string{
	workOpAdd:      "thêm",
	workOpProgress: "tiến độ",
	workOpWait:     "chờ",
	workOpDone:     "xong",
	workOpReopen:   "mở lại",
	workOpOkay:     "OK",
}

// workList is the page's rows in order: each group latest move first, and
// the okayed ones all or the latest few.
func (m Model) workList() []Work {
	var out []Work
	for _, g := range workGroups {
		var group []Work
		for _, w := range m.work {
			if w.Status == g.status {
				group = append(group, w)
			}
		}
		sort.SliceStable(group, func(i, j int) bool { return group[i].At.After(group[j].At) })
		if g.status == WorkOkayed && !m.workAll && len(group) > workOkayedShown {
			group = group[:workOkayedShown]
		}
		out = append(out, group...)
	}
	return out
}

// workSelected is the index of the row under the cursor in list, the
// first when the row it was on left the page, -1 on an empty page.
func (m Model) workSelected(list []Work) int {
	for i, w := range list {
		if w.ID == m.workSel {
			return i
		}
	}
	if len(list) == 0 {
		return -1
	}
	return 0
}

// showWork shows the page with the cursor on the first row.
func (m *Model) showWork() {
	m.workSel = 0
	if list := m.workList(); len(list) > 0 {
		m.workSel = list[0].ID
	}
	m.switchTo(viewWork, 0)
}

// moveWork moves the cursor n rows down (up if n < 0).
func (m *Model) moveWork(n int) {
	list := m.workList()
	if i := m.workSelected(list); i >= 0 {
		m.workSel = list[min(len(list)-1, max(0, i+n))].ID
	}
}

// workKey is a key on the page; handled is false for keys the chat takes.
func (m Model) workKey(k tea.KeyMsg) (Model, bool) {
	list := m.workList()
	i := m.workSelected(list)
	switch k.Type {
	case tea.KeyEsc:
		m.switchTo(viewChat, 0)
	case tea.KeyUp:
		m.moveWork(-1)
	case tea.KeyDown:
		m.moveWork(1)
	case tea.KeyHome:
		m.moveWork(-len(list))
	case tea.KeyEnd:
		m.moveWork(len(list))
	case tea.KeyEnter:
		if i >= 0 {
			if m.workOpen == nil {
				m.workOpen = map[int]bool{}
			}
			m.workOpen[list[i].ID] = !m.workOpen[list[i].ID]
		}
	case tea.KeySpace:
		m.okayWork(list, i)
	case tea.KeyF1:
		m.help = true
	case tea.KeyCtrlC:
		// Interrupting the Mate works on the page too.
		return m, false
	case tea.KeyRunes:
		switch strings.ToLower(string(k.Runes)) {
		case "j":
			m.moveWork(1)
		case "k":
			m.moveWork(-1)
		case "a":
			m.workAll = !m.workAll
		case "t":
			if i < 0 || list[i].Status != WorkWaiting || list[i].Topic <= 0 {
				m.note = "chỉ dòng đang chờ topic mới mở được topic"
				break
			}
			m.openTopic(list[i].Topic)
		case " ":
			m.okayWork(list, i)
		case "?":
			m.help = true
		}
	}
	return m, true
}

// okayWork is Space on row i: the captain's tick, when the row is done.
func (m *Model) okayWork(list []Work, i int) {
	if i < 0 || list[i].Status != WorkDone {
		m.note = "chỉ dòng đã xong mới OK được"
		return
	}
	id := list[i].ID
	m.note = fmt.Sprintf("✓ đã OK việc #%d", id)
	m.call(func(ctx context.Context, c Conn) tea.Msg { return noted(c.OkayWork(ctx, id)) })
}

// clickWork moves the cursor to the row at screen row y of the page.
func (m *Model) clickWork(y int) {
	w, _ := m.size()
	_, rows := m.workView(w, m.topicRows())
	row := y - len(m.header(w))
	if row < 0 || row >= len(rows) || rows[row] < 0 {
		return
	}
	m.workSel = m.workList()[rows[row]].ID
}

// workView is the page as rows lines, scrolled so the row under the
// cursor shows whole, and for each line the index of its row in workList
// (-1 for headings and blanks).
func (m Model) workView(w, rows int) ([]string, []int) {
	lines, at := m.workLines(w)
	sel := m.workSelected(m.workList())
	start, end := 0, 0
	for i, r := range at {
		if r == sel {
			if end == 0 {
				start = i
			}
			end = i + 1
		}
	}
	room := rows
	if len(lines) > rows {
		// The last row says what is below.
		room = max(1, rows-1)
	}
	offset := 0
	if end > room {
		offset = min(start, end-room)
	}
	offset = min(offset, max(0, len(lines)-room))
	view := append([]string(nil), lines[offset:min(len(lines), offset+room)]...)
	idx := append([]int(nil), at[offset:min(len(at), offset+room)]...)
	if offset+room < len(lines) {
		view, idx = append(view, " "+styleAccent.Render("▾ cuộn xuống: còn việc")), append(idx, -1)
	}
	for len(view) < rows {
		view, idx = append(view, ""), append(idx, -1)
	}
	return view, idx
}

// workLines are every line of the page and the row each belongs to.
func (m Model) workLines(w int) ([]string, []int) {
	list := m.workList()
	if len(list) == 0 {
		return []string{"", styleMuted.Render(" Bảng việc trống. Anh giao việc hoặc Mate đề xuất thì việc hiện ở đây.")}, []int{-1, -1}
	}
	okayed := 0
	for _, x := range m.work {
		if x.Status == WorkOkayed {
			okayed++
		}
	}
	sel := m.workSelected(list)
	var lines []string
	var at []int
	add := func(l string, r int) { lines, at = append(lines, l), append(at, r) }
	i := 0
	for _, g := range workGroups {
		first := true
		for i < len(list) && list[i].Status == g.status {
			if first {
				n := 0
				for _, x := range list {
					if x.Status == g.status {
						n++
					}
				}
				if g.status == WorkOkayed {
					n = okayed
				}
				heading := fmt.Sprintf(" %s · %d", g.heading, n)
				if g.status == WorkOkayed && !m.workAll && okayed > workOkayedShown {
					heading += fmt.Sprintf(" · %d mới nhất, a xem hết", workOkayedShown)
				}
				add("", -1)
				add(styleBold.Inherit(styleMuted).Render(heading), -1)
				first = false
			}
			for _, l := range m.workRow(list[i], i == sel, w) {
				add(l, i)
			}
			i++
		}
	}
	return lines, at
}

// workRow is one row: the tick cell, the title and its status on the
// first line, the Mate's last line under it, and the history when the
// row is open.
func (m Model) workRow(x Work, on bool, w int) []string {
	lead := "   "
	if on {
		lead = " " + styleAccent.Render("▌") + " "
	}
	var box string
	switch x.Status {
	case WorkDone:
		box = styleSuccess.Render("[ ]")
	case WorkOkayed:
		box = styleSuccess.Render(" ✓ ")
	default:
		box = styleMuted.Render(" · ")
	}
	var right []string
	if x.Kind == WorkProposed {
		right = append(right, styleAttention.Render("đề xuất"))
	}
	switch x.Status {
	case WorkDone:
		right = append(right, styleSuccess.Render("xong"))
	case WorkWorking:
		right = append(right, styleWorking.Render("đang làm"))
	case WorkWaiting:
		right = append(right, styleAttention.Render(fmt.Sprintf("chờ topic #%d", x.Topic)))
	case WorkOkayed:
		right = append(right, styleMuted.Render("đã OK"))
	}
	if !x.At.IsZero() {
		right = append(right, styleMuted.Render(age(m.now().Sub(x.At))))
	}
	rightText := strings.Join(right, styleMuted.Render(" · ")) + " "
	id := styleMuted.Render(fmt.Sprintf("#%d ", x.ID))
	indent := ansi.StringWidth(lead) + 4 + ansi.StringWidth(id)
	text := max(10, w-indent-1)
	titleStyle := styleBold
	if x.Status == WorkOkayed {
		titleStyle = styleMuted
	}
	title := wrap(x.Title, max(10, text-ansi.StringWidth(rightText)-1))
	pad := strings.Repeat(" ", indent)
	out := []string{spread(lead+box+" "+id+titleStyle.Render(title[0]), rightText, w)}
	for _, part := range title[1:] {
		out = append(out, pad+titleStyle.Render(part))
	}
	if last := strings.TrimSpace(x.Last); last != "" {
		for _, part := range wrap(last, text) {
			out = append(out, pad+styleMuted.Render(part))
		}
	}
	if m.workOpen[x.ID] {
		for _, n := range x.Notes {
			head := workOps[n.Op]
			if head == "" {
				head = n.Op
			}
			if n.Topic > 0 {
				head += fmt.Sprintf(" #%d", n.Topic)
			}
			head += " · " + age(m.now().Sub(n.At))
			if strings.TrimSpace(n.Text) == "" {
				out = append(out, pad+styleFaint.Render("└ "+head))
				continue
			}
			first := styleFaint.Render("└ " + head + " · ")
			parts := wrap(n.Text, max(10, text-ansi.StringWidth(first)))
			out = append(out, pad+first+parts[0])
			for _, part := range parts[1:] {
				out = append(out, pad+"  "+part)
			}
		}
	}
	return out
}
