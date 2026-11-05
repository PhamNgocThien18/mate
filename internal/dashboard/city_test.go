package dashboard

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// citySnapshot is actionSnapshot with what the City reads besides the
// sheet: an open question from the fixture crew, a live binding, the
// project's own actions, and the harnesses a Mate can be created with.
func citySnapshot() query.Snapshot {
	snap := actionSnapshot()
	p := &snap.Projects[0]
	p.Actions = []query.ActionAvailability{{Action: "remove_project", Available: true}}
	p.Daemon = query.AutoDaemon{Sends: 2, Notice: "mihuv2 is manual"}
	p.Crews[0].Binding = query.KnownField(query.BindingValue{Status: query.BindingActive})
	p.Box = query.KnownField(query.BoxView{Inbox: []query.BoxEntry{
		{Crew: fixtureCrew, Verb: "needs-decision", Text: "Stripe or bank?", Resolve: "resolve: k3 asks Stripe or bank?", AssignKey: "crews/k3.status@120"},
		{Crew: fixtureCrew, Verb: "blocked", Text: "old", Resolve: "resolve: old", AssignKey: "crews/k3.status@40", Handed: true},
	}})
	snap.Workspace = query.KnownField(query.WorkspaceValue{MateHarness: "codex"})
	snap.Harnesses = []query.Harness{{Kind: "claude", Mate: true}, {Kind: "codex", Mate: true}, {Kind: "pi"}}
	return snap
}

func cityServer(t *testing.T, f *fixture, rec *actRecorder, notice bool) *httptest.Server {
	t.Helper()
	snap := citySnapshot()
	server, err := New(Options{Workspace: f.ws, DB: f.read, Deps: Deps{
		Now:      func() time.Time { return fixtureNow },
		Snapshot: func(context.Context) (query.Snapshot, error) { return snap, nil },
		Act:      rec.act,
		Notice:   notice,
	}})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestCityButtonsBesideTheSheet(t *testing.T) {
	f := newFixture(t)
	ts := cityServer(t, f, &actRecorder{}, false)
	resp, err := ts.Client().Get(ts.URL + "/api/projects/" + fixtureProject + "/actions")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got ActionsResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.MateMore) != 3 {
		t.Fatalf("mate_more = %+v", got.MateMore)
	}
	if ok := got.MateMore[2]; ok.Action != "okay" || !ok.Available {
		t.Errorf("okay = %+v, want offered without a Mate: it writes the board's log", ok)
	}
	if say := got.MateMore[0]; say.Action != "say" || say.Available || !strings.Contains(say.Reason, "not headless") {
		t.Errorf("say = %+v, want refused: the fixture Mate is not headless", say)
	}
	if rm := got.MateMore[1]; rm.Action != "remove_project" || !rm.Available || !strings.Contains(rm.Confirm, "1 running crew") {
		t.Errorf("remove = %+v", rm)
	}
	more := got.CrewMore[fixtureCrew]
	if len(more) != 4 {
		t.Fatalf("crew_more = %+v", more)
	}
	if peek := more[0]; !peek.Available {
		t.Errorf("peek = %+v, want offered on a live binding", peek)
	}
	if n := more[1]; n.Available || n.Reason != "Jev is not configured" {
		t.Errorf("explain = %+v", n)
	}
	if a := more[2]; !a.Available || a.Key != "crews/k3.status@120" {
		t.Errorf("assign = %+v", a)
	}
	if a := more[3]; a.Available || a.Reason != "the Mate already has it" {
		t.Errorf("handed assign = %+v", a)
	}
	if len(got.Harnesses) != 2 || got.Harnesses[0].Kind != "codex" || !got.Harnesses[0].Default {
		t.Errorf("harnesses = %+v, want the Mate harnesses with the default first", got.Harnesses)
	}
	if got.Daemon == nil || got.Daemon.Sends != 2 || got.Daemon.Notice == "" {
		t.Errorf("daemon = %+v", got.Daemon)
	}
}

func TestCityPressesGoToTheConsole(t *testing.T) {
	f := newFixture(t)
	rec := &actRecorder{}
	ts := cityServer(t, f, rec, true)

	// assign carries the server's own resolve line, never the page's: it
	// is the console's resolve.
	status, e, ok := post(t, ts, `{"action":"assign","project":"`+fixtureProject+`","crew":"`+fixtureCrew+`","key":"crews/k3.status@120","line":"rm -rf /","text":"rm -rf /"}`, nil)
	if status != 200 || ok.Line != "did resolve" {
		t.Fatalf("assign = %d %+v", status, e)
	}
	if got := rec.got[len(rec.got)-1]; got.Input != "resolve: k3 asks Stripe or bank?" || got.Key != "crews/k3.status@120" || got.Crew != fixtureCrew {
		t.Errorf("assign request = %+v", got)
	}
	if status, e, _ := post(t, ts, `{"action":"assign","project":"`+fixtureProject+`","crew":"`+fixtureCrew+`","key":"crews/k3.status@40"}`, nil); status != 409 || !strings.Contains(e.Error, "already has it") {
		t.Errorf("handed assign = %d %+v", status, e)
	}
	if status, _, _ := post(t, ts, `{"action":"assign","project":"`+fixtureProject+`","crew":"`+fixtureCrew+`","key":"made-up"}`, nil); status != 400 {
		t.Errorf("unknown entry = %d, want 400", status)
	}

	// The chat's actions share say's availability: refused, nothing sent.
	long := strings.Repeat("x", 9000)
	for _, body := range []string{
		`{"action":"say","project":"` + fixtureProject + `","text":"` + long + `"}`,
		`{"action":"answer","project":"` + fixtureProject + `","news":1,"text":"[a]"}`,
		`{"action":"fork","project":"` + fixtureProject + `","news":1,"text":"why?"}`,
	} {
		if status, e, _ := post(t, ts, body, nil); status != 409 || !strings.Contains(e.Error, "not headless") {
			t.Errorf("%.40s = %d %+v", body, status, e)
		}
	}

	// Adding a project names one that is not there yet.
	if status, e, _ := post(t, ts, `{"action":"add_project","project":"`+fixtureProject+`"}`, nil); status != 409 || !strings.Contains(e.Error, "already in the workspace") {
		t.Errorf("add existing = %d %+v", status, e)
	}
	if status, e, _ := post(t, ts, `{"action":"add_project","project":"Bad Name"}`, nil); status != 409 {
		t.Errorf("add bad name = %d %+v", status, e)
	}
	status, e, ok = post(t, ts, `{"action":"add_project","project":"landing","repo":"../landing"}`, nil)
	if status != 200 || ok.Line != "did onboard" {
		t.Fatalf("add = %d %+v", status, e)
	}
	if got := rec.got[len(rec.got)-1]; got.TargetKind != "workspace" || got.Input != "landing" || got.Repo != "../landing" {
		t.Errorf("add request = %+v", got)
	}
	if status, e, _ := post(t, ts, `{"action":"peek","project":"`+fixtureProject+`","crew":"`+fixtureCrew+`"}`, nil); status != 200 {
		t.Errorf("peek = %d %+v", status, e)
	}
}

func TestAllActionsAreOneSnapshot(t *testing.T) {
	f := newFixture(t)
	snap := citySnapshot()
	loads := 0
	server, err := New(Options{Workspace: f.ws, DB: f.read, Deps: Deps{
		Now:      func() time.Time { return fixtureNow },
		Snapshot: func(context.Context) (query.Snapshot, error) { loads++; return snap, nil },
		Act:      (&actRecorder{}).act,
	}})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()
	resp, err := ts.Client().Get(ts.URL + "/api/actions")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got AllActionsResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if loads != 1 {
		t.Errorf("snapshot read %d times for one answer", loads)
	}
	one, ok := got.Projects[fixtureProject]
	if !ok || len(one.Mate) != 5 || len(one.CrewMore[fixtureCrew]) != 4 {
		t.Fatalf("projects = %+v", got.Projects)
	}
}
