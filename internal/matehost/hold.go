package matehost

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// What the captain sends the Mate waits UndoDelay in the host before it
// goes, so the captain can take it back (CON-222): a message, an answer to
// a news, the tick on a done row of the work board. Every UI sends through
// the host's socket (OpSubmit, OpAnswer, OpWorkOkay) and takes back
// through it (OpUndo), so the REPL, Office and City show the same item
// counting down, and one closed right after sending loses nothing: the
// host sends it. An app line (OpDeliver) is never held; only the captain's
// words have an undo.
//
// The host tells each item and when it goes (State.Held); a client counts
// the seconds down from that itself, so no event is sent per second.

// UndoDelay is how long the captain has to take back what they sent. It is
// the one place the rule is written.
const UndoDelay = 5 * time.Second

// Kinds of held items.
const (
	HeldSay    = "say"
	HeldAnswer = "answer"
	HeldOkay   = "okay"
)

// Held is one thing the captain sent that the host has not run yet.
type Held struct {
	// ID names it for OpUndo; ids start at 1 with each host.
	ID   int    `json:"id"`
	Kind string `json:"kind"`
	// Text is the message or the answer; empty for a tick.
	Text string `json:"text,omitempty"`
	// News is the news an answer answers; Work the row a tick okays.
	News int `json:"news,omitempty"`
	Work int `json:"work,omitempty"`
	// At is when the host took it, Due when it goes.
	At  time.Time `json:"at"`
	Due time.Time `json:"due"`
}

// ErrNotHeld refuses an undo of something that has gone to the Mate, or
// was never held.
var ErrNotHeld = errors.New("already sent: nothing to undo")

// heldItem is a held item and how to stop its timer.
type heldItem struct {
	Held
	stop func() bool
}

// afterFunc runs fn after d, and returns what stops it (time.AfterFunc).
type afterFunc func(d time.Duration, fn func()) (stop func() bool)

func realAfter(d time.Duration, fn func()) func() bool { return time.AfterFunc(d, fn).Stop }

// Say holds a captain message; it runs (Submit) once UndoDelay is over.
func (h *Host) Say(text string) (Held, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Held{}, errors.New("a message has no text")
	}
	return h.hold(Held{Kind: HeldSay, Text: text})
}

// Answer holds the captain's answer to news id; it goes (AnswerNews) once
// UndoDelay is over. A news that is not there or is closed is refused now,
// as AnswerNews would refuse it.
func (h *Host) Answer(id int, text string) (Held, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Held{}, errors.New("an answer has no text")
	}
	if err := h.checkAnswer(id); err != nil {
		return Held{}, err
	}
	return h.hold(Held{Kind: HeldAnswer, Text: text, News: id})
}

// Okay holds the captain's tick on done row id of the work board; it goes
// (OkayWork) once UndoDelay is over. A row that is not there or not done
// is refused now, as OkayWork would refuse it. A row already held is held
// once.
func (h *Host) Okay(id int) (Held, error) {
	if h.newsPath == "" {
		return Held{}, ErrNoWork
	}
	if err := checkOkay(WorkPath(filepath.Dir(h.newsPath)), id); err != nil {
		return Held{}, err
	}
	return h.hold(Held{Kind: HeldOkay, Work: id})
}

// Undo takes back held item id before it goes, and returns it: the client
// puts its text back where the captain wrote it.
func (h *Host) Undo(id int) (Held, error) {
	h.mu.Lock()
	i := slices.IndexFunc(h.held, func(it heldItem) bool { return it.ID == id })
	if i < 0 {
		h.mu.Unlock()
		return Held{}, fmt.Errorf("%w (#%d)", ErrNotHeld, id)
	}
	it := h.held[i]
	h.held = slices.Delete(h.held, i, i+1)
	it.stop()
	ev := h.eventLocked(nil)
	h.mu.Unlock()
	h.emit(ev)
	return it.Held, nil
}

// hold puts it on hold. A tick on a row already held is that held item:
// the check and the hold are under one lock, so two ticks hold one.
func (h *Host) hold(it Held) (Held, error) {
	h.mu.Lock()
	if h.stopping {
		h.mu.Unlock()
		return Held{}, ErrStopping
	}
	for _, o := range h.held {
		if it.Kind == HeldOkay && o.Kind == HeldOkay && o.Work == it.Work {
			h.mu.Unlock()
			return o.Held, nil
		}
	}
	h.heldSeq++
	it.ID, it.At = h.heldSeq, h.now()
	it.Due = it.At.Add(UndoDelay)
	id := it.ID
	h.held = append(h.held, heldItem{Held: it, stop: h.after(UndoDelay, func() { h.release(id) })})
	ev := h.eventLocked(nil)
	h.mu.Unlock()
	h.emit(ev)
	return it, nil
}

// release runs held item id once its delay is over, as the host ran it
// before it had an undo, and with it every item held before it: they all
// wait the same UndoDelay, so theirs is over too. Each timer runs on its
// own goroutine; releaseMu and taking the items in id order keep the
// Mate getting them in the order the captain sent them, whichever timer
// fires first. One taken back or dropped by Stop is gone.
func (h *Host) release(id int) {
	h.releaseMu.Lock()
	defer h.releaseMu.Unlock()
	h.mu.Lock()
	var due []Held
	h.held = slices.DeleteFunc(h.held, func(it heldItem) bool {
		if it.ID > id {
			return false
		}
		it.stop()
		due = append(due, it.Held)
		return true
	})
	h.mu.Unlock()
	for _, it := range due {
		h.run1(it)
	}
}

// run1 runs one held item. The event of what it ran is the first without
// it, so a client that sees an answer leave sees its news answered in the
// same event; every path below emits one (a turn, the news or the board
// read again, a warning).
func (h *Host) run1(it Held) {
	switch it.Kind {
	case HeldSay:
		h.Submit(it.Text)
	case HeldAnswer:
		if err := h.AnswerNews(it.News, it.Text); err != nil {
			h.Warn(fmt.Errorf("câu trả lời cho #%d chưa tới Mate: %w", it.News, err))
		}
	case HeldOkay:
		if err := h.OkayWork(it.Work); err != nil {
			h.Warn(fmt.Errorf("OK việc #%d chưa ghi được: %w", it.Work, err))
		}
	}
}

// heldWordsLocked reports whether a captain message or answer is held: an
// app line or a reload waits for it, as it waits for the queue.
func (h *Host) heldWordsLocked() bool {
	return slices.ContainsFunc(h.held, func(it heldItem) bool { return it.Kind != HeldOkay })
}

// heldLocked is the held items, oldest first.
func (h *Host) heldLocked() []Held {
	if len(h.held) == 0 {
		return nil
	}
	out := make([]Held, len(h.held))
	for i, it := range h.held {
		out[i] = it.Held
	}
	return out
}

// dropHeldLocked stops every held item: the host is stopping.
func (h *Host) dropHeldLocked() {
	for _, it := range h.held {
		it.stop()
	}
	h.held = nil
}

// checkAnswer refuses an answer to a news that is not there or is closed.
func (h *Host) checkAnswer(id int) error {
	if h.newsPath == "" {
		return ErrNoNews
	}
	list, err := LoadNews(h.newsPath)
	if err != nil {
		return err
	}
	_, err = answerable(list, id)
	return err
}

// answerable is news id of list, when the captain can answer it: it is
// there and not closed. AnswerNews asks it again when the answer goes.
func answerable(list []News, id int) (News, error) {
	n, ok := findNews(list, id)
	if !ok {
		return News{}, fmt.Errorf("%w: #%d", ErrNoNews, id)
	}
	if n.Closed() {
		// An answer would open it again and spend a Mate turn closing it.
		return News{}, fmt.Errorf("news #%d is already closed", id)
	}
	return n, nil
}

// checkOkay refuses a tick on a row that is not there or not done, with
// the check OkayWork runs when the tick goes (okayable).
func checkOkay(path string, id int) error {
	list, err := LoadWork(path)
	if err != nil {
		return err
	}
	w, ok := findWork(list, id)
	if !ok {
		return fmt.Errorf("%w: #%d", ErrNoWork, id)
	}
	return okayable(w)
}
