package matehost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// TestMain runs this test binary as a REPL client when a test starts it so
// (replHelper), to be killed like the REPL process `mate mate host` runs.
func TestMain(m *testing.M) {
	if sock := os.Getenv("MATE_TEST_REPL_HELPER"); sock != "" {
		os.Exit(replHelper(sock, os.Getenv("MATE_TEST_REPL_TYPE")))
	}
	os.Exit(m.Run())
}

// replHelper is a REPL client of the host at sock, keys read from a pipe:
// it types text, waits until the host holds it as the draft (the REPL is
// connected then), and sends it.
func replHelper(sock, text string) int {
	c := Client{Path: sock}
	in, keys := io.Pipe()
	if text != "" {
		go func() {
			_, _ = keys.Write([]byte(text))
			for {
				if st, err := c.State(context.Background()); err == nil && st.Draft == text {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			_, _ = keys.Write([]byte("\r"))
		}()
	}
	if err := RunREPL(context.Background(), REPLConn(c), Config{Project: "p"}, tea.WithInput(in), tea.WithOutput(io.Discard), tea.WithoutSignalHandler()); err != nil {
		return 1
	}
	return 0
}

// clientLoop is a REPL connected to a host's socket, its Update run by the
// test: link delivers what the host says to msgs, and until applies it.
// Its calls go over the socket, made at once in the test's goroutine.
type clientLoop struct {
	t    *testing.T
	m    Model
	msgs chan tea.Msg
}

func startClient(t *testing.T, c Client) *clientLoop {
	t.Helper()
	conn := REPLConn(c)
	m := NewModel(conn, Config{Project: "p"}, nil)
	m.calls = &syncCalls{}
	m.width, m.height = 100, 30
	l := &clientLoop{t: t, m: m, msgs: make(chan tea.Msg, 4096)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		link(ctx, conn, func(msg tea.Msg) {
			select {
			case l.msgs <- msg:
			case <-ctx.Done():
			}
		})
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return l
}

func (l *clientLoop) until(what string, ok func(m Model) bool) {
	l.t.Helper()
	deadline := time.After(10 * time.Second)
	for !ok(l.m) {
		select {
		case msg := <-l.msgs:
			next, _ := l.m.Update(msg)
			l.m = next.(Model)
			drain(&l.m)
		case <-deadline:
			l.t.Fatalf("the REPL never got to: %s (screen:\n%s)", what, ansi.Strip(l.m.View()))
		}
	}
}

func (l *clientLoop) press(keys ...any) {
	l.t.Helper()
	press(l.t, &l.m, keys...)
}

func hasText(items []Item, text string) bool {
	return slices.ContainsFunc(items, func(it Item) bool { return it.Text == text })
}

func waitHost(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("the host never got to: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Everything the captain does in the REPL reaches the host over its
// socket: a message, ^C on a turn, a session opened, asked and closed, an
// answer to a news, the Work page's OK.
func TestREPLClientDrivesTheHostOverTheSocket(t *testing.T) {
	f := newSessionFixture(t)
	work := WorkPath(f.dir)
	if _, err := AddWork(work, WorkAssigned, "Epic 3 sổ cái", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := DoneWork(work, 1, "PR #12 đã merge", time.Now()); err != nil {
		t.Fatal(err)
	}
	f.host.RefreshWork()
	c, _, _ := serve(t, f.host)
	l := startClient(t, c)
	clock := time.Now()
	l.m.now = func() time.Time { return clock }
	l.until("connected", func(m Model) bool { return m.linked && len(m.news) == 1 && len(m.work) == 1 })

	l.press("xin chào", tea.KeyEnter)
	if it := f.done(t, 1)[0]; it.Text != "xin chào" || it.Reply != "re: xin chào" {
		t.Fatalf("the message's turn: %+v", it)
	}
	l.until("the reply shown", func(m Model) bool { return hasText(m.items, "xin chào") && m.running == nil })

	l.press("slow, ngắt giúp anh", tea.KeyEnter)
	l.until("the slow turn running", func(m Model) bool {
		return m.state.Busy && m.running != nil && m.running.Text == "slow, ngắt giúp anh"
	})
	l.press(tea.KeyCtrlC)
	if it := f.done(t, 2)[1]; !it.Interrupted {
		t.Fatalf("^C did not interrupt the turn: %+v", it)
	}
	l.until("the interrupted turn shown", func(m Model) bool { return hasText(m.items, "slow, ngắt giúp anh") && !m.state.Busy })

	// The topic page: frame #1, its session.
	l.press(tea.KeyCtrlT, tea.KeyEnter, "f")
	l.until("the session shown", func(m Model) bool { return m.mode == viewSession && m.openID == 1 && len(m.sessions) == 1 })
	l.press("vì sao?", tea.KeyEnter)
	if items := f.sessionItems(t, 1, 1); items[0].Text != "vì sao?" {
		t.Fatalf("the session was asked %q", items[0].Text)
	}
	l.until("the session's answer shown", func(m Model) bool {
		s, ok := m.findSession(1)
		return ok && len(s.Items) == 1 && len(m.input) == 0
	})
	l.press(tea.KeyCtrlX)
	l.until("the session closed", func(m Model) bool { return m.mode == viewTopics && len(m.sessions) == 0 })
	if s := f.host.Sessions(); len(s) != 0 {
		t.Fatalf("the host still has sessions %+v", s)
	}

	// An answer goes after its undo delay.
	l.press(tea.KeyTab, "nhận cả XOA", tea.KeyEnter)
	if l.m.pending == nil {
		t.Fatalf("no answer pending (note %q)", l.m.note)
	}
	clock = clock.Add(undoDelay + time.Second)
	next, _ := l.m.Update(tickMsg(clock))
	l.m = next.(Model)
	drain(&l.m)
	if it := f.done(t, 3)[2]; it.News != 1 || it.Text != "nhận cả XOA" {
		t.Fatalf("the answer's turn: %+v", it)
	}

	// The Work page: Space OKs the done row.
	l.press(tea.KeyEsc, tea.KeyCtrlL)
	if l.m.mode != viewWork {
		t.Fatalf("^L left mode %v", l.m.mode)
	}
	l.press(tea.KeySpace)
	list, _ := LoadWork(work)
	if w, _ := findWork(list, 1); w.Status != WorkOkayed {
		t.Fatalf("Space left #1 %q", w.Status)
	}
	l.until("the row okayed", func(m Model) bool { return len(m.work) == 1 && m.work[0].Status == WorkOkayed })
}

// A REPL killed in the middle of a turn takes nothing with it: the turn
// ends and is kept, the host lives on, and the REPL is started again.
func TestREPLKilledMidTurnLeavesTheTurnRunning(t *testing.T) {
	f := newFixture(t)
	dir, err := os.MkdirTemp("/tmp", "mh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, SocketName)
	const text = "slow, giết REPL giữa chừng"
	starts := make(chan *os.Process, 4)
	var mu sync.Mutex
	n := 0
	repl := func(ctx context.Context) error {
		mu.Lock()
		first := n == 0
		n++
		mu.Unlock()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^$")
		cmd.Env = append(os.Environ(), "MATE_TEST_REPL_HELPER="+path)
		if first {
			cmd.Env = append(cmd.Env, "MATE_TEST_REPL_TYPE="+text)
		}
		if err := cmd.Start(); err != nil {
			return err
		}
		starts <- cmd.Process
		return cmd.Wait()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lived := make(chan error, 1)
	var log strings.Builder
	var logMu sync.Mutex
	go func() {
		lived <- f.host.Live(ctx, LiveOptions{Socket: path, NewsPoll: 50 * time.Millisecond, REPL: repl, Restart: 50 * time.Millisecond,
			Log: writerFunc(func(p []byte) (int, error) { logMu.Lock(); defer logMu.Unlock(); return log.Write(p) })})
	}()

	first := <-starts
	waitHost(t, "the REPL's message running", func() bool { return f.host.State().Busy })
	if err := first.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-starts:
	case <-time.After(10 * time.Second):
		t.Fatal("the REPL was not started again")
	}
	it := f.done(t, 1)[0]
	if it.Text != text || it.Reply != "re: "+text || it.Interrupted || it.Error != "" {
		t.Fatalf("the turn the REPL left: %+v", it)
	}
	if kept := History(filepath.Join(f.dir, HistoryName), 0); len(kept) != 1 || kept[0].Reply != it.Reply {
		t.Fatalf("host.jsonl = %+v", kept)
	}
	if st := f.host.State(); st.Stopping {
		t.Fatal("the host stopped with its REPL")
	}
	logMu.Lock()
	said := log.String()
	logMu.Unlock()
	if !strings.Contains(said, "the REPL ended") {
		t.Errorf("the host did not say its REPL ended: %q", said)
	}
	cancel()
	select {
	case err := <-lived:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Live did not return")
	}
	if !f.host.State().Stopping {
		t.Fatal("the host was not stopped")
	}
}

type writerFunc func([]byte) (int, error)

func (w writerFunc) Write(p []byte) (int, error) { return w(p) }

// The host's stop op ends Live, and its REPL with it.
func TestLiveEndsOnTheStopOp(t *testing.T) {
	f := newFixture(t)
	dir, err := os.MkdirTemp("/tmp", "mh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, SocketName)
	replEnded := make(chan struct{})
	repl := func(ctx context.Context) error {
		<-ctx.Done()
		close(replEnded)
		return nil
	}
	lived := make(chan error, 1)
	go func() {
		lived <- f.host.Live(context.Background(), LiveOptions{Socket: path, NewsPoll: 50 * time.Millisecond, REPL: repl})
	}()
	c := Client{Path: path}
	waitHost(t, "the socket up", func() bool { return c.Ping(context.Background()) == nil })
	if _, err := c.Stop(context.Background(), 0, false); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-lived:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Live did not return on stop")
	}
	select {
	case <-replEnded:
	default:
		t.Fatal("Live returned with its REPL still running")
	}
}

// A host that restarts: the REPL says it lost the host, keeps the draft
// typed, connects again on its own and shows the new host's chat and news.
func TestREPLReconnectsAfterTheHostRestarts(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "mh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, SocketName)
	serveAt := func(h *Host) (context.CancelFunc, <-chan error) {
		ctx, cancel := context.WithCancel(context.Background())
		served := make(chan error, 1)
		go func() {
			served <- h.Serve(ctx, path, nil)
			close(served)
		}()
		t.Cleanup(func() {
			cancel()
			<-served
		})
		return cancel, served
	}

	a := newFixture(t)
	stopA, servedA := serveAt(a.host)
	l := startClient(t, Client{Path: path})
	a.host.Submit("trước khi restart")
	a.done(t, 1)
	l.until("A's chat", func(m Model) bool { return m.linked && hasText(m.items, "trước khi restart") && m.running == nil })
	l.press("nháp đang gõ")
	waitHost(t, "A holding the draft", func() bool { return a.host.State().Draft == "nháp đang gõ" })

	stopA()
	<-servedA
	l.until("the host lost", func(m Model) bool { return m.lost })
	if screen := ansi.Strip(l.m.View()); !strings.Contains(screen, offlineNote) {
		t.Errorf("the REPL does not say it lost the host:\n%s", screen)
	}
	l.press("!")
	l.press(tea.KeyEnter)
	if l.m.note != offlineSend || string(l.m.input) != "nháp đang gõ!" {
		t.Fatalf("Enter with no host: note %q, input %q", l.m.note, string(l.m.input))
	}

	b := newFixture(t)
	appendItemFile(filepath.Join(b.dir, HistoryName), Item{At: time.Now(), Source: SourceCaptain, Text: "trên host mới", Reply: "ok", Done: true})
	if _, err := OpenNews(filepath.Join(b.dir, NewsName), News{Title: "Câu hỏi mới", Question: "Làm gì tiếp?"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	b.host.RefreshNews()
	serveAt(b.host)
	l.until("B's state", func(m Model) bool { return m.linked && len(m.news) == 1 && hasText(m.items, "trên host mới") })
	if hasText(l.m.items, "trước khi restart") {
		t.Errorf("the REPL still shows A's chat: %+v", l.m.items)
	}
	if got := string(l.m.input); got != "nháp đang gõ!" {
		t.Fatalf("the draft became %q", got)
	}
	waitHost(t, "B holding the draft", func() bool { return b.host.State().Draft == "nháp đang gõ!" })
	if screen := ansi.Strip(l.m.View()); strings.Contains(screen, offlineNote) {
		t.Errorf("the REPL still says it lost the host:\n%s", screen)
	}
}

// Two REPLs of one Mate, the pane's and one in another terminal, show the
// same chat and the same draft.
func TestTwoREPLsShareTheChatAndTheDraft(t *testing.T) {
	f := newFixture(t)
	c, _, _ := serve(t, f.host)
	a, b := startClient(t, c), startClient(t, c)
	a.until("a connected", func(m Model) bool { return m.linked })
	b.until("b connected", func(m Model) bool { return m.linked })

	a.press("chung")
	b.until("b showing a's draft", func(m Model) bool { return string(m.input) == "chung" })
	b.press(" một nháp")
	a.until("a showing b's draft", func(m Model) bool { return string(m.input) == "chung một nháp" })
	a.press(tea.KeyEnter)
	f.done(t, 1)
	for name, l := range map[string]*clientLoop{"a": a, "b": b} {
		l.until(name+" showing the turn, its composer empty", func(m Model) bool {
			return hasText(m.items, "chung một nháp") && m.running == nil && len(m.input) == 0
		})
	}

	// A REPL opened later starts with the draft the host keeps.
	b.press("để sau")
	waitHost(t, "the host holding b's draft", func() bool { return f.host.State().Draft == "để sau" })
	late := startClient(t, c)
	late.until("the late REPL showing the draft", func(m Model) bool { return m.linked && string(m.input) == "để sau" })
}

// While a draft of its own is on its way, a REPL keeps what it typed over
// another REPL's; the host takes its own last.
func TestREPLKeepsItsDraftWhileItsOwnIsOnItsWay(t *testing.T) {
	m, _ := withFake(NewModel(nil, Config{Project: "p"}, nil))
	m.calls = &heldCalls{}
	press(t, &m, "của tôi")
	m.applyEvent(Event{DraftSet: true, DraftBy: "khác", State: State{Draft: "của người khác"}})
	if got := string(m.input); got != "của tôi" {
		t.Fatalf("another REPL's draft replaced the one on its way: %q", got)
	}
	for range m.draftSent {
		m.applyEvent(Event{DraftSet: true, DraftBy: m.id, State: State{Draft: "của tôi"}})
	}
	m.applyEvent(Event{DraftSet: true, DraftBy: "khác", State: State{Draft: "của người khác"}})
	if got := string(m.input); got != "của người khác" {
		t.Fatalf("another REPL's later draft was not taken: %q", got)
	}
}

// heldCalls makes no call: what is sent is still on its way.
type heldCalls struct{}

func (heldCalls) Go(func(context.Context) tea.Msg) {}

// A message the host never got goes back to the composer, and the REPL
// says why.
func TestREPLGivesBackAMessageTheHostRefused(t *testing.T) {
	m, f := withFake(NewModel(nil, Config{Project: "p"}, nil))
	m.conn = refusingConn{f}
	press(t, &m, "gửi đi", tea.KeyEnter)
	if string(m.input) != "gửi đi" || m.running != nil || !strings.Contains(m.note, "the Mate is stopping") {
		t.Fatalf("after a refused message: input %q, running %+v, note %q", string(m.input), m.running, m.note)
	}
}

type refusingConn struct{ *fakeConn }

// The draft the host keeps is the chat's: what the captain writes in a
// topic stays out of it, and another REPL's draft heard while the chat is
// not shown is there when it is.
func TestREPLSharesOnlyTheChatsDraft(t *testing.T) {
	m, f := withFake(newsModel(100, 30))
	press(t, &m, "chat dở", tea.KeyCtrlT, tea.KeyEnter, tea.KeyTab, "trả lời #3")
	if draft, _ := f.state(); draft != "chat dở" {
		t.Fatalf("typing in a topic made the draft %q", draft)
	}
	// The fake tells no draft back; the host has told back all of them.
	m.draftHeard = m.draftSent
	m.applyEvent(Event{News: m.news, DraftSet: true, DraftBy: "khác", State: State{Draft: "từ REPL kia"}})
	if string(m.input) != "trả lời #3" {
		t.Fatalf("another REPL's draft went into the topic's composer: %q", string(m.input))
	}
	press(t, &m, tea.KeyEsc)
	if m.mode != viewChat || string(m.input) != "từ REPL kia" {
		t.Fatalf("back in the chat: mode %v, composer %q", m.mode, string(m.input))
	}
}

// An event whose state was read before the one shown leaves the state
// shown; a turn it finished still shows.
func TestREPLKeepsTheNewestState(t *testing.T) {
	m, _ := withFake(NewModel(nil, Config{Project: "p"}, nil))
	at := time.Now()
	m.applyEvent(Event{Seq: 5, Item: &Item{At: at, Source: SourceCaptain, Text: "xong", Reply: "ok", Done: true}})
	m.applyEvent(Event{Seq: 4, State: State{Busy: true}, Item: &Item{At: at, Source: SourceCaptain, Text: "xong"}})
	if m.state.Busy || m.running != nil {
		t.Fatalf("an older event made the Mate busy again: state %+v, running %+v", m.state, m.running)
	}
	m.applyEvent(Event{Seq: 3, Item: &Item{At: at.Add(-time.Minute), Source: SourceCaptain, Text: "trước", Done: true}})
	if !hasText(m.items, "trước") || !hasText(m.items, "xong") {
		t.Fatalf("items = %+v", m.items)
	}
}

// An answer whose undo delay ends with the host away is not lost: it goes
// back to its frame, and so does one the host could not be reached for.
func TestREPLGivesBackAnAnswerTheHostNeverGot(t *testing.T) {
	m, _ := withFake(newsModel(100, 30))
	clock := m.now()
	m.now = func() time.Time { return clock }
	press(t, &m, tea.KeyCtrlT, tea.KeyEnter, tea.KeyTab, "dùng a", tea.KeyEnter)
	if m.pending == nil {
		t.Fatal("no answer pending")
	}
	m.linked = false
	clock = clock.Add(undoDelay + time.Second)
	m.tickNews()
	if m.pending != nil || m.drafts[3] != "dùng a" || !strings.Contains(m.note, "#3") {
		t.Fatalf("offline flush: pending %+v, draft %q, note %q", m.pending, m.drafts[3], m.note)
	}

	m.linked = true
	m.drafts[3] = ""
	m.conn = unreachableConn{m.conn.(*fakeConn)}
	m.pending = &pendingAnswer{id: 3, text: "dùng b", at: clock}
	clock = clock.Add(undoDelay + time.Second)
	m.tickNews()
	drain(&m)
	if m.drafts[3] != "dùng b" || !strings.Contains(m.note, "không nối được với Mate") {
		t.Fatalf("failed answer: draft %q, note %q", m.drafts[3], m.note)
	}
}

type unreachableConn struct{ *fakeConn }

func (unreachableConn) AnswerNews(context.Context, int, string) error { return unreachable }

func (refusingConn) Submit(context.Context, string) error { return refused(ErrStopping) }

// A REPL opened before its host waits for it, saying so, and connects once
// it is there.
func TestREPLWaitsForAHostNotYetUp(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "mh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, SocketName)
	l := startClient(t, Client{Path: path})
	l.until("the host missing", func(m Model) bool { return m.lost })
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- f.host.Serve(ctx, path, nil) }()
	defer func() {
		cancel()
		if err := <-served; err != nil && !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
	}()
	l.until("connected", func(m Model) bool { return m.linked && !m.lost })
}

// BenchmarkDraftEvent is what one keystroke costs the host with a REPL
// subscribed (CON-223): SetDraftBy emits, and publish encodes the whole
// snapshot once for its subscribers, here with 30 news and 30 work rows.
func BenchmarkDraftEvent(b *testing.B) {
	dir := b.TempDir()
	news := filepath.Join(dir, NewsName)
	for i := range 30 {
		if _, err := OpenNews(news, News{Title: fmt.Sprintf("Topic %d", i), Question: strings.Repeat("Câu hỏi khá dài cho captain. ", 6),
			Options: []NewsOption{{Key: "a", Text: "Một"}, {Key: "b", Text: "Hai"}}}, time.Now()); err != nil {
			b.Fatal(err)
		}
		if _, err := AddWork(WorkPath(dir), WorkAssigned, fmt.Sprintf("Việc %d", i), "ghi chú", time.Now()); err != nil {
			b.Fatal(err)
		}
	}
	h := New(&Runner{}, Options{News: news})
	h.RefreshNews()
	h.RefreshWork()
	s := newLineStream(discardConn{}, 1<<20, time.Second)
	h.subMu.Lock()
	h.subs[s] = struct{}{}
	h.subMu.Unlock()
	go s.run()
	defer s.abort()
	line, _ := eventLine(h.eventLocked(nil))
	b.ResetTimer()
	for i := range b.N {
		h.SetDraftBy(strconv.Itoa(i), "a")
	}
	b.ReportMetric(float64(len(line)), "bytes/event")
}

// discardConn is a net.Conn that takes every write.
type discardConn struct{ net.Conn }

func (discardConn) Write(p []byte) (int, error)      { return len(p), nil }
func (discardConn) Close() error                     { return nil }
func (discardConn) SetWriteDeadline(time.Time) error { return nil }
