package dashboard

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/matehost"
)

// dutyLog is a Mate's duty.jsonl as `mate duty` writes it (CON-158): one
// row done and waiting for the captain's tick, one waiting on a topic, one
// the Mate proposed and works on, and one okayed.
const dutyLog = `{"at":"2026-09-19T10:30:00Z","op":"add","id":1,"kind":"assigned","title":"Gộp make shot vào pre-push hook","text":"anh giao trong chat"}
{"at":"2026-09-19T10:40:00Z","op":"done","id":1,"text":"PR #232 merged"}
{"at":"2026-09-19T10:31:00Z","op":"add","id":2,"kind":"assigned","title":"Đổi lane xcode lên 6"}
{"at":"2026-09-19T10:35:00Z","op":"wait","id":2,"topic":14,"text":"4 hay 6 lane?"}
{"at":"2026-09-19T10:32:00Z","op":"add","id":3,"kind":"proposed","title":"Dọn DerivedData nguội"}
{"at":"2026-09-19T10:33:00Z","op":"progress","id":3,"text":"launchd job trong hu-259"}
{"at":"2026-09-19T10:00:00Z","op":"add","id":4,"title":"Chặn crew pull main"}
{"at":"2026-09-19T10:05:00Z","op":"done","id":4}
{"at":"2026-09-19T10:06:00Z","op":"okay","id":4}
not json
`

func writeDutyLog(t *testing.T, f *fixture, project, body string) string {
	t.Helper()
	path := matehost.DutyPath(f.ws.MateDir(project))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDutyListsTheBoardWithEachRowsState(t *testing.T) {
	f := newFixture(t)
	writeDutyLog(t, f, fixtureProject, dutyLog)

	var got DutyResponse
	f.get(t, "/api/duty?project="+fixtureProject, http.StatusOK, &got)
	if got.Project != fixtureProject || len(got.Projects) != 1 {
		t.Fatalf("response = %+v", got)
	}
	p := got.Projects[0]
	if p.Project != fixtureProject || !p.Logged || p.Error != "" {
		t.Fatalf("project = %+v", p)
	}
	var order, states []string
	for _, w := range p.Duty {
		order = append(order, w.Title)
		states = append(states, w.Status)
	}
	// Latest move first; the okayed row stays, it is the history.
	if strings.Join(order, "|") != "Gộp make shot vào pre-push hook|Đổi lane xcode lên 6|Dọn DerivedData nguội|Chặn crew pull main" {
		t.Errorf("order = %v", order)
	}
	if strings.Join(states, "|") != "done|waiting|working|okayed" {
		t.Errorf("states = %v", states)
	}
	byID := map[int]DutyEntry{}
	for _, w := range p.Duty {
		byID[w.ID] = w
	}
	if w := byID[1]; w.Kind != "assigned" || w.Last != "PR #232 merged" || w.DoneAt != "2026-09-19T10:40:00.000000000Z" || len(w.Notes) != 2 {
		t.Errorf("row 1 = %+v", w)
	}
	if w := byID[2]; w.Topic != 14 || w.Last != "4 hay 6 lane?" {
		t.Errorf("row 2 = %+v", w)
	}
	if w := byID[3]; w.Kind != "proposed" {
		t.Errorf("row 3 = %+v", w)
	}
	if w := byID[4]; w.Kind != "assigned" || w.OkayedAt == "" {
		t.Errorf("row 4 = %+v, want assigned by default and okayed", w)
	}

	// A project with no log has no board; the whole workspace lists it.
	var all DutyResponse
	f.get(t, "/api/duty", http.StatusOK, &all)
	if all.Project != "" || len(all.Projects) < 1 {
		t.Fatalf("workspace response = %+v", all)
	}
	var e ErrorResponse
	f.get(t, "/api/duty?project=nope", http.StatusNotFound, &e)
}

// The tick is a press like the others: it goes to the console with the
// row, and a press without a row is refused before anything runs.
func TestOkayGoesToTheConsoleWithItsRow(t *testing.T) {
	f := newFixture(t)
	rec := &actRecorder{}
	ts := actingServer(t, f, rec)
	status, e, ok := post(t, ts, `{"action":"okay","project":"`+fixtureProject+`","duty":3}`, nil)
	if status != 200 || ok.Line != "did okay" {
		t.Fatalf("okay = %d %+v", status, e)
	}
	if got := rec.got[len(rec.got)-1]; got.Duty != 3 || got.Project != fixtureProject {
		t.Errorf("okay request = %+v", got)
	}
	if status, e, _ := post(t, ts, `{"action":"okay","project":"`+fixtureProject+`"}`, nil); status != 400 || !strings.Contains(e.Error, "row") {
		t.Errorf("okay without a row = %d %+v", status, e)
	}
}
