package matehost

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Colours are semantic and adapt to a light or dark terminal; text itself
// keeps the terminal's own colour. Attention is kept for what waits on the
// captain.
var (
	colMuted   = lipgloss.AdaptiveColor{Light: "242", Dark: "245"}
	colFaint   = lipgloss.AdaptiveColor{Light: "248", Dark: "240"}
	colBorder  = lipgloss.AdaptiveColor{Light: "252", Dark: "238"}
	colAccent  = lipgloss.AdaptiveColor{Light: "26", Dark: "75"}
	colWorking = lipgloss.AdaptiveColor{Light: "30", Dark: "80"}
	colSuccess = lipgloss.AdaptiveColor{Light: "28", Dark: "114"}
	colDanger  = lipgloss.AdaptiveColor{Light: "160", Dark: "203"}

	styleMuted   = lipgloss.NewStyle().Foreground(colMuted)
	styleFaint   = lipgloss.NewStyle().Foreground(colFaint)
	styleBorder  = lipgloss.NewStyle().Foreground(colBorder)
	styleAccent  = lipgloss.NewStyle().Foreground(colAccent)
	styleWorking = lipgloss.NewStyle().Foreground(colWorking)
	styleSuccess = lipgloss.NewStyle().Foreground(colSuccess)
	styleDanger  = lipgloss.NewStyle().Foreground(colDanger)
	styleBold    = lipgloss.NewStyle().Bold(true)
	styleCursor  = lipgloss.NewStyle().Reverse(true)
)

// composerRows is the most lines the composer shows.
const composerRows = 8

func (m Model) View() string {
	w, h := m.size()
	out := make([]string, 0, h)
	out = append(out, m.header(w)...)
	if m.mode == viewTopics && !m.help {
		// The Topic page takes the chat's place, composer and all: each
		// frame has its own line to write in.
		view, _ := m.topicView(w, m.topicRows())
		out = append(out, view...)
		out = append(out, m.topicBelow(w)...)
	} else if m.mode == viewWork && !m.help {
		view, _ := m.workView(w, m.topicRows())
		out = append(out, view...)
		out = append(out, m.topicBelow(w)...)
	} else {
		out = append(out, m.chatView(w)...)
		out = append(out, m.below(w)...)
		out = append(out, m.composer(w)...)
	}
	out = append(out, m.footer(w)...)
	for i, l := range out {
		out[i] = ansi.Truncate(l, w, "")
	}
	return strings.Join(out, "\n")
}

// chatView is the conversation's lines on screen, or the help.
func (m Model) chatView(w int) []string {
	room := m.viewRows()
	body := m.body(w)
	scroll := min(m.scroll, max(0, len(body)-room))
	end := len(body) - scroll
	view := body[max(0, end-room):end]
	for len(view) < room {
		view = append([]string{""}, view...)
	}
	if scroll > 0 && !m.help {
		hint := "▾ đang xem tin cũ · End về cuối"
		if m.unseen > 0 {
			hint = fmt.Sprintf("▾ %d tin mới · End", m.unseen)
		}
		view[len(view)-1] = " " + styleAccent.Render(hint)
	}
	return view
}

// trayRow is the screen row of the tray's line for the newest news owed,
// -1 when the chat does not show one.
func (m Model) trayRow() int {
	w, h := m.size()
	if m.mode != viewChat || m.help || len(m.owed()) == 0 {
		return -1
	}
	// The line is the tray's last, over the queue and the composer.
	return h - len(m.footer(w)) - len(m.composer(w)) - len(m.queueLines(w)) - 1
}

func (m Model) size() (int, int) {
	w, h := m.width, m.height
	if w <= 0 {
		w = 80
	}
	if h <= 0 {
		h = 24
	}
	return w, h
}

// viewRows is how many lines the conversation has on screen.
func (m Model) viewRows() int {
	w, h := m.size()
	return max(1, h-len(m.header(w))-len(m.below(w))-len(m.composer(w))-len(m.footer(w)))
}

// maxScroll is how far the chat scrolls up: its top line at the top of
// the screen.
func (m Model) maxScroll() int {
	w, _ := m.size()
	return max(0, len(m.body(w))-m.viewRows())
}

func (m Model) body(w int) []string {
	if m.help {
		return helpLines()
	}
	if m.mode == viewSession {
		return m.sessionConversation(w)
	}
	return m.conversation(w)
}

// --- header and footer ---

func (m Model) header(w int) []string {
	if m.mode == viewSession {
		return m.sessionHeader(w)
	}
	left := " " + styleBold.Inherit(styleAccent).Render("mate") + styleMuted.Render(" · "+m.cfg.Project)
	switch m.mode {
	case viewTopics:
		left += styleMuted.Render(" › ") + styleBold.Inherit(styleAttention).Render("Topic")
	case viewWork:
		left += styleMuted.Render(" › ") + styleBold.Inherit(styleAccent).Render("Work")
	}
	var right []string
	if m.crews >= 0 {
		right = append(right, fmt.Sprintf("crew %d", m.crews))
	}
	if ctx := m.context(); ctx > 0 {
		right = append(right, "ctx "+count(ctx))
	}
	if m.cfg.Model != "" && w >= 90 {
		right = append(right, m.cfg.Model)
	}
	rightText := styleMuted.Render(strings.Join(right, " · ")) + " "
	if b := m.badge(); b != "" {
		rightText = b + "   " + rightText
	}
	line := spread(left, rightText, w)
	return []string{line, styleBorder.Render(strings.Repeat("─", w))}
}

// context is the Mate's context at its last request.
func (m Model) context() int {
	if m.running != nil && m.running.Context > 0 {
		return m.running.Context
	}
	for i := len(m.items) - 1; i >= 0; i-- {
		if m.items[i].Context > 0 {
			return m.items[i].Context
		}
	}
	return 0
}

type keyHint struct{ key, does string }

func (m Model) footer(w int) []string {
	if m.note != "" {
		return []string{" " + styleMuted.Render(m.note)}
	}
	if m.lost {
		return []string{" " + styleDanger.Render(offlineNote)}
	}
	var hints []keyHint
	switch {
	case m.help:
		hints = []keyHint{{"Esc", "đóng"}, {"PgUp PgDn", "cuộn"}}
	case m.mode == viewSession:
		hints = []keyHint{{"Enter", "hỏi"}, {"^J", "xuống dòng"}, {"Esc", "về topic"}, {"^X", "đóng session"}, {"↑↓ PgUp", "cuộn"}}
		if s, ok := m.findSession(m.openID); ok && s.Busy() {
			hints = append(hints[:3], append([]keyHint{{"^C", "ngắt"}}, hints[3:]...)...)
		}
		if n, ok := m.findNews(m.openID); ok && n.Proposal != "" {
			hints = append([]keyHint{{"^S", "chốt"}}, hints...)
		}
	case m.mode == viewTopics && m.typing:
		hints = []keyHint{{"Enter", "gửi"}, {"^J", "xuống dòng"}, {"Tab", "về lựa chọn"}, {"Esc", "về chat"}}
	case m.mode == viewTopics && w >= 100:
		hints = []keyHint{{"↑↓", "chọn"}, {"Enter", "mở/thu gọn"}, {"1–9", "nhảy"}, {"^N", "chưa trả lời tiếp"}, {"Esc", "về chat"}}
	case m.mode == viewTopics:
		hints = []keyHint{{"↑↓", "chọn"}, {"Enter", "mở/thu"}, {"Esc", "về chat"}}
	case m.mode == viewWork:
		hints = []keyHint{{"↑↓", "chọn"}, {"Space", "OK việc xong"}, {"Enter", "lịch sử"}, {"t", "mở topic"}, {"a", "xem hết đã OK"}, {"Esc", "về chat"}}
	case m.state.Busy:
		hints = []keyHint{{"Enter", "xếp hàng"}, {"^X", "bỏ tin cuối"}, {"^C", "ngắt"}, {"?", "phím tắt"}}
		if len(m.input) > 0 {
			hints = append(hints[:1], append([]keyHint{{"^U", "xoá ô"}}, hints[1:]...)...)
		}
	default:
		hints = []keyHint{{"Enter", "gửi"}, {"^J", "xuống dòng"}, {"^T", "topic"}, {"^L", "việc"}, {"↑↓ PgUp", "cuộn"}, {"?", "phím tắt"}}
		if len(m.input) > 0 {
			hints = append(hints[:2], append([]keyHint{{"^U", "xoá ô"}}, hints[2:]...)...)
		}
		if len(m.state.Held) > 0 {
			hints = append([]keyHint{{"u", "hoàn tác"}}, hints...)
		} else if len(m.owed()) > 0 {
			hints = append(hints[:3], append([]keyHint{{"^N", "trả lời"}}, hints[3:]...)...)
		}
		if m.failed() {
			hints = append([]keyHint{{"^R", "thử lại"}}, hints...)
		}
	}
	if m.mode == viewTopics && len(m.state.Held) > 0 && !m.typing {
		hints = append([]keyHint{{"u", "hoàn tác"}}, hints...)
	}
	return []string{" " + hintLine(hints, w-2)}
}

// hintLine is keys and what they do, as many as fit in w columns.
func hintLine(hints []keyHint, w int) string {
	line := ""
	for _, h := range hints {
		part := styleBold.Render(strings.TrimSpace(h.key)) + " " + styleMuted.Render(h.does)
		next := part
		if line != "" {
			next = line + styleMuted.Render(" · ") + part
		}
		if ansi.StringWidth(next) > w {
			break
		}
		line = next
	}
	return line
}

// failed reports whether the last item is a captain message whose turn
// failed, which ^R sends again.
func (m Model) failed() bool {
	if m.running != nil || len(m.items) == 0 {
		return false
	}
	last := m.items[len(m.items)-1]
	return last.Source == SourceCaptain && last.Error != ""
}

// --- the strip under the conversation ---

func (m Model) below(w int) []string {
	if m.mode == viewSession {
		return append(m.proposalFrame(w), m.backgroundLine(w)...)
	}
	out := m.tray(w)
	out = append(out, m.backgroundLine(w)...)
	return append(out, m.queueLines(w)...)
}

// queueLines are the captain's messages waiting for the Mate's turn.
func (m Model) queueLines(w int) []string {
	var out []string
	if n := len(m.queue); n > 0 {
		out = append(out, " "+styleMuted.Render(fmt.Sprintf("Xếp hàng (%d) · gửi lần lượt khi Mate xong lượt này", n)))
		for i, q := range m.queue {
			if i == 3 {
				out = append(out, styleMuted.Render(fmt.Sprintf("  +%d tin nữa", n-3)))
				break
			}
			first, _, _ := strings.Cut(q, "\n")
			out = append(out, styleMuted.Render(ansi.Truncate(fmt.Sprintf("  %d › %s", i+1, first), w-1, "…")))
		}
	}
	return out
}

// --- composer ---

func (m Model) composer(w int) []string {
	inner := max(4, w-4)
	status, right := m.status()
	tail := styleBorder.Render("┐")
	if right != "" {
		tail = " " + styleMuted.Render(right) + styleBorder.Render(" ─┐")
	}
	room := max(0, w-3-1-ansi.StringWidth(tail))
	if ansi.StringWidth(status) > room {
		status = ansi.Truncate(status, room, "…")
	}
	fill := max(0, w-3-ansi.StringWidth(status)-1-ansi.StringWidth(tail))
	// A blank line keeps the composer off the conversation above it.
	out := []string{"", styleBorder.Render("┌─ ") + status + " " + styleBorder.Render(strings.Repeat("─", fill)) + tail}
	hint := "Nhắn cho Mate…"
	switch {
	case m.mode == viewSession:
		hint = "Hỏi tiếp session…"
	case m.state.Busy:
		hint = "Gõ tiếp, tin sẽ xếp hàng…"
	}
	for _, l := range m.inputLines(inner, hint) {
		out = append(out, styleBorder.Render("│ ")+l+strings.Repeat(" ", max(0, inner-ansi.StringWidth(l)))+styleBorder.Render(" │"))
	}
	return append(out, styleBorder.Render("└"+strings.Repeat("─", max(0, w-2))+"┘"))
}

// status is what the composer's border says the Mate is doing, and the key
// that stops it.
func (m Model) status() (string, string) {
	if m.mode == viewSession {
		return m.sessionStatus()
	}
	spin := spinnerFrames[m.frame%len(spinnerFrames)]
	switch {
	case m.state.Stopping:
		return styleMuted.Render("○ Mate đang dừng"), ""
	case !m.state.Busy:
		return styleMuted.Render("○ Mate rảnh"), ""
	}
	since := ""
	if !m.state.TurnStartedAt.IsZero() {
		since = " · " + seconds(m.now().Sub(m.state.TurnStartedAt))
	}
	what := "Mate đang làm"
	if m.state.TurnSource == SourceApp && m.running != nil {
		what = "Mate đang đọc tin " + m.running.Label
	}
	if t := m.runningTool(); t != nil {
		what += " · " + toolLabel(*t)
	}
	return styleWorking.Render(spin + " " + what + since), "^C ngắt"
}

func (m Model) runningTool() *ToolRecord {
	if m.running == nil {
		return nil
	}
	for i := len(m.running.Tools) - 1; i >= 0; i-- {
		if t := m.running.Tools[i]; !t.Done {
			return &t
		}
	}
	return nil
}

// inputLines are the composer's lines, wrapped to width, with the cursor
// drawn on the character it stands on; at most composerRows of them,
// around the cursor. An empty composer shows hint.
func (m Model) inputLines(width int, hint string) []string {
	if len(m.input) == 0 {
		return []string{styleCursor.Render(" ") + styleMuted.Render(ansi.Truncate(hint, width-1, ""))}
	}
	type pos struct{ row, col int }
	var rows [][]rune
	var cur pos
	line, lineW := []rune{}, 0
	for i := 0; i <= len(m.input); i++ {
		if i == m.cursor {
			cur = pos{len(rows), len(line)}
		}
		if i == len(m.input) {
			break
		}
		r := m.input[i]
		if r == '\n' {
			rows, line, lineW = append(rows, line), []rune{}, 0
			continue
		}
		rw := ansi.StringWidth(string(r))
		if lineW+rw > width-1 {
			rows, line, lineW = append(rows, line), []rune{}, 0
			if i == m.cursor {
				cur = pos{len(rows), 0}
			}
		}
		line, lineW = append(line, r), lineW+rw
	}
	rows = append(rows, line)
	out := make([]string, len(rows))
	for i, r := range rows {
		if i != cur.row {
			out[i] = string(r)
			continue
		}
		under, rest := " ", ""
		if cur.col < len(r) {
			under, rest = string(r[cur.col]), string(r[cur.col+1:])
		}
		out[i] = string(r[:cur.col]) + styleCursor.Render(under) + rest
	}
	start := max(0, cur.row-composerRows+1)
	return out[start:min(len(out), start+composerRows)]
}

// --- conversation ---

func (m Model) conversation(w int) []string {
	var out []string
	add := func(lines []string) {
		if len(lines) == 0 {
			return
		}
		out = append(out, "")
		out = append(out, lines...)
	}
	for i, it := range m.items {
		if !it.InChat() {
			continue
		}
		last := i == len(m.items)-1 && m.running == nil
		add(m.itemLines(it, w, last))
	}
	if it := m.running; it != nil && it.Source == SourceCaptain {
		add(m.itemLines(*it, w, true))
	}
	return out
}

func (m Model) itemLines(it Item, w int, last bool) []string {
	var out []string
	if it.Source == SourceCaptain {
		out = append(captainLines(it, w), "")
	} else {
		out = []string{styleFaint.Render(fmt.Sprintf("   · %s · %s", it.Label, it.At.Format("15:04")))}
	}
	switch {
	case !it.Done:
		return append(out, m.liveLines(it, w)...)
	case it.Error != "":
		out = append(out, styleDanger.Render(ansi.Truncate(fmt.Sprintf(" ✗ Lượt này lỗi · %s · %s", firstLine(it.Error), endAt(it).Format("15:04")), w-1, "…")))
		if last && it.Source == SourceCaptain {
			out = append(out, styleMuted.Render("   Tin của anh vẫn còn · ^R thử lại"))
		}
		return out
	}
	if it.Reply != "" {
		out = append(out, replyLines(it.Reply, w)...)
	}
	if it.Interrupted {
		return append(out, styleMuted.Render(fmt.Sprintf("   ○ Anh đã ngắt · %s · phần trả lời trên chưa đầy đủ", seconds(time.Duration(it.MS)*time.Millisecond))))
	}
	return append(out, metaLine(it, w))
}

// liveLines are a running turn: the message it is writing, with the
// cursor at its end. What it wrote before its last tool, and the tools,
// are its own work; the composer's border names the tool.
func (m Model) liveLines(it Item, w int) []string {
	said := strings.TrimSpace(it.Reply[min(it.Lead, len(it.Reply)):])
	if said == "" {
		spin := spinnerFrames[m.frame%len(spinnerFrames)]
		return []string{styleWorking.Render(" "+spin) + styleMuted.Render(" đang nghĩ…")}
	}
	out := replyLines(said, w)
	out[len(out)-1] += styleAccent.Render("▌")
	return out
}

func captainLines(it Item, w int) []string {
	at := it.At.Format("15:04")
	text := wrap(it.Text, max(10, w-3-len(at)-3))
	out := make([]string, len(text))
	for i, l := range text {
		if i == 0 {
			out[i] = spread(styleMuted.Render(" › ")+l, styleMuted.Render(at)+" ", w)
		} else {
			out[i] = "   " + l
		}
	}
	return out
}

// replyLines are the Mate's words: a dot, then its markdown, indented.
func replyLines(reply string, w int) []string {
	lines := markdown(reply, max(10, w-4))
	for i := range lines {
		if i == 0 {
			lines[i] = styleAccent.Render(" ● ") + lines[i]
		} else {
			lines[i] = "   " + lines[i]
		}
	}
	return lines
}

// metaLine closes a finished turn: how long it took, its tokens, and the
// time it ended.
func metaLine(it Item, w int) string {
	var parts []string
	if it.MS > 0 {
		parts = append(parts, seconds(time.Duration(it.MS)*time.Millisecond))
	}
	if it.Tokens > 0 {
		parts = append(parts, count(it.Tokens)+" tok")
	}
	at := endAt(it).Format("15:04") + " "
	left := ansi.Truncate("   "+strings.Join(parts, " · "), max(4, w-len(at)-2), "…")
	return spread(styleMuted.Render(left), styleMuted.Render(at), w)
}

func toolLabel(t ToolRecord) string {
	if t.Detail == "" {
		return t.Name
	}
	return t.Name + ": " + t.Detail
}

// --- help ---

func helpLines() []string {
	rows := [][2]string{
		{"Enter", "gửi (Mate bận: xếp hàng)"},
		{"^J · Alt+Enter", "xuống dòng"},
		{"^U · ⌘⌫", "xoá sạch ô nhập"},
		{"^W · ⌥⌫", "xoá một từ"},
		{"^A ^E ^K", "đầu dòng · cuối dòng · xoá tới cuối dòng"},
		{"^C", "Mate bận: ngắt lượt · có chữ: xoá ô"},
		{"Esc", "ngắt lượt đang chạy"},
		{"^X", "bỏ tin xếp hàng cuối"},
		{"^R", "thử lại lượt lỗi"},
		{"^O (Topic)", "mở/thu gọn lượt cũ trong khung"},
		{"^T", "trang Topic: mỗi câu hỏi một khung"},
		{"^N", "mở topic chưa trả lời tiếp theo"},
		{"^L", "trang Work: bảng việc của Mate"},
		{"Space · t (Work)", "OK việc xong · mở topic dòng đang chờ"},
		{"Enter · a (Work)", "lịch sử dòng · xem hết việc đã OK"},
		{"#3 …", "trả lời topic #3 ngay từ chat"},
		{"↑↓ · j k (Topic)", "dời con trỏ · Enter/Space mở, thu"},
		{"1–9 (Topic)", "nhảy tới khung thứ n và mở"},
		{"a b c d (khung mở)", "chọn đáp án · Tab gõ tự do"},
		{"f (Topic)", "bàn topic trong session: fork chỉ đọc của Mate"},
		{"Esc · ^X (session)", "về Topic, session chạy tiếp · đóng session"},
		{"u", "hoàn tác câu trả lời trong 5 giây"},
		{"PgUp PgDn · ⇧↑ ⇧↓", "cuộn trang · dòng"},
		{"↑ ↓ (ô trống)", "cuộn một dòng"},
		{"bánh xe chuột", "cuộn chat · dời con trỏ Topic"},
		{"click", "mở khung topic · dòng khay"},
		{"⌥/⇧ + kéo chuột", "bôi đen chữ để copy"},
		{"Home End (ô trống)", "lên đầu · về cuối"},
		{"? · F1", "bảng phím này"},
	}
	out := []string{"", " " + styleBold.Render("Phím tắt"), ""}
	for _, r := range rows {
		out = append(out, fmt.Sprintf("   %s%s%s", styleBold.Render(r[0]), strings.Repeat(" ", max(1, 22-ansi.StringWidth(r[0]))), styleMuted.Render(r[1])))
	}
	return out
}

// --- text ---

var (
	mdBold   = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	mdCode   = regexp.MustCompile("`([^`]+)`")
	mdBullet = regexp.MustCompile(`^(\s*)[-*] (.*)$`)
	mdNumber = regexp.MustCompile(`^(\s*)(\d+[.)]) (.*)$`)
	mdHead   = regexp.MustCompile(`^#{1,6} (.*)$`)
)

// markdown renders the little markdown a reply has: headings and bold in
// bold, code in the accent colour, bullets as dots with a hanging indent,
// fenced code muted and unwrapped. Lines are wrapped to width.
func markdown(text string, width int) []string {
	var out []string
	fence := false
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fence = !fence
			continue
		}
		if fence {
			out = append(out, styleMuted.Render(ansi.Truncate("  "+line, width, "…")))
			continue
		}
		hang, body := "", line
		if g := mdHead.FindStringSubmatch(line); g != nil {
			body = styleBold.Render(g[1])
		} else if g := mdBullet.FindStringSubmatch(line); g != nil {
			hang, body = g[1]+"  ", g[1]+"• "+inline(g[2])
		} else if g := mdNumber.FindStringSubmatch(line); g != nil {
			hang, body = g[1]+strings.Repeat(" ", len(g[2])+1), g[1]+g[2]+" "+inline(g[3])
		} else {
			body = inline(line)
		}
		for i, l := range wrap(body, max(4, width-len(hang))) {
			if i > 0 {
				l = hang + l
			}
			out = append(out, l)
		}
	}
	if len(out) == 0 {
		out = []string{""}
	}
	return out
}

func inline(s string) string {
	s = mdBold.ReplaceAllStringFunc(s, func(b string) string { return styleBold.Render(b[2 : len(b)-2]) })
	return mdCode.ReplaceAllStringFunc(s, func(c string) string { return styleAccent.Render(c[1 : len(c)-1]) })
}

// wrap breaks s at words to width columns; a word longer than width is
// broken where it must.
func wrap(s string, width int) []string {
	return strings.Split(ansi.Wrap(s, width, ""), "\n")
}

// spread puts right at the right edge of a line of width w after left.
func spread(left, right string, w int) string {
	gap := w - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		return left
	}
	return left + strings.Repeat(" ", gap) + right
}

func endAt(it Item) time.Time { return it.At.Add(time.Duration(it.MS) * time.Millisecond) }

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

func seconds(d time.Duration) string {
	switch {
	case d < 10*time.Second:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	default:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
}

func count(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	default:
		return fmt.Sprint(n)
	}
}
