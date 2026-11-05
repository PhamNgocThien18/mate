package dashboard

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/timeline"
	"github.com/nguyenngocanh94/mate/internal/timeline/read"
)

// Board day window: what `?days=` defaults to and the most it may ask for.
const (
	boardDefaultDays = 14
	boardMaxDays     = 90
)

// handleBoard is GET /api/board?days=<n>: the Office's whiteboard (CON-84),
// one column per local calendar day, for the workspace and for each project.
// A day is the server clock's own zone - the captain's day, not UTC's -
// so it is not cached: the window moves at midnight whether or not an
// event does.
func (s *Server) handleBoard(ctx context.Context, r *http.Request, env envelope) (any, error) {
	days := boardDefaultDays
	if n, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("days"))); err == nil && n > 0 {
		days = min(n, boardMaxDays)
	}
	now := s.deps.now()
	loc := now.Location()
	y, m, d := now.Date()
	first := time.Date(y, m, d-(days-1), 0, 0, 0, 0, loc)
	since := db.FormatTime(first)

	out := BoardResponse{envelope: env, Days: days, Zone: now.Format("-07:00"), Projects: []BoardProject{}}
	index := map[string]int{}
	for i := 0; i < days; i++ {
		date := first.AddDate(0, 0, i).Format(time.DateOnly)
		out.Dates = append(out.Dates, date)
		index[date] = i
	}
	// dayOf is the column a stored timestamp falls in, or -1 outside the
	// window (an empty or unparseable stamp is outside it too).
	dayOf := func(ts string) int {
		if ts == "" {
			return -1
		}
		t := db.ParseTime(ts)
		if t.IsZero() {
			return -1
		}
		if i, ok := index[t.In(loc).Format(time.DateOnly)]; ok {
			return i
		}
		return -1
	}

	for _, ref := range s.ws.Projects() {
		p := BoardProject{Name: ref.Name, Days: newBoardDays(out.Dates)}
		tasks, err := s.tasks(ctx, ref.Name, now)
		if err != nil {
			p.Error = err.Error()
		}
		for _, t := range tasks {
			meta, _ := s.ws.ReadCrewMeta(ref.Name, t.Crew)
			role := boardRole(s, ref.Name, t, meta)
			if i := dayOf(t.SpawnedAt); i >= 0 {
				p.Days[i].Spawned[role]++
			}
			if !t.Closed {
				continue
			}
			i := dayOf(t.ClosedAt)
			if i < 0 {
				continue
			}
			if role == "review" {
				p.Days[i].Reviews[reviewWord(t.Review)]++
			} else {
				p.Days[i].Closed[closedWord(t)]++
			}
		}
		out.Projects = append(out.Projects, p)
	}
	// Taken after the appends, which may move the slice.
	byName := map[string]*BoardProject{}
	for i := range out.Projects {
		byName[out.Projects[i].Name] = &out.Projects[i]
	}

	turns, err := read.TurnTokensBetween(ctx, s.db.SQL(), since, "")
	if err != nil {
		return nil, err
	}
	for _, t := range turns {
		project, kind, at := t.Project, t.Kind, t.At
		tk := Tokens{Input: t.Input, CacheRead: t.CacheRead, CacheWrite: t.CacheWrite, Output: t.Output, Thinking: t.Thinking}
		p, i := byName[project], dayOf(at)
		if p == nil || i < 0 {
			continue
		}
		tk.Total = tk.Input + tk.CacheRead + tk.CacheWrite + tk.Output
		day := &p.Days[i]
		day.Tokens = addTokens(day.Tokens, tk)
		switch kind {
		case timeline.ActorMate:
			day.MateTokens += tk.Total
		case timeline.ActorCrew:
			day.CrewTokens += tk.Total
		}
	}

	incidents, err := read.Incidents(ctx, s.db.SQL(), since, "")
	if err != nil {
		return nil, err
	}
	for _, in := range incidents {
		if p, i := byName[in.Project], dayOf(in.OpenedAt); p != nil && i >= 0 {
			p.Days[i].Incidents[in.Kind]++
		}
	}

	out.Total = newBoardDays(out.Dates)
	for _, p := range out.Projects {
		for i, d := range p.Days {
			t := &out.Total[i]
			t.Tokens = addTokens(t.Tokens, d.Tokens)
			t.MateTokens += d.MateTokens
			t.CrewTokens += d.CrewTokens
			addCounts(t.Spawned, d.Spawned)
			addCounts(t.Closed, d.Closed)
			addCounts(t.Reviews, d.Reviews)
			addCounts(t.Incidents, d.Incidents)
		}
	}
	return out, nil
}

// boardRole is what a task was for: a reviewer (review.meta, so Review is
// set), a scout (store.Workspace.CrewIsScout), else a ship.
func boardRole(s *Server, project string, t Task, meta map[string]string) string {
	switch {
	case t.Review != "":
		return "review"
	case s.ws.CrewIsScout(project, t.Crew, meta):
		return "scout"
	}
	return "ship"
}

// closedWord buckets a closed ship or scout the way the cabinet words it,
// without `merged`: the timeline sees only the merges mate did itself, so
// a count of them would read as work not landing.
func closedWord(t Task) string {
	switch {
	case t.CloseState == "failed" && t.FailedReason != "":
		return "didnt_start"
	case t.CloseState == "failed":
		return "discarded"
	case t.CloseState == "":
		return "finished"
	}
	return t.CloseState
}

// reviewWord is a closed reviewer's verdict, `no_verdict` for one closed
// before it wrote any.
func reviewWord(v string) string {
	if v == "" || v == "pending" {
		return "no_verdict"
	}
	return v
}

func newBoardDays(dates []string) []BoardDay {
	out := make([]BoardDay, len(dates))
	for i, d := range dates {
		out[i] = BoardDay{Date: d, Spawned: map[string]int{}, Closed: map[string]int{}, Reviews: map[string]int{}, Incidents: map[string]int{}}
	}
	return out
}

func addTokens(a, b Tokens) Tokens {
	return Tokens{Input: a.Input + b.Input, CacheRead: a.CacheRead + b.CacheRead, CacheWrite: a.CacheWrite + b.CacheWrite,
		Output: a.Output + b.Output, Thinking: a.Thinking + b.Thinking, Total: a.Total + b.Total}
}

func addCounts(into, from map[string]int) {
	for k, n := range from {
		into[k] += n
	}
}
