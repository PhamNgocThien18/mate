package main

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/dashboard"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
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

// TestADashboardPressIsTheSheetsRequest: consoleAction branches on the
// target kind, so a page's press must carry the one the sheet's entry
// builds for the same action (CON-141).
func TestADashboardPressIsTheSheetsRequest(t *testing.T) {
	for _, tc := range []struct {
		in   dashboard.ActionRequest
		want console.ActionRequest
	}{
		{dashboard.ActionRequest{Action: "start", Project: "shop"}, console.ActionRequest{Action: console.ActionStart, Target: "shop", TargetKind: "mate"}},
		{dashboard.ActionRequest{Action: "resume", Project: "shop"}, console.ActionRequest{Action: console.ActionResume, Target: "shop", TargetKind: "mate"}},
		{dashboard.ActionRequest{Action: "onboard", Project: "shop"}, console.ActionRequest{Action: console.ActionOnboard, Target: "shop", TargetKind: "project-mate"}},
		{dashboard.ActionRequest{Action: "stop", Project: "shop"}, console.ActionRequest{Action: console.ActionStop, Target: "shop", TargetKind: "mate"}},
		{dashboard.ActionRequest{Action: "restart_mate", Project: "shop"}, console.ActionRequest{Action: console.ActionRestartMate, Target: "shop", TargetKind: "project"}},
		{dashboard.ActionRequest{Action: "clear_composer", Project: "shop"}, console.ActionRequest{Action: console.ActionClearComposer, Target: "shop", TargetKind: "project"}},
		{dashboard.ActionRequest{Action: "mode", Project: "shop"}, console.ActionRequest{Action: console.ActionMode, Target: "shop", TargetKind: "project"}},
		{dashboard.ActionRequest{Action: "stop", Project: "shop", Crew: "buy"}, console.ActionRequest{Action: console.ActionStop, Target: "shop", TargetKind: "crew", Crew: "buy"}},
		{dashboard.ActionRequest{Action: "merge", Project: "shop", Crew: "buy"}, console.ActionRequest{Action: console.ActionMerge, Target: "shop", TargetKind: "crew", Crew: "buy"}},
		{dashboard.ActionRequest{Action: "restart_crew", Project: "shop", Crew: "buy"}, console.ActionRequest{Action: console.ActionRestartCrew, Target: "shop", TargetKind: "crew", Crew: "buy"}},
		{dashboard.ActionRequest{Action: "diff", Project: "shop", Crew: "buy"}, console.ActionRequest{Action: console.ActionDiff, Target: "shop", TargetKind: "crew", Crew: "buy"}},
		// The City's (CON-142): the new-project form, the box's [assign],
		// the project's remove, a create with a harness, a peek and Jev.
		{dashboard.ActionRequest{Action: "add_project", Project: "landing", Repo: "../landing"}, console.ActionRequest{Action: console.ActionOnboard, TargetKind: "workspace", Input: "landing", Repo: "../landing"}},
		{dashboard.ActionRequest{Action: "assign", Project: "shop", Crew: "buy", Key: "crews/buy.status@9", Line: "resolve: buy asks"}, console.ActionRequest{Action: console.ActionResolve, Target: "shop", TargetKind: "project", Crew: "buy", Input: "resolve: buy asks", Key: "crews/buy.status@9"}},
		{dashboard.ActionRequest{Action: "remove_project", Project: "shop"}, console.ActionRequest{Action: console.ActionRemoveProject, Target: "shop", TargetKind: "project"}},
		{dashboard.ActionRequest{Action: "onboard", Project: "shop", Harness: "codex"}, console.ActionRequest{Action: console.ActionOnboard, Target: "shop", TargetKind: "project-mate", Harness: "codex"}},
		{dashboard.ActionRequest{Action: "peek", Project: "shop", Crew: "buy"}, console.ActionRequest{Action: console.ActionPeek, Target: "shop", TargetKind: "crew", Crew: "buy"}},
		{dashboard.ActionRequest{Action: "explain_notice", Project: "shop", Crew: "buy"}, console.ActionRequest{Action: console.ActionNotice, Target: "shop", TargetKind: "crew", Crew: "buy"}},
	} {
		if got := dashboardActionRequest(tc.in); got != tc.want {
			t.Errorf("%+v -> %+v, want %+v", tc.in, got, tc.want)
		}
	}
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
