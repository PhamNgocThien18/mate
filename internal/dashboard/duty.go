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

// DutyResponse is GET /api/duty[?project=<p>]: every project's board of
// what the captain asked its Mate for and what the Mate proposed
// (`mate/duty.jsonl`, CON-158), okayed rows too, for the Office's and the
// City's Duty tab. It reads the log through matehost.LoadDuty on every
// request and is not cached: the Mate writing it is not an event.
type DutyResponse struct {
	envelope
	// Project is the one asked for, empty for the whole workspace.
	Project  string        `json:"project,omitempty"`
	Projects []ProjectDuty `json:"projects"`
}

// ProjectDuty is one project's board, latest move first.
type ProjectDuty struct {
	Project string `json:"project"`
	// Logged is whether the project's Mate has a duty log at all: a Mate
	// that never put anything on the board has none, which is not the
	// same answer as an empty board.
	Logged bool        `json:"logged"`
	Duty   []DutyEntry `json:"duty"`
	// Error is why the log could not be read; Duty is empty then.
	Error string `json:"error,omitempty"`
}

// DutyEntry is one row as its log folds now. Status is matehost's word:
// `working`, `waiting` (on news Topic), `done` (the captain may tick it),
// `okayed` (ticked, off the board).
type DutyEntry struct {
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
	Notes    []DutyNote `json:"notes"`
}

// DutyNote is one update on a row, oldest first.
type DutyNote struct {
	At    string `json:"at"`
	Op    string `json:"op"`
	Text  string `json:"text,omitempty"`
	Topic int    `json:"topic,omitempty"`
}

func (s *Server) handleDuty(_ context.Context, r *http.Request, env envelope) (any, error) {
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
	out := DutyResponse{envelope: env, Project: project, Projects: []ProjectDuty{}}
	for _, name := range names {
		out.Projects = append(out.Projects, s.projectDuty(name))
	}
	return out, nil
}

// projectDuty folds one project's duty log. It reads the file and writes
// nothing: the tick is POST /api/actions `okay`.
func (s *Server) projectDuty(project string) ProjectDuty {
	out := ProjectDuty{Project: project, Duty: []DutyEntry{}}
	path := matehost.DutyPath(s.ws.MateDir(project))
	if _, err := os.Stat(path); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			out.Error = err.Error()
		}
		return out
	}
	out.Logged = true
	list, err := matehost.LoadDuty(path)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].At.After(list[j].At) })
	for _, w := range list {
		e := DutyEntry{
			ID: w.ID, Kind: w.Kind, Title: w.Title, Status: w.Status, Topic: w.Topic, Last: w.Last,
			AddedAt: db.FormatTime(w.AddedAt), At: db.FormatTime(w.At), DoneAt: db.FormatTime(w.DoneAt), OkayedAt: db.FormatTime(w.OkayedAt),
			Notes: []DutyNote{},
		}
		for _, n := range w.Notes {
			e.Notes = append(e.Notes, DutyNote{At: db.FormatTime(n.At), Op: n.Op, Text: n.Text, Topic: n.Topic})
		}
		out.Duty = append(out.Duty, e)
	}
	return out
}
