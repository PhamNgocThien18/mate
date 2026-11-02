package matehost

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
)

// A topic's session (CON-104) is where the captain talks a news over before
// answering it: a fork of the Mate's session, so it knows all the Mate
// knows, that can read but not act (harness.ForkTurns). Its runs go on
// beside the Mate's own turns, outside the turn lock, one at a time per
// session. Opening one does not answer the news; the answer still goes to
// the Mate, who acts on it. The fork's session id is kept in the news log,
// so a session closed by hand or for idling opens again where it was; what
// was said in it is kept in TopicsDir, one file per news.

const (
	// MaxSessions is how many sessions can be open at once.
	MaxSessions = 3
	// SessionIdle is how long an open session waits for the captain before
	// it closes by itself.
	SessionIdle = 30 * time.Minute
	// TopicsDir is where sessions keep their files, in the Mate directory.
	TopicsDir = "topics"
)

// Errors a session can be refused with.
var (
	ErrTooManySessions = fmt.Errorf("đã mở %d session; ^X đóng bớt một cái", MaxSessions)
	ErrNoSession       = errors.New("topic này chưa mở session")
	ErrSessionBusy     = errors.New("session đang trả lời; chờ xong hoặc ^C ngắt")
	ErrNoForks         = errors.New("harness của Mate này không mở được session")
)

// TopicsPath is the sessions' directory of the Mate in mateDir.
func TopicsPath(mateDir string) string { return filepath.Join(mateDir, TopicsDir) }

// SessionInfo is one open session as the REPL shows it.
type SessionInfo struct {
	News int
	// Fork is the fork's session id; empty until its first run ends.
	Fork     string
	OpenedAt time.Time
	// Items are what was said in it, oldest first; Running is the run in
	// flight.
	Items   []Item
	Running *Item
}

// Busy reports whether the session is answering.
func (s SessionInfo) Busy() bool { return s.Running != nil }

// Tokens are the session's tokens other than cache reads.
func (s SessionInfo) Tokens() int {
	n := 0
	for _, it := range s.Items {
		n += it.Tokens
	}
	return n
}

// Label is the session's short name: s- and the start of its fork's id.
func (s SessionInfo) Label() string {
	if len(s.Fork) >= 4 {
		return "s-" + s.Fork[:4]
	}
	return "s-mới"
}

type session struct {
	news   int
	fork   string
	items  []Item
	run    *Run
	cur    *Item
	opened time.Time
	active time.Time
}

// ForkAllow are the commands a session may run: reading git, files, pull
// requests and the project's state, and proposing an answer to a news. bin
// is the mate binary the Mate's manual names.
func ForkAllow(bin, project string) []string {
	var out []string
	for _, c := range []string{"git status*", "git diff*", "git log*", "git show*", "git range-diff*", "git rev-parse*",
		"ls*", "pwd", "cat *", "head *", "tail *", "wc *", "grep *", "rg *",
		"gh pr view*", "gh pr diff*", "gh pr checks*", "gh issue view*"} {
		out = append(out, "Bash("+c+")")
	}
	bins := []string{"mate"}
	if bin != "" && bin != "mate" {
		bins = append(bins, bin)
	}
	for _, b := range bins {
		for _, sub := range []string{"diff " + project + " *", "report " + project + " *", "peek " + project + " *", "state " + project + " *",
			"recall " + project + "*", "usage " + project + "*", "news list " + project + "*", "crew list " + project + "*",
			"backlog " + project, "backlog " + project + " --all", "news propose " + project + " *"} {
			out = append(out, fmt.Sprintf("Bash(%s %s)", b, sub))
		}
	}
	return out
}

// sessionPrompt is a session's first message: who it is and what it may
// do, then the captain's words.
func sessionPrompt(n News, project, bin, text string) string {
	if bin == "" {
		bin = "mate"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "⟦topic #%d⟧ You are now a side session forked from the Mate, to talk topic #%d over with the captain before they answer it. ", n.ID, n.ID)
	fmt.Fprintf(&b, "Topic: %s.", n.Title)
	if q := strings.TrimSpace(n.Question); q != "" {
		fmt.Fprintf(&b, " Question: %s", q)
	}
	if len(n.Context) > 0 {
		b.WriteString("\nContext:")
		for _, c := range n.Context {
			b.WriteString("\n- " + strings.TrimSpace(c))
		}
	}
	if len(n.Options) > 0 {
		b.WriteString("\nOptions:")
		for _, o := range n.Options {
			fmt.Fprintf(&b, "\n- [%s] %s", o.Key, o.Text)
			if o.Recommended {
				b.WriteString(" (recommended)")
			}
		}
	}
	b.WriteString("\nYou cannot act here: spawning, sending, merging, closing news and editing files are all blocked, and the Mate does not see this conversation. ")
	b.WriteString("Read what helps (files, git, `mate diff|report|peek|state|recall`), explain, and help the captain decide. ")
	fmt.Fprintf(&b, "When you two agree on an answer, propose it in one line with `%s news propose %s %d \"<the answer>\"`; the captain sends it to the Mate with ^S. ", bin, project, n.ID)
	b.WriteString("Reply in the captain's language.\n\n")
	b.WriteString(text)
	return b.String()
}

// OpenSession opens news id's session, or shows the one open.
func (h *Host) OpenSession(id int) error {
	if h.forks == nil {
		return ErrNoForks
	}
	h.mu.Lock()
	if _, ok := h.sessions[id]; !ok {
		if len(h.sessions) >= MaxSessions {
			h.mu.Unlock()
			return ErrTooManySessions
		}
		n, ok := findNews(h.news, id)
		if !ok {
			h.mu.Unlock()
			return fmt.Errorf("%w: #%d", ErrNoNews, id)
		}
		now := h.now()
		h.sessions[id] = &session{news: id, fork: n.Session, items: History(h.sessionFile(id), 0), opened: now, active: now}
	}
	ev := h.eventLocked(nil)
	h.mu.Unlock()
	h.emit(ev)
	return nil
}

// AskSession runs the captain's message in news id's open session.
func (h *Host) AskSession(id int, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	h.mu.Lock()
	s, ok := h.sessions[id]
	switch {
	case !ok:
		h.mu.Unlock()
		return ErrNoSession
	case s.run != nil:
		h.mu.Unlock()
		return ErrSessionBusy
	case h.stopping:
		h.mu.Unlock()
		return ErrStopping
	}
	n, _ := findNews(h.news, id)
	l := h.runner.Launch
	if s.fork == "" && !h.runner.opened(l.Dir, l.SessionID) {
		h.mu.Unlock()
		return errors.New("Mate chưa có phiên nào để fork; nhắn Mate một câu trước")
	}
	args, err := h.forks.ForkTurnArgs(l.Args, s.fork, h.allow, h.topicsDir)
	if err != nil {
		h.mu.Unlock()
		return err
	}
	prompt := text
	if s.fork == "" {
		prompt = sessionPrompt(n, l.Project, h.mateBin, text)
	}
	run, err := startRun(l, args, 0, prompt, h.runner.Turns, h.runner.Grace, func() {})
	if err != nil {
		h.mu.Unlock()
		return err
	}
	item := &Item{At: h.now(), Source: SourceCaptain, Text: text, News: id}
	s.run, s.cur, s.active = run, item, item.At
	ev := h.eventLocked(nil)
	h.mu.Unlock()
	h.emit(ev)
	go h.waitSession(s, run, item)
	return nil
}

func (h *Host) waitSession(s *session, run *Run, item *Item) {
	var st streamState
	reply := run.Wait(func(ev harness.TurnEvent) {
		h.mu.Lock()
		changed := h.progressLocked(item, ev, &st)
		var out Event
		if changed {
			out = h.eventLocked(nil)
		}
		h.mu.Unlock()
		if changed {
			h.emit(out)
		}
	})
	h.mu.Lock()
	result := strings.TrimSpace(reply.Text)
	item.Reply = finalReply(strings.TrimSpace(item.Reply), result)
	item.MS = reply.DurationMS
	if item.MS <= 0 {
		item.MS = h.now().Sub(item.At).Milliseconds()
	}
	item.Tokens = reply.Tokens
	item.Interrupted = reply.Interrupted
	if reply.Err != nil {
		item.Error = reply.Err.Error()
	} else if reply.IsError && result != "" {
		item.Error = result
	}
	item.Done = true
	forked := ""
	// A run that came back in the Mate's own session did not fork; keeping
	// that id would send the next run into the Mate's session.
	if s.fork == "" && reply.SessionID != "" && reply.SessionID != h.runner.Launch.SessionID {
		s.fork, forked = reply.SessionID, reply.SessionID
	}
	h.mu.Unlock()
	// The files before the session in memory: a session closed the moment
	// it answered and opened again reads its fork from the news and its
	// turns from the session file, so neither may lag the turn's end
	// (CON-132). Until then the run is still in flight to AskSession.
	if forked != "" && h.newsPath != "" {
		_ = newsOp(h.newsPath, newsEvent{At: h.now(), Op: opSession, ID: s.news, Text: forked}, true)
		h.RefreshNews()
	}
	h.appendItem(h.sessionFile(s.news), *item)
	h.mu.Lock()
	s.items = append(s.items, *item)
	s.run, s.cur, s.active = nil, nil, h.now()
	ev := h.eventLocked(nil)
	h.mu.Unlock()
	h.emit(ev)
}

// InterruptSession stops news id's session run in flight, if any.
func (h *Host) InterruptSession(id int) bool {
	h.mu.Lock()
	var run *Run
	if s, ok := h.sessions[id]; ok {
		run = s.run
	}
	h.mu.Unlock()
	if run == nil {
		return false
	}
	run.Interrupt()
	return true
}

// CloseSession closes news id's session, ending its run in flight. What
// was said in it stays, and opening it again resumes the fork.
func (h *Host) CloseSession(id int) {
	h.mu.Lock()
	s, ok := h.sessions[id]
	delete(h.sessions, id)
	ev := h.eventLocked(nil)
	h.mu.Unlock()
	if ok && s.run != nil {
		s.run.Kill()
	}
	h.emit(ev)
}

// reapSessions closes every session that has waited SessionIdle for the
// captain.
func (h *Host) reapSessions() {
	h.mu.Lock()
	var idle []int
	for id, s := range h.sessions {
		if s.run == nil && h.now().Sub(s.active) >= SessionIdle {
			idle = append(idle, id)
		}
	}
	h.mu.Unlock()
	sort.Ints(idle)
	for _, id := range idle {
		h.CloseSession(id)
		h.emitNote(fmt.Errorf("session của #%d tự đóng sau %d phút rảnh · f mở lại", id, int(SessionIdle.Minutes())))
	}
}

// sessionsLocked is every open session, by news.
// Sessions are the open sessions now, by news.
func (h *Host) Sessions() []SessionInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sessionsLocked()
}

func (h *Host) sessionsLocked() []SessionInfo {
	out := make([]SessionInfo, 0, len(h.sessions))
	for _, s := range h.sessions {
		info := SessionInfo{News: s.news, Fork: s.fork, OpenedAt: s.opened, Items: append([]Item(nil), s.items...)}
		if s.cur != nil {
			cp := *s.cur
			cp.Tools = append([]ToolRecord(nil), s.cur.Tools...)
			info.Running = &cp
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].News < out[j].News })
	return out
}

func (h *Host) sessionFile(id int) string {
	if h.topicsDir == "" {
		return ""
	}
	return filepath.Join(h.topicsDir, strconv.Itoa(id)+".jsonl")
}

// appendItem adds it to the history file at path; an empty path keeps none.
func (h *Host) appendItem(path string, it Item) {
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	appendItemFile(path, it)
}
