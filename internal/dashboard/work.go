package dashboard

import (
	"context"
	"errors"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/matehost"
)

// WorkResponse is GET /api/work[?project=<p>]: every project's board of
// what the captain asked its Mate for and what the Mate proposed
// (`mate/work.jsonl`, CON-158), okayed rows too, for the Office's and the
// City's Work tab. It reads the log through matehost.LoadWork on every
// request and is not cached: the Mate writing it is not an event.
type WorkResponse struct {
	envelope
	// Project is the one asked for, empty for the whole workspace.
	Project  string        `json:"project,omitempty"`
	Projects []ProjectWork `json:"projects"`
}

// ProjectWork is one project's board, latest move first.
type ProjectWork struct {
	Project string `json:"project"`
	// Logged is whether the project's Mate has a work log at all: a Mate
	// that never put anything on the board has none, which is not the
	// same answer as an empty board.
	Logged bool        `json:"logged"`
	Work   []WorkEntry `json:"work"`
	// Error is why the log could not be read; Work is empty then.
	Error string `json:"error,omitempty"`
}

// WorkEntry is one row as its log folds now. Status is matehost's word:
// `working`, `waiting` (on news Topic), `done` (the captain may tick it),
// `okayed` (ticked, off the board).
type WorkEntry struct {
	ID       int        `json:"id"`
	Kind     string     `json:"kind"`
	Title    string     `json:"title"`
	Status   string     `json:"status"`
	Topic    int        `json:"topic,omitempty"`
	Last     string     `json:"last,omitempty"`
	AddedAt  string     `json:"added_at"`
	At       string     `json:"at"`
	DoneAt   string     `json:"done_at,omitempty"`
	OkayedAt string     `json:"okayed_at,omitempty"`
	Notes    []WorkNote `json:"notes"`
}

// WorkNote is one update on a row, oldest first.
type WorkNote struct {
	At    string `json:"at"`
	Op    string `json:"op"`
	Text  string `json:"text,omitempty"`
	Topic int    `json:"topic,omitempty"`
}

func (s *Server) handleWork(_ context.Context, r *http.Request, env envelope) (any, error) {
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	var names []string
	if project != "" {
		if _, ok := s.ws.Project(project); !ok {
			return nil, errNotFound{reason: unknownProject(s.ws, project)}
		}
		names = []string{project}
	} else {
		for _, ref := range s.ws.Projects() {
			names = append(names, ref.Name)
		}
	}
	out := WorkResponse{envelope: env, Project: project, Projects: []ProjectWork{}}
	for _, name := range names {
		out.Projects = append(out.Projects, s.projectWork(name))
	}
	return out, nil
}

// projectWork folds one project's work log. It reads the file and writes
// nothing: the tick is POST /api/actions `okay`.
func (s *Server) projectWork(project string) ProjectWork {
	out := ProjectWork{Project: project, Work: []WorkEntry{}}
	path := matehost.WorkPath(s.ws.MateDir(project))
	if _, err := os.Stat(path); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			out.Error = err.Error()
		}
		return out
	}
	out.Logged = true
	list, err := matehost.LoadWork(path)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].At.After(list[j].At) })
	for _, w := range list {
		e := WorkEntry{
			ID: w.ID, Kind: w.Kind, Title: w.Title, Status: w.Status, Topic: w.Topic, Last: w.Last,
			AddedAt: db.FormatTime(w.AddedAt), At: db.FormatTime(w.At), DoneAt: db.FormatTime(w.DoneAt), OkayedAt: db.FormatTime(w.OkayedAt),
			Notes: []WorkNote{},
		}
		for _, n := range w.Notes {
			e.Notes = append(e.Notes, WorkNote{At: db.FormatTime(n.At), Op: n.Op, Text: n.Text, Topic: n.Topic})
		}
		out.Work = append(out.Work, e)
	}
	return out
}
