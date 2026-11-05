package core

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/nguyenngocanh94/mate/internal/autopilot"
	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/timeline"
	timelineread "github.com/nguyenngocanh94/mate/internal/timeline/read"
	"github.com/nguyenngocanh94/mate/internal/watch"
)

// What a snapshot is assembled from (CON-208): query.LoadLive reads files and
// Herdr's word about which agents are up, and the functions below put in
// what only the engine's loops know - recovery, the observer's readings, the
// timeline's staleness, the daemon's state - and the token ledger. They
// lived beside the console in cmd/mate until the engine computed the one
// snapshot every UI reads.

// livenessBound caps the Herdr question a snapshot asks, so a wedged server
// slows one read and never freezes the UIs.
const livenessBound = 3 * time.Second

// liveness asks Herdr which agents are up, for query.LoadLive. It never
// starts a server; a question that cannot be asked is left unasked and the
// rows keep their recorded state. It was the console's consoleLiveness.
func liveness(ctx context.Context, ws *store.Workspace, deps spawn.Deps) query.Liveness {
	if deps.Runtime == nil {
		return query.Liveness{}
	}
	spec, err := spawn.SessionSpec(deps, ws)
	if err != nil {
		return query.Liveness{}
	}
	ctx, cancel := context.WithTimeout(ctx, livenessBound)
	defer cancel()
	return query.ReadLiveness(ctx, deps.Runtime, spec)
}

// withCrewHealth puts the observer's latest readings into a snapshot
// query.Load built out of files alone.
//
// A crew with no reading keeps the Absent field Load gave it: "the observer
// has not looked at this crew" is a different sentence from any observation,
// and the row must not borrow a neighbour's or fall back to a zero value
// that would render as "agent gone".
func withCrewHealth(snap query.Snapshot, health map[watch.CrewRef]watch.Health) query.Snapshot {
	if len(health) == 0 {
		return snap
	}
	for p := range snap.Projects {
		project := &snap.Projects[p]
		for c := range project.Crews {
			crew := &project.Crews[c]
			h, ok := health[watch.CrewRef{Project: project.ProjectID, Crew: crew.CrewID}]
			if !ok {
				continue
			}
			crew.Health = query.KnownField(crewHealth(h))
		}
	}
	return snap
}

// withTimelineStale says on the status line which projects' timeline has
// stopped moving (CON-139): the tree is read from files and stays current,
// but the dashboard, budgets and no-turn checks read the timeline.
func withTimelineStale(snap query.Snapshot, ingest *timeline.Ingester) query.Snapshot {
	if ingest == nil {
		return snap
	}
	for _, st := range ingest.Stale() {
		if snap.Runtime.Notice != "" {
			snap.Runtime.Notice += "; "
		}
		snap.Runtime.Notice += fmt.Sprintf("timeline %s stale since %s: %s",
			st.Project, st.Since.Local().Format("15:04"), st.Error)
	}
	return snap
}

// withRuntimeNotice puts the observer's standing word about the terminal
// runtime into a snapshot query.Load built out of files alone. A UI cannot
// ask Herdr itself, so an empty notice is the only way it can tell "nothing
// was observed" apart from "nothing could be observed"; without it every
// file-read row keeps looking fresh while Herdr is down.
func withRuntimeNotice(snap query.Snapshot, watcher CrewHealth) query.Snapshot {
	if watcher == nil {
		return snap
	}
	notice, at := watcher.RuntimeNotice()
	if notice == "" {
		return snap
	}
	snap.Runtime = query.RuntimeStatus{Notice: notice, At: at}
	return snap
}

// withAutoStatus puts the daemon's own state into a snapshot query.Load built
// out of files alone.
//
// A project the daemon has never ticked keeps the zero AutoDaemon, which
// renders as the bare mode word: "this console has sent nothing for it" is
// the truth, and it is what the zero value already says.
func withAutoStatus(snap query.Snapshot, status map[string]autopilot.Status) query.Snapshot {
	if len(status) == 0 {
		return snap
	}
	for p := range snap.Projects {
		project := &snap.Projects[p]
		s, ok := status[project.ProjectID]
		if !ok {
			continue
		}
		project.Daemon = query.AutoDaemon{
			Sends:      s.Sends,
			LastSentAt: s.LastSentAt,
			Notice:     s.Notice,
			NoticeAt:   s.NoticeAt,
		}
	}
	return snap
}

// crewHealth converts one observation into the read model's DTO. The
// composer word travels as itself: internal/ui/console may not import
// internal/send, and an unmeasured harness's state must render as whatever
// it was called rather than as a blank.
func crewHealth(h watch.Health) query.CrewHealth {
	return query.CrewHealth{
		AgentPresent: h.AgentPresent,
		Composer:     composerDTO(h.Composer),
		QuietFor:     h.QuietFor,
		ComposerFor:  h.ComposerFor,
		ObservedAt:   h.ObservedAt,
		Source:       h.Source,
	}
}

func composerDTO(state send.ComposerState) query.CrewComposer {
	switch state {
	case send.StateEmpty:
		return query.ComposerEmpty
	case send.StatePending:
		return query.ComposerPending
	case send.StateBusy:
		return query.ComposerBusy
	default:
		return query.ComposerUnknown
	}
}

// withTokens puts each row's token usage into a snapshot query.Load built
// out of files alone (mvp.md M5 task 27): it opens `.mate/mate.db`
// read-only - a reader never needs the writer's lock to read a ledger the
// observer already committed - and fills CrewNode.Tokens from
// `v_task_ledger` and MateNode.Tokens from `v_now`.
//
// A workspace with no database yet (no console has ever ingested it) is not
// an error: every row simply keeps the Absent Load already gave it, the
// same as a crew's health in withCrewHealth.
func withTokens(snap query.Snapshot, ws *store.Workspace) query.Snapshot {
	handle, err := db.OpenRead(ws)
	if err != nil {
		return snap
	}
	defer handle.Close()

	for p := range snap.Projects {
		project := &snap.Projects[p]
		if tok, ok := mateTokens(handle, project.ProjectID); ok {
			meta, err := ws.ReadMateMeta(project.ProjectID)
			if err == nil && meta[spawn.MetaSessionID] != "" {
				tok.ContextTokens, tok.ContextPct = nil, nil
				if usage, known, err := handle.LatestContext(context.Background(), timeline.MateActorID(project.ProjectID), meta[spawn.MetaSessionID]); err == nil && known {
					tok.ContextTokens, tok.ContextPct = &usage.Tokens, usage.Pct
				}
			}
			project.Mate.Tokens = query.KnownField(tok)
		}
		for c := range project.Crews {
			crew := &project.Crews[c]
			if tok, ok := crewTokens(handle, project.ProjectID, crew.CrewID); ok {
				crew.Tokens = query.KnownField(tok)
			}
		}
	}
	return snap
}

// crewTokens reads one crew's row of `v_task_ledger`. No row (the crew has
// no turn recorded yet, or the timeline has not ingested it) reports ok=
// false, which leaves the Absent field Load gave it rather than a Known
// zero that would render as "no tokens spent".
func crewTokens(handle *db.DB, project, crew string) (query.TokenValue, bool) {
	actorID := timeline.CrewActorID(project, crew)
	t, err := timelineread.TaskTokensOf(context.Background(), handle.SQL(), actorID)
	if err != nil {
		return query.TokenValue{}, false
	}
	return tokenContext(handle, actorID, tokenValue(t.Total, t.Cost, t.ContextPct)), true
}

// mateTokens reads the Mate's row of `v_now`: tokens_today and
// context_pct. There is no per-task ledger for a Mate - its turns belong to
// the project rather than to any one task (docs/timeline.md) - so "today"
// is the best whole-task-shaped number `v_now` offers, and cost is left nil
// rather than approximated from it: a day boundary is not a task boundary,
// and a wrong-looking dollar figure is worse than none.
func mateTokens(handle *db.DB, project string) (query.TokenValue, bool) {
	actorID := timeline.MateActorID(project)
	t, err := timelineread.TodayOf(context.Background(), handle.SQL(), actorID)
	if err != nil {
		return query.TokenValue{}, false
	}
	return tokenContext(handle, actorID, tokenValue(t.Tokens, sql.NullFloat64{}, t.ContextPct)), true
}

func tokenValue(total int64, cost, contextPct sql.NullFloat64) query.TokenValue {
	v := query.TokenValue{Total: total}
	if cost.Valid {
		c := cost.Float64
		v.Cost = &c
	}
	if contextPct.Valid {
		p := contextPct.Float64
		v.ContextPct = &p
	}
	return v
}

func tokenContext(handle *db.DB, actorID string, value query.TokenValue) query.TokenValue {
	if c, ok, err := handle.LatestContext(context.Background(), actorID, ""); err == nil && ok {
		value.ContextTokens, value.ContextPct = &c.Tokens, c.Pct
	}
	return value
}
