package dashboard

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/diagnostics"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// The journey field (journey.go, docs/design/task-page.md): the brief and
// hand-back read and parsed, the meta keys the page needs, the observer's
// incidents about the crew, the Mate's turns about it, and the project's
// workflow checked against the recording.

const fixtureHandback = `Closes HU-246
Part of HU-228

## For the next crew
- The knob is wired through SelectedBlockChrome.
- Learning: none (HU-246 4397c62): nothing new beyond HU-245's "no headless touch".

| criterion | pass/fail | command | output |
| --- | --- | --- | --- |
| No RotateGesture left | pass | xcodebuild test … | Test run passed |
| Knob turns the block | pass | same | theKnobTurnsTheBlock passed |
| Real touch on the knob | fail | – | no way to send touches headless |

## Deviations from Build
- Added holdGesture()/releaseGesture(), not named in Build.

## Still open
- A real touch on the knob is not verified.
- Zoom (HU-229) may add two-finger gestures.
`

func TestParseHandbackReadsTheCrewsShape(t *testing.T) {
	hb := parseHandback(fixtureHandback)
	if want := []string{"Closes HU-246", "Part of HU-228"}; !reflect.DeepEqual(hb.Closes, want) {
		t.Errorf("closes = %q, want %q", hb.Closes, want)
	}
	if len(hb.Criteria) != 3 {
		t.Fatalf("criteria = %+v, want 3 rows", hb.Criteria)
	}
	if c := hb.Criteria[0]; c.Name != "No RotateGesture left" || !c.Pass || c.Command != "xcodebuild test …" || c.Output != "Test run passed" {
		t.Errorf("first criterion = %+v", c)
	}
	if c := hb.Criteria[2]; c.Pass || c.Result != "fail" {
		t.Errorf("a failed row is not a pass: %+v", c)
	}
	if want := []string{"Added holdGesture()/releaseGesture(), not named in Build."}; !reflect.DeepEqual(hb.Deviations, want) {
		t.Errorf("deviations = %q", hb.Deviations)
	}
	if len(hb.StillOpen) != 2 || !strings.HasPrefix(hb.StillOpen[1], "Zoom") {
		t.Errorf("still open = %q", hb.StillOpen)
	}
	if hb.Learning != `none (HU-246 4397c62): nothing new beyond HU-245's "no headless touch".` {
		t.Errorf("learning = %q", hb.Learning)
	}
	if hb.Markdown != fixtureHandback {
		t.Error("the markdown is kept verbatim")
	}
}

func TestCheckWorkflowTicksStepsFromTheRecording(t *testing.T) {
	p := diagnostics.Performance{
		Executions: []diagnostics.Execution{
			{ID: "w", Tool: "exec", IsWrapper: true, Command: "make pre-merge E=E2", StartedAt: "2026-10-30T18:00:00Z"},
			{ID: "1", Tool: "Bash", Command: "cat docs/harness/mate.md | head -40", StartedAt: "2026-10-30T18:02:15Z"},
			{ID: "2", Tool: "Bash", Command: "cd x; make pre-merge E=E2", StartedAt: "2026-10-30T18:09:31Z"},
			{ID: "3", Tool: "Read", Target: "/repo/docs/harness/workflows/learn.md", StartedAt: "2026-10-30T18:14:00Z"},
			{ID: "p", Tool: "Bash", Poll: true, Command: "make shot NAME=x", StartedAt: "2026-10-30T18:15:00Z"},
		},
		Skills: []diagnostics.SkillUse{{Name: "ios-ui-verify", Count: 1, Loads: []diagnostics.SkillLoad{{At: "2026-10-30T18:12:00Z"}}}},
	}
	steps := []store.WorkflowStep{
		{Step: "read mate.md", Read: `docs/harness/mate\.md`},
		{Step: "gate", Command: `^(cd \S+; )?make pre-merge`},
		{Step: "shot", Command: `make shot`},
		{Step: "learn", Read: `workflows/learn\.md$`},
		{Step: "verify ui", Skill: `^ios-ui-verify$`, Files: `\.swift$`},
		{Step: "verify android", Skill: `^android-verify$`, Files: `\.kt$`},
		{Step: "learning line", Handback: `^- Learning:`},
		{Step: "bad", Command: `(`},
	}
	got := checkWorkflow(steps, store.WorkflowRoleShip, p, fixtureHandback, []string{"ios/Sources/NoteEditorView.swift", "docs/x.md"}, "2026-10-30T17:59:00Z")
	if got[4].Rule != `loads a skill matching ^ios-ui-verify$; only when a changed file matches \.swift$` {
		t.Errorf("rule = %q", got[4].Rule)
	}
	for i := range got {
		got[i].Rule = ""
	}
	want := []WorkflowCheck{
		{Step: "read mate.md", State: WorkflowDone, Evidence: "cat docs/harness/mate.md | head -40", At: "2026-10-30T18:02:15Z"},
		{Step: "gate", State: WorkflowDone, Evidence: "cd x; make pre-merge E=E2", At: "2026-10-30T18:09:31Z"},
		{Step: "shot", State: WorkflowMissed, Evidence: "nothing matches in: 3 commands the crew ran"},
		{Step: "learn", State: WorkflowDone, Evidence: "/repo/docs/harness/workflows/learn.md", At: "2026-10-30T18:14:00Z"},
		{Step: "verify ui", State: WorkflowDone, Evidence: "skill ios-ui-verify", At: "2026-10-30T18:12:00Z"},
		{Step: "verify android", State: WorkflowNA, Evidence: `no changed file matches \.kt$`},
		{Step: "learning line", State: WorkflowDone, Evidence: `- Learning: none (HU-246 4397c62): nothing new beyond HU-245's "no headless touch".`},
		{Step: "bad", State: WorkflowUnknown, Evidence: "bad pattern · command: error parsing regexp: missing closing ): `(`"},
	}
	if !reflect.DeepEqual(got, want) {
		for i := range want {
			if i < len(got) && got[i] != want[i] {
				t.Errorf("step %d:\n got %+v\nwant %+v", i, got[i], want[i])
			}
		}
		if len(got) != len(want) {
			t.Errorf("got %d checks, want %d", len(got), len(want))
		}
	}

	// Without the changed files a step with `files` that was not taken is
	// unknown, not missed and not n/a; a step taken anyway is still done.
	got = checkWorkflow(steps[4:6], store.WorkflowRoleShip, p, "", nil, "")
	if got[0].State != WorkflowDone || got[1].State != WorkflowUnknown {
		t.Errorf("without changed files: %+v", got)
	}
}

// A task that started before the repo asked for a step has it as n/a; a
// step whose evidence was never recorded is unknown, not missed; a missed
// step names every source it was looked for in.
func TestCheckWorkflowSinceAndUnrecordedEvidence(t *testing.T) {
	steps := []store.WorkflowStep{
		{Step: "gate plan", Command: `make gate-plan`, Since: "2026-10-28"},
		{Step: "pre-merge", Command: `^make pre-merge`, Since: "2026-10-20"},
		{Step: "learning", Handback: `Learning:`},
		{Step: "verify", Skill: `ios-ui-verify`, Handback: `verified`},
		{Step: "bad since", Command: `x`, Since: "last week"},
	}
	// Bash calls are recorded as their JSON input; an anchored pattern
	// still sees the command line, and so does the evidence.
	recorded := diagnostics.Performance{Executions: []diagnostics.Execution{{ID: "1", Tool: "Bash", Command: `{"command":"make pre-merge E=E1","description":"gate"}`, StartedAt: "2026-10-23T17:30:00Z"}}}
	got := checkWorkflow(steps, store.WorkflowRoleShip, recorded, "", nil, "2026-10-23T17:21:51Z")
	want := []struct{ state, evidence string }{
		{WorkflowNA, "added to the workflow on 2026-10-28, after this task started"},
		{WorkflowDone, "make pre-merge E=E1"},
		{WorkflowUnknown, "no hand-back to check"},
		{WorkflowMissed, "nothing matches in: 0 skills it loaded"},
		{WorkflowUnknown, "bad pattern · since: last week is not a date"},
	}
	for i, w := range want {
		if got[i].State != w.state || got[i].Evidence != w.evidence {
			t.Errorf("%s: got %s %q, want %s %q", steps[i].Step, got[i].State, got[i].Evidence, w.state, w.evidence)
		}
	}
	if got[0].Rule != "runs a command matching make gate-plan; asked since 2026-10-28" {
		t.Errorf("rule = %q", got[0].Rule)
	}

	got = checkWorkflow(steps[1:4], store.WorkflowRoleShip, diagnostics.Performance{}, "Closes HU-36\n", nil, "")
	want = []struct{ state, evidence string }{
		{WorkflowUnknown, "no command recorded to check"},
		{WorkflowMissed, "nothing matches in: its hand-back"},
		{WorkflowMissed, "nothing matches in: its hand-back"},
	}
	for i, w := range want {
		if got[i].State != w.state || got[i].Evidence != w.evidence {
			t.Errorf("unrecorded %s: got %s %q, want %s %q", steps[i+1].Step, got[i].State, got[i].Evidence, w.state, w.evidence)
		}
	}
}

// The HU-403 scout's report (CON-201), shortened: a scout hands back a
// report.md, not a handback.md, and its Learning line is a list item.
const fixtureScoutReport = `## For the next crew
- I could NOT reproduce the captain's mismatch. I ran 9 end-to-end runs on the iPhone 16 sim.
- Learning: follow-up (rev 1)
  - evidence: this report's runs u/z/q/s/t; destination: the Mate's PROJECT.md; owner: Mate.

## Smallest fix
Not named. No cause is proven.
`

// mihuv2's workflow as project.yaml declared it on 2026-11-04, with the
// Learning step asked of scouts too.
var mihuv2Workflow = []store.WorkflowStep{
	{Step: "Read docs/harness/mate.md first", Read: `docs/harness/mate\.md`, Since: "2026-10-27"},
	{Step: "Follow docs/harness/workflows/feature.md", Read: `docs/harness/workflows/feature\.md`, Since: "2026-10-17"},
	{Step: "make agent-start", Command: "make agent-start", Since: "2026-10-20"},
	{Step: "make gate-plan", Command: "make gate-plan", Since: "2026-10-28"},
	{Step: "Gate with make pre-merge on the commit", Command: "make pre-merge"},
	{Step: "Screenshots with make shot", Command: "make shot", Since: "2026-10-29"},
	{Step: "ios-ui-verify after a UI change", Skill: "ios-ui-verify", Files: `\.swift$`},
	{Step: "Read docs/harness/workflows/learn.md before handing back", Read: `docs/harness/workflows/learn\.md`, Since: "2026-10-27"},
	{Step: "Hand-back has a Learning line", Handback: "Learning:", Since: "2026-10-27", Roles: []string{store.WorkflowRoleShip, store.WorkflowRoleScout}},
	{Step: "Hand-back has the criteria table with the gate receipt", Handback: `^\| *Gates? green`},
}

// A scout neither commits nor gates: the feature steps are n/a for it and
// say why, and the Learning step asked of scouts is ticked on its report.
func TestCheckWorkflowAsksAScoutOnlyItsOwnSteps(t *testing.T) {
	p := diagnostics.Performance{Executions: []diagnostics.Execution{
		{ID: "1", Tool: "Bash", Command: `{"command":"cd x; xcodebuild build-for-testing"}`, StartedAt: "2026-11-04T14:50:00Z"},
	}}
	got := checkWorkflow(mihuv2Workflow, store.WorkflowRoleScout, p, fixtureScoutReport, nil, "2026-11-04T14:37:17Z")
	for i, c := range got {
		switch i {
		case 8:
			if c.State != WorkflowDone || c.Evidence != "- Learning: follow-up (rev 1)" {
				t.Errorf("learning step on the report: %+v", c)
			}
			if c.Rule != "writes a hand-back line matching Learning:; asked of ship and scout crews; asked since 2026-10-27" {
				t.Errorf("learning rule = %q", c.Rule)
			}
		default:
			if c.State != WorkflowNA || c.Evidence != "asked of ship crews, and this crew is a scout" {
				t.Errorf("%s: %s %q, want n/a for a scout", c.Step, c.State, c.Evidence)
			}
		}
	}
	// A ship crew is still asked every step.
	got = checkWorkflow(mihuv2Workflow, store.WorkflowRoleShip, p, "", nil, "2026-11-04T14:37:17Z")
	if got[2].State != WorkflowMissed || got[8].State != WorkflowUnknown {
		t.Errorf("ship crew: %+v", got)
	}
	// So is a reviewer, but only for the steps that name it.
	got = checkWorkflow([]store.WorkflowStep{{Step: "verdict", Handback: "Verdict:", Roles: []string{store.WorkflowRoleReview}}}, store.WorkflowRoleReview, p, "Verdict: approve\n", nil, "")
	if got[0].State != WorkflowDone {
		t.Errorf("review step: %+v", got[0])
	}
}

// The ledger counts a scout's report as its hand-back, so the Journey reads
// it as one: the header's "handed back" and the Journey agree.
func TestTaskJourneyReadsAScoutsReportAsItsHandback(t *testing.T) {
	f := newFixture(t)
	if err := f.ws.UpdateCrewMeta(fixtureProject, fixtureCrew, map[string]string{"kind": "scout"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(f.ws.CrewReport(fixtureProject, fixtureCrew)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.ws.CrewReport(fixtureProject, fixtureCrew), []byte(fixtureScoutReport), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := f.ws.LoadProject(fixtureProject)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Workflow = mihuv2Workflow
	if err := f.ws.SaveProject(fixtureProject, cfg); err != nil {
		t.Fatal(err)
	}

	var body TaskResponse
	f.get(t, "/api/projects/shop/tasks/buybtn", 200, &body)
	j := body.Journey
	if body.Ledger.Role != store.WorkflowRoleScout {
		t.Fatalf("role = %q", body.Ledger.Role)
	}
	if j.Handback == nil || j.Handback.Kind != HandbackReport || j.Handback.Path != f.ws.CrewReport(fixtureProject, fixtureCrew) || j.Handback.Learning != "follow-up (rev 1)" {
		t.Fatalf("handback = %+v", j.Handback)
	}
	for _, m := range j.Missing {
		if m == "hand-back" {
			t.Errorf("missing still names the hand-back: %v", j.Missing)
		}
	}
	missed := []string{}
	for _, w := range j.Workflow {
		if w.State == WorkflowMissed || w.State == WorkflowUnknown {
			missed = append(missed, w.Step+"="+w.State)
		}
	}
	if len(missed) != 0 {
		t.Errorf("a scout is checked against ship steps: %v (%+v)", missed, j.Workflow)
	}

	// A ship crew's report (a reviewer's verdict, say) is not its hand-back.
	if err := f.ws.UpdateCrewMeta(fixtureProject, fixtureCrew, map[string]string{"kind": "ship"}); err != nil {
		t.Fatal(err)
	}
	f.appendEvent(t)
	f.get(t, "/api/projects/shop/tasks/buybtn", 200, &body)
	if body.Journey.Handback != nil {
		t.Errorf("a ship crew's report read as its hand-back: %+v", body.Journey.Handback)
	}
	if err := os.WriteFile(f.ws.CrewHandback(fixtureProject, fixtureCrew), []byte(fixtureHandback), 0o644); err != nil {
		t.Fatal(err)
	}
	f.appendEvent(t)
	f.get(t, "/api/projects/shop/tasks/buybtn", 200, &body)
	if body.Journey.Handback == nil || body.Journey.Handback.Kind != HandbackFile {
		t.Errorf("handback.md kind: %+v", body.Journey.Handback)
	}
}

func TestTaskJourneyReadsTheCrewsFiles(t *testing.T) {
	f := newFixture(t)
	brief := "# Current worker role\n\nYou ship.\n\n# Task\n\n## Captain's words\n\nAdd the Buy button, keep it simple.\n\n## Build\n\n- Edit README.md\n\n# How to read the task\n\nIgnore this.\n"
	if err := os.MkdirAll(filepath.Dir(f.ws.CrewBrief(fixtureProject, fixtureCrew)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.ws.CrewBrief(fixtureProject, fixtureCrew), []byte(brief), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.ws.CrewHandback(fixtureProject, fixtureCrew), []byte(fixtureHandback), 0o644); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 19, 10, 49, 0, 0, time.UTC)
	for _, state := range []string{store.IncidentOpen, store.IncidentResolved} {
		if err := f.ws.AppendIncident(fixtureProject, store.IncidentEntry{Time: at, Crew: fixtureCrew, Kind: "no_turn", State: state, Text: "no turn in the ledger"}); err != nil {
			t.Fatal(err)
		}
		at = at.Add(time.Minute)
	}
	if err := f.ws.AppendIncident(fixtureProject, store.IncidentEntry{Time: at, Crew: "other", Kind: "stale", State: store.IncidentOpen, Text: "not this crew"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := f.ws.LoadProject(fixtureProject)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Workflow = []store.WorkflowStep{
		{Step: "Commit the work", Command: `git commit`},
		{Step: "Write the hand-back", Handback: `^\| criterion`},
		{Step: "Verify the UI", Skill: `ios-ui-verify`, Files: `\.swift$`},
	}
	if err := f.ws.SaveProject(fixtureProject, cfg); err != nil {
		t.Fatal(err)
	}

	var body TaskResponse
	f.get(t, "/api/projects/shop/tasks/buybtn", 200, &body)
	j := body.Journey
	if j.Brief == nil || j.Brief.CaptainWords != "Add the Buy button, keep it simple." {
		t.Errorf("brief = %+v", j.Brief)
	}
	if j.Brief != nil && (!strings.HasPrefix(j.Brief.Task, "## Captain's words") || strings.Contains(j.Brief.Task, "Ignore this") || strings.Contains(j.Brief.Task, "You ship")) {
		t.Errorf("the task section is the `# Task` body only:\n%s", j.Brief.Task)
	}
	if j.Handback == nil || len(j.Handback.Criteria) != 3 || j.Handback.Closes[0] != "Closes HU-246" {
		t.Errorf("handback = %+v", j.Handback)
	}
	if j.Meta["state"] != "spawned" || j.Meta["worktree"] != ".worktrees/shop-buybtn" || j.Meta["harness"] != "" {
		t.Errorf("meta = %v: the page's keys, not the whole file", j.Meta)
	}
	if len(j.Incidents) != 2 || j.Incidents[0].State != "open" || j.Incidents[1].State != "resolved" || j.Incidents[0].Kind != "no_turn" {
		t.Errorf("incidents = %+v: this crew's lines, open and resolved", j.Incidents)
	}
	if len(j.MateTurns) != 2 || j.MateTurns[0].Activities[0].Kind != "crew.spawned" || !strings.HasPrefix(j.MateTurns[1].Prompt, "buybtn asked") {
		t.Errorf("mate_turns = %+v: the spawn and the digest", j.MateTurns)
	}
	states := []string{}
	for _, w := range j.Workflow {
		states = append(states, w.Step+"="+w.State)
	}
	// The codex fixture committed README.md, the one changed file,
	// so the Swift-only step does not apply.
	if want := []string{"Commit the work=done", "Write the hand-back=done", "Verify the UI=n/a"}; !reflect.DeepEqual(states, want) {
		t.Errorf("workflow = %v, want %v (%+v)", states, want, j.Workflow)
	}
	if !reflect.DeepEqual(j.ChangedFiles, []string{"README.md"}) || len(j.Missing) != 0 {
		t.Errorf("changed_files = %v, missing = %v", j.ChangedFiles, j.Missing)
	}

	// A crew with none of the files says what is missing and draws no
	// workflow when the project declares none.
	cfg.Workflow = nil
	if err := f.ws.SaveProject(fixtureProject, cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f.ws.CrewHandback(fixtureProject, fixtureCrew)); err != nil {
		t.Fatal(err)
	}
	f.appendEvent(t)
	f.get(t, "/api/projects/shop/tasks/buybtn", 200, &body)
	if body.Journey.Handback != nil || body.Journey.Workflow != nil || !reflect.DeepEqual(body.Journey.Missing, []string{"hand-back"}) {
		t.Errorf("after removing the hand-back: handback=%v workflow=%v missing=%v", body.Journey.Handback, body.Journey.Workflow, body.Journey.Missing)
	}
}
