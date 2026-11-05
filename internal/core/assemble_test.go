package core

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/autopilot"
	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/timeline"
	"github.com/nguyenngocanh94/mate/internal/watch"
)

// The functions a snapshot is assembled from, moved here from cmd/mate with
// their tests (CON-208).

// While Herdr cannot be reached the observer stands a notice a UI can draw.
// A snapshot straight out of query.Load cannot have one - query reads files
// only - so without this merge the tree on screen keeps looking fresh while
// nothing behind it can be re-read.
func TestWithRuntimeNoticeCarriesHerdrDownIntoTheSnapshot(t *testing.T) {
	w := shopWorkspace(t)
	down := observability.NewError(observability.CodeRuntimeUnavailable,
		"herdr is not running; start or resume the Mate with s to bring it back (session mate-shop)")
	watcher := watch.New(w, watch.Deps{
		Runtime: runtime.NewFake(),
		Session: func(context.Context) error { return down },
	})
	if err := watcher.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	snap, err := query.Load(context.Background(), w, testHarnesses, nil)
	if err != nil {
		t.Fatalf("query.Load: %v", err)
	}
	if snap.Runtime.Notice != "" {
		t.Fatalf("Runtime straight out of query.Load = %+v, want empty: query reads files only", snap.Runtime)
	}
	snap = withRuntimeNotice(snap, watcher)
	if snap.Runtime.Notice == "" || snap.Runtime.At.IsZero() {
		t.Fatalf("Runtime = %+v, want the observer's notice", snap.Runtime)
	}
	if !strings.Contains(snap.Runtime.Notice, "herdr is not running") || !strings.Contains(snap.Runtime.Notice, "mate-shop") {
		t.Fatalf("Runtime notice = %q, want the failure and the session it names", snap.Runtime.Notice)
	}
}

func TestWithCrewHealthLeavesUnobservedCrewsAlone(t *testing.T) {
	snap := query.Snapshot{Projects: []query.ProjectNode{{
		ProjectID: "shop",
		Crews: []query.CrewNode{
			{CrewID: "k3", Health: query.AbsentField[query.CrewHealth]("no observer has looked at this crew yet")},
			{CrewID: "k9", Health: query.AbsentField[query.CrewHealth]("no observer has looked at this crew yet")},
		},
	}}}
	observed := map[watch.CrewRef]watch.Health{
		{Project: "shop", Crew: "k3"}: {AgentPresent: true, Composer: send.StateBusy, QuietFor: 3 * time.Second, Source: "jev"},
		// A crew of another project with the same id must not leak across.
		{Project: "blog", Crew: "k9"}: {AgentPresent: false, Composer: send.StateUnknown},
	}

	out := withCrewHealth(snap, observed)
	k3 := out.Projects[0].Crews[0].Health
	if !k3.IsKnown() || k3.Value.Composer != query.ComposerBusy || k3.Value.QuietFor != 3*time.Second || k3.Value.Source != "jev" {
		t.Fatalf("k3 health = %+v, want the observation", k3)
	}
	if k9 := out.Projects[0].Crews[1].Health; k9.State != query.Absent {
		t.Fatalf("k9 health = %+v, want it left Absent", k9)
	}
}

func TestComposerDTOCarriesEveryMeasuredState(t *testing.T) {
	for state, want := range map[send.ComposerState]query.CrewComposer{
		send.StateEmpty:                     query.ComposerEmpty,
		send.StatePending:                   query.ComposerPending,
		send.StateBusy:                      query.ComposerBusy,
		send.StateUnknown:                   query.ComposerUnknown,
		send.ComposerState("something new"): query.ComposerUnknown,
	} {
		if got := composerDTO(state); got != want {
			t.Fatalf("composerDTO(%q) = %q, want %q", state, got, want)
		}
	}
}

// A project the daemon has not ticked keeps the zero value: "this console has
// sent nothing for it" is what the zero value already says, and borrowing a
// neighbour's would put another project's clock on this row.
func TestWithAutoStatusLeavesUntickedProjectsAlone(t *testing.T) {
	snap := query.Snapshot{Projects: []query.ProjectNode{
		{ProjectID: "shop"}, {ProjectID: "blog"},
	}}
	merged := withAutoStatus(snap, map[string]autopilot.Status{
		"shop": {Sends: 3, Notice: "not delivered"},
	})
	if merged.Projects[0].Daemon.Sends != 3 {
		t.Fatalf("shop = %+v, want the daemon's state", merged.Projects[0].Daemon)
	}
	if merged.Projects[1].Daemon != (query.AutoDaemon{}) {
		t.Fatalf("blog = %+v, want the zero value", merged.Projects[1].Daemon)
	}
}

// withTimelineStale is silent with no ingest: an engine that is not the
// timeline's writer has no staleness of its own to report.
func TestWithTimelineStaleSaysNothingWithoutAnIngest(t *testing.T) {
	snap := withTimelineStale(query.Snapshot{Runtime: query.RuntimeStatus{Notice: "x"}}, nil)
	if snap.Runtime.Notice != "x" {
		t.Fatalf("notice %q, want it untouched", snap.Runtime.Notice)
	}
}

// withTokens (mvp.md M5 task 27) reads `.mate/mate.db` read-only and
// fills CrewNode.Tokens and MateNode.Tokens from the ledger; a workspace
// with no database yet leaves both Absent, exactly the shape withCrewHealth
// already has for Health.
func TestWithTokensFillsFromTheLedger(t *testing.T) {
	w := shopWorkspace(t)
	if err := w.WriteMateMeta("shop", map[string]string{"harness": "claude"}); err != nil {
		t.Fatalf("WriteMateMeta: %v", err)
	}
	if err := w.WriteCrewMeta("shop", "k3", map[string]string{"state": "spawned"}); err != nil {
		t.Fatalf("WriteCrewMeta: %v", err)
	}

	snap, err := query.Load(context.Background(), w, testHarnesses, nil)
	if err != nil {
		t.Fatalf("query.Load: %v", err)
	}
	if got := withTokens(snap, w); got.Projects[0].Crews[0].Tokens.State != query.Absent {
		t.Fatalf("Tokens before any database exists = %+v, want Absent", got.Projects[0].Crews[0].Tokens)
	}

	handle := withTimelineDB(t, w)
	crewActor := timeline.CrewActorID("shop", "k3")
	mateActor := timeline.MateActorID("shop")
	seedActorTurnAndPricing(t, handle, crewActor, "shop", "crew", "test-model", 1000, 200, 0, 300, 400_000)
	seedActorTurnAndPricing(t, handle, mateActor, "shop", "mate", "test-model", 500, 0, 0, 50, 400_000)
	if _, err := handle.SQL().Exec(`UPDATE pricing SET input_per_m = 2, output_per_m = 4 WHERE model = 'test-model'`); err != nil {
		t.Fatalf("price the model: %v", err)
	}
	if _, err := handle.SQL().Exec(`UPDATE turn SET started_at = ? WHERE actor_id = ?`,
		db.FormatTime(time.Now()), mateActor); err != nil {
		t.Fatalf("date the mate's turn: %v", err)
	}

	snap, err = query.Load(context.Background(), w, testHarnesses, nil)
	if err != nil {
		t.Fatalf("query.Load: %v", err)
	}
	got := withTokens(snap, w)
	crew := got.Projects[0].Crews[0]
	if !crew.Tokens.IsKnown() {
		t.Fatalf("crew Tokens = %+v, want Known", crew.Tokens)
	}
	if crew.Tokens.Value.Total != 1500 {
		t.Fatalf("crew total tokens = %d, want 1500", crew.Tokens.Value.Total)
	}
	wantCost := (1000*2.0 + 300*4.0) / 1_000_000.0
	if crew.Tokens.Value.Cost == nil {
		t.Fatalf("crew cost = nil, want ~%v", wantCost)
	}
	if d := *crew.Tokens.Value.Cost - wantCost; d > 1e-9 || d < -1e-9 {
		t.Fatalf("crew cost = %v, want ~%v", *crew.Tokens.Value.Cost, wantCost)
	}
	if crew.Tokens.Value.ContextPct == nil {
		t.Fatalf("crew ContextPct = nil, want the seeded context window's percentage")
	}

	mate := got.Projects[0].Mate
	if !mate.Tokens.IsKnown() || mate.Tokens.Value.Total != 550 {
		t.Fatalf("mate Tokens = %+v, want Known with total 550", mate.Tokens)
	}
}

// A model with no known window still has an absolute context: the Mate's row
// keeps its token count and leaves the percentage out (it was the console's
// half of cmd/mate's TestUsageAndConsoleKeepAbsoluteContextWithoutAModelWindow).
func TestMateTokensKeepAbsoluteContextWithoutAModelWindow(t *testing.T) {
	w := shopWorkspace(t)
	handle := withTimelineDB(t, w)
	seedActorTurnAndPricing(t, handle, timeline.MateActorID("shop"), "shop", "mate", "unpriced-model", 400, 0, 0, 100, 200_000)
	if _, err := handle.SQL().Exec(`DELETE FROM pricing`); err != nil {
		t.Fatal(err)
	}
	tokens, ok := mateTokens(handle, "shop")
	if !ok || tokens.ContextTokens == nil || *tokens.ContextTokens != 400 || tokens.ContextPct != nil {
		t.Fatalf("lost absolute context: %+v", tokens)
	}
}

// Herdr that does not answer leaves the liveness empty, and the snapshot
// still comes: every row keeps its recorded state (internal/query's
// ReadLiveness).
func TestASnapshotComesWhileHerdrDoesNotAnswer(t *testing.T) {
	w := shopWorkspace(t)
	rt := runtime.NewFake()
	rt.SessionLookupErr = errors.New("herdr: connection refused")
	deps := spawn.Deps{Runtime: rt, Names: rt.Names, ConfigHome: t.TempDir()}
	if got := liveness(context.Background(), w, deps); got.Asked || got.Agents != nil {
		t.Fatalf("liveness = %+v, want nothing asked", got)
	}
	if got := liveness(context.Background(), w, spawn.Deps{}); got.Asked {
		t.Fatalf("liveness with no runtime = %+v, want nothing asked", got)
	}
	e := New(Options{Workspace: w, Spawn: deps, Harnesses: testHarnesses})
	defer e.Stop()
	snap, err := e.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot while Herdr does not answer: %v", err)
	}
	if len(snap.Projects) != 1 || snap.Projects[0].ProjectID != "shop" {
		t.Fatalf("projects %+v, want shop", snap.Projects)
	}
}
