package dashboard

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/timeline"
	"github.com/nguyenngocanh94/mate/internal/timeline/read"
)

// handleBoardDay is GET /api/board/day?date=YYYY-MM-DD: one day of the
// board, listed rather than counted (CON-88). The day is the server
// clock's zone, as the board's columns are, so the lists add up to the
// day's row: the same turns, the same spawns and closes, the same
// incidents.
func (s *Server) handleBoardDay(ctx context.Context, r *http.Request, env envelope) (any, error) {
	now := s.deps.now()
	loc := now.Location()
	raw := strings.TrimSpace(r.URL.Query().Get("date"))
	start, err := time.ParseInLocation(time.DateOnly, raw, loc)
	if err != nil {
		return nil, errBadRequest{reason: "date must be a day as YYYY-MM-DD"}
	}
	date := start.Format(time.DateOnly)
	end := start.AddDate(0, 0, 1)
	since, until := db.FormatTime(start), db.FormatTime(end)
	// onDay is whether a stored timestamp falls on the day, in its zone.
	onDay := func(ts string) bool {
		if ts == "" {
			return false
		}
		t := db.ParseTime(ts)
		return !t.IsZero() && t.In(loc).Format(time.DateOnly) == date
	}

	out := BoardDayResponse{envelope: env, Date: date, Zone: now.Format("-07:00"),
		Actors: []BoardActor{}, Tasks: []BoardTask{}, Incidents: []BoardIncident{}}

	// The day's turns, by actor: the Actors list, and each task's share.
	actors := map[string]*BoardActor{}
	turns, err := read.TurnTokensBetween(ctx, s.db.SQL(), since, until)
	if err != nil {
		return nil, err
	}
	for _, t := range turns {
		id, project, kind, name, at := t.ActorID, t.Project, t.Kind, t.Name, t.At
		tk := Tokens{Input: t.Input, CacheRead: t.CacheRead, CacheWrite: t.CacheWrite, Output: t.Output, Thinking: t.Thinking}
		if !onDay(at) || (kind != timeline.ActorMate && kind != timeline.ActorCrew) {
			continue
		}
		tk.Total = tk.Input + tk.CacheRead + tk.CacheWrite + tk.Output
		a := actors[id]
		if a == nil {
			a = &BoardActor{Project: project, Kind: kind, Name: name}
			if kind == timeline.ActorMate {
				a.Name = ""
			}
			actors[id] = a
		}
		a.Turns++
		a.Tokens = addTokens(a.Tokens, tk)
	}

	known := map[string]bool{}
	for _, ref := range s.ws.Projects() {
		known[ref.Name] = true
		tasks, err := s.tasks(ctx, ref.Name, now)
		if err != nil {
			if out.Errors == nil {
				out.Errors = map[string]string{}
			}
			out.Errors[ref.Name] = err.Error()
		}
		for _, t := range tasks {
			bt := BoardTask{Project: ref.Name, Task: t, Opened: onDay(t.SpawnedAt), ClosedToday: t.Closed && onDay(t.ClosedAt)}
			if a := actors[timeline.CrewActorID(ref.Name, t.Crew)]; a != nil {
				bt.DayTurns, bt.DayTokens = a.Turns, a.Tokens
			}
			if bt.Opened || bt.ClosedToday || bt.DayTurns > 0 {
				out.Tasks = append(out.Tasks, bt)
			}
		}
	}
	for _, a := range actors {
		if known[a.Project] {
			out.Actors = append(out.Actors, *a)
		}
	}
	sort.Slice(out.Actors, func(i, j int) bool {
		a, b := out.Actors[i], out.Actors[j]
		if a.Tokens.Total != b.Tokens.Total {
			return a.Tokens.Total > b.Tokens.Total
		}
		if a.Project != b.Project {
			return a.Project < b.Project
		}
		return a.Name < b.Name
	})
	sort.SliceStable(out.Tasks, func(i, j int) bool {
		a, b := out.Tasks[i], out.Tasks[j]
		if a.DayTokens.Total != b.DayTokens.Total {
			return a.DayTokens.Total > b.DayTokens.Total
		}
		return a.SpawnedAt < b.SpawnedAt
	})

	incidents, err := read.Incidents(ctx, s.db.SQL(), since, until)
	if err != nil {
		return nil, err
	}
	for _, r := range incidents {
		in := BoardIncident{Project: r.Project, Kind: r.Kind, ActorKind: r.ActorKind, Name: r.ActorName,
			OpenedAt: r.OpenedAt, ResolvedAt: r.ResolvedAt}
		if !known[in.Project] || !onDay(in.OpenedAt) {
			continue
		}
		if in.ActorKind == timeline.ActorMate {
			in.Name = ""
		}
		out.Incidents = append(out.Incidents, in)
	}
	return out, nil
}
