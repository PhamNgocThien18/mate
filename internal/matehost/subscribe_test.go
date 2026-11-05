package matehost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// serve runs h's socket until the test ends and returns a client of it, a
// way to stop it, and what Serve returned (then closed), once it has.
func serve(t *testing.T, h *Host) (Client, context.CancelFunc, <-chan error) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "mh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	path := filepath.Join(dir, SocketName)
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() {
		served <- h.Serve(ctx, path, nil)
		close(served)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-served:
		case <-time.After(5 * time.Second):
			t.Error("Serve did not return")
		}
	})
	c := Client{Path: path}
	deadline := time.Now().Add(5 * time.Second)
	for c.Ping(ctx) != nil {
		if time.Now().After(deadline) {
			t.Fatal("socket never answered")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return c, cancel, served
}

func subscribe(t *testing.T, c Client) (<-chan Event, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	events, err := c.Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return events, cancel
}

func next(t *testing.T, events <-chan Event) Event {
	t.Helper()
	select {
	case ev, ok := <-events:
		if !ok {
			t.Fatal("the subscription ended")
		}
		return ev
	case <-time.After(10 * time.Second):
		t.Fatal("no event came")
	}
	return Event{}
}

func subscribers(h *Host) int {
	h.subMu.Lock()
	defer h.subMu.Unlock()
	return len(h.subs)
}

func waitSubscribers(t *testing.T, h *Host, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for subscribers(h) != n {
		if time.Now().After(deadline) {
			t.Fatalf("subscribers = %d, want %d", subscribers(h), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func wire(t *testing.T, ev Event) string {
	t.Helper()
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// fill sets every settable field under v to a value other than its zero,
// so a field the wire form forgets shows up as a difference.
func fill(v reflect.Value, n *int) {
	*n++
	switch v.Kind() {
	case reflect.String:
		v.SetString(fmt.Sprintf("s%d", *n))
	case reflect.Int, reflect.Int64:
		v.SetInt(int64(*n))
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		fill(v.Elem(), n)
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 2, 2))
		fill(v.Index(0), n)
		fill(v.Index(1), n)
	case reflect.Struct:
		if v.Type() == reflect.TypeOf(time.Time{}) {
			v.Set(reflect.ValueOf(time.Date(2026, 11, 5, 9, 0, *n, *n, time.UTC)))
			return
		}
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).CanSet() {
				fill(v.Field(i), n)
			}
		}
	default:
		panic("fill: no rule for " + v.Kind().String())
	}
}

// An Event comes back from its JSON with every field, the ones the history
// file and the sessions op leave out (Lead, a tool's id and start) among
// them, and with nil and empty slices as they were.
func TestEventWireRoundTrip(t *testing.T) {
	var full Event
	n := 0
	fill(reflect.ValueOf(&full).Elem(), &n)
	empty := Event{Queue: []string{}, Sessions: []SessionInfo{}, Item: &Item{Tools: []ToolRecord{}}}
	for _, ev := range []Event{full, {}, empty} {
		data, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		var back Event
		if err := json.Unmarshal(data, &back); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(back, ev) {
			t.Errorf("round trip lost something:\n got %+v\nwant %+v\nwire %s", back, ev, data)
		}
	}
	if full.Item.Lead == 0 || full.Item.Tools[0].ID == "" || full.Sessions[0].Running.Lead == 0 {
		t.Fatal("fill left the wire-only fields empty")
	}
}

// A subscriber gets a turn's events (start, streamed text, tool, end) in
// the order and with the content the REPL's callback gets them.
func TestSubscribeGetsATurnAsTheCallbackDoes(t *testing.T) {
	f := newFixture(t)
	c, _, _ := serve(t, f.host)
	events, _ := subscribe(t, c)
	if first := next(t, events); first.Item != nil || first.State.Busy {
		t.Fatalf("first event of an idle host = %+v", first)
	}
	f.mu.Lock()
	from := len(f.events)
	f.mu.Unlock()

	f.host.Submit("chatty stream")
	f.done(t, 1)
	f.mu.Lock()
	want := append([]Event(nil), f.events[from:]...)
	f.mu.Unlock()
	var started, streamed, tool bool
	for i, w := range want {
		got := next(t, events)
		if wire(t, got) != wire(t, w) {
			t.Fatalf("event %d:\n got %s\nwant %s", i, wire(t, got), wire(t, w))
		}
		if it := got.Item; it != nil {
			started = started || i == 0 && !it.Done && it.Reply == ""
			streamed = streamed || !it.Done && it.Reply != ""
			tool = tool || !it.Done && len(it.Tools) > 0
		}
	}
	if last := want[len(want)-1]; !started || !streamed || !tool || last.Item == nil || !last.Item.Done {
		t.Fatalf("the turn's events miss a stage (start %v, text %v, tool %v, last %+v)", started, streamed, tool, last.Item)
	}
	select {
	case ev := <-events:
		t.Fatalf("an event the callback never got: %s", wire(t, ev))
	case <-time.After(100 * time.Millisecond):
	}
}

// A client that comes in during a turn draws it at once: the first event
// has the running turn, the queue, the draft, news, work and sessions.
func TestSubscribeMidTurnStartsWithHowThingsStand(t *testing.T) {
	f := newSessionFixture(t)
	if _, err := AddWork(WorkPath(f.dir), WorkAssigned, "Ship it", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	f.host.RefreshWork()
	if err := f.host.OpenSession(1); err != nil {
		t.Fatal(err)
	}
	f.host.SetDraft("đang gõ dở")
	f.host.Submit("slow việc dài")
	waitBusy(t, f.host)
	f.host.Submit("xếp hàng")
	c, _, _ := serve(t, f.host)

	first := next(t, subscribeOnly(t, c))
	if it := first.Item; it == nil || it.Done || it.Text != "slow việc dài" {
		t.Fatalf("first item = %+v, want the running turn", first.Item)
	}
	if s := first.State; !s.Busy || s.Draft != "đang gõ dở" || s.Queued != 1 || s.TurnSource != SourceCaptain {
		t.Fatalf("first state = %+v", s)
	}
	if len(first.Queue) != 1 || first.Queue[0] != "xếp hàng" || len(first.News) != 1 || len(first.Work) != 1 ||
		len(first.Sessions) != 1 || first.Sessions[0].News != 1 {
		t.Fatalf("first event = %s", wire(t, first))
	}
	// The queued message runs too before the test lets go of its files.
	f.done(t, 2)
}

func subscribeOnly(t *testing.T, c Client) <-chan Event {
	events, _ := subscribe(t, c)
	return events
}

// Two clients get the same events in the same order.
func TestSubscribeTwoClientsSeeTheSameEvents(t *testing.T) {
	f := newFixture(t)
	c, _, _ := serve(t, f.host)
	a, b := subscribeOnly(t, c), subscribeOnly(t, c)
	next(t, a)
	next(t, b)
	f.mu.Lock()
	from := len(f.events)
	f.mu.Unlock()
	f.host.Submit("chatty")
	f.host.ClearDraft()
	f.done(t, 1)
	f.mu.Lock()
	n := len(f.events) - from
	f.mu.Unlock()
	for i := 0; i < n; i++ {
		if x, y := wire(t, next(t, a)), wire(t, next(t, b)); x != y {
			t.Fatalf("event %d differs:\n%s\n%s", i, x, y)
		}
	}
}

// A client that reads nothing slows neither the host nor another client:
// once it is a full queue behind, the host closes its connection.
func TestSubscribeSlowClientIsCutOff(t *testing.T) {
	f := newFixture(t)
	f.host.subQueue = 4
	c, _, _ := serve(t, f.host)
	slow, err := net.Dial("unix", c.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Close()
	if _, err := slow.Write([]byte(`{"op":"subscribe"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	fast := subscribeOnly(t, c)
	next(t, fast)
	waitSubscribers(t, f.host, 2)

	big := strings.Repeat("x", 64<<10)
	sent := 0
	for ; sent < 500 && subscribers(f.host) == 2; sent++ {
		start := time.Now()
		f.host.emitNote(fmt.Errorf("%d %s", sent, big))
		if d := time.Since(start); d > time.Second {
			t.Fatalf("emit %d took %v", sent, d)
		}
		if ev := next(t, fast); !strings.HasPrefix(ev.Note, fmt.Sprintf("%d ", sent)) {
			t.Fatalf("fast client got %.20q, want note %d", ev.Note, sent)
		}
	}
	if subscribers(f.host) != 1 {
		t.Fatalf("the slow client was not cut off after %d events", sent)
	}
	t.Logf("the slow client was cut off after %d events of %d KiB", sent, len(big)>>10)
	// The fast client goes on getting everything.
	f.host.emitNote(errors.New("after"))
	if ev := next(t, fast); ev.Note != "after" {
		t.Fatalf("fast client got %.20q after the cut", ev.Note)
	}
	_ = slow.SetReadDeadline(time.Now().Add(10 * time.Second))
	if _, err := io.Copy(io.Discard, slow); err != nil {
		t.Fatalf("the slow client's connection did not end in EOF: %v", err)
	}
}

// A client that goes away is no longer a subscriber; a host that stops
// closes its subscribers' connections, and they see the end.
func TestSubscribeEndsWithEitherSide(t *testing.T) {
	f := newFixture(t)
	c, stop, served := serve(t, f.host)
	_, leave := subscribe(t, c)
	stays := subscribeOnly(t, c)
	next(t, stays)
	waitSubscribers(t, f.host, 2)
	leave()
	waitSubscribers(t, f.host, 1)

	f.host.ClearDraft()
	stop()
	if ev := next(t, stays); !ev.DraftCleared {
		t.Fatalf("event queued before the stop = %+v, want the cleared draft", ev)
	}
	select {
	case _, ok := <-stays:
		if ok {
			t.Fatal("an event after the host stopped")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the subscription did not end with the host")
	}
	select {
	case err := <-served:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return")
	}
	if subscribers(f.host) != 0 {
		t.Fatal("a subscriber outlived Serve")
	}
}

// sameError reports whether a client's error is the Host method's: the
// same text, and the same sentinel behind it.
func sameError(t *testing.T, op string, direct, remote error) {
	t.Helper()
	if (direct == nil) != (remote == nil) || direct != nil && direct.Error() != remote.Error() {
		t.Fatalf("%s: direct %v, socket %v", op, direct, remote)
	}
	for _, c := range errorCodes {
		if errors.Is(direct, c.err) != errors.Is(remote, c.err) {
			t.Fatalf("%s: errors.Is(%v) direct %v, socket %v", op, c.err, errors.Is(direct, c.err), errors.Is(remote, c.err))
		}
	}
}

// Each REPL op answers as the Host method does, errors included: one host
// is called directly, a twin through its socket.
func TestSocketREPLOpsMatchDirectCalls(t *testing.T) {
	ctx := context.Background()
	t.Run("no forks", func(t *testing.T) {
		d, s := newFixture(t), newFixture(t)
		c, _, _ := serve(t, s.host)
		sameError(t, "session-open", d.host.OpenSession(1), c.OpenSession(ctx, 1))
	})

	d, s := newSessionFixture(t), newSessionFixture(t)
	c, _, _ := serve(t, s.host)
	both := func(fn func(f *fixture)) { fn(d); fn(s) }

	// Drafts.
	for _, text := range []string{"nháp", ""} {
		d.host.SetDraft(text)
		if err := c.SetDraft(ctx, text); err != nil {
			t.Fatal(err)
		}
		if x, y := d.host.State().Draft, s.host.State().Draft; x != y || x != text {
			t.Fatalf("draft: direct %q, socket %q", x, y)
		}
	}

	// Taking back a queued message.
	text, ok := d.host.TakeLast()
	rtext, rok, err := c.TakeLast(ctx)
	if err != nil || text != rtext || ok != rok || ok {
		t.Fatalf("take-last of nothing: direct %q %v, socket %q %v %v", text, ok, rtext, rok, err)
	}
	both(func(f *fixture) { f.host.Submit("slow lượt một") })
	waitBusy(t, d.host)
	waitBusy(t, s.host)
	both(func(f *fixture) { f.host.Submit("lượt hai") })
	text, ok = d.host.TakeLast()
	rtext, rok, err = c.TakeLast(ctx)
	if err != nil || text != rtext || ok != rok || text != "lượt hai" {
		t.Fatalf("take-last: direct %q %v, socket %q %v %v", text, ok, rtext, rok, err)
	}
	if x, y := d.host.State().Queued, s.host.State().Queued; x != 0 || y != 0 {
		t.Fatalf("queued after take-last: direct %d, socket %d", x, y)
	}

	// News seen.
	d.host.SeenNews(1)
	if err := c.SeenNews(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if x, y := d.host.newsSnapshot()[0].Status, s.host.newsSnapshot()[0].Status; x != y || x == NewsNew {
		t.Fatalf("news status after seen: direct %q, socket %q", x, y)
	}

	// Sessions: refusals, opening, interrupting.
	both(func(f *fixture) {
		for _, title := range []string{"hai", "ba", "bốn"} {
			if _, err := OpenNews(f.host.newsPath, News{Title: title}, time.Now()); err != nil {
				t.Fatal(err)
			}
		}
		f.host.RefreshNews()
	})
	sameError(t, "session-open 9", d.host.OpenSession(9), c.OpenSession(ctx, 9))
	for _, id := range []int{1, 2, 3, 4} {
		sameError(t, fmt.Sprintf("session-open %d", id), d.host.OpenSession(id), c.OpenSession(ctx, id))
	}
	if len(d.host.Sessions()) != MaxSessions || len(s.host.Sessions()) != MaxSessions {
		t.Fatalf("sessions: direct %d, socket %d", len(d.host.Sessions()), len(s.host.Sessions()))
	}
	got, err := c.InterruptSession(ctx, 1)
	if want := d.host.InterruptSession(1); err != nil || got != want || got {
		t.Fatalf("session-interrupt with nothing running: direct %v, socket %v %v", want, got, err)
	}
	both(func(f *fixture) {
		if err := f.host.AskSession(1, "slow câu hỏi"); err != nil {
			t.Fatal(err)
		}
	})
	got, err = c.InterruptSession(ctx, 1)
	if want := d.host.InterruptSession(1); err != nil || got != want || !got {
		t.Fatalf("session-interrupt: direct %v, socket %v %v", want, got, err)
	}
	d.sessionItems(t, 1, 1)
	s.sessionItems(t, 1, 1)

	// The work board.
	both(func(f *fixture) {
		if _, err := AddWork(WorkPath(f.dir), WorkAssigned, "Ship it", "", time.Now()); err != nil {
			t.Fatal(err)
		}
	})
	d.host.RefreshWork()
	if err := c.RefreshWork(ctx); err != nil {
		t.Fatal(err)
	}
	if x, y := len(workOf(d.host)), len(workOf(s.host)); x != 1 || y != 1 {
		t.Fatalf("work after refresh: direct %d, socket %d", x, y)
	}
	// The tick is held for its undo (Host.Okay): refused at once, or run
	// once the timers run out.
	okay := func(id int) error { _, err := d.host.Okay(id); return err }
	sockOkay := func(id int) error { _, err := c.OkayWork(ctx, id); return err }
	sameError(t, "work-okay 5", okay(5), sockOkay(5))
	sameError(t, "work-okay not done", okay(1), sockOkay(1))
	both(func(f *fixture) {
		if err := DoneWork(WorkPath(f.dir), 1, "xong", time.Now()); err != nil {
			t.Fatal(err)
		}
	})
	sameError(t, "work-okay", okay(1), sockOkay(1))
	for _, f := range []*fixture{d, s} {
		if list, err := LoadWork(WorkPath(f.dir)); err != nil || list[0].Status != WorkDone {
			t.Fatalf("work while the okay is held: %+v, %v", list, err)
		}
	}
	both(func(f *fixture) { f.timers.fire(t) })
	if x, y := workOf(d.host)[0].Status, workOf(s.host)[0].Status; x != y || x != WorkOkayed {
		t.Fatalf("work after okay: direct %q, socket %q", x, y)
	}

	both(func(f *fixture) { f.host.Interrupt() })
	d.done(t, 1)
	s.done(t, 1)
}

func workOf(h *Host) []Work {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.work
}
