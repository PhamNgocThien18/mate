package matehost

import (
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// sessionModel is newsModel with a host that can open sessions.
func sessionModel(w, h int) Model {
	m := newsModel(w, h)
	m.conn.(*fakeConn).forks = true
	return m
}

// refresh gives the model the host's sessions, as its event would.
func refresh(m *Model) {
	f := m.conn.(*fakeConn)
	f.mu.Lock()
	sessions := slices.Clone(f.sessions)
	f.mu.Unlock()
	m.applyEvent(Event{News: m.news, Work: m.work, Queue: m.queue, State: m.state, Sessions: sessions})
}

// The design's session (v2): f on a frame opens its session in the chat's
// place, the topic and session in the header, the question pinned under
// it, a composer of its own; Esc goes back to the page and leaves it
// running, which the frame and a line over the footer show.
func TestREPLSession(t *testing.T) {
	for _, w := range []int{80, 100} {
		m := sessionModel(w, 30)
		press(t, &m, tea.KeyCtrlT, tea.KeyEnter)
		page := fits(t, "frame", m)
		if !strings.Contains(page, "f bàn trong session") {
			t.Errorf("frame@%d does not offer a session:\n%s", w, page)
		}
		press(t, &m, "f")
		refresh(&m)
		if m.mode != viewSession || m.openID != 3 {
			t.Fatalf("f@%d: mode %v, news #%d", w, m.mode, m.openID)
		}
		empty := fits(t, "session", m)
		// The topic in full opens the session (CON-122): the question, its
		// context and its options, to read; the chat's composer and the
		// page's choosable options stay out.
		for _, want := range []string{"mate · hellovietnam › Topic #3 › session s-mới", "Dark mode không áp dụng trên bản clone. Sửa theo cách nào?",
			"Theme đọc từ THEME trong .env.local", "a) Thêm THEME=system vào .env.example", "khuyên dùng", "c) Chỉ ghi chú vào README",
			"bản fork của Mate", "Hỏi tiếp session…", "○ session rảnh", "Esc về topic", "^X đóng session"} {
			if !strings.Contains(empty, want) {
				t.Errorf("session@%d lacks %q:\n%s", w, want, empty)
			}
		}
		if strings.Contains(empty, "Nhắn cho Mate") || strings.Contains(empty, "[a]") {
			t.Errorf("session@%d shows the chat's composer or the options:\n%s", w, empty)
		}

		// A run in flight, with one exchange before it.
		f := m.conn.(*fakeConn)
		f.session(3, func(s *SessionInfo) {
			s.Fork = "ab12cd34"
			s.Items = []Item{{At: m.now(), Source: SourceCaptain, Text: "fallback nghĩa là gì?", Reply: "Nghĩa là theo **hệ thống** khi THEME trống.", Done: true, Tokens: 1500}}
			s.Running = &Item{At: m.now(), Source: SourceCaptain, Text: "vậy a ổn chứ?", Tools: []ToolRecord{{Name: "Read", Detail: ".env.example", Started: m.now()}}}
		})
		refresh(&m)
		live := fits(t, "running", m)
		for _, want := range []string{"session s-ab12", "1.5k tok", "fallback nghĩa là gì?", "Nghĩa là theo hệ thống", "vậy a ổn chứ?", "session đang trả lời · Read: .env.example", "^C ngắt"} {
			if !strings.Contains(live, want) {
				t.Errorf("running@%d lacks %q:\n%s", w, want, live)
			}
		}

		press(t, &m, tea.KeyEsc)
		if m.mode != viewTopics || m.openID != 3 {
			t.Fatalf("Esc@%d: mode %v, open #%d", w, m.mode, m.openID)
		}
		back := fits(t, "back", m)
		for _, want := range []string{"session · ● chưa trả lời", "vẫn chạy ngầm: đang Read: .env.example · f quay lại", "f về session"} {
			if !strings.Contains(back, want) {
				t.Errorf("page@%d lacks %q:\n%s", w, want, back)
			}
		}

		// Done while the captain is away: the frame says so until reopened.
		f.session(3, func(s *SessionInfo) {
			s.Items = append(s.Items, Item{At: m.now(), Source: SourceCaptain, Text: "vậy a ổn chứ?", Reply: "Ổn.", Done: true})
			s.Running = nil
		})
		refresh(&m)
		if done := fits(t, "done", m); !strings.Contains(done, "● session") || !strings.Contains(done, "đã trả lời · f xem") {
			t.Errorf("page@%d does not show the session's unseen reply:\n%s", w, done)
		}
		press(t, &m, "f")
		refresh(&m)
		press(t, &m, tea.KeyEsc)
		if again := fits(t, "seen", m); strings.Contains(again, "● session") {
			t.Errorf("page@%d still marks a seen session:\n%s", w, again)
		}

		// ^X closes it without answering.
		press(t, &m, "f", tea.KeyCtrlX)
		refresh(&m)
		if m.mode != viewTopics || len(m.sessions) != 0 || !strings.Contains(m.note, "đã đóng session của #3") {
			t.Fatalf("^X@%d: mode %v, %d sessions, note %q", w, m.mode, len(m.sessions), m.note)
		}
		if n, _ := m.findNews(3); !n.Owed() {
			t.Fatalf("closing the session answered #3")
		}
	}
}

func TestREPLSessionRefusals(t *testing.T) {
	m := newsModel(100, 30)
	press(t, &m, tea.KeyCtrlT, tea.KeyEnter, "f")
	if m.mode != viewTopics || !strings.Contains(m.note, "không mở được session") {
		t.Fatalf("a host without forks: mode %v, note %q", m.mode, m.note)
	}
	m = sessionModel(100, 30)
	press(t, &m, tea.KeyCtrlT, tea.KeyDown, tea.KeyDown, tea.KeyEnter)
	if m.openID != 1 {
		t.Fatalf("open #%d, want the closed #1", m.openID)
	}
	press(t, &m, "f")
	if m.mode != viewTopics || !strings.Contains(m.note, "#1 đã xong") {
		t.Fatalf("f on a closed news: mode %v, note %q", m.mode, m.note)
	}
}

// Settling (CON-104 PR3): the session proposes the answer with `mate news
// propose`; ^S sends it to the Mate as the captain's answer, held for its
// undo, and closes the session. ↑ edits it first.
func TestREPLSessionSettles(t *testing.T) {
	m := sessionModel(100, 30)
	press(t, &m, tea.KeyCtrlT, tea.KeyEnter, "f")
	refresh(&m)
	press(t, &m, tea.KeyCtrlS)
	if m.mode != viewSession || !strings.Contains(m.note, "chưa đề xuất") {
		t.Fatalf("^S with no proposal: mode %v, note %q", m.mode, m.note)
	}
	m.note = ""
	m.news[1].Proposal = "Chọn a: THEME=system trong .env.example"
	screen := fits(t, "proposed", m)
	for _, want := range []string{"Chốt cho #3 · chưa gửi", "Chọn a: THEME=system trong .env.example", "^S gửi cho Mate · ↑ sửa", "^S chốt"} {
		if !strings.Contains(screen, want) {
			t.Errorf("session lacks %q:\n%s", want, screen)
		}
	}
	press(t, &m, tea.KeyUp)
	if string(m.input) != m.news[1].Proposal || !m.editProposal {
		t.Fatalf("↑ did not put the proposal in the composer: %q", string(m.input))
	}
	press(t, &m, " và ghi vào README")
	if edited := fits(t, "editing", m); !strings.Contains(edited, "đang sửa bên dưới") || !strings.Contains(edited, "^S gửi bản sửa") {
		t.Errorf("editing the proposal:\n%s", edited)
	}
	press(t, &m, tea.KeyCtrlS)
	refresh(&m)
	if p, ok := lastHeld(m); m.mode != viewTopics || !ok || p.News != 3 || p.Text != "Chọn a: THEME=system trong .env.example và ghi vào README" {
		t.Fatalf("^S: mode %v, held %+v", m.mode, m.state.Held)
	}
	if len(m.sessions) != 0 {
		t.Fatalf("^S left the session open")
	}
	if m.openID != 0 || m.selID != 4 {
		t.Fatalf("after ^S the page shows open #%d, cursor #%d; want #3 folded and the cursor on #4", m.openID, m.selID)
	}
}
