package read

import (
	"context"
	"database/sql"
	"errors"
)

// Task is one row of `v_task_ledger`: what a crew's task cost and how it
// went.
type Task struct {
	CrewActorID, Crew, Text                        string
	Branch, SpawnedAt, ClosedAt, CloseState        string
	QuestionCount, HandbackCount, Turns            int64
	Input, CacheRead, CacheWrite, Output, Thinking int64
	ContextTokensLast                              int64
	LastModel                                      string
	ContextPct                                     sql.NullFloat64
	WaitedMs                                       int64
	Cost                                           sql.NullFloat64
	MergedAt                                       string
}

// Tasks reads `v_task_ledger` for a project, oldest spawn first.
func Tasks(ctx context.Context, q Querier, project string) ([]Task, error) {
	return collect(ctx, q, func(rows *sql.Rows, t *Task) error {
		var branch, spawnedAt, closedAt, closeState, lastModel, mergedAt sql.NullString
		var ctxLast sql.NullInt64
		if err := rows.Scan(&t.CrewActorID, &t.Crew, &t.Text, &branch, &spawnedAt, &closedAt, &closeState,
			&t.QuestionCount, &t.HandbackCount, &t.Turns,
			&t.Input, &t.CacheRead, &t.CacheWrite, &t.Output, &t.Thinking,
			&ctxLast, &lastModel, &t.ContextPct, &t.WaitedMs, &t.Cost, &mergedAt); err != nil {
			return err
		}
		t.Branch, t.SpawnedAt, t.ClosedAt = branch.String, spawnedAt.String, closedAt.String
		t.CloseState, t.LastModel, t.MergedAt = closeState.String, lastModel.String, mergedAt.String
		t.ContextTokensLast = ctxLast.Int64
		return nil
	}, `
		SELECT crew_actor_id, crew, text, branch, spawned_at, closed_at, close_state,
		       question_count, handback_count, turns,
		       input_tokens, cache_read_tokens, cache_write_tokens, output_tokens, thinking_tokens,
		       context_tokens_last, last_model, context_pct, waited_ms, cost, merged_at
		  FROM v_task_ledger WHERE project = ? ORDER BY spawned_at, crew`, project)
}

// UsageTask is a `v_task_ledger` row with what `mate usage` adds from the
// crew's turns: the effort of its latest turn and the spread of its context.
type UsageTask struct {
	Crew                                string
	Turns, Input, CacheRead, CacheWrite int64
	Output                              int64
	Cost, ContextPct                    sql.NullFloat64
	ContextTokens                       sql.NullInt64
	Questions, WaitedMs                 int64
	SpawnedAt, ClosedAt                 sql.NullString
	Model, Effort                       string
	ContextAvg, ContextMax              sql.NullInt64
	ContextSum, ContextCalls            int64
}

// UsageTasks reads UsageTask for every task of a project, oldest spawn
// first.
func UsageTasks(ctx context.Context, q Querier, project string) ([]UsageTask, error) {
	return collect(ctx, q, func(rows *sql.Rows, t *UsageTask) error {
		return rows.Scan(&t.Crew, &t.Turns, &t.Input, &t.CacheRead, &t.CacheWrite, &t.Output,
			&t.Cost, &t.ContextPct, &t.ContextTokens, &t.Questions, &t.WaitedMs, &t.SpawnedAt, &t.ClosedAt,
			&t.Model, &t.Effort, &t.ContextAvg, &t.ContextMax, &t.ContextSum, &t.ContextCalls)
	}, `
		SELECT crew, turns, input_tokens, cache_read_tokens, cache_write_tokens, output_tokens,
		       cost, context_pct, context_tokens_last, question_count, waited_ms, spawned_at, closed_at,
		       COALESCE(last_model, ''),
		       COALESCE((SELECT u.effort FROM turn u WHERE u.actor_id = l.crew_actor_id
		                  ORDER BY u.started_at DESC, u.ordinal DESC LIMIT 1), ''),
		       (SELECT CAST(ROUND(AVG(u.context_tokens_after)) AS INTEGER) FROM turn u
		         WHERE u.actor_id = l.crew_actor_id AND u.context_tokens_after > 0),
		       (SELECT MAX(u.context_tokens_after) FROM turn u
		         WHERE u.actor_id = l.crew_actor_id AND u.context_tokens_after > 0),
		       (SELECT COALESCE(SUM(u.context_tokens_after), 0) FROM turn u
		         WHERE u.actor_id = l.crew_actor_id AND u.context_tokens_after > 0),
		       (SELECT COUNT(*) FROM turn u
		         WHERE u.actor_id = l.crew_actor_id AND u.context_tokens_after > 0)
		  FROM v_task_ledger l WHERE project = ? ORDER BY spawned_at`, project)
}

// TaskTokens is one task's token total (input, cache and output), its cost
// and its latest context percentage.
type TaskTokens struct {
	Total            int64
	Cost, ContextPct sql.NullFloat64
}

// TaskTokensOf reads TaskTokens for one crew. A crew with no ledger row is
// sql.ErrNoRows.
func TaskTokensOf(ctx context.Context, q Querier, crewActorID string) (TaskTokens, error) {
	var t TaskTokens
	err := q.QueryRowContext(ctx, `
		SELECT COALESCE(input_tokens,0)+COALESCE(cache_read_tokens,0)+COALESCE(cache_write_tokens,0)+COALESCE(output_tokens,0),
		       cost, context_pct
		  FROM v_task_ledger WHERE crew_actor_id = ?`, crewActorID).Scan(&t.Total, &t.Cost, &t.ContextPct)
	return t, err
}

// TaskWorktree is the worktree a crew's task row records, "" when there is
// no task row.
func TaskWorktree(ctx context.Context, q Querier, crewActorID string) (string, error) {
	var worktree string
	err := q.QueryRowContext(ctx, `SELECT worktree FROM task WHERE crew_actor_id=?`, crewActorID).Scan(&worktree)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return worktree, err
}

// Now is one row of `v_now` with the target's display name and the context
// of the actor's latest turn: as a percentage of the model's window, and in
// tokens.
type Now struct {
	ActorID, ActorName, ActorKind, Project string
	State, Since, Target, Detail           string
	TokensToday                            int64
	ContextPct                             sql.NullFloat64
	ContextTokens                          sql.NullInt64
}

// NowRows reads `v_now` for one project.
func NowRows(ctx context.Context, q Querier, project string) ([]Now, error) {
	return collect(ctx, q, func(rows *sql.Rows, r *Now) error {
		var state, since, target, detail sql.NullString
		if err := rows.Scan(&r.ActorID, &r.ActorName, &r.ActorKind, &r.Project,
			&state, &since, &target, &detail, &r.TokensToday, &r.ContextPct, &r.ContextTokens); err != nil {
			return err
		}
		r.State, r.Since, r.Target, r.Detail = state.String, since.String, target.String, detail.String
		return nil
	}, `
		SELECT n.actor_id, n.actor_name, n.actor_kind, n.project, n.state, n.since,
		       t.name, n.detail, n.tokens_today,
                   (SELECT CASE WHEN p.context_window > 0 THEN 100.0 * u.context_tokens_after / p.context_window END
                    FROM turn u LEFT JOIN pricing p ON p.model=u.model WHERE u.actor_id=n.actor_id ORDER BY u.started_at DESC, u.ordinal DESC LIMIT 1),
		       (SELECT u.context_tokens_after FROM turn u WHERE u.actor_id = n.actor_id ORDER BY u.started_at DESC, u.ordinal DESC LIMIT 1)
		  FROM v_now n LEFT JOIN actor t ON t.id = n.target_actor_id
		 WHERE n.project = ?`, project)
}

// Today is an actor's `v_now` tokens_today and context_pct.
type Today struct {
	Tokens     int64
	ContextPct sql.NullFloat64
}

// TodayOf reads Today for one actor. An actor `v_now` has no row for is
// sql.ErrNoRows.
func TodayOf(ctx context.Context, q Querier, actorID string) (Today, error) {
	var t Today
	err := q.QueryRowContext(ctx, `
		SELECT COALESCE(tokens_today,0), context_pct FROM v_now WHERE actor_id = ?`,
		actorID).Scan(&t.Tokens, &t.ContextPct)
	return t, err
}

// MateContextPct is the context percentage `v_now` holds for a project's
// Mate, NULL when there is no Mate row or it has not been measured.
func MateContextPct(ctx context.Context, q Querier, project string) (sql.NullFloat64, error) {
	var pct sql.NullFloat64
	err := q.QueryRowContext(ctx, `SELECT n.context_pct FROM v_now n WHERE n.project = ? AND n.actor_kind = 'mate' LIMIT 1`, project).Scan(&pct)
	if errors.Is(err, sql.ErrNoRows) {
		return sql.NullFloat64{}, nil
	}
	return pct, err
}

// MovedActors are the actors of a project with a transition recorded by an
// event past since.
func MovedActors(ctx context.Context, q Querier, project string, since int64) ([]string, error) {
	return collect(ctx, q, func(rows *sql.Rows, id *string) error {
		return rows.Scan(id)
	}, `SELECT DISTINCT actor_id FROM transition WHERE project = ? AND event_id > ?`, project, since)
}
