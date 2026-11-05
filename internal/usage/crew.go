package usage

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/timeline"
	"github.com/nguyenngocanh94/mate/internal/timeline/read"
)

// writeCrewTurns is the `usage <project> <crew>` form: one row per model
// call, in the order they happened.
func writeCrewTurns(ctx context.Context, out io.Writer, handle *db.DB, project, crew string) error {
	actorID := timeline.CrewActorID(project, crew)
	turns, err := read.Turns(ctx, handle.SQL(), actorID)
	if err != nil {
		return err
	}

	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TURN\tSTARTED\tMODEL\tEFFORT\tIN\tCACHED\tOUT\tTOTAL\tCTX\tDURATION\tOUTCOME")
	n := 0
	for _, t := range turns {
		ordinal, startedAt, endedAt := t.Ordinal, t.StartedAt, t.EndedAt
		model, effort, outcome := t.Model, t.Effort, t.Outcome
		in, cacheRead, cacheWrite, output, ctxAfter := t.Input, t.CacheRead, t.CacheWrite, t.Output, t.ContextAfter
		started := db.ParseTime(startedAt.String)
		var dur time.Duration
		if endedAt.Valid {
			dur = db.ParseTime(endedAt.String).Sub(started)
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			ordinal+1, started.Local().Format("15:04:05"), dashIfEmpty(model), dashIfEmpty(effort),
			query.HumanizeTokens(in), query.HumanizeTokens(cacheRead+cacheWrite), query.HumanizeTokens(output),
			query.HumanizeTokens(in+cacheRead+cacheWrite+output), query.HumanizeTokens(ctxAfter),
			dur.Round(time.Millisecond), dashIfEmpty(outcome))
		n++
	}
	_ = tw.Flush()
	if n == 0 {
		fmt.Fprintf(out, "no turns recorded for %s\n", crew)
	}
	return nil
}

// writeCrewSessions is the `usage <project> <crew> --sessions` form
// (CON-73): one row per harness session the crew has had, in order, with
// the session's calls, tokens and largest context. It is how a task with
// two review rounds shows what each round cost: the acceptance of CON-73
// is the tokens after `pr-open` against the tokens before it.
func writeCrewSessions(ctx context.Context, out io.Writer, handle *db.DB, project, crew string) error {
	actorID := timeline.CrewActorID(project, crew)
	sessions, err := read.Sessions(ctx, handle.SQL(), actorID)
	if err != nil {
		return err
	}

	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SESSION\tTURNS\tIN\tCACHED\tOUT\tTOTAL\tCTX-MAX\tSTARTED\tENDED")
	n := 0
	var grand int64
	for _, s := range sessions {
		ordinal, turns, in, cached, output, ctxMax := s.CrewSession, s.Turns, s.Input, s.Cached, s.Output, s.ContextMax
		startedAt, endedAt := s.StartedAt, s.EndedAt
		total := in + cached + output
		grand += total
		fmt.Fprintf(tw, "%d\t%d\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			ordinal, turns, query.HumanizeTokens(in), query.HumanizeTokens(cached), query.HumanizeTokens(output),
			query.HumanizeTokens(total), query.HumanizeTokens(ctxMax),
			clockWord(startedAt), clockWord(endedAt))
		n++
	}
	_ = tw.Flush()
	if n == 0 {
		fmt.Fprintf(out, "no turns recorded for %s\n", crew)
		return nil
	}
	fmt.Fprintf(out, "total: %d session(s), %s tokens\n", n, query.HumanizeTokens(grand))
	return nil
}

// clockWord is a stored time as a local clock, or a dash for none.
func clockWord(v sql.NullString) string {
	if !v.Valid || v.String == "" {
		return "-"
	}
	return db.ParseTime(v.String).Local().Format("15:04:05")
}

// contextWord keeps missing usage distinct from a measured zero.
func contextWord(v sql.NullInt64) string {
	if !v.Valid {
		return "?"
	}
	return query.HumanizeTokens(v.Int64)
}
