package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/timeline"
)

// The API's whole claim is that it repeats the database rather than
// recomputing it. Every test here therefore compares an endpoint's number
// to the view or the helper the CLI reads for the same thing - never to a
// number written down in the test.

func TestWorkspaceCardRepeatsTheViews(t *testing.T) {
	f := newFixture(t)
	var got WorkspaceResponse
	f.get(t, "/api/workspace", http.StatusOK, &got)

	if len(got.Projects) != 1 || got.Projects[0].Name != fixtureProject {
		t.Fatalf("projects = %+v, want one named %s", got.Projects, fixtureProject)
	}
	card := got.Projects[0]
	if card.Mode != "auto" {
		t.Errorf("mode = %q, want auto, the default (CON-115)", card.Mode)
	}
	if card.Mate.Harness != "claude" {
		t.Errorf("mate harness = %q, want claude (actor.harness)", card.Mate.Harness)
	}
	if !card.Mate.Running {
		t.Error("the Mate has a started_at and no stopped_at; it must read as running")
	}

	// Every number against v_now itself.
	wantState, wantSince, wantTokens := f.nowRow(t, timeline.MateActorID(fixtureProject))
	if card.Mate.State != wantState || card.Mate.Since != wantSince || card.Mate.TokensToday != wantTokens {
		t.Errorf("mate card = %+v, want v_now's %q/%q/%d", card.Mate, wantState, wantSince, wantTokens)
	}
	crewState, _, _ := f.nowRow(t, timeline.CrewActorID(fixtureProject, fixtureCrew))
	if crewState == "" {
		crewState = "unknown"
	}
	if card.CrewsByState[crewState] != 1 {
		t.Errorf("crews_by_state = %v, want one crew under %q", card.CrewsByState, crewState)
	}
	if card.InboxWaiting != f.inboxLen(t) {
		t.Errorf("inbox_waiting = %d, want box.Inbox's %d", card.InboxWaiting, f.inboxLen(t))
	}
	if got.LastEventID != f.lastEventID(t) || got.GeneratedAt == "" {
		t.Errorf("envelope = %+v, want last_event_id %d and a generated_at", got.envelope, f.lastEventID(t))
	}
}

func TestProjectTasksEqualTheTaskLedger(t *testing.T) {
	f := newFixture(t)
	var got ProjectResponse
	f.get(t, "/api/projects/"+fixtureProject, http.StatusOK, &got)

	if len(got.Tasks) != 1 {
		t.Fatalf("tasks = %d, want the fixture's one crew", len(got.Tasks))
	}
	task := got.Tasks[0]
	if task.Crew != fixtureCrew {
		t.Fatalf("crew = %q, want %q", task.Crew, fixtureCrew)
	}

	// v_task_ledger is the contract: read the same row the CLI's ledger
	// reads and compare bucket by bucket.
	var turns, in, cacheRead, cacheWrite, out, thinking, asked, waited int64
	var branch string
	if err := f.read.SQL().QueryRow(`
		SELECT turns, input_tokens, cache_read_tokens, cache_write_tokens, output_tokens,
		       thinking_tokens, question_count, waited_ms, branch
		  FROM v_task_ledger WHERE project = ? AND crew = ?`, fixtureProject, fixtureCrew).
		Scan(&turns, &in, &cacheRead, &cacheWrite, &out, &thinking, &asked, &waited, &branch); err != nil {
		t.Fatalf("read v_task_ledger: %v", err)
	}
	if task.Turns != turns {
		t.Errorf("turns = %d, want %d", task.Turns, turns)
	}
	want := Tokens{Input: in, CacheRead: cacheRead, CacheWrite: cacheWrite, Output: out,
		Thinking: thinking, Total: in + cacheRead + cacheWrite + out}
	if task.Tokens != want {
		t.Errorf("tokens = %+v, want %+v", task.Tokens, want)
	}
	if task.QuestionCount != asked || task.WaitedMs != waited || task.Branch != branch {
		t.Errorf("task = %+v, want asked %d, waited %d, branch %q", task, asked, waited, branch)
	}
	if task.Cost != nil {
		t.Errorf("cost = %v, want null: the fixture's model has no price", *task.Cost)
	}

	// tool_count is the one ledger number no view carries: it is summed off
	// `turn`, so it must equal that sum.
	var tools int64
	if err := f.read.SQL().QueryRow(`SELECT COALESCE(SUM(tool_count),0) FROM turn WHERE actor_id = ?`,
		timeline.CrewActorID(fixtureProject, fixtureCrew)).Scan(&tools); err != nil {
		t.Fatalf("sum tool_count: %v", err)
	}
	if task.ToolCount != tools {
		t.Errorf("tool_count = %d, want turn's own sum %d", task.ToolCount, tools)
	}
	if tools == 0 {
		t.Error("the fixture crew ran tools; a ledger reporting none is not reading them")
	}
	if task.Harness != "codex" {
		t.Errorf("harness = %q, want the crew's actor.harness codex", task.Harness)
	}

	// The Mate's block is computed from `turn` because no task row exists
	// for a Mate - the same reason cmd/mate's mateLedgerRow exists.
	var mateIn, mateCacheRead, mateCacheWrite, mateOut int64
	if err := f.read.SQL().QueryRow(`
		SELECT COALESCE(SUM(input_tokens),0), COALESCE(SUM(cache_read_tokens),0),
		       COALESCE(SUM(cache_write_tokens),0), COALESCE(SUM(output_tokens),0)
		  FROM turn WHERE actor_id = ?`, timeline.MateActorID(fixtureProject)).
		Scan(&mateIn, &mateCacheRead, &mateCacheWrite, &mateOut); err != nil {
		t.Fatalf("sum the Mate's turns: %v", err)
	}
	if got.Mate.Tokens.Total != mateIn+mateCacheRead+mateCacheWrite+mateOut {
		t.Errorf("mate total = %d, want %d", got.Mate.Tokens.Total, mateIn+mateCacheRead+mateCacheWrite+mateOut)
	}
	if got.Mate.LastTurn == nil {
		t.Error("the Mate has turns; last_turn must not be null")
	}
	if got.InboxError != "" {
		t.Errorf("inbox_error = %q, want none", got.InboxError)
	}
	if len(got.Inbox) != f.inboxLen(t) {
		t.Errorf("inbox = %d item(s), want box.Inbox's %d", len(got.Inbox), f.inboxLen(t))
	}
}

func TestTaskPageCarriesTurnsStatusLinesAndAnsweredQuestions(t *testing.T) {
	f := newFixture(t)
	var got TaskResponse
	f.get(t, "/api/projects/"+fixtureProject+"/tasks/"+fixtureCrew, http.StatusOK, &got)

	if int64(len(got.Turns)) != got.Ledger.Turns {
		t.Fatalf("%d turn(s) listed, ledger says %d", len(got.Turns), got.Ledger.Turns)
	}
	if len(got.Turns) == 0 {
		t.Fatal("the fixture crew took turns; the task page lists none")
	}
	for i, turn := range got.Turns {
		if turn.ID == "" {
			t.Fatalf("turn %d has no id", i)
		}
		if turn.Ref.Path == "" {
			t.Errorf("turn %s has no ref: every number on the page must be traceable", turn.ID)
		}
		if i > 0 && got.Turns[i-1].StartedAt > turn.StartedAt {
			t.Errorf("turns are out of order at %d: %s then %s", i, got.Turns[i-1].StartedAt, turn.StartedAt)
		}
		if turn.Tokens.Total != turn.Tokens.Input+turn.Tokens.CacheRead+turn.Tokens.CacheWrite+turn.Tokens.Output {
			t.Errorf("turn %s total does not equal its buckets: %+v", turn.ID, turn.Tokens)
		}
	}

	// The three lines the crew wrote, in the order it wrote them.
	if len(got.StatusLines) != 3 {
		t.Fatalf("status lines = %d, want the crew's 3", len(got.StatusLines))
	}
	verbs := []string{got.StatusLines[0].Verb, got.StatusLines[1].Verb, got.StatusLines[2].Verb}
	if strings.Join(verbs, ",") != "working,needs-decision,wait-mate" {
		t.Errorf("status verbs = %v, want working, needs-decision, wait-mate", verbs)
	}

	// The question and the answer joined through `cause`, which is what the
	// ingest recorded and what `question.waited_ms` measures.
	if len(got.Questions) != 1 {
		t.Fatalf("questions = %d, want the crew's 1", len(got.Questions))
	}
	q := got.Questions[0]
	if !strings.Contains(q.Text, "checkout page URL") {
		t.Errorf("question text = %q", q.Text)
	}
	if q.AnsweredEventID == 0 || q.Answer == "" {
		t.Errorf("question %+v was answered in the fixture; the page shows no answer", q)
	}
	if q.WaitedMs == nil || *q.WaitedMs <= 0 {
		t.Errorf("waited_ms = %v, want the wait the ingest measured", q.WaitedMs)
	}
	var wantWaited int64
	if err := f.read.SQL().QueryRow(`SELECT waited_ms FROM question WHERE id = ?`, q.ID).Scan(&wantWaited); err != nil {
		t.Fatalf("read question: %v", err)
	}
	if *q.WaitedMs != wantWaited {
		t.Errorf("waited_ms = %d, want the row's %d", *q.WaitedMs, wantWaited)
	}

	if got.Branch.Name != fixtureBranch || !got.Branch.Exists {
		t.Errorf("branch = %+v, want %s, present", got.Branch, fixtureBranch)
	}
}

func TestMatePageGroupsModelCallsIntoPromptExchanges(t *testing.T) {
	f := newFixture(t)
	var got MateResponse
	f.get(t, "/api/projects/"+fixtureProject+"/mate", http.StatusOK, &got)
	var want int64
	if err := f.read.SQL().QueryRow(`SELECT COUNT(*) FROM (
		SELECT session_id,harness_turn_ref FROM turn WHERE actor_id=?
		GROUP BY session_id,harness_turn_ref)`, timeline.MateActorID(fixtureProject)).Scan(&want); err != nil {
		t.Fatal(err)
	}
	if int64(len(got.Exchanges)) < want || want == 0 {
		t.Fatalf("mate page has %d exchanges, database has %d prompt ids", len(got.Exchanges), want)
	}
	var calls int64
	var request, reply, crewQuestion, crewAnswer bool
	for i, exchange := range got.Exchanges {
		calls += exchange.ModelCalls
		if i > 0 && got.Exchanges[i-1].StartedAt > exchange.StartedAt {
			t.Errorf("exchanges are out of order at %d", i)
		}
		if strings.Contains(exchange.Prompt, "Add a Buy button") && exchange.Source == "captain" {
			request = true
			if exchange.ModelCalls != 0 || exchange.Response != "" {
				t.Errorf("unmatched captain request should remain visible without invented work: %+v", exchange)
			}
		}
		if strings.Contains(exchange.Response, "buybtn is ready") {
			reply = true
		}
		if exchange.Source == "crew" && strings.Contains(exchange.Prompt, "checkout page URL") {
			crewQuestion = true
			for _, activity := range exchange.Activities {
				if activity.Kind == "crew.answer" && activity.Crew == fixtureCrew &&
					strings.Contains(activity.Text, "pages/checkout-express.html") {
					crewAnswer = true
				}
			}
		}
	}
	if calls != got.Mate.Turns {
		t.Errorf("exchanges contain %d model calls, Mate ledger says %d", calls, got.Mate.Turns)
	}
	if !request || !reply || !crewQuestion || !crewAnswer {
		t.Errorf("Mate exchanges lost input, reply or crew answer: request=%t reply=%t question=%t answer=%t",
			request, reply, crewQuestion, crewAnswer)
	}
}

func TestCrewDigestShowsTheQuestionWithoutTransportInstructions(t *testing.T) {
	got := readablePrompt(`digest: 1 item(s) — ios16 needs-decision: "Debug or Release?" — status files under /tmp/work/crews; act per AGENTS.md section 10`)
	if got != `ios16 asks: "Debug or Release?"` {
		t.Errorf("readablePrompt = %q", got)
	}
}

func TestTurnPageCarriesItsActionsAndItsStoryRows(t *testing.T) {
	f := newFixture(t)
	var task TaskResponse
	f.get(t, "/api/projects/"+fixtureProject+"/tasks/"+fixtureCrew, http.StatusOK, &task)

	// The turn with the most tool calls is the one worth opening.
	best := task.Turns[0]
	for _, turn := range task.Turns {
		if turn.ToolCount > best.ToolCount {
			best = turn
		}
	}
	// A turn id carries `#` (`crew:shop:buybtn#<session>#turn#20`), so the
	// path segment has to be escaped - as docs/dashboard.md tells task 29's
	// UI to do. An unescaped `#` would make the browser send only the part
	// before it and treat the rest as a fragment.
	var got TurnResponse
	f.get(t, "/api/projects/"+fixtureProject+"/tasks/"+fixtureCrew+"/turns/"+url.PathEscape(best.ID),
		http.StatusOK, &got)

	if got.Turn.ID != best.ID {
		t.Fatalf("turn = %q, want %q", got.Turn.ID, best.ID)
	}
	// `tool_count` counts the calls the harness made; `action` also holds
	// the synthesised `thinking` rows the ingest writes for a busy stretch
	// no call explains (docs/timeline.md section 4), so the two agree only
	// once those are set aside.
	real := 0
	for _, a := range got.Actions {
		if a.Tool == "" {
			t.Errorf("action %s has no tool", a.ID)
		}
		if a.Tool != "thinking" {
			real++
		}
	}
	if int64(real) != best.ToolCount {
		t.Errorf("%d tool call(s) listed beside %d thinking row(s), the turn records tool_count %d",
			real, len(got.Actions)-real, best.ToolCount)
	}

	// The events inside the turn are v_story rows, and every one of them
	// belongs to this turn.
	if len(got.Events) == 0 {
		t.Fatal("a turn with tool calls has events inside it; none were returned")
	}
	all, err := timeline.Story(context.Background(), f.read.SQL(), timeline.StoryQuery{Project: fixtureProject})
	if err != nil {
		t.Fatalf("timeline.Story: %v", err)
	}
	want := 0
	for _, e := range all {
		if e.Turn == best.ID {
			want++
		}
	}
	if len(got.Events) != want {
		t.Errorf("events = %d, want v_story's %d for this turn", len(got.Events), want)
	}
	for _, e := range got.Events {
		if e.Turn != best.ID {
			t.Errorf("event %d belongs to turn %q, not %q", e.ID, e.Turn, best.ID)
		}
	}
}

func TestEventsMatchWhatTheEventsCommandPrints(t *testing.T) {
	f := newFixture(t)
	var got EventsResponse
	f.get(t, "/api/events?since=0&project="+fixtureProject, http.StatusOK, &got)

	want, err := timeline.Story(context.Background(), f.read.SQL(), timeline.StoryQuery{Project: fixtureProject})
	if err != nil {
		t.Fatalf("timeline.Story: %v", err)
	}
	if len(got.Events) != len(want) {
		t.Fatalf("events = %d, want the story's %d", len(got.Events), len(want))
	}
	for i := range want {
		if got.Events[i].ID != want[i].ID || got.Events[i].Kind != want[i].Kind {
			t.Fatalf("event %d = %d/%s, want %d/%s", i,
				got.Events[i].ID, got.Events[i].Kind, want[i].ID, want[i].Kind)
		}
	}
	if len(got.Now) == 0 {
		t.Error("a since of 0 covers every transition; the scene rows that moved must be included")
	}
}

func TestEventsReturnsImmediatelyForANewerEventAndEmptyAfterTheWait(t *testing.T) {
	f := newFixture(t)
	last := f.lastEventID(t)

	// Newer than `since`: the wait is never entered, so even a long one
	// costs nothing.
	start := time.Now()
	var fresh EventsResponse
	f.get(t, "/api/events?since=0&wait=20&project="+fixtureProject, http.StatusOK, &fresh)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("a poll with newer events waited %s; it must return at once", elapsed)
	}
	if len(fresh.Events) == 0 {
		t.Fatal("since=0 must return the whole story")
	}

	// Nothing newer: the wait runs out and the answer is an empty list, not
	// a held connection.
	start = time.Now()
	var empty EventsResponse
	f.get(t, "/api/events?since="+strconv.FormatInt(last, 10)+"&wait=1&project="+fixtureProject, http.StatusOK, &empty)
	if len(empty.Events) != 0 {
		t.Fatalf("events after the newest id = %d, want none", len(empty.Events))
	}
	if elapsed := time.Since(start); elapsed < time.Second {
		t.Fatalf("the poll returned after %s; it must wait the second it was asked for", elapsed)
	}
	if empty.LastEventID != last {
		t.Errorf("last_event_id = %d, want %d", empty.LastEventID, last)
	}
}

func TestACachedAnswerIsRebuiltOnlyWhenTheEventIDMoves(t *testing.T) {
	f := newFixture(t)
	var first WorkspaceResponse
	f.get(t, "/api/workspace", http.StatusOK, &first)
	builds := f.server.cache.builds

	for i := 0; i < 3; i++ {
		var again WorkspaceResponse
		f.get(t, "/api/workspace", http.StatusOK, &again)
		if again.GeneratedAt != first.GeneratedAt || again.LastEventID != first.LastEventID {
			t.Fatalf("a repeat read was recomputed: %+v then %+v", first.envelope, again.envelope)
		}
	}
	if f.server.cache.builds != builds {
		t.Fatalf("the answer was rebuilt %d time(s) while the event id stood still",
			f.server.cache.builds-builds)
	}

	id := f.appendEvent(t)
	var moved WorkspaceResponse
	f.get(t, "/api/workspace", http.StatusOK, &moved)
	if moved.LastEventID != id {
		t.Fatalf("last_event_id = %d, want the new event's %d", moved.LastEventID, id)
	}
	if f.server.cache.builds == builds {
		t.Fatal("the event id moved and nothing was recomputed")
	}
}

func TestAnUnknownProjectOrCrewIs404WithAReason(t *testing.T) {
	f := newFixture(t)
	for _, path := range []string{
		"/api/projects/nosuch",
		"/api/projects/nosuch/mate",
		"/api/projects/nosuch/tasks/buybtn",
		"/api/projects/" + fixtureProject + "/tasks/nosuch",
		"/api/projects/" + fixtureProject + "/tasks/nosuch/diff",
		"/api/projects/" + fixtureProject + "/tasks/" + fixtureCrew + "/turns/nosuch",
		"/api/events?project=nosuch",
	} {
		var got ErrorResponse
		f.get(t, path, http.StatusNotFound, &got)
		if got.Error == "" {
			t.Errorf("GET %s: a 404 must say why", path)
		}
		if !strings.Contains(got.Error, "nosuch") {
			t.Errorf("GET %s: reason %q does not name what was missing", path, got.Error)
		}
	}
}

func TestDiffIsTheCLIsTextAndSaysSoWhenTheBranchIsGone(t *testing.T) {
	f := newFixture(t)
	var got DiffResponse
	f.get(t, "/api/projects/"+fixtureProject+"/tasks/"+fixtureCrew+"/diff", http.StatusOK, &got)
	if !got.Exists || !strings.Contains(got.Text, "docs: add Buy link") {
		t.Fatalf("diff = %+v, want the branch's text", got)
	}

	// A branch git no longer has: the answer is empty with a reason, not an
	// error, because a crew whose branch was deleted is a crew whose work
	// landed.
	f.server.deps.BranchExists = func(context.Context, string, string, string) (bool, error) { return false, nil }
	f.server.cache = newCache()
	var gone DiffResponse
	f.get(t, "/api/projects/"+fixtureProject+"/tasks/"+fixtureCrew+"/diff", http.StatusOK, &gone)
	if gone.Exists || gone.Text != "" || gone.Reason == "" {
		t.Fatalf("diff of a deleted branch = %+v, want empty with a reason", gone)
	}
}

func TestANonLoopbackBindIsRefused(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:7777", "192.168.1.10:7777", ":7777", "example.com:7777"} {
		if _, err := Listen(Options{Addr: addr}); err == nil {
			t.Errorf("Listen(%q) was allowed without --allow-remote", addr)
		}
	}
	for _, addr := range []string{"127.0.0.1:0", "localhost:0", "[::1]:0"} {
		ln, err := Listen(Options{Addr: addr})
		if err != nil {
			t.Errorf("Listen(%q): %v", addr, err)
			continue
		}
		_ = ln.Close()
	}
}

func TestTheServerRefusesAWriterHandle(t *testing.T) {
	f := newFixture(t)
	writer, err := db.Open(f.ws)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer writer.Close()
	if _, err := New(Options{Workspace: f.ws, DB: writer}); err == nil {
		t.Fatal("a dashboard built on the writer handle would hold the console out of its own timeline")
	}
}

func TestTheEmbeddedUIIsServedAtTheRoot(t *testing.T) {
	f := newFixture(t)
	resp, err := f.http.Client().Get(f.http.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("GET / content type = %q, want html", ct)
	}
}

// TestNoReadAcceptsAWrite: the one write is POST /api/actions
// (actions_test.go); a POST to a read is refused by the mux.
func TestNoReadAcceptsAWrite(t *testing.T) {
	f := newFixture(t)
	for _, path := range []string{
		"/api/workspace",
		"/api/projects/" + fixtureProject,
		"/api/events",
	} {
		resp, err := f.http.Client().Post(f.http.URL+path, "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Errorf("POST %s was accepted; a read takes no write", path)
		}
	}
}

// --- helpers that read the database the endpoints claim to repeat ---

func (f *fixture) lastEventID(t *testing.T) int64 {
	t.Helper()
	var id sql.NullInt64
	if err := f.read.SQL().QueryRow(`SELECT MAX(id) FROM event`).Scan(&id); err != nil {
		t.Fatalf("MAX(event.id): %v", err)
	}
	return id.Int64
}

func (f *fixture) nowRow(t *testing.T, actorID string) (state, since string, tokens int64) {
	t.Helper()
	var s, at sql.NullString
	if err := f.read.SQL().QueryRow(
		`SELECT state, since, tokens_today FROM v_now WHERE actor_id = ?`, actorID).
		Scan(&s, &at, &tokens); err != nil {
		t.Fatalf("read v_now for %s: %v", actorID, err)
	}
	return s.String, at.String, tokens
}

func (f *fixture) inboxLen(t *testing.T) int {
	t.Helper()
	items, reason := f.server.inbox(fixtureProject)
	if reason != "" {
		t.Fatalf("the fixture's box must read: %s", reason)
	}
	return len(items)
}

func TestContextSurvivesUnknownPricingAndClearsOnFreshSession(t *testing.T) {
	f := newFixture(t)
	writer, err := db.Open(f.ws)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := writer.SQL().Exec(`DELETE FROM pricing`); err != nil {
		t.Fatal(err)
	}
	rows, err := f.server.sceneRows(context.Background(), fixtureProject)
	if err != nil {
		t.Fatal(err)
	}
	id := timeline.MateActorID(fixtureProject)
	row := rows[id]
	if row.ContextTokens == nil || *row.ContextTokens <= 0 || row.ContextPct != nil {
		t.Fatalf("unpriced context: %+v", row)
	}
	meta, err := f.ws.ReadMateMeta(fixtureProject)
	if err != nil {
		t.Fatal(err)
	}
	meta["session_id"] = "fresh-session-without-usage"
	if err := f.ws.WriteMateMeta(fixtureProject, meta); err != nil {
		t.Fatal(err)
	}
	rows, err = f.server.sceneRows(context.Background(), fixtureProject)
	if err != nil {
		t.Fatal(err)
	}
	if rows[id].ContextTokens != nil || rows[id].ContextPct != nil {
		t.Fatalf("borrowed old context: %+v", rows[id])
	}
}

func TestTaskLaunchSettingsAreReadAlongsideObservedModel(t *testing.T) {
	f := newFixture(t)
	meta, err := f.ws.ReadCrewMeta(fixtureProject, fixtureCrew)
	if err != nil {
		t.Fatal(err)
	}
	meta["model"], meta["effort"], meta["state"] = "requested-model", "high", "failed"
	if err := f.ws.WriteCrewMeta(fixtureProject, fixtureCrew, meta); err != nil {
		t.Fatal(err)
	}
	var got ProjectResponse
	f.get(t, "/api/projects/"+fixtureProject, http.StatusOK, &got)
	task := got.Tasks[0]
	if task.Harness != "codex" || task.Model != "requested-model" || task.Effort != "high" || task.LastModel == "" || task.LastModel == task.Model {
		t.Fatalf("task launch settings = %+v", task)
	}
}

func TestTaskMissingLaunchSettingsRemainUnknown(t *testing.T) {
	f := newFixture(t)
	var got ProjectResponse
	f.get(t, "/api/projects/"+fixtureProject, http.StatusOK, &got)
	if got.Tasks[0].Model != "" || got.Tasks[0].Effort != "" {
		t.Fatalf("invented launch settings: %+v", got.Tasks[0])
	}
}

func TestTaskCarriesAReviewersVerdict(t *testing.T) {
	// A response is cached per event id, so each case gets its own fixture.
	for _, c := range []struct {
		name           string
		reviewer       bool
		report, review string
	}{
		{name: "not a reviewer", review: ""},
		{name: "no report yet", reviewer: true, review: "pending"},
		{name: "report without a Verdict", reviewer: true, report: "## Summary\nStill reading.\n", review: "pending"},
		{name: "Verdict", reviewer: true, report: "## Summary\nTwo gaps.\n\n## Verdict\nchanges-requested\nThe retry is untested.\n", review: "changes-requested"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			if c.reviewer {
				if err := store.WriteMeta(f.ws.CrewReviewMeta(fixtureProject, fixtureCrew), map[string]string{"head": "abc"}); err != nil {
					t.Fatal(err)
				}
			}
			if c.report != "" {
				if err := os.WriteFile(f.ws.CrewReport(fixtureProject, fixtureCrew), []byte(c.report), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var got ProjectResponse
			f.get(t, "/api/projects/"+fixtureProject, http.StatusOK, &got)
			if got.Tasks[0].Review != c.review {
				t.Fatalf("review = %q, want %q", got.Tasks[0].Review, c.review)
			}
		})
	}
}

func TestTaskSaysItsRole(t *testing.T) {
	for _, c := range []struct {
		name, kind string
		reviewer   bool
		role       string
	}{
		{name: "ship", role: "ship"},
		{name: "scout", kind: "scout", role: "scout"},
		{name: "reviewer", reviewer: true, role: "review"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			if c.kind != "" {
				meta, err := f.ws.ReadCrewMeta(fixtureProject, fixtureCrew)
				if err != nil {
					t.Fatal(err)
				}
				meta[store.MetaKind] = c.kind
				if err := f.ws.WriteCrewMeta(fixtureProject, fixtureCrew, meta); err != nil {
					t.Fatal(err)
				}
			}
			if c.reviewer {
				if err := store.WriteMeta(f.ws.CrewReviewMeta(fixtureProject, fixtureCrew), map[string]string{"head": "abc"}); err != nil {
					t.Fatal(err)
				}
			}
			var got ProjectResponse
			f.get(t, "/api/projects/"+fixtureProject, http.StatusOK, &got)
			if got.Tasks[0].Role != c.role {
				t.Fatalf("role = %q, want %q", got.Tasks[0].Role, c.role)
			}
		})
	}
}

func TestTaskSaysWhyASpawnFailed(t *testing.T) {
	f := newFixture(t)
	meta, err := f.ws.ReadCrewMeta(fixtureProject, fixtureCrew)
	if err != nil {
		t.Fatal(err)
	}
	meta["state"], meta["failed_reason"] = "failed", "herdr refused the launch"
	if err := f.ws.WriteCrewMeta(fixtureProject, fixtureCrew, meta); err != nil {
		t.Fatal(err)
	}
	var got ProjectResponse
	f.get(t, "/api/projects/"+fixtureProject, http.StatusOK, &got)
	if got.Tasks[0].FailedReason != "herdr refused the launch" {
		t.Fatalf("failed_reason = %q", got.Tasks[0].FailedReason)
	}
}

// The project page names a task's reviewers and its tokens with them
// (CON-75), from the task's reviews= and the reviewer's review.meta alike.
func TestProjectPageRollsReviewersIntoTheTask(t *testing.T) {
	f := newFixture(t)
	if err := f.ws.WriteCrewMeta(fixtureProject, "rev", map[string]string{"task": "Review " + fixtureCrew, "state": "spawned"}); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteMeta(f.ws.CrewReviewMeta(fixtureProject, "rev"), map[string]string{"crew": fixtureCrew, "head": "abc"}); err != nil {
		t.Fatal(err)
	}
	var got ProjectResponse
	f.get(t, "/api/projects/"+fixtureProject, http.StatusOK, &got)
	var task Task
	for _, tk := range got.Tasks {
		if tk.Crew == fixtureCrew {
			task = tk
		}
	}
	if len(task.Reviews) != 1 || task.Reviews[0] != "rev" {
		t.Fatalf("reviews = %v, want [rev]", task.Reviews)
	}
	// The reviewer has no ledger row in this corpus, so the roll-up is the
	// task's own total, never less.
	if task.TokensWithReviews != task.Tokens.Total || task.ReviewOf != "" {
		t.Fatalf("tokens_with_reviews = %d (total %d), review_of = %q", task.TokensWithReviews, task.Tokens.Total, task.ReviewOf)
	}
}

// A task names the kinds of agent its crew delegated to, first use first and
// each once, from the `agent` of its tool.called events; a crew that
// delegated to none has no list (CON-124). The Office badges a crew with them.
func TestTaskNamesTheAgentsItDelegatedTo(t *testing.T) {
	f := newFixture(t)
	var before ProjectResponse
	f.get(t, "/api/projects/"+fixtureProject, http.StatusOK, &before)
	if before.Tasks[0].Agents != nil {
		t.Fatalf("agents = %v before any delegation, want none", before.Tasks[0].Agents)
	}
	writer, err := db.Open(f.ws)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	actor := timeline.CrewActorID(fixtureProject, fixtureCrew)
	for i, c := range []struct{ at, payload string }{
		{"2026-09-19T10:45:10Z", `{"agent":"backend-reviewer","class":"agent","target":"backend-reviewer","tool":"Agent"}`},
		{"2026-09-19T10:45:20Z", `{"agent":"Explore","class":"agent","target":"Explore","tool":"Agent"}`},
		{"2026-09-19T10:45:30Z", `{"agent":"backend-reviewer","class":"agent","target":"backend-reviewer","tool":"Agent"}`},
		{"2026-09-19T10:45:40Z", `{"class":"agent","target":"Find the checkout page","tool":"Task"}`},
		{"2026-09-19T10:45:50Z", `{"class":"shell","target":"echo '\"agent\":'","tool":"Bash"}`},
	} {
		if _, err := writer.SQL().Exec(
			`INSERT INTO event(dedup, project, at, actor_id, kind, payload) VALUES (?, ?, ?, ?, ?, ?)`,
			fmt.Sprintf("agents-%d", i), fixtureProject, db.FormatTime(mustTime(c.at)), actor, timeline.KindToolCalled, c.payload); err != nil {
			t.Fatalf("insert a tool.called: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	var got ProjectResponse
	f.get(t, "/api/projects/"+fixtureProject, http.StatusOK, &got)
	want := []string{"backend-reviewer", "Explore"}
	if !reflect.DeepEqual(got.Tasks[0].Agents, want) {
		t.Fatalf("agents = %v, want %v: first use first, each once, a delegation of no named kind and a shell echo left out", got.Tasks[0].Agents, want)
	}
	var one TaskResponse
	f.get(t, "/api/projects/"+fixtureProject+"/tasks/"+fixtureCrew, http.StatusOK, &one)
	if !reflect.DeepEqual(one.Ledger.Agents, want) {
		t.Fatalf("task agents = %v, want %v", one.Ledger.Agents, want)
	}

	// The server keeps what it has read and reads only the events past it
	// (CON-126): one ingested later but used earlier still comes first.
	writer, err = db.Open(f.ws)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if _, err := writer.SQL().Exec(
		`INSERT INTO event(dedup, project, at, actor_id, kind, payload) VALUES (?, ?, ?, ?, ?, ?)`,
		"agents-late", fixtureProject, db.FormatTime(mustTime("2026-09-19T10:45:00Z")), actor, timeline.KindToolCalled,
		`{"agent":"Explore","class":"agent","target":"Explore","tool":"Agent"}`); err != nil {
		t.Fatalf("insert a late tool.called: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	var later ProjectResponse
	f.get(t, "/api/projects/"+fixtureProject, http.StatusOK, &later)
	if want := []string{"Explore", "backend-reviewer"}; !reflect.DeepEqual(later.Tasks[0].Agents, want) {
		t.Fatalf("agents after a late event = %v, want %v", later.Tasks[0].Agents, want)
	}
}

// Agent City draws a tent per repo and puts each crew in its repo's tent
// (CON-125), from the workspace card and the task row alone.
func TestWorkspaceAndTasksNameTheirRepos(t *testing.T) {
	f := newFixture(t)
	cfg, err := f.ws.LoadProject(fixtureProject)
	if err != nil {
		t.Fatal(err)
	}
	want := cfg.RepoNames()
	if len(want) != 1 {
		t.Fatalf("fixture repos = %v, want one", want)
	}
	var ws WorkspaceResponse
	f.get(t, "/api/workspace", http.StatusOK, &ws)
	if got := ws.Projects[0].Repos; len(got) != 1 || got[0] != want[0] {
		t.Errorf("card repos = %v, want project.yaml's %v", got, want)
	}
	// The fixture crew's meta names no repo (written before M9), so it works
	// in the project's sole repo.
	var p ProjectResponse
	f.get(t, "/api/projects/"+fixtureProject, http.StatusOK, &p)
	if got := p.Tasks[0].Repo; got != want[0] {
		t.Errorf("task repo = %q, want the sole repo %q", got, want[0])
	}
}

func TestTaskRepoIsAbsentWhenTheMetaNamesAGoneRepo(t *testing.T) {
	f := newFixture(t)
	meta, err := f.ws.ReadCrewMeta(fixtureProject, fixtureCrew)
	if err != nil {
		t.Fatal(err)
	}
	meta["repo"] = "removed"
	if err := f.ws.WriteCrewMeta(fixtureProject, fixtureCrew, meta); err != nil {
		t.Fatal(err)
	}
	var p ProjectResponse
	f.get(t, "/api/projects/"+fixtureProject, http.StatusOK, &p)
	if got := p.Tasks[0].Repo; got != "" {
		t.Errorf("task repo = %q, want none for a repo the project no longer has", got)
	}
}

// The summary view is the full task answer's ledger, status lines and
// decisions, and none of its turns, branch or performance (CON-126).
func TestHistoryIsEveryCrewsLinesAndDecisionsSinceATime(t *testing.T) {
	f := newFixture(t)
	var sum TaskSummaryResponse
	f.get(t, "/api/projects/"+fixtureProject+"/tasks/"+fixtureCrew+"?view=summary", http.StatusOK, &sum)
	if len(sum.StatusLines) < 2 {
		t.Fatal("the fixture crew needs two status lines for a since between them to mean anything")
	}
	path := "/api/projects/" + fixtureProject + "/history?since="
	var all HistoryResponse
	f.get(t, path+"2000-01-01T00:00:00Z", http.StatusOK, &all)
	got := all.Crews[fixtureCrew]
	if got == nil || !reflect.DeepEqual(got.StatusLines, sum.StatusLines) || !reflect.DeepEqual(got.Questions, sum.Questions) {
		t.Fatalf("the history's %s differs from its task summary: %+v", fixtureCrew, got)
	}

	// A since between two lines keeps the later ones only.
	cut := sum.StatusLines[len(sum.StatusLines)-1].At
	var late HistoryResponse
	f.get(t, path+db.FormatTime(db.ParseTime(cut)), http.StatusOK, &late)
	for crew, h := range late.Crews {
		for _, l := range h.StatusLines {
			if l.At < cut {
				t.Errorf("%s: a line at %s came back for since %s", crew, l.At, cut)
			}
		}
	}
	if n := len(late.Crews[fixtureCrew].StatusLines); n == 0 || n >= len(sum.StatusLines) {
		t.Errorf("since the last line kept %d of %d lines", n, len(sum.StatusLines))
	}

	f.get(t, "/api/projects/"+fixtureProject+"/history", http.StatusBadRequest, nil)
	f.get(t, path+"yesterday", http.StatusBadRequest, nil)
	f.get(t, "/api/projects/nosuch/history?since=2000-01-01T00:00:00Z", http.StatusNotFound, nil)
}

func TestTaskSummaryIsTheFullAnswerWithoutTurnsOrPerformance(t *testing.T) {
	f := newFixture(t)
	path := "/api/projects/" + fixtureProject + "/tasks/" + fixtureCrew
	var full TaskResponse
	f.get(t, path, http.StatusOK, &full)
	var raw map[string]json.RawMessage
	f.get(t, path+"?view=summary", http.StatusOK, &raw)
	for _, gone := range []string{"turns", "branch", "performance"} {
		if _, ok := raw[gone]; ok {
			t.Errorf("the summary carries %q", gone)
		}
	}
	var sum TaskSummaryResponse
	f.get(t, path+"?view=summary", http.StatusOK, &sum)
	if len(full.StatusLines) == 0 {
		t.Fatal("the fixture crew has no status lines; the comparison would prove nothing")
	}
	if !reflect.DeepEqual(sum.Ledger, full.Ledger) || !reflect.DeepEqual(sum.StatusLines, full.StatusLines) || !reflect.DeepEqual(sum.Questions, full.Questions) {
		t.Errorf("the summary's ledger, status lines or questions differ from the full answer's")
	}
	f.get(t, "/api/projects/"+fixtureProject+"/tasks/nosuch?view=summary", http.StatusNotFound, nil)
}

// An inbox item carries the key an `assign` press names it by exactly when
// the Mate can be handed it, the same key the crew's Assign button has.
func TestInboxItemsCarryTheirAssignKey(t *testing.T) {
	f := newFixture(t)
	// The fixture crew's last line is wait-mate; a fresh question is one
	// the Mate can be handed.
	if err := f.ws.AppendStatus(fixtureProject, fixtureCrew, "needs-decision: Stripe or bank?"); err != nil {
		t.Fatal(err)
	}
	items, reason := f.server.inbox(fixtureProject)
	if reason != "" {
		t.Fatalf("the fixture's box must read: %s", reason)
	}
	box := query.LoadBox(f.ws, fixtureProject).Value.Inbox
	resolvable := 0
	for i, e := range box {
		want := ""
		if e.Resolvable() {
			want = e.AssignKey
			resolvable++
		}
		if items[i].AssignKey != want {
			t.Errorf("item %d (%s) assign_key = %q, want %q", i, e.Verb, items[i].AssignKey, want)
		}
	}
	if resolvable == 0 {
		t.Fatal("the fixture has no question the Mate can be handed; the test proves nothing")
	}
}
