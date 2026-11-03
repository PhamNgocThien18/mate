package dashboard

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// The City's actions beyond the console's sheet (CON-142): talking to a
// headless Mate the way its REPL does, reading a crew's screen, handing an
// inbox question to the Mate, and adding or removing a project. Each one is
// still run by the console (Deps.Act), so a page never does what the
// console could not.
const (
	actSay           = "say"
	actInterrupt     = "interrupt"
	actAnswer        = "answer"
	actFork          = "fork"
	actForkClose     = "fork_close"
	actPeek          = "peek"
	actNotice        = "explain_notice"
	actAssign        = "assign"
	actAddProject    = "add_project"
	actRemoveProject = "remove_project"
	// actOkay is the captain's tick on a done row of the Mate's work board
	// (CON-158): the one write the board takes from a page.
	actOkay = "okay"
)

// talk are the actions that go to the Mate's chat, and can run whenever a
// message can be sent.
var talk = map[string]bool{actSay: true, actInterrupt: true, actAnswer: true, actFork: true, actForkClose: true}

// maxActionBody bounds a press: a chat message is the longest thing in one.
const maxActionBody = 64 << 10

// HarnessChoice is one agent a new Mate can be created with, the
// workspace's default first.
type HarnessChoice struct {
	Kind    string `json:"kind"`
	Default bool   `json:"default,omitempty"`
}

// Daemon is what the auto-mode daemon last did for a project: how many
// digests it delivered, when the last one went, and why the one queued has
// not gone yet.
type Daemon struct {
	Sends      int    `json:"sends"`
	LastSentAt string `json:"last_sent_at,omitempty"`
	Notice     string `json:"notice,omitempty"`
	NoticeAt   string `json:"notice_at,omitempty"`
}

func daemonOf(p query.ProjectNode) *Daemon {
	d := p.Daemon
	if d.Sends == 0 && d.Notice == "" {
		return nil
	}
	out := &Daemon{Sends: d.Sends, Notice: d.Notice}
	if !d.LastSentAt.IsZero() {
		out.LastSentAt = db.FormatTime(d.LastSentAt)
	}
	if !d.NoticeAt.IsZero() {
		out.NoticeAt = db.FormatTime(d.NoticeAt)
	}
	return out
}

func harnessChoices(snap query.Snapshot) []HarnessChoice {
	def := query.HarnessKind("")
	if snap.Workspace.IsKnown() {
		def = snap.Workspace.Value.MateHarness
	}
	out := []HarnessChoice{}
	for _, h := range snap.Harnesses {
		if h.Mate && h.Kind == def {
			out = append(out, HarnessChoice{Kind: string(h.Kind), Default: true})
		}
	}
	for _, h := range snap.Harnesses {
		if h.Mate && h.Kind != def {
			out = append(out, HarnessChoice{Kind: string(h.Kind)})
		}
	}
	return out
}

// mateMore are the Mate's City buttons: the chat (`say`, which stands for
// answer, fork and interrupt too) and removing the project.
func (s *Server) mateMore(p query.ProjectNode) []Button {
	say := Button{Action: actSay, Label: "Send", Available: true}
	switch {
	case !mateRunning(p):
		say.Available, say.Reason = false, "the Mate is not running · start it first"
	case !s.ws.MateHeadless(p.ProjectID):
		say.Available, say.Reason = false, "this Mate is not headless · type in its pane"
	}
	remove := button(p.Actions, actRemoveProject, "Remove project…", "")
	if remove.Available {
		crews := "no running crews"
		switch n := len(p.Crews); n {
		case 0:
		case 1:
			crews = "1 running crew"
		default:
			crews = strconv.Itoa(n) + " running crews"
		}
		// The console's own question (removeProjectChoice).
		remove.Confirm = "Remove project " + p.Name + "? This stops its Mate and " + crews + ". Its memory, backlog, crew records and repos stay on disk; `mate project add " + p.Name + "` brings it back."
	}
	// The tick writes the board's log, which needs no Mate: it is offered
	// wherever the console acts at all.
	okay := Button{Action: actOkay, Label: "OK", Available: true}
	return []Button{say, remove, okay}
}

// mateRunning is whether the Mate can be stopped, the snapshot's own word
// for running.
func mateRunning(p query.ProjectNode) bool { return lookup(p.Mate.Actions, "stop").Available }

// crewMore are a crew's City buttons: read its screen, ask Jev about it,
// and hand each of its open questions to the Mate.
func (s *Server) crewMore(p query.ProjectNode, c query.CrewNode) []Button {
	live := c.Binding.IsKnown() && c.Binding.Value.Status == query.BindingActive
	peek := Button{Action: actPeek, Label: "Peek", Available: live}
	notice := Button{Action: actNotice, Label: "Explain notice", Available: live && s.deps.Notice}
	if !live {
		peek.Reason, notice.Reason = "no live agent to read", "no live agent to read"
	} else if !s.deps.Notice {
		notice.Reason = "Jev is not configured"
	}
	out := []Button{peek, notice}
	if !p.Box.IsKnown() {
		return out
	}
	for _, e := range p.Box.Value.Inbox {
		if e.Crew != c.CrewID || !e.Resolvable() {
			continue
		}
		b := Button{Action: actAssign, Label: "Assign to Mate", Available: true, Key: e.AssignKey}
		switch {
		case e.Handed || e.Assigned.State == query.BoxAssignSent:
			b.Available, b.Reason = false, "the Mate already has it"
		case e.Assigned.State == query.BoxAssignQueued:
			b.Available, b.Reason = false, "queued for the Mate's composer"
		case p.Mode == query.ModeAuto:
			b.Reason = "auto mode hands it over by itself"
		}
		out = append(out, b)
	}
	return out
}

// resolveLine is the resolve line of the inbox entry key names, from the
// server's own snapshot.
func (s *Server) resolveLine(p query.ProjectNode, crew, key string) string {
	if !p.Box.IsKnown() {
		return ""
	}
	for _, e := range p.Box.Value.Inbox {
		if e.Crew == crew && e.AssignKey == key {
			return e.Resolve
		}
	}
	return ""
}

// addProject is POST /api/actions {action: add_project, project: <new
// name>, repo}: the workspace's own action, which names a project that is
// not there yet.
func (s *Server) addProject(w http.ResponseWriter, r *http.Request, req ActionRequest) {
	if err := store.ValidateProjectName(req.Project); err != nil {
		s.writeError(w, r, http.StatusConflict, "Action refused: "+err.Error()+" · nothing started", "")
		return
	}
	if _, ok := s.ws.Project(req.Project); ok {
		s.writeError(w, r, http.StatusConflict, "Action refused: project "+req.Project+" is already in the workspace · nothing started", "")
		return
	}
	target := "workspace"
	if busy, ok := s.acting.take(target, req.Action); !ok {
		s.writeError(w, r, http.StatusConflict, "Action refused: "+busy+" is still running on the workspace · nothing started", "")
		return
	}
	defer s.acting.release(target)
	s.run(w, r, context.WithoutCancel(r.Context()), req)
}

// ChatResponse is GET /api/projects/{project}/chat: the headless Mate's
// conversation as its REPL shows it (`mate/history.jsonl`, captain
// messages outside a news only), newest last, and what the Mate is doing
// now.
type ChatResponse struct {
	envelope
	Project  string          `json:"project"`
	Headless bool            `json:"headless"`
	Items    []ChatItem      `json:"items"`
	State    *matehost.State `json:"state"`
	// StateError is why the Mate's host did not say what it is doing.
	StateError string `json:"state_error,omitempty"`
}

// ChatItem is one message and its reply.
type ChatItem struct {
	At          string   `json:"at"`
	Source      string   `json:"source"`
	Label       string   `json:"label,omitempty"`
	Text        string   `json:"text"`
	Reply       string   `json:"reply,omitempty"`
	Silent      bool     `json:"silent,omitempty"`
	Interrupted bool     `json:"interrupted,omitempty"`
	Error       string   `json:"error,omitempty"`
	Done        bool     `json:"done"`
	Tools       []string `json:"tools"`
	MS          int64    `json:"ms,omitempty"`
	Tokens      int      `json:"tokens,omitempty"`
	News        int      `json:"news,omitempty"`
}

// chatLimit is how many items a chat shows; the Office keeps the rest.
const chatLimit = 80

// chatHistory is the last n items of a Mate's history the chat shows: the
// captain's messages and the Mate's replies, as its REPL shows them
// (CON-151).
func chatHistory(path string, n int) []matehost.Item {
	var out []matehost.Item
	for _, it := range matehost.History(path, 0) {
		if it.InChat() {
			out = append(out, it)
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

func chatItems(items []matehost.Item) []ChatItem {
	out := make([]ChatItem, 0, len(items))
	for _, it := range items {
		c := ChatItem{At: db.FormatTime(it.At), Source: it.Source, Label: it.Label, Text: it.Text, Reply: it.Reply,
			Silent: it.Silent, Interrupted: it.Interrupted, Error: it.Error, Done: it.Done, MS: it.MS, Tokens: it.Tokens,
			News: it.News, Tools: []string{}}
		for _, t := range it.Tools {
			c.Tools = append(c.Tools, t.Name)
		}
		out = append(out, c)
	}
	return out
}

func (s *Server) handleChat(ctx context.Context, r *http.Request, env envelope) (any, error) {
	project, err := s.project(r)
	if err != nil {
		return nil, err
	}
	dir := s.ws.MateDir(project)
	out := ChatResponse{envelope: env, Project: project, Headless: s.ws.MateHeadless(project), Items: []ChatItem{}}
	out.Items = chatItems(chatHistory(matehost.HistoryPath(dir), chatLimit))
	if !out.Headless {
		return out, nil
	}
	c := matehost.Dial(dir)
	c.Timeout = time.Second
	st, err := c.State(ctx)
	if err != nil {
		out.StateError = err.Error()
		return out, nil
	}
	out.State = &st
	return out, nil
}

// ForkResponse is GET /api/projects/{project}/forks/{news}: what was said
// in a news's private session (CON-104), and whether it is open and
// answering now.
type ForkResponse struct {
	envelope
	Project string     `json:"project"`
	News    int        `json:"news"`
	Items   []ChatItem `json:"items"`
	Open    *bool      `json:"open"`
	Running *ChatItem  `json:"running,omitempty"`
}

func (s *Server) handleFork(ctx context.Context, r *http.Request, env envelope) (any, error) {
	project, err := s.project(r)
	if err != nil {
		return nil, err
	}
	id, err := strconv.Atoi(r.PathValue("news"))
	if err != nil || id <= 0 {
		return nil, notFound("news %q is not a news id", r.PathValue("news"))
	}
	dir := s.ws.MateDir(project)
	out := ForkResponse{envelope: env, Project: project, News: id, Items: []ChatItem{}}
	path := filepath.Join(matehost.TopicsPath(dir), strconv.Itoa(id)+".jsonl")
	if _, err := os.Stat(path); err == nil {
		out.Items = chatItems(matehost.History(path, 0))
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	c := matehost.Dial(dir)
	c.Timeout = time.Second
	list, err := c.Sessions(ctx)
	if err != nil {
		return out, nil
	}
	open := false
	for _, sess := range list {
		if sess.News == id {
			open = true
			if sess.Running != nil {
				run := chatItems([]matehost.Item{*sess.Running})[0]
				out.Running = &run
			}
		}
	}
	out.Open = &open
	return out, nil
}
