package matehost

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// A topic's session in the REPL (CON-104, design v2): `f` on the Topic page
// opens it in the chat's place, under a header that names the topic and the
// session and its tokens, with the question pinned below. It is a chat like
// the Mate's, with a composer of its own. Esc goes back to the page and
// leaves it running; the frame and a line over the footer say so. ^X closes
// it without answering, ^C stops its run. Once the session proposes an
// answer (`mate news propose`), a frame over the composer holds it: ^S
// sends it to the Mate as the captain's answer and closes the session; ↑
// puts it in the composer to edit, and ^S then sends the edit.

// sessionDraft keys a session's composer draft in Model.drafts.
const sessionDraft = 1 << 20

func (m Model) findSession(id int) (SessionInfo, bool) {
	for _, s := range m.sessions {
		if s.News == id {
			return s, true
		}
	}
	return SessionInfo{}, false
}

// unseen reports whether news id's session said something the captain has
// not seen.
func (m Model) sessionUnseen(s SessionInfo) bool { return len(s.Items) > m.sessSeen[s.News] }

// openSession opens news id's session and shows it.
func (m *Model) openSession(id int) {
	n, ok := m.findNews(id)
	if !ok {
		return
	}
	if n.Closed() {
		m.note = fmt.Sprintf("#%d đã xong; không mở session nữa", id)
		return
	}
	m.call(func(ctx context.Context, c Conn) tea.Msg {
		return replyMsg{op: callOpenSession, id: id, err: c.OpenSession(ctx, id)}
	})
}

// sessionOpened shows the session the host opened. Its event may come
// after this answer: the view waits for it (sessionShown).
func (m *Model) sessionOpened(r replyMsg) {
	if r.err != nil {
		m.note = failure(r.err)
		return
	}
	m.selID = r.id
	m.switchTo(viewSession, r.id)
	m.scroll, m.unseen = 0, 0
	s, ok := m.findSession(r.id)
	m.sessionShown = ok
	if ok {
		m.seeSession(s)
	}
}

// sessionAsked takes the host's answer to a question to session r.id:
// refused, the question goes back to the composer it was sent from, as
// it stayed there when the host was called in the REPL's own process.
func (m *Model) sessionAsked(r replyMsg) {
	if r.err == nil {
		return
	}
	m.note = failure(r.err)
	if m.mode == viewSession && m.openID == r.id && len(m.input) == 0 {
		m.setInput([]rune(r.text))
	}
}

func (m Model) closeSession(id int) {
	m.call(func(ctx context.Context, c Conn) tea.Msg { return noted(c.CloseSession(ctx, id)) })
}

// sessionKey is a key while a session is shown; handled is false for keys
// the composer and the chat's scrolling take.
func (m Model) sessionKey(k tea.KeyMsg) (Model, bool) {
	id := m.openID
	switch k.Type {
	case tea.KeyEsc:
		m.leaveSession()
	case tea.KeyCtrlX:
		m.closeSession(id)
		m.note = fmt.Sprintf("đã đóng session của #%d; topic vẫn chờ trả lời · f mở lại", id)
		m.leaveSession()
	case tea.KeyCtrlC:
		s, _ := m.findSession(id)
		switch {
		case s.Busy():
			m.note = "đang ngắt lượt của session…"
			m.call(func(ctx context.Context, c Conn) tea.Msg {
				ok, err := c.InterruptSession(ctx, id)
				return replyMsg{op: callInterruptSession, id: id, ok: ok, err: err}
			})
		case len(m.input) > 0:
			m.setInput(nil)
		}
	case tea.KeyEnter:
		if k.Alt {
			return m, false
		}
		text := strings.TrimSpace(string(m.input))
		if text == "" {
			return m, true
		}
		m.setInput(nil)
		m.scroll, m.editProposal = 0, false
		m.call(func(ctx context.Context, c Conn) tea.Msg {
			if err := c.AskSession(ctx, id, text); err != nil {
				return replyMsg{op: callAskSession, id: id, text: text, err: err}
			}
			return nil
		})
	case tea.KeyCtrlS:
		m.settle()
	case tea.KeyUp:
		n, _ := m.findNews(id)
		if len(m.input) > 0 || n.Proposal == "" {
			return m, false
		}
		m.setInput([]rune(n.Proposal))
		m.editProposal = true
	case tea.KeyCtrlR:
		// The chat's retry does not apply to a session.
	default:
		return m, false
	}
	return m, true
}

// settle sends the session's proposal, or the captain's edit of it, as the
// answer to its news, behind the usual undo delay, and closes the session.
func (m *Model) settle() {
	id := m.openID
	n, _ := m.findNews(id)
	text := strings.TrimSpace(n.Proposal)
	if m.editProposal {
		text = strings.TrimSpace(string(m.input))
	}
	if text == "" {
		m.note = "session chưa đề xuất câu trả lời nào; bảo nó đề xuất trước"
		return
	}
	m.setInput(nil)
	m.leaveSession()
	m.answer(id, text, "Đã chốt từ session")
	m.closeSession(id)
}

// seeSession marks what session s said so far as seen.
func (m *Model) seeSession(s SessionInfo) {
	if m.sessSeen == nil {
		m.sessSeen = map[int]int{}
	}
	m.sessSeen[s.News] = len(s.Items)
}

// leaveSession goes back to the Topic page with the session's frame open.
func (m *Model) leaveSession() {
	id := m.openID
	m.editProposal = false
	m.selID = id
	m.switchTo(viewTopics, id)
}

// --- drawing ---

// sessionHeader is the session's title, the captain's count of topics owed,
// its tokens and age, then the question it is about.
func (m Model) sessionHeader(w int) []string {
	s, _ := m.findSession(m.openID)
	n, _ := m.findNews(m.openID)
	left := " " + styleBold.Inherit(styleAccent).Render("mate") + styleMuted.Render(" · "+m.cfg.Project+" › ") +
		styleBold.Inherit(styleAttention).Render(fmt.Sprintf("Topic #%d", n.ID)) + styleMuted.Render(" › session "+s.Label())
	right := []string{}
	if t := s.Tokens(); t > 0 {
		right = append(right, count(t)+" tok")
	}
	if !s.OpenedAt.IsZero() {
		right = append(right, age(m.now().Sub(s.OpenedAt)))
	}
	rightText := styleMuted.Render(strings.Join(right, " · ")) + " "
	if b := m.badge(); b != "" {
		rightText = b + "   " + rightText
	}
	q := ansi.Truncate(strings.Join(strings.Fields(topicQuestion(n)), " "), max(4, w-4), "…")
	return []string{
		spread(left, rightText, w),
		styleBorder.Render(strings.Repeat("─", w)),
		" " + styleBold.Render(q),
		styleBorder.Render(strings.Repeat("─", w)),
	}
}

// sessionConversation is the topic the session is about, in full, then
// what was said in the session shown.
func (m Model) sessionConversation(w int) []string {
	s, _ := m.findSession(m.openID)
	n, _ := m.findNews(m.openID)
	out := topicBrief(n, w)
	if len(s.Items) == 0 && s.Running == nil {
		return append(out, "", styleMuted.Render(" Session này là bản fork của Mate: biết mọi thứ Mate biết, chỉ đọc, không làm gì."),
			styleMuted.Render(" Hỏi gì về topic này cũng được. Mate chính không thấy cuộc bàn này cho tới khi anh chốt."))
	}
	for i, it := range s.Items {
		last := i == len(s.Items)-1 && s.Running == nil
		out = append(append(out, ""), m.itemLines(it, w, last)...)
	}
	if it := s.Running; it != nil {
		out = append(append(out, ""), m.itemLines(*it, w, true)...)
	}
	return out
}

// topicBrief is the topic in full at the top of its session: the question,
// its context and notes, and its options, to read, not to choose (the
// answer goes to the Mate with ^S). The question is also pinned in the
// header, so it stays while this scrolls away.
func topicBrief(n News, w int) []string {
	inner := max(10, w-2)
	out := []string{""}
	for _, l := range wrap(styleBold.Render(topicQuestion(n)), inner) {
		out = append(out, " "+l)
	}
	if len(n.Context) > 0 || len(n.Notes) > 0 {
		out = append(out, "")
	}
	for _, c := range n.Context {
		for _, l := range wrap(c, max(4, inner-2)) {
			out = append(out, " "+styleBorder.Render("│ ")+styleMuted.Render(l))
		}
	}
	for _, note := range n.Notes {
		out = append(out, " "+styleMuted.Render(ansi.Truncate(fmt.Sprintf("· %s  %s", note.At.Format("15:04"), note.Text), inner, "…")))
	}
	if len(n.Options) > 0 {
		out = append(out, "")
		for _, o := range n.Options {
			key := styleMuted.Render(o.Key + ")")
			text := o.Text
			if o.Recommended {
				text += " · " + styleSuccess.Render("khuyên dùng")
			}
			for j, l := range wrap(text, max(4, inner-4)) {
				if j == 0 {
					out = append(out, " "+key+" "+l)
				} else {
					out = append(out, "    "+l)
				}
			}
		}
	}
	return out
}

// proposalFrame is the answer the session proposed, over the composer.
func (m Model) proposalFrame(w int) []string {
	n, ok := m.findNews(m.openID)
	if !ok || n.Proposal == "" {
		return nil
	}
	label := fmt.Sprintf("Chốt cho #%d · chưa gửi", n.ID)
	hints := []keyHint{{"^S", "gửi cho Mate"}, {"↑", "sửa"}}
	if m.editProposal {
		label = fmt.Sprintf("Chốt cho #%d · đang sửa bên dưới", n.ID)
		hints = []keyHint{{"^S", "gửi bản sửa"}}
	}
	inner := max(10, w-4)
	top := styleBorder.Render("┌─ ") + styleBold.Inherit(styleAccent).Render(label) + " " + styleBorder.Render(strings.Repeat("─", max(0, w-5-ansi.StringWidth(label)))+"┐")
	out := []string{"", top}
	for _, l := range wrap(n.Proposal, inner) {
		out = append(out, styleBorder.Render("│ ")+l+strings.Repeat(" ", max(0, inner-ansi.StringWidth(l)))+styleBorder.Render(" │"))
	}
	text := hintLine(hints, w-6)
	return append(out, styleBorder.Render("└─ ")+text+" "+styleBorder.Render(strings.Repeat("─", max(0, w-5-ansi.StringWidth(text)))+"┘"))
}

// sessionStatus is what the session's composer border says it is doing.
func (m Model) sessionStatus() (string, string) {
	s, _ := m.findSession(m.openID)
	if s.Running == nil {
		return styleMuted.Render("○ session rảnh"), ""
	}
	spin := spinnerFrames[m.frame%len(spinnerFrames)]
	what := "session đang trả lời"
	if t := runningToolOf(s.Running); t != nil {
		what += " · " + toolLabel(*t)
	}
	return styleWorking.Render(spin + " " + what + " · " + seconds(m.now().Sub(s.Running.At))), "^C ngắt"
}

func runningToolOf(it *Item) *ToolRecord {
	for i := len(it.Tools) - 1; i >= 0; i-- {
		if t := it.Tools[i]; !t.Done {
			return &t
		}
	}
	return nil
}

// sessionMark is how a frame's border shows its session: running, done
// with something unseen, or open and quiet.
func (m Model) sessionMark(id int) string {
	s, ok := m.findSession(id)
	switch {
	case !ok:
		return ""
	case s.Busy():
		return styleWorking.Render(spinnerFrames[m.frame%len(spinnerFrames)] + " session")
	case m.sessionUnseen(s):
		return styleAttention.Render("● session")
	}
	return styleMuted.Render("session")
}

// backgroundLine is the line over the footer while a session not shown is
// running or has something unseen.
func (m Model) backgroundLine(w int) []string {
	for _, s := range m.sessions {
		if m.mode == viewSession && s.News == m.openID {
			continue
		}
		var text string
		switch {
		case s.Busy():
			what := "nghĩ"
			if t := runningToolOf(s.Running); t != nil {
				what = toolLabel(*t)
			}
			text = fmt.Sprintf(" session %s (#%d) vẫn chạy ngầm: đang %s · f quay lại", s.Label(), s.News, what)
		case m.sessionUnseen(s):
			text = fmt.Sprintf(" session %s (#%d) đã trả lời · f xem", s.Label(), s.News)
		default:
			continue
		}
		return []string{styleMuted.Render(ansi.Truncate(text, w-1, "…"))}
	}
	return nil
}
