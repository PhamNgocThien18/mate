package console

import (
	"fmt"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// Bottom sheets (design F, G, K): actions, confirm, new project, the
// harness picker, a diff, and the key list. A sheet replaces the lower
// panes and never covers the selected row (layout.go's planStacked). The
// action menu draws its own four (action_menu.go); this file draws the rest
// and puts whichever sheet is open in the frame.

type sheetKind int

const (
	sheetNone sheetKind = iota
	sheetActions
	sheetConfirm
	sheetNewProject
	sheetHarness
	sheetDiff
	sheetKeys
	sheetMessage
	sheetWork
)

// sheetOpen is the sheet on screen, in the order the keyboard reaches them.
func (m Model) sheetOpen() sheetKind {
	if m.diff.open {
		return sheetDiff
	}
	if s := m.actions.sheet(); s != sheetNone {
		return s
	}
	switch {
	case m.keysOpen:
		return sheetKeys
	case m.shownMsg != nil:
		return sheetMessage
	case m.work.open:
		return sheetWork
	}
	return sheetNone
}

// menuOnScreen reports whether the sheet on screen is one of the action
// menu's, which the menu draws and takes the input of itself.
func (m Model) menuOnScreen() bool {
	s := m.actions.sheet()
	return s != sheetNone && s == m.sheetOpen()
}

// sheetTitle is the open sheet's rule title and meta.
func (m Model) sheetTitle() (gline, string) {
	if m.menuOnScreen() {
		return m.actions.title(m.menuFacts())
	}
	switch m.sheetOpen() {
	case sheetDiff:
		return gl().add(m.diffTitle(), tAcc), "esc"
	case sheetKeys:
		return gl().add("keys", tAcc), "any key"
	case sheetMessage:
		return gl().add("message", tAcc), "any key"
	case sheetWork:
		return gl().add("Work", tAcc).add(" "+m.g.Dot+" ", tDim).add(m.workProjectName(), tDim), "esc"
	}
	return gl(), ""
}

// dimmed is l with every segment in the dim token.
func dimmed(l gline) gline {
	out := gl()
	for _, s := range l.segs {
		out = out.add(s.text, tDim)
	}
	return out
}

// actionObjectTitle names the row the action menu acts on.
func (m Model) actionObjectTitle() gline {
	r := m.actions.row
	switch r.kind {
	case rowMate:
		return kindName(m.g.Mate, m.currentProject().Name)
	case rowCrew:
		if c, ok := m.crewByID(r.id); ok {
			return kindName(m.g.Crew, shortID(c.CrewID, m.g))
		}
	case rowProject:
		if p, ok := m.projectByID(r.id); ok {
			return gl().add(p.Name, tFg)
		}
	}
	return gl()
}

// sheetLines draws the open sheet's body.
func (m Model) sheetLines(p framePlan) []gline {
	if m.menuOnScreen() {
		return m.actions.view(p.w, m.menuFacts())
	}
	switch m.sheetOpen() {
	case sheetDiff:
		return m.diffSheetLines(p)
	case sheetKeys:
		return m.keysSheetLines(p)
	case sheetMessage:
		return m.messageSheetLines(p)
	case sheetWork:
		lines, _ := m.workSheetLines(p)
		return lines
	}
	return nil
}

// confirmObjectSub is the line under the object: its agent and status.
func (m Model) confirmObjectSub(r row) string {
	switch r.kind {
	case rowMate:
		mate := m.currentProject().Mate
		parts := []string{}
		if a := m.mateAgent(mate); a != "" {
			parts = append(parts, a)
		}
		parts = append(parts, mateWord(mate))
		return strings.Join(parts, " "+m.g.Dot+" ")
	case rowCrew:
		if c, ok := m.crewByID(r.id); ok {
			return string(c.HarnessKind) + " " + m.g.Dot + " " + crewWord(c)
		}
	}
	return ""
}

// ---------- a diff ----------

func (m Model) diffSheetLines(p framePlan) []gline {
	raw := m.diffLines(p.w)
	out := make([]gline, 0, len(raw))
	for _, t := range raw[clampInt(m.diff.top, 0, max0(len(raw)-1)):] {
		out = append(out, gl().pad(1).add(t, diffLineTok(t)).cut(p.w, m.g))
	}
	return out
}

func diffTextLines(text string) []string {
	if strings.TrimSpace(text) == "" {
		return []string{"the command returned nothing"}
	}
	return strings.Split(strings.TrimRight(text, "\n"), "\n")
}

// diffLineTok tints a unified-diff line: additions green, removals red,
// headers dim.
func diffLineTok(text string) tok {
	switch {
	case strings.HasPrefix(text, "+++"), strings.HasPrefix(text, "---"):
		return tDim
	case strings.HasPrefix(text, "+"):
		return tGreen
	case strings.HasPrefix(text, "-"):
		return tRed
	case strings.HasPrefix(text, "@@"), strings.HasPrefix(text, "diff --git"),
		strings.HasPrefix(text, "index "), strings.HasPrefix(text, "new file"),
		strings.HasPrefix(text, "deleted file"), strings.HasPrefix(text, "similarity index"),
		strings.HasPrefix(text, "rename "):
		return tDim
	}
	return tFg
}

// ---------- ?: the key list ----------

// keyTable is design I's "7 Keys and mouse", less what this Console does
// not do.
var keyTable = [][2]string{
	{"↑ ↓", "move; fields in detail"},
	{"enter", "show in next pane"},
	{"a", "actions"},
	{"tab", "next pane"},
	{"esc", "back to the list"},
	{"n", "new project"},
	{"s", "start the mate"},
	{"m", "flip its mode"},
	{"v", "projects / mates only"},
	{"y", "copy"},
	{"w", "work board of the mate"},
	toolKeyRows,
	{"l", "box: whole log"},
	{"r", "refresh"},
	{"q", "quit"},
	{"click", "same as enter"},
	{"click rule", "focus that pane"},
	{"[assign]", "hand to the mate"},
	{"wheel", "scroll that pane"},
	{"shift+drag", "select text"},
}

// toolKeyRows marks where keyRows draws the snapshot's tool keys.
var toolKeyRows = [2]string{"", "tool keys"}

// keyRows is keyTable with the tool keys of the snapshot in their place,
// each as "<scope> <label>": "e  crew report". A binding on a key the
// Console owns (ownedKeys) is never pressed through to its tool, so it is
// not offered, and the Console's own row for that key stays.
func (m Model) keyRows() [][2]string {
	out := make([][2]string, 0, len(keyTable)+len(m.tree.Commands))
	for _, k := range keyTable {
		if k == toolKeyRows {
			for _, b := range m.tree.Commands {
				if !ownedKeys[b.Key] {
					out = append(out, [2]string{b.Key, b.Scope + " " + b.Label})
				}
			}
			continue
		}
		out = append(out, k)
	}
	return out
}

func (m Model) keysSheetLines(p framePlan) []gline {
	rows := m.keyRows()
	kw := 0
	for _, k := range rows {
		kw = maxInt(kw, cells(k[0]))
	}
	kw = minInt(kw+2, p.w/3)
	out := make([]gline, 0, len(rows))
	for _, k := range rows {
		key := k[0]
		if m.g.Name == "ascii" {
			key = strings.NewReplacer("↑ ↓", "^ v", "▸", "+", "·", ".").Replace(key)
		}
		out = append(out, gl().pad(2).add(padRight(key, kw), tFg).add(strings.ReplaceAll(k[1], "·", m.g.Dot), tDim).cut(p.w, m.g))
	}
	return out
}

// recordedHarness is the current Project's Mate harness, or "".
func (m Model) recordedHarness() query.HarnessKind {
	mate := m.currentProject().Mate
	if !mate.Designated.IsKnown() {
		return ""
	}
	return mate.Designated.Value.HarnessKind
}

// itoa is fmt.Sprint for an int, for the few places a count is text.
func itoa(n int) string { return fmt.Sprint(n) }

// messageSheetLines is the status line in full: its text wrapped, then
// each detail line it stands for.
func (m Model) messageSheetLines(p framePlan) []gline {
	msg := *m.shownMsg
	w := max0(p.w - 4)
	var out []gline
	for _, l := range wrapWords(msg.text, w) {
		out = append(out, gl().pad(2).join(m.messageLine(footerMsg{tone: msg.tone, text: l})).cut(p.w, m.g))
	}
	for _, d := range strings.Split(msg.detail, "\n") {
		if d == "" {
			continue
		}
		for i, l := range wrapWords(d, max0(w-2)) {
			lead := m.g.Dot + " "
			if i > 0 {
				lead = "  "
			}
			out = append(out, gl().pad(2).add(lead, tDim).add(l, tFg).cut(p.w, m.g))
		}
	}
	return out
}
