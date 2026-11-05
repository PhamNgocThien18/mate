package console

import (
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"strconv"

	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/query"
)

// The box's keys, live while Tab has focused it (design D): ↑↓ move, Enter
// shows the item's crew in the next pane - where the captain answers inside
// that crew's own session - and `a` or a click on [assign] hands the item
// to the Mate. Moving the selection moves the list to the crew too, so
// detail explains why it waits. Every action still goes through
// ActionFunc; the box itself decides nothing.

func restartMateChoice(project string) actionChoice {
	return actionChoice{
		action: coreapi.ActionRestartMate, enabled: true, dangerous: true,
		desc: "Restart the Mate of " + project,
		req: coreapi.ActionRequest{
			Action: coreapi.ActionRestartMate, Target: project, TargetKind: "project",
		},
	}
}

func clearComposerChoice(project string) actionChoice {
	return actionChoice{
		action: coreapi.ActionClearComposer, enabled: true,
		desc: "Clear the Mate's composer",
		req: coreapi.ActionRequest{
			Action: coreapi.ActionClearComposer, Target: project, TargetKind: "project",
		},
	}
}

// onBoxKey is a key while the box has focus.
func (m Model) onBoxKey(key string) (Model, tea.Cmd) {
	items := m.boxItems()
	sel := m.boxSelection(items)
	switch key {
	case "esc", "backspace":
		m.focus = paneList
		return m, nil
	case "l":
		return m.toggleBoxAll(), nil
	case "up", "k":
		return m.selectBoxItem(items, sel-1), nil
	case "down", "j":
		return m.selectBoxItem(items, sel+1), nil
	}
	if sel < 0 {
		return m, nil
	}
	switch key {
	case "enter":
		return m.showBoxItem(items[sel])
	case "a":
		return m.beginBoxAssign(items[sel])
	}
	return m, nil
}

// selectBoxItem moves the box selection, and the list with it.
func (m Model) selectBoxItem(items []boxItem, i int) Model {
	if len(items) == 0 {
		return m
	}
	i = clampInt(i, 0, len(items)-1)
	m.boxSel = i
	m.msg = footerMsg{}
	return m.followBoxItem(items[i])
}

// followBoxItem puts the list selection on the row an item names: its crew
// (or the Mate, for the Mate's own incident) on a Project, its Project on
// the workspace.
func (m Model) followBoxItem(it boxItem) Model {
	f := m.cur()
	id := it.project
	if f.kind == frameProject {
		id = mateRowID(m.currentProject().Mate)
		if it.e.Crew != "" && it.e.Crew != "mate" {
			if _, ok := m.crewByID(it.e.Crew); ok {
				id = it.e.Crew
			}
		}
	}
	rows := m.currentRows()
	if i := indexOfRowID(rows, id); i >= 0 {
		f.sel, f.selID = i, id
		m = m.setCur(f)
	}
	m.detailSel = 0
	return m.relayout()
}

// toggleBoxAll flips between the inbox and the whole log (`l`). It is
// Console state for the run and is never persisted: a debugging view that
// survived a restart would quietly become the default.
func (m Model) toggleBoxAll() Model {
	m.boxAll = !m.boxAll
	m.boxSel = -1
	m.msg = footerMsg{}
	return m
}

// beginBoxActions is the actions sheet of the Project's Mate, the box's
// own owner.
func (m Model) beginBoxActions() Model {
	if m.cur().kind != frameProject {
		return m
	}
	r := row{kind: rowMate, id: mateRowID(m.currentProject().Mate)}
	m.actions = m.actions.openSheet(r, m.menuFor(r, true))
	return m
}

// beginBoxAssign is `a`, and the item's own [assign] button: hand the item
// to the Mate and ask it to answer the crew. A message holds no question -
// only `l` can put one under the cursor - and the refusal says so rather
// than doing nothing.
func (m Model) beginBoxAssign(it boxItem) (Model, tea.Cmd) {
	if it.news != nil {
		m.msg = errMsg("Assign refused: the Mate opened this news for you " + m.g.Dot + " Enter answers it in the Mate")
		return m, nil
	}
	if !it.e.Resolvable() {
		m.msg = errMsg("Assign refused: a " + string(it.e.Kind) + " entry holds no question " + m.g.Dot + " nothing was sent")
		return m, nil
	}
	if m.actions.busy {
		return m, nil
	}
	return m.runAction(boxAssignChoice(it.project, it.e))
}

// boxAssignChoice acts on a box entry, not on the selected row, which is
// why it is built here rather than in the actions sheet.
func boxAssignChoice(project string, e query.BoxEntry) actionChoice {
	return actionChoice{
		action: coreapi.ActionResolve, enabled: true,
		desc: "Ask the Mate to resolve " + e.Crew + "'s " + e.Verb,
		req: coreapi.ActionRequest{
			Action: coreapi.ActionResolve, Target: project, TargetKind: "project",
			Crew: e.Crew, Input: e.Resolve, Key: e.AssignKey,
		},
	}
}

// showBoxItem is Enter on an item: show its crew in the next pane, the
// same way Enter on that crew's row does, so the box cannot show a pane
// the list would refuse. On the workspace level the crew's row lives on
// its Project, which opens first; the Mates list already has the Mate's.
func (m Model) showBoxItem(it boxItem) (Model, tea.Cmd) {
	if it.e.Crew == "" {
		m.msg = errMsg("Show refused: this entry names no crew " + m.g.Dot + " nothing was shown")
		return m, nil
	}
	switch {
	case m.cur().kind == frameProject:
	case it.e.Crew != "mate":
		if jumped, ok := m.jumpToCrew(it.e.Crew); ok {
			m = jumped
		}
	case m.cur().kind == frameWorkspace:
		m = m.open(frame{kind: frameProject, id: it.project})
	}
	r, ok := m.boxItemRow(it)
	if !ok {
		m.msg = errMsg("Show refused: crew " + it.e.Crew + " is not in this snapshot; it was closed " + m.g.Dot + " nothing was shown")
		return m, nil
	}
	m, stage := m.stageRow(r)
	if it.news == nil {
		return m, stage
	}
	return m, tea.Batch(stage, m.openNews(it.project, it.news.ID))
}

// newsOpenedMsg is the REPL's answer to an open-news request: nil once the
// request is written for the REPL to read.
type newsOpenedMsg struct{ err error }

// openNews asks the Project's REPL to open news id beside the box: the
// Mate's pane shows it, and the captain answers there.
func (m Model) openNews(project string, id int) tea.Cmd {
	action, ctx := m.action, m.baseCtx()
	req := coreapi.ActionRequest{Action: coreapi.ActionOpenNews, Target: project, TargetKind: "project", Input: strconv.Itoa(id)}
	return func() tea.Msg {
		if action == nil {
			return newsOpenedMsg{err: fmt.Errorf("Console actions are not wired")}
		}
		_, err := action(ctx, req)
		return newsOpenedMsg{err: err}
	}
}

// boxItemRow is the Project row an item names.
func (m Model) boxItemRow(it boxItem) (row, bool) {
	if m.cur().kind == frameMates && it.e.Crew == "mate" {
		return row{kind: rowMate, id: it.project}, true
	}
	if m.cur().kind != frameProject {
		return row{}, false
	}
	if it.e.Crew == "mate" {
		return row{kind: rowMate, id: mateRowID(m.currentProject().Mate)}, true
	}
	if _, ok := m.crewByID(it.e.Crew); !ok {
		return row{}, false
	}
	return row{kind: rowCrew, id: it.e.Crew}, true
}
