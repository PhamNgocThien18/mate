package read

import (
	"context"
	"database/sql"
)

// Question is one row of `question` with its answer joined on: the
// answerer's display name from `actor`, the line as sent from `message`, and
// the answering event's payload for when there is no `message` row.
type Question struct {
	CrewActorID, ID, Kind string
	AskedEventID          int64
	AskedAt, Text         string
	// AnsweredEventID is NULL while the question is open.
	AnsweredEventID                         sql.NullInt64
	AnsweredAt, AnsweredBy, Answer, Payload string
	WaitedMs                                sql.NullInt64
}

const questionSelect = `
		SELECT q.crew_actor_id, q.id, q.kind, q.asked_event_id, q.asked_at, q.text, q.answered_event_id, q.answered_at,
		       a.name, m.text, e.payload, q.waited_ms
		  FROM question q
		  LEFT JOIN actor a ON a.id = q.answered_by_actor_id
		  LEFT JOIN message m ON m.event_id = q.answered_event_id
		  LEFT JOIN event e ON e.id = q.answered_event_id`

func scanQuestion(rows *sql.Rows, q *Question) error {
	var answeredAt, answeredBy, answer, payload sql.NullString
	if err := rows.Scan(&q.CrewActorID, &q.ID, &q.Kind, &q.AskedEventID, &q.AskedAt, &q.Text,
		&q.AnsweredEventID, &answeredAt, &answeredBy, &answer, &payload, &q.WaitedMs); err != nil {
		return err
	}
	q.AnsweredAt, q.AnsweredBy, q.Answer, q.Payload = answeredAt.String, answeredBy.String, answer.String, payload.String
	return nil
}

// CrewQuestions reads what one crew asked, oldest first.
func CrewQuestions(ctx context.Context, q Querier, crewActorID string) ([]Question, error) {
	return collect(ctx, q, scanQuestion, questionSelect+`
		 WHERE q.crew_actor_id = ? ORDER BY q.asked_at, q.id`, crewActorID)
}

// QuestionsSince reads what the crews whose actor id starts with
// actorPrefix asked at or after since, oldest first.
func QuestionsSince(ctx context.Context, q Querier, actorPrefix, since string) ([]Question, error) {
	return collect(ctx, q, scanQuestion, questionSelect+`
		 WHERE q.crew_actor_id LIKE ? ESCAPE '\' AND q.asked_at >= ? ORDER BY q.asked_at, q.id`,
		likePrefix(actorPrefix), since)
}

// Asked is one question a crew of a project asked: when, which crew, what,
// and when it was answered ("" while open).
type Asked struct {
	AskedAt, Crew, Text, AnsweredAt string
}

// AskedSince reads the latest questions the crews of a project asked at or
// after since, newest first, at most limit.
func AskedSince(ctx context.Context, q Querier, project, since string, limit int) ([]Asked, error) {
	return collect(ctx, q, func(rows *sql.Rows, a *Asked) error {
		return rows.Scan(&a.AskedAt, &a.Crew, &a.Text, &a.AnsweredAt)
	}, `
		SELECT q.asked_at, c.name, q.text, COALESCE(q.answered_at, '')
		  FROM question q
		  JOIN actor c ON c.id = q.crew_actor_id
		 WHERE c.project = ? AND q.asked_at >= ?
		 ORDER BY q.asked_at DESC LIMIT ?`, project, since, limit)
}

// Message is one row of `message` with its event.
type Message struct {
	EventID                     int64
	At, Kind, Channel, From, To string
	Text, RefPath               string
	RefOffset                   int64
}

// ActorMessages reads the messages of a project an actor sent or received,
// in story order.
func ActorMessages(ctx context.Context, q Querier, project, actorID string) ([]Message, error) {
	return collect(ctx, q, func(rows *sql.Rows, m *Message) error {
		return rows.Scan(&m.EventID, &m.At, &m.Kind, &m.Channel, &m.From, &m.To, &m.Text,
			&m.RefPath, &m.RefOffset)
	}, `
		SELECT e.id,e.at,e.kind,m.channel,m.from_actor_id,m.to_actor_id,m.text,e.ref_path,e.ref_offset
		  FROM message m JOIN event e ON e.id=m.event_id
		 WHERE e.project=? AND (m.from_actor_id=? OR m.to_actor_id=?)
		 ORDER BY e.at,e.id`, project, actorID, actorID)
}

// Sent is one line the Mate of a project sent a crew: when, to which crew,
// and the text.
type Sent struct {
	At, Crew, Text string
}

// SentToCrewsSince reads the latest lines a project's Mate sent its crews
// at or after since and after each crew's spawn, newest first, at most
// limit: the corrections, not the briefs.
func SentToCrewsSince(ctx context.Context, q Querier, project, since string, limit int) ([]Sent, error) {
	return collect(ctx, q, func(rows *sql.Rows, s *Sent) error {
		return rows.Scan(&s.At, &s.Crew, &s.Text)
	}, `
		SELECT e.at, c.name, m.text
		  FROM message m
		  JOIN event e ON e.id = m.event_id
		  JOIN actor f ON f.id = m.from_actor_id
		  JOIN actor c ON c.id = m.to_actor_id
		  LEFT JOIN task t ON t.crew_actor_id = c.id
		 WHERE e.project = ? AND f.kind = 'mate' AND c.kind = 'crew' AND e.at >= ?
		   AND (t.spawned_at IS NULL OR t.spawned_at = '' OR e.at > t.spawned_at)
		 ORDER BY e.at DESC LIMIT ?`, project, since, limit)
}
