package matehost

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func sampleModel(w, h int) Model {
	at := time.Date(2026, 10, 15, 14, 2, 0, 0, time.Local)
	items := []Item{
		{At: at, Source: SourceCaptain, Text: "ok anh đã có 2 test user là gmail của anh.", Done: true,
			Reply: "Em đã ghi nhận việc anh thêm 2 Gmail làm test user. Phía Google đủ để bắt đầu rồi. Em đã đánh thức crew để chạy lại luồng OAuth.",
			Tools: []ToolRecord{{Name: "Bash", Detail: "mate wake hellovietnam", Done: true, MS: 900}}, MS: 4100, Tokens: 1200, Context: 84000},
		{At: at.Add(time.Minute), Source: SourceApp, Label: "digest", Text: Marker + "digest: 1 item(s) — e1 budget 50%: 10.1M of 20M — status files under /x; act per AGENTS.md section 10", Reply: "—", Silent: true, Done: true, MS: 2000},
		{At: at.Add(3 * time.Minute), Source: SourceCaptain, Text: "Trong lúc chờ, mình bàn màn Hồ sơ nhé. Anh muốn có ảnh đại diện và tên hiển thị, chưa cần đổi mật khẩu.", Done: true,
			Reply: "Được ạ. Em đề xuất màn **Hồ sơ** gồm:\n- Ảnh đại diện: chọn từ máy, cắt vuông, tối đa 2 MB\n- Tên hiển thị: 2–40 ký tự, cho phép tiếng Việt có dấu\n- Email: chỉ đọc, lấy từ `auth.users`\n\nAnh muốn em tạo task cho crew mới luôn, hay chờ màn đăng nhập merge xong rồi mới làm?", MS: 2800, Tokens: 900},
		{At: at.Add(19 * time.Minute), Source: SourceCaptain, Text: "Tóm tắt giúp anh các crew đang chạy.", Done: true, Error: "exit status 1: API Error: 529 overloaded"},
	}
	m := NewModel(nil, Config{Project: "hellovietnam", Model: "opus"}, items)
	m.now = func() time.Time { return at.Add(20 * time.Minute) }
	m.crews = 3
	m.width, m.height = w, h
	return m
}

func busyModel(w, h int) Model {
	m := sampleModel(w, h)
	m.items = m.items[:3]
	start := m.now().Add(-7 * time.Second)
	m.state = State{Busy: true, TurnSource: SourceCaptain, TurnStartedAt: start}
	m.running = &Item{At: start, Source: SourceCaptain, Text: "Làm luôn đi, tách thành task riêng.",
		Reply: "Em tạo task e1-profile gồm 3 phần như trên và giao cho một crew mới. Crew làm trên nhánh riêng nên không đụng vào",
		Tools: []ToolRecord{{ID: "1", Name: "Bash", Detail: "mate task new e1-profile --from-chat", Done: true, MS: 800}, {ID: "2", Name: "Bash", Detail: "mate crew spawn e1-profile", Started: start.Add(4 * time.Second)}}}
	m.queue = []string{`à nhớ đổi tên hiển thị của app thành "Hello Vietnam" nhé`, "xong thì tóm tắt giúp anh các crew đang chạy"}
	m.input = []rune("Ảnh đại diện lưu ở đâu em,\nSupabase Storage hay S3?")
	m.cursor = len(m.input)
	return m
}

// TestREPLFitsTheWidth draws the design's screens at 80 and 100 columns:
// no line may be wider than the pane, and the screen is exactly its height.
func TestREPLFitsTheWidth(t *testing.T) {
	for _, w := range []int{80, 100, 60} {
		help := sampleModel(w, 30)
		help.help = true
		scrolled := sampleModel(w, 30)
		scrolled.wheel(1)
		for name, m := range map[string]Model{"idle": sampleModel(w, 30), "busy": busyModel(w, 30), "help": help, "scrolled": scrolled} {
			view := m.View()
			lines := strings.Split(view, "\n")
			if len(lines) != 30 {
				t.Errorf("%s@%d: %d lines, want 30", name, w, len(lines))
			}
			for i, l := range lines {
				if got := ansi.StringWidth(l); got > w {
					t.Errorf("%s@%d line %d is %d wide: %q", name, w, i, got, ansi.Strip(l))
				}
			}
			if os.Getenv("MATE_REPL_SHOW") != "" {
				t.Logf("%s@%d\n%s", name, w, ansi.Strip(view))
			}
		}
	}
}

func TestREPLScreens(t *testing.T) {
	idle := ansi.Strip(sampleModel(100, 40).View())
	for _, want := range []string{"mate · hellovietnam", "crew 3 · ctx 84.0k · opus", "› ok anh đã có 2 test user", "● Em đã ghi nhận", "4.1s · 1.2k tok", "• Ảnh đại diện", "✗ Lượt này lỗi · exit status 1: API Error: 529 overloaded", "^R thử lại", "○ Mate rảnh"} {
		if !strings.Contains(idle, want) {
			t.Errorf("idle screen lacks %q:\n%s", want, idle)
		}
	}
	for _, not := range []string{"· digest ·", "tool ·", "mate wake"} {
		if strings.Contains(idle, not) {
			t.Errorf("idle screen shows %q, which is not the conversation:\n%s", not, idle)
		}
	}
	busy := ansi.Strip(busyModel(100, 40).View())
	for _, want := range []string{"đụng vào▌", "Xếp hàng (2)", `1 › à nhớ đổi tên`, "Mate đang làm · Bash: mate crew spawn e1-profile · 7.0s", "^C ngắt", "Supabase Storage hay S3?"} {
		if !strings.Contains(busy, want) {
			t.Errorf("busy screen lacks %q:\n%s", want, busy)
		}
	}
	if strings.Contains(busy, "mate task new") {
		t.Errorf("the running turn's tools entered the conversation:\n%s", busy)
	}
}

func TestREPLComposerKeys(t *testing.T) {
	m, f := withFake(NewModel(nil, Config{Project: "p"}, nil))
	f.queue = []string{"first", "second"}
	press := func(k tea.KeyMsg) {
		next, _ := m.key(k)
		m = next.(Model)
		drain(&m)
	}
	for _, r := range "xin chào" {
		press(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	press(tea.KeyMsg{Type: tea.KeyCtrlJ})
	for _, r := range "dòng hai" {
		press(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	press(tea.KeyMsg{Type: tea.KeyCtrlW})
	press(tea.KeyMsg{Type: tea.KeyUp})
	press(tea.KeyMsg{Type: tea.KeyCtrlK})
	if got := string(m.input); got != "xin c\ndòng " {
		t.Fatalf("input = %q", got)
	}
	if draft, _ := f.state(); draft != "xin c\ndòng " {
		t.Fatalf("draft = %q", draft)
	}
	press(tea.KeyMsg{Type: tea.KeyBackspace, Alt: true})
	if got := string(m.input); got != "xin \ndòng " {
		t.Fatalf("⌥⌫ left %q", got)
	}
	press(tea.KeyMsg{Type: tea.KeyCtrlU})
	if draft, _ := f.state(); len(m.input) != 0 || draft != "" {
		t.Fatalf("^U left %q", string(m.input))
	}
	for _, r := range "lại" {
		press(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	press(tea.KeyMsg{Type: tea.KeyCtrlC})
	if len(m.input) != 0 {
		t.Fatalf("^C left %q", string(m.input))
	}
	press(tea.KeyMsg{Type: tea.KeyUp})
	if _, queue := f.state(); string(m.input) != "" || len(queue) != 2 {
		t.Fatalf("↑ on an empty composer filled %q, queue %q", string(m.input), queue)
	}
	press(tea.KeyMsg{Type: tea.KeyCtrlX})
	press(tea.KeyMsg{Type: tea.KeyCtrlX})
	if _, queue := f.state(); len(queue) != 0 {
		t.Fatalf("^X left %q", queue)
	}
	press(tea.KeyMsg{Type: tea.KeyCtrlX})
	if m.note != "không có tin nào đang xếp hàng" {
		t.Fatalf("^X on an empty queue: note %q", m.note)
	}
}

// TestREPLMouseWheel: the wheel scrolls the chat and never fills the
// composer, which the ↑ keys a terminal makes of the wheel once did.
func TestREPLMouseWheel(t *testing.T) {
	m, f := withFake(sampleModel(80, 12))
	f.queue = []string{"queued"}
	wheel := func(b tea.MouseButton) {
		next, _ := m.Update(tea.MouseMsg{Button: b, Action: tea.MouseActionPress})
		m = next.(Model)
	}
	bottom := ansi.Strip(m.View())
	next, _ := m.key(tea.KeyMsg{Type: tea.KeyUp})
	m = next.(Model)
	if m.scroll != 1 || len(m.input) != 0 {
		t.Fatalf("↑ on an empty composer: scroll %d, input %q", m.scroll, string(m.input))
	}
	next, _ = m.key(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	wheel(tea.MouseButtonWheelUp)
	wheel(tea.MouseButtonWheelUp)
	if _, queue := f.state(); m.scroll != 2*wheelLines || len(m.input) != 0 || len(queue) != 1 {
		t.Fatalf("wheel up: scroll %d, input %q, queue %q", m.scroll, string(m.input), queue)
	}
	up := ansi.Strip(m.View())
	if up == bottom || !strings.Contains(up, "đang xem tin cũ") {
		t.Errorf("wheel up did not scroll the chat:\n%s", up)
	}
	next, _ = m.Update(tea.MouseMsg{Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	m = next.(Model)
	if m.scroll != 2*wheelLines {
		t.Fatalf("a click scrolled to %d", m.scroll)
	}
	for range 3 {
		wheel(tea.MouseButtonWheelDown)
	}
	if m.scroll != 0 || ansi.Strip(m.View()) != bottom {
		t.Fatalf("wheel down left scroll %d", m.scroll)
	}

	// Past the top the wheel stops, so one notch down moves at once.
	for range 50 {
		wheel(tea.MouseButtonWheelUp)
	}
	top := ansi.Strip(m.View())
	if m.scroll != m.maxScroll() || m.scroll == 0 {
		t.Fatalf("50 notches up: scroll %d, top at %d", m.scroll, m.maxScroll())
	}
	wheel(tea.MouseButtonWheelDown)
	if ansi.Strip(m.View()) == top {
		t.Fatalf("one notch down after overscrolling did not move the view (scroll %d)", m.scroll)
	}
	next, _ = m.key(tea.KeyMsg{Type: tea.KeyHome})
	m = next.(Model)
	if m.scroll != m.maxScroll() {
		t.Fatalf("Home scrolled to %d, top at %d", m.scroll, m.maxScroll())
	}
	next, _ = m.key(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	if ansi.Strip(m.View()) == top {
		t.Fatal("↓ after Home did not move the view")
	}
}

// TestREPLChatIsTheConversation: the chat is the captain's messages and
// the Mate's replies (CON-151). App turns stay out, whether the Mate had
// something to say or saved itself before a restart.
func TestREPLChatIsTheConversation(t *testing.T) {
	m := sampleModel(80, 30)
	m.items = append(m.items[:3],
		Item{At: m.now(), Source: SourceApp, Label: "digest", Text: Marker + "digest: e1 merged", Reply: "HU-165 is done and set to Done in Linear.", Done: true, MS: 4000},
		Item{At: m.now(), Source: SourceApp, Label: "stow", Text: Marker + "stow: you are about to be restarted",
			Reply: "Everything is saved, so a restart loses nothing this session knew.", Done: true, MS: 9000},
		Item{At: m.now(), Source: SourceApp, Label: "event", Text: Marker + "event: e1 failed", Done: true, Error: "exit status 1"})
	for _, view := range []string{ansi.Strip(m.View()), func() string {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlO})
		return ansi.Strip(next.(Model).View())
	}()} {
		for _, not := range []string{"HU-165", "Everything is saved", "ghi nhớ", "digest", "lỗi"} {
			if strings.Contains(view, not) {
				t.Fatalf("an app turn entered the chat (%q):\n%s", not, view)
			}
		}
	}
}

// TestREPLLiveShowsTheMessageInProgress: a running turn shows what it
// writes after its last tool, not what it wrote before it.
func TestREPLLiveShowsTheMessageInProgress(t *testing.T) {
	m := busyModel(100, 40)
	m.running.Reply = "Em đang xem lại kế hoạch.\n\nĐã tạo task e1-profile"
	m.running.Lead = len("Em đang xem lại kế hoạch.\n\n")
	view := ansi.Strip(m.View())
	if strings.Contains(view, "xem lại kế hoạch") || !strings.Contains(view, "Đã tạo task e1-profile▌") {
		t.Fatalf("the live turn shows its narration:\n%s", view)
	}
	m.running.Reply = m.running.Reply[:m.running.Lead]
	if view := ansi.Strip(m.View()); !strings.Contains(view, "đang nghĩ…") {
		t.Fatalf("a turn between messages does not say it is thinking:\n%s", view)
	}
}

// TestREPLHoldsTheReadPlace: while the captain reads up the chat, a turn
// that ends or grows below does not move what they read.
func TestREPLHoldsTheReadPlace(t *testing.T) {
	m := sampleModel(80, 16)
	m.scrollBy(4)
	top := func(m Model) string { return ansi.Strip(strings.Split(m.View(), "\n")[2]) }
	before := top(m)
	at := m.now()
	m.apply(Event{Item: &Item{At: at, Source: SourceCaptain, Text: "đang chạy", Reply: "một"}})
	if got := top(m); got != before {
		t.Fatalf("a running turn moved the view: %q, was %q", got, before)
	}
	m.apply(Event{Item: &Item{At: at, Source: SourceCaptain, Text: "đang chạy", Reply: "một\n\nhai\n\nba\n\nbốn"}})
	if got := top(m); got != before {
		t.Fatalf("a growing turn moved the view: %q, was %q", got, before)
	}
	m.apply(Event{Item: &Item{At: at, Source: SourceApp, Label: "event", Reply: "Crew e1 xong.", Done: true}})
	if m.unseen != 0 {
		t.Fatalf("an app turn counted as unseen: %d", m.unseen)
	}
	m.apply(Event{Item: &Item{At: at, Source: SourceCaptain, Text: "đang chạy", Reply: "một\n\nhai\n\nba\n\nbốn", Done: true}})
	if got := top(m); got != before {
		t.Fatalf("a finished turn moved the view: %q, was %q", got, before)
	}
	if m.unseen != 1 {
		t.Fatalf("unseen = %d, want 1", m.unseen)
	}

	// At the end the chat still follows what comes.
	m.scrollBy(-1 << 30)
	m.apply(Event{Item: &Item{At: at, Source: SourceCaptain, Text: "nữa", Reply: "Tin cuối cùng", Done: true}})
	if view := ansi.Strip(m.View()); !strings.Contains(view, "Tin cuối cùng") {
		t.Fatalf("the chat did not follow at its end:\n%s", view)
	}
}
