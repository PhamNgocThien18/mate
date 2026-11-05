package matehost

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
)

// fakeClaude answers like `claude -p --output-format stream-json`: it echoes
// its argv and stdin into the result, sleeps while the prompt says "slow",
// answers "—" to a prompt that says "quiet", and fails on "crash". A prompt
// that says "chatty" writes text before its tool and two messages after
// it, the last being the result; one that also says "stream" streams that
// text (--include-partial-messages) before each whole message. One that
// says "exitlate" fails after its result.
const fakeClaude = `#!/bin/sh
prompt=$(cat)
echo "$*" >> "$(dirname "$0")/argv.log"
echo "$prompt" >> "$(dirname "$0")/prompts.log"
case "$prompt" in *crash*) echo "boom" >&2; exit 3;; esac
say() {
	case "$prompt" in *stream*)
		printf '{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"%s"}}}\n' "$1";;
	esac
	printf '{"type":"assistant","message":{"content":[{"type":"text","text":"%s"}]}}\n' "$1"
}
echo '{"type":"system","subtype":"init","session_id":"s1"}'
case "$prompt" in *chatty*) say "Epic 1 has 3 tasks left.";; esac
echo '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash"}]}}'
echo '{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1"}]}}'
case "$prompt" in *slow*) sleep 2;; esac
case "$prompt" in *quiet*) reply='—';; *) reply="re: $(printf '%s' "$prompt" | tail -n 1 | tr -d '"\\')";; esac
case "$prompt" in *chatty*) say "Noted in the backlog."; say "$reply";; esac
sid=s1
case "$*" in *--fork-session*|*"--resume f1"*) sid=f1;; esac
printf '{"type":"result","subtype":"success","result":"%s","session_id":"%s","is_error":false}\n' "$reply" "$sid"
case "$prompt" in *exitlate*) echo "late" >&2; exit 3;; esac
`

// streamTurns is a HeadlessTurns over the fake's stream-json lines.
type streamTurns struct{}

func (streamTurns) TurnArgs(launch []string, id string, opened bool) []string {
	var out []string
	for i := 0; i < len(launch); i++ {
		if launch[i] == "--session-id" || launch[i] == "--resume" {
			i++
			continue
		}
		out = append(out, launch[i])
	}
	flag := "--session-id"
	if opened {
		flag = "--resume"
	}
	return append(out, flag, id, "-p", "--output-format", "stream-json", "--verbose")
}

func (streamTurns) LaunchSessionID([]string) string   { return "" }
func (streamTurns) SessionOpened(string, string) bool { return false }

func (streamTurns) ParseTurnEvent(line []byte) (harness.TurnEvent, bool) {
	var l struct {
		Type      string `json:"type"`
		Result    string `json:"result"`
		IsError   bool   `json:"is_error"`
		SessionID string `json:"session_id"`
		Event     struct {
			Delta struct{ Text string } `json:"delta"`
		} `json:"event"`
		Message struct {
			Content []struct {
				Type, Name, ID, Text string
				ToolUseID            string `json:"tool_use_id"`
			} `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &l) != nil || l.Type == "" {
		return harness.TurnEvent{}, false
	}
	ev := harness.TurnEvent{Delta: l.Event.Delta.Text}
	for _, c := range l.Message.Content {
		switch c.Type {
		case "text":
			ev.Said = c.Text
		case "tool_use":
			ev.Calls = append(ev.Calls, harness.ToolCall{ID: c.ID, Name: c.Name})
		case "tool_result":
			ev.Results = append(ev.Results, harness.ToolResult{ID: c.ToolUseID})
		}
	}
	if l.Type == "result" {
		ev.Final, ev.Text, ev.IsError, ev.SessionID = true, l.Result, l.IsError, l.SessionID
	}
	return ev, true
}

func fileLock(path string) LockFunc {
	return func() (func(), bool, error) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, false, err
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			f.Close()
			if errors.Is(err, syscall.EWOULDBLOCK) {
				return nil, false, nil
			}
			return nil, false, err
		}
		return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, true, nil
	}
}

type fixture struct {
	dir    string
	host   *Host
	opened map[string]bool
	// timers runs out what the host holds for its undo (hold.go).
	timers *fakeTimers

	mu     sync.Mutex
	events []Event
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	prog := filepath.Join(dir, "claude")
	if err := os.WriteFile(prog, []byte(fakeClaude), 0o755); err != nil {
		t.Fatal(err)
	}
	f := &fixture{dir: dir, opened: map[string]bool{}, timers: &fakeTimers{}}
	runner := &Runner{
		Launch: Launch{Project: "p", Harness: "fake", Program: prog, Args: []string{"--model", "opus", "--session-id", "s1"}, Dir: dir, SessionID: "s1"},
		Turns:  streamTurns{},
		Lock:   fileLock(filepath.Join(dir, ".turn.lock")),
		Opened: func(_, id string) bool {
			f.mu.Lock()
			defer f.mu.Unlock()
			return f.opened[id]
		},
		Grace: 200 * time.Millisecond,
	}
	f.host = New(runner, Options{History: filepath.Join(dir, HistoryName), News: filepath.Join(dir, NewsName), After: f.timers.after})
	f.host.Subscribe(func(ev Event) {
		f.mu.Lock()
		f.events = append(f.events, ev)
		if ev.Item != nil && ev.Item.Done && ev.Item.Error == "" {
			f.opened["s1"] = true
		}
		f.mu.Unlock()
	})
	return f
}

// done waits for n finished items and returns them in order.
func (f *fixture) done(t *testing.T, n int) []Item {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		var items []Item
		for _, ev := range f.events {
			if ev.Item != nil && ev.Item.Done {
				items = append(items, *ev.Item)
			}
		}
		f.mu.Unlock()
		if len(items) >= n {
			return items
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("fewer than %d turns finished", n)
	return nil
}

func (f *fixture) read(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestHostCaptainTurnCreatesThenResumesSession(t *testing.T) {
	f := newFixture(t)
	f.host.Submit("xin chào")
	f.done(t, 1)
	f.host.Submit("lần hai")
	items := f.done(t, 2)
	if items[0].Reply != "re: xin chào" || items[1].Reply != "re: lần hai" {
		t.Fatalf("replies = %q, %q", items[0].Reply, items[1].Reply)
	}
	argv := strings.Split(strings.TrimSpace(f.read(t, "argv.log")), "\n")
	if len(argv) != 2 {
		t.Fatalf("argv.log = %q", argv)
	}
	if !strings.Contains(argv[0], "--session-id s1") || strings.Contains(argv[0], "--resume") {
		t.Errorf("first turn argv = %q, want --session-id", argv[0])
	}
	if !strings.Contains(argv[1], "--resume s1") || strings.Contains(argv[1], "--session-id") {
		t.Errorf("second turn argv = %q, want --resume", argv[1])
	}
	for _, a := range argv {
		if !strings.HasSuffix(a, "-p --output-format stream-json --verbose") || !strings.HasPrefix(a, "--model opus") {
			t.Errorf("argv = %q", a)
		}
	}
	if got := History(HistoryPath(f.dir), 0); len(got) != 2 || got[1].Text != "lần hai" {
		t.Errorf("history = %+v", got)
	}
}

func TestHostQueuesCaptainAndRefusesAppWhileBusy(t *testing.T) {
	f := newFixture(t)
	f.host.Submit("slow one")
	waitBusy(t, f.host)
	f.host.Submit("second")
	if s := f.host.State(); !s.Busy || s.Queued != 1 {
		t.Fatalf("state = %+v, want busy with one queued", s)
	}
	if err := f.host.Deliver(Marker + "digest: x"); !errors.Is(err, ErrBusy) {
		t.Fatalf("deliver while busy = %v, want ErrBusy", err)
	}
	items := f.done(t, 2)
	if items[0].Text != "slow one" || items[1].Text != "second" {
		t.Fatalf("order = %q, %q", items[0].Text, items[1].Text)
	}
	// One run at a time: the second prompt was read after the first ended.
	if got := f.read(t, "prompts.log"); got != "slow one\nsecond\n" {
		t.Errorf("prompts = %q", got)
	}
}

func TestHostRefusesWhenAnotherProcessHoldsTheTurnLock(t *testing.T) {
	f := newFixture(t)
	release, ok, err := fileLock(filepath.Join(f.dir, ".turn.lock"))()
	if err != nil || !ok {
		t.Fatalf("lock: %v %v", ok, err)
	}
	if err := f.host.Deliver(Marker + "digest: x"); !errors.Is(err, ErrBusy) {
		t.Fatalf("deliver under a held lock = %v, want ErrBusy", err)
	}
	f.host.Submit("waits")
	if s := f.host.State(); s.Queued != 1 || s.Busy {
		t.Fatalf("state = %+v, want the message queued", s)
	}
	release()
	items := f.done(t, 1)
	if items[0].Text != "waits" {
		t.Fatalf("item = %+v", items[0])
	}
}

func TestHostAppTurnLabelAndSilence(t *testing.T) {
	f := newFixture(t)
	if err := f.host.Deliver(Marker + "digest: 1 item(s) quiet"); err != nil {
		t.Fatal(err)
	}
	items := f.done(t, 1)
	if it := items[0]; it.Source != SourceApp || it.Label != "digest" || !it.Silent || it.Visible() {
		t.Fatalf("item = %+v, want a silent digest", it)
	}
	if err := f.host.Deliver(Marker + "resolve: incident stale"); err != nil {
		t.Fatal(err)
	}
	items = f.done(t, 2)
	if it := items[1]; it.Label != "resolve" || it.Silent {
		t.Fatalf("item = %+v, want a visible resolve", it)
	}
}

func TestHostReportsAFailedRun(t *testing.T) {
	f := newFixture(t)
	f.host.Submit("crash now")
	items := f.done(t, 1)
	if !strings.Contains(items[0].Error, "boom") {
		t.Fatalf("error = %q, want the stderr tail", items[0].Error)
	}
	if f.host.State().Busy {
		t.Fatal("still busy after a failed run")
	}
}

func TestHostInterruptAndStop(t *testing.T) {
	f := newFixture(t)
	f.host.Submit("slow")
	waitBusy(t, f.host)
	if !f.host.Interrupt() {
		t.Fatal("interrupt found no turn")
	}
	if it := f.done(t, 1)[0]; !it.Interrupted {
		t.Fatalf("item = %+v, want interrupted", it)
	}

	f.host.Submit("slow again")
	waitBusy(t, f.host)
	f.host.Submit("queued")
	if killed := f.host.Stop(5*time.Second, false); killed {
		t.Fatal("a stop with time to wait killed the turn")
	}
	if err := f.host.Deliver(Marker + "digest: x"); !errors.Is(err, ErrStopping) {
		t.Fatalf("deliver after stop = %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	if got := len(f.done(t, 2)); got != 2 {
		t.Fatalf("the queued message ran after stop: %d turns", got)
	}
}

func TestSocketRoundTrip(t *testing.T) {
	f := newFixture(t)
	sock, err := os.MkdirTemp("/tmp", "mh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sock)
	path := filepath.Join(sock, SocketName)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopped := make(chan struct{})
	go func() { _ = f.host.Serve(ctx, path, func() { close(stopped) }) }()
	c := Client{Path: path}
	deadline := time.Now().Add(5 * time.Second)
	for c.Ping(ctx) != nil {
		if time.Now().After(deadline) {
			t.Fatal("socket never answered")
		}
		time.Sleep(20 * time.Millisecond)
	}
	f.host.SetDraft("chưa gửi")
	if s, err := c.State(ctx); err != nil || s.Draft != "chưa gửi" || s.Busy {
		t.Fatalf("state = %+v, %v", s, err)
	}
	if err := c.ClearDraft(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.Deliver(ctx, Marker+"digest: slow"); err != nil {
		t.Fatal(err)
	}
	var busy *BusyError
	if err := c.Deliver(ctx, Marker+"digest: again"); !errors.As(err, &busy) {
		t.Fatalf("second deliver = %v, want *BusyError", err)
	}
	if s, _ := c.State(ctx); !s.Busy || s.Draft != "" || s.TurnSource != SourceApp {
		t.Fatalf("state = %+v", s)
	}
	f.done(t, 1)
	if items, err := c.History(ctx, 5); err != nil || len(items) != 1 {
		t.Fatalf("history = %+v, %v", items, err)
	}
	if _, err := c.Stop(ctx, time.Second, false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not call back")
	}
}

func TestTurnEnvDropsNestedSessionAndAddsLaunchEnv(t *testing.T) {
	env := turnEnv([]string{"PATH=/bin", "CLAUDECODE=1", "GONE=x", "MATE_PROJECT_ID=old"},
		Launch{Env: []string{"MATE_PROJECT_ID=p"}, UnsetEnv: []string{"GONE"}}, 3)
	got := strings.Join(env, " ")
	if got != "PATH=/bin MATE_PROJECT_ID=p "+EnvHostTurn+"=3" {
		t.Fatalf("env = %q", got)
	}
}

func TestLabel(t *testing.T) {
	for in, want := range map[string]string{
		Marker + "digest: 1 item(s) — x":  "digest",
		Marker + "resolve: incident":      "resolve",
		"pr-merged: #12":                  "pr-merged",
		Marker + "a sentence: with colon": "mate",
		"plain":                           "mate",
	} {
		if got := Label(in); got != want {
			t.Errorf("Label(%q) = %q, want %q", in, got, want)
		}
	}
}

func waitBusy(t *testing.T, h *Host) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !h.State().Busy {
		if time.Now().After(deadline) {
			t.Fatal("host never got busy")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Let the fake reach its sleep, so the prompt is on disk.
	time.Sleep(300 * time.Millisecond)
}

func TestSummary(t *testing.T) {
	for in, want := range map[string]string{
		Marker + "digest: 2 item(s) — e1 budget 50%: 10M of 20M · e2 blocked: stuck, quiet for 5m — status files under /x/crews; act per AGENTS.md section 10": "e1 budget 50%: 10M of 20M · e2 blocked: stuck, quiet for 5m",
		Marker + `resolve: e1 asked: "why"  — read /x`: `e1 asked: "why" — read /x`,
		"plain words": "plain words",
	} {
		if got := Summary(in); got != want {
			t.Errorf("Summary(%q) = %q, want %q", in, got, want)
		}
	}
}

// A turn's tools reach the REPL while it runs and are kept, ended, in its
// item and the history.
func TestHostRecordsTools(t *testing.T) {
	f := newFixture(t)
	f.host.Submit("xin chào")
	items := f.done(t, 1)
	if tools := items[0].Tools; len(tools) != 1 || tools[0].Name != "Bash" || !tools[0].Done || tools[0].Error {
		t.Fatalf("tools = %+v", tools)
	}
	if items[0].MS <= 0 {
		t.Errorf("ms = %d", items[0].MS)
	}
	running := false
	f.mu.Lock()
	for _, ev := range f.events {
		if ev.Item != nil && !ev.Item.Done && len(ev.Item.Tools) == 1 {
			running = true
		}
	}
	f.mu.Unlock()
	if !running {
		t.Error("no event showed the running turn's tool")
	}
	if got := History(HistoryPath(f.dir), 0); len(got) != 1 || len(got[0].Tools) != 1 {
		t.Errorf("history = %+v", got)
	}
}

// A turn that writes text, calls a tool, then writes more keeps only its
// last message: the text before the tool is the Mate's narration, not what
// it says to the captain (CON-151, reversing CON-79).
func TestHostKeepsTheLastMessage(t *testing.T) {
	for _, prompt := range []string{"chatty", "chatty stream"} {
		t.Run(prompt, func(t *testing.T) {
			f := newFixture(t)
			f.host.Submit(prompt)
			want := "re: " + prompt
			if got := f.done(t, 1)[0].Reply; got != want {
				t.Fatalf("reply = %q, want %q", got, want)
			}
			if got := History(HistoryPath(f.dir), 0); len(got) != 1 || got[0].Reply != want {
				t.Errorf("history = %+v", got)
			}
		})
	}
}

// An app turn that wrote something before its tools and then answered "—"
// is still a silent digest.
func TestHostAppTurnSilentAfterChatter(t *testing.T) {
	for _, mode := range []string{"chatty", "chatty stream"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			if err := f.host.Deliver(Marker + "digest: 1 item(s) quiet " + mode); err != nil {
				t.Fatal(err)
			}
			it := f.done(t, 1)[0]
			if !it.Silent || it.Visible() || it.Reply != "—" {
				t.Fatalf("item = %+v, want a silent digest", it)
			}
		})
	}
}

// TestFinalReply: a finished turn keeps its last message, what the Mate
// says to the captain; what it wrote before its tools is narration
// (CON-151). A turn with no result keeps how far it got.
func TestFinalReply(t *testing.T) {
	for _, c := range []struct{ streamed, result, want string }{
		{"", "only", "only"},
		{"Checking the plan.\n\nDone: two tasks started.", "Done: two tasks started.", "Done: two tasks started."},
		{"a", "b", "b"},          // the stream missed the last message
		{"so far", "", "so far"}, // interrupted: how far it got
		{"chatter\n\n—", "—", "—"},
		{"partial", "API error", "API error"},
	} {
		if got := finalReply(c.streamed, c.result); got != c.want {
			t.Errorf("finalReply(%q, %q) = %q, want %q", c.streamed, c.result, got, c.want)
		}
	}
}

// A run that fails after its result keeps that result's turn: the runner
// reports a failure only when no result came, so the reply is the whole
// reply and the turn is not an error.
func TestHostRunFailingAfterItsResult(t *testing.T) {
	f := newFixture(t)
	f.host.Submit("chatty exitlate")
	it := f.done(t, 1)[0]
	want := "re: chatty exitlate"
	if it.Error != "" || it.Reply != want {
		t.Fatalf("item = %+v, want reply %q and no error", it, want)
	}
}

func TestHostReloadSwitchesSessionBetweenTurns(t *testing.T) {
	f := newFixture(t)
	sock, err := os.MkdirTemp("/tmp", "mh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sock)
	path := filepath.Join(sock, SocketName)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = f.host.Serve(ctx, path, nil) }()
	c := Client{Path: path}
	deadline := time.Now().Add(5 * time.Second)
	for c.Ping(ctx) != nil {
		if time.Now().After(deadline) {
			t.Fatal("socket never answered")
		}
		time.Sleep(20 * time.Millisecond)
	}
	next := f.host.runner.Launch
	next.Args = []string{"--model", "opus", "--session-id", "s2"}
	next.SessionID = "s2"

	f.host.Submit("slow")
	waitBusy(t, f.host)
	var busy *BusyError
	if err := c.Reload(ctx, next); !errors.As(err, &busy) {
		t.Fatalf("reload during a turn = %v, want *BusyError", err)
	}
	f.done(t, 1)
	other := next
	other.Project = "q"
	if err := c.Reload(ctx, other); err == nil || errors.As(err, &busy) {
		t.Fatalf("reload for another Mate = %v, want a refusal", err)
	}
	if err := c.Reload(ctx, next); err != nil {
		t.Fatalf("reload when idle: %v", err)
	}
	f.host.Submit("after")
	f.done(t, 2)
	argv := strings.Split(strings.TrimSpace(f.read(t, "argv.log")), "\n")
	if len(argv) != 2 || !strings.Contains(argv[0], "--session-id s1") || !strings.Contains(argv[1], "--session-id s2") || strings.Contains(argv[1], "s1") {
		t.Fatalf("argv = %q, want the second turn on a fresh s2", argv)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var noted bool
	for _, ev := range f.events {
		noted = noted || strings.Contains(ev.Note, "s2")
	}
	if !noted {
		t.Fatal("the REPL was not told the session changed")
	}
}

// A page's captain ops (CON-142) are the REPL's own: a submitted message is
// a captain turn once its undo is over, an answer lands in its news, and a
// closed news refuses at once.
func TestSocketCaptainOps(t *testing.T) {
	f := newFixture(t)
	sock, err := os.MkdirTemp("/tmp", "mh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sock)
	path := filepath.Join(sock, SocketName)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = f.host.Serve(ctx, path, nil) }()
	c := Client{Path: path}
	deadline := time.Now().Add(5 * time.Second)
	for c.Ping(ctx) != nil {
		if time.Now().After(deadline) {
			t.Fatal("socket never answered")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := c.Submit(ctx, "  "); err == nil {
		t.Error("submitted an empty message")
	}
	if held, err := c.Submit(ctx, "hello from the city"); err != nil || held.Kind != HeldSay || held.Text != "hello from the city" {
		t.Fatalf("submit = %+v, %v", held, err)
	}
	f.timers.fire(t)
	items := f.done(t, 1)
	if it := items[0]; it.Source != SourceCaptain || it.Text != "hello from the city" {
		t.Fatalf("submitted item = %+v", it)
	}
	if ok, err := c.Interrupt(ctx, 0); err != nil || ok {
		t.Fatalf("interrupt with nothing running = %v, %v", ok, err)
	}

	news := filepath.Join(f.dir, NewsName)
	if _, err := OpenNews(news, News{Title: "Dark mode", Question: "Which way?"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	f.host.RefreshNews()
	if _, err := c.AnswerNews(ctx, 1, "[a] env"); err != nil {
		t.Fatal(err)
	}
	f.timers.fire(t)
	if it := f.done(t, 2)[1]; it.News != 1 || it.Text != "[a] env" {
		t.Fatalf("answer item = %+v", it)
	}
	if err := CloseNews(news, 1, "done", false, time.Now()); err != nil {
		t.Fatal(err)
	}
	f.host.RefreshNews()
	if _, err := c.AnswerNews(ctx, 1, "again"); err == nil || !strings.Contains(err.Error(), "already closed") {
		t.Fatalf("answer to a closed news = %v", err)
	}
	if err := c.AskSession(ctx, 1, "why?"); err == nil || !strings.Contains(err.Error(), ErrNoForks.Error()) {
		t.Fatalf("session without forks = %v", err)
	}
	if list, err := c.Sessions(ctx); err != nil || len(list) != 0 {
		t.Fatalf("sessions = %+v, %v", list, err)
	}
}
