package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// freeAddr lets the kernel pick the port: the default one belongs to
// whatever console is open on this machine.
const freeAddr = "127.0.0.1:0"

// The console serves the dashboard while it is open and stops with it.
func TestStartConsoleDashboardServesUntilStopped(t *testing.T) {
	w := usageWorkspace(t)
	url, stop, err := startConsoleDashboard(context.Background(), w, freeAddr, false, nil, nil, nil, false)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	resp, err := http.Get(url + "api/workspace")
	if err != nil {
		stop()
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	stop()
	stop() // idempotent
	if r, err := http.Get(url + "api/workspace"); err == nil {
		r.Body.Close()
		t.Error("dashboard still answering after stop")
	}
}

// A busy port is reported, not fatal.
func TestStartConsoleDashboardBusyPort(t *testing.T) {
	w := usageWorkspace(t)
	served, stop, err := startConsoleDashboard(context.Background(), w, freeAddr, false, nil, nil, nil, false)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer stop()
	u, err := url.Parse(strings.Fields(served)[0])
	if err != nil {
		t.Fatalf("parse %q: %v", served, err)
	}
	if _, _, err := startConsoleDashboard(context.Background(), w, u.Host, false, nil, nil, nil, false); err == nil {
		t.Fatal("second start on the same port should fail")
	}
}

// A routable bind needs console.yaml's allow_remote, the way `mate dashboard`
// needs --allow-remote: without it the dashboard is not started and the
// error says what to set. With it the status line also names the LAN URL.
func TestStartConsoleDashboardRemoteNeedsAllow(t *testing.T) {
	w := usageWorkspace(t)
	if _, _, err := startConsoleDashboard(context.Background(), w, "0.0.0.0:0", false, nil, nil, nil, false); err == nil || !strings.Contains(err.Error(), "allow-remote") {
		t.Fatalf("routable bind without allow_remote: %v", err)
	}
	served, stop, err := startConsoleDashboard(context.Background(), w, "0.0.0.0:0", true, nil, nil, nil, false)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer stop()
	urls := strings.Fields(served)
	if !strings.HasPrefix(urls[0], "http://127.0.0.1:") {
		t.Fatalf("first URL should be loopback: %q", served)
	}
	// The LAN URL exists only on a machine with a private IPv4 address.
	if len(urls) == 2 && (strings.HasPrefix(urls[1], "http://127.") || !strings.HasPrefix(urls[1], "http://")) {
		t.Fatalf("LAN URL: %q", served)
	}
	if len(urls) > 2 {
		t.Fatalf("too many URLs: %q", served)
	}
}

// A page's press runs where it ran before the dashboard learned the
// core's request (CON-206): the chat on the headless Mate's host, with the
// text and the news the page sent (dashboardTalk), and everything else
// through the console's one action function - the work board's tick too,
// which is now the work sheet's okay_work and says what a page's okay said.
func TestAPagePressRunsWhereItRanBefore(t *testing.T) {
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
	var console []coreapi.ActionRequest
	act := func(ctx context.Context, req coreapi.ActionRequest) (string, error) {
		mu.Lock()
		console = append(console, req)
		mu.Unlock()
		return web(ctx, req)
	}
	// A running Mate offers stop, which is what lets a page talk to it.
	snap := query.Snapshot{Projects: []query.ProjectNode{{ProjectID: "shop", Name: "shop",
		Mate: query.MateNode{Actions: []query.ActionAvailability{{Action: "stop", Available: true}}}}}}
	load := func(context.Context) (query.Snapshot, error) { return snap, nil }
	served, stop, err := startConsoleDashboard(context.Background(), w, freeAddr, false, nil, load, act, false)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	base := strings.Fields(served)[0]

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
	if len(console) != 0 {
		t.Errorf("the chat reached the console's action function: %+v", console)
	}
	mu.Unlock()

	status, line := pagePress(t, base, fmt.Sprintf(`{"action":"okay","project":"shop","work":%d}`, id))
	if want := fmt.Sprintf("okayed work #%d of shop; it is off the board", id); status != http.StatusOK || line != want {
		t.Fatalf("okay = %d %q, want %q", status, line, want)
	}
	mu.Lock()
	if len(console) != 1 || console[0].Action != coreapi.ActionOkayWork || console[0].Input != strconv.Itoa(id) || console[0].Target != "shop" {
		t.Errorf("the console's action function got %+v, want one okay_work of row %d", console, id)
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

// pageWorkspace is a workspace with shop, whose Mate runs headless, and
// the database a dashboard reads. It lives under /tmp: the Mate's socket
// is in its directory, and a t.TempDir path on macOS is longer than a unix
// socket path may be.
func pageWorkspace(t *testing.T) *store.Workspace {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "pg")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	w, err := store.Init(root, workspaceDefaults())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(w.ProjectHome("shop"), "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)
	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: repo, DefaultBranch: "main"}}}); err != nil {
		t.Fatal(err)
	}
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

// mateHostCalls stands in for a headless Mate's host: it answers every
// request on the Mate's socket as done, and keeps what each one asked.
type mateHostCalls struct {
	mu    sync.Mutex
	calls []hostCall
}

func (h *mateHostCalls) last() hostCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.calls) == 0 {
		return hostCall{}
	}
	return h.calls[len(h.calls)-1]
}

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
