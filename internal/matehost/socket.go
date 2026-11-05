package matehost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"
)

// Socket ops.
const (
	OpPing       = "ping"
	OpDeliver    = "deliver"
	OpState      = "state"
	OpStop       = "stop"
	OpHistory    = "history"
	OpClearDraft = "clear-draft"
	OpReload     = "reload"
	// The captain's ops from a page (CON-142): what the REPL's composer,
	// Esc, a news answer and a topic's session do, so a message typed on
	// the dashboard is the same turn as one typed in the Mate's pane.
	OpSubmit       = "submit"
	OpInterrupt    = "interrupt"
	OpAnswer       = "answer"
	OpSessionAsk   = "session-ask"
	OpSessionClose = "session-close"
	OpSessions     = "sessions"
	// The REPL's own ops (CON-221), so a REPL can run as a client of the
	// host: each answers as the Host method it is named after does, errors
	// included.
	OpSubscribe        = "subscribe"
	OpSetDraft         = "set-draft"
	OpTakeLast         = "take-last"
	OpSeenNews         = "seen-news"
	OpSessionOpen      = "session-open"
	OpSessionInterrupt = "session-interrupt"
	OpWorkOkay         = "work-okay"
	OpWorkRefresh      = "work-refresh"
	// OpUndo takes back what the captain sent while it waits out its undo
	// (hold.go, CON-222): a submit, an answer or a work-okay, each held
	// UndoDelay before it goes.
	OpUndo = "undo"
)

type request struct {
	Op    string `json:"op"`
	Text  string `json:"text,omitempty"`
	Force bool   `json:"force,omitempty"`
	// WaitMS bounds a stop's wait for the turn in flight.
	WaitMS int64 `json:"wait_ms,omitempty"`
	N      int   `json:"n,omitempty"`
	// Launch is what a reload switches the host to.
	Launch *Launch `json:"launch,omitempty"`
	// News is the news an answer or a session op is about.
	News int `json:"news,omitempty"`
	// Work is the work board row a work op is about.
	Work int `json:"work,omitempty"`
	// From is the REPL a set-draft comes from (Host.SetDraftBy).
	From string `json:"from,omitempty"`
	// Held is the held item an undo takes back.
	Held int `json:"held,omitempty"`
}

type response struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	// Busy is set on a delivery refused because the Mate is busy, so the
	// caller can tell it from a failure.
	Busy   bool   `json:"busy,omitempty"`
	State  *State `json:"state,omitempty"`
	Items  []Item `json:"items,omitempty"`
	Killed bool   `json:"killed,omitempty"`
	// Interrupted is whether an interrupt found a turn to stop.
	Interrupted bool `json:"interrupted,omitempty"`
	// Sessions are the open topic sessions, for OpSessions.
	Sessions []SessionInfo `json:"sessions,omitempty"`
	// Code names the host's error (errorCodes), so a client can give back
	// the error the Host method returned, errors.Is and all.
	Code string `json:"code,omitempty"`
	// Text and Taken are what OpTakeLast took.
	Text  string `json:"text,omitempty"`
	Taken bool   `json:"taken,omitempty"`
	// Held is what a submit, an answer or a work-okay put on hold, and what
	// an undo took back.
	Held *Held `json:"held,omitempty"`
}

// errorCodes are the errors a client of the REPL's ops can tell apart.
var errorCodes = []struct {
	code string
	err  error
}{
	{"busy", ErrBusy},
	{"queued", ErrQueued},
	{"stopping", ErrStopping},
	{"no-news", ErrNoNews},
	{"no-work", ErrNoWork},
	{"no-forks", ErrNoForks},
	{"too-many-sessions", ErrTooManySessions},
	{"no-session", ErrNoSession},
	{"session-busy", ErrSessionBusy},
	{"not-held", ErrNotHeld},
}

// failed is the answer to an op whose Host method returned err.
func failed(err error) response {
	resp := response{Error: err.Error()}
	for _, c := range errorCodes {
		if errors.Is(err, c.err) {
			resp.Code = c.code
			break
		}
	}
	return resp
}

// hostError is an error the host answered with: the text the Host method's
// error had, and the error it wrapped, when the host named one.
type hostError struct {
	msg string
	err error
}

func (e *hostError) Error() string { return e.msg }
func (e *hostError) Unwrap() error { return e.err }

func opError(resp response) error {
	e := &hostError{msg: resp.Error}
	for _, c := range errorCodes {
		if c.code == resp.Code {
			e.err = c.err
			break
		}
	}
	return e
}

// maxSocketPath is the longest path a unix socket binds (panerun's rule).
var maxSocketPath = len(syscall.RawSockaddrUnix{}.Path) - 1

func checkSocketPath(path string) error {
	if len(path) > maxSocketPath {
		return fmt.Errorf("matehost: socket path %s is %d bytes; a unix socket path holds at most %d", path, len(path), maxSocketPath)
	}
	return nil
}

// Serve answers the socket at path until ctx ends. stop is called after a
// stop request was answered, so the caller can exit. When it ends, every
// subscriber is closed after what is queued for it, and Serve returns once
// they are gone.
func (h *Host) Serve(ctx context.Context, path string, stop func()) error {
	if err := checkSocketPath(path); err != nil {
		return err
	}
	h.openSubscribers()
	defer h.closeSubscribers()
	// A socket file left by a host that crashed would refuse the bind; the
	// host lock already says no other host is alive.
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	defer os.Remove(path)
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go h.serveConn(conn, stop)
	}
}

func (h *Host) serveConn(conn net.Conn, stop func()) {
	defer conn.Close()
	var req request
	if err := json.NewDecoder(bufio.NewReader(conn)).Decode(&req); err != nil {
		_ = json.NewEncoder(conn).Encode(response{Error: err.Error()})
		return
	}
	if req.Op == OpSubscribe {
		h.serveSubscriber(conn)
		return
	}
	resp := h.answer(req)
	_ = json.NewEncoder(conn).Encode(resp)
	if req.Op == OpStop && resp.OK && stop != nil {
		stop()
	}
}

func (h *Host) answer(req request) response {
	switch req.Op {
	case OpPing:
		return response{OK: true}
	case OpState:
		s := h.State()
		return response{OK: true, State: &s}
	case OpDeliver:
		if err := h.Deliver(req.Text); err != nil {
			busy := errors.Is(err, ErrBusy) || errors.Is(err, ErrQueued) || errors.Is(err, ErrStopping)
			return response{Error: err.Error(), Busy: busy}
		}
		s := h.State()
		return response{OK: true, State: &s}
	case OpStop:
		killed := h.Stop(time.Duration(req.WaitMS)*time.Millisecond, req.Force)
		return response{OK: true, Killed: killed}
	case OpHistory:
		return response{OK: true, Items: History(h.history, req.N)}
	case OpClearDraft:
		h.ClearDraft()
		return response{OK: true}
	case OpReload:
		if req.Launch == nil {
			return response{Error: "a reload names no launch"}
		}
		if err := h.Reload(*req.Launch); err != nil {
			busy := errors.Is(err, ErrBusy) || errors.Is(err, ErrQueued) || errors.Is(err, ErrStopping)
			return response{Error: err.Error(), Busy: busy}
		}
		return response{OK: true}
	case OpSubmit:
		held, err := h.Say(req.Text)
		if err != nil {
			resp := failed(err)
			resp.Busy = errors.Is(err, ErrStopping)
			return resp
		}
		return h.heldResponse(held)
	case OpInterrupt:
		if req.News > 0 {
			return response{OK: true, Interrupted: h.InterruptSession(req.News)}
		}
		return response{OK: true, Interrupted: h.Interrupt()}
	case OpAnswer:
		held, err := h.Answer(req.News, req.Text)
		if err != nil {
			return failed(err)
		}
		return h.heldResponse(held)
	case OpSessionAsk:
		if err := h.OpenSession(req.News); err != nil {
			return response{Error: err.Error()}
		}
		if err := h.AskSession(req.News, req.Text); err != nil {
			return response{Error: err.Error(), Busy: errors.Is(err, ErrSessionBusy)}
		}
		return response{OK: true, Sessions: h.Sessions()}
	case OpSessionClose:
		h.CloseSession(req.News)
		return response{OK: true}
	case OpSessions:
		return response{OK: true, Sessions: h.Sessions()}
	case OpSetDraft:
		h.SetDraftBy(req.Text, req.From)
		return response{OK: true}
	case OpTakeLast:
		text, ok := h.TakeLast()
		return response{OK: true, Text: text, Taken: ok}
	case OpSeenNews:
		h.SeenNews(req.News)
		return response{OK: true}
	case OpSessionOpen:
		if err := h.OpenSession(req.News); err != nil {
			return failed(err)
		}
		return response{OK: true}
	case OpSessionInterrupt:
		return response{OK: true, Interrupted: h.InterruptSession(req.News)}
	case OpWorkOkay:
		held, err := h.Okay(req.Work)
		if err != nil {
			return failed(err)
		}
		return h.heldResponse(held)
	case OpUndo:
		held, err := h.Undo(req.Held)
		if err != nil {
			return failed(err)
		}
		return response{OK: true, Held: &held}
	case OpWorkRefresh:
		h.RefreshWork()
		return response{OK: true}
	}
	return response{Error: fmt.Sprintf("unknown op %q", req.Op)}
}

// heldResponse answers an op that put held on hold: it and the state.
func (h *Host) heldResponse(held Held) response {
	s := h.State()
	return response{OK: true, State: &s, Held: &held}
}

// Refused reports whether err is the host's answer to an op (a refusal, or
// a busy Mate) rather than a host that could not be reached.
func Refused(err error) bool {
	var refused *hostError
	var busy *BusyError
	return errors.As(err, &refused) || errors.As(err, &busy)
}

// Client talks to a host's socket.
type Client struct {
	Path string
	// Timeout bounds one request; zero means 5 seconds. A stop is given its
	// own wait on top.
	Timeout time.Duration
}

// Dial is a client for the Mate in mateDir.
func Dial(mateDir string) Client { return Client{Path: SocketPath(mateDir)} }

// BusyError is a delivery the host refused because the Mate is busy.
type BusyError struct{ Reason string }

func (e *BusyError) Error() string { return e.Reason }

func (c Client) timeout() time.Duration {
	if c.Timeout <= 0 {
		return 5 * time.Second
	}
	return c.Timeout
}

// do is one request and its answer; a refusal comes back as an error
// that names the op.
func (c Client) do(ctx context.Context, req request, extra time.Duration) (response, error) {
	resp, err := c.roundTrip(ctx, req, extra)
	if err != nil {
		return response{}, err
	}
	if !resp.OK {
		if resp.Busy {
			return resp, &BusyError{Reason: resp.Error}
		}
		return resp, fmt.Errorf("matehost: %s: %s", req.Op, resp.Error)
	}
	return resp, nil
}

// call is do for the REPL's ops: a refusal is the Host method's own error
// (hostError).
func (c Client) call(ctx context.Context, req request) (response, error) {
	resp, err := c.roundTrip(ctx, req, 0)
	if err != nil {
		return response{}, err
	}
	if !resp.OK {
		return resp, opError(resp)
	}
	return resp, nil
}

func (c Client) roundTrip(ctx context.Context, req request, extra time.Duration) (response, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout()+extra)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.Path)
	if err != nil {
		return response{}, err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return response{}, err
	}
	var resp response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return response{}, fmt.Errorf("matehost: %s: %w", req.Op, err)
	}
	return resp, nil
}

// Ping reports whether a host answers.
func (c Client) Ping(ctx context.Context) error {
	_, err := c.do(ctx, request{Op: OpPing}, 0)
	return err
}

// Deliver hands the host an app line; a *BusyError means try later.
func (c Client) Deliver(ctx context.Context, text string) error {
	_, err := c.do(ctx, request{Op: OpDeliver, Text: text}, 0)
	return err
}

// State asks the host what it is doing.
func (c Client) State(ctx context.Context) (State, error) {
	resp, err := c.do(ctx, request{Op: OpState}, 0)
	if err != nil {
		return State{}, err
	}
	if resp.State == nil {
		return State{}, errors.New("matehost: state: no state in the answer")
	}
	return *resp.State, nil
}

// Stop asks the host to finish (or, with force, kill) its turn and exit.
func (c Client) Stop(ctx context.Context, wait time.Duration, force bool) (killed bool, err error) {
	resp, err := c.do(ctx, request{Op: OpStop, Force: force, WaitMS: wait.Milliseconds()}, wait)
	return resp.Killed, err
}

// History is the host's last n items.
func (c Client) History(ctx context.Context, n int) ([]Item, error) {
	resp, err := c.do(ctx, request{Op: OpHistory, N: n}, 0)
	return resp.Items, err
}

// ClearDraft drops the captain's unsent draft.
func (c Client) ClearDraft(ctx context.Context) error {
	_, err := c.do(ctx, request{Op: OpClearDraft}, 0)
	return err
}

// Reload switches the host to l between turns (Host.Reload); a *BusyError
// means a turn runs or waits. A host older than the op answers it as
// unknown.
func (c Client) Reload(ctx context.Context, l Launch) error {
	_, err := c.do(ctx, request{Op: OpReload, Launch: &l}, 0)
	return err
}

// Submit hands the host a captain message, exactly as one typed in the
// REPL's composer: the host holds it UndoDelay (Host.Say), then it runs now
// or after the turn in flight. A host older than the hold answers with no
// Held, having started or queued it at once.
func (c Client) Submit(ctx context.Context, text string) (Held, error) {
	resp, err := c.do(ctx, request{Op: OpSubmit, Text: text}, 0)
	return heldOf(resp), err
}

// heldOf is what an op's answer says it put on hold, or took back.
func heldOf(resp response) Held {
	if resp.Held == nil {
		return Held{}
	}
	return *resp.Held
}

// Interrupt stops the Mate's turn in flight, or with news > 0 that news's
// session run. It reports whether there was one.
func (c Client) Interrupt(ctx context.Context, news int) (bool, error) {
	resp, err := c.do(ctx, request{Op: OpInterrupt, News: news}, 0)
	return resp.Interrupted, err
}

// AnswerNews is the captain answering news id: held UndoDelay
// (Host.Answer), then sent (Host.AnswerNews).
func (c Client) AnswerNews(ctx context.Context, id int, text string) (Held, error) {
	resp, err := c.do(ctx, request{Op: OpAnswer, News: id, Text: text}, 0)
	return heldOf(resp), err
}

// AskSession opens news id's session if it is not open and asks it text.
// A *BusyError means the session is still answering.
func (c Client) AskSession(ctx context.Context, id int, text string) error {
	_, err := c.do(ctx, request{Op: OpSessionAsk, News: id, Text: text}, 0)
	return err
}

// CloseSession closes news id's session.
func (c Client) CloseSession(ctx context.Context, id int) error {
	_, err := c.do(ctx, request{Op: OpSessionClose, News: id}, 0)
	return err
}

// Sessions are the host's open topic sessions.
func (c Client) Sessions(ctx context.Context) ([]SessionInfo, error) {
	resp, err := c.do(ctx, request{Op: OpSessions}, 0)
	return resp.Sessions, err
}

// SetDraft records what the captain has typed and not sent (Host.SetDraft).
func (c Client) SetDraft(ctx context.Context, text string) error {
	_, err := c.call(ctx, request{Op: OpSetDraft, Text: text})
	return err
}

// TakeLast takes back the newest queued captain message (Host.TakeLast).
func (c Client) TakeLast(ctx context.Context) (string, bool, error) {
	resp, err := c.call(ctx, request{Op: OpTakeLast})
	return resp.Text, resp.Taken, err
}

// SeenNews marks news id as opened by the captain (Host.SeenNews).
func (c Client) SeenNews(ctx context.Context, id int) error {
	_, err := c.call(ctx, request{Op: OpSeenNews, News: id})
	return err
}

// OpenSession opens news id's session, or shows the one open
// (Host.OpenSession).
func (c Client) OpenSession(ctx context.Context, id int) error {
	_, err := c.call(ctx, request{Op: OpSessionOpen, News: id})
	return err
}

// InterruptSession stops news id's session run in flight and reports
// whether there was one (Host.InterruptSession).
func (c Client) InterruptSession(ctx context.Context, id int) (bool, error) {
	resp, err := c.call(ctx, request{Op: OpSessionInterrupt, News: id})
	return resp.Interrupted, err
}

// OkayWork is the captain's tick on done row id of the work board: held
// UndoDelay (Host.Okay), then written (Host.OkayWork).
func (c Client) OkayWork(ctx context.Context, id int) (Held, error) {
	resp, err := c.call(ctx, request{Op: OpWorkOkay, Work: id})
	return heldOf(resp), err
}

// Undo takes back held item id before it goes (Host.Undo); errors.Is
// ErrNotHeld once it has gone.
func (c Client) Undo(ctx context.Context, id int) (Held, error) {
	resp, err := c.call(ctx, request{Op: OpUndo, Held: id})
	return heldOf(resp), err
}

// RefreshWork has the host read the work board again (Host.RefreshWork).
func (c Client) RefreshWork(ctx context.Context) error {
	_, err := c.call(ctx, request{Op: OpWorkRefresh})
	return err
}
