package console

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/mate/internal/names"
	"github.com/nguyenngocanh94/mate/internal/query"
)

// The action menu (CON-174) is a model of its own inside the Console: the
// actions sheet (design F) and its confirmation (design G), the new-project
// form (design K), the harness picker, and the one action that may be
// running. Model keeps it in its actions field, hands it the keyboard and
// the mouse while one of its sheets is open (update), and draws the sheet
// it says is on screen (view).
//
// The menu reads nothing of the tree itself. What it needs - the harnesses
// the picker lists, the Projects a new name must not repeat, the words for
// the row it acts on - comes in a menuFacts that Model builds from the tree
// on screen for every key and every frame. What the input asks of Model -
// carry out an entry, start an action, say something on the message line -
// comes back as a menuIntent that Model carries out in the same Update.
// Model reads the menu's fields; it changes them only through the menu's
// methods.
//
// The running action is not input. Every action of the Console runs here,
// the box's and the Work sheet's as well as the menu's, so Model starts and
// ends it itself (start, finish, abandon).

// actionMenu is the menu's state. The zero value has nothing open and
// nothing running.
type actionMenu struct {
	// open is the actions sheet, and only that sheet: the confirmation, the
	// form and the picker have fields of their own below. entries is what
	// the sheet lists, row is the row they were built for - the box's `o`
	// opens the sheet for a row the list's cursor is not on, and the
	// sheet's title names it - and index is the sheet's cursor.
	open    bool
	entries []menuEntry
	row     row
	index   int
	// confirm is the open confirmation: over the sheet it was asked from,
	// or over nothing when a dangerous choice came from the picker.
	confirm *actionConfirmation
	// inputMode is the new-project form: name is the Project name typed so
	// far, repo the repository path, and field the one the keyboard types
	// into.
	inputMode bool
	name      string
	repo      string
	field     onboardField
	// pending is the action the open sub-modal will run - the form or the
	// picker. It is kept here rather than read back off the sheet's cursor
	// because 'n', 's' and 'h' all open a modal with no sheet behind it.
	pending actionChoice
	// harnessPick is the claude/codex chooser, and harnessIndex its cursor.
	// It is its own flag rather than a mode of the form: one takes free
	// text, the other a bounded choice, and only the second can be
	// confirmed afterwards.
	harnessPick  bool
	harnessIndex int

	// busy is an action in flight. runningDesc names it ("stop crew_xyz")
	// for the running line and for the one message that can still be shown
	// if it is abandoned; startedAt is when it began, and the tick's running
	// line counts from it.
	busy        bool
	runningDesc string
	startedAt   time.Time
	// cancel cancels the context the in-flight ActionFunc runs under.
	// Bubble Tea 1.2.4 cannot stop that goroutine itself (tea.go: "we'll
	// have to leak the goroutine until Cmd returns"), so this is the one
	// signal quitting mid-action can still send it.
	cancel context.CancelFunc
	// done is closed when the in-flight ActionFunc returns, whether it
	// finished or was cancelled. A quit mid-action hands it to cmd/mate
	// (Model.AbandonedActionDone) so the process waits for the action's own
	// cleanup instead of exiting under it.
	done chan struct{}
	// abandoned is set when the captain quits while busy: the description
	// of what was abandoned, which cmd/mate reads (Model.AbandonedAction) to
	// say so plainly once the terminal is restored, since the Console has
	// nothing left to draw by then.
	abandoned string
	// afterRead is the last action's outcome, which the snapshot read the
	// action asked for puts back on the message line (takeAfterRead).
	afterRead *footerMsg
}

type actionConfirmation struct {
	choice actionChoice
	// key is the key that asked, and the only one that confirms; label is
	// the entry's own words ("Stop mate…"), which the sheet asks back.
	key, label string
}

// menuFacts is what the menu reads of the Console when it takes a key or
// draws a sheet: the glyphs, and facts of the tree on screen. Model builds
// it fresh each time (Model.menuFacts) and the menu keeps none of it, so a
// snapshot that lands while a sheet is open is what the next key acts on and
// what the next frame shows.
type menuFacts struct {
	g glyphSet
	// harnesses is the picker's rows, the default first (Model.harnessOrder),
	// and catalog is the snapshot's harnesses, for their marks.
	harnesses []query.HarnessKind
	catalog   []query.Harness
	// projects is the workspace's Projects, which a new name must not repeat.
	projects []query.ProjectNode
	// object names the row the sheets act on (Model.actionObjectTitle), and
	// objectSub is the confirmation's line under it (Model.confirmObjectSub).
	object    gline
	objectSub string
	// recorded is the harness the current Project's Mate records, and
	// project is that Project's name: the picker marks the one and names the
	// other.
	recorded query.HarnessKind
	project  string
}

// menuIntent is what a key in the menu asks of Model, which owns the tree,
// the panes, the message line and ActionFunc. It is carried out in the same
// Update (Model.carryOut): a Cmd would go round Bubble Tea's loop and land a
// message later. The zero value asks nothing.
type menuIntent struct {
	// note, when set, replaces the message line; an empty one clears it.
	note *footerMsg
	// run is a choice for Model to start through ActionFunc (runAction).
	run *actionChoice
	// entry is an actions-sheet entry that is not an action, for Model to
	// carry out on row (Model.carryOutEntry). An entry that is an action
	// comes back as run, or opens its confirmation inside the menu.
	entry *menuEntry
	row   row
}

// noted is an intent that only replaces the message line.
func noted(msg footerMsg) menuIntent { return menuIntent{note: &msg} }

// ---------- sheets ----------

// sheet is the menu's sheet on screen, in the order the frame draws them
// (Model.sheetOpen): a confirmation over the sheet it was asked from, then
// the form, the picker and the actions sheet; sheetNone when none is open.
func (a actionMenu) sheet() sheetKind {
	switch {
	case a.confirm != nil:
		return sheetConfirm
	case a.inputMode:
		return sheetNewProject
	case a.harnessPick:
		return sheetHarness
	case a.open:
		return sheetActions
	}
	return sheetNone
}

// closeSheets closes every sheet of the menu and forgets what they held,
// all but row, which only the next openSheet replaces. A running action is
// not a sheet: it goes on.
func (a actionMenu) closeSheets() actionMenu {
	a = a.closeForm().closePicker()
	a.open = false
	a.entries = nil
	a.index = 0
	a.confirm = nil
	return a
}

// openSheet opens the actions sheet for row r on entries, which Model built
// from the tree on screen (Model.menuFor). No entries open nothing.
func (a actionMenu) openSheet(r row, entries []menuEntry) actionMenu {
	a = a.closeSheets()
	a.row = r
	a.entries = entries
	a.open = len(entries) > 0
	return a
}

// openForm opens the new-project form for choice. The sheet is deliberately
// NOT opened behind it: Esc from the form returns the captain to the list
// they were on, rather than stranding them in a sheet they never asked for.
func (a actionMenu) openForm(choice actionChoice) actionMenu {
	a = a.closeSheets()
	a.inputMode = true
	a.pending = choice
	return a
}

// openPicker opens the harness picker for an already-built choice, with its
// cursor on index. Like the form it leaves the sheet closed, so Esc returns
// to the list rather than to a sheet the captain never opened.
func (a actionMenu) openPicker(choice actionChoice, index int) actionMenu {
	a = a.closeSheets()
	a.harnessPick = true
	a.harnessIndex = index
	a.pending = choice
	return a
}

// closeForm closes the new-project form and forgets the draft.
func (a actionMenu) closeForm() actionMenu {
	a.inputMode = false
	a.name, a.repo, a.field = "", "", fieldName
	a.pending = actionChoice{}
	return a
}

// closePicker closes the harness picker.
func (a actionMenu) closePicker() actionMenu {
	a.harnessPick = false
	a.harnessIndex = 0
	a.pending = actionChoice{}
	return a
}

// runPending is the tail of every way to an action: confirm first when the
// action is destructive, otherwise ask Model to run it.
func (a actionMenu) runPending(choice actionChoice) (actionMenu, menuIntent) {
	if choice.dangerous {
		a.confirm = &actionConfirmation{choice: choice}
		return a, menuIntent{}
	}
	return a, menuIntent{run: &choice}
}

// ---------- input ----------

// update takes the captain's input while one of the menu's sheets is open.
// A key goes to the sheet that has the keyboard: the form, the picker, the
// confirmation, then the actions sheet. The wheel moves a cursor and a
// click is Enter on the line clicked (onWheel, onClick). Any other message,
// or input while no sheet is open, asks nothing.
func (a actionMenu) update(msg tea.Msg, f menuFacts) (actionMenu, menuIntent) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case a.inputMode:
			return a.onFormKey(msg, f)
		case a.harnessPick:
			return a.onPickerKey(msg, f)
		case a.confirm != nil:
			return a.onConfirmKey(msg.String())
		case a.open:
			return a.onSheetKey(msg.String())
		}
	case menuWheel:
		return a.onWheel(msg.delta, f), menuIntent{}
	case menuClick:
		return a.onClick(msg.line, f)
	}
	return a, menuIntent{}
}

// onSheetKey is a key while the actions sheet is open.
func (a actionMenu) onSheetKey(key string) (actionMenu, menuIntent) {
	switch key {
	case "esc", "backspace":
		return a.closeSheets(), menuIntent{}
	case "up", "k":
		a.index = clampInt(a.index-1, 0, len(a.entries)-1)
		return a, menuIntent{}
	case "down", "j":
		a.index = clampInt(a.index+1, 0, len(a.entries)-1)
		return a, menuIntent{}
	case "enter":
		return a.runEntry(a.index)
	}
	for i, e := range a.entries {
		if e.key == key {
			a.index = i
			return a.runEntry(i)
		}
	}
	return a, menuIntent{}
}

// runEntry runs one entry of the open sheet. An entry that cannot run does
// nothing: its reason is already on the row. An entry that is not an action
// closes the sheet and goes to Model (carryOutEntry). A dangerous action
// opens its confirmation; any other action closes the sheet and asks Model
// to run it.
func (a actionMenu) runEntry(i int) (actionMenu, menuIntent) {
	if i < 0 || i >= len(a.entries) {
		return a, menuIntent{}
	}
	e := a.entries[i]
	if !e.enabled {
		return a, menuIntent{}
	}
	if e.kind != entryAction {
		return a.closeSheets(), menuIntent{entry: &e, row: a.row}
	}
	if e.confirms() {
		key := e.key
		if e.choice.confirmKey != "" {
			key = e.choice.confirmKey
		}
		a.confirm = &actionConfirmation{choice: e.choice, key: key, label: e.label}
		return a, menuIntent{}
	}
	return a.closeSheets(), menuIntent{run: &e.choice}
}

// onConfirmKey is a key while the confirm sheet is open: Enter cancels,
// the key that asked runs, Esc goes back to the actions (design G).
func (a actionMenu) onConfirmKey(key string) (actionMenu, menuIntent) {
	c := a.confirm
	switch key {
	case "enter":
		return a.closeSheets(), menuIntent{}
	case "esc", "backspace":
		a.confirm = nil
		if len(a.entries) == 0 {
			return a.closeSheets(), menuIntent{}
		}
		return a, menuIntent{}
	}
	if key == c.key {
		choice := c.choice
		return a.closeSheets(), menuIntent{run: &choice}
	}
	return a, menuIntent{}
}

// onboardField is one field of the new-project form: the Project's name,
// and optionally the git repository it starts with (`mate project add
// <name> [<repo>]`). A Project may be created with no repo and given repos
// later with `mate project repo add` (docs/mvp.md M9).
type onboardField int

const (
	fieldName onboardField = iota
	fieldRepo
)

// Input limits: a name longer than this is no name anyone types, and a path
// longer than PATH_MAX-ish is not a path this filesystem holds.
const (
	onboardNameLimit = 120
	onboardRepoLimit = 1024
)

// onFormKey is a key while the new-project form is open.
func (a actionMenu) onFormKey(msg tea.KeyMsg, f menuFacts) (actionMenu, menuIntent) {
	// field is the draft the keyboard types into, limit its length, and
	// label what the message line calls it.
	field, limit, label := &a.name, onboardNameLimit, "Project name"
	if a.field == fieldRepo {
		field, limit, label = &a.repo, onboardRepoLimit, "Repo path"
	}
	switch msg.String() {
	case "tab", "shift+tab", "up", "down":
		a.field = 1 - a.field
		return a, menuIntent{}
	case "esc", "backspace":
		if msg.String() == "backspace" {
			if *field != "" {
				r := []rune(*field)
				*field = string(r[:len(r)-1])
				return a, menuIntent{}
			}
			// Backspace on an empty Repo steps back into Name, the way
			// it would in any two-field form, rather than throwing the
			// typed name away.
			if a.field == fieldRepo {
				a.field = fieldName
				return a, menuIntent{}
			}
		}
		// Esc unwinds exactly one step: back to the sheet when the sheet is
		// what opened this form, and out of the menu entirely when the 'n'
		// key did (openForm leaves the sheet closed).
		if a.open {
			return a.closeForm(), menuIntent{}
		}
		return a.closeSheets(), menuIntent{}
	case "enter":
		if strings.TrimSpace(a.name) == "" {
			a.field = fieldName
			return a, noted(errMsg("Action refused: project name is required · nothing started"))
		}
		// A name the store would refuse is not sent: the rule line under
		// the field already says why, in amber (design K).
		if why := projectNameProblem(strings.TrimSpace(a.name), f.projects); why != "" {
			a.field = fieldName
			return a, menuIntent{}
		}
		// Enter on the name moves on to the repo, so the common case is
		// name, Enter, path, Enter - never a submit with the path unasked.
		// Enter on an empty repo is the answer "no repo yet": the Project
		// is created without one.
		if a.field == fieldName && strings.TrimSpace(a.repo) == "" {
			a.field = fieldRepo
			return a, noted(footerMsg{})
		}
		// The pending choice is the form's own, not whatever the sheet's
		// cursor happens to sit on: the 'n' key never opened a sheet.
		choice := a.pending
		if choice.action == "" {
			return a.closeSheets(), menuIntent{}
		}
		choice.req.Input = strings.TrimSpace(a.name)
		choice.req.Repo = strings.TrimSpace(a.repo)
		return a, menuIntent{run: &choice}
	}
	var do menuIntent
	for _, r := range msg.Runes {
		if unicode.IsPrint(r) && !unicode.IsSpace(r) || r == ' ' {
			if len([]rune(*field)) < limit {
				*field += string(r)
				do = noted(footerMsg{})
			} else {
				do = noted(infoMsg(fmt.Sprintf("%s limit reached (%d characters); further input is ignored", label, limit)))
			}
		}
	}
	return a, do
}

// projectNameProblem is why a typed name would be refused, or "". The shape
// is the store's own rule (internal/names); uniqueness is read off the
// Projects of the snapshot the captain is looking at.
func projectNameProblem(name string, projects []query.ProjectNode) string {
	if !names.ValidProject(name) {
		return "not a project name: " + names.ProjectRule
	}
	for _, p := range projects {
		if p.Name == name || p.ProjectID == name {
			return name + " already exists"
		}
	}
	return ""
}

// onPickerKey drives the picker. Enter completes the pending choice with
// the selected kind and then takes the same branch the sheet's Enter would:
// a dangerous action opens the confirmation, everything else runs.
func (a actionMenu) onPickerKey(msg tea.KeyMsg, f menuFacts) (actionMenu, menuIntent) {
	switch msg.String() {
	case "esc", "backspace":
		return a.closePicker(), menuIntent{}
	case "up", "k":
		if a.harnessIndex > 0 {
			a.harnessIndex--
		}
		return a, menuIntent{}
	case "down", "j":
		if a.harnessIndex+1 < len(f.harnesses) {
			a.harnessIndex++
		}
		return a, menuIntent{}
	case "enter":
		if a.harnessIndex >= len(f.harnesses) {
			// A snapshot that carries no harness catalog offers nothing
			// to pick; Enter is not a choice of a harness nobody listed.
			return a.closePicker(), menuIntent{}
		}
		choice := a.pending
		choice.req.Harness = f.harnesses[a.harnessIndex]
		return a.closePicker().runPending(choice)
	default:
		return a, menuIntent{}
	}
}

// ---------- the mouse ----------

// menuWheel and menuClick are the mouse over the menu's sheet, which Model
// hit-tests against the frame it drew: a turn of the wheel (delta is -1 or
// +1), and a click on line of the sheet's body (0 is its first line).
type menuWheel struct{ delta int }

type menuClick struct{ line int }

// onWheel moves the cursor of the actions sheet or of the picker. The
// confirmation and the form have none.
func (a actionMenu) onWheel(delta int, f menuFacts) actionMenu {
	switch a.sheet() {
	case sheetActions:
		a.index = clampInt(a.index+delta, 0, max0(len(a.entries)-1))
	case sheetHarness:
		a.harnessIndex = clampInt(a.harnessIndex+delta, 0, len(f.harnesses)-1)
	}
	return a
}

// onClick is a click on line i of the sheet's body. On an entry of the
// actions sheet or a row of the picker it is Enter on that line, like every
// other row; anywhere else it does nothing.
func (a actionMenu) onClick(i int, f menuFacts) (actionMenu, menuIntent) {
	switch a.sheet() {
	case sheetActions:
		if i >= 0 && i < len(a.entries) {
			a.index = i
			return a.runEntry(i)
		}
	case sheetHarness:
		if i >= 0 && i < len(f.harnesses) {
			a.harnessIndex = i
			return a.onPickerKey(tea.KeyMsg{Type: tea.KeyEnter}, f)
		}
	}
	return a, menuIntent{}
}

// ---------- the running action ----------

// start runs choice through action and returns the Cmd that calls it. The
// sheets the choice came from close, and the menu is busy until finish.
//
// The context is a child of base - the program's own (Model.baseCtx), not
// context.Background() - and its cancel is kept, so quitting mid-action
// (abandon) has a real signal to send the goroutine Bubble Tea otherwise
// leaks until it returns on its own.
func (a actionMenu) start(choice actionChoice, action ActionFunc, base context.Context) (actionMenu, tea.Cmd) {
	a.open = false
	a.inputMode = false
	a.harnessPick = false
	a.pending = actionChoice{}
	a.busy = true
	a.runningDesc = string(choice.action) + " " + actionObject(choice)
	a.startedAt = time.Now()
	ctx, cancel := context.WithCancel(base)
	a.cancel = cancel
	done := make(chan struct{})
	a.done = done
	req := choice.req
	return a, func() tea.Msg {
		defer close(done)
		if action == nil {
			return actionDoneMsg{choice: choice, err: fmt.Errorf("Console actions are not wired")}
		}
		text, err := action(ctx, req)
		return actionDoneMsg{choice: choice, text: text, err: err}
	}
}

// runningLine is the message line while an action runs. Past the first
// second it carries the time spent, so a slow start reads as progress
// rather than a hang.
func (a actionMenu) runningLine(elapsed time.Duration, g glyphSet) footerMsg {
	// The elapsed time leads: on a 40-column status line the object's name
	// is what gets cut, never how long it has been running.
	text := "Running " + a.runningDesc + " " + g.Ellipsis
	if elapsed >= time.Second {
		text = elapsed.Truncate(time.Second).String() + " " + g.Dot + " " + text
	}
	return infoMsg(text)
}

// runningAt is the running line as of at, the refresh tick's clock; false
// when no action runs or either clock is unset.
func (a actionMenu) runningAt(at time.Time, g glyphSet) (footerMsg, bool) {
	if !a.busy || a.startedAt.IsZero() || at.IsZero() {
		return footerMsg{}, false
	}
	return a.runningLine(at.Sub(a.startedAt), g), true
}

// finish is the in-flight action's return (its actionDoneMsg): nothing runs
// any more, and the sheets it was started from are gone.
func (a actionMenu) finish() actionMenu {
	a.busy = false
	a.cancel = nil
	a.runningDesc = ""
	a.confirm = nil
	a.open = false
	a.entries = nil
	return a
}

// abandon is q or Ctrl+C while busy. It cancels the context the action is
// running under - the one signal available, since Bubble Tea leaks the
// goroutine rather than stopping it - and records what was abandoned, so
// cmd/mate can tell the captain plainly once the terminal is back
// (Model.AbandonedAction). The Console has nothing left to draw once
// tea.Quit takes effect, which is why this cannot just be the message line:
// it is the one thing that survives past View returning empty.
func (a actionMenu) abandon() actionMenu {
	if a.cancel != nil {
		a.cancel()
	}
	desc := a.runningDesc
	if desc == "" {
		desc = "an action"
	}
	a.abandoned = desc
	return a
}

// setAfterRead keeps outcome for the snapshot read the action asked for.
func (a actionMenu) setAfterRead(outcome footerMsg) actionMenu {
	a.afterRead = &outcome
	return a
}

// takeAfterRead hands over the outcome waiting for its read, if any, and
// forgets it: one read puts it back on the message line, never two.
func (a actionMenu) takeAfterRead() (actionMenu, *footerMsg) {
	outcome := a.afterRead
	a.afterRead = nil
	return a, outcome
}

// ---------- drawing ----------

// title is the open sheet's rule title and meta.
func (a actionMenu) title(f menuFacts) (gline, string) {
	switch a.sheet() {
	case sheetActions:
		t := gl().add("actions", tAcc)
		if obj := f.object; len(obj.segs) > 0 {
			t = t.add(" "+f.g.Dot+" ", tDim).join(dimmed(obj))
		}
		return t, "esc"
	case sheetConfirm:
		return gl().add(confirmVerb(a.confirm.choice, a.confirm.label)+"?", tAcc), "esc"
	case sheetNewProject:
		return gl().add("new project", tAcc), "esc"
	case sheetHarness:
		return gl().add("harness", tAcc).add(" "+f.g.Dot+" new mate", tDim), "esc"
	}
	return gl(), ""
}

// keyHints is the key line while one of the menu's sheets is open.
func (a actionMenu) keyHints(g glyphSet) []keyHint {
	switch a.sheet() {
	case sheetActions:
		return []keyHint{{g.UpDown, "move"}, {"enter", "run"}, {"or its key", ""}, {"esc", ""}}
	case sheetConfirm:
		verb := strings.Fields(confirmVerb(a.confirm.choice, a.confirm.label))
		return []keyHint{{"enter", "cancel"}, {a.confirm.key, verb[0]}, {"esc", "back"}}
	case sheetNewProject:
		return []keyHint{{"enter", "create"}, {"tab", "field"}, {"esc", "cancel"}}
	case sheetHarness:
		return []keyHint{{g.UpDown, "move"}, {"enter", "use"}, {"esc", "cancel"}}
	}
	return nil
}

// view is the open sheet's body at width w, under the rule Model draws
// from title.
func (a actionMenu) view(w int, f menuFacts) []gline {
	switch a.sheet() {
	case sheetActions:
		return a.actionsLines(w, f.g)
	case sheetConfirm:
		return a.confirmLines(w, f)
	case sheetNewProject:
		return a.formLines(w, f)
	case sheetHarness:
		return a.pickerLines(w, f)
	}
	return nil
}

// ---------- F: actions ----------

// menuKeyWidth is the key column: "enter" and two blanks.
const menuKeyWidth = 7

// actionsLines draws the actions sheet: its entries, then what the one
// under the cursor does.
func (a actionMenu) actionsLines(w int, g glyphSet) []gline {
	var out []gline
	for i, e := range a.entries {
		on := i == a.index
		lead := gl().pad(1)
		if on {
			lead = gl().add(g.Selected, tAcc)
		}
		var l gline
		if e.enabled {
			right := gl()
			if e.confirms() {
				right = gl().add("confirm", tDim).pad(1)
			}
			l = spread(gl().pad(1).add(padRight(e.key, menuKeyWidth), tFg).add(e.label, tFg), right, w-1, g)
		} else {
			// The label keeps its cells; the reason is cut to what is left.
			left := gl().pad(1).add(padRight(g.Dot, menuKeyWidth), tFaint).add(e.label, tFaint)
			room := w - 1 - left.width() - 2
			reason := gl()
			if room >= 4 {
				reason = gl().add(truncateEnd(e.reason, room-1, g), tDim).pad(1)
			}
			l = spread(left, reason, w-1, g)
		}
		out = append(out, lead.join(l).padTo(w, g).selected(on))
	}
	if i := a.index; i >= 0 && i < len(a.entries) && a.entries[i].about != "" {
		out = append(out, gl())
		for _, part := range wrapWords(a.entries[i].about, w-4) {
			out = append(out, gl().pad(2).add(part, tDim))
		}
	}
	return out
}

// ---------- G: confirm ----------

// confirmLabelWidth is the confirm sheet's label column.
const confirmLabelWidth = 10

// confirmVerb is the question the sheet asks: "stop mate", "merge crew".
func confirmVerb(c actionChoice, label string) string {
	if label != "" {
		return strings.ToLower(strings.TrimSuffix(label, "…"))
	}
	return string(c.action)
}

func (a actionMenu) confirmLines(w int, f menuFacts) []gline {
	c := a.confirm
	object, scope, effect := actionObjectDescription(c.choice)
	vw := maxInt(8, w-confirmLabelWidth-1)
	block := func(label string, lines []gline) []gline {
		out := make([]gline, 0, len(lines)+1)
		for i, l := range lines {
			lead := gl().pad(confirmLabelWidth)
			if i == 0 {
				lead = gl().pad(2).add(padRight(label, confirmLabelWidth-2), tDim)
			}
			out = append(out, lead.join(l))
		}
		return append(out, gl())
	}
	objLines := []gline{gl().add(object, tFg)}
	if obj := f.object; len(obj.segs) > 0 {
		objLines = []gline{obj}
		if sub := f.objectSub; sub != "" {
			objLines = append(objLines, wrapped(sub, tDim, vw)...)
		}
	}
	out := []gline{gl()}
	if c.choice.confirmPrompt != "" {
		// The action's own question, when it has one: a merge names both
		// branches here, where the key that runs it is pressed.
		for _, part := range wrapWords(c.choice.confirmPrompt, w-4) {
			out = append(out, gl().pad(2).add(part, tFg))
		}
		out = append(out, gl())
	}
	out = append(out, block("object", objLines)...)
	out = append(out, block("scope", wrapped(scope, tDim, vw))...)
	out = append(out, block("effect", wrapped(effect, tDim, vw))...)
	verb := confirmVerb(c.choice, c.label)
	out = append(out,
		gl().add(f.g.Selected, tAcc).pad(1).add(padRight("enter", menuKeyWidth), tFg).add("cancel", tFg).padTo(w, f.g).selected(true),
		gl().pad(2).add(padRight(c.key, menuKeyWidth), tFg).add(verb, tRed),
	)
	return out
}

// actionObjectDescription is the confirm sheet's scope and effect, in the
// words each dangerous action needs (design G: object, scope, effect).
func actionObjectDescription(c actionChoice) (string, string, string) {
	object := c.req.Target
	if object == "" {
		object = "workspace"
	}
	if c.req.Crew != "" {
		object += "/" + c.req.Crew
	}
	scope := "Recorded state and the runtime resources named by this action."
	effect := "The snapshot is re-read when the service returns."
	switch c.req.Action {
	case ActionStop:
		if c.req.TargetKind == "crew" {
			scope = "This crew's agent, tab, worktree and branch. Its brief, report and status file stay."
			effect = "Refused, changing nothing, if the branch is not landed or the worktree is dirty. Otherwise the agent stops, the worktree and branch are removed and the task is finished."
			break
		}
		scope = "This Mate only. Its crews keep running."
		effect = "The agent stops; the binding is released once the service confirms it."
	case ActionRemoveProject:
		scope = "This Project's Mate and every running crew; the Project leaves the workspace list."
		effect = "Refused, leaving the Project listed, if any agent cannot be stopped (a crew with unlanded work, for one). Memory, backlog, crew records and repos stay; each crew is stopped as Stop crew does, so a landed branch and its worktree go with it."
	case ActionRestartMate:
		scope = "This Mate only. Its crews keep running."
		effect = "The Mate is stopped and started again; the next pane shows the new one."
	case ActionRepair:
		scope = "The recorded binding only; no live agent is assumed."
		effect = "A stale binding is cleared. Unknown state is never treated as safe to clean."
	case ActionRestartCrew:
		object = c.req.Target + "/" + c.req.Crew
		scope = "This Crew's agent and pane only. Its worktree, branch and status file stay."
		effect = "A live agent is stopped first; then the harness starts again in the same worktree and the brief is pointed at once more."
	case ActionMerge:
		object = c.req.Target + "/" + c.req.Crew
		scope = "The Project's default branch, and this crew's branch, worktree and pane."
		effect = "Fast-forward only; anything else refuses and changes nothing. If it lands, the crew is finished and its branch and worktree are removed."
	}
	return object, scope, effect
}

// ---------- K: new project ----------

func (a actionMenu) formLines(w int, f menuFacts) []gline {
	g := f.g
	input := func(which onboardField, value, placeholder string) gline {
		if a.field == which {
			return gl().add(g.Selected, tAcc).pad(1).add(value, tFg).add(g.Cursor, tAcc).padTo(w, g).selected(true)
		}
		if value == "" {
			return gl().pad(2).add(placeholder, tFaint)
		}
		return gl().pad(2).add(value, tFg)
	}
	name := strings.TrimSpace(a.name)
	rule, ruleTok := names.ProjectRule+", unique", tDim
	if why := projectNameProblem(name, f.projects); why != "" && name != "" {
		rule, ruleTok = why, tAmber
	}
	out := []gline{
		gl().pad(2).add("name", tDim),
		input(fieldName, a.name, ""),
	}
	for _, part := range wrapWords(rule, w-6) {
		out = append(out, gl().pad(4).add(part, ruleTok))
	}
	out = append(out,
		gl().pad(2).add("repo "+g.Dot+" optional", tDim),
		input(fieldRepo, a.repo, "path or git url"),
		gl().pad(4).add("leave blank to add later", tDim),
		gl(),
	)
	enter := "create " + name
	if name == "" {
		enter = "create"
	}
	out = append(out,
		gl().pad(2).add(padRight("enter", menuKeyWidth), tDim).add(enter, tFg),
		gl().pad(2+menuKeyWidth).add("no mate is started", tDim),
		gl().pad(2).add(padRight("tab", menuKeyWidth), tDim).add("next field", tFg),
		gl().pad(2).add(padRight("esc", menuKeyWidth), tDim).add("cancel", tFg),
	)
	return out
}

// ---------- the harness picker ----------

func (a actionMenu) pickerLines(w int, f menuFacts) []gline {
	var out []gline
	for i, kind := range f.harnesses {
		on := i == a.harnessIndex
		lead := gl().pad(1)
		if on {
			lead = gl().add(f.g.Selected, tAcc)
		}
		l := lead.pad(1).add(harnessIcon(string(kind), f.g, f.catalog), tFg).pad(1).add(string(kind), tFg)
		if f.recorded == kind {
			l = l.add("  current", tDim)
		}
		out = append(out, l.padTo(w, f.g).selected(on))
	}
	out = append(out, gl())
	for _, part := range wrapWords("Creates and starts the Mate of "+f.project+" with this harness.", w-4) {
		out = append(out, gl().pad(2).add(part, tDim))
	}
	return out
}
