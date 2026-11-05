package matehost

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// focusFixture is a host with one news, watching its news.focus and
// work.focus; read waits for the host to have read the next request the
// test makes. The host's clock is the fixture's, moved by advance.
type focusFixture struct {
	dir   string
	h     *Host
	c     Client
	asked chan struct{}
	mu    sync.Mutex
	clock time.Time
}

func newFocusFixture(t *testing.T) *focusFixture {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, NewsName)
	if _, err := OpenNews(path, News{Title: "Vượt ngân sách"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	f := &focusFixture{dir: dir, asked: make(chan struct{}, 64), clock: time.Now()}
	f.h = New(&Runner{}, Options{News: path, Now: f.now})
	f.h.Subscribe(func(ev Event) {
		if ev.Focus > 0 || ev.FocusWork {
			f.asked <- struct{}{}
		}
	})
	f.c, _, _ = serve(t, f.h)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go f.h.WatchNews(ctx, 10*time.Millisecond)
	// A request written before WatchNews has read the files counts as one
	// made before the host, and is never emitted. Ask until one is: the
	// host is past that read. Then the probe is let go stale.
	for read := false; !read; {
		if err := RequestWorkFocus(dir, time.Now()); err != nil {
			t.Fatal(err)
		}
		select {
		case <-f.asked:
			read = true
		case <-time.After(200 * time.Millisecond):
		}
	}
	time.Sleep(50 * time.Millisecond)
	for len(f.asked) > 0 {
		<-f.asked
	}
	f.advance(focusFresh)
	return f
}

func (f *focusFixture) now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.clock
}

func (f *focusFixture) advance(d time.Duration) {
	f.mu.Lock()
	f.clock = f.clock.Add(d)
	f.mu.Unlock()
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
	f := newFocusFixture(t)
	advance := f.advance

	if ev := f.first(t); ev.Focus != 0 || ev.FocusWork {
		t.Fatalf("with no fresh request: first event focus #%d, work %v", ev.Focus, ev.FocusWork)
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

// The stage's case, from the host to the REPL client (CON-224; the
// console and the pane runner are not in it): the console asks for a news,
// the host reads the request before the stage's REPL is up, and the REPL
// that connects next opens that news; a `w` after it opens the Work page
// in the same way.
func TestAREPLStartedAfterTheRequestOpensIt(t *testing.T) {
	f := newFocusFixture(t)

	if err := RequestNewsFocus(f.dir, 1, time.Now()); err != nil {
		t.Fatal(err)
	}
	f.read(t, "news")
	startClient(t, f.c).until("news #1 open", func(m Model) bool { return m.mode == viewTopics && m.selID == 1 })

	f.advance(time.Millisecond)
	if err := RequestWorkFocus(f.dir, time.Now()); err != nil {
		t.Fatal(err)
	}
	f.read(t, "work")
	startClient(t, f.c).until("the Work page", func(m Model) bool { return m.mode == viewWork })
}
