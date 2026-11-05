package core

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// heldActs is an Options.Act that holds every request whose Input is
// "hold" until release is closed, telling held when it starts; any other
// request returns at once. It records what ran.
type heldActs struct {
	held    chan coreapi.ActionRequest
	release chan struct{}

	mu  sync.Mutex
	ran []coreapi.ActionRequest
}

func newHeldActs() *heldActs {
	return &heldActs{held: make(chan coreapi.ActionRequest, 8), release: make(chan struct{})}
}

func (h *heldActs) act(ctx context.Context, req coreapi.ActionRequest) (string, error) {
	h.mu.Lock()
	h.ran = append(h.ran, req)
	h.mu.Unlock()
	if req.Input == "hold" {
		h.held <- req
		select {
		case <-h.release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return "did " + string(req.Action), nil
}

func (h *heldActs) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.ran)
}

// hold starts req on e, held, and returns once it runs; the returned
// channel gets its error when it ends.
func hold(t *testing.T, e *Engine, h *heldActs, req coreapi.ActionRequest) <-chan error {
	t.Helper()
	req.Input = "hold"
	done := make(chan error, 1)
	go func() {
		_, err := e.Act(context.Background(), req)
		done <- err
	}()
	select {
	case <-h.held:
	case err := <-done:
		t.Fatalf("%s to hold returned %v without running", req.Action, err)
	case <-time.After(10 * time.Second):
		t.Fatalf("%s to hold never ran", req.Action)
	}
	return done
}

var (
	stopMate   = coreapi.ActionRequest{Action: coreapi.ActionStop, Target: "shop", TargetKind: "mate"}
	mergeCrew  = coreapi.ActionRequest{Action: coreapi.ActionMerge, Target: "shop", TargetKind: "crew", Crew: "buy"}
	addProject = coreapi.ActionRequest{Action: coreapi.ActionOnboard, TargetKind: "workspace", Input: "landing"}
)

// A second action on a target an action is still running on is refused
// with coreapi.ErrBusy, in the line every UI shows, and starts nothing
// (CON-209). The target is the Project, or the crew when the request names
// one; adding a Project holds the whole workspace. Another target runs.
func TestActRefusesASecondActionOnABusyTarget(t *testing.T) {
	for _, tc := range []struct {
		name          string
		first, second coreapi.ActionRequest
		refusal       string
	}{
		{"the Mate, from its stop to its restart", stopMate,
			coreapi.ActionRequest{Action: coreapi.ActionRestartMate, Target: "shop", TargetKind: "project"},
			"Action refused: stop is still running on shop · nothing started"},
		{"a crew, from its merge to its stop", mergeCrew,
			coreapi.ActionRequest{Action: coreapi.ActionStop, Target: "shop", TargetKind: "crew", Crew: "buy"},
			"Action refused: merge is still running on shop/buy · nothing started"},
		{"a crew, from its merge to an assign of its question", mergeCrew,
			coreapi.ActionRequest{Action: coreapi.ActionResolve, Target: "shop", TargetKind: "project", Crew: "buy", Input: "resolve: buy asks"},
			"Action refused: merge is still running on shop/buy · nothing started"},
		{"the workspace, from one new Project to another", addProject,
			coreapi.ActionRequest{Action: coreapi.ActionOnboard, TargetKind: "workspace", Input: "docs"},
			"Action refused: onboard is still running on the workspace · nothing started"},
		{"two peeks of one crew", coreapi.ActionRequest{Action: coreapi.ActionPeek, Target: "shop", TargetKind: "crew", Crew: "buy"},
			coreapi.ActionRequest{Action: coreapi.ActionPeek, Target: "shop", TargetKind: "crew", Crew: "buy"},
			"Action refused: peek is still running on shop/buy · nothing started"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHeldActs()
			e := New(Options{Workspace: shopWorkspace(t), Act: h.act})
			defer e.Stop()
			done := hold(t, e, h, tc.first)
			_, err := e.Act(context.Background(), tc.second)
			if !errors.Is(err, coreapi.ErrBusy) || err.Error() != tc.refusal {
				t.Fatalf("second action = %v, want ErrBusy %q", err, tc.refusal)
			}
			if got := h.count(); got != 1 {
				t.Fatalf("%d actions ran, want only the first", got)
			}
			// The Mate of shop is another target than its crew, and the
			// crew another than the Mate.
			other := coreapi.ActionRequest{Action: coreapi.ActionMode, Target: "other", TargetKind: "project"}
			if tc.first.Crew != "" {
				other = coreapi.ActionRequest{Action: coreapi.ActionMode, Target: "shop", TargetKind: "project"}
			}
			if _, err := e.Act(context.Background(), other); err != nil {
				t.Fatalf("an action on another target: %v", err)
			}
			close(h.release)
			if err := <-done; err != nil {
				t.Fatalf("first action: %v", err)
			}
			if _, err := e.Act(context.Background(), tc.second); err != nil {
				t.Fatalf("the second action once the first ended: %v", err)
			}
		})
	}
}

// Talking to the Mate, reading a crew's screen, asking Jev about it,
// ticking the work board and opening a news or the board hold a lock of
// their own: they run while a stop or a merge runs on the same Mate or
// crew, and do not hold it up.
func TestChatAndPeekRunDuringAStopOrAMerge(t *testing.T) {
	w := hostWorkspace(t)
	calls := fakeHost(t, w.MateDir("shop"))
	h := newHeldActs()
	e := New(Options{Workspace: w, Act: h.act})
	defer e.Stop()
	stopped := hold(t, e, h, stopMate)
	merged := hold(t, e, h, mergeCrew)

	for _, req := range []coreapi.ActionRequest{
		{Action: coreapi.ActionSay, Target: "shop", TargetKind: "mate", Input: "ship it"},
		{Action: coreapi.ActionInterrupt, Target: "shop", TargetKind: "mate"},
		{Action: coreapi.ActionAnswer, Target: "shop", TargetKind: "mate", Input: "[a]", News: 7},
		{Action: coreapi.ActionFork, Target: "shop", TargetKind: "mate", Input: "why?", News: 7},
		{Action: coreapi.ActionForkClose, Target: "shop", TargetKind: "mate", News: 7},
		{Action: coreapi.ActionOkayWork, Target: "shop", TargetKind: "project", Input: "3"},
		{Action: coreapi.ActionOpenNews, Target: "shop", TargetKind: "project", Input: "7"},
		{Action: coreapi.ActionOpenWork, Target: "shop", TargetKind: "project"},
		{Action: coreapi.ActionNotice, Target: "shop", TargetKind: "mate"},
		{Action: coreapi.ActionPeek, Target: "shop", TargetKind: "crew", Crew: "buy"},
		{Action: coreapi.ActionNotice, Target: "shop", TargetKind: "crew", Crew: "buy"},
	} {
		if _, err := e.Act(context.Background(), req); err != nil {
			t.Errorf("%s during a stop and a merge: %v", req.Action, err)
		}
	}
	if got := len(calls.all()); got != 6 {
		t.Errorf("the Mate's host was asked %d times, want once per chat action and once for the tick", got)
	}
	// The stop and the merge still hold their targets.
	for _, req := range []coreapi.ActionRequest{stopMate, mergeCrew} {
		if _, err := e.Act(context.Background(), req); !errors.Is(err, coreapi.ErrBusy) {
			t.Errorf("a second %s = %v, want ErrBusy", req.Action, err)
		}
	}
	close(h.release)
	for _, done := range []<-chan error{stopped, merged} {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}

// The chat runs in the core, on the headless Mate's host, and never
// reaches Options.Act: each action is the host's op with the text and the
// news the UI sent, answered in the line the pages show.
func TestTheChatRunsOnTheMatesHost(t *testing.T) {
	w := hostWorkspace(t)
	calls := fakeHost(t, w.MateDir("shop"))
	h := newHeldActs()
	e := New(Options{Workspace: w, Act: h.act})
	defer e.Stop()
	for _, tc := range []struct {
		req  coreapi.ActionRequest
		line string
		call hostCall
	}{
		{coreapi.ActionRequest{Action: coreapi.ActionSay, Target: "shop", Input: "ship it"}, "sent to the Mate of shop", hostCall{Op: matehost.OpSubmit, Text: "ship it"}},
		{coreapi.ActionRequest{Action: coreapi.ActionInterrupt, Target: "shop", News: 7}, "interrupted the session of news #7", hostCall{Op: matehost.OpInterrupt, News: 7}},
		{coreapi.ActionRequest{Action: coreapi.ActionInterrupt, Target: "shop"}, "interrupted the Mate's turn", hostCall{Op: matehost.OpInterrupt}},
		{coreapi.ActionRequest{Action: coreapi.ActionAnswer, Target: "shop", News: 7, Input: "[a]"}, "answer to news #7 sent to the Mate of shop", hostCall{Op: matehost.OpAnswer, News: 7, Text: "[a]"}},
		{coreapi.ActionRequest{Action: coreapi.ActionFork, Target: "shop", News: 7, Input: "why?"}, "asked the session of news #7; it answers read-only and changes nothing", hostCall{Op: matehost.OpSessionAsk, News: 7, Text: "why?"}},
		{coreapi.ActionRequest{Action: coreapi.ActionForkClose, Target: "shop", News: 7}, "session of news #7 closed; what was said in it stays", hostCall{Op: matehost.OpSessionClose, News: 7}},
	} {
		line, err := e.Act(context.Background(), tc.req)
		if err != nil || line != tc.line {
			t.Errorf("%s = %q, %v; want %q", tc.req.Action, line, err, tc.line)
		}
		if got := calls.last(); got != tc.call {
			t.Errorf("%s reached the host as %+v, want %+v", tc.req.Action, got, tc.call)
		}
	}
	if got := h.count(); got != 0 {
		t.Fatalf("the chat reached Options.Act %d times", got)
	}
}

// The host holds what the captain sends for its undo (CON-222): a say, an
// answer and a tick are held there, an undo takes one back by its id, and
// each line says so. None reaches Options.Act.
func TestTheHostHoldsTheCaptainsWords(t *testing.T) {
	w := hostWorkspace(t)
	calls := holdingHost(t, w.MateDir("shop"))
	h := newHeldActs()
	e := New(Options{Workspace: w, Act: h.act})
	defer e.Stop()
	for _, tc := range []struct {
		req  coreapi.ActionRequest
		line string
		call hostCall
	}{
		{coreapi.ActionRequest{Action: coreapi.ActionSay, Target: "shop", Input: "ship it"}, "goes to the Mate of shop in 5 s · undo until then", hostCall{Op: matehost.OpSubmit, Text: "ship it"}},
		{coreapi.ActionRequest{Action: coreapi.ActionAnswer, Target: "shop", News: 7, Input: "[a]"}, "answer to news #7 goes to the Mate of shop in 5 s · undo until then", hostCall{Op: matehost.OpAnswer, News: 7, Text: "[a]"}},
		{coreapi.ActionRequest{Action: coreapi.ActionOkayWork, Target: "shop", TargetKind: "project", Input: "3"}, "OK of work #3 of shop goes in 5 s · undo until then", hostCall{Op: matehost.OpWorkOkay, Work: 3}},
		{coreapi.ActionRequest{Action: coreapi.ActionUndo, Target: "shop", Input: "4"}, "took back the answer to news #7 · nothing sent", hostCall{Op: matehost.OpUndo, Held: 4}},
	} {
		line, err := e.Act(context.Background(), tc.req)
		if err != nil || line != tc.line {
			t.Errorf("%s = %q, %v; want %q", tc.req.Action, line, err, tc.line)
		}
		if got := calls.last(); got != tc.call {
			t.Errorf("%s reached the host as %+v, want %+v", tc.req.Action, got, tc.call)
		}
	}
	if _, err := e.Act(context.Background(), coreapi.ActionRequest{Action: coreapi.ActionUndo, Target: "shop", Input: "9"}); !errors.Is(err, matehost.ErrNotHeld) {
		t.Errorf("undo of an item gone = %v, want ErrNotHeld", err)
	}
	if _, err := e.Act(context.Background(), coreapi.ActionRequest{Action: coreapi.ActionUndo, Target: "shop", Input: "x"}); err == nil {
		t.Error("undo naming no item ran")
	}
	if got := h.count(); got != 0 {
		t.Fatalf("the host's work reached Options.Act %d times", got)
	}
}

// A tick with no headless Mate's host to hold it - no Mate, no host to
// dial, a host older than work-okay - runs at once through Options.Act, as
// before the undo.
func TestTheTickRunsInTheConsoleWithoutAHost(t *testing.T) {
	okay := coreapi.ActionRequest{Action: coreapi.ActionOkayWork, Target: "shop", TargetKind: "project", Input: "3"}
	old := hostWorkspace(t)
	serveFakeHost(t, old.MateDir("shop"), func(hostCall) string { return `{"error":"unknown op \"work-okay\""}` })
	for name, w := range map[string]*store.Workspace{"no Mate": shopWorkspace(t), "no host": hostWorkspace(t), "an older host": old} {
		h := newHeldActs()
		e := New(Options{Workspace: w, Act: h.act})
		if _, err := e.Act(context.Background(), okay); err != nil {
			t.Errorf("%s: okay = %v", name, err)
		}
		if got := h.count(); got != 1 {
			t.Errorf("%s: Options.Act ran %d times, want the tick once", name, got)
		}
		e.Stop()
	}
}

// A chat to a Mate that is not running, or that runs in its pane, is
// refused in the words the pages show on a disabled chat, and asks no host.
func TestTheChatRefusesAMateItCannotReach(t *testing.T) {
	w := shopWorkspace(t)
	h := newHeldActs()
	e := New(Options{Workspace: w, Act: h.act})
	defer e.Stop()
	say := coreapi.ActionRequest{Action: coreapi.ActionSay, Target: "shop", TargetKind: "mate", Input: "hi"}
	if _, err := e.Act(context.Background(), say); err == nil || err.Error() != "the Mate is not running · start it first" {
		t.Fatalf("say to a Mate with no record = %v", err)
	}
	// Stopped: the record keeps its mode and names no agent.
	if err := w.WriteMateMeta("shop", map[string]string{"harness": "claude", store.MetaMateMode: store.MateModeHeadless}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Act(context.Background(), say); err == nil || err.Error() != "the Mate is not running · start it first" {
		t.Fatalf("say to a stopped Mate = %v", err)
	}
	if err := w.WriteMateMeta("shop", map[string]string{"harness": "claude", "agent": "mate-shop"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Act(context.Background(), say); err == nil || err.Error() != "this Mate is not headless · type in its pane" {
		t.Fatalf("say to a pane Mate = %v", err)
	}
	if _, err := e.Act(context.Background(), coreapi.ActionRequest{Action: coreapi.ActionSay, Target: "nowhere", Input: "hi"}); err == nil {
		t.Fatal("say to a project not in the workspace went through")
	}
	if got := h.count(); got != 0 {
		t.Fatalf("a refused chat reached Options.Act %d times", got)
	}
}

// The context is the caller's: a console that quits in the middle of an
// action cancels it, and the action is told. Whatever an action returns,
// failed or cancelled, the cached snapshot is dropped and the target is
// free again.
func TestActPassesTheCallersContextAndAlwaysCleansUp(t *testing.T) {
	h := newHeldActs()
	e := New(Options{Workspace: shopWorkspace(t), Act: h.act})
	defer e.Stop()
	computed := 0
	e.snaps.compute = func(context.Context) (query.Snapshot, error) {
		computed++
		return query.Snapshot{}, nil
	}
	if _, err := e.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	req := stopMate
	req.Input = "hold"
	done := make(chan error, 1)
	go func() {
		_, err := e.Act(ctx, req)
		done <- err
	}()
	<-h.held
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled action = %v, want the context's error", err)
	}
	if _, err := e.Snapshot(context.Background()); err != nil || computed != 2 {
		t.Fatalf("a read after the cancelled action computed %d snapshots in all (%v), want 2", computed, err)
	}
	if _, err := e.Act(context.Background(), stopMate); err != nil {
		t.Fatalf("the target after a cancelled action: %v", err)
	}

	// A refusal changed nothing, and drops nothing either.
	held := hold(t, e, h, stopMate)
	if _, err := e.Snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := computed
	if _, err := e.Act(context.Background(), stopMate); !errors.Is(err, coreapi.ErrBusy) {
		t.Fatalf("second stop = %v, want ErrBusy", err)
	}
	if _, err := e.Snapshot(context.Background()); err != nil || computed != before {
		t.Fatalf("a refused action dropped the snapshot: %d computations, want %d", computed, before)
	}
	close(h.release)
	if err := <-held; err != nil {
		t.Fatal(err)
	}
}

// Adding a Project holds the workspace, not a Project that happens to be
// named "workspace".
func TestAddingAProjectDoesNotHoldAProjectNamedWorkspace(t *testing.T) {
	h := newHeldActs()
	e := New(Options{Workspace: shopWorkspace(t), Act: h.act})
	defer e.Stop()
	done := hold(t, e, h, addProject)
	if _, err := e.Act(context.Background(), coreapi.ActionRequest{Action: coreapi.ActionMode, Target: "workspace", TargetKind: "project"}); err != nil {
		t.Fatalf("mode of project workspace while a project is added: %v", err)
	}
	close(h.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// With no Options.Act, an action other than the chat says so and runs
// nothing.
func TestActWithoutAnActionFunction(t *testing.T) {
	e := New(Options{Workspace: shopWorkspace(t)})
	defer e.Stop()
	if _, err := e.Act(context.Background(), stopMate); err == nil {
		t.Fatal("stop with no action function went through")
	}
}

// hostWorkspace is a shopWorkspace under /tmp, whose Mate's socket path
// fits the unix limit, with shop's Mate recorded running headless.
func hostWorkspace(t *testing.T) *store.Workspace {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "core")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	w, err := store.Init(root, store.Defaults{MateHarness: "claude", CrewHarness: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddProject("shop", store.ProjectConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteMateMeta("shop", map[string]string{"harness": "claude", "agent": "mate-shop", store.MetaMateMode: store.MateModeHeadless}); err != nil {
		t.Fatal(err)
	}
	return w
}

// hostCall is what one request asked a Mate's host.
type hostCall struct {
	Op   string `json:"op"`
	Text string `json:"text"`
	News int    `json:"news"`
	Work int    `json:"work"`
	Held int    `json:"held"`
}

type hostCalls struct {
	mu    sync.Mutex
	calls []hostCall
}

func (h *hostCalls) all() []hostCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]hostCall(nil), h.calls...)
}

func (h *hostCalls) last() hostCall {
	all := h.all()
	if len(all) == 0 {
		return hostCall{}
	}
	return all[len(all)-1]
}

// fakeHost answers on the socket of the Mate in mateDir the way a headless
// Mate's host from before the undo (CON-222) did, every request as done at
// once, and records what each asked. It speaks only matehost's wire
// format, so a change there shows up here.
func fakeHost(t *testing.T, mateDir string) *hostCalls {
	t.Helper()
	return serveFakeHost(t, mateDir, func(hostCall) string { return `{"ok":true,"state":{},"interrupted":true}` })
}

// holdingHost is fakeHost as a host with the undo answers: what the
// captain sends is held as item 4, and an undo gives back item 4, an answer
// to news 7.
func holdingHost(t *testing.T, mateDir string) *hostCalls {
	t.Helper()
	return serveFakeHost(t, mateDir, func(c hostCall) string {
		var held matehost.Held
		switch c.Op {
		case matehost.OpSubmit:
			held = matehost.Held{ID: 4, Kind: matehost.HeldSay, Text: c.Text}
		case matehost.OpAnswer:
			held = matehost.Held{ID: 4, Kind: matehost.HeldAnswer, Text: c.Text, News: c.News}
		case matehost.OpWorkOkay:
			held = matehost.Held{ID: 4, Kind: matehost.HeldOkay, Work: c.Work}
		case matehost.OpUndo:
			if c.Held != 4 {
				return `{"error":"already sent: nothing to undo","code":"not-held"}`
			}
			held = matehost.Held{ID: 4, Kind: matehost.HeldAnswer, Text: "[a]", News: 7}
		default:
			return `{"ok":true,"state":{}}`
		}
		data, _ := json.Marshal(map[string]any{"ok": true, "state": map[string]any{}, "held": held})
		return string(data)
	})
}

func serveFakeHost(t *testing.T, mateDir string, reply func(hostCall) string) *hostCalls {
	t.Helper()
	if err := os.MkdirAll(mateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", matehost.SocketPath(mateDir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	h := &hostCalls{}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			var call hostCall
			if err := json.NewDecoder(conn).Decode(&call); err == nil {
				h.mu.Lock()
				h.calls = append(h.calls, call)
				h.mu.Unlock()
				_, _ = conn.Write([]byte(reply(call) + "\n"))
			}
			conn.Close()
		}
	}()
	return h
}
