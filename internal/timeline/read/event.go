package read

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// LastEventID is `MAX(event.id)` over the whole workspace, 0 for an empty
// database.
func LastEventID(ctx context.Context, q Querier) (int64, error) {
	var id sql.NullInt64
	if err := q.QueryRowContext(ctx, `SELECT MAX(id) FROM event`).Scan(&id); err != nil {
		return 0, err
	}
	return id.Int64, nil
}

// ToolAgent is one event of kind `tool.called` whose payload names the
// agent the tool call started.
type ToolAgent struct {
	Project, ActorID, Agent, At string
}

// ToolAgents reads the events of the given kind with an id in (after, upTo]
// whose payload carries an `agent`.
func ToolAgents(ctx context.Context, q Querier, after, upTo int64, kind string) ([]ToolAgent, error) {
	// `+kind` keeps the planner on the id range rather than the kind
	// index, which would read every tool call ever recorded again.
	return collect(ctx, q, func(rows *sql.Rows, a *ToolAgent) error {
		return rows.Scan(&a.Project, &a.ActorID, &a.Agent, &a.At)
	}, `
			SELECT project, actor_id, json_extract(payload, '$.agent'), at
			  FROM event
			 WHERE id > ? AND id <= ? AND +kind = ? AND payload LIKE '%"agent":%'
			   AND json_extract(payload, '$.agent') IS NOT NULL`, after, upTo, kind)
}

// Event is an event row with what a reader of its payload needs.
type Event struct {
	ActorID          string
	ID               int64
	At, TurnID, Kind string
	Payload, RefPath string
	RefOffset        int64
}

func scanEvent(rows *sql.Rows, e *Event) error {
	var turnID sql.NullString
	if err := rows.Scan(&e.ActorID, &e.ID, &e.At, &turnID, &e.Kind, &e.Payload,
		&e.RefPath, &e.RefOffset); err != nil {
		return err
	}
	e.TurnID = turnID.String
	return nil
}

// ActorEventsOfKind reads one actor's events of one kind, in story order.
func ActorEventsOfKind(ctx context.Context, q Querier, actorID, kind string) ([]Event, error) {
	return collect(ctx, q, scanEvent, `
		SELECT actor_id, id, at, turn_id, kind, payload, ref_path, ref_offset
		  FROM event WHERE actor_id = ? AND kind = ? ORDER BY at, id`,
		actorID, kind)
}

// ProjectEventsOfKind reads the events of one kind in a project at or after
// since, of the actors whose id starts with actorPrefix, in story order.
func ProjectEventsOfKind(ctx context.Context, q Querier, project, since, kind, actorPrefix string) ([]Event, error) {
	return collect(ctx, q, scanEvent, `
		SELECT actor_id, id, at, turn_id, kind, payload, ref_path, ref_offset
		  FROM event WHERE project = ? AND at >= ? AND +kind = ? AND actor_id LIKE ? ESCAPE '\'
		 ORDER BY at, id`,
		project, since, kind, likePrefix(actorPrefix))
}

// ActorEventsOfKinds reads one actor's events in a project whose kind is
// one of kinds, in story order.
func ActorEventsOfKinds(ctx context.Context, q Querier, project, actorID string, kinds ...string) ([]Event, error) {
	if len(kinds) == 0 {
		return nil, nil
	}
	args := []any{project, actorID}
	for _, k := range kinds {
		args = append(args, k)
	}
	return collect(ctx, q, scanEvent, `
		SELECT actor_id, id, at, turn_id, kind, payload, ref_path, ref_offset FROM event
		 WHERE project=? AND actor_id=? AND kind IN (`+placeholders(len(kinds))+`)
		 ORDER BY at,id`, args...)
}

// TelemetryEvents reads one actor's `telemetry.*` events in id order.
func TelemetryEvents(ctx context.Context, q Querier, actorID string) ([]Event, error) {
	return collect(ctx, q, scanEvent, `SELECT actor_id,id,at,turn_id,kind,payload,ref_path,ref_offset FROM event WHERE actor_id=? AND kind LIKE 'telemetry.%' ORDER BY id`, actorID)
}

// TelemetryCursor is what the native observer last recorded for an actor:
// when, and the last parser error if any.
type TelemetryCursor struct {
	ObservedAt string
	Error      sql.NullString
}

// TelemetryCursorOf reads TelemetryCursor for one actor. ok is false when
// the database predates the `telemetry_cursor` table: an old read-only
// database is read as it is, never migrated.
func TelemetryCursorOf(ctx context.Context, q Querier, actorID string) (TelemetryCursor, bool, error) {
	var out TelemetryCursor
	var table int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='telemetry_cursor'`).Scan(&table); err != nil {
		return out, false, err
	}
	if table == 0 {
		return out, false, nil
	}
	var observed sql.NullString
	if err := q.QueryRowContext(ctx, `SELECT MAX(observed_at),MAX(NULLIF(error,'')) FROM telemetry_cursor WHERE actor_id=?`, actorID).Scan(&observed, &out.Error); err != nil {
		return out, false, err
	}
	out.ObservedAt = observed.String
	return out, true, nil
}

// Counts is how many events, turns and actions a project has recorded.
type Counts struct {
	Events, Turns, Actions int
}

// ProjectCounts reads Counts for one project.
func ProjectCounts(ctx context.Context, q Querier, project string) (Counts, error) {
	var c Counts
	err := q.QueryRowContext(ctx,
		`SELECT
			   (SELECT COUNT(*) FROM event WHERE project = ?),
			   (SELECT COUNT(*) FROM turn t JOIN actor a ON a.id = t.actor_id WHERE a.project = ?),
			   (SELECT COUNT(*) FROM action c JOIN actor a ON a.id = c.actor_id WHERE a.project = ?)`,
		project, project, project).Scan(&c.Events, &c.Turns, &c.Actions)
	return c, err
}

// Actor is what `actor` records of one actor beyond its name: the harness
// it runs, when it was first seen and when it went.
type Actor struct {
	Harness, FirstSeen, GoneAt string
}

// ActorOf reads Actor for one id; ok is false when there is no such actor.
func ActorOf(ctx context.Context, q Querier, actorID string) (Actor, bool, error) {
	var out Actor
	var harness, firstSeen, goneAt sql.NullString
	err := q.QueryRowContext(ctx,
		`SELECT harness, first_seen, gone_at FROM actor WHERE id = ?`, actorID).
		Scan(&harness, &firstSeen, &goneAt)
	if errors.Is(err, sql.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	out.Harness, out.FirstSeen, out.GoneAt = harness.String, firstSeen.String, goneAt.String
	return out, true, nil
}

// Incident is one row of `incident` with the kind and name of its actor.
type Incident struct {
	Project, Kind, ActorKind, ActorName, OpenedAt, ResolvedAt string
}

// Incidents reads the incidents of the workspace opened at or after since
// and, when until is not "", before until, oldest first.
func Incidents(ctx context.Context, q Querier, since, until string) ([]Incident, error) {
	query := `
		SELECT i.project, i.kind, COALESCE(a.kind, ''), COALESCE(a.name, ''), i.opened_at, COALESCE(i.resolved_at, '')
		  FROM incident i LEFT JOIN actor a ON a.id = i.actor_id
		 WHERE i.opened_at >= ?`
	args := []any{since}
	if until != "" {
		query += ` AND i.opened_at < ?`
		args = append(args, until)
	}
	query += `
		 ORDER BY i.opened_at, i.id`
	return collect(ctx, q, func(rows *sql.Rows, in *Incident) error {
		return rows.Scan(&in.Project, &in.Kind, &in.ActorKind, &in.ActorName, &in.OpenedAt, &in.ResolvedAt)
	}, query, args...)
}

// likePrefix is a LIKE pattern, with `\` as the escape, matching strings
// that start with prefix.
func likePrefix(prefix string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(prefix) + "%"
}

// placeholders is n comma-separated `?`.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
