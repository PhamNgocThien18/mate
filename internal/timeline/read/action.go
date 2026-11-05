package read

import (
	"context"
	"database/sql"
)

// Action is one tool call. `action` carries no source ref of its own - the
// schema puts the locator on `event` - so the ref is the one of the
// `tool.called` event the row was written beside, through `action.event_id`.
type Action struct {
	ID, TurnID, SessionID string
	At, EndedAt           string
	Tool, Target, Summary string
	DurationMs            sql.NullInt64
	OK                    sql.NullBool
	EventID               int64
	RefPath               string
	RefOffset             int64
}

const actionSelect = `
		SELECT a.id, COALESCE(a.turn_id,''), COALESCE(t.session_id,''), a.at, a.ended_at,
		       a.tool, a.target, a.summary, a.duration_ms, a.ok,
		       a.event_id, COALESCE(e.ref_path,''), COALESCE(e.ref_offset,0)
		  FROM action a LEFT JOIN turn t ON t.id = a.turn_id LEFT JOIN event e ON e.id = a.event_id`

func scanAction(rows *sql.Rows, a *Action) error {
	var endedAt sql.NullString
	var eventID sql.NullInt64
	if err := rows.Scan(&a.ID, &a.TurnID, &a.SessionID, &a.At, &endedAt,
		&a.Tool, &a.Target, &a.Summary, &a.DurationMs, &a.OK,
		&eventID, &a.RefPath, &a.RefOffset); err != nil {
		return err
	}
	a.EndedAt, a.EventID = endedAt.String, eventID.Int64
	return nil
}

// TurnActions reads the tool calls of one turn in the order they ran.
func TurnActions(ctx context.Context, q Querier, turnID string) ([]Action, error) {
	return collect(ctx, q, scanAction, actionSelect+`
		 WHERE a.turn_id = ? ORDER BY a.at, a.id`, turnID)
}

// ActorActions reads every tool call of one actor in the order they ran,
// leaving out the `thinking` rows, which are not tool calls.
func ActorActions(ctx context.Context, q Querier, actorID string) ([]Action, error) {
	return collect(ctx, q, scanAction, actionSelect+`
		 WHERE a.actor_id = ? AND a.tool <> 'thinking' ORDER BY a.at, a.id`, actorID)
}
