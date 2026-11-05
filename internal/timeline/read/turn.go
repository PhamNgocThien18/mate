package read

import (
	"context"
	"database/sql"
	"errors"
)

// Turn is one row of `turn`: one model call, with the kind of the event
// that triggered it and its context as a percentage of the model's window.
type Turn struct {
	ID, SessionID, HarnessTurnRef string
	Ordinal                       int64
	// StartedAt and EndedAt stay nullable: a turn that has not ended has no
	// duration yet, which `mate usage` renders differently from one that
	// ended at its start.
	StartedAt, EndedAt                             sql.NullString
	TriggerEventID                                 int64
	TriggerKind, Outcome, Model, Effort            string
	Input, CacheRead, CacheWrite, Output, Thinking int64
	ContextAfter, ToolCount                        int64
	RefPath                                        string
	RefOffset                                      int64
	ContextPct                                     sql.NullFloat64
}

// Turns reads an actor's turns in the order they happened. The trigger's
// kind is a join onto `event`: `turn` records only `trigger_event_id`.
func Turns(ctx context.Context, q Querier, actorID string) ([]Turn, error) {
	return collect(ctx, q, func(rows *sql.Rows, t *Turn) error {
		var triggerKind, outcome, model, effort sql.NullString
		var trigger sql.NullInt64
		if err := rows.Scan(&t.ID, &t.SessionID, &t.HarnessTurnRef, &t.Ordinal,
			&t.StartedAt, &t.EndedAt, &trigger, &triggerKind,
			&outcome, &model, &effort, &t.Input, &t.CacheRead, &t.CacheWrite,
			&t.Output, &t.Thinking, &t.ContextAfter, &t.ToolCount,
			&t.RefPath, &t.RefOffset, &t.ContextPct); err != nil {
			return err
		}
		t.TriggerEventID, t.TriggerKind = trigger.Int64, triggerKind.String
		t.Outcome, t.Model, t.Effort = outcome.String, model.String, effort.String
		return nil
	}, `
		SELECT u.id, u.session_id, u.harness_turn_ref, u.ordinal,
		       u.started_at, u.ended_at, u.trigger_event_id, e.kind,
		       u.outcome, u.model, u.effort, u.input_tokens, u.cache_read_tokens, u.cache_write_tokens,
		       u.output_tokens, u.thinking_tokens, u.context_tokens_after, u.tool_count,
		       u.ref_path, u.ref_offset,
		       CASE WHEN p.context_window > 0
		            THEN 100.0 * u.context_tokens_after / p.context_window ELSE NULL END
		  FROM turn u
		  LEFT JOIN event e ON e.id = u.trigger_event_id
		  LEFT JOIN pricing p ON p.model = u.model
		 WHERE u.actor_id = ?
		 ORDER BY u.started_at, u.ordinal`, actorID)
}

// TurnTotals is an actor's turn count and token buckets, summed off `turn`.
type TurnTotals struct {
	Turns, Input, CacheRead, CacheWrite, Output, Thinking int64
}

// ActorTotals sums an actor's turns. There is no task row for a Mate, so
// `v_task_ledger` cannot answer for one and this does.
func ActorTotals(ctx context.Context, q Querier, actorID string) (TurnTotals, error) {
	var t TurnTotals
	err := q.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(cache_read_tokens),0),
		       COALESCE(SUM(cache_write_tokens),0), COALESCE(SUM(output_tokens),0),
		       COALESCE(SUM(thinking_tokens),0)
		  FROM turn WHERE actor_id = ?`, actorID).
		Scan(&t.Turns, &t.Input, &t.CacheRead, &t.CacheWrite, &t.Output, &t.Thinking)
	return t, err
}

// ActorCost is an actor's cost, computed the way `v_task_ledger` computes a
// task's, including the guard that a pricing row of all zeroes is not a
// price (docs/timeline.md section 10). No priced turn is a NULL cost.
func ActorCost(ctx context.Context, q Querier, actorID string) (sql.NullFloat64, error) {
	var cost sql.NullFloat64
	err := q.QueryRowContext(ctx, `
		SELECT SUM(
			u.input_tokens       * COALESCE(p.input_per_m, 0) / 1000000.0 +
			u.cache_read_tokens  * COALESCE(p.cache_read_per_m, 0) / 1000000.0 +
			u.cache_write_tokens * COALESCE(p.cache_write_per_m, 0) / 1000000.0 +
			u.output_tokens      * COALESCE(p.output_per_m, 0) / 1000000.0)
		  FROM turn u JOIN pricing p ON p.model = u.model
		 WHERE u.actor_id = ?
		   AND (p.input_per_m > 0 OR p.cache_read_per_m > 0 OR p.cache_write_per_m > 0 OR p.output_per_m > 0)`,
		actorID).Scan(&cost)
	return cost, err
}

// LastModel is the model and effort of an actor's most recent turn, both ""
// when it has none.
func LastModel(ctx context.Context, q Querier, actorID string) (model, effort string, err error) {
	err = q.QueryRowContext(ctx, `
		SELECT COALESCE(model, ''), COALESCE(effort, '') FROM turn WHERE actor_id = ?
		 ORDER BY started_at DESC, ordinal DESC LIMIT 1`, actorID).Scan(&model, &effort)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", nil
	}
	return model, effort, err
}

// ContextStats is the average and the largest context of an actor's turns
// that measured one, both NULL when none did.
type ContextStats struct {
	Avg, Max sql.NullInt64
}

// ActorContext reads ContextStats for one actor.
func ActorContext(ctx context.Context, q Querier, actorID string) (ContextStats, error) {
	var s ContextStats
	err := q.QueryRowContext(ctx, `
		SELECT CAST(ROUND(AVG(context_tokens_after)) AS INTEGER), MAX(context_tokens_after)
		  FROM turn WHERE actor_id = ? AND context_tokens_after > 0`, actorID).
		Scan(&s.Avg, &s.Max)
	return s, err
}

// ToolCount sums `turn.tool_count` for an actor.
func ToolCount(ctx context.Context, q Querier, actorID string) (int64, error) {
	var n int64
	err := q.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(tool_count),0) FROM turn WHERE actor_id = ?`, actorID).Scan(&n)
	return n, err
}

// ToolCounts is ToolCount for every actor of a project in one query, keyed
// by actor id. An actor with no turn has no key.
func ToolCounts(ctx context.Context, q Querier, project string) (map[string]int64, error) {
	type count struct {
		id string
		n  int64
	}
	rows, err := collect(ctx, q, func(rows *sql.Rows, c *count) error {
		return rows.Scan(&c.id, &c.n)
	}, `
		SELECT t.actor_id, COALESCE(SUM(t.tool_count),0)
		  FROM turn t JOIN actor a ON a.id = t.actor_id
		 WHERE a.project = ?
		 GROUP BY t.actor_id`, project)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(rows))
	for _, c := range rows {
		out[c.id] = c.n
	}
	return out, nil
}

// Session is one harness session of an actor, summed over its turns.
type Session struct {
	CrewSession, Turns, Input, Cached, Output, ContextMax int64
	StartedAt, EndedAt                                    sql.NullString
}

// Sessions reads an actor's sessions in the order they started.
func Sessions(ctx context.Context, q Querier, actorID string) ([]Session, error) {
	return collect(ctx, q, func(rows *sql.Rows, s *Session) error {
		return rows.Scan(&s.CrewSession, &s.Turns, &s.Input, &s.Cached, &s.Output, &s.ContextMax,
			&s.StartedAt, &s.EndedAt)
	}, `
		SELECT s.crew_session, COUNT(*),
		       COALESCE(SUM(u.input_tokens), 0), COALESCE(SUM(u.cache_read_tokens + u.cache_write_tokens), 0),
		       COALESCE(SUM(u.output_tokens), 0), COALESCE(MAX(u.context_tokens_after), 0),
		       MIN(u.started_at), MAX(COALESCE(u.ended_at, u.started_at))
		  FROM turn u JOIN session s ON s.id = u.session_id
		 WHERE u.actor_id = ?
		 GROUP BY s.id ORDER BY MIN(u.started_at), s.id`, actorID)
}

// TurnTokens is one turn's tokens with the actor it belongs to and its time
// (`started_at`, or `ended_at` when it has no start).
type TurnTokens struct {
	ActorID, Project, Kind, Name, At               string
	Input, CacheRead, CacheWrite, Output, Thinking int64
}

// TurnTokensBetween reads every turn of the workspace at or after since and,
// when until is not "", before until.
func TurnTokensBetween(ctx context.Context, q Querier, since, until string) ([]TurnTokens, error) {
	query := `
		SELECT a.id, a.project, a.kind, a.name, COALESCE(t.started_at, t.ended_at),
		       t.input_tokens, t.cache_read_tokens, t.cache_write_tokens, t.output_tokens, t.thinking_tokens
		  FROM turn t JOIN actor a ON a.id = t.actor_id
		 WHERE COALESCE(t.started_at, t.ended_at) >= ?`
	args := []any{since}
	if until != "" {
		query += ` AND COALESCE(t.started_at, t.ended_at) < ?`
		args = append(args, until)
	}
	return collect(ctx, q, func(rows *sql.Rows, t *TurnTokens) error {
		return rows.Scan(&t.ActorID, &t.Project, &t.Kind, &t.Name, &t.At,
			&t.Input, &t.CacheRead, &t.CacheWrite, &t.Output, &t.Thinking)
	}, query, args...)
}
