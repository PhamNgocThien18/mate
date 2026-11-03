package query

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// testHarnesses is a harness catalog as cmd/mate hands one to Load.
var testHarnesses = Harnesses{
	List: []Harness{
		{Kind: "claude", Icon: HarnessIcon{Nerd: "\uec82", Unicode: "✻", ASCII: "*"}},
		{Kind: "codex", Icon: HarnessIcon{Nerd: "\uec81", Unicode: "⌬", ASCII: "#"}},
	},
	MateDefault: "claude",
}

func newWorkspace(t *testing.T, projects ...string) *store.Workspace {
	t.Helper()
	root := t.TempDir()
	ws, err := store.Init(root, store.Defaults{MateHarness: "claude", CrewHarness: "codex"})
	if err != nil {
		t.Fatalf("init workspace: %v", err)
	}
	for _, name := range projects {
		if err := os.MkdirAll(filepath.Join(root, name, name, ".git"), 0o755); err != nil {
			t.Fatalf("create repo %s: %v", name, err)
		}
		if err := ws.AddProject(name, store.ProjectConfig{Repos: []store.RepoConfig{{Path: name + "/" + name}}}); err != nil {
			t.Fatalf("add project %s: %v", name, err)
		}
	}
	return ws
}

func TestLoadListsRegisteredProjectsWithTheirRepo(t *testing.T) {
	ws := newWorkspace(t, "shop", "blog")
	snap, err := Load(context.Background(), ws, testHarnesses, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(snap.Projects) != 2 {
		t.Fatalf("projects = %+v, want two", snap.Projects)
	}
	if snap.Workspace.State != Known || snap.Workspace.Value.Root != ws.Root() {
		t.Fatalf("workspace field = %+v, want the store's own resolved root", snap.Workspace)
	}
	if snap.AsOf.IsZero() {
		t.Fatal("AsOf is zero; a snapshot must say how old it is")
	}
	p := snap.Projects[0]
	if p.ProjectID != "shop" || p.Name != "shop" {
		t.Fatalf("first project = %+v, want shop", p)
	}
	if p.Repos.State != Known || len(p.Repos.Value) != 1 {
		t.Fatalf("repos = %+v, want the one registered repo", p.Repos)
	}
	if got, want := p.Repos.Value[0].Path, "shop/shop"; got != want {
		t.Fatalf("repo path = %q, want %q", got, want)
	}
	if got := p.Repos.Value[0].DefaultBranch; got != store.DefaultBranch {
		t.Fatalf("default branch = %q, want %q", got, store.DefaultBranch)
	}
}

// TestLoadReportsNoMateAsAbsentNotUnknown: a project whose mate.meta does
// not exist has no Mate, which the read established. That is Absent - the
// state the Console renders as "none assigned" - and never Unknown, which
// would claim the read failed.
func TestLoadReportsNoMateAsAbsentNotUnknown(t *testing.T) {
	ws := newWorkspace(t, "shop")
	snap, err := Load(context.Background(), ws, testHarnesses, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	mate := snap.Projects[0].Mate
	if mate.Designated.State != Absent {
		t.Fatalf("designation = %+v, want Absent for a project with no mate.meta", mate.Designated)
	}
	if mate.Designated.Reason == "" {
		t.Fatal("an Absent designation must carry the reason there is no Mate")
	}
	if len(snap.Warnings) != 0 {
		t.Fatalf("warnings = %+v, want none: nothing failed to read", snap.Warnings)
	}
	if a := snap.Projects[0].Attention; a.State != Known || a.Value.Kind != AttentionNoMate {
		t.Fatalf("project attention = %+v, want the no-mate kind", a)
	}
}

// TestLoadReadsMateMetaAndCrewStatus is the shape mvp.md section 3
// describes: a Mate row exists because mate.meta exists, its recorded pane
// is what makes it running, and a Crew's status is the last line of its
// own .status file.
func TestLoadReadsMateMetaAndCrewStatus(t *testing.T) {
	ws := newWorkspace(t, "shop")
	if err := ws.WriteMateMeta("shop", map[string]string{
		"harness": "claude", "session": "fm-x", "pane": "p-1", "agent": "mate-shop",
	}); err != nil {
		t.Fatalf("write mate.meta: %v", err)
	}
	if err := ws.WriteCrewMeta("shop", "k3", map[string]string{
		"task": "wire the webhook", "harness": "codex", "model": "gpt-5.5", "effort": "high",
		"worktree": "/w/shop-k3", "branch": "mate/k3",
	}); err != nil {
		t.Fatalf("write crew meta: %v", err)
	}
	for _, line := range []string{"working: reading the brief", "needs-decision: which adapter?"} {
		if err := ws.AppendStatus("shop", "k3", line); err != nil {
			t.Fatalf("append status: %v", err)
		}
	}

	snap, err := Load(context.Background(), ws, testHarnesses, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	p := snap.Projects[0]
	if p.Mate.Designated.State != Known {
		t.Fatalf("designation = %+v, want Known once mate.meta exists", p.Mate.Designated)
	}
	if got := p.Mate.Designated.Value.Status; got != MateRunning {
		t.Fatalf("mate status = %q, want running for a recorded pane", got)
	}
	if got := p.Mate.Designated.Value.HarnessKind; got != HarnessKind("claude") {
		t.Fatalf("mate harness = %q, want claude", got)
	}
	if p.Mate.Binding.State != Known || p.Mate.Binding.Reason == "" {
		t.Fatalf("binding = %+v, want Known with the caveat that it proves nothing about the agent", p.Mate.Binding)
	}

	if len(p.Crews) != 1 {
		t.Fatalf("crews = %+v, want the one crew meta", p.Crews)
	}
	c := p.Crews[0]
	if c.CrewID != "k3" || c.Task != "wire the webhook" || c.HarnessKind != HarnessKind("codex") {
		t.Fatalf("crew = %+v, want k3 from its meta", c)
	}
	if c.Model != "gpt-5.5" || c.Effort != "high" {
		t.Fatalf("crew profile = %q/%q, want the model and effort it was spawned with", c.Model, c.Effort)
	}
	if got := c.Status; got != CrewStatus("needs-decision") {
		t.Fatalf("crew status = %q, want the last status line's state word", got)
	}
	if c.Worktree.State != Known || c.Worktree.Value.Branch != "mate/k3" {
		t.Fatalf("worktree = %+v, want the recorded worktree and branch", c.Worktree)
	}
}

// TestLoadShowsAnAnsweredQuestionAsWorking is CON-105: the crew asked,
// the Mate replied through `mate send`, and the crew - told to wait - wrote
// nothing more. The inbox let go of the question at the reply; the crew row
// must agree, so it is working and nothing about it needs attention.
func TestLoadShowsAnAnsweredQuestionAsWorking(t *testing.T) {
	ws := newWorkspace(t, "shop")
	if err := ws.WriteCrewMeta("shop", "k3", map[string]string{"task": "profile", "state": "spawned"}); err != nil {
		t.Fatalf("write crew meta: %v", err)
	}
	if err := ws.AppendStatus("shop", "k3", "needs-decision: 50 or 60?"); err != nil {
		t.Fatalf("append status: %v", err)
	}
	load := func() CrewNode {
		t.Helper()
		snap, err := Load(context.Background(), ws, testHarnesses, nil)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		return snap.Projects[0].Crews[0]
	}
	if c := load(); c.Status != CrewNeedsDecision || c.Attention.State != Known {
		t.Fatalf("before the reply: status %q, attention %+v; want needs-decision needing attention", c.Status, c.Attention)
	}
	if err := ws.AppendSent("shop", store.SentEntry{
		Time: time.Now().Add(time.Second), Source: store.SourceMate, Target: store.CrewTarget("k3"), Text: "60; wait for PLA-88",
	}); err != nil {
		t.Fatalf("append sent: %v", err)
	}
	if c := load(); c.Status != CrewWorking || c.Attention.State == Known {
		t.Fatalf("after the reply: status %q, attention %+v; want working with no attention", c.Status, c.Attention)
	}
}

// TestLoadGivesACrewThatWroteNothingTheSpawnedState: the meta is what
// records the crew, so a crew with no status line yet is `spawned` - the
// last rule of mvp.md section 4b's resolution order, and not blank.
func TestLoadGivesACrewThatWroteNothingTheSpawnedState(t *testing.T) {
	ws := newWorkspace(t, "shop")
	if err := ws.WriteCrewMeta("shop", "k9", map[string]string{"task": "scout", "state": "spawned"}); err != nil {
		t.Fatalf("write crew meta: %v", err)
	}
	snap, err := Load(context.Background(), ws, testHarnesses, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := snap.Projects[0].Crews[0].Status; got != CrewSpawned {
		t.Fatalf("status = %q, want %q", got, CrewSpawned)
	}
}

// TestLoadResolvesEachCrewStateInTheOrderOfSection4b walks the whole table
// on real files: the meta first, then an open incident, then the crew's own
// last verb, then `spawned`. The incident case is the one the observer
// (task 18) will fill in for real; until then box.Load reads no
// incidents.log, so this drives it through the box view the same way Load
// does and asserts the rule, not the producer.
func TestLoadResolvesEachCrewStateInTheOrderOfSection4b(t *testing.T) {
	ws := newWorkspace(t, "shop")
	write := func(crew string, meta map[string]string, status ...string) {
		t.Helper()
		if err := ws.WriteCrewMeta("shop", crew, meta); err != nil {
			t.Fatalf("write crew meta %s: %v", crew, err)
		}
		for _, line := range status {
			if err := ws.AppendStatus("shop", crew, line); err != nil {
				t.Fatalf("append status %s: %v", crew, err)
			}
		}
	}
	// A crew's own last verb, including the legacy spellings.
	write("k1", map[string]string{"state": "spawned"}, "working: reading the ticket")
	write("k2", map[string]string{"state": "spawned"}, "working: a", "needs-decision: A or B?")
	write("k3", map[string]string{"state": "spawned"}, "wait-mate: ready in branch mate/k3")
	write("k4", map[string]string{"state": "spawned"}, "done: ready in branch mate/k4")
	// Nothing written at all.
	write("k5", map[string]string{"state": "spawned"})
	// The meta's terminal state outranks whatever the crew last said.
	write("k6", map[string]string{"state": "finished", "stopped_at": "2026-09-18T10:00:00Z"}, "working: mid-turn when it was closed")
	write("k7", map[string]string{"state": "failed", "failed_reason": "startup screen not recognised"})
	// Backward compatibility: stopped_at with no state= is finished.
	write("k8", map[string]string{"stopped_at": "2026-09-17T10:00:00Z"})

	snap, err := Load(context.Background(), ws, testHarnesses, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	p := snap.Projects[0]
	got := map[string]CrewStatus{}
	for _, c := range p.Crews {
		got[c.CrewID] = c.Status
	}
	want := map[string]CrewStatus{
		"k1": CrewWorking,
		"k2": CrewNeedsDecision,
		"k3": CrewWaitMate,
		"k4": CrewWaitMate,
		"k5": CrewSpawned,
	}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("crew %s = %q, want %q", id, got[id], w)
		}
	}
	// k6, k7 and k8 are closed and therefore not rows at all.
	for _, id := range []string{"k6", "k7", "k8"} {
		if _, ok := got[id]; ok {
			t.Errorf("crew %s is closed and must not be a row", id)
		}
	}
	if p.ClosedCrews != 3 {
		t.Errorf("ClosedCrews = %d, want 3", p.ClosedCrews)
	}
}

// TestLoadReadsBlockedFromTheObserversOpenIncidents drives the `blocked`
// rule end to end over real files: the observer (internal/watch) appends to
// `incidents.log`, box.Load merges it, and Load resolves the state from
// there. Nothing is hand-built; the rule under test is the resolution.
//
// k1 has an open incident and is blocked even though it last said
// `working:`; k2's incident was resolved, so it is back to its own verb;
// k3 has none.
func TestLoadReadsBlockedFromTheObserversOpenIncidents(t *testing.T) {
	ws := newWorkspace(t, "shop")
	for _, id := range []string{"k1", "k2", "k3"} {
		if err := ws.WriteCrewMeta("shop", id, map[string]string{"state": "spawned"}); err != nil {
			t.Fatalf("write crew meta %s: %v", id, err)
		}
		if err := ws.AppendStatus("shop", id, "working: reading the ticket"); err != nil {
			t.Fatalf("append status %s: %v", id, err)
		}
	}
	for _, inc := range []store.IncidentEntry{
		{Time: time.Now().Add(-time.Hour), Crew: "k1", Kind: "stale", State: store.IncidentOpen, Text: "no status for 20m"},
		{Time: time.Now().Add(-time.Hour), Crew: "k2", Kind: "runtime_lost", State: store.IncidentOpen, Text: "the agent left herdr"},
		{Time: time.Now(), Crew: "k2", Kind: "runtime_lost", State: store.IncidentResolved, Text: "the agent is back"},
	} {
		if err := ws.AppendIncident("shop", inc); err != nil {
			t.Fatalf("AppendIncident: %v", err)
		}
	}

	snap, err := Load(context.Background(), ws, testHarnesses, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	p := snap.Projects[0]
	if p.ClosedCrews != 0 {
		t.Fatalf("ClosedCrews = %d, want 0: an incident never closes a crew", p.ClosedCrews)
	}
	got := map[string]CrewStatus{}
	attention := map[string]Field[Attention]{}
	for _, c := range p.Crews {
		got[c.CrewID] = c.Status
		attention[c.CrewID] = c.Attention
	}
	want := map[string]CrewStatus{"k1": CrewBlocked, "k2": CrewWorking, "k3": CrewWorking}
	for id, wantState := range want {
		if got[id] != wantState {
			t.Errorf("crew %s = %q, want %q", id, got[id], wantState)
		}
	}
	// And `blocked` is attention, with a sentence naming the observer.
	if a := attention["k1"]; a.State != Known || a.Value.Kind != AttentionBlocked {
		t.Fatalf("k1 attention = %+v, want a Known blocked attention", a)
	}
	if a := attention["k3"]; a.State == Known {
		t.Fatalf("k3 attention = %+v, want none: it is simply working", a)
	}
}

// Load reads only `.mate/`'s flat files; token usage lives in the derived
// `.mate/mate.db` (mvp.md M5 task 27), so both the Mate and a Crew must
// come back with Tokens Absent, exactly the way Health does - the Console's
// wiring fills it in afterwards, never this package.
func TestLoadLeavesTokensAbsent(t *testing.T) {
	ws := newWorkspace(t, "shop")
	if err := ws.WriteMateMeta("shop", map[string]string{"harness": "claude"}); err != nil {
		t.Fatalf("write mate meta: %v", err)
	}
	if err := ws.WriteCrewMeta("shop", "k3", map[string]string{"state": "spawned"}); err != nil {
		t.Fatalf("write crew meta: %v", err)
	}

	snap, err := Load(context.Background(), ws, testHarnesses, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	p := snap.Projects[0]
	if p.Mate.Tokens.State != Absent {
		t.Fatalf("Mate.Tokens.State = %v, want Absent", p.Mate.Tokens.State)
	}
	if len(p.Crews) != 1 || p.Crews[0].Tokens.State != Absent {
		t.Fatalf("Crew.Tokens.State = %+v, want Absent", p.Crews)
	}
}

// TestLoadDoesNotBlockOnABudgetIncident: decision 2026-09-20 (mvp.md section
// 4b) - a `budget` incident is real and stays in the box, but it must not
// displace a crew's own declared state the way `stale`/`runtime_lost` do.
func TestLoadDoesNotBlockOnABudgetIncident(t *testing.T) {
	ws := newWorkspace(t, "shop")
	if err := ws.WriteCrewMeta("shop", "k9", map[string]string{"state": "spawned"}); err != nil {
		t.Fatalf("write crew meta: %v", err)
	}
	if err := ws.AppendStatus("shop", "k9", "working: burning through the task"); err != nil {
		t.Fatalf("append status: %v", err)
	}
	if err := ws.AppendIncident("shop", store.IncidentEntry{
		Time: time.Now(), Crew: "k9", Kind: "budget", State: store.IncidentOpen,
		Text: "620,000 tokens of 500,000 tokens",
	}); err != nil {
		t.Fatalf("AppendIncident: %v", err)
	}

	snap, err := Load(context.Background(), ws, testHarnesses, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var got CrewStatus
	for _, c := range snap.Projects[0].Crews {
		if c.CrewID == "k9" {
			got = c.Status
		}
	}
	if got != CrewWorking {
		t.Fatalf("k9 status = %q with only a budget incident open, want %q (never blocked)", got, CrewWorking)
	}
}

// TestCrewStateOfBlocksOnAnOpenIncident: `blocked` is the observer's state
// and the only way in is an unresolved incident, which outranks the crew's
// own last verb but never a terminal meta state (mvp.md section 4b).
func TestCrewStateOfBlocksOnAnOpenIncident(t *testing.T) {
	spawned := map[string]string{"state": "spawned"}
	if got := CrewStateOf(spawned, true, "working"); got != CrewBlocked {
		t.Errorf("open incident over working = %q, want %q", got, CrewBlocked)
	}
	if got := CrewStateOf(spawned, false, "working"); got != CrewWorking {
		t.Errorf("no incident = %q, want %q", got, CrewWorking)
	}
	closed := map[string]string{"state": "finished", "stopped_at": "2026-09-18T10:00:00Z"}
	if got := CrewStateOf(closed, true, "working"); got != CrewFinished {
		t.Errorf("open incident over a closed crew = %q, want %q", got, CrewFinished)
	}
}

// TestLoadPicksUpAProjectRegisteredAfterOpen: the Console's 'r' is meant to
// show a project another process just added, so every load re-reads
// workspace.yaml rather than answering from the config cached at Open.
func TestLoadPicksUpAProjectRegisteredAfterOpen(t *testing.T) {
	ws := newWorkspace(t, "shop")
	other, err := store.Open(ws.Root())
	if err != nil {
		t.Fatalf("open workspace again: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(ws.Root(), "blog", "blog", ".git"), 0o755); err != nil {
		t.Fatalf("create repo: %v", err)
	}
	if err := other.AddProject("blog", store.ProjectConfig{Repos: []store.RepoConfig{{Path: "blog/blog"}}}); err != nil {
		t.Fatalf("add project: %v", err)
	}
	snap, err := Load(context.Background(), ws, testHarnesses, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(snap.Projects) != 2 {
		t.Fatalf("projects = %+v, want the newly registered one too", snap.Projects)
	}
}

// TestLoadHidesClosedCrewsAndCountsThem: closing is the decision that ends
// a task and it is `mate crew stop`'s `state=finished|failed`, not the
// crew's own report (2026-09-18). A crew that said `wait-mate` is still a
// row; a closed one is not, and ClosedCrews says how many were dropped.
func TestLoadHidesClosedCrewsAndCountsThem(t *testing.T) {
	ws := newWorkspace(t, "shop")
	if err := ws.WriteCrewMeta("shop", "k1", map[string]string{
		"task": "ship", "agent": "crew-k1", "pane": "w1:p2", "state": "spawned"}); err != nil {
		t.Fatalf("write crew meta: %v", err)
	}
	if err := ws.AppendStatus("shop", "k1", "wait-mate: ready in branch mate/k1"); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := ws.WriteCrewMeta("shop", "k9", map[string]string{
		"task": "scout", "state": "finished", "stopped_at": "2026-09-18T10:18:34Z", "teardown": "clean"}); err != nil {
		t.Fatalf("write crew meta: %v", err)
	}
	snap, err := Load(context.Background(), ws, testHarnesses, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	p := snap.Projects[0]
	if len(p.Crews) != 1 || p.Crews[0].CrewID != "k1" {
		t.Fatalf("crews = %+v, want only the open k1", p.Crews)
	}
	if p.Crews[0].Status != CrewWaitMate || p.Crews[0].Closed {
		t.Fatalf("k1 = status %q closed %v, want wait-mate and open", p.Crews[0].Status, p.Crews[0].Closed)
	}
	if p.ClosedCrews != 1 {
		t.Fatalf("closed = %d, want 1", p.ClosedCrews)
	}
}

// TestLoadFillsUpdatedFromTheBox: the UPDATED column is the newest box
// entry for the row - a crew's last status line or message, the Mate's
// last typed or sent line - and Absent with a reason when there is none
// (2026-09-19: the column was blank for every row).
func TestLoadFillsUpdatedFromTheBox(t *testing.T) {
	ws := newWorkspace(t, "shop")
	if err := ws.WriteCrewMeta("shop", "k1", map[string]string{"task": "ship", "agent": "crew-k1", "pane": "w1:p2", "state": "spawned"}); err != nil {
		t.Fatal(err)
	}
	snap, err := Load(context.Background(), ws, testHarnesses, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := snap.Projects[0].Crews[0].LastEvent; got.State != Absent {
		t.Fatalf("a silent crew's last event = %+v, want Absent", got)
	}
	if err := ws.AppendStatus("shop", "k1", "working: reading the brief"); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 19, 3, 30, 0, 0, time.UTC)
	if err := ws.AppendSent("shop", store.SentEntry{Time: at, Source: "user", Target: "mate", Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	if snap, err = Load(context.Background(), ws, testHarnesses, nil); err != nil {
		t.Fatalf("load: %v", err)
	}
	p := snap.Projects[0]
	if got := p.Crews[0].LastEvent; got.State != Known || got.Value.EventType != "status" || got.Value.OccurredAt.IsZero() {
		t.Fatalf("crew last event = %+v, want the status line's time", got)
	}
	if got := p.Mate.LastEvent; got.State != Known || !got.Value.OccurredAt.Equal(at) {
		t.Fatalf("mate last event = %+v, want the line typed at %s", got, at)
	}
}

// The snapshot carries the workspace's default Mate harness, resolved
// against the catalog: workspace.yaml's `mate_harness`, the catalog's
// default when the key is missing, and "" for a kind nothing can start.
func TestLoadCarriesTheWorkspaceDefaultMateHarness(t *testing.T) {
	for _, tc := range []struct {
		name string
		line string // replaces "mate_harness: claude" in workspace.yaml
		want HarnessKind
	}{
		{"claude", "mate_harness: claude", HarnessKind("claude")},
		{"codex", "mate_harness: codex", HarnessKind("codex")},
		{"missing key", "", testHarnesses.MateDefault},
		{"unknown kind", "mate_harness: gemini", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := newWorkspace(t, "shop")
			raw, err := os.ReadFile(ws.WorkspaceFile())
			if err != nil {
				t.Fatalf("read workspace.yaml: %v", err)
			}
			edited := strings.Replace(string(raw), "mate_harness: claude", tc.line, 1)
			if !strings.Contains(string(raw), "mate_harness: claude") {
				t.Fatalf("setup: workspace.yaml has no mate_harness line:\n%s", raw)
			}
			if err := os.WriteFile(ws.WorkspaceFile(), []byte(edited), 0o644); err != nil {
				t.Fatalf("write workspace.yaml: %v", err)
			}
			snap, err := Load(context.Background(), ws, testHarnesses, nil)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if snap.Workspace.State != Known || snap.Workspace.Value.MateHarness != tc.want {
				t.Fatalf("workspace field = %+v, want mate harness %q", snap.Workspace, tc.want)
			}
		})

	}
}

// A headless Mate's open news reach the snapshot from `mate/news.jsonl`,
// newest first: Absent before the Mate opens one, a closed news gone.
func TestLoadReadsTheMatesOpenNews(t *testing.T) {
	ws := newWorkspace(t, "shop")
	news := func() Field[[]NewsItem] {
		t.Helper()
		snap, err := Load(context.Background(), ws, testHarnesses, nil)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		return snap.Projects[0].Mate.News
	}
	if got := news(); got.State != Absent {
		t.Fatalf("News without a log = %+v, want Absent", got)
	}
	if err := os.MkdirAll(ws.MateDir("shop"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := matehost.NewsPath(ws.MateDir("shop"))
	at := time.Date(2026, 10, 16, 8, 0, 0, 0, time.UTC)
	for i, title := range []string{"Epic 1 xong", "Duyệt 12 điểm", "Vượt ngân sách"} {
		if _, err := matehost.OpenNews(path, matehost.News{Title: title, Crew: "e1"}, at.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if err := matehost.CloseNews(path, 1, "ok", false, at.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got := news()
	if !got.IsKnown() || len(got.Value) != 2 || got.Value[0].ID != 3 || got.Value[1].ID != 2 || !got.Value[0].Owed || got.Value[0].Crew != "e1" {
		t.Fatalf("News = %+v, want #3 then #2, both owed", got)
	}
}

// The Mate's work board reaches the snapshot with every row, okayed ones
// too, latest move first, and its notes; no log at all is Absent.
func TestLoadReadsTheMatesWorkBoard(t *testing.T) {
	ws := newWorkspace(t, "shop")
	work := func() Field[[]WorkItem] {
		t.Helper()
		snap, err := Load(context.Background(), ws, testHarnesses, nil)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		return snap.Projects[0].Mate.Work
	}
	if got := work(); got.State != Absent {
		t.Fatalf("Work without a log = %+v, want Absent", got)
	}
	if err := os.MkdirAll(ws.MateDir("shop"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := matehost.WorkPath(ws.MateDir("shop"))
	at := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	for i, title := range []string{"Epic 1", "Epic 2", "Tidy the docs"} {
		kind := matehost.WorkAssigned
		if i == 2 {
			kind = matehost.WorkProposed
		}
		if _, err := matehost.AddWork(path, kind, title, "", at.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if err := matehost.DoneWork(path, 1, "PR #9 merged", at.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := matehost.OkayWork(path, 1, at.Add(11*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := matehost.WaitWork(path, 2, 7, "which account?", at.Add(12*time.Minute)); err != nil {
		t.Fatal(err)
	}
	got := work()
	if !got.IsKnown() || len(got.Value) != 3 {
		t.Fatalf("Work = %+v, want three rows", got)
	}
	v := got.Value
	if v[0].ID != 2 || v[0].Status != "waiting" || v[0].Topic != 7 || v[0].Last != "which account?" {
		t.Fatalf("latest row = %+v, want #2 waiting on topic 7", v[0])
	}
	if v[1].ID != 1 || v[1].Status != "okayed" || v[1].Open() || len(v[1].Notes) != 3 || v[1].Notes[1].Text != "PR #9 merged" {
		t.Fatalf("okayed row = %+v, want #1 okayed with three notes", v[1])
	}
	if v[2].ID != 3 || v[2].Kind != "proposed" || v[2].Status != "working" {
		t.Fatalf("oldest row = %+v, want #3 proposed, working", v[2])
	}
	forYou, working, waiting, okayed := MateNode{Work: got}.WorkCounts()
	if forYou != 0 || working != 1 || waiting != 1 || okayed != 1 {
		t.Fatalf("counts = %d %d %d %d, want 0 1 1 1", forYou, working, waiting, okayed)
	}
}

// The tool keys cmd/mate hands Load reach the snapshot as data, in the
// order given, and the snapshot holds its own copy.
func TestLoadCarriesCommandBindings(t *testing.T) {
	ws := newWorkspace(t, "shop")
	tools := []CommandBinding{
		{Key: "e", Label: "report", Scope: "crew", Role: "review"},
		{Key: "t", Label: "tasks", Scope: "project", Role: "tasks"},
	}
	snap, err := Load(context.Background(), ws, testHarnesses, tools)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !reflect.DeepEqual(snap.Commands, tools) {
		t.Fatalf("Commands = %+v, want %+v", snap.Commands, tools)
	}
	tools[0].Key = "z"
	if snap.Commands[0].Key != "e" {
		t.Fatal("the snapshot shares the caller's slice")
	}
	if snap, err = Load(context.Background(), ws, testHarnesses, nil); err != nil || snap.Commands != nil {
		t.Fatalf("Load with no tools: Commands = %+v, %v; want none", snap.Commands, err)
	}
}

// unreadable makes path unreadable for the rest of the test, so a test can
// tell whether Load opened it.
func unreadable(t *testing.T, path string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file, so this cannot tell whether Load opened it")
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
}

// TestLoadDoesNotReadAClosedCrewsStatus: a terminal meta outranks every
// other input to a crew's state (mvp.md section 4b), so a closed crew's
// `.status` cannot change what Load reports and is not read for its state.
// A workspace keeps every crew it ever closed; reading each of their status
// files on every console refresh was most of what a refresh cost.
func TestLoadDoesNotReadAClosedCrewsStatus(t *testing.T) {
	ws := newWorkspace(t, "shop")
	if err := ws.WriteCrewMeta("shop", "k9", map[string]string{"task": "scout", "state": "finished"}); err != nil {
		t.Fatal(err)
	}
	if err := ws.AppendStatus("shop", "k9", "working: mid-turn when it was closed"); err != nil {
		t.Fatal(err)
	}
	unreadable(t, ws.CrewStatus("shop", "k9"))
	if err := ws.WriteCrewMeta("shop", "k1", map[string]string{"task": "ship", "state": "spawned"}); err != nil {
		t.Fatal(err)
	}
	if err := ws.AppendStatus("shop", "k1", "wait-mate: ready in branch mate/k1"); err != nil {
		t.Fatal(err)
	}

	snap, err := Load(context.Background(), ws, testHarnesses, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, w := range snap.Warnings {
		if w.Row.ID == "k9" {
			t.Errorf("warning %+v: a closed crew's status was read", w)
		}
	}
	p := snap.Projects[0]
	if p.ClosedCrews != 1 {
		t.Errorf("ClosedCrews = %d, want 1", p.ClosedCrews)
	}
	if len(p.Crews) != 1 || p.Crews[0].CrewID != "k1" || p.Crews[0].Status != CrewWaitMate {
		t.Fatalf("crews = %+v, want only k1 at wait-mate", p.Crews)
	}
}

// TestLoadReadsTheLastVerbTheSameWhetherOrNotTheBoxLoads: a crew's last
// verb comes out of the box view Load already read, and out of the crew's
// own status file when the box could not be read. Both must give the state
// the status file says, under the same parse rules.
func TestLoadReadsTheLastVerbTheSameWhetherOrNotTheBoxLoads(t *testing.T) {
	cases := []struct {
		lines []string
		want  CrewStatus
	}{
		{[]string{"wait-mate: ready in branch mate/k1"}, CrewWaitMate},
		{[]string{"working: a", "oops"}, CrewWorking},
		{[]string{"working: a", "needs-decision: A or B?", "   "}, CrewNeedsDecision},
		{[]string{"needs-decision: a", "working: b", "blocked: legacy"}, CrewNeedsDecision},
		{[]string{"working: a", "done: legacy"}, CrewWaitMate},
		{[]string{"working: a", "failed: legacy"}, CrewWaitMate},
		{[]string{"working: a", "pr-open: https://example.test/pr/1"}, CrewWaitMate},
		{[]string{"hello there"}, CrewSpawned},
		{nil, CrewSpawned},
	}
	for _, boxReadable := range []bool{true, false} {
		for i, tc := range cases {
			ws := newWorkspace(t, "shop")
			if err := ws.WriteCrewMeta("shop", "k1", map[string]string{"task": "ship", "state": "spawned"}); err != nil {
				t.Fatal(err)
			}
			for _, l := range tc.lines {
				if err := ws.AppendStatus("shop", "k1", l); err != nil {
					t.Fatal(err)
				}
			}
			if !boxReadable {
				// One unreadable status file fails box.Load for the whole
				// project; Load then reads each open crew's file itself.
				if err := ws.WriteCrewMeta("shop", "k9", map[string]string{"state": "finished"}); err != nil {
					t.Fatal(err)
				}
				if err := ws.AppendStatus("shop", "k9", "working: x"); err != nil {
					t.Fatal(err)
				}
				unreadable(t, ws.CrewStatus("shop", "k9"))
			}
			snap, err := Load(context.Background(), ws, testHarnesses, nil)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			p := snap.Projects[0]
			if (p.Box.State == Known) != boxReadable {
				t.Fatalf("box = %+v, want Known=%v", p.Box, boxReadable)
			}
			if len(p.Crews) != 1 || p.Crews[0].Status != tc.want {
				t.Errorf("box readable %v, case %d %q: crews = %+v, want k1 %q", boxReadable, i, tc.lines, p.Crews, tc.want)
			}
		}
	}
}
