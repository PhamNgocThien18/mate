package console

import (
	tea "github.com/charmbracelet/bubbletea"
)

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m.relayout(), nil
	case treeLoadedMsg:
		m = m.onTreeLoaded(msg)
		// The auto-refresh chain starts here, once, on the first
		// treeLoadedMsg the Console ever sees - see Init's own comment for
		// why not there directly. Every later treeLoadedMsg (a manual 'r',
		// an action's own re-read, or the chain's own
		// tick) finds treeTickStarted already true and this is a no-op.
		if !m.treeTickStarted {
			m.treeTickStarted = true
			return m, treeTickCmd(m.treeTickInterval(), m.treeGen)
		}
		return m, nil
	case treeTickMsg:
		return m.onTreeTick(msg)
	case actionDoneMsg:
		return m.onActionDone(msg)
	case stageDoneMsg:
		return m.onStageDone(msg), nil
	case workOpenedMsg:
		return m.onWorkOpened(msg)
	case newsOpenedMsg:
		if msg.err != nil {
			m.msg = errMsg("Open news refused: " + msg.err.Error())
		}
		return m, nil
	case commandViewDoneMsg:
		return m.onCommandViewDone(msg), nil
	case tea.KeyMsg:
		return m.onKey(msg)
	case tea.MouseMsg:
		return m.onMouse(msg)
	default:
		return m, nil
	}
}

// onTreeLoaded applies a fresh read. The first load starts at the top of
// the tree (there is no prior position to keep); every later refresh - only
// 'r' and the return from an attach trigger one - re-anchors the whole
// navigation stack by identity instead of discarding it, then clamps
// whatever the new tree no longer contains (see reconcileSelection).
func (m Model) onTreeLoaded(msg treeLoadedMsg) Model {
	m.treeLoadInFlight = false
	if msg.err != nil {
		if m.hasLoaded {
			// The header's "stale" wording (frame.go) reads this rather than
			// m.msg: the footer line below is cleared by the reader's very
			// next keystroke, but the tree on screen stays exactly what the
			// last successful load said until another one succeeds, so the
			// header keeps saying so for exactly as long as that is true.
			m.lastLoadErr = msg.err
			var outcome *footerMsg
			if m.actions, outcome = m.actions.takeAfterRead(); outcome != nil {
				m.msg = *outcome
				m.msg.text += " " + m.g.Dot + " snapshot re-read failed; r retries"
				return m
			}
			// A refresh that failed does not throw away the snapshot that is
			// already on screen: the error page says "no earlier snapshot is
			// loaded", which would be false, and the reader would lose a
			// usable picture over a transient read. The header already says
			// "stale" and how old what they are looking at is; the message
			// line says the refresh did not happen.
			m.msg = errMsg("Refresh failed: " + msg.err.Error() + " " + m.g.Dot +
				" still showing the snapshot from " + m.tree.AsOf.Format("15:04:05"))
			return m
		}
		m.phase = phaseFailed
		m.loadErr = msg.err
		m.msg = errMsg("Snapshot read failed")
		return m
	}
	first := !m.hasLoaded
	m.phase = phaseReady
	m.loadErr = nil
	m.lastLoadErr = nil
	m.tree = msg.tree
	m.hasLoaded = true
	if first {
		m.stack = []frame{{kind: m.rootKind()}}
		m.focus = paneList
		m.detail = false
		m.msg = footerMsg{}
		m = m.relayout()
		return m.applyActionAfterRead()
	}
	m = m.reconcileSelection()
	return m.applyActionAfterRead()
}

// startLoad issues a tree load and marks it in flight, whether it was asked
// for by the reader ('r'), the failed-phase retry, or a background tick
// (onTreeTick). Every call site that can issue a loadCmd goes through this
// rather than calling loadCmd directly, so treeLoadInFlight always reflects
// whether a load is actually outstanding.
func (m Model) startLoad() (Model, tea.Cmd) {
	m.treeLoadInFlight = true
	return m, loadCmd(m.load)
}

// onTreeTick is the auto-refresh chain (Init, treeTickCmd): every
// treeTickInterval it starts a background load unless one is already in
// flight, then always reschedules itself so the chain never stops on its
// own. A tick whose gen no longer matches m.treeGen is a leftover from a
// chain Init did not start (never happens in production - see Init's own
// comment - but the same defence session_mode.go's ticks carry) and is
// dropped without rescheduling, since the chain that owns treeGen is
// already rescheduling itself.
func (m Model) onTreeTick(msg treeTickMsg) (Model, tea.Cmd) {
	if msg.gen != m.treeGen {
		return m, nil
	}
	if line, ok := m.actions.runningAt(msg.at, m.g); ok {
		m.msg = line
	}
	next := treeTickCmd(m.treeTickInterval(), m.treeGen)
	if m.treeLoadInFlight {
		return m, next
	}
	m, load := m.startLoad()
	return m, tea.Batch(load, next)
}

// applyActionAfterRead puts the last action's outcome back on the message
// line once the read it asked for has landed (actionMenu.takeAfterRead).
func (m Model) applyActionAfterRead() Model {
	var outcome *footerMsg
	if m.actions, outcome = m.actions.takeAfterRead(); outcome != nil {
		m.msg = *outcome
	}
	return m
}

// onEnter opens a Project row, toggles a crew group, and shows a
// Mate or Crew row in the next pane (stageRow).
func (m Model) onEnter() (tea.Model, tea.Cmd) {
	r, ok := m.selectedRow()
	if !ok {
		return m, nil
	}
	switch r.kind {
	case rowProject:
		return m.open(frame{kind: frameProject, id: r.id}), nil
	case rowCompletedGroup:
		parent := m.cur().id
		if m.completedOpen == nil {
			m.completedOpen = map[string]bool{}
		}
		m.completedOpen[parent] = !m.completedOpen[parent]
		m.detailSel = 0
		return m.relayout(), nil
	case rowHandedBackGroup:
		parent := m.cur().id
		if m.handedBackOpen == nil {
			m.handedBackOpen = map[string]bool{}
		}
		m.handedBackOpen[parent] = !m.handedBackOpen[parent]
		m.detailSel = 0
		return m.relayout(), nil
	case rowMate, rowCrew:
		return m.stageRow(r)
	default:
		return m, nil
	}
}

// open pushes a new frame and puts the selection on its first row.
func (m Model) open(f frame) Model {
	m.msg = footerMsg{}
	m.focus = paneList
	m.detail = false
	m.diff = diffFlow{}
	m.detailSel = 0
	m.boxSel = -1
	return m.push(f).relayout()
}
