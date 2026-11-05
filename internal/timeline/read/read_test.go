package read

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/db"
)

// The readers' own output is pinned by the dashboard's and cmd/mate's
// tests, which ran unchanged across the move into this package. This file
// covers what the package added: the optional upper bounds, the LIKE
// escaping, the IN list and the answers for a missing row.

func open(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.OpenPath(filepath.Join(t.TempDir(), "mate.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func seed(t *testing.T) Querier {
	t.Helper()
	d := open(t)
	for _, s := range []string{
		`INSERT INTO actor (id, project, kind, name) VALUES
			('shop/mate', 'shop', 'mate', 'mate'),
			('shop/crew/a_b', 'shop', 'crew', 'a_b'),
			('shop/crew/axb', 'shop', 'crew', 'axb')`,
		`INSERT INTO session (id, actor_id) VALUES ('s1', 'shop/crew/a_b')`,
		`INSERT INTO turn (id, actor_id, session_id, ordinal, started_at, input_tokens) VALUES
			('t1', 'shop/crew/a_b', 's1', 0, '2026-11-01T10:00:00Z', 10),
			('t2', 'shop/crew/a_b', 's1', 1, '2026-11-02T10:00:00Z', 20)`,
		`INSERT INTO event (dedup, project, at, actor_id, kind) VALUES
			('d1', 'shop', '2026-11-01T10:00:00Z', 'shop/crew/a_b', 'status.appended'),
			('d2', 'shop', '2026-11-01T11:00:00Z', 'shop/crew/axb', 'status.appended'),
			('d3', 'shop', '2026-11-01T12:00:00Z', 'shop/mate', 'crew.spawned'),
			('d4', 'shop', '2026-11-01T13:00:00Z', 'shop/mate', 'merge.done'),
			('d5', 'shop', '2026-11-01T14:00:00Z', 'shop/mate', 'crew.failed')`,
		`INSERT INTO incident (id, project, actor_id, kind, opened_event_id, opened_at) VALUES
			('i1', 'shop', 'shop/crew/a_b', 'stalled', 1, '2026-11-01T10:00:00Z'),
			('i2', 'shop', 'shop/crew/a_b', 'stalled', 2, '2026-11-02T10:00:00Z')`,
	} {
		if _, err := d.SQL().Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	return d.SQL()
}

func TestEmptyDatabase(t *testing.T) {
	ctx, q := context.Background(), open(t).SQL()
	if id, err := LastEventID(ctx, q); err != nil || id != 0 {
		t.Errorf("LastEventID = %d, %v; want 0, nil", id, err)
	}
	if wt, err := TaskWorktree(ctx, q, "shop/crew/x"); err != nil || wt != "" {
		t.Errorf("TaskWorktree = %q, %v; want empty, nil", wt, err)
	}
	if pct, err := MateContextPct(ctx, q, "shop"); err != nil || pct.Valid {
		t.Errorf("MateContextPct = %+v, %v; want NULL, nil", pct, err)
	}
	if m, e, err := LastModel(ctx, q, "shop/mate"); err != nil || m != "" || e != "" {
		t.Errorf("LastModel = %q, %q, %v; want empty", m, e, err)
	}
	if _, ok, err := ActorOf(ctx, q, "shop/mate"); err != nil || ok {
		t.Errorf("ActorOf = ok %v, %v; want not found", ok, err)
	}
	if _, ok, err := TelemetryCursorOf(ctx, q, "shop/mate"); err != nil || !ok {
		t.Errorf("TelemetryCursorOf = ok %v, %v; want the table present", ok, err)
	}
}

func TestBounds(t *testing.T) {
	ctx, q := context.Background(), seed(t)
	all, err := TurnTokensBetween(ctx, q, "2026-11-01T00:00:00Z", "")
	if err != nil || len(all) != 2 {
		t.Fatalf("TurnTokensBetween open = %d rows, %v; want 2", len(all), err)
	}
	day, err := TurnTokensBetween(ctx, q, "2026-11-01T00:00:00Z", "2026-11-02T00:00:00Z")
	if err != nil || len(day) != 1 || day[0].Input != 10 || day[0].Name != "a_b" {
		t.Fatalf("TurnTokensBetween one day = %+v, %v; want t1 alone", day, err)
	}
	inc, err := Incidents(ctx, q, "2026-11-01T00:00:00Z", "")
	if err != nil || len(inc) != 2 {
		t.Fatalf("Incidents open = %d rows, %v; want 2", len(inc), err)
	}
	inc, err = Incidents(ctx, q, "2026-11-01T00:00:00Z", "2026-11-02T00:00:00Z")
	if err != nil || len(inc) != 1 || inc[0].ActorName != "a_b" || inc[0].ActorKind != "crew" {
		t.Fatalf("Incidents one day = %+v, %v; want i1 alone", inc, err)
	}
}

func TestPrefixIsLiteral(t *testing.T) {
	ctx, q := context.Background(), seed(t)
	// `_` in a prefix is a character, not LIKE's any-one wildcard: the
	// prefix of crew a_b must not match crew axb.
	got, err := ProjectEventsOfKind(ctx, q, "shop", "", "status.appended", "shop/crew/a_")
	if err != nil || len(got) != 1 || got[0].ActorID != "shop/crew/a_b" {
		t.Fatalf("ProjectEventsOfKind = %+v, %v; want a_b's line alone", got, err)
	}
}

func TestKindList(t *testing.T) {
	ctx, q := context.Background(), seed(t)
	got, err := ActorEventsOfKinds(ctx, q, "shop", "shop/mate", "crew.spawned", "crew.failed")
	if err != nil || len(got) != 2 || got[0].Kind != "crew.spawned" || got[1].Kind != "crew.failed" {
		t.Fatalf("ActorEventsOfKinds = %+v, %v; want spawned then failed", got, err)
	}
	if got, err := ActorEventsOfKinds(ctx, q, "shop", "shop/mate"); err != nil || got != nil {
		t.Fatalf("ActorEventsOfKinds with no kind = %+v, %v; want nothing", got, err)
	}
}
