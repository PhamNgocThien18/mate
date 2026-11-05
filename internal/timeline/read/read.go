// Package read is the read side of `mate.db`: every query a reader outside
// `internal/db` and `internal/timeline` runs against the schema of
// docs/timeline.md lives here, so a column that changes is changed in one
// package rather than in the dashboard and in `cmd/mate` separately
// (CON-166).
//
// Each function takes a context, a Querier and its parameters, and returns
// typed rows: never a *sql.Rows. A text column a caller only ever reads as
// text is a string, NULL reading as ""; a column whose NULL means something
// other than its zero (an unpriced cost, a turn that has not ended) keeps
// its sql.Null type, so a caller can tell the two apart as it did before.
//
// Readers take no lock (docs/timeline.md): the database is derived, and a
// reader that finds no `mate.db`, or one `mate reindex` is rebuilding,
// handles that itself, as it did before this package existed. The package
// imports nothing of mate's: event kinds and actor ids are the caller's to
// pass, so `timeline` stays the one place that names them.
package read

import (
	"context"
	"database/sql"
)

// Querier is the part of *sql.DB and *sql.Tx a read needs. The dashboard
// passes the *db.DB it holds (`SQL()`); a CLI command passes the connection
// it opened itself.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// collect runs a query and scans every row with scan, closing the rows.
func collect[T any](ctx context.Context, q Querier, scan func(*sql.Rows, *T) error, query string, args ...any) ([]T, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []T
	for rows.Next() {
		var v T
		if err := scan(rows, &v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, rows.Close()
}
