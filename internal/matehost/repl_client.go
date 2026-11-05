package matehost

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// The REPL is a client of its host's socket (CON-223): it hears the host
// through OpSubscribe and acts through the socket's ops, never by calling
// the host in its own process. The host lives on when the REPL ends; the
// REPL lives on when the host restarts, and connects again.

// Conn is the REPL's way to its host: what the REPL calls, each as the Host
// method it is named after. REPLConn is the socket's; tests give a fake.
type Conn interface {
	// Subscribe is the host's events from now on, starting with one of how
	// things stand (Client.Subscribe); History its last n finished items.
	Subscribe(ctx context.Context) (<-chan Event, error)
	History(ctx context.Context, n int) ([]Item, error)

	Submit(ctx context.Context, text string) error
	Interrupt(ctx context.Context) (bool, error)
	// SetDraft records the draft; from is the REPL's id (Event.DraftBy).
	SetDraft(ctx context.Context, text, from string) error
	TakeLast(ctx context.Context) (string, bool, error)
	SeenNews(ctx context.Context, id int) error
	AnswerNews(ctx context.Context, id int, text string) error
	OpenSession(ctx context.Context, id int) error
	AskSession(ctx context.Context, id int, text string) error
	InterruptSession(ctx context.Context, id int) (bool, error)
	CloseSession(ctx context.Context, id int) error
	OkayWork(ctx context.Context, id int) error
}

// REPLConn is c as the REPL's Conn. An op the host refuses comes back as
// the Host method's own error, with its text, so the note the REPL shows
// is the one it showed when it called the host in its own process. Empty
// text is no call at all, as it was for the Host methods; the socket would
// refuse it.
func REPLConn(c Client) Conn { return replConn{c} }

type replConn struct{ c Client }

func (r replConn) Subscribe(ctx context.Context) (<-chan Event, error) { return r.c.Subscribe(ctx) }

func (r replConn) History(ctx context.Context, n int) ([]Item, error) {
	resp, err := r.c.call(ctx, request{Op: OpHistory, N: n})
	return resp.Items, err
}

func (r replConn) Submit(ctx context.Context, text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	_, err := r.c.call(ctx, request{Op: OpSubmit, Text: text})
	return err
}

func (r replConn) Interrupt(ctx context.Context) (bool, error) {
	resp, err := r.c.call(ctx, request{Op: OpInterrupt})
	return resp.Interrupted, err
}

func (r replConn) SetDraft(ctx context.Context, text, from string) error {
	_, err := r.c.call(ctx, request{Op: OpSetDraft, Text: text, From: from})
	return err
}

func (r replConn) TakeLast(ctx context.Context) (string, bool, error) { return r.c.TakeLast(ctx) }

func (r replConn) SeenNews(ctx context.Context, id int) error { return r.c.SeenNews(ctx, id) }

func (r replConn) AnswerNews(ctx context.Context, id int, text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	_, err := r.c.call(ctx, request{Op: OpAnswer, News: id, Text: text})
	return err
}

func (r replConn) OpenSession(ctx context.Context, id int) error { return r.c.OpenSession(ctx, id) }

// AskSession is the socket's session-ask, which opens the session when it
// is not open; the REPL asks only in a session it has opened.
func (r replConn) AskSession(ctx context.Context, id int, text string) error {
	_, err := r.c.call(ctx, request{Op: OpSessionAsk, News: id, Text: text})
	return err
}

func (r replConn) InterruptSession(ctx context.Context, id int) (bool, error) {
	return r.c.InterruptSession(ctx, id)
}

func (r replConn) CloseSession(ctx context.Context, id int) error {
	_, err := r.c.call(ctx, request{Op: OpSessionClose, News: id})
	return err
}

func (r replConn) OkayWork(ctx context.Context, id int) error { return r.c.OkayWork(ctx, id) }

// replHistory is how many items the REPL shows again when it connects.
const replHistory = 200

// Retry waits of the REPL's link: the first after the host went away, and
// the longest between two tries.
const (
	linkRetryFirst = 100 * time.Millisecond
	linkRetryMax   = 2 * time.Second
	// linkGrace is how long a REPL started beside its host (they start
	// together in the Mate's pane) waits for it before saying it is not
	// there.
	linkGrace = 1500 * time.Millisecond
)

// Messages the link sends the REPL.
type (
	// linkUpMsg is a connection made: the host's first event (how things
	// stand) and its history, read after subscribing, so a turn that
	// ended in between is in both and the REPL keeps it once.
	linkUpMsg struct {
		first   Event
		history []Item
	}
	// linkDownMsg is the host gone, or not there yet; the link keeps
	// trying.
	linkDownMsg struct{}
)

// link keeps the REPL connected to its host until ctx ends, and sends
// what the host says, in order: a linkUpMsg on each connection, then each
// event as a hostMsg, then a linkDownMsg when the connection is lost.
// send must not wait on the REPL's Update (a forwarder's Put).
func link(ctx context.Context, conn Conn, send func(tea.Msg)) {
	wait := time.Duration(0)
	down := false
	started, ever := time.Now(), false
	for ctx.Err() == nil {
		if wait > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		}
		if connected := linkOnce(ctx, conn, send); connected {
			wait = 0
			down, ever = false, true
		}
		if ctx.Err() != nil {
			return
		}
		if !down && (ever || time.Since(started) >= linkGrace) {
			send(linkDownMsg{})
			down = true
		}
		wait = min(max(wait*2, linkRetryFirst), linkRetryMax)
	}
}

// linkOnce is one connection, until it is lost; it reports whether it was
// made.
func linkOnce(ctx context.Context, conn Conn, send func(tea.Msg)) bool {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	events, err := conn.Subscribe(ctx)
	if err != nil {
		return false
	}
	first, ok := <-events
	if !ok {
		return false
	}
	history, err := conn.History(ctx, replHistory)
	if err != nil {
		return false
	}
	send(linkUpMsg{first: first, history: history})
	for ev := range events {
		send(hostMsg(ev))
	}
	return true
}

// caller runs the REPL's calls to its host, in the order they are made,
// without Update waiting on any; what a call returns, when not nil, goes to
// Update as a message.
type caller interface {
	Go(call func(ctx context.Context) tea.Msg)
}

// opQueue is the caller of a running REPL: one goroutine, so a draft is
// never overtaken by an older one and Esc reaches the host before the
// message typed after it.
type opQueue struct {
	ctx    context.Context
	cancel context.CancelFunc
	q      *forwarder[func(context.Context) tea.Msg]
}

func newOpQueue(send func(tea.Msg)) *opQueue {
	ctx, cancel := context.WithCancel(context.Background())
	o := &opQueue{ctx: ctx, cancel: cancel}
	o.q = newForwarder(func(call func(context.Context) tea.Msg) {
		if msg := call(o.ctx); msg != nil {
			send(msg)
		}
	})
	return o
}

func (o *opQueue) Go(call func(ctx context.Context) tea.Msg) { o.q.Put(call) }

// Close drops the calls not yet made and cuts the one in flight short; it
// returns once that one has returned.
func (o *opQueue) Close() {
	o.cancel()
	o.q.Close()
	<-o.q.Done()
}

// newClientID names one REPL, for Event.DraftBy.
func newClientID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// RunREPL runs the REPL on conn until the captain quits it (Config.Quit),
// its terminal goes, or ctx ends. With no options it takes the terminal:
// the alternate screen, the mouse and stdin.
func RunREPL(ctx context.Context, conn Conn, cfg Config, opts ...tea.ProgramOption) error {
	if len(opts) == 0 {
		// Mouse reporting makes the wheel scroll the view; without it the
		// terminal turns the wheel into ↑/↓ keys.
		opts = []tea.ProgramOption{tea.WithAltScreen(), tea.WithMouseCellMotion()}
		if in := newSeqInput(os.Stdin); in != nil {
			opts = append(opts, tea.WithInput(in))
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	opts = append(opts, tea.WithContext(ctx))

	m := NewModel(conn, cfg, nil)
	var p *tea.Program
	ready := make(chan struct{})
	fw := newForwarder(func(msg tea.Msg) {
		<-ready
		p.Send(msg)
	})
	ops := newOpQueue(fw.Put)
	m.calls = ops
	p = tea.NewProgram(m, opts...)
	close(ready)

	linked := make(chan struct{})
	go func() {
		defer close(linked)
		link(ctx, conn, fw.Put)
	}()
	_, err := p.Run()
	cancel()
	<-linked
	ops.Close()
	fw.Close()
	<-fw.Done()
	if ctx.Err() != nil && errors.Is(err, tea.ErrProgramKilled) {
		// Ended from outside (ctx), not a failure of the REPL's own.
		return nil
	}
	return err
}
