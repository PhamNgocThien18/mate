package core

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/dashboard"
	"github.com/nguyenngocanh94/mate/internal/query"
)

// The dashboard the engine serves, moved here from cmd/mate's
// startConsoleDashboard with its tests (CON-208).

// freeAddr lets the kernel pick the port: the default one belongs to
// whatever console is open on this machine.
const freeAddr = "127.0.0.1:0"

// dashboardEngine starts an engine over a shopWorkspace that serves the
// dashboard on o, until the test ends. compute, when it is given, is the
// engine's snapshot computation, set before anything can read it, and the
// cache's clock then stands still: a kept snapshot never ages out, so only
// an Invalidate drops it, however slow the machine.
func dashboardEngine(t *testing.T, o DashboardOptions, compute ...func(context.Context) (query.Snapshot, error)) *Engine {
	t.Helper()
	w := shopWorkspace(t)
	withTimelineDB(t, w)
	e := New(Options{Workspace: w, Harnesses: testHarnesses, Dashboard: &o})
	for _, c := range compute {
		e.snaps.compute = c
		frozen := time.Now()
		e.snaps.now = func() time.Time { return frozen }
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(e.Stop)
	return e
}

// get is GET base+path: the status and the body.
func get(t *testing.T, base, path string) (int, string) {
	t.Helper()
	resp, err := http.Get(strings.TrimRight(base, "/") + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp.StatusCode, string(body)
}

// The engine serves the dashboard while it runs and stops with it.
func TestEngineServesTheDashboardUntilStopped(t *testing.T) {
	e := dashboardEngine(t, DashboardOptions{Addr: freeAddr})
	served, err := e.Dashboard()
	if err != nil {
		t.Fatalf("dashboard: %v", err)
	}
	base := strings.Fields(served)[0]
	if status, _ := get(t, base, "/api/workspace"); status != http.StatusOK {
		t.Errorf("status = %d, want 200", status)
	}
	e.Stop()
	e.Stop() // idempotent
	if r, err := http.Get(base + "api/workspace"); err == nil {
		r.Body.Close()
		t.Error("dashboard still answering after stop")
	}
}

// A busy port is reported, not fatal: the engine runs on without a
// dashboard and says why.
func TestEngineDashboardOnABusyPort(t *testing.T) {
	first := dashboardEngine(t, DashboardOptions{Addr: freeAddr})
	served, err := first.Dashboard()
	if err != nil {
		t.Fatalf("dashboard: %v", err)
	}
	u, err := url.Parse(strings.Fields(served)[0])
	if err != nil {
		t.Fatalf("parse %q: %v", served, err)
	}
	second := dashboardEngine(t, DashboardOptions{Addr: u.Host})
	if url, err := second.Dashboard(); err == nil || url != "" {
		t.Fatalf("second dashboard on the same port: %q, %v; want the reason it did not start", url, err)
	}
}

// A routable bind needs console.yaml's allow_remote, the way `mate dashboard`
// needs --allow-remote: without it the dashboard is not started and the
// error says what to set. With it the status line also names the LAN URL.
func TestEngineDashboardRemoteNeedsAllow(t *testing.T) {
	refused := dashboardEngine(t, DashboardOptions{Addr: "0.0.0.0:0"})
	if _, err := refused.Dashboard(); err == nil || !strings.Contains(err.Error(), "allow-remote") {
		t.Fatalf("routable bind without allow_remote: %v", err)
	}
	e := dashboardEngine(t, DashboardOptions{Addr: "0.0.0.0:0", AllowRemote: true})
	served, err := e.Dashboard()
	if err != nil {
		t.Fatalf("start: %v", err)
	}
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

// The dashboard's own answers are the engine's: the snapshot every UI reads,
// the fresh one a press is checked against, and an Invalidate after every
// action. Without an action function it reads no snapshot at all.
func TestEngineDashboardDepsAreTheEngines(t *testing.T) {
	w := shopWorkspace(t)
	act := func(context.Context, coreapi.ActionRequest) (string, error) { return "done", nil }
	e := New(Options{Workspace: w, Dashboard: &DashboardOptions{Deps: dashboard.Deps{Act: act, Notice: true}}})
	defer e.Stop()
	computed := 0
	e.snaps.compute = func(context.Context) (query.Snapshot, error) {
		computed++
		return query.Snapshot{}, nil
	}
	deps := e.dashboardDeps()
	if deps.Snapshot == nil || deps.FreshSnapshot == nil || !deps.Notice {
		t.Fatalf("deps %+v, want the engine's snapshots and the caller's notice", deps)
	}
	ctx := context.Background()
	for range 2 {
		if _, err := deps.Snapshot(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if computed != 1 {
		t.Fatalf("two reads computed %d snapshots, want 1", computed)
	}
	if line, err := deps.Act(ctx, coreapi.ActionRequest{Action: coreapi.ActionMode}); line != "done" || err != nil {
		t.Fatalf("act = %q, %v; want the caller's answer", line, err)
	}
	if _, err := deps.Snapshot(ctx); err != nil || computed != 2 {
		t.Fatalf("a read after an action computed %d snapshots in all (%v), want 2", computed, err)
	}

	readOnly := New(Options{Workspace: w, Dashboard: &DashboardOptions{Deps: dashboard.Deps{Notice: true}}})
	defer readOnly.Stop()
	if deps := readOnly.dashboardDeps(); deps.Snapshot != nil || deps.FreshSnapshot != nil || deps.Act != nil || deps.Notice {
		t.Fatalf("read-only deps %+v, want no snapshot, no action and no notice", deps)
	}
}
