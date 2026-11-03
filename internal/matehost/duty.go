package matehost

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Duty is the board of what the captain asked the Mate for, and what the
// Mate proposed (CON-158): where each stands, so the captain can ask "how
// is the project doing?" without reading backlog.md or scrolling the chat.
// It is the Mate's report, not a question: anything that needs the
// captain's answer is a news (a topic), and a row that waits on one only
// names it. The Mate writes the board with `mate duty`; the captain only
// ticks a done row (OkayDuty), which clears it from the board. The Mate
// never waits for that tick.
//
// The log (DutyName) is append-only, one event per line, like news.jsonl;
// a row is the fold of its events.

// DutyName is the duty log in the Mate directory.
const DutyName = "duty.jsonl"

// Duty kinds: who put the row on the board.
const (
	DutyAssigned = "assigned"
	DutyProposed = "proposed"
)

// Duty statuses. Done is what the captain may tick; nothing on the board
// is owed by the captain.
const (
	DutyWorking = "working"
	DutyWaiting = "waiting"
	DutyDone    = "done"
	DutyOkayed  = "okayed"
)

// Duty ops, one per log line.
const (
	dutyOpAdd      = "add"
	dutyOpProgress = "progress"
	dutyOpWait     = "wait"
	dutyOpDone     = "done"
	dutyOpReopen   = "reopen"
	dutyOpOkay     = "okay"
)

// DutyNote is one update on a row: the op that made it and the Mate's
// line, if it gave one.
type DutyNote struct {
	At   time.Time `json:"at"`
	Op   string    `json:"op"`
	Text string    `json:"text,omitempty"`
	// Topic is the news the row waited on, on a wait note.
	Topic int `json:"topic,omitempty"`
}

// Duty is one row as its log has it now.
type Duty struct {
	ID     int    `json:"id"`
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	Status string `json:"status"`
	// Topic is the news the row waits on while Status is waiting.
	Topic   int        `json:"topic,omitempty"`
	AddedAt time.Time  `json:"added_at"`
	At      time.Time  `json:"at"`
	Notes   []DutyNote `json:"notes,omitempty"`
	// Last is the Mate's latest line, the one a list shows under the title.
	Last     string    `json:"last,omitempty"`
	DoneAt   time.Time `json:"done_at,omitzero"`
	OkayedAt time.Time `json:"okayed_at,omitzero"`
}

// Open reports whether the row is still on the board: not okayed.
func (w Duty) Open() bool { return w.Status != DutyOkayed }

type dutyEvent struct {
	At    time.Time `json:"at"`
	Op    string    `json:"op"`
	ID    int       `json:"id"`
	Kind  string    `json:"kind,omitempty"`
	Title string    `json:"title,omitempty"`
	Text  string    `json:"text,omitempty"`
	Topic int       `json:"topic,omitempty"`
}

// DutyPath is the duty log of the Mate in mateDir.
func DutyPath(mateDir string) string { return filepath.Join(mateDir, DutyName) }

// ErrNoDuty is a row id the log does not have.
var ErrNoDuty = errors.New("no such duty")

// AddDuty puts a row on the board and returns its id: one more than the
// largest the log has, decided under an exclusive lock on the log.
func AddDuty(path string, kind, title, text string, now time.Time) (int, error) {
	if strings.TrimSpace(title) == "" {
		return 0, errors.New("a duty row needs a title")
	}
	if kind != DutyAssigned && kind != DutyProposed {
		return 0, fmt.Errorf("a duty row is %s or %s, not %q", DutyAssigned, DutyProposed, kind)
	}
	var id int
	err := withDutyLog(path, func(list []Duty, f *os.File) error {
		for _, x := range list {
			id = max(id, x.ID)
		}
		id++
		return writeDutyEvent(f, dutyEvent{At: now, Op: dutyOpAdd, ID: id, Kind: kind, Title: title, Text: text})
	})
	return id, err
}

// ProgressDuty adds the Mate's line to a row and puts it back to working:
// a topic it waited on is answered, or a step is done.
func ProgressDuty(path string, id int, text string, now time.Time) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("progress needs the line that says what moved")
	}
	return dutyOp(path, dutyEvent{At: now, Op: dutyOpProgress, ID: id, Text: text}, func(w Duty) error {
		if w.Status == DutyOkayed {
			return fmt.Errorf("duty #%d is okayed; reopen it first", id)
		}
		return nil
	})
}

// WaitDuty marks a row as waiting on news topic: the Mate asked the
// captain something the row cannot go on without.
func WaitDuty(path string, id, topic int, text string, now time.Time) error {
	if topic <= 0 {
		return errors.New("wait needs the topic the row waits on")
	}
	return dutyOp(path, dutyEvent{At: now, Op: dutyOpWait, ID: id, Topic: topic, Text: text}, func(w Duty) error {
		if w.Status == DutyOkayed {
			return fmt.Errorf("duty #%d is okayed; reopen it first", id)
		}
		return nil
	})
}

// DoneDuty marks a row done: the work is in, and the captain may tick it
// off. The Mate goes on with other work; it does not wait for the tick.
func DoneDuty(path string, id int, text string, now time.Time) error {
	return dutyOp(path, dutyEvent{At: now, Op: dutyOpDone, ID: id, Text: text}, func(w Duty) error {
		if w.Status == DutyOkayed {
			return fmt.Errorf("duty #%d is okayed; reopen it first", id)
		}
		return nil
	})
}

// ReopenDuty puts a done or okayed row back to working: the captain said
// in the chat that it is not OK after all, or more turned up.
func ReopenDuty(path string, id int, text string, now time.Time) error {
	return dutyOp(path, dutyEvent{At: now, Op: dutyOpReopen, ID: id, Text: text}, func(w Duty) error {
		if w.Status != DutyDone && w.Status != DutyOkayed {
			return fmt.Errorf("duty #%d is %s, not done", id, w.Status)
		}
		return nil
	})
}

// OkayDuty is the captain's tick on a done row: it leaves the board. Only
// the app calls it, never the Mate.
func OkayDuty(path string, id int, now time.Time) error {
	return dutyOp(path, dutyEvent{At: now, Op: dutyOpOkay, ID: id}, func(w Duty) error {
		if w.Status != DutyDone {
			return fmt.Errorf("duty #%d is %s, not done", id, w.Status)
		}
		return nil
	})
}

func dutyOp(path string, ev dutyEvent, check func(Duty) error) error {
	return withDutyLog(path, func(list []Duty, f *os.File) error {
		w, ok := findDuty(list, ev.ID)
		if !ok {
			return fmt.Errorf("%w: #%d", ErrNoDuty, ev.ID)
		}
		if err := check(w); err != nil {
			return err
		}
		return writeDutyEvent(f, ev)
	})
}

func findDuty(list []Duty, id int) (Duty, bool) {
	for _, w := range list {
		if w.ID == id {
			return w, true
		}
	}
	return Duty{}, false
}

// withDutyLog runs fn with the log read and held open for appending under
// an exclusive lock, so two writers never pick the same id.
func withDutyLog(path string, fn func([]Duty, *os.File) error) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck
	list, err := LoadDuty(path)
	if err != nil {
		return err
	}
	return fn(list, f)
}

func writeDutyEvent(f *os.File, ev dutyEvent) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	return err
}

// LoadDuty folds the log into its rows, oldest first. A missing log has
// none; a line that does not parse, or names a row never added, is
// skipped.
func LoadDuty(path string) ([]Duty, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var list []Duty
	index := map[int]int{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var ev dutyEvent
		if json.Unmarshal(sc.Bytes(), &ev) != nil || ev.ID <= 0 {
			continue
		}
		if ev.Op == dutyOpAdd {
			if _, dup := index[ev.ID]; dup {
				continue
			}
			index[ev.ID] = len(list)
			kind := ev.Kind
			if kind == "" {
				kind = DutyAssigned
			}
			w := Duty{ID: ev.ID, Kind: kind, Title: ev.Title, Status: DutyWorking, AddedAt: ev.At, At: ev.At, Last: ev.Text}
			w.Notes = append(w.Notes, DutyNote{At: ev.At, Op: dutyOpAdd, Text: ev.Text})
			list = append(list, w)
			continue
		}
		i, ok := index[ev.ID]
		if !ok {
			continue
		}
		w := &list[i]
		switch ev.Op {
		case dutyOpProgress:
			w.Status, w.Topic = DutyWorking, 0
		case dutyOpWait:
			w.Status, w.Topic = DutyWaiting, ev.Topic
		case dutyOpDone:
			w.Status, w.Topic, w.DoneAt = DutyDone, 0, ev.At
		case dutyOpReopen:
			w.Status, w.Topic, w.DoneAt, w.OkayedAt = DutyWorking, 0, time.Time{}, time.Time{}
		case dutyOpOkay:
			w.Status, w.OkayedAt = DutyOkayed, ev.At
		default:
			continue
		}
		w.Notes = append(w.Notes, DutyNote{At: ev.At, Op: ev.Op, Text: ev.Text, Topic: ev.Topic})
		if ev.Text != "" {
			w.Last = ev.Text
		}
		w.At = ev.At
	}
	return list, sc.Err()
}
