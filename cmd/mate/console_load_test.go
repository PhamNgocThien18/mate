package main

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/core"
	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/query"
)

// The console reads the engine's snapshot with its own keys in Commands and
// the notifications' word on the runtime line. The snapshot is shared with
// Mate Office and Agent City, so what the console adds goes on its copy and
// never into the one the next reader gets.
func TestConsoleLoadAddsItsKeysToTheSharedSnapshot(t *testing.T) {
	shared := query.Snapshot{
		Projects: []query.ProjectNode{{ProjectID: "shop"}},
		Runtime:  query.RuntimeStatus{Notice: "herdr is not running"},
	}
	snapshot := func(context.Context) (query.Snapshot, error) { return shared, nil }
	bindings := []query.CommandBinding{{Key: "e", Label: "report"}}
	status := ""
	load := consoleLoad(snapshot, bindings, func() string { return status })

	snap, err := load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(snap.Commands, bindings) || snap.Runtime.Notice != "herdr is not running" {
		t.Fatalf("commands %+v, runtime %q; want the console's keys and the engine's line", snap.Commands, snap.Runtime.Notice)
	}
	snap.Commands[0].Key = "x" // a reader's change of its own copy
	status = "notifications: no notify-send"
	snap, err = load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Commands[0].Key != "e" || snap.Runtime.Notice != "herdr is not running; notifications: no notify-send" {
		t.Fatalf("commands %+v, runtime %q", snap.Commands, snap.Runtime.Notice)
	}
	if shared.Commands != nil || shared.Runtime.Notice != "herdr is not running" {
		t.Fatalf("the shared snapshot changed under its other readers: %+v", shared)
	}

	boom := errors.New("workspace.yaml does not parse")
	failing := consoleLoad(func(context.Context) (query.Snapshot, error) { return query.Snapshot{}, boom }, bindings, func() string { return "" })
	if _, err := failing(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the engine's", err)
	}
}

// The snapshot is handed out again for two seconds, but not after an action
// the console ran: the engine's Act drops it, and the next read shows what
// the action changed (CON-208, CON-209). An action that does not go
// through Act would be seen only once the two seconds are over.
func TestConsoleReadsAFreshSnapshotAfterAnAction(t *testing.T) {
	w, deps := consoleFixture(t, "shop")
	raw := engineActions(w, deps, nil)
	engine := core.New(core.Options{Workspace: w, Spawn: deps, Harnesses: consoleHarnesses(), Act: raw})
	defer engine.Stop()
	load := consoleLoad(engine.Snapshot, nil, func() string { return "" })
	tui := tuiAction(engine.Act, nil)
	ctx := context.Background()
	mode := func() query.Mode {
		t.Helper()
		snap, err := load(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return snap.Projects[0].Mode
	}
	press := func(act coreapi.ActionFunc) {
		t.Helper()
		if _, err := act(ctx, coreapi.ActionRequest{Action: coreapi.ActionMode, Target: "shop", TargetKind: "project"}); err != nil {
			t.Fatalf("mode: %v", err)
		}
	}

	if got := mode(); got != query.ModeAuto {
		t.Fatalf("mode %q, want auto (CON-115)", got)
	}
	press(tui)
	if got := mode(); got != query.ModeManual {
		t.Fatalf("mode %q right after the console's action, want manual", got)
	}
	// The same action run without Invalidate: the cached snapshot stands.
	press(raw)
	if !w.Auto("shop") {
		t.Fatal("the action did not release the manual hold")
	}
	if got := mode(); got != query.ModeManual {
		t.Fatalf("mode %q, want the cached manual: only an action's Invalidate drops the snapshot early", got)
	}
}
