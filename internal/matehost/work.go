package matehost

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/platform"
)

// Work is the board of what the captain asked the Mate for, and what the
// Mate proposed (CON-158): where each stands, so the captain can ask "how
// is the project doing?" without reading backlog.md or scrolling the chat.
// It is the Mate's report, not a question: anything that needs the
// captain's answer is a news (a topic), and a row that waits on one only
// names it. The Mate writes the board with `mate work`; the captain only
// ticks a done row (OkayWork), which clears it from the board. The Mate
// never waits for that tick.
//
// The log (WorkName) is append-only, one event per line, like news.jsonl;
// a row is the fold of its events.

// WorkName is the work log in the Mate directory.
const WorkName = "work.jsonl"

// Work kinds: who put the row on the board.
const (
	WorkAssigned = "assigned"
	WorkProposed = "proposed"
)

// Work statuses. Done is what the captain may tick; nothing on the board
// is owed by the captain.
const (
	WorkWorking = "working"
	WorkWaiting = "waiting"
	WorkDone    = "done"
	WorkOkayed  = "okayed"
)

// Work ops, one per log line.
const (
	workOpAdd      = "add"
	workOpProgress = "progress"
	workOpWait     = "wait"
	workOpDone     = "done"
	workOpReopen   = "reopen"
	workOpOkay     = "okay"
)

// WorkNote is one update on a row: the op that made it and the Mate's
// line, if it gave one.
type WorkNote struct {
	At   time.Time `json:"at"`
	Op   string    `json:"op"`
	Text string    `json:"text,omitempty"`
	// Topic is the news the row waited on, on a wait note.
	Topic int `json:"topic,omitempty"`
}

// Work is one row as its log has it now.
type Work struct {
	ID     int    `json:"id"`
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	Status string `json:"status"`
	// Topic is the news the row waits on while Status is waiting.
	Topic   int        `json:"topic,omitempty"`
	AddedAt time.Time  `json:"added_at"`
	At      time.Time  `json:"at"`
	Notes   []WorkNote `json:"notes,omitempty"`
	// Last is the Mate's latest line, the one a list shows under the title.
	Last     string    `json:"last,omitempty"`
	DoneAt   time.Time `json:"done_at,omitzero"`
	OkayedAt time.Time `json:"okayed_at,omitzero"`
}

// Open reports whether the row is still on the board: not okayed.
func (w Work) Open() bool { return w.Status != WorkOkayed }

type workEvent struct {
	At    time.Time `json:"at"`
	Op    string    `json:"op"`
	ID    int       `json:"id"`
	Kind  string    `json:"kind,omitempty"`
	Title string    `json:"title,omitempty"`
	Text  string    `json:"text,omitempty"`
	Topic int       `json:"topic,omitempty"`
}

// WorkPath is the work log of the Mate in mateDir.
func WorkPath(mateDir string) string { return filepath.Join(mateDir, WorkName) }

// ErrNoWork is a row id the log does not have.
var ErrNoWork = errors.New("no such work")

// AddWork puts a row on the board and returns its id: one more than the
// largest the log has, decided under an exclusive lock on the log.
func AddWork(path string, kind, title, text string, now time.Time) (int, error) {
	if strings.TrimSpace(title) == "" {
		return 0, errors.New("a work row needs a title")
	}
	if kind != WorkAssigned && kind != WorkProposed {
		return 0, fmt.Errorf("a work row is %s or %s, not %q", WorkAssigned, WorkProposed, kind)
	}
	var id int
	err := withWorkLog(path, func(list []Work, f *os.File) error {
		for _, x := range list {
			id = max(id, x.ID)
		}
		id++
		return writeWorkEvent(f, workEvent{At: now, Op: workOpAdd, ID: id, Kind: kind, Title: title, Text: text})
	})
	return id, err
}

// ProgressWork adds the Mate's line to a row and puts it back to working:
// a topic it waited on is answered, or a step is done.
func ProgressWork(path string, id int, text string, now time.Time) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("progress needs the line that says what moved")
	}
	return workOp(path, workEvent{At: now, Op: workOpProgress, ID: id, Text: text}, func(w Work) error {
		if w.Status == WorkOkayed {
			return fmt.Errorf("work #%d is okayed; reopen it first", id)
		}
		return nil
	})
}

// WaitWork marks a row as waiting on news topic: the Mate asked the
// captain something the row cannot go on without.
func WaitWork(path string, id, topic int, text string, now time.Time) error {
	if topic <= 0 {
		return errors.New("wait needs the topic the row waits on")
	}
	return workOp(path, workEvent{At: now, Op: workOpWait, ID: id, Topic: topic, Text: text}, func(w Work) error {
		if w.Status == WorkOkayed {
			return fmt.Errorf("work #%d is okayed; reopen it first", id)
		}
		return nil
	})
}

// DoneWork marks a row done: the work is in, and the captain may tick it
// off. The Mate goes on with other work; it does not wait for the tick.
func DoneWork(path string, id int, text string, now time.Time) error {
	return workOp(path, workEvent{At: now, Op: workOpDone, ID: id, Text: text}, func(w Work) error {
		if w.Status == WorkOkayed {
			return fmt.Errorf("work #%d is okayed; reopen it first", id)
		}
		return nil
	})
}

// ReopenWork puts a done or okayed row back to working: the captain said
// in the chat that it is not OK after all, or more turned up.
func ReopenWork(path string, id int, text string, now time.Time) error {
	return workOp(path, workEvent{At: now, Op: workOpReopen, ID: id, Text: text}, func(w Work) error {
		if w.Status != WorkDone && w.Status != WorkOkayed {
			return fmt.Errorf("work #%d is %s, not done", id, w.Status)
		}
		return nil
	})
}

// OkayWork is the captain's tick on a done row: it leaves the board. Only
// the app calls it, never the Mate.
func OkayWork(path string, id int, now time.Time) error {
	return workOp(path, workEvent{At: now, Op: workOpOkay, ID: id}, okayable)
}

// okayable refuses a tick on a row that is not done.
func okayable(w Work) error {
	if w.Status != WorkDone {
		return fmt.Errorf("work #%d is %s, not done", w.ID, w.Status)
	}
	return nil
}

func workOp(path string, ev workEvent, check func(Work) error) error {
	return withWorkLog(path, func(list []Work, f *os.File) error {
		w, ok := findWork(list, ev.ID)
		if !ok {
			return fmt.Errorf("%w: #%d", ErrNoWork, ev.ID)
		}
		if err := check(w); err != nil {
			return err
		}
		return writeWorkEvent(f, ev)
	})
}

func findWork(list []Work, id int) (Work, bool) {
	for _, w := range list {
		if w.ID == id {
			return w, true
		}
	}
	return Work{}, false
}

// withWorkLog runs fn with the log read and held open for appending under
// an exclusive lock, so two writers never pick the same id.
func withWorkLog(path string, fn func([]Work, *os.File) error) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := platform.Lock(f); err != nil {
		return err
	}
	defer platform.Unlock(f) //nolint:errcheck
	list, err := LoadWork(path)
	if err != nil {
		return err
	}
	return fn(list, f)
}

func writeWorkEvent(f *os.File, ev workEvent) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	return err
}

// LoadWork folds the log into its rows, oldest first. A missing log has
// none; a line that does not parse, or names a row never added, is
// skipped.
func LoadWork(path string) ([]Work, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var list []Work
	index := map[int]int{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var ev workEvent
		if json.Unmarshal(sc.Bytes(), &ev) != nil || ev.ID <= 0 {
			continue
		}
		if ev.Op == workOpAdd {
			if _, dup := index[ev.ID]; dup {
				continue
			}
			index[ev.ID] = len(list)
			kind := ev.Kind
			if kind == "" {
				kind = WorkAssigned
			}
			w := Work{ID: ev.ID, Kind: kind, Title: ev.Title, Status: WorkWorking, AddedAt: ev.At, At: ev.At, Last: ev.Text}
			w.Notes = append(w.Notes, WorkNote{At: ev.At, Op: workOpAdd, Text: ev.Text})
			list = append(list, w)
			continue
		}
		i, ok := index[ev.ID]
		if !ok {
			continue
		}
		w := &list[i]
		switch ev.Op {
		case workOpProgress:
			w.Status, w.Topic = WorkWorking, 0
		case workOpWait:
			w.Status, w.Topic = WorkWaiting, ev.Topic
		case workOpDone:
			w.Status, w.Topic, w.DoneAt = WorkDone, 0, ev.At
		case workOpReopen:
			w.Status, w.Topic, w.DoneAt, w.OkayedAt = WorkWorking, 0, time.Time{}, time.Time{}
		case workOpOkay:
			w.Status, w.OkayedAt = WorkOkayed, ev.At
		default:
			continue
		}
		w.Notes = append(w.Notes, WorkNote{At: ev.At, Op: ev.Op, Text: ev.Text, Topic: ev.Topic})
		if ev.Text != "" {
			w.Last = ev.Text
		}
		w.At = ev.At
	}
	return list, sc.Err()
}
