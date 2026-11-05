package matehost

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// focusFixture is a host with one news, whose news.focus and work.focus
// hold requests older than it, watching them; read waits for the host to
// have read the next request the test makes. now is the host's clock, nil
// for the real one.
type focusFixture struct {
	dir   string
	h     *Host
	c     Client
	asked chan struct{}
}

func newFocusFixture(t *testing.T, now func() time.Time) *focusFixture {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, NewsName)
	if _, err := OpenNews(path, News{Title: "Vượt ngân sách"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := RequestNewsFocus(dir, 1, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := RequestWorkFocus(dir, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	f := &focusFixture{dir: dir, h: New(&Runner{}, Options{News: path, Now: now}), asked: make(chan struct{}, 16)}
	// WatchNews reads the news once before it reads the requests already
	// there; a request written before that would count as one of them.
	started := make(chan struct{})
	var once sync.Once
	f.h.Subscribe(func(ev Event) {
		once.Do(func() { close(started) })
		if ev.Focus > 0 || ev.FocusWork {
			f.asked <- struct{}{}
		}
	})
	f.c, _, _ = serve(t, f.h)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go f.h.WatchNews(ctx, 10*time.Millisecond)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("WatchNews never read the news")
	}
	// It reads them right after, before its first tick (10ms).
	time.Sleep(50 * time.Millisecond)
	return f
}

func (f *focusFixture) read(t *testing.T, what string) {
	t.Helper()
	select {
	case <-f.asked:
	case <-time.After(5 * time.Second):
		t.Fatalf("the %s request was never read", what)
	}
}

// first is the first event a subscriber that connects now gets.
func (f *focusFixture) first(t *testing.T) Event {
	t.Helper()
	events, cancel := subscribe(t, f.c)
	defer cancel()
	return next(t, events)
}

// A REPL the console's stage starts beside an open request (CON-224) can
// subscribe after the host has read the request and emitted it. Its first
// event carries the request while it is fresh; a REPL that connects later
// is not sent back to it.
func TestALateSubscriberGetsTheFreshFocus(t *testing.T) {
	var mu sync.Mutex
	clock := time.Now()
	now := func() time.Time { mu.Lock(); defer mu.Unlock(); return clock }
	advance := func(d time.Duration) { mu.Lock(); clock = clock.Add(d); mu.Unlock() }
	f := newFocusFixture(t, now)

	if ev := f.first(t); ev.Focus != 0 || ev.FocusWork {
		t.Fatalf("before any request: first event focus #%d, work %v", ev.Focus, ev.FocusWork)
	}

	if err := RequestNewsFocus(f.dir, 1, time.Now()); err != nil {
		t.Fatal(err)
	}
	f.read(t, "news")
	advance(focusFresh - time.Second)
	if ev := f.first(t); ev.Focus != 1 || ev.FocusWork {
		t.Fatalf("a REPL that came after a fresh news request: focus #%d, work %v; want #1", ev.Focus, ev.FocusWork)
	}

	// A work request after it: the later of the two is the captain's now.
	advance(time.Millisecond)
	if err := RequestWorkFocus(f.dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	f.read(t, "work")
	if ev := f.first(t); ev.Focus != 0 || !ev.FocusWork {
		t.Fatalf("after a news then a work request: focus #%d, work %v; want the Work page", ev.Focus, ev.FocusWork)
	}

	advance(focusFresh)
	if ev := f.first(t); ev.Focus != 0 || ev.FocusWork {
		t.Fatalf("a REPL that came %s after the requests: focus #%d, work %v; want none", focusFresh, ev.Focus, ev.FocusWork)
	}
}

// The stage's case end to end (CON-224): the console asks for a news, the
// host reads the request before the stage's REPL is up, and the REPL that
// connects next opens that news; a `w` after it opens the Work page in the
// same way.
func TestAREPLStartedAfterTheRequestOpensIt(t *testing.T) {
	f := newFocusFixture(t, nil)

	if err := RequestNewsFocus(f.dir, 1, time.Now()); err != nil {
		t.Fatal(err)
	}
	f.read(t, "news")
	startClient(t, f.c).until("news #1 open", func(m Model) bool { return m.mode == viewTopics && m.selID == 1 })

	if err := RequestWorkFocus(f.dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	f.read(t, "work")
	startClient(t, f.c).until("the Work page", func(m Model) bool { return m.mode == viewWork })
}
