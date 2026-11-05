package matehost

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// newsModel is the design's Topic page (v2, t1): two news owed, #3 the
// older, and #1 answered and closed today.
func newsModel(w, h int) Model {
	m, _ := withFake(sampleModel(w, h))
	at := m.now().Add(-4 * time.Minute)
	m.news = []News{
		{ID: 1, Title: "Nhà cung cấp đăng nhập", Question: "Đăng nhập dùng nhà cung cấp nào trước?", Crew: "e1-login-resume", Status: NewsDone, Answer: "[a] Google",
			OpenedAt: at.Add(-time.Hour), At: m.now().Add(-42 * time.Minute)},
		{ID: 3, Title: "Dark mode", Crew: "e1-settings-resume", Kind: "needs-decision", Status: NewsWaiting, OpenedAt: at, At: at,
			Context:  []string{`Crew báo: "dark mode never applies on my clone…"`, "Theme đọc từ THEME trong .env.local. File này không có trong git nên clone mới luôn về light."},
			Question: "Dark mode không áp dụng trên bản clone. Sửa theo cách nào?",
			Options:  []NewsOption{{Key: "a", Text: "Thêm THEME=system vào .env.example, fallback theo hệ thống", Recommended: true}, {Key: "b", Text: "Chuyển cấu hình theme vào config/theme.ts"}, {Key: "c", Text: "Chỉ ghi chú vào README"}}},
		{ID: 4, Title: "PR #12", Question: "PR #12 thanh toán MoMo đã sẵn sàng. Anh review rồi cho merge chứ?", Crew: "e2-payments", Status: NewsNew,
			OpenedAt: m.now().Add(-time.Minute), At: m.now().Add(-time.Minute)},
	}
	return m
}

// lastHeld is the newest item the REPL shows the host holding.
func lastHeld(m Model) (Held, bool) {
	if n := len(m.state.Held); n > 0 {
		return m.state.Held[n-1], true
	}
	return Held{}, false
}

func press(t *testing.T, m *Model, keys ...any) {
	t.Helper()
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k := k.(type) {
		case tea.KeyType:
			msg = tea.KeyMsg{Type: k}
		case string:
			for _, r := range k {
				next, _ := m.key(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
				*m = next.(Model)
				drain(m)
			}
			continue
		}
		next, _ := m.key(msg)
		*m = next.(Model)
		drain(m)
	}
}

// fits fails when a line of the screen is wider than the pane or the
// screen is not its height.
func fits(t *testing.T, name string, m Model) string {
	t.Helper()
	w, h := m.size()
	view := m.View()
	lines := strings.Split(view, "\n")
	if len(lines) != h {
		t.Errorf("%s: %d lines, want %d", name, len(lines), h)
	}
	for i, l := range lines {
		if got := ansi.StringWidth(l); got > w {
			t.Errorf("%s line %d is %d wide: %q", name, i, got, ansi.Strip(l))
		}
	}
	return ansi.Strip(view)
}

func TestREPLTopicPage(t *testing.T) {
	for _, w := range []int{80, 100} {
		m := newsModel(w, 30)
		chat := fits(t, "chat", m)
		for _, want := range []string{"● 2 chưa trả lời", "● chưa trả lời  #4 e2-payments: PR #12 thanh toán MoMo", "+1 · ^N trả lời"} {
			if !strings.Contains(chat, want) {
				t.Errorf("chat@%d lacks %q:\n%s", w, want, chat)
			}
		}

		press(t, &m, tea.KeyCtrlT)
		page := fits(t, "page", m)
		for _, want := range []string{"mate · hellovietnam › Topic", "▸ #3 · e1-settings-resume", "● chưa trả lời", "Dark mode không áp dụng trên bản clone. Sửa theo cách nào?", "#4 · e2-payments", "Đã trả lời", "#1 · e1-login-resume", "✓ đã trả lời", "Đăng nhập dùng nhà cung cấp nào trước?", "Esc về chat"} {
			if !strings.Contains(page, want) {
				t.Errorf("page@%d lacks %q:\n%s", w, want, page)
			}
		}
		// Owed first, oldest first; no frame open; the age only from 90
		// columns.
		if strings.Index(page, "#3 ·") > strings.Index(page, "#4 ·") || strings.Index(page, "#4 ·") > strings.Index(page, "Đã trả lời") {
			t.Errorf("page@%d out of order:\n%s", w, page)
		}
		if strings.Contains(page, "[a]") || strings.Contains(page, "Nhắn cho Mate") {
			t.Errorf("page@%d opened a frame or kept the chat's composer:\n%s", w, page)
		}
		if got := strings.Contains(page, "chưa trả lời · 4p"); got != (w >= ageFrom) {
			t.Errorf("page@%d age shown %v:\n%s", w, got, page)
		}

		// The cursor moves without opening; Enter opens and folds.
		press(t, &m, tea.KeyDown)
		if m.selID != 4 || m.openID != 0 {
			t.Fatalf("↓: cursor #%d, open #%d", m.selID, m.openID)
		}
		press(t, &m, tea.KeyEnter)
		if m.openID != 4 {
			t.Fatalf("Enter opened #%d", m.openID)
		}
		press(t, &m, tea.KeyEnter)
		if m.openID != 0 {
			t.Fatalf("Enter again left #%d open", m.openID)
		}

		// 1 jumps to the first frame and opens it.
		press(t, &m, "1")
		open := fits(t, "open", m)
		if m.selID != 3 || m.openID != 3 {
			t.Fatalf("1: cursor #%d, open #%d", m.selID, m.openID)
		}
		for _, want := range []string{"│ Crew báo:", "[a] Thêm THEME=system", "khuyên dùng", "[c] Chỉ ghi chú vào README", "› Tab để gõ câu trả lời tự do…", "a/b/c chọn · Tab gõ · Enter thu gọn"} {
			if !strings.Contains(open, want) {
				t.Errorf("open@%d lacks %q:\n%s", w, want, open)
			}
		}

		// An option answers: the frame folds into the answered, the cursor
		// goes to the next owed, unopened.
		press(t, &m, "b")
		if p, ok := lastHeld(m); !ok || p.News != 3 || p.Text != "[b] Chuyển cấu hình theme vào config/theme.ts" {
			t.Fatalf("held = %+v", m.state.Held)
		}
		if m.openID != 0 || m.selID != 4 {
			t.Fatalf("after the answer: open #%d, cursor #%d", m.openID, m.selID)
		}
		answered := fits(t, "answered", m)
		if !strings.Contains(answered, "✓ #3 · Đã chọn B · u hoàn tác (5s)") || strings.Index(answered, "#3 ·") < strings.Index(answered, "Đã trả lời") {
			t.Errorf("answered@%d:\n%s", w, answered)
		}
		if !strings.Contains(answered, "● 1 chưa trả lời") {
			t.Errorf("answered@%d still counts #3 owed:\n%s", w, answered)
		}
		// The next letters are keys of the page, not another choice.
		press(t, &m, "a")
		if p, _ := lastHeld(m); len(m.state.Held) != 1 || p.Text != "[b] Chuyển cấu hình theme vào config/theme.ts" {
			t.Fatalf("a after the answer chose again: %+v", m.state.Held)
		}
		// u takes it back from the host; the frame opens to choose again.
		press(t, &m, "u")
		if f := m.conn.(*fakeConn); len(m.state.Held) != 0 || len(f.held) != 0 || m.openID != 3 || len(m.input) != 0 {
			t.Fatalf("u: held %+v (host %+v), open #%d, input %q", m.state.Held, f.held, m.openID, string(m.input))
		}

		// Tab writes in the frame; its draft outlives Esc and ^N comes
		// back to it.
		press(t, &m, tea.KeyTab, "làm a", tea.KeyEsc)
		if m.mode != viewChat || len(m.input) != 0 {
			t.Fatalf("Esc left mode %d input %q", m.mode, string(m.input))
		}
		press(t, &m, tea.KeyCtrlN)
		if m.mode != viewTopics || m.openID != 3 || string(m.input) != "làm a" || m.typing {
			t.Fatalf("^N opened #%d with draft %q, typing %v", m.openID, string(m.input), m.typing)
		}
		if v := fits(t, "draft", m); !strings.Contains(v, "› làm a") {
			t.Errorf("draft@%d not in the frame:\n%s", w, v)
		}
		press(t, &m, tea.KeyTab, tea.KeyEnter)
		if p, ok := lastHeld(m); !ok || p.News != 3 || p.Text != "làm a" || len(m.input) != 0 {
			t.Fatalf("written answer: held %+v, input %q", m.state.Held, string(m.input))
		}
		// u gives a written answer back to its frame's composer.
		press(t, &m, "u")
		if m.openID != 3 || string(m.input) != "làm a" || len(m.state.Held) != 0 {
			t.Fatalf("u on a written answer: open #%d, input %q, held %+v", m.openID, string(m.input), m.state.Held)
		}
		if w == 100 && testing.Verbose() {
			t.Logf("\n%s\n\n%s\n\n%s", chat, page, open)
		}
	}
}

// TestREPLTopicTallFrame: a frame taller than the screen scrolls the page
// so its question stays in view, and says there is more below.
func TestREPLTopicTallFrame(t *testing.T) {
	m := newsModel(80, 14)
	press(t, &m, tea.KeyCtrlT, tea.KeyDown)
	m.openTopic(3)
	m.selID = 3
	view := fits(t, "tall", m)
	for _, want := range []string{"▸ #3", "Dark mode không áp dụng", "▾ cuộn xuống"} {
		if !strings.Contains(view, want) {
			t.Errorf("tall frame lacks %q:\n%s", want, view)
		}
	}
	press(t, &m, tea.KeyDown)
	view = fits(t, "below", m)
	if !strings.Contains(view, "▸ #4") {
		t.Errorf("the cursor's frame is out of view:\n%s", view)
	}
}

// TestREPLTopicClicks: a click opens the frame it lands on, and the
// tray's line in the chat opens the oldest frame owed.
func TestREPLTopicClicks(t *testing.T) {
	m := newsModel(100, 30)
	click := func(y int) {
		next, _ := m.Update(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress, Y: y})
		m = next.(Model)
	}
	row := func(s string) int {
		for i, l := range strings.Split(ansi.Strip(m.View()), "\n") {
			if strings.Contains(l, s) {
				return i
			}
		}
		t.Fatalf("no line has %q:\n%s", s, ansi.Strip(m.View()))
		return -1
	}
	click(row("● chưa trả lời  #4"))
	if m.mode != viewTopics || m.openID != 3 {
		t.Fatalf("the tray opened mode %d #%d", m.mode, m.openID)
	}
	click(row("#4 · e2-payments"))
	if m.openID != 4 || m.selID != 4 {
		t.Fatalf("a click on #4 opened #%d, cursor #%d", m.openID, m.selID)
	}
	click(row("PR #12 thanh toán"))
	if m.openID != 0 {
		t.Fatalf("a second click left #%d open", m.openID)
	}
	next, _ := m.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp, Action: tea.MouseActionPress})
	m = next.(Model)
	if m.selID != 3 || m.scroll != 0 {
		t.Fatalf("wheel up on the page: cursor #%d, chat scroll %d", m.selID, m.scroll)
	}
}

func TestREPLChatNewsReference(t *testing.T) {
	m := newsModel(100, 30)
	press(t, &m, "#3 chọn a", tea.KeyEnter)
	if p, ok := lastHeld(m); !ok || p.News != 3 || !strings.HasPrefix(p.Text, "[a] Thêm THEME") {
		t.Fatalf("held = %+v", m.state.Held)
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "✓ #3 · Đã ghi từ chat · u hoàn tác") {
		t.Errorf("no confirmation in the tray:\n%s", v)
	}
}

// A closed news is read, not answered: its frame takes no letter and no
// writing, and a chat reference to it goes to the Mate as a message.
func TestREPLClosedNewsReadOnly(t *testing.T) {
	m := newsModel(100, 30)
	press(t, &m, tea.KeyCtrlT, "3")
	if m.openID != 1 {
		t.Fatalf("3 opened #%d", m.openID)
	}
	press(t, &m, "a", tea.KeyTab, "x", tea.KeyEnter)
	if m.typing || len(m.input) != 0 || len(m.state.Held) != 0 {
		t.Fatalf("a closed news took input %q, typing %v, held %+v", string(m.input), m.typing, m.state.Held)
	}
	press(t, &m, "3")
	v := fits(t, "closed", m)
	if !strings.Contains(v, "✓ Xong") || strings.Contains(v, "Tab để gõ") {
		t.Errorf("closed frame still offers to write:\n%s", v)
	}
	press(t, &m, tea.KeyEsc)
	if m.chatNewsRef("#1 a") || len(m.state.Held) != 0 || !strings.Contains(m.note, "#1 đã xong") {
		t.Fatalf("a chat reference to a closed news: held %+v, note %q", m.state.Held, m.note)
	}
}

// A news closed before today is archived off the page; one the Mate still
// works on is answered.
func TestREPLTopicGroups(t *testing.T) {
	m := newsModel(100, 30)
	m.news = append(m.news,
		News{ID: 5, Title: "Cũ", Question: "Câu của hôm qua", Status: NewsDone, OpenedAt: m.now().Add(-48 * time.Hour), At: m.now().Add(-30 * time.Hour)},
		News{ID: 6, Title: "PR #64", Question: "Merge PR #64?", Status: NewsWorking, Answer: "merge", OpenedAt: m.now().Add(-time.Hour), At: m.now().Add(-time.Minute)})
	press(t, &m, tea.KeyCtrlT)
	page := fits(t, "groups", m)
	if strings.Contains(page, "#5") || !strings.Contains(page, "#6") {
		t.Errorf("groups:\n%s", page)
	}
	if strings.Index(page, "#6") < strings.Index(page, "Đã trả lời") || strings.Index(page, "#6") > strings.Index(page, "#1 ·") {
		t.Errorf("#6 is not the newest answered:\n%s", page)
	}
}

// A focus event opens its news's frame, from the chat or the page.
func TestREPLOpensTheFocusedNews(t *testing.T) {
	m := newsModel(100, 30)
	m.apply(Event{News: m.news, Focus: 3})
	if m.mode != viewTopics || m.openID != 3 || m.selID != 3 {
		t.Fatalf("focus left mode %d, open #%d, cursor #%d", m.mode, m.openID, m.selID)
	}
}

// TestREPLTopicFoldsSettledRounds is CON-103 in the Topic page: in the open
// frame, the rounds before the captain's last message are one line each,
// and ^O unfolds them - while writing too - until another frame opens.
func TestREPLTopicFoldsSettledRounds(t *testing.T) {
	m := sampleModel(100, 60)
	at := m.now()
	m.news = []News{{ID: 3, Title: "E0: cần bản xuất design", Status: NewsWorking, OpenedAt: at, At: at}}
	m.items = append(m.items,
		Item{At: at, Source: SourceCaptain, News: 3, Text: "đã up hết design lên rồi đó", Done: true,
			Reply: "Cảm ơn anh. Em đã gửi hai điểm cần anh chốt.\n\nĐoạn hai của lượt cũ."},
		Item{At: at, Source: SourceApp, News: 3, Label: "event", Done: true,
			Reply: "Đã giao việc khung bản đồ.\n\nĐoạn hai của tin app cũ."},
		Item{At: at, Source: SourceApp, News: 3, Label: "event", Silent: true, Reply: "—", Done: true},
		Item{At: at, Source: SourceCaptain, News: 3, Text: "ok, anh đã update lại trên linear", Done: true,
			Reply: "Đã giao việc onboard.\n\nPLA-12 vẫn chờ anh chọn cách làm sheet."},
	)
	m.openTopic(3)
	view := ansi.Strip(m.View())
	for _, want := range []string{"2 lượt trước đã xong · ^O mở", "▸ đã up hết design lên rồi đó → Cảm ơn anh.", "▸ ● Đã giao việc khung bản đồ.", "PLA-12 vẫn chờ anh chọn cách làm sheet.", "^O lượt cũ"} {
		if !strings.Contains(view, want) {
			t.Errorf("folded frame lacks %q:\n%s", want, view)
		}
	}
	for _, gone := range []string{"Đoạn hai của lượt cũ.", "Đoạn hai của tin app cũ."} {
		if strings.Contains(view, gone) {
			t.Errorf("folded frame still shows %q:\n%s", gone, view)
		}
	}

	unfolded := func(m Model, how string) {
		t.Helper()
		view := ansi.Strip(m.View())
		for _, want := range []string{"Đoạn hai của lượt cũ.", "Đoạn hai của tin app cũ.", "PLA-12 vẫn chờ anh chọn cách làm sheet."} {
			if !strings.Contains(view, want) {
				t.Errorf("%s did not unfold %q:\n%s", how, want, view)
			}
		}
		if strings.Contains(view, "lượt trước đã xong") {
			t.Errorf("%s: the frame still says rounds are folded:\n%s", how, view)
		}
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	m = next.(Model)
	unfolded(m, "^O")

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = next.(Model)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
	m = next.(Model)
	if !m.typing {
		t.Fatalf("^O while writing left the composer")
	}
	unfolded(m, "^O while writing")

	m.switchTo(viewTopics, 0)
	m.openTopic(3)
	if !strings.Contains(ansi.Strip(m.View()), "2 lượt trước đã xong") {
		t.Errorf("reopening the frame kept its rounds unfolded:\n%s", ansi.Strip(m.View()))
	}
}
