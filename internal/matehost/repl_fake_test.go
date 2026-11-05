package matehost

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// fakeConn is a host for the REPL's tests: it records what the REPL asks
// and keeps the little state the REPL reads back (the draft, the queue the
// captain takes from, the open sessions).
type fakeConn struct {
	mu        sync.Mutex
	draft     string
	queue     []string
	submitted []string
	interrupt int
	answers   []string
	seen      []int
	okayed    []int
	asked     []string
	closed    []int
	// forks lets sessions open; sessions are the open ones, as the
	// host's event would carry them (refresh).
	forks    bool
	sessions []SessionInfo
	// held is what the fake holds for its undo, as the host would; now is
	// the REPL's clock, which it counts held items down by.
	held  []Held
	now   func() time.Time
	undid []int
}

// hold keeps p as the host holds what the captain sends.
func (f *fakeConn) hold(p Held) Held {
	now := time.Now()
	if f.now != nil {
		now = f.now()
	}
	p.ID, p.At, p.Due = len(f.held)+len(f.undid)+1, now, now.Add(UndoDelay)
	f.held = append(f.held, p)
	return p
}

func (f *fakeConn) Undo(_ context.Context, id int) (Held, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, p := range f.held {
		if p.ID == id {
			f.held = slices.Delete(f.held, i, i+1)
			f.undid = append(f.undid, id)
			return p, nil
		}
	}
	return Held{}, refused(ErrNotHeld)
}

var errFakeNoStream = errors.New("fakeConn streams nothing")

func (f *fakeConn) Subscribe(context.Context) (<-chan Event, error) { return nil, errFakeNoStream }
func (f *fakeConn) History(context.Context, int) ([]Item, error)    { return nil, errFakeNoStream }

func (f *fakeConn) Submit(_ context.Context, text string) (Held, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.submitted = append(f.submitted, text)
	return f.hold(Held{Kind: HeldSay, Text: text}), nil
}

func (f *fakeConn) Interrupt(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.interrupt++
	return true, nil
}

func (f *fakeConn) SetDraft(_ context.Context, text, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.draft = text
	return nil
}

func (f *fakeConn) TakeLast(context.Context) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.queue) == 0 {
		return "", false, nil
	}
	last := f.queue[len(f.queue)-1]
	f.queue = f.queue[:len(f.queue)-1]
	return last, true, nil
}

func (f *fakeConn) SeenNews(_ context.Context, id int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, id)
	return nil
}

func (f *fakeConn) AnswerNews(_ context.Context, id int, text string) (Held, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers = append(f.answers, text)
	return f.hold(Held{Kind: HeldAnswer, Text: text, News: id}), nil
}

func (f *fakeConn) OpenSession(_ context.Context, id int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.forks {
		return refused(ErrNoForks)
	}
	if !slices.ContainsFunc(f.sessions, func(s SessionInfo) bool { return s.News == id }) {
		f.sessions = append(f.sessions, SessionInfo{News: id, OpenedAt: time.Now()})
	}
	return nil
}

func (f *fakeConn) AskSession(_ context.Context, id int, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, text)
	return nil
}

func (f *fakeConn) InterruptSession(context.Context, int) (bool, error) { return true, nil }

func (f *fakeConn) CloseSession(_ context.Context, id int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = append(f.closed, id)
	f.sessions = slices.DeleteFunc(f.sessions, func(s SessionInfo) bool { return s.News == id })
	return nil
}

func (f *fakeConn) OkayWork(_ context.Context, id int) (Held, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.okayed = append(f.okayed, id)
	return f.hold(Held{Kind: HeldOkay, Work: id}), nil
}

// session runs fn on open session id, under the fake's lock.
func (f *fakeConn) session(id int, fn func(s *SessionInfo)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.sessions {
		if f.sessions[i].News == id {
			fn(&f.sessions[i])
		}
	}
}

func (f *fakeConn) state() (draft string, queue []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.draft, append([]string(nil), f.queue...)
}

// syncCalls runs the REPL's calls at once, in the test's goroutine, and
// keeps what they return for drain to give to Update.
type syncCalls struct {
	msgs []tea.Msg
}

func (s *syncCalls) Go(call func(ctx context.Context) tea.Msg) {
	if msg := call(context.Background()); msg != nil {
		s.msgs = append(s.msgs, msg)
	}
}

// withFake gives m a fake host it is connected to, its calls made at once.
func withFake(m Model) (Model, *fakeConn) {
	f := &fakeConn{now: func() time.Time { return m.now() }}
	m.conn, m.calls, m.linked = f, &syncCalls{}, true
	return m, f
}

// drain gives Update what m's calls returned, until there is nothing left.
func drain(m *Model) {
	sc, ok := m.calls.(*syncCalls)
	if !ok {
		return
	}
	for len(sc.msgs) > 0 {
		msg := sc.msgs[0]
		sc.msgs = sc.msgs[1:]
		next, _ := m.Update(msg)
		*m = next.(Model)
	}
}

// refused is err as REPLConn gives back a refusal of the host's.
func refused(err error) error { return &hostError{msg: err.Error(), err: err} }

// unreachable is a call that never reached the host.
var unreachable = errors.New("dial unix host.sock: connect: no such file or directory")
