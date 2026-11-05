package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/query"
)

// actionSnapshot is the console's snapshot for the fixture project: a
// running Mate and the fixture crew, handed back, with the availability the
// query layer would publish for them.
func actionSnapshot() query.Snapshot {
	avail := func(pairs ...any) []query.ActionAvailability {
		var out []query.ActionAvailability
		for i := 0; i < len(pairs); i += 2 {
			out = append(out, query.ActionAvailability{Action: pairs[i].(string), Available: pairs[i+1].(bool), Reason: "recorded " + pairs[i].(string)})
		}
		return out
	}
	return query.Snapshot{Projects: []query.ProjectNode{{
		ProjectID: fixtureProject, Name: fixtureProject, Mode: query.ModeManual,
		Mate: query.MateNode{Actions: avail("start", false, "stop", true, "resume", false, "onboard", false,
			"restart_mate", true, "clear_composer", true)},
		Crews: []query.CrewNode{{
			CrewID: fixtureCrew, Status: query.CrewWaitMate,
			Worktree: query.KnownField(query.WorktreeValue{Branch: fixtureBranch}),
			Repo:     query.KnownField(query.RepoValue{DefaultBranch: "main"}),
			Actions:  avail("stop", true, "repair", false, "diff", true, "restart_crew", true, "merge", true),
		}},
	}}}
}

type actRecorder struct {
	mu  sync.Mutex
	got []coreapi.ActionRequest
	err error
}

// fail makes every later action return err.
func (a *actRecorder) fail(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.err = err
}

func (a *actRecorder) act(_ context.Context, req coreapi.ActionRequest) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.got = append(a.got, req)
	if a.err != nil {
		return "", a.err
	}
	return "did " + string(req.Action), nil
}

func actingServer(t *testing.T, f *fixture, rec *actRecorder) *httptest.Server {
	t.Helper()
	snap := actionSnapshot()
	server, err := New(Options{Workspace: f.ws, DB: f.read, Deps: Deps{
		Now:      func() time.Time { return fixtureNow },
		Snapshot: func(context.Context) (query.Snapshot, error) { return snap, nil },
		Act:      rec.act,
	}})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func post(t *testing.T, ts *httptest.Server, body string, header map[string]string) (int, ErrorResponse, ActionResult) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/actions", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	var e ErrorResponse
	var ok ActionResult
	_ = json.Unmarshal(raw, &e)
	_ = json.Unmarshal(raw, &ok)
	return resp.StatusCode, e, ok
}

func TestActionsAreTheConsoleSheetsWithItsAvailability(t *testing.T) {
	f := newFixture(t)
	ts := actingServer(t, f, &actRecorder{})
	resp, err := ts.Client().Get(ts.URL + "/api/projects/" + fixtureProject + "/actions")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got ActionsResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.ReadOnly != "" {
		t.Fatalf("read_only = %q on the console's dashboard", got.ReadOnly)
	}
	var mate []string
	for _, b := range got.Mate {
		mate = append(mate, b.Action)
	}
	if strings.Join(mate, " ") != "start stop restart_mate mode clear_composer" {
		t.Fatalf("mate buttons = %v", mate)
	}
	if start := got.Mate[0]; start.Available || start.Reason != "recorded start" {
		t.Errorf("start = %+v, want refused with the query layer's reason", start)
	}
	if mode := got.Mate[3]; mode.Label != "Mode → auto" || !mode.Available {
		t.Errorf("mode = %+v, want the flip to auto offered on a manual project", mode)
	}
	if got.Mate[1].Confirm == "" || got.Mate[4].Confirm != "" {
		t.Errorf("stop must ask first and clear composer must not: %+v %+v", got.Mate[1], got.Mate[4])
	}
	crew := got.Crews[fixtureCrew]
	if len(crew) != 4 {
		t.Fatalf("crew buttons = %+v, want stop, restart, merge, diff", crew)
	}
	merge := crew[2]
	if merge.Action != "merge" || !merge.Available || !strings.Contains(merge.Confirm, "Merge "+fixtureBranch+" into main") {
		t.Errorf("merge = %+v, want it offered and naming both branches", merge)
	}
}

func TestADashboardWithoutTheConsoleIsReadOnly(t *testing.T) {
	f := newFixture(t)
	var got ActionsResponse
	f.get(t, "/api/projects/"+fixtureProject+"/actions", http.StatusOK, &got)
	if got.ReadOnly == "" || len(got.Mate) != 0 {
		t.Fatalf("actions = %+v, want read-only and no buttons", got)
	}
	resp, err := f.http.Client().Post(f.http.URL+"/api/actions", "application/json", strings.NewReader(`{"action":"stop","project":"`+fixtureProject+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST on a read-only dashboard = %d, want 403", resp.StatusCode)
	}
}

func TestAPressRunsTheConsolesActionAndAnswersItsLine(t *testing.T) {
	f := newFixture(t)
	rec := &actRecorder{}
	ts := actingServer(t, f, rec)
	status, e, ok := post(t, ts, `{"action":"merge","project":"`+fixtureProject+`","crew":"`+fixtureCrew+`"}`, nil)
	if status != http.StatusOK || ok.Line != "did merge" {
		t.Fatalf("merge = %d %q %+v", status, ok.Line, e)
	}
	want := coreapi.ActionRequest{Action: coreapi.ActionMerge, Target: fixtureProject, TargetKind: "crew", Crew: fixtureCrew, HeadlessOnly: true}
	if len(rec.got) != 1 || rec.got[0] != want {
		t.Fatalf("act got %+v, want %+v", rec.got, want)
	}

	rec.err = errors.New("send: the composer is not empty")
	status, e, _ = post(t, ts, `{"action":"clear_composer","project":"`+fixtureProject+`"}`, nil)
	if status != http.StatusUnprocessableEntity || e.Error != "send: the composer is not empty" {
		t.Fatalf("a failed action = %d %+v, want 422 with the service's own words", status, e)
	}
}

// A press is checked against a snapshot read anew (Deps.FreshSnapshot), not
// the one the core hands out for a couple of seconds: a stop drawn while the
// Mate ran is refused once a fresh read says it no longer does, and the
// buttons a GET draws still come from the handed-out one (CON-208).
func TestAPressIsCheckedAgainstAFreshSnapshot(t *testing.T) {
	f := newFixture(t)
	rec := &actRecorder{}
	handedOut := actionSnapshot()
	fresh := actionSnapshot()
	fresh.Projects[0].Mate.Actions = []query.ActionAvailability{{Action: "stop", Reason: "the Mate is not running"}}
	var freshReads atomic.Int32
	server, err := New(Options{Workspace: f.ws, DB: f.read, Deps: Deps{
		Now:      func() time.Time { return fixtureNow },
		Snapshot: func(context.Context) (query.Snapshot, error) { return handedOut, nil },
		FreshSnapshot: func(context.Context) (query.Snapshot, error) {
			freshReads.Add(1)
			return fresh, nil
		},
		Act: rec.act,
	}})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	status, e, _ := post(t, ts, `{"action":"stop","project":"`+fixtureProject+`"}`, nil)
	if status != http.StatusConflict || !strings.Contains(e.Error, "the Mate is not running") {
		t.Fatalf("stop = %d %+v, want 409 with the fresh read's reason", status, e)
	}
	if freshReads.Load() != 1 || len(rec.got) != 0 {
		t.Fatalf("%d fresh reads, acted %+v; want one read and nothing run", freshReads.Load(), rec.got)
	}
	resp, err := http.Get(ts.URL + "/api/projects/" + fixtureProject + "/actions")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out ActionsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if freshReads.Load() != 1 || len(out.Mate) < 2 || out.Mate[1].Action != "stop" || !out.Mate[1].Available {
		t.Fatalf("GET drew %+v after %d fresh reads; want the handed-out snapshot's stop", out.Mate, freshReads.Load())
	}
}

func TestAPressIsRefusedBeforeAnythingRuns(t *testing.T) {
	f := newFixture(t)
	rec := &actRecorder{}
	ts := actingServer(t, f, rec)
	for _, tc := range []struct {
		name   string
		body   string
		header map[string]string
		status int
	}{
		{"unavailable", `{"action":"start","project":"` + fixtureProject + `"}`, nil, http.StatusConflict},
		{"not offered", `{"action":"retry","project":"` + fixtureProject + `"}`, nil, http.StatusBadRequest},
		{"remove not allowed by the snapshot", `{"action":"remove_project","project":"` + fixtureProject + `"}`, nil, http.StatusConflict},
		{"repair is not wired", `{"action":"repair","project":"` + fixtureProject + `","crew":"` + fixtureCrew + `"}`, nil, http.StatusBadRequest},
		{"unknown project", `{"action":"stop","project":"nosuch"}`, nil, http.StatusNotFound},
		{"closed crew", `{"action":"stop","project":"` + fixtureProject + `","crew":"gone"}`, nil, http.StatusNotFound},
		{"another origin", `{"action":"stop","project":"` + fixtureProject + `"}`, map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"a form", `{"action":"stop","project":"` + fixtureProject + `"}`, map[string]string{"Content-Type": "text/plain"}, http.StatusUnsupportedMediaType},
	} {
		if status, e, _ := post(t, ts, tc.body, tc.header); status != tc.status {
			t.Errorf("%s: status = %d (%+v), want %d", tc.name, status, e, tc.status)
		}
	}
	if len(rec.got) != 0 {
		t.Fatalf("act ran %+v; every press above must be refused first", rec.got)
	}
	if status, _, _ := post(t, ts, `{"action":"stop","project":"`+fixtureProject+`"}`, map[string]string{"Origin": ts.URL}); status != http.StatusOK {
		t.Fatalf("a press from the page's own origin = %d, want 200", status)
	}
}

// The lock on a target is the core's (CON-209): a press the core refuses
// because another action holds the target is a 409 with the core's line
// verbatim, as a press the snapshot does not offer is, and any other error
// stays a 422. internal/core's tests hold the lock itself, and cmd/mate's
// the console and a page racing for one target.
func TestABusyTargetIsAConflict(t *testing.T) {
	f := newFixture(t)
	rec := &actRecorder{err: &coreapi.BusyError{Running: coreapi.ActionRestartMate, Target: fixtureProject}}
	ts := actingServer(t, f, rec)
	status, e, _ := post(t, ts, `{"action":"clear_composer","project":"`+fixtureProject+`"}`, nil)
	want := "Action refused: restart_mate is still running on " + fixtureProject + " · nothing started"
	if status != http.StatusConflict || e.Error != want {
		t.Fatalf("the press = %d %+v, want 409 %q", status, e, want)
	}
	// A core that is stopping (CON-210) refuses the same way.
	rec.fail(fmt.Errorf("wrapped: %w", coreapi.ErrStopping))
	if status, e, _ := post(t, ts, `{"action":"clear_composer","project":"`+fixtureProject+`"}`, nil); status != http.StatusConflict || e.Error != "mate core is stopping · nothing started" {
		t.Fatalf("a press while the core stops = %d %+v, want 409 with the core's line", status, e)
	}
	rec.fail(errors.New("the composer could not be read"))
	if status, e, _ := post(t, ts, `{"action":"clear_composer","project":"`+fixtureProject+`"}`, nil); status != http.StatusUnprocessableEntity || e.Error != "the composer could not be read" {
		t.Fatalf("a failed action = %d %+v, want 422 with its error", status, e)
	}
}

// TestAPressIsTheConsoleSheetsRequest: cmd/mate's consoleAction branches
// on the target kind, so a page's press reaches Act as the request the
// console's sheet builds for the same action (CON-141). The chat keeps its
// own words, with the text in Input and the news in News, and the work
// board's tick is the work sheet's okay_work with the row in Input
// (CON-206). A page's words go nowhere else: an assign hands over the
// server's resolve line, and every other press drops the text. Every press
// also says that a page cannot show a pane Mate (CON-205).
func TestAPressIsTheConsoleSheetsRequest(t *testing.T) {
	cases := []struct {
		in   ActionRequest
		want coreapi.ActionRequest
	}{
		{ActionRequest{Action: "start", Project: "shop"}, coreapi.ActionRequest{Action: coreapi.ActionStart, Target: "shop", TargetKind: "mate"}},
		{ActionRequest{Action: "resume", Project: "shop"}, coreapi.ActionRequest{Action: coreapi.ActionResume, Target: "shop", TargetKind: "mate"}},
		{ActionRequest{Action: "onboard", Project: "shop"}, coreapi.ActionRequest{Action: coreapi.ActionOnboard, Target: "shop", TargetKind: "project-mate"}},
		{ActionRequest{Action: "stop", Project: "shop"}, coreapi.ActionRequest{Action: coreapi.ActionStop, Target: "shop", TargetKind: "mate"}},
		{ActionRequest{Action: "restart_mate", Project: "shop"}, coreapi.ActionRequest{Action: coreapi.ActionRestartMate, Target: "shop", TargetKind: "project"}},
		{ActionRequest{Action: "clear_composer", Project: "shop"}, coreapi.ActionRequest{Action: coreapi.ActionClearComposer, Target: "shop", TargetKind: "project"}},
		{ActionRequest{Action: "mode", Project: "shop"}, coreapi.ActionRequest{Action: coreapi.ActionMode, Target: "shop", TargetKind: "project"}},
		{ActionRequest{Action: "stop", Project: "shop", Crew: "buy"}, coreapi.ActionRequest{Action: coreapi.ActionStop, Target: "shop", TargetKind: "crew", Crew: "buy"}},
		{ActionRequest{Action: "merge", Project: "shop", Crew: "buy"}, coreapi.ActionRequest{Action: coreapi.ActionMerge, Target: "shop", TargetKind: "crew", Crew: "buy"}},
		{ActionRequest{Action: "restart_crew", Project: "shop", Crew: "buy"}, coreapi.ActionRequest{Action: coreapi.ActionRestartCrew, Target: "shop", TargetKind: "crew", Crew: "buy"}},
		{ActionRequest{Action: "diff", Project: "shop", Crew: "buy"}, coreapi.ActionRequest{Action: coreapi.ActionDiff, Target: "shop", TargetKind: "crew", Crew: "buy"}},
		// The City's (CON-142): the new-project form, the box's [assign],
		// the project's remove, a create with a harness, a peek and Jev.
		{ActionRequest{Action: "add_project", Project: "landing", Repo: "../landing"}, coreapi.ActionRequest{Action: coreapi.ActionOnboard, TargetKind: "workspace", Input: "landing", Repo: "../landing"}},
		{ActionRequest{Action: "add_project", Project: "landing"}, coreapi.ActionRequest{Action: coreapi.ActionOnboard, TargetKind: "workspace", Input: "landing"}},
		{ActionRequest{Action: "assign", Project: "shop", Crew: "buy", Key: "crews/buy.status@9", Line: "resolve: buy asks", Text: "the page's own words"}, coreapi.ActionRequest{Action: coreapi.ActionResolve, Target: "shop", TargetKind: "project", Crew: "buy", Input: "resolve: buy asks", Key: "crews/buy.status@9"}},
		{ActionRequest{Action: "remove_project", Project: "shop"}, coreapi.ActionRequest{Action: coreapi.ActionRemoveProject, Target: "shop", TargetKind: "project"}},
		{ActionRequest{Action: "onboard", Project: "shop", Harness: "codex"}, coreapi.ActionRequest{Action: coreapi.ActionOnboard, Target: "shop", TargetKind: "project-mate", Harness: "codex"}},
		{ActionRequest{Action: "peek", Project: "shop", Crew: "buy"}, coreapi.ActionRequest{Action: coreapi.ActionPeek, Target: "shop", TargetKind: "crew", Crew: "buy"}},
		{ActionRequest{Action: "explain_notice", Project: "shop", Crew: "buy"}, coreapi.ActionRequest{Action: coreapi.ActionNotice, Target: "shop", TargetKind: "crew", Crew: "buy"}},
		{ActionRequest{Action: "stop", Project: "shop", Text: "the page's own words", News: 7, Work: 3}, coreapi.ActionRequest{Action: coreapi.ActionStop, Target: "shop", TargetKind: "mate"}},
		// The chat (CON-142): a say has no news, and an interrupt or a
		// fork_close no text, whatever the page sent.
		{ActionRequest{Action: "say", Project: "shop", Text: "ship the button"}, coreapi.ActionRequest{Action: coreapi.ActionSay, Target: "shop", TargetKind: "mate", Input: "ship the button"}},
		{ActionRequest{Action: "say", Project: "shop", Text: "ship the button", News: 7}, coreapi.ActionRequest{Action: coreapi.ActionSay, Target: "shop", TargetKind: "mate", Input: "ship the button"}},
		{ActionRequest{Action: "interrupt", Project: "shop"}, coreapi.ActionRequest{Action: coreapi.ActionInterrupt, Target: "shop", TargetKind: "mate"}},
		{ActionRequest{Action: "interrupt", Project: "shop", News: 7, Text: "stop"}, coreapi.ActionRequest{Action: coreapi.ActionInterrupt, Target: "shop", TargetKind: "mate", News: 7}},
		{ActionRequest{Action: "answer", Project: "shop", News: 7, Text: "[a] Stripe"}, coreapi.ActionRequest{Action: coreapi.ActionAnswer, Target: "shop", TargetKind: "mate", Input: "[a] Stripe", News: 7}},
		{ActionRequest{Action: "fork", Project: "shop", News: 7, Text: "why Stripe?"}, coreapi.ActionRequest{Action: coreapi.ActionFork, Target: "shop", TargetKind: "mate", Input: "why Stripe?", News: 7}},
		{ActionRequest{Action: "fork_close", Project: "shop", News: 7, Text: "bye"}, coreapi.ActionRequest{Action: coreapi.ActionForkClose, Target: "shop", TargetKind: "mate", News: 7}},
		// The work board's tick (CON-158).
		{ActionRequest{Action: "okay", Project: "shop", Work: 12}, coreapi.ActionRequest{Action: coreapi.ActionOkayWork, Target: "shop", TargetKind: "project", Input: "12"}},
	}
	rows := map[string]bool{}
	for _, tc := range cases {
		rows[tc.in.Action] = true
		got := coreRequest(tc.in)
		if !got.HeadlessOnly {
			t.Errorf("%+v -> %+v, want HeadlessOnly: a page cannot show a pane Mate", tc.in, got)
		}
		got.HeadlessOnly = false
		if got != tc.want {
			t.Errorf("%+v -> %+v, want %+v", tc.in, got, tc.want)
		}
	}

	// Every word a page can press has a row: the buttons the server draws,
	// the three a Mate's first button may be, the chat's and add_project.
	f := newFixture(t)
	s := &Server{ws: f.ws, deps: Deps{Notice: true}}
	node := citySnapshot().Projects[0]
	pressable := map[string]bool{"start": true, "resume": true, "onboard": true, actAddProject: true}
	for word := range talk {
		pressable[word] = true
	}
	buttons := append(mateButtons(node), s.mateMore(node)...)
	for _, c := range node.Crews {
		buttons = append(buttons, append(crewButtons(node.ProjectID, c), s.crewMore(node, c)...)...)
	}
	for _, b := range buttons {
		pressable[b.Action] = true
	}
	for word := range pressable {
		if !rows[word] {
			t.Errorf("a page can press %q and the table has no row for it", word)
		}
	}
}
