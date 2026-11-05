package matehost

import (
	"context"
	"errors"
	"fmt"
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
	// Quit lets ^C on an empty composer, with no turn running, end the
	// REPL. The host's own pane leaves it false: that pane stays open.
	Quit bool
}

// Model is the REPL's bubbletea model.
type Model struct {
	// conn reaches the host; calls runs what the REPL asks of it (caller).
	conn  Conn
	calls caller
	// id names this REPL to the host, so it tells its own draft changes
	// from another REPL's (Event.DraftBy).
	id  string
	cfg Config
	now func() time.Time

	// linked is whether the REPL is connected to its host; lost whether
	// it was and is no longer, or never managed to.
	linked, lost bool
	// draftSent counts the drafts sent on this connection, draftHeard
	// those the host has told back (or that failed): while some are on
	// their way, another REPL's draft does not replace the one typed.
	draftSent, draftHeard int
	// sessionShown is set once the session the REPL shows was in an
	// event; until then an event without it does not close the view.
	sessionShown bool
	// seq is the Event.Seq of the state shown.
	seq int64

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
	drafts map[int]string
	// labels say how the captain answered each news (answer), for the
	// line of an answer the host holds (answerLine); confirm is the answer
	// the host has since sent.
	labels    map[int]string
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

// NewModel shows history (oldest first) and then what the host does, on
// conn. RunREPL gives it the caller its calls run on.
func NewModel(conn Conn, cfg Config, history []Item) Model {
	return Model{conn: conn, id: newClientID(), cfg: cfg, now: time.Now, items: history, crews: -1}
}

// call runs fn on the host without Update waiting for it; what it returns
// comes back to Update.
func (m Model) call(fn func(ctx context.Context, c Conn) tea.Msg) {
	if m.conn == nil || m.calls == nil {
		return
	}
	conn := m.conn
	m.calls.Go(func(ctx context.Context) tea.Msg { return fn(ctx, conn) })
}

// noted is a call's error as a message for the captain; no error, none.
func noted(err error) tea.Msg {
	if err == nil {
		return nil
	}
	return replyMsg{op: callNote, err: err}
}

// failure is what the REPL tells the captain of a call that failed: the
// host's own words when it refused, else that it could not be reached.
func failure(err error) string {
	var refused *hostError
	if errors.As(err, &refused) {
		return err.Error()
	}
	return "không nối được với Mate; chưa làm được việc này"
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
		return m, tick()
	case crewsMsg:
		m.crews = int(msg)
		return m, m.readCrews(crewsEvery)
	case hostMsg:
		m.apply(Event(msg))
	case linkUpMsg:
		m.connected(msg)
	case linkDownMsg:
		m.linked, m.lost = false, true
		// The note would hide that the host is gone (footer).
		m.note = ""
	case replyMsg:
		m.reply(msg)
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
	// An event whose state was read before the one shown last (Event.Seq)
	// brings only what it alone carries: a finished turn, a note, a focus.
	stale := ev.Seq != 0 && ev.Seq < m.seq
	if !stale {
		before := m.state.Held
		m.seq = ev.Seq
		m.state = ev.State
		m.queue = ev.Queue
		m.news = ev.News
		m.sentAnswers(before)
		m.work = ev.Work
		m.sessions = ev.Sessions
		if m.mode == viewSession {
			if s, ok := m.findSession(m.openID); ok {
				m.sessionShown = true
				m.seeSession(s)
			} else if m.sessionShown {
				// Closed for idling, or by the host stopping.
				m.leaveSession()
			}
		}
	}
	if ev.Note != "" {
		m.note = ev.Note
	}
	if ev.DraftCleared {
		m.setChatDraft("")
	}
	if ev.DraftSet {
		m.heardDraft(ev, stale)
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
	if it == nil || (stale && !it.Done) {
		return
	}
	if !it.Done {
		m.running = it
		return
	}
	if !stale {
		m.running = nil
	}
	if m.hasItem(*it) {
		// Read with the history on connecting (linkUpMsg).
		return
	}
	m.items = append(m.items, *it)
	if m.scroll > 0 && it.InChat() && m.mode == viewChat {
		m.unseen++
	}
}

// hasItem reports whether it is one of the last items shown already.
func (m Model) hasItem(it Item) bool {
	for i := len(m.items) - 1; i >= max(0, len(m.items)-replHistory); i-- {
		if o := m.items[i]; o.At.Equal(it.At) && o.Source == it.Source && o.Text == it.Text {
			return true
		}
	}
	return false
}

// connected takes a connection to the host: its history in place of what
// was shown, and its first event as how things stand. The chat's draft
// typed while the host was away stays and is sent to it; a REPL whose chat
// composer is empty takes the draft the host keeps.
func (m *Model) connected(up linkUpMsg) {
	m.linked, m.lost = true, false
	m.draftSent, m.draftHeard = 0, 0
	m.items = up.history
	m.running = nil
	m.sessionShown = true
	m.seq = 0
	m.apply(up.first)
	m.scroll = min(m.scroll, m.maxScroll())
	if m.scroll == 0 {
		m.unseen = 0
	}
	if m.note == offlineSend {
		m.note = ""
	}
	if draft := m.chatDraft(); draft != "" {
		m.sendDraft(draft)
	} else if up.first.State.Draft != "" {
		m.setChatDraft(up.first.State.Draft)
	}
}

// The captain's draft the host keeps is the chat's composer: a topic's
// answer or a session's question is not the Mate's draft, and a REPL
// beside this one must not get it in its chat.

// chatDraft is what the chat's composer holds, shown or not.
func (m Model) chatDraft() string {
	if m.mode == viewChat {
		return string(m.input)
	}
	return m.drafts[0]
}

// setChatDraft puts text in the chat's composer, shown or not, without
// telling the host.
func (m *Model) setChatDraft(text string) {
	if m.mode == viewChat {
		if text != string(m.input) {
			m.input = []rune(text)
			m.cursor = len(m.input)
		}
		return
	}
	if m.drafts == nil {
		m.drafts = map[int]string{}
	}
	m.drafts[0] = text
}

// heardDraft is a draft change the host told of. One this REPL made is
// counted; another REPL's replaces the chat's composer, unless a draft
// of this one's is still on its way (the host takes that one last), or a
// later state was shown already.
func (m *Model) heardDraft(ev Event, stale bool) {
	if ev.DraftBy == m.id {
		m.draftHeard = min(m.draftHeard+1, m.draftSent)
		return
	}
	if stale || m.draftHeard < m.draftSent {
		return
	}
	m.setChatDraft(ev.State.Draft)
}

// pushDraft sends the chat's composer to the host as the captain's draft,
// when the chat is shown.
func (m *Model) pushDraft() {
	if m.mode == viewChat {
		m.sendDraft(string(m.input))
	}
}

func (m *Model) sendDraft(text string) {
	if !m.linked {
		// Sent when the host is back (connected).
		return
	}
	m.draftSent++
	id := m.id
	m.call(func(ctx context.Context, c Conn) tea.Msg {
		if err := c.SetDraft(ctx, text, id); err != nil {
			return replyMsg{op: callDraft, err: err}
		}
		return nil
	})
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
		case m.state.Busy:
			m.interrupt()
		case !empty:
			m.setInput(nil)
		case m.cfg.Quit:
			return m, tea.Quit
		default:
			m.note = "pane này luôn mở; dừng Mate từ console"
		}
		return m, nil
	case tea.KeyEsc:
		if m.state.Busy {
			m.interrupt()
		}
		return m, nil
	case tea.KeyF1:
		m.help = true
		return m, nil
	case tea.KeyCtrlR:
		m.retry()
		return m, nil
	case tea.KeyCtrlX:
		m.call(func(ctx context.Context, c Conn) tea.Msg {
			_, ok, err := c.TakeLast(ctx)
			return replyMsg{op: callTakeLast, ok: ok, err: err}
		})
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
	m.pushDraft()
	return m, nil
}

// interrupt stops the Mate's turn in flight. The REPL knows one runs from
// the host's last event; the host stops whichever runs when it gets this.
func (m *Model) interrupt() {
	m.note = "đang ngắt lượt này…"
	m.call(func(ctx context.Context, c Conn) tea.Msg {
		_, err := c.Interrupt(ctx)
		return noted(err)
	})
}

// send hands the composer to the host, which holds it for its undo, then
// runs it or queues it behind the turn in flight.
func (m *Model) send() {
	text := strings.TrimSpace(string(m.input))
	if text == "" {
		return
	}
	if !m.linked {
		m.note = offlineSend
		return
	}
	m.setInput(nil)
	if m.chatNewsRef(text) {
		return
	}
	m.scroll, m.unseen = 0, 0
	m.submit(text)
}

// submit hands text to the host; a message the host never got goes back
// to the composer.
func (m *Model) submit(text string) {
	m.call(func(ctx context.Context, c Conn) tea.Msg {
		held, err := c.Submit(ctx, text)
		if err != nil {
			return replyMsg{op: callSubmit, text: text, err: err}
		}
		return replyMsg{op: callHeld, held: held}
	})
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
	if !m.linked {
		m.note = offlineSend
		return
	}
	m.submit(last.Text)
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
	m.pushDraft()
}

// What the REPL says while it is not connected to its host.
const (
	offlineNote = "mất kết nối với Mate, đang nối lại…"
	offlineSend = "chưa nối được với Mate; tin vẫn nằm trong ô soạn"
)

// Calls whose answer the REPL acts on (replyMsg).
const (
	callNote = iota + 1
	callDraft
	callAnswer
	callSubmit
	callTakeLast
	callOpenSession
	callAskSession
	callInterruptSession
	callHeld
	callUndo
)

// replyMsg is the host's answer to a call the REPL acts on.
type replyMsg struct {
	op   int
	id   int
	text string
	ok   bool
	err  error
	// held is what the host put on hold (callHeld) or gave back
	// (callUndo).
	held Held
}

// reply acts on the host's answer to a call, as the REPL acted on the Host
// method's return when it called the host in its own process.
func (m *Model) reply(r replyMsg) {
	switch r.op {
	case callNote:
		m.note = failure(r.err)
	case callAnswer:
		m.note = fmt.Sprintf("Mate chưa nhận câu trả lời cho #%d: %s", r.id, failure(r.err))
		m.giveBackAnswer(r.id, r.text)
	case callDraft:
		// Never told back: no longer on its way.
		m.draftHeard = min(m.draftHeard+1, m.draftSent)
	case callHeld:
		m.heard(r.held)
	case callUndo:
		m.undone(r)
	case callSubmit:
		m.note = "Mate chưa nhận tin: " + failure(r.err)
		if m.chatDraft() == "" {
			m.setChatDraft(r.text)
			m.pushDraft()
		}
	case callTakeLast:
		switch {
		case r.err != nil:
			m.note = failure(r.err)
		case !r.ok:
			m.note = "không có tin nào đang xếp hàng"
		}
	case callOpenSession:
		m.sessionOpened(r)
	case callAskSession:
		m.sessionAsked(r)
	case callInterruptSession:
		if r.err != nil {
			m.note = failure(r.err)
		}
	}
}
