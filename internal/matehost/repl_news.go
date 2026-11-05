package matehost

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// News in the REPL are the design's topics (CON-104, design v2 of
// 2026-10-18): a badge in the header and a tray over the composer while the
// captain owes an answer, and ^T for the Topic page, which takes the chat's
// place. Each news is one frame on the page. Folded, a frame is three
// lines: its number and crew, its question, and whether it is answered.
// Open, it holds all the captain needs to answer in place: the crew's
// context, the options a–d, and a line to write in. One frame is open at a
// time; the cursor moves between frames without opening them. An answer
// waits undoDelay before it is sent, so `u` can take it back.

const (
	undoDelay  = 5 * time.Second
	confirmFor = 5 * time.Second
	// ageFrom is the width from which a frame's border shows its age.
	ageFrom = 90
)

var (
	colAttention   = lipgloss.AdaptiveColor{Light: "130", Dark: "214"}
	styleAttention = lipgloss.NewStyle().Foreground(colAttention)
)

type viewMode int

const (
	viewChat viewMode = iota
	viewTopics
	// viewSession shows the session of the news openID (repl_session.go).
	viewSession
	// viewWork shows the Mate's work board (repl_work.go).
	viewWork
)

// pendingAnswer is an answer waiting out its undo delay.
type pendingAnswer struct {
	id    int
	text  string
	label string
	at    time.Time
}

// newsRef is a captain message in the chat that answers a news: `#3 …`.
var newsRef = regexp.MustCompile(`^#(\d+)\s+(?s)(.+)$`)

func (m Model) findNews(id int) (News, bool) { return findNews(m.news, id) }

// answered reports whether a news reads as answered: the captain owes it
// nothing, or their answer to it waits out its undo delay. The Mate at
// work on an answer is answered too; the page shows only the two states.
func (m Model) answered(n News) bool {
	return !n.Owed() || m.pending != nil && m.pending.id == n.ID
}

// owed are the news waiting on the captain, oldest first.
func (m Model) owed() []News {
	owed, _ := m.topics()
	return owed
}

// topics are the page's frames in their two groups: the news owed, oldest
// first, then those answered, newest first. A news closed before today is
// archived and off the page.
func (m Model) topics() (owed, answered []News) {
	today := m.now().Format("2006-01-02")
	for _, n := range m.news {
		switch {
		case !m.answered(n):
			owed = append(owed, n)
		case n.Closed() && (n.Archived || n.At.Format("2006-01-02") != today):
		default:
			answered = append(answered, n)
		}
	}
	sort.SliceStable(owed, func(i, j int) bool { return owed[i].At.Before(owed[j].At) })
	sort.SliceStable(answered, func(i, j int) bool { return m.answeredAt(answered[i]).After(m.answeredAt(answered[j])) })
	return owed, answered
}

// topicList is the page's frames in order.
func (m Model) topicList() []News {
	owed, answered := m.topics()
	return append(owed, answered...)
}

// answeredAt is when a news last changed, or when the answer waiting out
// its undo delay was given.
func (m Model) answeredAt(n News) time.Time {
	if p := m.pending; p != nil && p.id == n.ID {
		return p.at
	}
	if !n.AnsweredAt.IsZero() && n.AnsweredAt.After(n.At) {
		return n.AnsweredAt
	}
	return n.At
}

// selected is the index of the frame under the cursor in list, the first
// when the news it was on left the page, -1 on an empty page.
func (m Model) selected(list []News) int {
	for i, n := range list {
		if n.ID == m.selID {
			return i
		}
	}
	if len(list) == 0 {
		return -1
	}
	return 0
}

// --- moving between views ---

// switchTo shows mode with frame open open (0 for none), keeping the
// composer of what it leaves: the chat's draft, and each frame's.
func (m *Model) switchTo(mode viewMode, open int) {
	m.stash()
	if open != m.openID {
		m.roundsOpen = false
	}
	m.mode, m.openID, m.typing = mode, open, false
	m.restore()
}

// draftKey is the draft the composer holds now: 0 the chat's, n frame n's,
// -1 none (the page with every frame folded).
func (m Model) draftKey() int {
	switch {
	case m.mode == viewChat:
		return 0
	case m.mode == viewSession:
		return sessionDraft + m.openID
	case m.openID > 0:
		return m.openID
	}
	return -1
}

func (m *Model) stash() {
	if m.drafts == nil {
		m.drafts = map[int]string{}
	}
	if key := m.draftKey(); key >= 0 {
		m.drafts[key] = string(m.input)
	}
}

func (m *Model) restore() {
	r := []rune(m.drafts[m.draftKey()])
	m.input, m.cursor = r, len(r)
}

// openTopic shows the page with the cursor on news id and its frame open.
func (m *Model) openTopic(id int) {
	n, ok := m.findNews(id)
	if !ok {
		return
	}
	m.selID = id
	m.switchTo(viewTopics, id)
	if n.Status == NewsNew {
		m.call(func(ctx context.Context, c Conn) tea.Msg { return noted(c.SeenNews(ctx, id)) })
	}
}

// showTopics shows the page from the chat, every frame folded and the
// cursor on the first.
func (m *Model) showTopics() {
	m.selID = 0
	if list := m.topicList(); len(list) > 0 {
		m.selID = list[0].ID
	}
	m.switchTo(viewTopics, 0)
}

// toggle opens the frame under the cursor, or folds it when it is open.
func (m *Model) toggle() {
	list := m.topicList()
	i := m.selected(list)
	if i < 0 {
		return
	}
	if m.openID == list[i].ID {
		m.switchTo(viewTopics, 0)
		return
	}
	m.openTopic(list[i].ID)
}

// moveCursor moves the cursor n frames down (up if n < 0), opening none.
func (m *Model) moveCursor(n int) {
	list := m.topicList()
	if i := m.selected(list); i >= 0 {
		m.selID = list[min(len(list)-1, max(0, i+n))].ID
	}
}

// nextOwed opens the frame owed after the one under the cursor, oldest
// first, wrapping around; from the chat, the oldest owed.
func (m *Model) nextOwed() {
	owed := m.owed()
	if len(owed) == 0 {
		m.note = "không có topic nào chờ anh"
		return
	}
	next := owed[0]
	if m.mode == viewTopics {
		// The frame under the cursor first, unless it is open already.
		for i, n := range owed {
			if n.ID != m.selID {
				continue
			}
			next = n
			if m.openID == n.ID {
				next = owed[(i+1)%len(owed)]
			}
			break
		}
	}
	m.openTopic(next.ID)
}

// --- answering ---

// answer queues an answer to news id behind the undo delay. On the page
// its frame folds into the answered, and the cursor moves to the frame
// owed after it, unopened.
func (m *Model) answer(id int, text, label string) {
	if m.pending != nil {
		m.flush()
	}
	owed := m.owed()
	m.pending = &pendingAnswer{id: id, text: text, label: label, at: m.now()}
	if m.mode != viewTopics {
		return
	}
	if m.openID == id {
		m.switchTo(viewTopics, 0)
	}
	for i, n := range owed {
		if n.ID != id {
			continue
		}
		rest := append(append([]News(nil), owed[i+1:]...), owed[:i]...)
		if len(rest) > 0 {
			m.selID = rest[0].ID
		}
		break
	}
}

// undo takes back the answer waiting out its delay; on the page its frame
// opens again to be answered.
func (m *Model) undo() {
	p := m.pending
	m.pending = nil
	m.note = fmt.Sprintf("đã hoàn tác câu trả lời cho #%d", p.id)
	if m.mode == viewTopics {
		m.openTopic(p.id)
	}
}

// flush sends the pending answer.
func (m *Model) flush() {
	p := m.pending
	if p == nil {
		return
	}
	m.pending = nil
	if !m.linked {
		m.note = fmt.Sprintf("chưa nối được với Mate; câu trả lời cho #%d vẫn nằm trong khung", p.id)
		m.giveBackAnswer(p.id, p.text)
		return
	}
	m.confirm, m.confirmAt = fmt.Sprintf("✓ #%d đã trả lời · Mate đang xử lý", p.id), m.now()
	m.call(func(ctx context.Context, c Conn) tea.Msg {
		if err := c.AnswerNews(ctx, p.id, p.text); err != nil {
			return replyMsg{op: callAnswer, id: p.id, text: p.text, err: err}
		}
		return nil
	})
}

// giveBackAnswer puts an answer the host never got back in its frame's
// composer, unless the captain has written something else there since.
func (m *Model) giveBackAnswer(id int, text string) {
	m.confirm = ""
	if m.mode == viewTopics && m.openID == id {
		if len(m.input) == 0 {
			m.input, m.cursor = []rune(text), len([]rune(text))
		}
		return
	}
	if m.drafts == nil {
		m.drafts = map[int]string{}
	}
	if m.drafts[id] == "" {
		m.drafts[id] = text
	}
}

// tickNews sends an answer whose undo delay is over.
func (m *Model) tickNews() {
	if p := m.pending; p != nil && m.now().Sub(p.at) >= undoDelay {
		m.flush()
	}
}

func (m *Model) choose(n News, i int) {
	o := n.Options[i]
	m.answer(n.ID, fmt.Sprintf("[%s] %s", o.Key, o.Text), "Đã chọn "+strings.ToUpper(o.Key))
}

// --- keys ---

// newsKey handles a key that moves between the chat and the page, and
// every key on the page; handled is false for keys the composer takes.
func (m Model) newsKey(k tea.KeyMsg) (Model, bool) {
	switch k.Type {
	case tea.KeyCtrlT:
		if m.mode == viewTopics {
			m.switchTo(viewChat, 0)
		} else {
			m.showTopics()
		}
		return m, true
	case tea.KeyCtrlN:
		m.nextOwed()
		return m, true
	case tea.KeyCtrlL:
		if m.mode == viewWork {
			m.switchTo(viewChat, 0)
		} else {
			m.showWork()
		}
		return m, true
	}
	if m.pending != nil && k.Type == tea.KeyRunes && string(k.Runes) == "u" && !m.typing && (m.mode == viewTopics || len(m.input) == 0) {
		m.undo()
		return m, true
	}
	if m.mode == viewSession {
		return m.sessionKey(k)
	}
	if m.mode == viewWork {
		return m.workKey(k)
	}
	if m.mode != viewTopics {
		return m, false
	}
	if m.typing {
		return m.typingKey(k)
	}
	return m.topicKey(k)
}

// topicKey is a key on the page while the captain is not writing in a
// frame: the cursor, opening and folding, and the options of the frame
// open.
func (m Model) topicKey(k tea.KeyMsg) (Model, bool) {
	list := m.topicList()
	switch k.Type {
	case tea.KeyEsc:
		m.switchTo(viewChat, 0)
	case tea.KeyUp:
		m.moveCursor(-1)
	case tea.KeyDown:
		m.moveCursor(1)
	case tea.KeyHome:
		m.moveCursor(-len(list))
	case tea.KeyEnd:
		m.moveCursor(len(list))
	case tea.KeyEnter, tea.KeySpace:
		m.toggle()
	case tea.KeyTab:
		if n, ok := m.findNews(m.openID); ok && !n.Closed() {
			m.typing = true
		}
	case tea.KeyCtrlO:
		m.roundsOpen = !m.roundsOpen
	case tea.KeyF1:
		m.help = true
	case tea.KeyCtrlC:
		// Interrupting the Mate works on the page too.
		return m, false
	case tea.KeyRunes:
		r := strings.ToLower(string(k.Runes))
		if n, ok := m.findNews(m.openID); ok && !m.answered(n) {
			for i, o := range n.Options {
				if strings.ToLower(o.Key) == r {
					m.choose(n, i)
					return m, true
				}
			}
		}
		switch {
		case r == "j":
			m.moveCursor(1)
		case r == "k":
			m.moveCursor(-1)
		case r == "?":
			m.help = true
		case r == "f":
			// The open frame's session, or the one under the cursor.
			id := m.openID
			if i := m.selected(list); id == 0 && i >= 0 {
				id = list[i].ID
			}
			if id > 0 {
				m.openSession(id)
			}
		case len(r) == 1 && r >= "1" && r <= "9":
			if i, _ := strconv.Atoi(r); i <= len(list) {
				m.selID = list[i-1].ID
				m.openTopic(list[i-1].ID)
			}
		}
	}
	return m, true
}

// typingKey is a key while the captain writes in the open frame: Enter
// sends, Tab goes back to the options, and the composer takes the rest.
func (m Model) typingKey(k tea.KeyMsg) (Model, bool) {
	n, ok := m.findNews(m.openID)
	if !ok || n.Closed() {
		m.typing = false
		return m, true
	}
	switch k.Type {
	case tea.KeyEsc:
		m.switchTo(viewChat, 0)
	case tea.KeyTab:
		m.typing = false
	case tea.KeyEnter:
		if k.Alt {
			return m, false
		}
		text := strings.TrimSpace(string(m.input))
		if text == "" {
			return m, true
		}
		m.setInput(nil)
		label := "Đã gửi"
		if !n.Owed() {
			label = "Đã ghi thêm"
		}
		m.answer(n.ID, text, label)
	case tea.KeyUp:
		m.lineUp()
	case tea.KeyDown:
		m.lineDown()
	case tea.KeyHome:
		m.cursor = m.lineStart()
	case tea.KeyEnd:
		m.cursor = m.lineEnd()
	case tea.KeyCtrlO:
		m.roundsOpen = !m.roundsOpen
	case tea.KeyPgUp, tea.KeyPgDown, tea.KeyShiftUp, tea.KeyShiftDown, tea.KeyCtrlR, tea.KeyCtrlX:
		// The chat's keys do nothing here.
	default:
		return m, false
	}
	return m, true
}

// chatNewsRef sends a chat message that names a news (`#3 chọn a`) to that
// news instead, behind the same undo delay.
func (m *Model) chatNewsRef(text string) bool {
	g := newsRef.FindStringSubmatch(text)
	if g == nil {
		return false
	}
	id, _ := strconv.Atoi(g[1])
	n, ok := m.findNews(id)
	if !ok {
		return false
	}
	if n.Closed() {
		m.note = fmt.Sprintf("#%d đã xong nên tin này gửi cho Mate như tin thường", id)
		return false
	}
	answer := strings.TrimSpace(g[2])
	for _, o := range n.Options {
		if strings.EqualFold(answer, o.Key) || strings.EqualFold(answer, "chọn "+o.Key) {
			answer = fmt.Sprintf("[%s] %s", o.Key, o.Text)
		}
	}
	m.answer(id, answer, "Đã ghi từ chat")
	return true
}

// --- mouse ---

// clickTopic opens or folds the frame at screen row y of the page.
func (m *Model) clickTopic(y int) {
	w, _ := m.size()
	_, frames := m.topicView(w, m.topicRows())
	row := y - len(m.header(w))
	if row < 0 || row >= len(frames) || frames[row] < 0 {
		return
	}
	list := m.topicList()
	m.selID = list[frames[row]].ID
	m.toggle()
}

// clickChat opens the oldest frame owed when the captain clicks the
// tray's line in the chat.
func (m *Model) clickChat(y int) {
	if row := m.trayRow(); row >= 0 && y == row {
		if owed := m.owed(); len(owed) > 0 {
			m.openTopic(owed[0].ID)
		}
	}
}

// --- drawing ---

func age(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dp", max(1, int(d.Minutes())))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dg", int(d.Hours()))
	default:
		return fmt.Sprintf("%dn", int(d.Hours()/24))
	}
}

// badge is the header's count of news owed.
func (m Model) badge() string {
	owed := m.owed()
	if len(owed) == 0 {
		return ""
	}
	style := styleAttention
	for _, n := range owed {
		if n.Status == NewsNew {
			style = style.Bold(true)
		}
	}
	return style.Render(fmt.Sprintf("● %d chưa trả lời", len(owed)))
}

// answerLine is the answer waiting out its undo delay, or the one just
// sent.
func (m Model) answerLine() []string {
	if p := m.pending; p != nil {
		left := max(0, int((undoDelay-m.now().Sub(p.at)+time.Second-1)/time.Second))
		return []string{styleSuccess.Render(fmt.Sprintf(" ✓ #%d · %s · u hoàn tác (%ds)", p.id, p.label, left))}
	}
	if m.confirm != "" && m.now().Sub(m.confirmAt) < confirmFor {
		return []string{styleSuccess.Render(" " + m.confirm)}
	}
	return nil
}

// tray is the lines over the composer in the chat: an answer just sent,
// and the newest news owed.
func (m Model) tray(w int) []string {
	out := m.answerLine()
	owed := m.owed()
	if len(owed) == 0 {
		return out
	}
	n := owed[len(owed)-1]
	style := styleAttention
	if n.Status == NewsNew {
		style = style.Bold(true)
	}
	text := fmt.Sprintf(" ● chưa trả lời  #%d ", n.ID)
	if n.Crew != "" {
		text += n.Crew + ": "
	}
	text += topicQuestion(n)
	more := ""
	if len(owed) > 1 {
		more = fmt.Sprintf("+%d · ", len(owed)-1)
	}
	right := styleMuted.Render(more + "^N trả lời ")
	text = ansi.Truncate(text, max(4, w-ansi.StringWidth(right)-2), "…")
	return append(out, spread(style.Render(text), right, w))
}

// topicQuestion is what a frame asks: the news's question, or its title
// when it has none.
func topicQuestion(n News) string {
	if q := strings.TrimSpace(n.Question); q != "" {
		return q
	}
	return n.Title
}

// topicRows is how many lines the page has on screen.
func (m Model) topicRows() int {
	w, h := m.size()
	return max(1, h-len(m.header(w))-len(m.topicBelow(w))-len(m.footer(w)))
}

// topicBelow is the strip under the page: the answer waiting out its
// undo delay.
func (m Model) topicBelow(w int) []string { return append(m.answerLine(), m.backgroundLine(w)...) }

// topicView is the page as rows lines: the frames, scrolled so the frame
// under the cursor shows from its top, and for each line the index of its
// frame in topicList (-1 between frames).
func (m Model) topicView(w, rows int) ([]string, []int) {
	lines, frames := m.topicLines(w)
	sel := m.selected(m.topicList())
	start, end := 0, 0
	for i, f := range frames {
		if f == sel {
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
	at := append([]int(nil), frames[offset:min(len(frames), offset+room)]...)
	if offset+room < len(lines) {
		hint := "▾ cuộn xuống: còn topic đã trả lời"
		owed, _ := m.topics()
		for _, f := range frames[offset+room:] {
			if f >= 0 && f < len(owed) {
				hint = "▾ cuộn xuống: còn topic chưa trả lời"
				break
			}
		}
		view = append(view, " "+styleAccent.Render(hint))
		at = append(at, -1)
	}
	for len(view) < rows {
		view, at = append(view, ""), append(at, -1)
	}
	return view, at
}

// topicLines are every line of the page and the frame each belongs to.
func (m Model) topicLines(w int) ([]string, []int) {
	owed, answered := m.topics()
	list := append(append([]News(nil), owed...), answered...)
	if len(list) == 0 {
		return []string{"", styleMuted.Render(" Chưa có topic nào. Khi crew báo việc cần anh, Mate mở topic ở đây.")}, []int{-1, -1}
	}
	sel := m.selected(list)
	var lines []string
	var frames []int
	add := func(l string, f int) { lines, frames = append(lines, l), append(frames, f) }
	for i, n := range list {
		add("", -1)
		if i == len(owed) {
			add(styleMuted.Render(" Đã trả lời"), -1)
		}
		for _, l := range m.topicFrame(n, i == sel, w) {
			add(l, i)
		}
	}
	return lines, frames
}

// topicFrame is one topic: three lines folded, everything to answer it open.
func (m Model) topicFrame(n News, selected bool, w int) []string {
	open := n.ID == m.openID
	answered := m.answered(n)
	border := styleBorder
	if selected || open {
		border = styleAccent
	}
	inner := max(10, w-4)

	// The top border: the number and crew left, the state right.
	left := styleBold.Render(fmt.Sprintf("#%d", n.ID))
	if answered {
		left = styleMuted.Render(fmt.Sprintf("#%d", n.ID))
	}
	if selected {
		left = styleAccent.Render("▸ ") + left
	}
	if n.Crew != "" {
		left += styleMuted.Render(" · " + n.Crew)
	}
	state := styleAttention.Render("● chưa trả lời")
	since := n.At
	switch {
	case n.Status == NewsDropped:
		state, since = styleMuted.Render("✓ tự đóng"), m.answeredAt(n)
	case answered:
		state, since = styleSuccess.Render("✓ đã trả lời"), m.answeredAt(n)
	}
	if w >= ageFrom {
		state += styleMuted.Render(" · " + age(m.now().Sub(since)))
	}
	if mark := m.sessionMark(n.ID); mark != "" {
		state = mark + styleMuted.Render(" · ") + state
	}
	room := max(4, w-8-ansi.StringWidth(state))
	if ansi.StringWidth(left) > room {
		left = ansi.Truncate(left, room, "…")
	}
	fill := max(1, w-8-ansi.StringWidth(left)-ansi.StringWidth(state))
	out := []string{border.Render("┌─ ") + left + " " + border.Render(strings.Repeat("─", fill)) + " " + state + border.Render(" ─┐")}
	body := func(l string) {
		out = append(out, border.Render("│ ")+l+strings.Repeat(" ", max(0, inner-ansi.StringWidth(l)))+border.Render(" │"))
	}
	question := topicQuestion(n)

	if !open {
		q := ansi.Truncate(strings.Join(strings.Fields(question), " "), inner, "…")
		switch {
		case answered:
			q = styleMuted.Render(q)
		case selected:
			q = styleBold.Render(q)
		}
		body(q)
		return append(out, border.Render("└"+strings.Repeat("─", max(0, w-2))+"┘"))
	}

	for _, l := range wrap(styleBold.Render(question), inner) {
		body(l)
	}
	if len(n.Context) > 0 || len(n.Notes) > 0 {
		body("")
	}
	for _, c := range n.Context {
		for _, l := range wrap(c, max(4, inner-2)) {
			body(styleBorder.Render("│ ") + styleMuted.Render(l))
		}
	}
	for _, note := range n.Notes {
		body(styleMuted.Render(ansi.Truncate(fmt.Sprintf("· %s  %s", note.At.Format("15:04"), note.Text), inner, "…")))
	}
	if len(n.Options) > 0 {
		body("")
		for _, o := range n.Options {
			for _, l := range m.optionLines(n, o, inner) {
				body(l)
			}
		}
	}
	for _, l := range m.exchange(n.ID, inner) {
		body(l)
	}
	if n.Closed() {
		glyph, label := "✓", "Xong"
		if n.Status == NewsDropped {
			label = "Tự đóng"
		}
		body("")
		body(styleMuted.Render(fmt.Sprintf("%s %s · %s", glyph, label, n.At.Format("15:04"))))
	} else {
		body("")
		for _, l := range m.frameInput(inner) {
			body(l)
		}
	}
	return append(out, m.frameBottom(n, border, w))
}

// exchange is the captain's answers to news id and the Mate's replies.
// The app turn that opened the news shows only its reply, when it had one.
// The rounds before the captain's last message are settled: one line each,
// unless ^O unfolded them.
func (m Model) exchange(id, w int) []string {
	var out []string
	thread, from := m.settled(id)
	if folded := foldedLines(m.items, thread[:from], w); len(folded) > 0 {
		out = append(out, "", styleMuted.Render(fmt.Sprintf("%d lượt trước đã xong · ^O mở", len(folded))))
		out = append(out, folded...)
	}
	for _, i := range thread[from:] {
		if lines := m.threadLines(m.items[i], w, i == len(m.items)-1 && m.running == nil); len(lines) > 0 {
			out = append(append(out, ""), lines...)
		}
	}
	if it := m.running; it != nil && it.News == id {
		out = append(append(out, ""), m.threadLines(*it, w, true)...)
	}
	return out
}

// settled is news id's exchange as indexes into m.items, and how many of
// them are settled rounds to fold: those before the captain's last message
// (or the turn of it still running). None while ^O has them open.
func (m Model) settled(id int) (thread []int, from int) {
	for i, it := range m.items {
		if it.News == id {
			thread = append(thread, i)
		}
	}
	if m.roundsOpen {
		return thread, 0
	}
	last := -1
	for j, i := range thread {
		if m.items[i].Source == SourceCaptain {
			last = j
		}
	}
	if r := m.running; r != nil && r.News == id && r.Source == SourceCaptain {
		last = len(thread)
	}
	return thread, max(0, last)
}

// foldedLines are settled rounds of a news's exchange, one line each: the
// captain's message, then the first line of the Mate's reply.
func foldedLines(items []Item, thread []int, w int) []string {
	var out []string
	for _, i := range thread {
		it := items[i]
		reply := ""
		if it.Error == "" && !it.Silent {
			reply = firstLine(strings.TrimSpace(it.Reply))
		}
		var line string
		switch {
		case it.Source == SourceCaptain:
			line = "▸ " + strings.Join(strings.Fields(it.Text), " ")
			if reply != "" {
				line += styleMuted.Render(" → " + reply)
			}
		case reply != "":
			line = styleMuted.Render("▸ ● " + reply)
		default:
			continue
		}
		at := it.At.Format("15:04")
		line = ansi.Truncate(line, max(4, w-len(at)-2), "…")
		out = append(out, spread(line, styleMuted.Render(at), w))
	}
	return out
}

func (m Model) threadLines(it Item, w int, last bool) []string {
	if it.Source == SourceApp {
		if !it.Done {
			return m.liveLines(it, w)
		}
		if it.Silent || it.Reply == "" {
			return nil
		}
		return append(replyLines(it.Reply, w), metaLine(it, w))
	}
	return m.itemLines(it, w, last)
}

// frameInput is the open frame's line to write in: the composer while the
// captain writes, the draft or a hint while not.
func (m Model) frameInput(w int) []string {
	prompt := styleMuted.Render("› ")
	var lines []string
	switch {
	case m.typing:
		lines = m.inputLines(w-2, "Viết câu trả lời…")
	case len(m.input) > 0:
		lines = wrap(string(m.input), w-2)
		if len(lines) > composerRows {
			lines = lines[len(lines)-composerRows:]
		}
	default:
		lines = []string{styleMuted.Render("Tab để gõ câu trả lời tự do…")}
	}
	for i := range lines {
		if i == 0 {
			lines[i] = prompt + lines[i]
		} else {
			lines[i] = "  " + lines[i]
		}
	}
	return lines
}

// frameBottom is the open frame's bottom border, with the keys that work
// in it.
func (m Model) frameBottom(n News, border lipgloss.Style, w int) string {
	var hints []keyHint
	switch {
	case n.Closed():
		hints = []keyHint{{"Enter", "thu gọn"}}
	case m.typing:
		hints = []keyHint{{"Enter", "gửi"}, {"^J", "xuống dòng"}, {"Tab", "ra"}}
	case n.Owed() && len(n.Options) > 0:
		keys := make([]string, len(n.Options))
		for i, o := range n.Options {
			keys[i] = o.Key
		}
		hints = []keyHint{{strings.Join(keys, "/"), "chọn"}, {"Tab", "gõ"}, {"Enter", "thu gọn"}}
	case n.Owed():
		hints = []keyHint{{"Tab", "gõ"}, {"Enter", "thu gọn"}}
	default:
		hints = []keyHint{{"Tab", "ghi thêm"}, {"Enter", "thu gọn"}}
	}
	if !n.Closed() && !m.typing {
		does := "bàn trong session"
		if _, ok := m.findSession(n.ID); ok {
			does = "về session"
		}
		hints = append(hints, keyHint{"f", does})
	}
	if thread, from := m.settled(n.ID); from > 0 || (m.roundsOpen && len(thread) > 1) {
		hints = append(hints, keyHint{"^O", "lượt cũ"})
	}
	text := hintLine(hints, w-6)
	return border.Render("└─ ") + text + " " + border.Render(strings.Repeat("─", max(0, w-5-ansi.StringWidth(text)))+"┘")
}

func (m Model) optionLines(n News, o NewsOption, w int) []string {
	mark := "  "
	if strings.HasPrefix(n.Answer, "["+o.Key+"]") {
		mark = styleSuccess.Render("✓ ")
	}
	right := ""
	if o.Recommended {
		right = "khuyên dùng"
	}
	key := styleAccent.Render("[" + o.Key + "]")
	lines := wrap(o.Text, max(10, w-6-len(right)-2))
	out := make([]string, len(lines))
	for j, l := range lines {
		if j == 0 {
			out[j] = spread(mark+key+" "+l, styleSuccess.Render(right), w)
		} else {
			out[j] = "      " + l
		}
	}
	return out
}
