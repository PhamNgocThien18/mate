package matehost

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewsLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), NewsName)
	now := time.Date(2026, 10, 15, 14, 9, 0, 0, time.Local)
	id, err := OpenNews(path, News{Title: "Dark mode", Crew: "e1", Options: []NewsOption{{Key: "a", Text: "env"}, {Key: "b", Text: "config"}}}, now)
	if err != nil || id != 1 {
		t.Fatalf("open = %d, %v", id, err)
	}
	if id, _ := OpenNews(path, News{Title: "PR #12"}, now); id != 2 {
		t.Fatalf("second id = %d", id)
	}
	if _, err := OpenNews(path, News{}, now); err == nil {
		t.Fatal("a news without a title opened")
	}
	status := func(id int) string {
		list, err := LoadNews(path)
		if err != nil {
			t.Fatal(err)
		}
		n, _ := findNews(list, id)
		return n.Status
	}
	steps := []struct {
		op   newsEvent
		want string
	}{
		{newsEvent{Op: opSeen, ID: 1}, NewsWaiting},
		{newsEvent{Op: opUpdate, ID: 1, Text: "CI xanh"}, NewsWaiting},
		{newsEvent{Op: opAnswer, ID: 1, Text: "[a] env"}, NewsWorking},
		{newsEvent{Op: opFailed, ID: 1}, NewsWaiting},
		{newsEvent{Op: opAnswer, ID: 1, Text: "[a] env"}, NewsWorking},
		{newsEvent{Op: opAsk, ID: 1, Text: "hỏi lại"}, NewsNew},
		{newsEvent{Op: opClose, ID: 1, Text: "PR đã merge"}, NewsDone},
	}
	for i, s := range steps {
		s.op.At = now.Add(time.Duration(i+1) * time.Minute)
		if err := newsOp(path, s.op, true); err != nil {
			t.Fatal(err)
		}
		if got := status(1); got != s.want {
			t.Fatalf("after %s: status %q, want %q", s.op.Op, got, s.want)
		}
	}
	if err := UpdateNews(path, 1, "late", false, now); err == nil {
		t.Error("a closed news took an update")
	}
	if err := CloseNews(path, 2, "PR closed", true, now); err != nil || status(2) != NewsDropped {
		t.Fatalf("drop = %v, %q", err, status(2))
	}
	if err := CloseNews(path, 9, "", false, now); err == nil {
		t.Error("closed a news that does not exist")
	}
	list, _ := LoadNews(path)
	if n := list[0]; n.Answer != "[a] env" || len(n.Notes) != 2 || n.Notes[1].Text != "PR đã merge" {
		t.Fatalf("news 1 = %+v", n)
	}
	// Asking again is the news's question now; the options answered the
	// one before.
	if n := list[0]; n.Question != "hỏi lại" || len(n.Options) != 0 {
		t.Fatalf("news 1 after ask: question %q, options %v", n.Question, n.Options)
	}
	// Seen is the first step, the last answer the fifth: re-asking does not
	// forget either.
	if n := list[0]; !n.SeenAt.Equal(now.Add(time.Minute)) || !n.AnsweredAt.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("news 1 seen %v, answered %v", n.SeenAt, n.AnsweredAt)
	}
	if n := list[1]; !n.SeenAt.IsZero() || !n.AnsweredAt.IsZero() {
		t.Fatalf("news 2 was never seen or answered: %+v", n)
	}
}

// The captain's answer is a turn that names the news, belongs to it, and
// moves it to working; an app turn that opens a news belongs to it.
func TestHostAnswersAndOpensNews(t *testing.T) {
	f := newFixture(t)
	path := filepath.Join(f.dir, NewsName)
	if _, err := OpenNews(path, News{Title: "Dark mode", Question: "Sửa theo cách nào?"}, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	f.host.RefreshNews()
	if err := f.host.AnswerNews(1, "[a] env"); err != nil {
		t.Fatal(err)
	}
	items := f.done(t, 1)
	if it := items[0]; it.News != 1 || it.Text != "[a] env" || it.Source != SourceCaptain {
		t.Fatalf("answer item = %+v", it)
	}
	if prompt := f.read(t, "prompts.log"); !strings.Contains(prompt, "[news #1 · Dark mode] Hỏi: Sửa theo cách nào? · Trả lời: [a] env") {
		t.Fatalf("prompt = %q", prompt)
	}
	list, _ := LoadNews(path)
	if list[0].Status != NewsWorking {
		t.Fatalf("status = %q", list[0].Status)
	}
	if err := f.host.AnswerNews(7, "x"); err == nil {
		t.Error("answered a news that does not exist")
	}

	if err := f.host.Deliver(Marker + "digest: slow"); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenNews(path, News{Title: "PR #12"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	items = f.done(t, 2)
	if it := items[1]; it.Source != SourceApp || it.News != 2 {
		t.Fatalf("app item = %+v", it)
	}
}

func TestHostRefusesAnAnswerToAClosedNews(t *testing.T) {
	f := newFixture(t)
	path := filepath.Join(f.dir, NewsName)
	now := time.Now()
	if _, err := OpenNews(path, News{Title: "Vượt ngân sách"}, now); err != nil {
		t.Fatal(err)
	}
	if err := CloseNews(path, 1, "Crew đã trả PR", true, now); err != nil {
		t.Fatal(err)
	}
	f.host.RefreshNews()
	if err := f.host.AnswerNews(1, "cho chạy tiếp"); err == nil {
		t.Fatal("answered a closed news")
	}
	list, _ := LoadNews(path)
	if list[0].Status != NewsDropped || list[0].Answer != "" {
		t.Fatalf("news = %+v", list[0])
	}
}

// The console asks the REPL to open a news through news.focus: a request
// made while the REPL runs reaches it once; one older than the REPL does
// not.
func TestWatchNewsPassesTheConsolesFocus(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, NewsName)
	now := time.Now()
	if _, err := OpenNews(path, News{Title: "Vượt ngân sách"}, now); err != nil {
		t.Fatal(err)
	}
	if err := RequestNewsFocus(dir, 1, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	h := New(&Runner{}, Options{News: path})
	focus := make(chan int, 4)
	h.Subscribe(func(ev Event) {
		if ev.Focus > 0 {
			focus <- ev.Focus
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.WatchNews(ctx, 10*time.Millisecond)
	select {
	case id := <-focus:
		t.Fatalf("a request older than the REPL opened #%d", id)
	case <-time.After(60 * time.Millisecond):
	}
	if err := RequestNewsFocus(dir, 1, time.Now()); err != nil {
		t.Fatal(err)
	}
	select {
	case id := <-focus:
		if id != 1 {
			t.Fatalf("focus = #%d, want #1", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the request never reached the REPL")
	}
	select {
	case id := <-focus:
		t.Fatalf("one request opened #%d twice", id)
	case <-time.After(60 * time.Millisecond):
	}
}

// A session's proposal waits for the captain: it answers nothing, a later
// one replaces it, and the captain's answer clears it.
func TestProposeNews(t *testing.T) {
	path := filepath.Join(t.TempDir(), NewsName)
	now := time.Now()
	id, err := OpenNews(path, News{Title: "t", Question: "q?"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := ProposeNews(path, id, "  ", now); err == nil {
		t.Fatal("an empty proposal was kept")
	}
	for _, p := range []string{"a", "b thì hơn"} {
		if err := ProposeNews(path, id, p, now); err != nil {
			t.Fatal(err)
		}
	}
	list, _ := LoadNews(path)
	if n := list[0]; n.Proposal != "b thì hơn" || !n.Owed() || n.Answer != "" {
		t.Fatalf("after two proposals: %+v", n)
	}
	if err := withNewsLog(path, func(_ []News, f *os.File) error {
		return writeNewsEvent(f, newsEvent{At: now, Op: opAnswer, ID: id, Text: "b thì hơn"})
	}); err != nil {
		t.Fatal(err)
	}
	list, _ = LoadNews(path)
	if list[0].Proposal != "" {
		t.Fatalf("the answer left the proposal: %q", list[0].Proposal)
	}
	if err := CloseNews(path, id, "", false, now); err != nil {
		t.Fatal(err)
	}
	if err := ProposeNews(path, id, "c", now); err == nil {
		t.Fatal("a closed news took a proposal")
	}
}

func TestOpenNewsUnlessKeepsOneNewsPerReason(t *testing.T) {
	path := filepath.Join(t.TempDir(), NewsName)
	now := time.Date(2026, 11, 4, 20, 0, 0, 0, time.Local)
	if _, err := OpenNews(path, News{Title: "PR #12"}, now); err != nil {
		t.Fatal(err)
	}
	same := func(n News) bool { return n.Kind == "harness-review" && !n.Closed() }
	ask := News{Kind: "harness-review", Title: "friction over budget"}
	n, opened, err := OpenNewsUnless(path, ask, same, now)
	if err != nil || !opened || n.ID != 2 || n.Status != NewsNew {
		t.Fatalf("first = %+v, %v, %v; want #2 opened", n, opened, err)
	}
	again, opened, err := OpenNewsUnless(path, ask, same, now.Add(time.Hour))
	if err != nil || opened || again.ID != 2 {
		t.Fatalf("second = %+v, %v, %v; want #2 already open", again, opened, err)
	}
	if err := CloseNews(path, 2, "not now", false, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n, opened, _ := OpenNewsUnless(path, ask, same, now.Add(3*time.Hour)); !opened || n.ID != 3 {
		t.Fatalf("after close = %+v, %v; want #3 opened", n, opened)
	}
	if list, _ := LoadNews(path); len(list) != 3 {
		t.Fatalf("log has %d news, want 3", len(list))
	}
}
