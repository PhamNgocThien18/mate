package matehost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

// A client that subscribes (OpSubscribe) keeps its connection open: the
// host answers the request with one ok line, then writes every Event as
// one JSON line, in the order emit was called, starting with an Event of
// how things stand (the turn in flight, the queue, news, work, sessions,
// the state with the draft). The REPL in the host's process still gets the
// same events through its callback (Host.Subscribe); subscribers are on
// top of it.
//
// A subscriber that falls behind by more than subscribeQueue events, or
// whose write takes longer than subscribeWriteTimeout, is cut off: the
// host closes its connection and it is up to the client to connect again.
// emit never waits on a subscriber.
const (
	subscribeQueue        = 256
	subscribeWriteTimeout = 10 * time.Second
	// subscribeGrace is how long a stopping host gives its subscribers to
	// take the events still queued for them.
	subscribeGrace = time.Second
)

// errShuttingDown refuses a subscriber once Serve is ending.
var errShuttingDown = errors.New("the host is shutting down")

// publish hands ev to every subscriber. It is called by emit, in emit's
// order: subMu makes every subscriber see the events in the same order,
// and a subscriber's first event in the same order as the rest.
func (h *Host) publish(ev Event) {
	h.subMu.Lock()
	defer h.subMu.Unlock()
	if len(h.subs) == 0 {
		return
	}
	line, err := eventLine(ev)
	for s := range h.subs {
		if err != nil {
			// A subscriber that missed an event would draw a stale state;
			// cut it off, and it subscribes again from how things stand.
			s.abort()
			delete(h.subs, s)
			continue
		}
		if !s.Put(line) {
			delete(h.subs, s)
		}
	}
}

// serveSubscriber keeps conn as a subscriber until it ends: the client
// closes it, falls behind, or Serve ends.
func (h *Host) serveSubscriber(conn net.Conn) {
	h.subMu.Lock()
	if h.subsClosed {
		h.subMu.Unlock()
		_ = json.NewEncoder(conn).Encode(response{Error: errShuttingDown.Error()})
		return
	}
	h.mu.Lock()
	first := h.eventLocked(h.cur)
	h.mu.Unlock()
	line, err := eventLine(first)
	if err != nil {
		h.subMu.Unlock()
		_ = json.NewEncoder(conn).Encode(response{Error: err.Error()})
		return
	}
	ok, _ := json.Marshal(response{OK: true})
	s := newLineStream(conn, h.subQueue, h.subWriteTimeout)
	s.Put(append(ok, '\n'))
	s.Put(line)
	h.subs[s] = struct{}{}
	h.subWG.Add(1)
	h.subMu.Unlock()
	defer h.subWG.Done()

	// The client sends nothing more: a read ends when it closes its end.
	read := make(chan struct{})
	go func() {
		defer close(read)
		_, _ = io.Copy(io.Discard, conn)
		s.abort()
	}()
	s.run()
	<-read
	h.subMu.Lock()
	delete(h.subs, s)
	h.subMu.Unlock()
}

// closeSubscribers ends every subscriber after what is queued for it, and
// refuses new ones; it returns once they are all gone.
func (h *Host) closeSubscribers() {
	h.subMu.Lock()
	h.subsClosed = true
	for s := range h.subs {
		s.finish(subscribeGrace)
		delete(h.subs, s)
	}
	h.subMu.Unlock()
	h.subWG.Wait()
}

// openSubscribers lets subscribers in again, for a Serve that starts.
func (h *Host) openSubscribers() {
	h.subMu.Lock()
	h.subsClosed = false
	h.subMu.Unlock()
}

func eventLine(ev Event) ([]byte, error) {
	data, err := json.Marshal(ev)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// Subscribe is the host's events from now on, starting with one of how
// things stand. The channel is closed when the host closes the connection
// (it stopped, or this client fell behind and should subscribe again) or
// when ctx ends; ending ctx is how a caller lets go.
func (c Client) Subscribe(ctx context.Context) (<-chan Event, error) {
	timeout := c.timeout()
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(dialCtx, "unix", c.Path)
	if err != nil {
		return nil, err
	}
	// From here ctx ending closes the connection, the handshake included.
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
		case <-done:
		}
		_ = conn.Close()
	}()
	fail := func(err error) (<-chan Event, error) {
		close(done)
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	dec := json.NewDecoder(conn)
	if err := json.NewEncoder(conn).Encode(request{Op: OpSubscribe}); err != nil {
		return fail(fmt.Errorf("matehost: %s: %w", OpSubscribe, err))
	}
	var resp response
	if err := dec.Decode(&resp); err != nil {
		return fail(fmt.Errorf("matehost: %s: %w", OpSubscribe, err))
	}
	if !resp.OK {
		return fail(opError(resp))
	}
	_ = conn.SetDeadline(time.Time{})
	out := make(chan Event)
	go func() {
		defer close(out)
		defer close(done)
		for {
			var ev Event
			if dec.Decode(&ev) != nil {
				return
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

// The wire form of an Event (CON-221). Item and SessionInfo keep a few
// fields out of the history file and the sessions op (json:"-", or no
// tags at all), which a client drawing the REPL still needs: the running
// message's start (Lead) and the tools' ids and start times. The Event's
// own slices, an item's tools and a session's items are written even when
// empty, so nil and empty come back as they went; the slices inside News
// and Work keep their omitempty, and an empty one comes back nil.

type wireTool struct {
	ToolRecord
	ID      string    `json:"id,omitempty"`
	Started time.Time `json:"started,omitzero"`
}

type wireItem struct {
	Item
	Lead  int        `json:"lead,omitempty"`
	Tools []wireTool `json:"tools"`
}

type wireSession struct {
	News     int        `json:"news"`
	Fork     string     `json:"fork,omitempty"`
	OpenedAt time.Time  `json:"opened_at"`
	Items    []wireItem `json:"items"`
	Running  *wireItem  `json:"running,omitempty"`
}

type wireEvent struct {
	Item         *wireItem     `json:"item,omitempty"`
	Queue        []string      `json:"queue"`
	DraftCleared bool          `json:"draft_cleared,omitempty"`
	News         []News        `json:"news"`
	Note         string        `json:"note,omitempty"`
	Focus        int           `json:"focus,omitempty"`
	Work         []Work        `json:"work"`
	FocusWork    bool          `json:"focus_work,omitempty"`
	Sessions     []wireSession `json:"sessions"`
	State        State         `json:"state"`
}

// MarshalJSON writes ev in its wire form.
func (ev Event) MarshalJSON() ([]byte, error) {
	w := wireEvent{Item: toWireItemPtr(ev.Item), Queue: ev.Queue, DraftCleared: ev.DraftCleared, News: ev.News, Note: ev.Note,
		Focus: ev.Focus, Work: ev.Work, FocusWork: ev.FocusWork, State: ev.State}
	if ev.Sessions != nil {
		w.Sessions = make([]wireSession, len(ev.Sessions))
		for i, s := range ev.Sessions {
			w.Sessions[i] = wireSession{News: s.News, Fork: s.Fork, OpenedAt: s.OpenedAt, Items: toWireItems(s.Items), Running: toWireItemPtr(s.Running)}
		}
	}
	return json.Marshal(w)
}

// UnmarshalJSON reads an Event's wire form.
func (ev *Event) UnmarshalJSON(data []byte) error {
	var w wireEvent
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	*ev = Event{Item: fromWireItemPtr(w.Item), Queue: w.Queue, DraftCleared: w.DraftCleared, News: w.News, Note: w.Note,
		Focus: w.Focus, Work: w.Work, FocusWork: w.FocusWork, State: w.State}
	if w.Sessions != nil {
		ev.Sessions = make([]SessionInfo, len(w.Sessions))
		for i, s := range w.Sessions {
			ev.Sessions[i] = SessionInfo{News: s.News, Fork: s.Fork, OpenedAt: s.OpenedAt, Items: fromWireItems(s.Items), Running: fromWireItemPtr(s.Running)}
		}
	}
	return nil
}

func toWireItem(it Item) wireItem {
	w := wireItem{Item: it, Lead: it.Lead}
	w.Item.Tools = nil
	if it.Tools != nil {
		w.Tools = make([]wireTool, len(it.Tools))
		for i, t := range it.Tools {
			w.Tools[i] = wireTool{ToolRecord: t, ID: t.ID, Started: t.Started}
		}
	}
	return w
}

func fromWireItem(w wireItem) Item {
	it := w.Item
	it.Lead = w.Lead
	it.Tools = nil
	if w.Tools != nil {
		it.Tools = make([]ToolRecord, len(w.Tools))
		for i, t := range w.Tools {
			rec := t.ToolRecord
			rec.ID, rec.Started = t.ID, t.Started
			it.Tools[i] = rec
		}
	}
	return it
}

func toWireItemPtr(it *Item) *wireItem {
	if it == nil {
		return nil
	}
	w := toWireItem(*it)
	return &w
}

func fromWireItemPtr(w *wireItem) *Item {
	if w == nil {
		return nil
	}
	it := fromWireItem(*w)
	return &it
}

func toWireItems(items []Item) []wireItem {
	if items == nil {
		return nil
	}
	out := make([]wireItem, len(items))
	for i, it := range items {
		out[i] = toWireItem(it)
	}
	return out
}

func fromWireItems(items []wireItem) []Item {
	if items == nil {
		return nil
	}
	out := make([]Item, len(items))
	for i, w := range items {
		out[i] = fromWireItem(w)
	}
	return out
}
