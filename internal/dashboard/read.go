package dashboard

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/timeline"
	"github.com/nguyenngocanh94/mate/internal/timeline/read"
)

// errNotFound is a 404 with a sentence: an unknown project or an unknown
// crew is a reader's mistake, not a broken server.
type errNotFound struct{ reason string }

func (e errNotFound) Error() string { return e.reason }

// errBadRequest is a question the API cannot answer as asked: a 400 with
// the reason.
type errBadRequest struct{ reason string }

func (e errBadRequest) Error() string { return e.reason }

func notFound(format string, a ...any) error {
	return errNotFound{reason: fmt.Sprintf(format, a...)}
}

// lastEventID is the generation every response is cached against:
// `MAX(event.id)` over the whole workspace, because a dashboard that
// refreshed one project's page when another project's event landed would be
// wrong in the cheap direction, and one that missed it would be wrong in
// the expensive one.
func (s *Server) lastEventID(ctx context.Context) (int64, error) {
	return read.LastEventID(ctx, s.db.SQL())
}

// sceneRows reads `v_now` for one project, keyed by actor id.
//
// It does not go through scene.Now: that helper is `mate events --scene`'s
// and selects the eight columns the snapshot line prints, which do not
// include `context_pct` (added to the view by schema v2 for task 27). The
// page needs it on every row, so read.NowRows reads the view directly and
// joins `actor` for the target's display name the same way scene.Now does.
func (s *Server) sceneRows(ctx context.Context, project string) (map[string]SceneRow, error) {
	rows, err := read.NowRows(ctx, s.db.SQL(), project)
	if err != nil {
		return nil, err
	}
	out := map[string]SceneRow{}
	for _, n := range rows {
		r := SceneRow{ActorID: n.ActorID, Actor: n.ActorName, ActorKind: n.ActorKind, Project: n.Project,
			State: n.State, Since: n.Since, Target: n.Target, Detail: n.Detail, TokensToday: n.TokensToday}
		r.ContextPct = nullFloat(n.ContextPct)
		if n.ContextTokens.Valid {
			v := n.ContextTokens.Int64
			r.ContextTokens = &v
		}
		out[r.ActorID] = r
	}
	if meta, err := s.ws.ReadMateMeta(project); err == nil && meta["session_id"] != "" {
		id := timeline.MateActorID(project)
		if row, ok := out[id]; ok {
			row.ContextTokens, row.ContextPct = nil, nil
			if usage, known, err := s.db.LatestContext(ctx, id, meta["session_id"]); err == nil && known {
				row.ContextTokens, row.ContextPct = &usage.Tokens, usage.Pct
			}
			out[id] = row
		}
	}
	return out, nil
}

// actorFacts are the two things `v_now` has no column for and a card needs:
// which harness the actor runs, and whether it has gone. Both live on
// `actor`, which the ingest fills from `mate.meta` and `crews/<id>.meta`
// (internal/timeline/meta.go): `gone_at` is the Mate's `stopped_at` and a
// crew's teardown time.
type actorFacts struct {
	harness   string
	firstSeen string
	goneAt    string
}

// running is "started and not stopped". An actor with no first_seen has
// never run at all, which is not the same as having stopped.
func (a actorFacts) running() bool { return a.firstSeen != "" && a.goneAt == "" }

func (s *Server) actorFacts(ctx context.Context, actorID string) (actorFacts, bool, error) {
	a, ok, err := read.ActorOf(ctx, s.db.SQL(), actorID)
	if err != nil || !ok {
		return actorFacts{}, false, err
	}
	return actorFacts{harness: a.Harness, firstSeen: a.FirstSeen, goneAt: a.GoneAt}, true, nil
}

// actorTotals is one actor's turn count, token buckets and cost, computed
// the way `v_task_ledger` computes a task's - including the guard that a
// pricing row of all zeroes is not a price (docs/timeline.md section 10).
// There is no task row for a Mate, so `v_task_ledger` cannot answer for
// one and this does, from `turn` directly. It is the same SQL
// cmd/mate/usage.go's mateLedgerRow runs (read.ActorTotals and
// read.ActorCost), and the unit tests compare the two number for number.
func (s *Server) actorTotals(ctx context.Context, actorID string) (int64, Tokens, *float64, error) {
	totals, err := read.ActorTotals(ctx, s.db.SQL(), actorID)
	if err != nil {
		return 0, Tokens{}, nil, err
	}
	tk := Tokens{Input: totals.Input, CacheRead: totals.CacheRead, CacheWrite: totals.CacheWrite,
		Output: totals.Output, Thinking: totals.Thinking}
	tk.Total = tk.Input + tk.CacheRead + tk.CacheWrite + tk.Output

	cost, err := read.ActorCost(ctx, s.db.SQL(), actorID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, tk, nil, err
	}
	return totals.Turns, tk, nullFloat(cost), nil
}

// toolCount sums `turn.tool_count` for an actor. `v_task_ledger` has no
// such column and the tier-3 ledger of docs/mvp.md M6 asks for it ("số tool
// call"), so it is read off `turn`, the same table the view's own token
// sums come from.
func (s *Server) toolCount(ctx context.Context, actorID string) (int64, error) {
	return read.ToolCount(ctx, s.db.SQL(), actorID)
}

// toolCounts is toolCount for every actor of a project in one query, for
// the task table, which would otherwise ask once per crew.
func (s *Server) toolCounts(ctx context.Context, project string) (map[string]int64, error) {
	return read.ToolCounts(ctx, s.db.SQL(), project)
}

// tasks reads `v_task_ledger` for a project, oldest spawn first, and hangs
// each crew's current scene state on it.
func (s *Server) tasks(ctx context.Context, project string, now time.Time) ([]Task, error) {
	return s.taskRows(ctx, project, now, "")
}

// taskRows is tasks, filled in for every crew, or for `only` alone when it
// is set: what a row gathers beyond the ledger - files under the crew's
// directory, its tool calls - is most of the table's time, and one task's
// page needs one row. Every row still carries the ledger's tokens, which a
// task's tokens_with_reviews sums over its reviewers.
func (s *Server) taskRows(ctx context.Context, project string, now time.Time, only string) ([]Task, error) {
	scenes, err := s.sceneRows(ctx, project)
	if err != nil {
		return nil, err
	}
	rows, err := read.Tasks(ctx, s.db.SQL(), project)
	if err != nil {
		return nil, err
	}

	out := []Task{}
	var ids []string
	for _, r := range rows {
		t := Task{Crew: r.Crew, Text: r.Text, Branch: r.Branch, SpawnedAt: r.SpawnedAt, ClosedAt: r.ClosedAt,
			CloseState: r.CloseState, LastModel: r.LastModel, MergedAt: r.MergedAt,
			QuestionCount: r.QuestionCount, HandbackCount: r.HandbackCount, Turns: r.Turns,
			Tokens: Tokens{Input: r.Input, CacheRead: r.CacheRead, CacheWrite: r.CacheWrite,
				Output: r.Output, Thinking: r.Thinking},
			ContextTokensLast: r.ContextTokensLast, WaitedMs: r.WaitedMs}
		t.Closed = t.CloseState != "" || t.ClosedAt != ""
		t.ContextPct, t.Cost = nullFloat(r.ContextPct), nullFloat(r.Cost)
		t.Tokens.Total = t.Tokens.Input + t.Tokens.CacheRead + t.Tokens.CacheWrite + t.Tokens.Output
		t.AgeMs = ageMs(t.SpawnedAt, t.ClosedAt, now)
		if sc, ok := scenes[r.CrewActorID]; ok {
			t.State, t.Since, t.Target, t.Detail = sc.State, sc.Since, sc.Target, sc.Detail
		}
		out = append(out, t)
		ids = append(ids, r.CrewActorID)
	}
	agents, err := s.agentsUsed(ctx, project)
	if err != nil {
		return nil, err
	}
	var tools map[string]int64
	if only == "" {
		if tools, err = s.toolCounts(ctx, project); err != nil {
			return nil, err
		}
	}
	cfg, cfgErr := s.ws.LoadProject(project)
	for i, id := range ids {
		if only != "" && out[i].Crew != only {
			continue
		}
		if only != "" {
			if tools == nil {
				tools = map[string]int64{}
			}
			if tools[id], err = s.toolCount(ctx, id); err != nil {
				return nil, err
			}
		}
		out[i].ToolCount = tools[id]
		out[i].Agents = agents[id]
		facts, _, err := s.actorFacts(ctx, id)
		if err != nil {
			return nil, err
		}
		out[i].Harness = facts.harness
		meta, err := s.ws.ReadCrewMeta(project, out[i].Crew)
		if err == nil {
			out[i].Model, out[i].Effort = meta["model"], meta["effort"]
			out[i].FailedReason = meta["failed_reason"]
		}
		if cfgErr == nil {
			if repo, err := cfg.CrewRepo(meta); err == nil {
				out[i].Repo = repo.Name
			}
		}
		out[i].Review = s.reviewVerdict(project, out[i].Crew)
		out[i].Role = taskRole(s.ws, project, out[i].Crew, out[i].Review, meta)
	}
	reviewersOf, reviewOf, err := s.ws.ReviewLinks(project)
	if err != nil {
		return nil, err
	}
	index := make(map[string]int, len(out))
	for i, t := range out {
		index[t.Crew] = i
	}
	for i := range out {
		t := &out[i]
		t.TokensWithReviews = t.Tokens.Total
		if task, ok := reviewOf[t.Crew]; ok {
			if _, known := index[task]; known {
				t.ReviewOf = task
			}
		}
		t.Reviews = reviewersOf[t.Crew]
		for _, reviewer := range t.Reviews {
			if j, ok := index[reviewer]; ok {
				t.TokensWithReviews += out[j].Tokens.Total
			}
		}
	}
	return out, nil
}

// agentsUsed is Task.Agents for every actor of a project: the distinct
// `agent` of its `tool.called` events, in the order each was first used.
//
// Finding them reads the payload of every `tool.called` event, which on a
// real workspace is over a second, and every task table, task page and board
// asks for it. So the server keeps them (agentIndex): the first call reads
// every event, each later one only the events appended since (CON-126).
func (s *Server) agentsUsed(ctx context.Context, project string) (map[string][]string, error) {
	return s.agents.used(ctx, s.db.SQL(), project)
}

// agentIndex is the first time each actor used each kind of agent, kept for
// the whole workspace and brought up to date from the event ids past `seen`.
type agentIndex struct {
	mu    sync.Mutex
	seen  int64                                   // highest event id read
	first map[string]map[string]map[string]string // project → actor → agent → first at
}

func (x *agentIndex) used(ctx context.Context, q read.Querier, project string) (map[string][]string, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	top, err := read.LastEventID(ctx, q)
	if err != nil {
		return nil, err
	}
	// A smaller highest id is a `mate reindex` under us: read it all again.
	if top < x.seen || x.first == nil {
		x.seen, x.first = 0, map[string]map[string]map[string]string{}
	}
	if top > x.seen {
		used, err := read.ToolAgents(ctx, q, x.seen, top, timeline.KindToolCalled)
		if err != nil {
			return nil, err
		}
		for _, u := range used {
			p, actor, agent, at := u.Project, u.ActorID, u.Agent, u.At
			if x.first[p] == nil {
				x.first[p] = map[string]map[string]string{}
			}
			if x.first[p][actor] == nil {
				x.first[p][actor] = map[string]string{}
			}
			if was, ok := x.first[p][actor][agent]; !ok || at < was {
				x.first[p][actor][agent] = at
			}
		}
		x.seen = top
	}
	out := map[string][]string{}
	for actor, agents := range x.first[project] {
		list := make([]string, 0, len(agents))
		for agent := range agents {
			list = append(list, agent)
		}
		sort.Slice(list, func(i, j int) bool {
			if a, b := agents[list[i]], agents[list[j]]; a != b {
				return a < b
			}
			return list[i] < list[j]
		})
		out[actor] = list
	}
	return out, nil
}

// taskRole is Task.Role: a reviewer first, then a scout, else a ship.
func taskRole(ws *store.Workspace, project, crew, review string, meta map[string]string) string {
	switch {
	case review != "":
		return store.WorkflowRoleReview
	case ws.CrewIsScout(project, crew, meta):
		return store.WorkflowRoleScout
	}
	return store.WorkflowRoleShip
}

// reviewVerdict is Task.Review: "" unless the crew has review.meta, then
// its report's Verdict, or "pending" while the report has none.
func (s *Server) reviewVerdict(project, crew string) string {
	if _, err := os.Stat(s.ws.CrewReviewMeta(project, crew)); err != nil {
		return ""
	}
	data, err := os.ReadFile(s.ws.CrewReport(project, crew))
	if err != nil {
		return "pending"
	}
	if v := store.ReviewVerdict(string(data)); v != "" {
		return v
	}
	return "pending"
}

// task is one crew's ledger row, or a 404 naming the crew.
func (s *Server) task(ctx context.Context, project, crew string, now time.Time) (Task, error) {
	all, err := s.taskRows(ctx, project, now, crew)
	if err != nil {
		return Task{}, err
	}
	for _, t := range all {
		if t.Crew == crew {
			return t, nil
		}
	}
	return Task{}, notFound("no crew %q in project %q; `mate crew list %s --all` names the ones there are", crew, project, project)
}

// turns reads an actor's turns in the order they happened, with the kind of
// the event that triggered each. The trigger's kind is a join onto `event`:
// `turn` records only `trigger_event_id`, and a page that showed a bare id
// would be showing the reader a number they cannot read.
func (s *Server) turns(ctx context.Context, actorID string) ([]Turn, error) {
	rows, err := read.Turns(ctx, s.db.SQL(), actorID)
	if err != nil {
		return nil, err
	}

	out := []Turn{}
	for _, r := range rows {
		t := Turn{ID: r.ID, SessionID: r.SessionID, HarnessTurnRef: r.HarnessTurnRef, Ordinal: r.Ordinal,
			StartedAt: r.StartedAt.String, EndedAt: r.EndedAt.String,
			TriggerEventID: r.TriggerEventID, TriggerKind: r.TriggerKind,
			Outcome: r.Outcome, Model: r.Model, Effort: r.Effort,
			Tokens: Tokens{Input: r.Input, CacheRead: r.CacheRead, CacheWrite: r.CacheWrite,
				Output: r.Output, Thinking: r.Thinking},
			ContextAfter: r.ContextAfter, ToolCount: r.ToolCount, Ref: Ref{Path: r.RefPath, Offset: r.RefOffset}}
		t.Tokens.Total = t.Tokens.Input + t.Tokens.CacheRead + t.Tokens.CacheWrite + t.Tokens.Output
		t.DurationMs = spanMs(t.StartedAt, t.EndedAt)
		t.ContextPct = nullFloat(r.ContextPct)
		out = append(out, t)
	}
	return out, nil
}

// turnByID is one turn of one actor. The actor is part of the lookup, not
// just the id: a turn id from another crew's page must 404 rather than
// render under this crew's heading.
func (s *Server) turnByID(ctx context.Context, actorID, turnID string) (Turn, error) {
	all, err := s.turns(ctx, actorID)
	if err != nil {
		return Turn{}, err
	}
	for _, t := range all {
		if t.ID == turnID {
			return t, nil
		}
	}
	return Turn{}, notFound("no turn %q recorded for this actor", turnID)
}

// actions reads the tool calls of one turn.
//
// `action` carries no `ref_path`/`ref_offset` of its own - the schema puts
// the locator on `event` - so the ref comes from the `tool.called` event the
// row was written beside, through `action.event_id`.
func (s *Server) actions(ctx context.Context, turnID string) ([]Action, error) {
	rows, err := read.TurnActions(ctx, s.db.SQL(), turnID)
	if err != nil {
		return nil, err
	}

	out := []Action{}
	for _, r := range rows {
		a := Action{ID: r.ID, At: r.At, EndedAt: r.EndedAt, Tool: r.Tool, Target: r.Target, Summary: r.Summary,
			EventID: r.EventID, Ref: Ref{Path: r.RefPath, Offset: r.RefOffset}}
		if r.DurationMs.Valid {
			v := r.DurationMs.Int64
			a.DurationMs = &v
		}
		if r.OK.Valid {
			v := r.OK.Bool
			a.OK = &v
		}
		out = append(out, a)
	}
	return out, nil
}

// turnEvents are the `v_story` rows recorded inside one turn - the same
// rows, in the same shape and the same field order, that `mate events`
// prints as JSON lines.
func (s *Server) turnEvents(ctx context.Context, project, turnID string) ([]timeline.StoryEvent, error) {
	out, err := timeline.Story(ctx, s.db.SQL(), timeline.StoryQuery{Project: project, TurnID: turnID})
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []timeline.StoryEvent{}
	}
	return out, nil
}

// statusLines are the `status.appended` events of one crew: the lines the
// crew itself wrote, with the verb and the text the ingest already split
// out of the payload (docs/timeline.md section 4).
func (s *Server) statusLines(ctx context.Context, actorID string) ([]StatusLine, error) {
	rows, err := read.ActorEventsOfKind(ctx, s.db.SQL(), actorID, timeline.KindStatusAppend)
	if err != nil {
		return nil, err
	}
	out := []StatusLine{}
	for _, e := range rows {
		out = append(out, statusLine(e))
	}
	return out, nil
}

// statusLine is a `status.appended` event as a status line, with the verb
// and the text the ingest split out of its payload.
func statusLine(e read.Event) StatusLine {
	line := StatusLine{EventID: e.ID, At: e.At, TurnID: e.TurnID, Ref: Ref{Path: e.RefPath, Offset: e.RefOffset}}
	ev := timeline.StoryEvent{Payload: []byte(payloadOrEmpty(e.Payload))}
	line.Verb, line.Text, line.Line = ev.Field("verb"), ev.Field("text"), ev.Field("line")
	return line
}

// questions are what the crew stopped and asked, with the answer joined on.
//
// The answer's text is not on `question` - the table records which event
// answered it, not what the answer said - so it is read from the answering
// event itself, and the answerer's display name from `actor`.
//
// Two sources, in that order, because the answering event is a
// `question.answered` and the line that carried the words is the
// `message.sent` beside it: `message.text` when a `message` row exists for
// the event, otherwise the event's own payload, whose `text` is the answer
// verbatim (docs/timeline.md's question.answered example). Preferring
// `message` keeps the page showing the line as sent even if the payload
// were ever trimmed.
func (s *Server) questions(ctx context.Context, actorID string) ([]Question, error) {
	rows, err := read.CrewQuestions(ctx, s.db.SQL(), actorID)
	if err != nil {
		return nil, err
	}
	out := []Question{}
	for _, r := range rows {
		out = append(out, question(r))
	}
	return out, nil
}

// question is a question row as the page shows it: the answer is the line
// as sent, else the answering event's own text.
func question(r read.Question) Question {
	q := Question{ID: r.ID, Kind: r.Kind, AskedEventID: r.AskedEventID, AskedAt: r.AskedAt, Text: r.Text,
		AnsweredEventID: r.AnsweredEventID.Int64, AnsweredAt: r.AnsweredAt, AnsweredBy: r.AnsweredBy, Answer: r.Answer}
	if q.Answer == "" && r.AnsweredEventID.Valid {
		q.Answer = timeline.StoryEvent{Payload: []byte(payloadOrEmpty(r.Payload))}.Field("text")
	}
	if r.WaitedMs.Valid {
		v := r.WaitedMs.Int64
		q.WaitedMs = &v
	}
	return q
}

// history is the status lines and decisions of every crew in a project
// since a time, by crew: what a tent in Agent City plays back (CON-127).
// Two queries for the whole project, where the task endpoint takes two a
// crew.
func (s *Server) history(ctx context.Context, project, since string) (map[string]*CrewHistory, error) {
	prefix := timeline.CrewActorID(project, "")
	out := map[string]*CrewHistory{}
	of := func(actorID string) *CrewHistory {
		crew := strings.TrimPrefix(actorID, prefix)
		if out[crew] == nil {
			out[crew] = &CrewHistory{StatusLines: []StatusLine{}, Questions: []Question{}}
		}
		return out[crew]
	}
	lines, err := read.ProjectEventsOfKind(ctx, s.db.SQL(), project, since, timeline.KindStatusAppend, prefix)
	if err != nil {
		return nil, err
	}
	for _, e := range lines {
		h := of(e.ActorID)
		h.StatusLines = append(h.StatusLines, statusLine(e))
	}
	questions, err := read.QuestionsSince(ctx, s.db.SQL(), prefix, since)
	if err != nil {
		return nil, err
	}
	for _, r := range questions {
		if q := question(r); q.Kind == "" || q.Kind == timeline.QuestionDecision {
			h := of(r.CrewActorID)
			h.Questions = append(h.Questions, q)
		}
	}
	return out, nil
}

// inbox is the project's waiting decisions, through query.LoadBox - the
// exact loader cmd/mate/console_session.go hands the console's rail, so
// the page and the TUI cannot disagree about what is waiting. It reads
// files and writes none.
func (s *Server) inbox(project string) ([]InboxItem, string) {
	field := query.LoadBox(s.ws, project)
	if !field.IsKnown() {
		return []InboxItem{}, field.Reason
	}
	view := field.Value
	out := make([]InboxItem, 0, len(view.Inbox))
	for _, e := range view.Inbox {
		item := InboxItem{
			Seq:       e.Seq,
			At:        db.FormatTime(e.At),
			Kind:      string(e.Kind),
			Source:    e.Source,
			Target:    e.Target,
			Crew:      e.Crew,
			Verb:      e.Verb,
			Text:      e.Text,
			Attention: e.Attention,
		}
		if e.Resolvable() {
			item.AssignKey = e.AssignKey
		}
		out = append(out, item)
	}
	return out, ""
}

// mateBlock is the Mate of one project: `v_now` for where it stands,
// `actor` for its harness and whether it has gone, `turn` for what it has
// spent and what it last did.
func (s *Server) mateBlock(ctx context.Context, project string, scenes map[string]SceneRow) (Mate, error) {
	actorID := timeline.MateActorID(project)
	facts, _, err := s.actorFacts(ctx, actorID)
	if err != nil {
		return Mate{}, err
	}
	out := Mate{Harness: facts.harness, Running: facts.running()}
	if sc, ok := scenes[actorID]; ok {
		out.State, out.Since, out.Target, out.Detail = sc.State, sc.Since, sc.Target, sc.Detail
		out.TokensToday, out.ContextPct, out.ContextTokens = sc.TokensToday, sc.ContextPct, sc.ContextTokens
	}
	turns, tokens, cost, err := s.actorTotals(ctx, actorID)
	if err != nil {
		return Mate{}, err
	}
	out.Turns, out.Tokens, out.Cost = turns, tokens, cost
	all, err := s.turns(ctx, actorID)
	if err != nil {
		return Mate{}, err
	}
	if len(all) > 0 {
		last := all[len(all)-1]
		out.LastTurn = &last
	}
	return out, nil
}

// nullFloat renders a nullable REAL as a pointer: a missing cost or an
// unknown context window is `null` on the wire and `?` in the CLI, never a
// zero the reader would believe.
func nullFloat(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	out := v.Float64
	return &out
}

func payloadOrEmpty(v string) string {
	if v == "" {
		return "{}"
	}
	return v
}

// spanMs is ended-started in milliseconds, 0 when either end is missing -
// a turn that has not ended has no duration yet, which is not a duration of
// zero, but a page showing a live turn wants a running row and not a null.
func spanMs(startedAt, endedAt string) int64 {
	if startedAt == "" || endedAt == "" {
		return 0
	}
	start, end := db.ParseTime(startedAt), db.ParseTime(endedAt)
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return 0
	}
	return end.Sub(start).Milliseconds()
}

func ageMs(spawnedAt, closedAt string, now time.Time) int64 {
	if spawnedAt == "" {
		return 0
	}
	start := db.ParseTime(spawnedAt)
	if start.IsZero() {
		return 0
	}
	end := now
	if closedAt != "" {
		if t := db.ParseTime(closedAt); !t.IsZero() {
			end = t
		}
	}
	if end.Before(start) {
		return 0
	}
	return end.Sub(start).Milliseconds()
}
