package matehost

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// fakeTimers is the clock of what the host holds for its undo: a timer
// runs only when the test says the delay is over (fire).
type fakeTimers struct {
	mu   sync.Mutex
	next int
	fns  map[int]func()
	asks []time.Duration
}

func (ft *fakeTimers) after(d time.Duration, fn func()) func() bool {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	if ft.fns == nil {
		ft.fns = map[int]func(){}
	}
	ft.next++
	id := ft.next
	ft.fns[id] = fn
	ft.asks = append(ft.asks, d)
	return func() bool {
		ft.mu.Lock()
		defer ft.mu.Unlock()
		_, ok := ft.fns[id]
		delete(ft.fns, id)
		return ok
	}
}

// fire runs out every timer started and not stopped, oldest first, as if
// UndoDelay had passed, and checks each was asked for UndoDelay.
func (ft *fakeTimers) fire(t *testing.T) {
	t.Helper()
	ft.mu.Lock()
	ids := make([]int, 0, len(ft.fns))
	for id := range ft.fns {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	fns := make([]func(), len(ids))
	for i, id := range ids {
		fns[i] = ft.fns[id]
		delete(ft.fns, id)
	}
	for _, d := range ft.asks {
		if d != UndoDelay {
			t.Errorf("a held item waits %v, want UndoDelay %v", d, UndoDelay)
		}
	}
	ft.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

// running is how many timers are started and not yet stopped or run.
func (ft *fakeTimers) running() int {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	return len(ft.fns)
}

func sameHeld(a, b Held) bool {
	return a.ID == b.ID && a.Kind == b.Kind && a.Text == b.Text && a.News == b.News && a.Work == b.Work && a.At.Equal(b.At) && a.Due.Equal(b.Due)
}

// turns is how many turns the fake harness was started for.
func (f *fixture) turns(t *testing.T) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, "argv.log"))
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "\n")
}

// A message the captain sends waits UndoDelay in the host, and the Mate
// gets it only then; one taken back before then never reaches the Mate,
// and the client gets its text back to put in the composer.
func TestHeldMessageGoesAfterTheDelayAndUndoTakesItBack(t *testing.T) {
	f := newFixture(t)
	c, _, _ := serve(t, f.host)
	ctx := t.Context()

	held, err := c.Submit(ctx, "  chạy lại make shot  ")
	if err != nil || held.ID == 0 || held.Kind != HeldSay || held.Text != "chạy lại make shot" {
		t.Fatalf("submit = %+v, %v", held, err)
	}
	if !held.Due.Equal(held.At.Add(UndoDelay)) {
		t.Fatalf("held at %v goes at %v, want %v later", held.At, held.Due, UndoDelay)
	}
	st, err := c.State(ctx)
	if err != nil || len(st.Held) != 1 || !sameHeld(st.Held[0], held) || st.Busy || st.Queued != 0 {
		t.Fatalf("state while held = %+v, %v", st, err)
	}
	if n := f.turns(t); n != 0 {
		t.Fatalf("the Mate was started %d times before the undo ran out", n)
	}

	back, err := c.Undo(ctx, held.ID)
	if err != nil || back.Text != "chạy lại make shot" || back.ID != held.ID {
		t.Fatalf("undo = %+v, %v", back, err)
	}
	if f.timers.running() != 0 {
		t.Fatal("the undone item's timer still runs")
	}
	f.timers.fire(t)
	if st, _ := c.State(ctx); len(st.Held) != 0 || st.Busy {
		t.Fatalf("state after the undo = %+v", st)
	}
	if n := f.turns(t); n != 0 {
		t.Fatalf("the Mate got a message the captain took back (%d turns)", n)
	}
	if _, err := c.Undo(ctx, held.ID); !errors.Is(err, ErrNotHeld) {
		t.Fatalf("a second undo = %v, want ErrNotHeld", err)
	}

	sent, err := c.Submit(ctx, "giữ nguyên")
	if err != nil {
		t.Fatal(err)
	}
	f.timers.fire(t)
	if it := f.done(t, 1)[0]; it.Source != SourceCaptain || it.Text != "giữ nguyên" {
		t.Fatalf("the turn = %+v", it)
	}
	if _, err := c.Undo(ctx, sent.ID); !errors.Is(err, ErrNotHeld) || !strings.Contains(err.Error(), "already sent") {
		t.Fatalf("undo of a message gone to the Mate = %v", err)
	}
}

// What the captain sent outlives the client that sent it: a REPL or a
// page that closes right after sending loses nothing, since the host
// holds it and sends it itself.
func TestHeldMessageGoesWhenTheClientHasClosed(t *testing.T) {
	f := newFixture(t)
	c, _, _ := serve(t, f.host)
	events, stop := subscribe(t, c)
	next(t, events)
	if _, err := REPLConn(c).Submit(t.Context(), "gửi rồi đóng"); err != nil {
		t.Fatal(err)
	}
	if ev := next(t, events); len(ev.State.Held) != 1 {
		t.Fatalf("the subscriber's event = %+v", ev.State)
	}
	stop()
	for range events {
	}
	f.timers.fire(t)
	if it := f.done(t, 1)[0]; it.Text != "gửi rồi đóng" || it.Source != SourceCaptain {
		t.Fatalf("the turn = %+v", it)
	}
	if items := History(filepath.Join(f.dir, HistoryName), 0); len(items) != 1 || items[0].Text != "gửi rồi đóng" {
		t.Fatalf("history = %+v", items)
	}
}

// An answer is held like a message: its news stays owed until it goes,
// then the answer lands in it. A news that is closed is refused at once,
// not after the delay.
func TestHeldAnswer(t *testing.T) {
	f := newFixture(t)
	news := filepath.Join(f.dir, NewsName)
	if _, err := OpenNews(news, News{Title: "Dark mode", Question: "Which way?"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	f.host.RefreshNews()
	if _, err := f.host.Answer(9, "x"); !errors.Is(err, ErrNoNews) {
		t.Fatalf("answer to no news = %v", err)
	}
	held, err := f.host.Answer(1, "[a] env")
	if err != nil || held.Kind != HeldAnswer || held.News != 1 {
		t.Fatalf("answer = %+v, %v", held, err)
	}
	if n, _ := findNews(f.host.newsSnapshot(), 1); !n.Owed() {
		t.Fatalf("news while the answer is held = %+v", n)
	}
	f.timers.fire(t)
	if it := f.done(t, 1)[0]; it.News != 1 || it.Text != "[a] env" {
		t.Fatalf("answer item = %+v", it)
	}
	list, err := LoadNews(news)
	if err != nil || list[0].Answer != "[a] env" {
		t.Fatalf("news after the answer went = %+v, %v", list, err)
	}

	if err := CloseNews(news, 1, "done", false, time.Now()); err != nil {
		t.Fatal(err)
	}
	f.host.RefreshNews()
	if _, err := f.host.Answer(1, "again"); err == nil || !strings.Contains(err.Error(), "already closed") {
		t.Fatalf("answer to a closed news = %v", err)
	}
	if f.timers.running() != 0 {
		t.Fatal("a refused answer was held")
	}
}

// A tick on the work board is held too: the row stays done until it goes,
// a second tick on it is the same held item, and Undo leaves the row done.
func TestHeldOkay(t *testing.T) {
	f := newFixture(t)
	path := WorkPath(f.dir)
	for _, title := range []string{"Ship it", "Fold make shot"} {
		id, err := AddWork(path, WorkAssigned, title, "", time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err := DoneWork(path, id, "xong", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.host.Okay(7); !errors.Is(err, ErrNoWork) {
		t.Fatalf("okay of no row = %v", err)
	}
	first, err := f.host.Okay(1)
	if err != nil || first.Kind != HeldOkay || first.Work != 1 {
		t.Fatalf("okay = %+v, %v", first, err)
	}
	if again, err := f.host.Okay(1); err != nil || again.ID != first.ID {
		t.Fatalf("a second okay = %+v, %v; want the held %d", again, err, first.ID)
	}
	second, err := f.host.Okay(2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.host.Undo(second.ID); err != nil {
		t.Fatal(err)
	}
	f.timers.fire(t)
	list, err := LoadWork(path)
	if err != nil || list[0].Status != WorkOkayed || list[1].Status != WorkDone {
		t.Fatalf("work after the okay went and the other was undone = %+v, %v", list, err)
	}
}

// An app line waits for a captain message or answer held for its undo, as
// it waits for the queue: the captain's words go first (mvp.md M21). A
// held tick holds nothing up.
func TestDeliverWaitsForHeldWords(t *testing.T) {
	f := newFixture(t)
	path := WorkPath(f.dir)
	id, err := AddWork(path, WorkAssigned, "Ship it", "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := DoneWork(path, id, "xong", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.host.Okay(id); err != nil {
		t.Fatal(err)
	}
	held, err := f.host.Say("trước digest")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.host.Deliver("digest"); !errors.Is(err, ErrQueued) {
		t.Fatalf("deliver while a message is held = %v, want ErrQueued", err)
	}
	if err := f.host.Reload(f.host.runner.Launch); !errors.Is(err, ErrQueued) {
		t.Fatalf("reload while a message is held = %v, want ErrQueued", err)
	}
	if _, err := f.host.Undo(held.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.host.Deliver("digest"); err != nil {
		t.Fatalf("deliver with only a tick held = %v", err)
	}
	f.done(t, 1)
}

// Stopping drops what is held, as it drops the queue.
func TestStopDropsHeld(t *testing.T) {
	f := newFixture(t)
	if _, err := f.host.Say("muộn quá"); err != nil {
		t.Fatal(err)
	}
	f.host.Stop(time.Second, false)
	if f.timers.running() != 0 {
		t.Fatal("a held item's timer outlived the stop")
	}
	if st := f.host.State(); len(st.Held) != 0 {
		t.Fatalf("state after the stop = %+v", st)
	}
	if _, err := f.host.Say("sau khi dừng"); !errors.Is(err, ErrStopping) {
		t.Fatalf("say while stopping = %v", err)
	}
	if n := f.turns(t); n != 0 {
		t.Fatalf("%d turns ran", n)
	}
}

// The REPL and a page show the same held item for the same message: the
// REPL from the host's event, a page from the host's state (GET /chat);
// either takes it back by its id.
func TestHeldItemIsTheSameForEveryClient(t *testing.T) {
	f := newFixture(t)
	c, _, _ := serve(t, f.host)
	events, _ := subscribe(t, c)
	m := NewModel(REPLConn(c), Config{Project: "shop"}, nil)
	m.width, m.height = 100, 20
	m.connected(linkUpMsg{first: next(t, events)})
	clock := time.Now()
	m.now = func() time.Time { return clock }

	// A page sends it, as the core does for City and Office.
	held, err := c.Submit(t.Context(), "từ City")
	if err != nil {
		t.Fatal(err)
	}
	m.apply(next(t, events))
	if len(m.state.Held) != 1 || !sameHeld(m.state.Held[0], held) {
		t.Fatalf("the REPL holds %+v, the page was told %+v", m.state.Held, held)
	}
	clock = held.Due.Add(-3500 * time.Millisecond)
	if v := ansi.Strip(m.View()); !strings.Contains(v, "⧗ gửi sau 4s · u hoàn tác › từ City") {
		t.Fatalf("the REPL does not count the page's message down:\n%s", v)
	}
	st, err := c.State(t.Context())
	if err != nil || len(st.Held) != 1 || st.Held[0].ID != held.ID {
		t.Fatalf("the page's state = %+v, %v", st, err)
	}
}
