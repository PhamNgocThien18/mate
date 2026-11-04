package matehost

import (
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// The REPL is what the captain sees in the Mate's pane: a chat with the
// Mate, laid out as the design of 2026-10-15 (CON-78) has it: a header, the
// conversation, the captain's queued messages, a composer whose border
// says what the Mate is doing, and the keys that work now. The
// conversation is the captain's messages and the Mate's replies, nothing
// else: app lines, their turns and the tools any turn called stay out
// (CON-151).

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// crewsEvery is how often the header's crew count is read again.
const crewsEvery = 15 * time.Second

type hostMsg Event

type tickMsg time.Time

type crewsMsg int

func tick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// Config is what the REPL shows besides the host's own items.
type Config struct {
	Project string
	// Model is the Mate's model, for the header; empty hides it.
	Model string
	// Crews counts the project's open crews, for the header; nil hides it.
	Crews func() (int, error)
}

// Model is the REPL's bubbletea model.
type Model struct {
	host *Host
	cfg  Config
	now  func() time.Time

	items   []Item
	running *Item
	queue   []string
	state   State

	input  []rune
	cursor int

	width, height int
	// scroll is how many lines the conversation is scrolled up from its
	// end; unseen counts turns that ended while it was.
	scroll int
	unseen int
	frame  int
	crews  int
	help   bool
	note   string

	news []News
	mode viewMode
	// selID is the news whose frame the cursor is on in the Topic page,
	// openID the one open (0 for none); typing is the captain writing in
	// it.
	selID  int
	openID int
	typing bool
	// roundsOpen unfolds the settled rounds of the open frame (^O).
	roundsOpen bool
	// drafts keep the composer of each view: 0 the chat's, n frame n's.
	drafts    map[int]string
	pending   *pendingAnswer
	confirm   string
	confirmAt time.Time

	// sessions are the open topic sessions; sessSeen counts the items of
	// each the captain has seen. editProposal is the captain editing a
	// session's proposal in its composer; ^S sends the edit.
	sessions     []SessionInfo
	sessSeen     map[int]int
	editProposal bool

	// work is the Mate's work board (repl_work.go); workSel the row under
	// the cursor on the Work page, workOpen the rows whose history shows,
	// workAll whether every okayed row is listed.
	work     []Work
	workSel  int
	workOpen map[int]bool
	workAll  bool
}

// NewModel shows history (oldest first) and then what the host does.
func NewModel(host *Host, cfg Config, history []Item) Model {
	return Model{host: host, cfg: cfg, now: time.Now, items: history, crews: -1}
}

// Program runs the REPL; the host's events reach it through Send, behind a
// queue so that a host call made from Update never waits on Update itself
// (CON-121).
func Program(m Model) *tea.Program {
	// Mouse reporting makes the wheel scroll the view; without it the
	// terminal turns the wheel into ↑/↓ keys.
	opts := []tea.ProgramOption{tea.WithAltScreen(), tea.WithMouseCellMotion()}
	if in := newSeqInput(os.Stdin); in != nil {
		opts = append(opts, tea.WithInput(in))
	}
	p := tea.NewProgram(m, opts...)
	f := newForwarder(func(ev Event) { p.Send(hostMsg(ev)) })
	m.host.Subscribe(f.Put)
	return p
}

func (m Model) Init() tea.Cmd { return tea.Batch(tick(), m.readCrews(0)) }

func (m Model) readCrews(after time.Duration) tea.Cmd {
	if m.cfg.Crews == nil {
		return nil
	}
	return func() tea.Msg {
		time.Sleep(after)
		n, err := m.cfg.Crews()
		if err != nil {
			n = -1
		}
		return crewsMsg(n)
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tickMsg:
		m.frame++
		m.tickNews()
		return m, tick()
	case crewsMsg:
		m.crews = int(msg)
		return m, m.readCrews(crewsEvery)
	case hostMsg:
		m.apply(Event(msg))
	case error:
		m.note = msg.Error()
	case tea.KeyMsg:
		return m.key(msg)
	case tea.MouseMsg:
		m.mouse(msg)
	}
	return m, nil
}

// wheelLines is how far one notch of the mouse wheel scrolls.
const wheelLines = 3

// mouse scrolls the view under the wheel; a click opens a topic: its
// frame on the Topic page, the tray's line in the chat.
func (m *Model) mouse(msg tea.MouseMsg) {
	if msg.Action != tea.MouseActionPress || m.help {
		return
	}
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.wheel(1)
	case tea.MouseButtonWheelDown:
		m.wheel(-1)
	case tea.MouseButtonLeft:
		switch m.mode {
		case viewTopics:
			m.clickTopic(msg.Y)
		case viewWork:
			m.clickWork(msg.Y)
		default:
			m.clickChat(msg.Y)
		}
	}
}

// wheel moves what is shown by one notch: up is n > 0. On the Topic and
// Work pages it moves the cursor; else it scrolls the chat.
func (m *Model) wheel(n int) {
	switch m.mode {
	case viewTopics:
		m.moveCursor(-n)
		return
	case viewWork:
		m.moveWork(-n)
		return
	}
	m.scrollBy(n * wheelLines)
}

// apply takes a host event. A chat read scrolled up keeps its top line
// where it is: what the event adds below would otherwise push it up.
func (m *Model) apply(ev Event) {
	if m.scroll == 0 || m.help || m.mode != viewChat {
		m.applyEvent(ev)
		return
	}
	w, _ := m.size()
	top := len(m.body(w)) - min(m.scroll, m.maxScroll()) - m.viewRows()
	m.applyEvent(ev)
	if m.mode == viewChat {
		m.scroll = min(m.maxScroll(), max(0, len(m.body(w))-m.viewRows()-top))
	}
}

func (m *Model) applyEvent(ev Event) {
	m.state = ev.State
	m.queue = ev.Queue
	m.news = ev.News
	m.work = ev.Work
	m.sessions = ev.Sessions
	if m.mode == viewSession {
		if s, ok := m.findSession(m.openID); ok {
			m.seeSession(s)
		} else {
			// Closed for idling, or by the host stopping.
			m.leaveSession()
		}
	}
	if ev.Note != "" {
		m.note = ev.Note
	}
	if ev.DraftCleared {
		m.setInput(nil)
	}
	if ev.Focus > 0 {
		m.help = false
		m.openTopic(ev.Focus)
	}
	if ev.FocusWork {
		m.help = false
		m.showWork()
	}
	it := ev.Item
	if it == nil {
		return
	}
	if !it.Done {
		m.running = it
		return
	}
	m.running = nil
	m.items = append(m.items, *it)
	if m.scroll > 0 && it.InChat() && m.mode == viewChat {
		m.unseen++
	}
}

// InChat reports whether a finished item takes room in the chat: a
// captain message outside any news, with the Mate's reply to it. The
// dashboard's chats show the same items.
func (it Item) InChat() bool {
	return it.News == 0 && it.Source == SourceCaptain
}

func (m Model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.note = ""
	if m.help {
		switch k.Type {
		case tea.KeyEsc, tea.KeyF1, tea.KeyCtrlC:
			m.help = false
			return m, nil
		case tea.KeyRunes:
			if string(k.Runes) == "?" {
				m.help = false
				return m, nil
			}
		}
	}
	next, handled := m.newsKey(k)
	if handled {
		return next, nil
	}
	m = next
	empty := len(m.input) == 0
	switch k.Type {
	case tea.KeyEnter:
		if k.Alt {
			m.insert('\n')
			break
		}
		m.send()
		return m, nil
	case tea.KeyCtrlJ:
		m.insert('\n')
	case tea.KeyCtrlC:
		switch {
		case m.host.Interrupt():
			m.note = "đang ngắt lượt này…"
		case !empty:
			m.setInput(nil)
		default:
			m.note = "pane này luôn mở; dừng Mate từ console"
		}
		return m, nil
	case tea.KeyEsc:
		if m.host.Interrupt() {
			m.note = "đang ngắt lượt này…"
		}
		return m, nil
	case tea.KeyF1:
		m.help = true
		return m, nil
	case tea.KeyCtrlR:
		m.retry()
		return m, nil
	case tea.KeyCtrlX:
		if _, ok := m.host.TakeLast(); !ok {
			m.note = "không có tin nào đang xếp hàng"
		}
		return m, nil
	case tea.KeyUp:
		if empty {
			m.scrollBy(1)
			return m, nil
		}
		m.lineUp()
		return m, nil
	case tea.KeyDown:
		if empty {
			m.scrollBy(-1)
			return m, nil
		}
		m.lineDown()
		return m, nil
	case tea.KeyPgUp:
		m.scrollBy(max(1, m.viewRows()-2))
		return m, nil
	case tea.KeyPgDown:
		m.scrollBy(-max(1, m.viewRows()-2))
		return m, nil
	case tea.KeyShiftUp:
		m.scrollBy(1)
		return m, nil
	case tea.KeyShiftDown:
		m.scrollBy(-1)
		return m, nil
	case tea.KeyHome:
		if empty {
			m.scrollBy(1 << 30)
			return m, nil
		}
		m.cursor = m.lineStart()
		return m, nil
	case tea.KeyEnd:
		if empty {
			m.scroll, m.unseen = 0, 0
			return m, nil
		}
		m.cursor = m.lineEnd()
		return m, nil
	case tea.KeyRunes:
		if empty && string(k.Runes) == "?" {
			m.help = true
			return m, nil
		}
		if k.Alt {
			return m, nil
		}
		for _, r := range k.Runes {
			if r == '\r' {
				r = '\n'
			}
			m.insert(r)
		}
	default:
		if !m.edit(k) {
			return m, nil
		}
	}
	m.host.SetDraft(string(m.input))
	return m, nil
}

// send hands the composer to the host: a turn now, or the queue.
func (m *Model) send() {
	text := strings.TrimSpace(string(m.input))
	if text == "" {
		return
	}
	m.setInput(nil)
	if m.chatNewsRef(text) {
		return
	}
	m.scroll, m.unseen = 0, 0
	if !m.state.Busy && len(m.queue) == 0 {
		// Shown at once; the host's start event replaces it.
		m.running = &Item{At: m.now(), Source: SourceCaptain, Text: text}
	}
	go m.host.Submit(text)
}

// retry sends again a captain message whose turn failed.
func (m *Model) retry() {
	if len(m.items) == 0 || m.running != nil {
		return
	}
	last := m.items[len(m.items)-1]
	if last.Source != SourceCaptain || last.Error == "" {
		m.note = "không có lượt lỗi nào để thử lại"
		return
	}
	m.running = &Item{At: m.now(), Source: SourceCaptain, Text: last.Text}
	go m.host.Submit(last.Text)
}

// scrollBy scrolls the chat n lines up (down if n < 0), no further up
// than its top: lines past it would have to be scrolled back first.
func (m *Model) scrollBy(n int) {
	m.scroll = min(m.maxScroll(), max(0, m.scroll+n))
	if m.scroll == 0 {
		m.unseen = 0
	}
}

func (m *Model) setInput(r []rune) {
	m.input, m.cursor = r, len(r)
	m.host.SetDraft(string(r))
}
