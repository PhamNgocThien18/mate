package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/core"
	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/dashboard"
	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// A page's chat runs in the core, on the headless Mate's host (core's
// Engine.Act), with the text and the news the page sent, and never reaches
// the console's action functions. A page's tick on the work board is the
// work sheet's okay_work (CON-206): the Mate's host holds it for its undo
// (CON-222), and where no host answers the engine ticks the row itself
// (CON-231) and says what the console said.
func TestAPagesChatRunsOnTheMatesHostAndTheRestInTheConsole(t *testing.T) {
	w := pageWorkspace(t)
	host := fakeMateHost(t, w.MateDir("shop"))
	hostGone := host.close
	path := matehost.WorkPath(w.MateDir("shop"))
	now := time.Now()
	id, err := matehost.AddWork(path, matehost.WorkAssigned, "Buy button", "", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := matehost.DoneWork(path, id, "", now); err != nil {
		t.Fatal(err)
	}
	web := engineActions(w, spawn.Deps{}, nil)
	var mu sync.Mutex
	var ran []coreapi.ActionRequest
	act := func(ctx context.Context, req coreapi.ActionRequest) (string, error) {
		mu.Lock()
		ran = append(ran, req)
		mu.Unlock()
		return web(ctx, req)
	}
	opts := consoleDashboard(w, "", false, false)
	if opts.Deps.Diff == nil || opts.Deps.BranchExists == nil {
		t.Fatalf("consoleDashboard left the CLI's answers out: %+v", opts.Deps)
	}
	engine := core.New(core.Options{Workspace: w, Act: act})
	defer engine.Stop()
	// A running Mate offers stop, which is what lets a page talk to it.
	snap := query.Snapshot{Projects: []query.ProjectNode{{ProjectID: "shop", Name: "shop",
		Mate: query.MateNode{Actions: []query.ActionAvailability{{Action: "stop", Available: true}}}}}}
	base := pageServer(t, w, opts, engine.Act, snap)

	for _, tc := range []struct {
		body, line string
		call       hostCall
	}{
		{`{"action":"say","project":"shop","text":"ship it"}`, "sent to the Mate of shop", hostCall{Op: matehost.OpSubmit, Text: "ship it"}},
		{`{"action":"interrupt","project":"shop","news":7}`, "interrupted the session of news #7", hostCall{Op: matehost.OpInterrupt, News: 7}},
		{`{"action":"answer","project":"shop","news":7,"text":"[a]"}`, "answer to news #7 sent to the Mate of shop", hostCall{Op: matehost.OpAnswer, News: 7, Text: "[a]"}},
		{`{"action":"fork","project":"shop","news":7,"text":"why?"}`, "asked the session of news #7; it answers read-only and changes nothing", hostCall{Op: matehost.OpSessionAsk, News: 7, Text: "why?"}},
		{`{"action":"fork_close","project":"shop","news":7}`, "session of news #7 closed; what was said in it stays", hostCall{Op: matehost.OpSessionClose, News: 7}},
	} {
		status, line := pagePress(t, base, tc.body)
		if status != http.StatusOK || line != tc.line {
			t.Errorf("%s = %d %q, want %q", tc.body, status, line, tc.line)
		}
		if got := host.last(); got != tc.call {
			t.Errorf("%s reached the Mate's host as %+v, want %+v", tc.body, got, tc.call)
		}
	}
	mu.Lock()
	if len(ran) != 0 {
		t.Errorf("the chat reached the console's action function: %+v", ran)
	}
	mu.Unlock()

	// The tick goes to the headless Mate's host too, which holds it for its
	// undo (CON-222); with its host gone the engine runs it at once.
	status, line := pagePress(t, base, fmt.Sprintf(`{"action":"okay","project":"shop","work":%d}`, id))
	if want := fmt.Sprintf("okayed work #%d of shop; it is off the board", id); status != http.StatusOK || line != want {
		t.Fatalf("okay = %d %q, want %q", status, line, want)
	}
	if got, want := host.last(), (hostCall{Op: matehost.OpWorkOkay, Work: id}); got != want {
		t.Errorf("the tick reached the Mate's host as %+v, want %+v", got, want)
	}
	hostGone()
	status, line = pagePress(t, base, fmt.Sprintf(`{"action":"okay","project":"shop","work":%d}`, id))
	if want := fmt.Sprintf("okayed work #%d of shop; it is off the board", id); status != http.StatusOK || line != want {
		t.Fatalf("okay with no host = %d %q, want %q", status, line, want)
	}
	mu.Lock()
	if len(ran) != 0 {
		t.Errorf("the tick reached the console's action function: %+v", ran)
	}
	mu.Unlock()
	list, err := matehost.LoadWork(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range list {
		if row.ID == id && row.Status != matehost.WorkOkayed {
			t.Errorf("row %d is %s after the tick, want okayed", id, row.Status)
		}
	}
}

// The console and a page that act on one target at the same time go
// through the same engine (CON-209): the first runs, and the second is
// refused with the engine's line and starts nothing, whichever of the two
// came first. The page gets it as a 409, the console as coreapi.ErrBusy,
// whose text its message line shows verbatim (internal/ui/console's tests).
func TestTheConsoleAndAPageActOnOneTargetOneAtATime(t *testing.T) {
	w := pageWorkspace(t)
	// The first action of each pair, the console's merge then the page's, runs until
	// released; anything else that runs returns at once, so an action let
	// through by mistake fails the test rather than hanging it.
	entered := make(chan struct{}, 2)
	release := []chan struct{}{make(chan struct{}), make(chan struct{})}
	var mu sync.Mutex
	var ran []coreapi.Action
	act := func(_ context.Context, req coreapi.ActionRequest) (string, error) {
		mu.Lock()
		ran = append(ran, req.Action)
		n := len(ran)
		mu.Unlock()
		if req.Action == coreapi.ActionMerge && n <= len(release) {
			entered <- struct{}{}
			<-release[n-1]
		}
		return "did " + string(req.Action), nil
	}
	running := func() {
		t.Helper()
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			t.Fatal("the first action never ran")
		}
	}
	engine := core.New(core.Options{Workspace: w, Act: act})
	defer engine.Stop()
	available := func(names ...string) []query.ActionAvailability {
		var out []query.ActionAvailability
		for _, n := range names {
			out = append(out, query.ActionAvailability{Action: n, Available: true})
		}
		return out
	}
	snap := query.Snapshot{Projects: []query.ProjectNode{{ProjectID: "shop", Name: "shop",
		Mate:  query.MateNode{Actions: available("stop")},
		Crews: []query.CrewNode{{CrewID: "buy", Actions: available("stop", "merge")}}}}}
	base := pageServer(t, w, consoleDashboard(w, "", false, false), engine.Act, snap)
	console := tuiAction(engine.Act, splitTerminal)
	ctx := context.Background()

	// The console's merge first: the page's stop of the same crew is
	// refused while it runs.
	merged := make(chan error, 1)
	go func() {
		_, err := console(ctx, coreapi.ActionRequest{Action: coreapi.ActionMerge, Target: "shop", TargetKind: "crew", Crew: "buy"})
		merged <- err
	}()
	running()
	status, line := pagePress(t, base, `{"action":"stop","project":"shop","crew":"buy"}`)
	if want := "Action refused: merge is still running on shop/buy · nothing started"; status != http.StatusConflict || line != want {
		t.Errorf("the page's stop during the console's merge = %d %q, want 409 %q", status, line, want)
	}
	close(release[0])
	if err := <-merged; err != nil {
		t.Fatalf("the console's merge: %v", err)
	}

	// The page's merge first: the console's stop of the same crew is
	// refused while it runs. (A restart of the Mate, the engine's own
	// action since CON-231, is held by the same lock.)
	pageMerged := make(chan int, 1)
	go func() {
		resp, err := http.Post(base+"/api/actions", "application/json", strings.NewReader(`{"action":"merge","project":"shop","crew":"buy"}`))
		if err != nil {
			pageMerged <- 0
			return
		}
		resp.Body.Close()
		pageMerged <- resp.StatusCode
	}()
	running()
	_, err := console(ctx, coreapi.ActionRequest{Action: coreapi.ActionStop, Target: "shop", TargetKind: "crew", Crew: "buy"})
	if want := "Action refused: merge is still running on shop/buy · nothing started"; !errors.Is(err, coreapi.ErrBusy) || err.Error() != want {
		t.Errorf("the console's stop during the page's merge = %v, want ErrBusy %q", err, want)
	}
	close(release[1])
	if status := <-pageMerged; status != http.StatusOK {
		t.Fatalf("the page's merge = %d, want 200", status)
	}

	mu.Lock()
	defer mu.Unlock()
	if want := []coreapi.Action{coreapi.ActionMerge, coreapi.ActionMerge}; !slices.Equal(ran, want) {
		t.Fatalf("ran %v, want only %v: a refused action starts nothing", ran, want)
	}
}

// pageServer serves the dashboard the console's engine serves, over w, at
// the URL it returns until the test ends: opts' Deps with act, the
// engine's Act, and snap as every snapshot, as the engine hands in its own.
func pageServer(t *testing.T, w *store.Workspace, opts *core.DashboardOptions, act coreapi.ActionFunc, snap query.Snapshot) string {
	t.Helper()
	deps := opts.Deps
	deps.Act = act
	deps.Snapshot = func(context.Context) (query.Snapshot, error) { return snap, nil }
	handle, err := db.OpenRead(w)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { handle.Close() })
	server, err := dashboard.New(dashboard.Options{Workspace: w, DB: handle, Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	page := httptest.NewServer(server.Handler())
	t.Cleanup(page.Close)
	return page.URL
}

// pageWorkspace is tmpShopWorkspace with shop's Mate running headless, and
// the database a dashboard reads.
func pageWorkspace(t *testing.T) *store.Workspace {
	t.Helper()
	w := tmpShopWorkspace(t)
	if err := w.WriteMateMeta("shop", map[string]string{"harness": "claude", "agent": "mate-shop", store.MetaMateMode: store.MateModeHeadless}); err != nil {
		t.Fatal(err)
	}
	handle, err := db.Open(w)
	if err != nil {
		t.Fatal(err)
	}
	if err := handle.Close(); err != nil {
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
}

// mateHostCalls is what a fakeMateHost was asked, in order.
type mateHostCalls struct {
	mu    sync.Mutex
	calls []hostCall
	ln    net.Listener
}

// close takes the host away: its socket is gone.
func (h *mateHostCalls) close() { h.ln.Close() }

// last is the latest request, or none.
func (h *mateHostCalls) last() hostCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.calls) == 0 {
		return hostCall{}
	}
	return h.calls[len(h.calls)-1]
}

// fakeMateHost answers on the socket of the Mate in mateDir the way a
// headless Mate's host does, every request as done, and records what each
// one asked. A real host (matehost.New) would run a harness turn for a
// say, an answer or a fork; this one speaks only the host's wire format,
// matehost's request and response, so a change there shows up here.
func fakeMateHost(t *testing.T, mateDir string) *mateHostCalls {
	t.Helper()
	if err := os.MkdirAll(mateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", matehost.SocketPath(mateDir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	h := &mateHostCalls{ln: ln}
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
				_, _ = conn.Write([]byte(`{"ok":true,"state":{},"interrupted":true}` + "\n"))
			}
			conn.Close()
		}
	}()
	return h
}

// pagePress is a page's POST /api/actions: the status, and the line or,
// when the press was refused, the error.
func pagePress(t *testing.T, base, body string) (int, string) {
	t.Helper()
	resp, err := http.Post(strings.TrimRight(base, "/")+"/api/actions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Line  string `json:"line"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, out.Error
	}
	return resp.StatusCode, out.Line
}

// A console that cannot split names the two pages to open, on loopback,
// with the LAN address when there is one; without a dashboard it says how
// to start one (CON-150).
func TestHeadlessHint(t *testing.T) {
	cases := []struct{ url, want string }{
		{"http://127.0.0.1:7777", "Mates run headless here, open in a browser: Mate Office http://127.0.0.1:7777/office/ · Agent City http://127.0.0.1:7777/city/"},
		{"http://127.0.0.1:7777/ http://192.168.1.5:7777", "Mates run headless here, open in a browser: Mate Office http://127.0.0.1:7777/office/ · Agent City http://127.0.0.1:7777/city/ · LAN http://192.168.1.5:7777"},
		{"", "Mates run headless here: run mate dashboard and open Mate Office or Agent City in a browser"},
	}
	for _, c := range cases {
		if got := headlessHint(c.url); got != c.want {
			t.Errorf("headlessHint(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}
