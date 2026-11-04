package console

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// The action menu's two edge cases (CON-174): the captain backs out while
// an action is running, and a snapshot lands in the middle of the menu or
// of the action. Each test pins what the Console does today, so moving the
// menu's state out of Model cannot change it unnoticed.

// ---------- backing out of a running action ----------

// Esc is "back" everywhere else, but it does not undo an action that has
// started: the keys wait (busy_quit_test.go), the action's context stays
// live, and its outcome still lands and asks for its re-read.
func TestEscWhileAnActionRunsCancelsNothingAndItsOutcomeLands(t *testing.T) {
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	m, cmd := confirmAndRunStop(t, blockingAction(started, release))
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	ctx := <-started

	running := m.msg
	for _, k := range []string{"esc", "backspace", "enter", "esc"} {
		next, c := send(t, m, key(k))
		if c != nil || next.msg != running || next.sheetOpen() != sheetNone || next.Quitting() {
			t.Fatalf("%q while the stop runs: cmd=%v msg=%q sheet=%v quitting=%v, want nothing to change",
				k, c != nil, next.msg.text, next.sheetOpen(), next.Quitting())
		}
		m = next
	}
	select {
	case <-ctx.Done():
		t.Fatal("esc cancelled the running action's context")
	default:
	}
	if desc, abandoned := m.AbandonedAction(); abandoned {
		t.Fatalf("esc recorded an abandonment: %q", desc)
	}

	close(release)
	m, reread := send(t, m, <-result)
	if m.msg.tone != toneOK || !strings.HasPrefix(m.msg.text, "Action stop completed") {
		t.Fatalf("outcome = %+v, want the completed stop", m.msg)
	}
	if reread == nil {
		t.Fatal("the finished stop asked for no re-read")
	}
	if _, ok := reread().(treeLoadedMsg); !ok {
		t.Fatal("the finished stop's Cmd is not a snapshot read")
	}
	if m, _ = send(t, m, key("a")); m.sheetOpen() != sheetActions {
		t.Fatalf("a after the stop finished opened %v, want the actions sheet: the keys answer again", m.sheetOpen())
	}
}

// Ctrl+C takes the same way out as q (busy_quit_test.go): the action's
// context is cancelled, the abandonment is recorded under the action's own
// name, and cmd/mate is handed a channel that closes once the action has
// returned.
func TestCtrlCWhileAnActionRunsCancelsItAndRecordsTheAbandonment(t *testing.T) {
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	m, cmd := confirmAndRunStop(t, blockingAction(started, release))
	go func() { cmd() }()
	ctx := <-started

	m, quit := send(t, m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if quit == nil || !m.Quitting() {
		t.Fatalf("ctrl+c mid-action: cmd=%v quitting=%v, want a quit", quit != nil, m.Quitting())
	}
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("ctrl+c did not cancel the running action's context")
	}
	want := "stop " + sampleTree().Projects[0].Crews[1].CrewID
	if desc, abandoned := m.AbandonedAction(); !abandoned || desc != want {
		t.Fatalf("AbandonedAction = %q, %v, want %q", desc, abandoned, want)
	}
	done := m.AbandonedActionDone()
	if done == nil {
		t.Fatal("quitting mid-action returned no channel to wait on")
	}
	select {
	case <-done:
		t.Fatal("done closed before the abandoned action returned")
	default:
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("done never closed after the abandoned action returned")
	}
}

// ---------- a snapshot landing in the middle ----------

// A background tick while an action runs moves the running line on and
// reads a new snapshot, and the action keeps running under it: the line
// stays, the keys still wait, and the outcome lands when the action
// returns and survives the re-read it asks for.
func TestASnapshotLandingWhileAnActionRunsLeavesItRunning(t *testing.T) {
	spy := &tickLoadSpy{tree: sampleTree()}
	m, _ := tickFixture(t, spy, 40, 36)
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	m.action = blockingAction(started, release)
	m = toRunningAttempt(t, m)
	m, _ = send(t, m, key("a"))
	m, _ = send(t, m, key("x"))
	m, cmd := send(t, m, key("x"))
	if cmd == nil {
		t.Fatal("the confirmed stop did not start")
	}
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	<-started

	moved := sampleTree()
	moved.Projects[0].Crews[1].Task = "Add idempotency-key index, rebased"
	spy.tree = moved
	m, next := send(t, m, treeTickMsg{gen: m.treeGen, at: m.actionStartedAt.Add(3*time.Second + 200*time.Millisecond)})
	runningAt3s := "3s " + m.g.Dot + " Running stop"
	if !strings.HasPrefix(m.msg.text, runningAt3s) {
		t.Fatalf("running line after the tick = %q, want it to lead with 3s", m.msg.text)
	}
	loadMsg, _ := splitBatch(t, next)
	if loadMsg == nil {
		t.Fatal("the tick read no snapshot while the stop ran")
	}
	m, _ = send(t, m, loadMsg)
	if got := m.tree.Projects[0].Crews[1].Task; got != moved.Projects[0].Crews[1].Task {
		t.Fatalf("the new snapshot did not land: task %q", got)
	}
	if !strings.HasPrefix(m.msg.text, runningAt3s) {
		t.Fatalf("the snapshot replaced the running line with %q", m.msg.text)
	}
	for _, k := range []string{"a", "x", "esc"} {
		if next, c := send(t, m, key(k)); c != nil || next.sheetOpen() != sheetNone {
			t.Fatalf("%q acted under the running stop once the snapshot landed: cmd=%v sheet=%v", k, c != nil, next.sheetOpen())
		}
	}

	close(release)
	m, reread := send(t, m, <-result)
	outcome := m.msg
	if !strings.HasPrefix(outcome.text, "Action stop completed") {
		t.Fatalf("outcome = %q, want the completed stop", outcome.text)
	}
	m, _ = send(t, m, reread())
	if m.msg != outcome {
		t.Fatalf("after its re-read the line says %q, want the outcome %q", m.msg.text, outcome.text)
	}
}

// An action's outcome is put back on the message line by the snapshot read
// the action asks for - a key that cleared the line meanwhile does not lose
// it - and by that one read only.
func TestAnActionsOutcomeComesBackWithItsReRead(t *testing.T) {
	m, _ := actRunner(t, sampleTree(), "mode is auto", nil)
	m, _ = send(t, m, key("enter")) // the payments-api Project, on its Mate row
	m, cmd := send(t, m, key("m"))
	if cmd == nil {
		t.Fatal("m did not run the mode flip")
	}
	m, reread := send(t, m, cmd())
	outcome := m.msg
	if outcome.tone != toneOK || !strings.Contains(outcome.text, "mode is auto") {
		t.Fatalf("outcome = %+v, want the mode flip's", outcome)
	}
	m, _ = send(t, m, key("down"))
	if m.msg.text != "" {
		t.Fatalf("precondition: moving the selection kept %q on the line", m.msg.text)
	}
	m, _ = send(t, m, reread())
	if m.msg != outcome {
		t.Fatalf("after the re-read the line says %q, want the outcome %q", m.msg.text, outcome.text)
	}
	m, _ = send(t, m, key("up"))
	m, _ = send(t, m, treeLoadedMsg{tree: sampleTree()})
	if m.msg.text != "" {
		t.Fatalf("a later read put %q back; the outcome belongs to its own re-read", m.msg.text)
	}
}

// When the read an action asks for fails, the outcome stays and says the
// picture behind it is stale - once. The next failed read is an ordinary
// refresh failure.
func TestAFailedReReadKeepsTheOutcomeAndSaysTheReadFailed(t *testing.T) {
	m, _ := actRunner(t, sampleTree(), "mode is auto", nil)
	m, _ = send(t, m, key("enter"))
	m, cmd := send(t, m, key("m"))
	m, _ = send(t, m, cmd())
	outcome := m.msg

	m, _ = send(t, m, treeLoadedMsg{err: errFake("database is locked")})
	want := outcome.text + " " + m.g.Dot + " snapshot re-read failed; r retries"
	if m.msg.tone != outcome.tone || m.msg.text != want {
		t.Fatalf("after a failed re-read the line = %+v, want %q in the outcome's tone", m.msg, want)
	}
	if !strings.Contains(strings.Split(renderFrame(t, m), "\n")[0], "stale") {
		t.Fatal("the list rule does not say stale after the failed re-read")
	}
	m, _ = send(t, m, treeLoadedMsg{err: errFake("database is locked")})
	if !strings.HasPrefix(m.msg.text, "Refresh failed: database is locked") {
		t.Fatalf("the next failed read says %q, want an ordinary refresh failure", m.msg.text)
	}
}

// A diff that lands before the previous action's re-read takes the message
// line: the re-read must not put the earlier outcome back under the diff.
func TestADiffDropsAnOutcomeStillWaitingForItsReRead(t *testing.T) {
	m := loaded(t, sampleTree(), nil)
	m.action = func(_ context.Context, req ActionRequest) (string, error) {
		if req.Action == ActionDiff {
			return "diff --git a/x b/x\n+added", nil
		}
		return "mode is auto", nil
	}
	m = toRunningAttempt(t, m)
	m, cmd := send(t, m, key("m"))
	m, reread := send(t, m, cmd()) // the mode's outcome waits for this read
	m, _ = send(t, m, key("a"))
	m, cmd = send(t, m, key("d"))
	if cmd == nil {
		t.Fatal("d did not run the diff")
	}
	m, _ = send(t, m, cmd())
	if !m.diff.open {
		t.Fatal("the diff did not open")
	}
	m, _ = send(t, m, reread())
	if m.msg.text != "" || !m.diff.open {
		t.Fatalf("the mode's re-read put %q back under the diff (open=%v)", m.msg.text, m.diff.open)
	}
}

// A snapshot that lands while the actions sheet is open leaves the sheet as
// it opened: the same entries, the cursor where the captain put it. The
// entries are built when the sheet opens and a read does not rebuild them,
// so the next key runs what the sheet shows - here a stop the new snapshot
// would no longer offer.
func TestASnapshotUnderTheActionsSheetKeepsItsEntriesAndCursor(t *testing.T) {
	m, got := actRunner(t, sampleTree(), "stopped", nil)
	m = toRunningAttempt(t, m)
	m, _ = send(t, m, key("a"))
	m, _ = send(t, m, key("down")) // the cursor on Stop crew…
	sheet := m.sheetLines(m.plan())

	moved := sampleTree()
	moved.AsOf = goldenAsOf
	moved.Projects[0].Crews[1].Binding.Value.Status = query.BindingStale
	m, _ = send(t, m, treeLoadedMsg{tree: moved})
	if m.sheetOpen() != sheetActions {
		t.Fatalf("the snapshot left %v open, want the actions sheet", m.sheetOpen())
	}
	if after := m.sheetLines(m.plan()); !reflect.DeepEqual(after, sheet) {
		t.Fatalf("the snapshot redrew the sheet:\nbefore %v\nafter  %v", sheet, after)
	}

	m, cmd := send(t, m, key("enter"))
	if cmd != nil || m.sheetOpen() != sheetConfirm || len(*got) != 0 {
		t.Fatalf("enter on the stop: cmd=%v sheet=%v calls=%d, want its confirmation", cmd != nil, m.sheetOpen(), len(*got))
	}
	m, cmd = send(t, m, key("x"))
	if cmd == nil {
		t.Fatal("x on the confirmation ran nothing")
	}
	_, _ = send(t, m, cmd())
	crew := sampleTree().Projects[0].Crews[1].CrewID
	if len(*got) != 1 || (*got)[0].Action != ActionStop || (*got)[0].Crew != crew {
		t.Fatalf("requests = %+v, want the stop of %s the sheet showed", *got, crew)
	}
}

// The new-project form keeps its draft through a snapshot, and Enter checks
// the typed name against the snapshot on screen, not the one the form opened
// on: a Project that appeared meanwhile makes the name a refusal.
func TestASnapshotUnderTheNewProjectFormKeepsTheDraftAndChecksTheNewTree(t *testing.T) {
	m, got := actRunner(t, actEmptyWorkspace(), "added", nil)
	m, _ = send(t, m, key("n"))
	m = actType(t, m, "ledger")

	moved := actEmptyWorkspace()
	moved.AsOf = goldenAsOf
	moved.Projects = []query.ProjectNode{{ProjectID: "ledger", Name: "ledger", Mate: absentMate("no mate")}}
	m, _ = send(t, m, treeLoadedMsg{tree: moved})
	if m.sheetOpen() != sheetNewProject {
		t.Fatalf("the snapshot left %v open, want the new-project form", m.sheetOpen())
	}
	if frame := renderFrame(t, m); !strings.Contains(frame, "ledger already exists") {
		t.Fatalf("the form's rule line does not read the new snapshot:\n%s", frame)
	}
	m, cmd := send(t, m, key("enter"))
	if cmd != nil || len(*got) != 0 || m.sheetOpen() != sheetNewProject {
		t.Fatalf("enter on a name the snapshot already has: cmd=%v calls=%d sheet=%v", cmd != nil, len(*got), m.sheetOpen())
	}

	m = actType(t, m, "2")
	m, _ = send(t, m, key("enter")) // name -> repo
	m, cmd = send(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("the kept draft did not dispatch once it was a new name")
	}
	_, _ = send(t, m, cmd())
	if len(*got) != 1 || (*got)[0].Input != "ledger2" {
		t.Fatalf("requests = %+v, want one onboard of ledger2", *got)
	}
}

// The harness picker keeps its cursor through a snapshot, and Enter takes
// the harness at the cursor in the snapshot on screen: a workspace default
// that changed meanwhile reorders the rows under the cursor.
func TestASnapshotUnderTheHarnessPickerKeepsTheCursorAndReadsTheNewCatalog(t *testing.T) {
	tree := actMateTree(absentMate("this Project has no Mate"), actMateCaps(true, false, false))
	m, got := actRunner(t, tree, "Mate created", nil)
	m, _ = send(t, m, key("enter"))
	m, _ = send(t, m, key("s"))
	m, _ = send(t, m, key("down")) // claude, codex: the cursor on codex

	moved := actMateTree(absentMate("this Project has no Mate"), actMateCaps(true, false, false))
	moved.AsOf = goldenAsOf
	moved.Workspace.Value.MateHarness = query.HarnessKind("codex") // codex, claude
	m, _ = send(t, m, treeLoadedMsg{tree: moved})
	if m.sheetOpen() != sheetHarness {
		t.Fatalf("the snapshot left %v open, want the harness picker", m.sheetOpen())
	}
	m, cmd := send(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("enter on the picker dispatched nothing")
	}
	_, _ = send(t, m, cmd())
	if len(*got) != 1 || (*got)[0].Harness != query.HarnessKind("claude") {
		t.Fatalf("requests = %+v, want one create carrying claude, the second row on screen", *got)
	}
}
