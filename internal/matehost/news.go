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

// News is what a headless Mate has to tell the captain outside the
// conversation they are having: something a crew reported that the Mate
// decided needs the captain - a decision, a key, a review, a failure. The
// Mate opens one with `mate news open` and closes it with `mate news
// close`; the captain reads and answers it in the REPL, apart from the
// chat. The news log (NewsName) is append-only, one event per line; a
// news is the fold of its events.

// NewsName is the news log in the Mate directory.
const NewsName = "news.jsonl"

// News statuses. New and Waiting are what the captain owes.
const (
	NewsNew     = "new"
	NewsWaiting = "waiting"
	NewsWorking = "working"
	NewsDone    = "done"
	NewsDropped = "dropped"
)

// News ops, one per log line.
const (
	opOpen    = "open"
	opUpdate  = "update"
	opAsk     = "ask"
	opSeen    = "seen"
	opAnswer  = "answer"
	opFailed  = "failed"
	opClose   = "close"
	opDrop    = "drop"
	opArchive = "archive"
	// opSession records the fork a news's session runs in (session.go).
	opSession = "session"
	// opPropose is a session proposing the captain's answer.
	opPropose = "propose"
)

// NewsOption is one answer the Mate offers.
type NewsOption struct {
	Key         string `json:"key"`
	Text        string `json:"text"`
	Recommended bool   `json:"recommended,omitempty"`
}

// NewsNote is a line added to a news after it opened: an update from the
// Mate, or why it closed.
type NewsNote struct {
	At   time.Time `json:"at"`
	Text string    `json:"text"`
}

// News is one news as its log has it now.
type News struct {
	ID       int          `json:"id"`
	Crew     string       `json:"crew,omitempty"`
	Kind     string       `json:"kind,omitempty"`
	Title    string       `json:"title"`
	Context  []string     `json:"context,omitempty"`
	Question string       `json:"question,omitempty"`
	Options  []NewsOption `json:"options,omitempty"`

	Status   string     `json:"status"`
	OpenedAt time.Time  `json:"opened_at"`
	At       time.Time  `json:"at"`
	Notes    []NewsNote `json:"notes,omitempty"`
	// SeenAt is when the captain last opened the news while it was new.
	SeenAt time.Time `json:"seen_at,omitzero"`
	// Answer is the captain's last answer, AnsweredAt when it was given.
	Answer     string    `json:"answer,omitempty"`
	AnsweredAt time.Time `json:"answered_at,omitzero"`
	Archived   bool      `json:"archived,omitempty"`
	// Session is the fork the news's session runs in, once it has run.
	Session string `json:"session,omitempty"`
	// Proposal is the answer a session proposed and the captain has not
	// sent yet (ProposeNews).
	Proposal string `json:"proposal,omitempty"`
}

// Owed reports whether the news waits on the captain.
func (n News) Owed() bool { return n.Status == NewsNew || n.Status == NewsWaiting }

// Closed reports whether the news is over.
func (n News) Closed() bool { return n.Status == NewsDone || n.Status == NewsDropped }

type newsEvent struct {
	At       time.Time    `json:"at"`
	Op       string       `json:"op"`
	ID       int          `json:"id"`
	Crew     string       `json:"crew,omitempty"`
	Kind     string       `json:"kind,omitempty"`
	Title    string       `json:"title,omitempty"`
	Context  []string     `json:"context,omitempty"`
	Question string       `json:"question,omitempty"`
	Options  []NewsOption `json:"options,omitempty"`
	Text     string       `json:"text,omitempty"`
}

// NewsFocusName is where the console asks the REPL to open a news: the
// captain pressed Enter on it in the box.
const NewsFocusName = "news.focus"

type newsFocus struct {
	ID int       `json:"id"`
	At time.Time `json:"at"`
}

// RequestNewsFocus asks the REPL of the Mate in mateDir to open news id.
func RequestNewsFocus(mateDir string, id int, now time.Time) error {
	data, err := json.Marshal(newsFocus{ID: id, At: now})
	if err != nil {
		return err
	}
	path := filepath.Join(mateDir, NewsFocusName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// WorkFocusName is where the console asks the REPL to show the work
// board: the captain pressed `w` on the Mate (CON-198).
const WorkFocusName = "work.focus"

// RequestWorkFocus asks the REPL of the Mate in mateDir to show its work
// board.
func RequestWorkFocus(mateDir string, now time.Time) error {
	data, err := json.Marshal(newsFocus{At: now})
	if err != nil {
		return err
	}
	path := filepath.Join(mateDir, WorkFocusName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// readFocusAt is when the last request in path was made.
func readFocusAt(path string) (time.Time, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, false
	}
	var f newsFocus
	if json.Unmarshal(data, &f) != nil || f.At.IsZero() {
		return time.Time{}, false
	}
	return f.At, true
}

// readNewsFocus is the news the last request named, with when it was made.
func readNewsFocus(path string) (newsFocus, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return newsFocus{}, false
	}
	var f newsFocus
	if json.Unmarshal(data, &f) != nil || f.ID <= 0 {
		return newsFocus{}, false
	}
	return f, true
}

// NewsPath is the news log of the Mate in mateDir.
func NewsPath(mateDir string) string { return filepath.Join(mateDir, NewsName) }

// ErrNoNews is a news id the log does not have.
var ErrNoNews = errors.New("no such news")

// OpenNews adds a news and returns its id: one more than the largest the
// log has, decided under an exclusive lock on the log.
func OpenNews(path string, n News, now time.Time) (int, error) {
	if strings.TrimSpace(n.Title) == "" {
		return 0, errors.New("a news needs a title")
	}
	var id int
	err := withNewsLog(path, func(list []News, f *os.File) error {
		for _, x := range list {
			id = max(id, x.ID)
		}
		id++
		return writeNewsEvent(f, newsEvent{At: now, Op: opOpen, ID: id, Crew: n.Crew, Kind: n.Kind, Title: n.Title, Context: n.Context, Question: n.Question, Options: n.Options})
	})
	return id, err
}

// UpdateNews adds the Mate's line to an open news and moves it to the top.
// It waits on the captain again only when the Mate asks them (ask): an
// update is not a new debt. An ask is the news's question from then on,
// without the options of the one before.
func UpdateNews(path string, id int, text string, ask bool, now time.Time) error {
	op := opUpdate
	if ask {
		op = opAsk
	}
	return newsOp(path, newsEvent{At: now, Op: op, ID: id, Text: text}, false)
}

// ProposeNews records the answer a topic's session proposes for news id
// (CON-104): the captain sends it to the Mate with ^S, or edits it first.
// It answers nothing by itself, and a later proposal replaces it.
func ProposeNews(path string, id int, text string, now time.Time) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("a proposal needs the answer it proposes")
	}
	return newsOp(path, newsEvent{At: now, Op: opPropose, ID: id, Text: text}, false)
}

// CloseNews ends a news: done once it is settled, dropped when what it was
// about went away before the captain answered. reason is shown with it.
func CloseNews(path string, id int, reason string, dropped bool, now time.Time) error {
	op := opClose
	if dropped {
		op = opDrop
	}
	return newsOp(path, newsEvent{At: now, Op: op, ID: id, Text: reason}, false)
}

func newsOp(path string, ev newsEvent, allowClosed bool) error {
	return withNewsLog(path, func(list []News, f *os.File) error {
		n, ok := findNews(list, ev.ID)
		if !ok {
			return fmt.Errorf("%w: #%d", ErrNoNews, ev.ID)
		}
		if n.Closed() && !allowClosed && ev.Op != opArchive {
			return fmt.Errorf("news #%d is already closed", ev.ID)
		}
		return writeNewsEvent(f, ev)
	})
}

func findNews(list []News, id int) (News, bool) {
	for _, n := range list {
		if n.ID == id {
			return n, true
		}
	}
	return News{}, false
}

// withNewsLog runs fn with the log read and held open for appending under
// an exclusive lock, so two writers never pick the same id.
func withNewsLog(path string, fn func([]News, *os.File) error) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck
	list, err := LoadNews(path)
	if err != nil {
		return err
	}
	return fn(list, f)
}

func writeNewsEvent(f *os.File, ev newsEvent) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	return err
}

// LoadNews folds the log into its news, oldest first. A missing log has
// none; a line that does not parse, or names a news never opened, is
// skipped.
func LoadNews(path string) ([]News, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var list []News
	index := map[int]int{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var ev newsEvent
		if json.Unmarshal(sc.Bytes(), &ev) != nil || ev.ID <= 0 {
			continue
		}
		if ev.Op == opOpen {
			if _, dup := index[ev.ID]; dup {
				continue
			}
			index[ev.ID] = len(list)
			list = append(list, News{ID: ev.ID, Crew: ev.Crew, Kind: ev.Kind, Title: ev.Title, Context: ev.Context,
				Question: ev.Question, Options: ev.Options, Status: NewsNew, OpenedAt: ev.At, At: ev.At})
			continue
		}
		i, ok := index[ev.ID]
		if !ok {
			continue
		}
		n := &list[i]
		switch ev.Op {
		case opUpdate:
			n.Notes = append(n.Notes, NewsNote{At: ev.At, Text: ev.Text})
		case opAsk:
			// A new question takes the place of the open one, and the
			// options answered that one, not this.
			n.Question, n.Options = ev.Text, nil
			if !n.Closed() {
				n.Status, n.Archived = NewsNew, false
			}
		case opSeen:
			if n.Status == NewsNew {
				n.Status, n.SeenAt = NewsWaiting, ev.At
			}
			continue
		case opAnswer:
			n.Answer, n.Status, n.AnsweredAt, n.Proposal = ev.Text, NewsWorking, ev.At, ""
		case opFailed:
			if n.Status == NewsWorking {
				n.Status = NewsWaiting
			}
		case opClose, opDrop:
			n.Status = NewsDone
			if ev.Op == opDrop {
				n.Status = NewsDropped
			}
			if ev.Text != "" {
				n.Notes = append(n.Notes, NewsNote{At: ev.At, Text: ev.Text})
			}
		case opArchive:
			n.Archived = true
			continue
		case opSession:
			n.Session = ev.Text
			continue
		case opPropose:
			n.Proposal = ev.Text
			continue
		default:
			continue
		}
		n.At = ev.At
	}
	return list, sc.Err()
}

// newsAnswerPrompt is the turn a captain's answer to a news becomes: the
// Mate's manual reads `[news #n]` as the captain answering news n. It
// carries the question it answers, so the Mate need not find it.
func newsAnswerPrompt(n News, text string) string {
	if q := strings.TrimSpace(n.Question); q != "" {
		return fmt.Sprintf("[news #%d · %s] Hỏi: %s · Trả lời: %s", n.ID, n.Title, q, text)
	}
	return fmt.Sprintf("[news #%d · %s] %s", n.ID, n.Title, text)
}
