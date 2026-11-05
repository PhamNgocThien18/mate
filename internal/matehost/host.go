package matehost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
)

// Sources of a turn.
const (
	SourceCaptain = "captain"
	SourceApp     = "app"
)

// Item is one message and what came of it: what the REPL shows and the
// history keeps.
type Item struct {
	At     time.Time `json:"at"`
	Source string    `json:"source"`
	// Label is an app line's tag (Label); empty for the captain.
	Label string `json:"label,omitempty"`
	Text  string `json:"text"`
	Reply string `json:"reply,omitempty"`
	// Silent is a reply that had nothing for the captain (Silent).
	Silent      bool   `json:"silent,omitempty"`
	Interrupted bool   `json:"interrupted,omitempty"`
	Error       string `json:"error,omitempty"`
	Done        bool   `json:"done,omitempty"`
	// Tools are the tools the turn called, in order.
	Tools []ToolRecord `json:"tools,omitempty"`
	// MS is how long the turn took; Tokens its tokens other than cache
	// reads; Context the context of its last request, in tokens.
	MS      int64 `json:"ms,omitempty"`
	Tokens  int   `json:"tokens,omitempty"`
	Context int   `json:"context,omitempty"`
	// News is the news the item belongs to: the one a captain message
	// answers, or the one an app line's turn opened. Such an item shows in
	// its news, not in the chat.
	News int `json:"news,omitempty"`
	// Lead is where the running turn's message in progress starts in
	// Reply: what came before it was written before a tool, and is not
	// shown.
	Lead int `json:"-"`
}

// ToolRecord is one tool a turn called.
type ToolRecord struct {
	ID      string    `json:"-"`
	Name    string    `json:"name"`
	Detail  string    `json:"detail,omitempty"`
	Started time.Time `json:"-"`
	// MS is how long the call took; Done and Error say how it ended. A
	// call that never ended was cut off with its turn.
	MS    int64 `json:"ms,omitempty"`
	Done  bool  `json:"done,omitempty"`
	Error bool  `json:"error,omitempty"`
}

// Visible reports whether the REPL shows the item's reply.
func (it Item) Visible() bool { return !it.Silent }

// State is what the host tells the rest of mate.
type State struct {
	Busy bool `json:"busy"`
	// Queued counts captain messages waiting for the current turn.
	Queued int `json:"queued"`
	// Draft is what the captain has typed and not sent.
	Draft         string    `json:"draft,omitempty"`
	TurnSource    string    `json:"turn_source,omitempty"`
	TurnStartedAt time.Time `json:"turn_started_at,omitzero"`
	Stopping      bool      `json:"stopping,omitempty"`
}

// Event is what the host tells its REPL.
type Event struct {
	// Item is the turn as it stands: set when it starts, as it streams
	// text and calls tools (Done false), and when it ends (Done true).
	Item *Item
	// Queue are the captain messages waiting, oldest first.
	Queue []string
	// DraftCleared is set when the console dropped the captain's draft.
	DraftCleared bool
	// News is every news, oldest first.
	News []News
	// Note is something the REPL should tell the captain.
	Note string
	// Focus is a news the console asked the REPL to open.
	Focus int
	// Work is the Mate's work board, oldest row first (CON-158).
	Work []Work
	// FocusWork is set when the console asked the REPL to show the board.
	FocusWork bool
	// Sessions are the open topic sessions, by news.
	Sessions []SessionInfo
	// State is the host's state after the event.
	State State
}

// Reporter tells Herdr what the host is doing (herdrReporter).
type Reporter interface {
	Report(state string)
}

// Host serialises the Mate's messages: one turn at a time, captain
// messages queued behind it, app lines refused while it runs.
type Host struct {
	runner   *Runner
	history  string
	reporter Reporter
	now      func() time.Time
	notify   func(Event)

	mu       sync.Mutex
	run      *Run
	cur      *Item
	started  time.Time
	queue    []message
	newsPath string
	news     []News
	newsSeen time.Time
	work     []Work
	workSeen time.Time
	draft    string
	stopping bool
	idle     chan struct{}

	// Sessions (session.go): the open ones by news, and what they run with.
	sessions  map[int]*session
	forks     harness.ForkTurns
	allow     []string
	mateBin   string
	topicsDir string

	// Socket subscribers (subscribe.go). subMu is taken before mu, never
	// after it.
	subMu           sync.Mutex
	subs            map[*lineStream]struct{}
	subsClosed      bool
	subWG           sync.WaitGroup
	subQueue        int
	subWriteTimeout time.Duration
}

// Options configure a Host.
type Options struct {
	// History is the file items are appended to; empty keeps none.
	History string
	// News is the news log (NewsPath); empty keeps none.
	News     string
	Reporter Reporter
	Now      func() time.Time
	// Topics is where sessions keep their files (TopicsPath); Forks runs
	// them, nil when the harness cannot; Allow are the commands a session
	// may run (ForkAllow); MateBin is the mate binary the Mate's manual
	// names.
	Topics  string
	Forks   harness.ForkTurns
	Allow   []string
	MateBin string
}

// New makes a host for runner.
func New(runner *Runner, opts Options) *Host {
	h := &Host{runner: runner, history: opts.History, newsPath: opts.News, reporter: opts.Reporter, now: opts.Now, idle: make(chan struct{}),
		sessions: map[int]*session{}, forks: opts.Forks, allow: opts.Allow, mateBin: opts.MateBin, topicsDir: opts.Topics,
		subs: map[*lineStream]struct{}{}, subQueue: subscribeQueue, subWriteTimeout: subscribeWriteTimeout}
	if h.now == nil {
		h.now = time.Now
	}
	close(h.idle)
	return h
}

// HistoryPath is the history file of the Mate in mateDir.
func HistoryPath(mateDir string) string { return filepath.Join(mateDir, HistoryName) }

// Subscribe sets the one listener for events in this process: the REPL.
// Clients of the socket subscribe there (OpSubscribe) instead.
func (h *Host) Subscribe(fn func(Event)) {
	h.mu.Lock()
	h.notify = fn
	h.mu.Unlock()
}

// Errors a delivery or a message can be refused with.
var (
	ErrStopping = errors.New("the Mate is stopping")
	ErrQueued   = errors.New("the captain has messages waiting")
)

// message is one turn to run: what the captain or the app said, the prompt
// the Mate is given for it, and the news it belongs to.
type message struct {
	Source, Text, Prompt string
	News                 int
}

// Submit is a captain message: it runs now, or after the turn in flight.
func (h *Host) Submit(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	h.submit(message{Source: SourceCaptain, Text: text, Prompt: text})
}

// AnswerNews is the captain answering news id: a turn that tells the Mate
// which news it answers, shown in that news, run now or queued like any
// captain message.
func (h *Host) AnswerNews(id int, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if h.newsPath == "" {
		return ErrNoNews
	}
	var n News
	err := withNewsLog(h.newsPath, func(list []News, f *os.File) error {
		var ok bool
		if n, ok = findNews(list, id); !ok {
			return fmt.Errorf("%w: #%d", ErrNoNews, id)
		}
		if n.Closed() {
			// An answer would open it again and spend a Mate turn closing it.
			return fmt.Errorf("news #%d is already closed", id)
		}
		return writeNewsEvent(f, newsEvent{At: h.now(), Op: opAnswer, ID: id, Text: text})
	})
	if err != nil {
		return err
	}
	h.RefreshNews()
	h.submit(message{Source: SourceCaptain, Text: text, Prompt: newsAnswerPrompt(n, text), News: id})
	return nil
}

func (h *Host) submit(msg message) {
	h.mu.Lock()
	if h.stopping {
		h.mu.Unlock()
		return
	}
	if h.run != nil || len(h.queue) > 0 {
		h.queue = append(h.queue, msg)
		ev := h.eventLocked(nil)
		h.mu.Unlock()
		h.emit(ev)
		return
	}
	h.mu.Unlock()
	if err := h.start(msg); errors.Is(err, ErrBusy) {
		// Another process holds the turn lock: wait in the queue and retry.
		h.mu.Lock()
		h.queue = append([]message{msg}, h.queue...)
		ev := h.eventLocked(nil)
		h.mu.Unlock()
		h.emit(ev)
		go h.retryQueue()
	} else if err != nil {
		h.finishWithError(msg, err)
	}
}

// Deliver is an app line. It is refused rather than queued while anything
// runs or waits, so the outbox keeps the line and its own retry and wedged
// rules (docs/mvp.md M21). It returns once the turn has started.
func (h *Host) Deliver(text string) error {
	h.mu.Lock()
	switch {
	case h.stopping:
		h.mu.Unlock()
		return ErrStopping
	case h.run != nil:
		h.mu.Unlock()
		return ErrBusy
	case len(h.queue) > 0:
		h.mu.Unlock()
		return ErrQueued
	}
	h.mu.Unlock()
	return h.start(message{Source: SourceApp, Text: text, Prompt: text})
}

// start runs one turn. Only one start can win: run is checked again under
// the lock it is set under.
func (h *Host) start(msg message) error {
	h.mu.Lock()
	if h.run != nil {
		h.mu.Unlock()
		return ErrBusy
	}
	run, err := h.runner.Start(msg.Prompt)
	if err != nil {
		h.mu.Unlock()
		return err
	}
	item := &Item{At: h.now(), Source: msg.Source, Text: msg.Text, News: msg.News}
	if msg.Source == SourceApp {
		item.Label = Label(msg.Text)
	}
	h.run, h.cur, h.started = run, item, item.At
	h.idle = make(chan struct{})
	ev := h.eventLocked(item)
	h.mu.Unlock()
	h.report("working")
	h.emit(ev)
	go h.wait(run, item)
	return nil
}

func (h *Host) wait(run *Run, item *Item) {
	var st streamState
	reply := run.Wait(func(ev harness.TurnEvent) {
		h.mu.Lock()
		changed := h.progressLocked(item, ev, &st)
		var out Event
		if changed {
			out = h.eventLocked(item)
		}
		h.mu.Unlock()
		if changed {
			h.emit(out)
		}
	})
	// The finished item is a copy: the running one stays as it was while
	// it is still h.cur, which a subscriber's first event reads under mu.
	h.mu.Lock()
	result := strings.TrimSpace(reply.Text)
	done := *item
	done.Tools = append([]ToolRecord(nil), item.Tools...)
	done.Reply = finalReply(strings.TrimSpace(item.Reply), result)
	if reply.DurationMS > 0 {
		done.MS = reply.DurationMS
	} else {
		done.MS = h.now().Sub(item.At).Milliseconds()
	}
	done.Tokens = reply.Tokens
	h.mu.Unlock()
	done.Interrupted = reply.Interrupted
	done.Silent = item.Source == SourceApp && Silent(result) && reply.Err == nil
	if reply.Err != nil {
		done.Error = reply.Err.Error()
	} else if reply.IsError && result != "" {
		done.Error = result
	}
	done.Done = true
	h.settleNews(&done)
	h.appendHistory(done)

	h.mu.Lock()
	h.run, h.cur = nil, nil
	var next *message
	if len(h.queue) > 0 && !h.stopping {
		next, h.queue = &h.queue[0], h.queue[1:]
	}
	close(h.idle)
	ev := h.eventLocked(&done)
	h.mu.Unlock()
	if next == nil {
		h.report("idle")
	}
	h.emit(ev)
	if next != nil {
		if err := h.start(*next); errors.Is(err, ErrBusy) {
			h.mu.Lock()
			h.queue = append([]message{*next}, h.queue...)
			h.mu.Unlock()
			go h.retryQueue()
		} else if err != nil {
			h.finishWithError(*next, err)
		}
	}
}

// settleNews ties a finished turn to its news. An app line whose turn
// opened news belongs to the first it opened, so its reply shows there and
// not in the chat; a failed answer leaves its news waiting on the captain
// again.
func (h *Host) settleNews(item *Item) {
	if h.newsPath == "" {
		return
	}
	switch {
	case item.News != 0 && (item.Error != "" || item.Interrupted):
		_ = newsOp(h.newsPath, newsEvent{At: h.now(), Op: opFailed, ID: item.News}, true)
	case item.Source == SourceApp:
		list, _ := LoadNews(h.newsPath)
		for _, n := range list {
			if !n.OpenedAt.Before(item.At) {
				item.News = n.ID
				break
			}
		}
	}
	h.RefreshNews()
}

// retryQueue starts the queue's head once the turn lock is free again; it
// is held by another process (a second host, or a test) only briefly.
func (h *Host) retryQueue() {
	for {
		time.Sleep(time.Second)
		h.mu.Lock()
		if h.stopping || h.run != nil || len(h.queue) == 0 {
			h.mu.Unlock()
			return
		}
		next := h.queue[0]
		h.queue = h.queue[1:]
		h.mu.Unlock()
		switch err := h.start(next); {
		case err == nil:
			return
		case errors.Is(err, ErrBusy):
			h.mu.Lock()
			h.queue = append([]message{next}, h.queue...)
			h.mu.Unlock()
		default:
			h.finishWithError(next, err)
			return
		}
	}
}

func (h *Host) finishWithError(msg message, err error) {
	item := Item{At: h.now(), Source: msg.Source, Text: msg.Text, News: msg.News, Error: err.Error(), Done: true}
	h.settleNews(&item)
	h.appendHistory(item)
	h.mu.Lock()
	ev := h.eventLocked(&item)
	h.mu.Unlock()
	h.emit(ev)
}

// Interrupt stops the turn in flight, if any.
func (h *Host) Interrupt() bool {
	h.mu.Lock()
	run := h.run
	h.mu.Unlock()
	if run == nil {
		return false
	}
	run.Interrupt()
	return true
}

// SetDraft records what the captain has typed and not sent.
func (h *Host) SetDraft(text string) {
	h.mu.Lock()
	h.draft = text
	h.mu.Unlock()
}

// ClearDraft drops the captain's draft; the REPL picks it up as an event.
func (h *Host) ClearDraft() {
	h.mu.Lock()
	h.draft = ""
	ev := h.eventLocked(nil)
	ev.DraftCleared = true
	h.mu.Unlock()
	h.emit(ev)
}

// TakeLast removes the newest queued captain message and returns it, for
// the captain to edit or drop.
func (h *Host) TakeLast() (string, bool) {
	h.mu.Lock()
	if len(h.queue) == 0 {
		h.mu.Unlock()
		return "", false
	}
	last := h.queue[len(h.queue)-1]
	if last.News != 0 {
		// An answer to a news is not taken back into the chat's composer.
		h.mu.Unlock()
		return "", false
	}
	text := last.Text
	h.queue = h.queue[:len(h.queue)-1]
	ev := h.eventLocked(nil)
	h.mu.Unlock()
	h.emit(ev)
	return text, true
}

// State is the host's state now.
func (h *Host) State() State {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.stateLocked()
}

func (h *Host) stateLocked() State {
	s := State{Busy: h.run != nil, Queued: len(h.queue), Draft: h.draft, Stopping: h.stopping}
	if h.cur != nil {
		s.TurnSource, s.TurnStartedAt = h.cur.Source, h.started
	}
	return s
}

// Reload makes every turn from now on run l: a fresh session for the same
// Mate, in the pane the host already runs in (context refresh, CON-133).
// Closing the pane to start again ended every `herdr agent attach` on it,
// the console's stage among them. It is refused while a turn runs or
// waits, so no turn is split across two sessions and nothing writes the
// old transcript once it returns.
func (h *Host) Reload(l Launch) error {
	if err := l.valid(); err != nil {
		return err
	}
	h.mu.Lock()
	cur := h.runner.Launch
	switch {
	case h.stopping:
		h.mu.Unlock()
		return ErrStopping
	case h.run != nil:
		h.mu.Unlock()
		return ErrBusy
	case len(h.queue) > 0:
		h.mu.Unlock()
		return ErrQueued
	case l.Project != cur.Project || l.Agent != cur.Agent || l.Harness != cur.Harness:
		h.mu.Unlock()
		return fmt.Errorf("matehost: a reload is for %s as %s on %s, this host runs %s as %s on %s",
			l.Project, l.Agent, l.Harness, cur.Project, cur.Agent, cur.Harness)
	}
	h.runner.Launch, h.runner.turns = l, 0
	ev := h.eventLocked(nil)
	ev.Note = "context refreshed; new session " + l.SessionID
	h.mu.Unlock()
	h.emit(ev)
	return nil
}

// Stop refuses every new message and waits up to wait for the turn in
// flight; force, or a turn still running when wait is up, kills it. Queued
// captain messages are dropped. It reports whether a turn had to be killed.
func (h *Host) Stop(wait time.Duration, force bool) (killed bool) {
	h.mu.Lock()
	h.stopping = true
	h.queue = nil
	run, idle := h.run, h.idle
	for _, s := range h.sessions {
		if s.run != nil {
			s.run.Kill()
		}
	}
	ev := h.eventLocked(nil)
	h.mu.Unlock()
	h.emit(ev)
	if run == nil {
		return false
	}
	if !force {
		select {
		case <-idle:
			return false
		case <-time.After(wait):
		}
	}
	run.Kill()
	<-idle
	return true
}

// finalReply is what a finished turn keeps as its reply: the result, the
// turn's last message, which is what the Mate says to the captain. The
// text it wrote before and between its tools is its own narration and
// stays out of the chat (CON-151, which reverses CON-79). A run with no
// result (interrupted, or failed: Runner sets Reply.Err only when no
// result came) keeps how far it got.
func finalReply(streamed, result string) string {
	if result != "" {
		return result
	}
	return streamed
}

// streamState is what progressLocked remembers between a turn's events.
type streamState struct {
	// streamed is set once the run streams text (Delta).
	streamed bool
	// para is set when a streamed message has ended, so the next
	// streamed text starts a new paragraph instead of gluing onto it.
	para bool
}

// progressLocked folds one run event into the running item and reports
// whether it changed anything the REPL shows. A run that streams text
// (Delta) is shown as it streams; one that does not shows each message's
// text (Said) as it arrives. Each message is its own paragraph.
func (h *Host) progressLocked(item *Item, ev harness.TurnEvent, st *streamState) bool {
	changed := false
	if ev.Delta != "" {
		if st.para {
			item.Reply = paragraph(item.Reply)
		}
		st.streamed, st.para = true, false
		item.Reply += ev.Delta
		changed = true
	} else if ev.Said != "" {
		if st.streamed {
			// The message whose text streamed has ended.
			st.para = true
		} else {
			item.Reply = paragraph(item.Reply) + ev.Said
			changed = true
		}
	}
	if ev.Context > 0 {
		item.Context = ev.Context
	}
	for _, c := range ev.Calls {
		if st.streamed && item.Reply != "" && !strings.HasSuffix(item.Reply, "\n\n") {
			// Text after a tool is a new paragraph.
			item.Reply = strings.TrimRight(item.Reply, "\n") + "\n\n"
		}
		item.Tools = append(item.Tools, ToolRecord{ID: c.ID, Name: c.Name, Detail: c.Detail, Started: h.now()})
		item.Lead = len(item.Reply)
		changed = true
	}
	for _, r := range ev.Results {
		for i := range item.Tools {
			if t := &item.Tools[i]; t.ID != "" && t.ID == r.ID && !t.Done {
				t.Done, t.Error, t.MS = true, r.IsError, h.now().Sub(t.Started).Milliseconds()
				changed = true
			}
		}
	}
	return changed
}

// paragraph is reply ready for a new paragraph to follow: unchanged when
// empty or already ending in a blank line.
func paragraph(reply string) string {
	if reply == "" || strings.HasSuffix(reply, "\n\n") {
		return reply
	}
	return strings.TrimRight(reply, "\n") + "\n\n"
}

// eventLocked is an event carrying a copy of item (which may be nil), the
// queue and the state.
func (h *Host) eventLocked(item *Item) Event {
	ev := Event{State: h.stateLocked(), News: h.news, Work: h.work, Sessions: h.sessionsLocked()}
	for _, q := range h.queue {
		text := q.Text
		if q.News != 0 {
			text = fmt.Sprintf("#%d · %s", q.News, text)
		}
		ev.Queue = append(ev.Queue, text)
	}
	if item != nil {
		cp := *item
		cp.Tools = append([]ToolRecord(nil), item.Tools...)
		ev.Item = &cp
	}
	return ev
}

// RefreshNews reads the news log again and tells the REPL when it changed.
func (h *Host) RefreshNews() {
	if h.newsPath == "" {
		return
	}
	fi, err := os.Stat(h.newsPath)
	if err != nil {
		return
	}
	list, err := LoadNews(h.newsPath)
	if err != nil {
		return
	}
	h.mu.Lock()
	h.news, h.newsSeen = list, fi.ModTime()
	ev := h.eventLocked(nil)
	h.mu.Unlock()
	h.emit(ev)
}

// RefreshWork reads the work board again and tells the REPL when it
// changed. The board lives beside the news log.
func (h *Host) RefreshWork() {
	if h.newsPath == "" {
		return
	}
	path := WorkPath(filepath.Dir(h.newsPath))
	fi, err := os.Stat(path)
	if err != nil {
		return
	}
	list, err := LoadWork(path)
	if err != nil {
		return
	}
	h.mu.Lock()
	h.work, h.workSeen = list, fi.ModTime()
	ev := h.eventLocked(nil)
	h.mu.Unlock()
	h.emit(ev)
}

// OkayWork is the captain's tick on a done row of the board, from the
// REPL's Work page.
func (h *Host) OkayWork(id int) error {
	if h.newsPath == "" {
		return ErrNoWork
	}
	if err := OkayWork(WorkPath(filepath.Dir(h.newsPath)), id, h.now()); err != nil {
		return err
	}
	h.RefreshWork()
	return nil
}

// WatchNews reads the news log and the work board whenever they change -
// the Mate writes them with `mate news` and `mate work` - until ctx ends.
func (h *Host) WatchNews(ctx context.Context, every time.Duration) {
	h.RefreshNews()
	h.RefreshWork()
	dir := filepath.Dir(h.newsPath)
	focusPath := filepath.Join(dir, NewsFocusName)
	workFocusPath := filepath.Join(dir, WorkFocusName)
	workPath := WorkPath(dir)
	// A request made before this REPL started is not the captain's now.
	asked, _ := readNewsFocus(focusPath)
	askedWork, _ := readFocusAt(workFocusPath)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		h.reapSessions()
		fi, err := os.Stat(h.newsPath)
		h.mu.Lock()
		seen := h.newsSeen
		h.mu.Unlock()
		if err == nil && !fi.ModTime().Equal(seen) {
			h.RefreshNews()
		}
		if fi, err := os.Stat(workPath); err == nil && h.newsPath != "" {
			h.mu.Lock()
			seen := h.workSeen
			h.mu.Unlock()
			if !fi.ModTime().Equal(seen) {
				h.RefreshWork()
			}
		}
		if at, ok := readFocusAt(workFocusPath); ok && h.newsPath != "" && at.After(askedWork) {
			askedWork = at
			h.mu.Lock()
			ev := h.eventLocked(nil)
			h.mu.Unlock()
			ev.FocusWork = true
			h.emit(ev)
		}
		if f, ok := readNewsFocus(focusPath); ok && h.newsPath != "" && f.At.After(asked.At) {
			asked = f
			h.mu.Lock()
			ev := h.eventLocked(nil)
			h.mu.Unlock()
			ev.Focus = f.ID
			h.emit(ev)
		}
	}
}

// SeenNews marks a news the captain has opened: it no longer reads as new.
func (h *Host) SeenNews(id int) {
	if h.newsPath == "" {
		return
	}
	if n, ok := findNews(h.newsSnapshot(), id); ok && n.Status == NewsNew {
		_ = newsOp(h.newsPath, newsEvent{At: h.now(), Op: opSeen, ID: id}, false)
		h.RefreshNews()
	}
}

// ArchiveNews takes a closed news out of the list.
func (h *Host) ArchiveNews(id int) {
	if h.newsPath == "" {
		return
	}
	_ = newsOp(h.newsPath, newsEvent{At: h.now(), Op: opArchive, ID: id}, true)
	h.RefreshNews()
}

func (h *Host) emitNote(err error) {
	h.mu.Lock()
	ev := h.eventLocked(nil)
	h.mu.Unlock()
	ev.Note = err.Error()
	h.emit(ev)
}

func (h *Host) newsSnapshot() []News {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.news
}

// emit tells the REPL and every socket subscriber. Subscribers are handed
// the event without waiting for them (publish).
func (h *Host) emit(ev Event) {
	h.mu.Lock()
	fn := h.notify
	h.mu.Unlock()
	h.publish(ev)
	if fn != nil {
		fn(ev)
	}
}

func (h *Host) report(state string) {
	if h.reporter != nil {
		h.reporter.Report(state)
	}
}

func (h *Host) appendHistory(it Item) {
	if h.history == "" {
		return
	}
	appendItemFile(h.history, it)
}

// appendItemFile adds it to the history file at path, one item per line.
func appendItemFile(path string, it Item) {
	data, err := json.Marshal(it)
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(data, '\n'))
}

// History returns the last n items of a history file, oldest first. A
// missing file has none; a line that does not parse is skipped.
func History(path string, n int) []Item {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var items []Item
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for sc.Scan() {
		var it Item
		if json.Unmarshal(sc.Bytes(), &it) == nil {
			items = append(items, it)
		}
	}
	if n > 0 && len(items) > n {
		items = items[len(items)-n:]
	}
	return items
}
