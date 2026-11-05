package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/diagnostics"
	"github.com/nguyenngocanh94/mate/internal/telemetry"
	"github.com/nguyenngocanh94/mate/internal/timeline"
	"github.com/nguyenngocanh94/mate/internal/timeline/read"
)

// CrewPerformance is the Task page's ledger row, model calls and diagnostics
// for one crew, for a reader that is not the HTTP API (`mate usage --why`):
// the terminal and the page read through the same projection, so they cannot
// disagree about what a Crew spent its tokens on.
func (s *Server) CrewPerformance(ctx context.Context, project, crew string) (Task, []Turn, diagnostics.Performance, error) {
	ledger, turns, _, performance, err := s.taskPerformance(ctx, project, crew)
	return ledger, turns, performance, err
}

func (s *Server) taskPerformance(ctx context.Context, project, crew string) (Task, []Turn, []Question, diagnostics.Performance, error) {
	now := s.deps.now()
	ledger, err := s.task(ctx, project, crew, now)
	if err != nil {
		return Task{}, nil, nil, diagnostics.Performance{}, err
	}
	actorID := timeline.CrewActorID(project, crew)
	turns, err := s.turns(ctx, actorID)
	if err != nil {
		return Task{}, nil, nil, diagnostics.Performance{}, err
	}
	questions, err := s.questions(ctx, actorID)
	if err != nil {
		return Task{}, nil, nil, diagnostics.Performance{}, err
	}
	performance, err := s.crewPerformance(ctx, actorID, ledger, turns, questions, now)
	if err != nil {
		return Task{}, nil, nil, diagnostics.Performance{}, err
	}
	return ledger, turns, questions, performance, nil
}

func (s *Server) crewPerformance(ctx context.Context, actorID string, ledger Task, turns []Turn, questions []Question, now time.Time) (diagnostics.Performance, error) {
	return s.actorPerformance(ctx, actorID, ledger.Closed, turns, questions, now)
}

// Both Mate and Crew use the same recorded activities and accounting rules.
// An actor without a task row (Mate) simply has no crew worktree to normalize.
func (s *Server) actorPerformance(ctx context.Context, actorID string, closed bool, turns []Turn, questions []Question, now time.Time) (diagnostics.Performance, error) {
	in := diagnostics.Input{Calls: make([]diagnostics.Call, 0, len(turns))}
	for _, t := range turns {
		in.Calls = append(in.Calls, diagnostics.Call{ID: t.ID, SessionID: t.SessionID, HarnessTurnRef: t.HarnessTurnRef,
			StartedAt: t.StartedAt, EndedAt: t.EndedAt, Model: t.Model, Ordinal: t.Ordinal, Tokens: performanceTokens(t.Tokens),
			ContextAfter: t.ContextAfter, Ref: diagnostics.Ref{Path: t.Ref.Path, Offset: t.Ref.Offset}, Outcome: t.Outcome})
	}
	for _, q := range questions {
		in.Decisions = append(in.Decisions, diagnostics.Decision{ID: q.ID, Kind: q.Kind, Text: q.Text, AskedAt: q.AskedAt, AnsweredAt: q.AnsweredAt, EventID: q.AskedEventID})
	}
	actions, err := read.ActorActions(ctx, s.db.SQL(), actorID)
	if err != nil {
		return diagnostics.Performance{}, err
	}
	for _, r := range actions {
		a := diagnostics.Action{ID: r.ID, CallID: r.TurnID, SessionID: r.SessionID, Tool: r.Tool, Target: r.Target,
			Summary: r.Summary, StartedAt: r.At, EndedAt: r.EndedAt, Ref: diagnostics.Ref{Path: r.RefPath, Offset: r.RefOffset}}
		if r.DurationMs.Valid {
			n := r.DurationMs.Int64
			a.DurationMs = &n
		}
		if r.OK.Valid {
			v := r.OK.Bool
			a.OK = &v
		}
		in.Actions = append(in.Actions, a)
	}
	events, err := read.TelemetryEvents(ctx, s.db.SQL(), actorID)
	if err != nil {
		return diagnostics.Performance{}, err
	}
	for _, e := range events {
		kind, raw, path, offset := e.Kind, e.Payload, e.RefPath, e.RefOffset
		if kind == "telemetry.profile" {
			var profile map[string]any
			if err = json.Unmarshal([]byte(raw), &profile); err != nil {
				in.RecordingError = "Recorded launch profile could not be decoded."
			} else {
				in.Profile = profile
			}
			continue
		}
		var f telemetry.Fact
		if err = json.Unmarshal([]byte(raw), &f); err != nil {
			in.RecordingError = fmt.Sprintf("A recorded %s event could not be decoded.", kind)
			continue
		}
		if f.Kind == "" {
			f.Kind = strings.TrimPrefix(kind, "telemetry.")
		}
		if f.SourcePath == "" {
			f.SourcePath = path
			f.SourceOffset = offset
		}
		in.Facts = append(in.Facts, f)
	}
	// Old read-only databases legitimately predate the telemetry cursor.
	// Their coverage stays unknown; opening the dashboard never migrates it.
	cursor, ok, err := read.TelemetryCursorOf(ctx, s.db.SQL(), actorID)
	if err != nil {
		return diagnostics.Performance{}, err
	}
	if ok {
		in.LastIngestedAt = cursor.ObservedAt
		if cursor.Error.Valid {
			in.RecordingError = cursor.Error.String
		}
	}
	worktree, err := read.TaskWorktree(ctx, s.db.SQL(), actorID)
	if err != nil {
		return diagnostics.Performance{}, err
	}
	return diagnostics.Project(in, diagnostics.Options{Now: now, Closed: closed, Worktree: worktree}), nil
}

func performanceTokens(t Tokens) diagnostics.Tokens {
	return diagnostics.Tokens{Input: t.Input, CacheRead: t.CacheRead, CacheWrite: t.CacheWrite, Output: t.Output, Thinking: t.Thinking, Total: t.Total}
}
