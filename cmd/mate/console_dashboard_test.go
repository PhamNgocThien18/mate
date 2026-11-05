package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/dashboard"
	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// A page's chat runs on the headless Mate's host (dashboardTalk), with the
// text and the news the page sent, and never reaches the console's action
// function; everything else runs through that function. A page's tick on
// the work board is the work sheet's okay_work there (CON-206): it ticks
// the row and says what the console says.
func TestAPagesChatRunsOnTheMatesHostAndTheRestInTheConsole(t *testing.T) {
	w := pageWorkspace(t)
	host := fakeMateHost(t, w.MateDir("shop"))
	path := matehost.WorkPath(w.MateDir("shop"))
	now := time.Now()
	id, err := matehost.AddWork(path, matehost.WorkAssigned, "Buy button", "", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := matehost.DoneWork(path, id, "", now); err != nil {
		t.Fatal(err)
	}
	_, web := tuiAndWebActions(w, spawn.Deps{}, nil, nil)
	var mu sync.Mutex
	var ran []coreapi.ActionRequest
	act := func(ctx context.Context, req coreapi.ActionRequest) (string, error) {
		mu.Lock()
		ran = append(ran, req)
		mu.Unlock()
		return web(ctx, req)
	}
	// The dashboard the console's engine serves, with the console's own
	// Deps; the snapshot is handed in, as the engine hands in its own. A
	// running Mate offers stop, which is what lets a page talk to it.
	opts := consoleDashboard(w, "", false, act, false)
	if opts.Deps.Act == nil || opts.Deps.Diff == nil || opts.Deps.BranchExists == nil {
		t.Fatalf("consoleDashboard left the page's action or the CLI's answers out: %+v", opts.Deps)
	}
	snap := query.Snapshot{Projects: []query.ProjectNode{{ProjectID: "shop", Name: "shop",
		Mate: query.MateNode{Actions: []query.ActionAvailability{{Action: "stop", Available: true}}}}}}
	deps := opts.Deps
	deps.Snapshot = func(context.Context) (query.Snapshot, error) { return snap, nil }
	handle, err := db.OpenRead(w)
	if err != nil {
		t.Fatal(err)
	}
	defer handle.Close()
	server, err := dashboard.New(dashboard.Options{Workspace: w, DB: handle, Deps: deps})
	if err != nil {
		t.Fatal(err)
	}
	page := httptest.NewServer(server.Handler())
	defer page.Close()
	base := page.URL

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

	status, line := pagePress(t, base, fmt.Sprintf(`{"action":"okay","project":"shop","work":%d}`, id))
	if want := fmt.Sprintf("okayed work #%d of shop; it is off the board", id); status != http.StatusOK || line != want {
		t.Fatalf("okay = %d %q, want %q", status, line, want)
	}
	mu.Lock()
	if len(ran) != 1 || ran[0].Action != coreapi.ActionOkayWork || ran[0].Input != strconv.Itoa(id) || ran[0].Target != "shop" {
		t.Errorf("the console's action function got %+v, want one okay_work of row %d", ran, id)
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
}

// mateHostCalls is what a fakeMateHost was asked, in order.
type mateHostCalls struct {
	mu    sync.Mutex
	calls []hostCall
}

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
	h := &mateHostCalls{}
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
