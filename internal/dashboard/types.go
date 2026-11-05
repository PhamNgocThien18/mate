package dashboard

import (
	"github.com/nguyenngocanh94/mate/internal/diagnostics"
	"github.com/nguyenngocanh94/mate/internal/timeline"
)

// The JSON shapes of docs/dashboard.md. Field order here is the field order
// on the wire, and a field is added at the end rather than in the middle,
// the same rule timeline.StoryEvent follows.
//
// Two conventions run through all of them. A nullable number is a pointer
// and renders as `null`, never as 0: a cost with no price and a context
// percentage with no known window are unknown, and docs/timeline.md's
// "a missing price is not a price of zero" is the same rule `mate usage`
// prints as `?`. A timestamp is the database's own string - RFC3339 with
// nanoseconds, in UTC (db.TimeFormat) - passed through rather than
// reformatted, so a number on the page and a row in the database compare
// byte for byte.

// envelope is on every response. GeneratedAt is when the snapshot behind
// the bytes was computed, which for a cached response is when the
// generation it belongs to was first built - the honest reading, since
// nothing in it has changed since.
type envelope struct {
	GeneratedAt string `json:"generated_at"`
	LastEventID int64  `json:"last_event_id"`
}

// Ref is a transcript locator: the file an event, turn or action was read
// out of, and the byte offset inside it. It is what makes every number on
// the page traceable back to the harness's own record (docs/mvp.md M6).
type Ref struct {
	Path   string `json:"path,omitempty"`
	Offset int64  `json:"offset,omitempty"`
}

// Tokens is the four billed buckets plus thinking. Total is
// input+cache_read+cache_write+output and excludes thinking, which is what
// `mate usage`'s TOTAL column sums and what `v_now.tokens_today` sums:
// thinking tokens are reported by the harness inside the output it already
// counted, so adding them would count them twice.
type Tokens struct {
	Input      int64 `json:"input"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
	Output     int64 `json:"output"`
	Thinking   int64 `json:"thinking"`
	Total      int64 `json:"total"`
}

// SceneRow is one row of `v_now`: where the scene projection last put an
// actor. State is empty for an actor no projection has placed - the
// captain, mate and the observer are in the story and not in the office
// (docs/timeline.md section 7).
type SceneRow struct {
	ActorID       string   `json:"actor_id"`
	Actor         string   `json:"actor"`
	ActorKind     string   `json:"actor_kind"`
	Project       string   `json:"project"`
	State         string   `json:"state"`
	Since         string   `json:"since,omitempty"`
	Target        string   `json:"target,omitempty"`
	Detail        string   `json:"detail,omitempty"`
	TokensToday   int64    `json:"tokens_today"`
	ContextPct    *float64 `json:"context_pct"`
	ContextTokens *int64   `json:"context_tokens"`
}

// MateCard is the Mate as a workspace card shows it (docs/mvp.md M6 tier 1).
type MateCard struct {
	Harness       string   `json:"harness"`
	Running       bool     `json:"running"`
	State         string   `json:"state"`
	Since         string   `json:"since,omitempty"`
	TokensToday   int64    `json:"tokens_today"`
	ContextPct    *float64 `json:"context_pct"`
	ContextTokens *int64   `json:"context_tokens"`
}

// Mate is the Mate as a project page shows it (tier 2): the card plus what
// it has spent and what its most recent turn was.
type Mate struct {
	Harness       string   `json:"harness"`
	Running       bool     `json:"running"`
	State         string   `json:"state"`
	Since         string   `json:"since,omitempty"`
	Target        string   `json:"target,omitempty"`
	Detail        string   `json:"detail,omitempty"`
	TokensToday   int64    `json:"tokens_today"`
	ContextPct    *float64 `json:"context_pct"`
	ContextTokens *int64   `json:"context_tokens"`
	Turns         int64    `json:"turns"`
	Tokens        Tokens   `json:"tokens"`
	Cost          *float64 `json:"cost"`
	LastTurn      *Turn    `json:"last_turn"`
}

// ProjectCard is one project on the workspace page.
type ProjectCard struct {
	Name string `json:"name"`
	// Mode is "auto" or "manual" - `.mate/projects/<p>/auto` (store.Auto).
	Mode string   `json:"mode"`
	Mate MateCard `json:"mate"`
	// CrewsByState counts this project's crew actors by scene state. A crew
	// no projection has placed counts under "unknown", which is a state of
	// the reader's knowledge and is named as one rather than dropped.
	CrewsByState map[string]int `json:"crews_by_state"`
	// InboxWaiting is box.Inbox's length: how many things are still waiting
	// on a decision, the same number the console's rail header shows.
	InboxWaiting int `json:"inbox_waiting"`
	// Repos names the project's repos, in project.yaml order; empty when it
	// has none or its project.yaml cannot be read. Agent City draws one tent
	// per repo (CON-125).
	Repos []string `json:"repos"`
	// Error is why this card is thin - an unreadable box, a project with no
	// timeline yet. The card is still returned: one broken project must not
	// blank the workspace.
	Error string `json:"error,omitempty"`
}

// WorkspaceResponse is GET /api/workspace.
type WorkspaceResponse struct {
	envelope
	Root     string        `json:"root"`
	Projects []ProjectCard `json:"projects"`
}

// Task is one row of the project page's table: `v_task_ledger` joined to
// the crew's current scene state.
type Task struct {
	Crew       string `json:"crew"`
	Text       string `json:"text"`
	Branch     string `json:"branch,omitempty"`
	State      string `json:"state"`
	Since      string `json:"since,omitempty"`
	Target     string `json:"target,omitempty"`
	Detail     string `json:"detail,omitempty"`
	CloseState string `json:"close_state,omitempty"`
	Closed     bool   `json:"closed"`
	SpawnedAt  string `json:"spawned_at,omitempty"`
	ClosedAt   string `json:"closed_at,omitempty"`
	MergedAt   string `json:"merged_at,omitempty"`
	// AgeMs is closed_at-spawned_at for a closed task and now-spawned_at
	// for an open one: how long the task has been alive, not how long ago
	// it started.
	AgeMs             int64    `json:"age_ms"`
	Turns             int64    `json:"turns"`
	Tokens            Tokens   `json:"tokens"`
	Cost              *float64 `json:"cost"`
	LastModel         string   `json:"last_model,omitempty"`
	ContextTokensLast int64    `json:"context_tokens_last"`
	ContextPct        *float64 `json:"context_pct"`
	QuestionCount     int64    `json:"question_count"`
	HandbackCount     int64    `json:"handback_count"`
	WaitedMs          int64    `json:"waited_ms"`
	ToolCount         int64    `json:"tool_count"`
	// Harness is the crew's `actor.harness`, the same source the Mate card
	// uses, so the page can name the harness even for a recording whose
	// telemetry carries no version.
	Harness string `json:"harness,omitempty"`
	// Model and Effort are the requested launch settings retained in crew meta.
	// LastModel is the observed model; absent settings remain unknown.
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	// Repo is the repo the crew works in: crew meta repo=, or the project's
	// sole repo for a meta written before M9 (store.CrewRepo). Absent when
	// neither answers, so a page can place a crew without one request per
	// task (CON-125).
	Repo string `json:"repo,omitempty"`
	// Review is set only for a reviewer crew (`mate review`, which leaves
	// review.meta beside the crew's report): the first line of the report's
	// Verdict section - pass, changes-requested or incomplete - or
	// "pending" while there is none. A reviewer is closed `finished` or
	// `failed` like any crew; this is what it concluded.
	Review string `json:"review,omitempty"`
	// Role is what the task was for: "review" for a reviewer crew (it has
	// review.meta, so Review is set), "scout" for a scout
	// (store.Workspace.CrewIsScout) and "ship" for a change to a repo, the default.
	Role string `json:"role"`
	// Reviews are the reviewers `mate review` started for this task (crew
	// meta reviews=, or the reviewer's review.meta naming this crew), and
	// ReviewOf the task a reviewer reviewed (CON-75).
	Reviews  []string `json:"reviews,omitempty"`
	ReviewOf string   `json:"review_of,omitempty"`
	// TokensWithReviews is Tokens.Total plus every reviewer's total: what
	// the task cost with its reviews. Equal to Tokens.Total for a task with
	// none, and the reviewer's own total for a reviewer row.
	TokensWithReviews int64 `json:"tokens_with_reviews"`
	// FailedReason is crew meta's failed_reason=, written when a spawn never
	// came up. A crew closed with `crew stop --discard` is `failed` too and
	// has none, which is how a page tells a broken start from work thrown
	// away.
	FailedReason string `json:"failed_reason,omitempty"`
	// Agents are the kinds of agent the crew delegated to, first use first:
	// the `agent` of its `tool.called` events (docs/timeline.md), which is
	// Claude's `subagent_type` - a project's `.claude/agents/<name>.md` such
	// as `backend-reviewer`, or a built-in such as `Explore`. Absent for a
	// crew that delegated to none, or only to agents of no named kind
	// (CON-124). The Office badges a crew with them.
	Agents []string `json:"agents,omitempty"`
}

// InboxItem is one thing still waiting on a decision, flattened from
// query.BoxView.Inbox - the same loader, the same order and the same
// vocabulary the console's rail draws.
type InboxItem struct {
	Seq       int    `json:"seq"`
	At        string `json:"at,omitempty"`
	Kind      string `json:"kind"`
	Source    string `json:"source,omitempty"`
	Target    string `json:"target,omitempty"`
	Crew      string `json:"crew,omitempty"`
	Verb      string `json:"verb,omitempty"`
	Text      string `json:"text"`
	Attention bool   `json:"attention"`
	// AssignKey is what an `assign` press names this entry by, set only
	// when the Mate can be handed it (query.BoxEntry.Resolvable).
	AssignKey string `json:"assign_key,omitempty"`
}

// ProjectResponse is GET /api/projects/{project}.
type ProjectResponse struct {
	envelope
	Project string      `json:"project"`
	Mode    string      `json:"mode"`
	Mate    Mate        `json:"mate"`
	Tasks   []Task      `json:"tasks"`
	Inbox   []InboxItem `json:"inbox"`
	// InboxError is why Inbox is empty when it should not be: the box read
	// failed. An unreadable sent.log must not blank the page.
	InboxError string `json:"inbox_error,omitempty"`
}

// MateResponse is the project's Mate ledger and its prompt-level exchanges.
type MateResponse struct {
	envelope
	Project   string         `json:"project"`
	Mate      Mate           `json:"mate"`
	Exchanges []MateExchange `json:"exchanges"`
}

// MateExchange is one prompt to the Mate and the work and reply it caused.
// Calls groups model calls by (session, harness_turn_ref); a missing ref is
// isolated as one call so unrelated work is never merged by a guess.
type MateExchange struct {
	ID           string               `json:"id"`
	Source       string               `json:"source"`
	Prompt       string               `json:"prompt"`
	PromptAt     string               `json:"prompt_at"`
	Response     string               `json:"response"`
	ResponseAt   string               `json:"response_at,omitempty"`
	StartedAt    string               `json:"started_at"`
	EndedAt      string               `json:"ended_at"`
	DurationMs   *int64               `json:"duration_ms"`
	ModelCalls   int64                `json:"model_calls"`
	ToolCalls    int64                `json:"tool_calls"`
	Model        string               `json:"model,omitempty"`
	Tokens       Tokens               `json:"tokens"`
	ContextAfter int64                `json:"context_tokens_after"`
	Activities   []MateActivity       `json:"activities"`
	PromptRef    Ref                  `json:"prompt_ref"`
	ResponseRef  Ref                  `json:"response_ref"`
	Overview     diagnostics.Overview `json:"overview"`
}

type MateActivity struct {
	At   string `json:"at"`
	Kind string `json:"kind"`
	Crew string `json:"crew,omitempty"`
	Text string `json:"text"`
}

// Turn is one model call.
type Turn struct {
	ID             string `json:"id"`
	SessionID      string `json:"-"`
	HarnessTurnRef string `json:"-"`
	Ordinal        int64  `json:"ordinal"`
	StartedAt      string `json:"started_at,omitempty"`
	EndedAt        string `json:"ended_at,omitempty"`
	DurationMs     int64  `json:"duration_ms"`
	TriggerEventID int64  `json:"trigger_event_id,omitempty"`
	TriggerKind    string `json:"trigger_kind,omitempty"`
	Outcome        string `json:"outcome,omitempty"`
	Model          string `json:"model,omitempty"`
	// Effort is the reasoning effort the turn ran at: the harness's own
	// record when it keeps one, else the launch profile (CON-75).
	Effort       string   `json:"effort,omitempty"`
	Tokens       Tokens   `json:"tokens"`
	ContextAfter int64    `json:"context_tokens_after"`
	ContextPct   *float64 `json:"context_pct"`
	ToolCount    int64    `json:"tool_count"`
	Ref          Ref      `json:"ref"`
}

// StatusLine is one line the crew wrote to `crews/<id>.status`, as the
// `status.appended` event recorded it.
type StatusLine struct {
	EventID int64  `json:"event_id"`
	At      string `json:"at"`
	TurnID  string `json:"turn_id,omitempty"`
	Verb    string `json:"verb,omitempty"`
	Text    string `json:"text"`
	Line    string `json:"line,omitempty"`
	Ref     Ref    `json:"ref"`
}

// Question is one thing the crew stopped and asked, with the answer it got.
// WaitedMs is null while it is still waiting, which is not the same as
// having waited zero.
type Question struct {
	ID string `json:"id"`
	// Kind is "decision" (a needs-decision line) or "handback" (a wait-mate
	// line the Mate or the captain then answered). The task page lists
	// decisions; both feed the decision_wait findings and WAITED.
	Kind            string `json:"kind"`
	AskedEventID    int64  `json:"asked_event_id"`
	AskedAt         string `json:"asked_at"`
	Text            string `json:"text"`
	AnsweredEventID int64  `json:"answered_event_id,omitempty"`
	AnsweredAt      string `json:"answered_at,omitempty"`
	AnsweredBy      string `json:"answered_by,omitempty"`
	Answer          string `json:"answer,omitempty"`
	WaitedMs        *int64 `json:"waited_ms"`
}

// Branch is the crew's branch and whether git still has it.
type Branch struct {
	Name   string `json:"name,omitempty"`
	Exists bool   `json:"exists"`
	// Reason is why Exists is false, or why it could not be established.
	Reason string `json:"reason,omitempty"`
}

// TaskResponse is GET /api/projects/{project}/tasks/{crew}.
type TaskResponse struct {
	envelope
	Project     string                  `json:"project"`
	Crew        string                  `json:"crew"`
	Ledger      Task                    `json:"ledger"`
	Turns       []Turn                  `json:"turns"`
	StatusLines []StatusLine            `json:"status_lines"`
	Questions   []Question              `json:"questions"`
	Branch      Branch                  `json:"branch"`
	Performance diagnostics.Performance `json:"performance"`
	// Journey is what the Journey tab reads beyond the fields above
	// (journey.go): brief, hand-back, meta, reviews, the Mate's turns about
	// this crew, incidents, the project's workflow checked.
	Journey Journey `json:"journey"`
}

// TaskSummaryResponse is GET /api/projects/{project}/tasks/{crew}?view=summary:
// TaskResponse without Turns, Branch and Performance (CON-126).
type TaskSummaryResponse struct {
	envelope
	Project     string       `json:"project"`
	Crew        string       `json:"crew"`
	Ledger      Task         `json:"ledger"`
	StatusLines []StatusLine `json:"status_lines"`
	Questions   []Question   `json:"questions"`
}

// HistoryResponse is GET /api/projects/{project}/history?since=<RFC3339>:
// every crew's status lines and decisions since a time, by crew name, for
// Agent City's tent to play back (CON-127). Crews with neither are absent.
type HistoryResponse struct {
	envelope
	Project string                  `json:"project"`
	Since   string                  `json:"since"`
	Crews   map[string]*CrewHistory `json:"crews"`
}

// CrewHistory is one crew's part of HistoryResponse: what its task page
// carries as status_lines and questions, from Since on.
type CrewHistory struct {
	StatusLines []StatusLine `json:"status_lines"`
	Questions   []Question   `json:"questions"`
}

// Action is one tool call inside a turn.
type Action struct {
	ID         string `json:"id"`
	At         string `json:"at"`
	EndedAt    string `json:"ended_at,omitempty"`
	Tool       string `json:"tool"`
	Target     string `json:"target,omitempty"`
	Summary    string `json:"summary,omitempty"`
	DurationMs *int64 `json:"duration_ms"`
	OK         *bool  `json:"ok"`
	EventID    int64  `json:"event_id,omitempty"`
	Ref        Ref    `json:"ref"`
}

// TurnResponse is GET /api/projects/{project}/tasks/{crew}/turns/{id}.
type TurnResponse struct {
	envelope
	Project string                `json:"project"`
	Crew    string                `json:"crew"`
	Turn    Turn                  `json:"turn"`
	Actions []Action              `json:"actions"`
	Events  []timeline.StoryEvent `json:"events"`
}

// DiffResponse is GET /api/projects/{project}/tasks/{crew}/diff. Text is
// `mate diff <project> <crew>` verbatim, and empty with a Reason when
// there is nothing to show.
type DiffResponse struct {
	envelope
	Project string `json:"project"`
	Crew    string `json:"crew"`
	Branch  string `json:"branch,omitempty"`
	Exists  bool   `json:"exists"`
	Text    string `json:"text"`
	Reason  string `json:"reason,omitempty"`
}

// EventsResponse is GET /api/events.
type EventsResponse struct {
	envelope
	Project string                `json:"project,omitempty"`
	Since   int64                 `json:"since"`
	Events  []timeline.StoryEvent `json:"events"`
	// Now are the `v_now` rows of the actors that moved since `since`: one
	// call refreshes both the story and the scene.
	Now []SceneRow `json:"now"`
	// TimelineStale are the projects whose timeline stopped moving: every
	// page built from it is behind the files since then (CON-139).
	TimelineStale []StaleTimeline `json:"timeline_stale,omitempty"`
}

// StaleTimeline is one project whose ingest has failed every pass since
// Since.
type StaleTimeline struct {
	Project string `json:"project"`
	Since   string `json:"since"`
	Error   string `json:"error"`
}

// NowResponse is GET /api/now: every `v_now` row of the workspace, or of
// one project with `?project=`. It is the snapshot /api/events only ever
// sends a slice of - the rows of the actors that moved - so a page that
// draws the whole scene (Mate Office, docs/dashboard.md section 11) can
// start from the full picture and then follow the deltas.
type NowResponse struct {
	envelope
	Project string     `json:"project,omitempty"`
	Now     []SceneRow `json:"now"`
}

// ErrorResponse is every 4xx and 5xx body. Reason is a sentence, not a
// code: the reader of this API is a page, and the page shows the sentence.
type ErrorResponse struct {
	envelope
	Error  string `json:"error"`
	Reason string `json:"reason,omitempty"`
}

// BoardResponse is GET /api/board: the Office's whiteboard (CON-84). Dates
// are local calendar days, oldest first, in Zone; Total is every project
// summed, one entry per date, as is each project's Days.
type BoardResponse struct {
	envelope
	Days     int            `json:"days"`
	Zone     string         `json:"zone"`
	Dates    []string       `json:"dates"`
	Total    []BoardDay     `json:"total"`
	Projects []BoardProject `json:"projects"`
}

// BoardProject is one project's days. Error says why its tasks could not
// be read; its tokens and incidents are still counted.
type BoardProject struct {
	Name  string     `json:"name"`
	Days  []BoardDay `json:"days"`
	Error string     `json:"error,omitempty"`
}

// BoardDay is one local day. Tokens are the turns started that day, Mate's
// and crews' (MateTokens and CrewTokens split Tokens.Total). Spawned counts
// tasks by role, Closed closed ships and scouts by final state, Reviews
// closed reviewers by verdict, Incidents opened incidents by kind. No cost:
// the board measures in tokens.
type BoardDay struct {
	Date       string         `json:"date"`
	Tokens     Tokens         `json:"tokens"`
	MateTokens int64          `json:"mate_tokens"`
	CrewTokens int64          `json:"crew_tokens"`
	Spawned    map[string]int `json:"spawned"`
	Closed     map[string]int `json:"closed"`
	Reviews    map[string]int `json:"reviews"`
	Incidents  map[string]int `json:"incidents"`
}

// BoardDayResponse is GET /api/board/day: one of the board's days opened
// up (CON-88). Every project is in it; the page filters by project.
// Actors are who spent tokens that day, biggest first; Tasks every task
// opened, closed or taking turns that day; Incidents the ones opened that
// day, oldest first. Errors names a project whose tasks could not be read.
type BoardDayResponse struct {
	envelope
	Date      string            `json:"date"`
	Zone      string            `json:"zone"`
	Actors    []BoardActor      `json:"actors"`
	Tasks     []BoardTask       `json:"tasks"`
	Incidents []BoardIncident   `json:"incidents"`
	Errors    map[string]string `json:"errors,omitempty"`
}

// BoardActor is one Mate or crew's turns started that day. Kind is
// timeline's actor kind, mate or crew.
type BoardActor struct {
	Project string `json:"project"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Turns   int64  `json:"turns"`
	Tokens  Tokens `json:"tokens"`
}

// BoardTask is a project page task with what it did that day: Opened
// when it was spawned that day, ClosedToday when it closed that day, and
// DayTurns and DayTokens its turns started that day. Its own Tokens stay
// the task's whole life.
type BoardTask struct {
	Project string `json:"project"`
	Task
	Opened      bool   `json:"opened"`
	ClosedToday bool   `json:"closed_today"`
	DayTurns    int64  `json:"day_turns"`
	DayTokens   Tokens `json:"day_tokens"`
}

// BoardIncident is one incident opened that day. Name is the crew's name,
// or empty for the Mate; ResolvedAt is empty while it is still open.
type BoardIncident struct {
	Project    string `json:"project"`
	Kind       string `json:"kind"`
	ActorKind  string `json:"actor_kind"`
	Name       string `json:"name"`
	OpenedAt   string `json:"opened_at"`
	ResolvedAt string `json:"resolved_at,omitempty"`
}
