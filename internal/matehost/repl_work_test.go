package matehost

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// workModel has a board on disk beside the news log: #1 done, #2 working
// with a long title, #3 proposed and waiting on topic 3, #4 okayed. The
// host reads both, as WatchNews does.
func workModel(t *testing.T, w, h int) Model {
	t.Helper()
	m := newsModel(w, h)
	dir := t.TempDir()
	now := m.now()
	path := WorkPath(dir)
	for _, r := range []struct{ kind, title, text string }{
		{WorkAssigned, "Epic 3 sổ cái", "anh giao trong chat"},
		{WorkAssigned, "HU-387 khay trang trí trong màn soạn, gộp HU-388..394 và chờ anh thử app trên máy thật trước khi đóng epic", "hai crew đang làm"},
		{WorkProposed, "Dọn tài liệu", ""},
		{WorkAssigned, "Epic 1", ""},
	} {
		if _, err := AddWork(path, r.kind, r.title, r.text, now.Add(-time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	steps := []error{
		DoneWork(path, 1, "PR #12 đã merge", now.Add(-10*time.Minute)),
		WaitWork(path, 3, 3, "phần nào trước?", now.Add(-5*time.Minute)),
		DoneWork(path, 4, "", now.Add(-50*time.Minute)),
		OkayWork(path, 4, now.Add(-40*time.Minute)),
	}
	for _, err := range steps {
		if err != nil {
			t.Fatal(err)
		}
	}
	m.host = New(&Runner{}, Options{News: filepath.Join(dir, NewsName), Now: m.now})
	m.host.RefreshWork()
	m.work = m.host.eventLocked(nil).Work
	return m
}

func TestREPLWorkPage(t *testing.T) {
	for _, w := range []int{60, 80, 120} {
		m := workModel(t, w, 30)
		if chat := fits(t, "chat", m); w >= 100 && !strings.Contains(chat, "^L việc") {
			t.Errorf("chat@%d does not name ^L:\n%s", w, chat)
		}
		press(t, &m, tea.KeyCtrlL)
		if m.mode != viewWork {
			t.Fatalf("^L left mode %d", m.mode)
		}
		page := fits(t, "work", m)
		for _, want := range []string{"mate · hellovietnam › Work", "Chờ anh OK · 1", "[ ] #1 Epic 3 sổ cái", "xong", "PR #12 đã merge",
			"Đang làm · 1", "#2 HU-387", "hai crew đang làm", "Chờ topic · 1", "#3 Dọn tài liệu", "đề xuất", "chờ topic #3", "Đã OK · 1", "#4 Epic 1"} {
			if !strings.Contains(page, want) {
				t.Errorf("work@%d lacks %q:\n%s", w, want, page)
			}
		}
		// Nothing is cut: the long title wraps onto lines of its own.
		if !strings.Contains(strings.Join(strings.Fields(page), " "), "khi đóng epic") {
			t.Errorf("work@%d cut the long title:\n%s", w, page)
		}
		prev := -1
		for _, h := range []string{"Chờ anh OK", "Đang làm", "Chờ topic", "Đã OK"} {
			at := strings.Index(page, h)
			if at < prev {
				t.Errorf("work@%d: group %q is out of order", w, h)
			}
			prev = at
		}
		press(t, &m, tea.KeyCtrlL)
		if m.mode != viewChat {
			t.Fatalf("^L on the page left mode %d", m.mode)
		}
	}
}

// Space ticks the done row off through the host; on another row it only
// says why not. Enter shows a row's history, t opens the topic a waiting
// row names, Esc goes back to the chat.
func TestREPLWorkPageKeys(t *testing.T) {
	m := workModel(t, 100, 30)
	press(t, &m, tea.KeyCtrlL)
	if m.workSel != 1 {
		t.Fatalf("cursor on #%d, want #1", m.workSel)
	}
	press(t, &m, tea.KeyDown)
	press(t, &m, tea.KeySpace)
	if m.note != "chỉ dòng đã xong mới OK được" {
		t.Fatalf("Space on a working row: note %q", m.note)
	}
	press(t, &m, tea.KeyUp, tea.KeySpace)
	deadline := time.Now().Add(2 * time.Second)
	for {
		list, _ := LoadWork(WorkPath(filepath.Dir(m.host.newsPath)))
		if w, _ := findWork(list, 1); w.Status == WorkOkayed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Space on the done row never okayed it")
		}
		time.Sleep(10 * time.Millisecond)
	}

	press(t, &m, "jj", tea.KeyEnter)
	if !m.workOpen[3] {
		t.Fatalf("Enter on #%d opened %v", m.workSel, m.workOpen)
	}
	if page := fits(t, "history", m); !strings.Contains(page, "└ chờ #3") || !strings.Contains(page, "└ thêm") {
		t.Errorf("history lacks its notes:\n%s", page)
	}
	press(t, &m, "t")
	if m.mode != viewTopics || m.openID != 3 {
		t.Fatalf("t on the waiting row: mode %d, open #%d", m.mode, m.openID)
	}
	press(t, &m, tea.KeyCtrlL, tea.KeyEsc)
	if m.mode != viewChat {
		t.Fatalf("Esc on the page left mode %d", m.mode)
	}
}

func TestREPLShowsTheFocusedWork(t *testing.T) {
	m := workModel(t, 100, 30)
	m.help = true
	m.apply(Event{News: m.news, Work: m.work, FocusWork: true})
	if m.mode != viewWork || m.help || m.workSel != 1 {
		t.Fatalf("focus left mode %d, help %v, cursor #%d", m.mode, m.help, m.workSel)
	}
}

func TestREPLWorkPageEmpty(t *testing.T) {
	m := newsModel(80, 20)
	press(t, &m, tea.KeyCtrlL)
	if page := fits(t, "empty", m); !strings.Contains(page, "Bảng việc trống") {
		t.Errorf("empty page:\n%s", page)
	}
	press(t, &m, tea.KeySpace, "t")
	if m.mode != viewWork {
		t.Fatalf("keys on an empty page left mode %d", m.mode)
	}
}

// The console asks the REPL for the Work page through work.focus, as it
// does a news through news.focus; the board itself is read again when
// `mate work` changes it.
func TestWatchNewsPassesTheWorkFocus(t *testing.T) {
	dir := t.TempDir()
	if err := RequestWorkFocus(dir, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	h := New(&Runner{}, Options{News: filepath.Join(dir, NewsName)})
	focus, rows := make(chan bool, 4), make(chan int, 16)
	h.Subscribe(func(ev Event) {
		if ev.FocusWork {
			focus <- true
		}
		rows <- len(ev.Work)
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.WatchNews(ctx, 10*time.Millisecond)
	select {
	case <-focus:
		t.Fatal("a request older than the REPL showed the page")
	case <-time.After(60 * time.Millisecond):
	}
	if _, err := AddWork(WorkPath(dir), WorkAssigned, "Epic 1", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	for n := 0; n != 1; {
		select {
		case n = <-rows:
		case <-time.After(2 * time.Second):
			t.Fatal("the new row never reached the REPL")
		}
	}
	if err := RequestWorkFocus(dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-focus:
	case <-time.After(2 * time.Second):
		t.Fatal("the request never reached the REPL")
	}
	select {
	case <-focus:
		t.Fatal("one request showed the page twice")
	case <-time.After(60 * time.Millisecond):
	}
}
