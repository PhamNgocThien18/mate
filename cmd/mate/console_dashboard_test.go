package main

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/dashboard"
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
// builds for the same action (CON-141). It also says, every time, that a
// page cannot show a pane Mate (CON-205), which the sheet leaves to the
// console that runs it (tuiAndWebActions).
func TestADashboardPressIsTheSheetsRequest(t *testing.T) {
	for _, tc := range []struct {
		in   dashboard.ActionRequest
		want coreapi.ActionRequest
	}{
		{dashboard.ActionRequest{Action: "start", Project: "shop"}, coreapi.ActionRequest{Action: coreapi.ActionStart, Target: "shop", TargetKind: "mate"}},
		{dashboard.ActionRequest{Action: "resume", Project: "shop"}, coreapi.ActionRequest{Action: coreapi.ActionResume, Target: "shop", TargetKind: "mate"}},
		{dashboard.ActionRequest{Action: "onboard", Project: "shop"}, coreapi.ActionRequest{Action: coreapi.ActionOnboard, Target: "shop", TargetKind: "project-mate"}},
		{dashboard.ActionRequest{Action: "stop", Project: "shop"}, coreapi.ActionRequest{Action: coreapi.ActionStop, Target: "shop", TargetKind: "mate"}},
		{dashboard.ActionRequest{Action: "restart_mate", Project: "shop"}, coreapi.ActionRequest{Action: coreapi.ActionRestartMate, Target: "shop", TargetKind: "project"}},
		{dashboard.ActionRequest{Action: "clear_composer", Project: "shop"}, coreapi.ActionRequest{Action: coreapi.ActionClearComposer, Target: "shop", TargetKind: "project"}},
		{dashboard.ActionRequest{Action: "mode", Project: "shop"}, coreapi.ActionRequest{Action: coreapi.ActionMode, Target: "shop", TargetKind: "project"}},
		{dashboard.ActionRequest{Action: "stop", Project: "shop", Crew: "buy"}, coreapi.ActionRequest{Action: coreapi.ActionStop, Target: "shop", TargetKind: "crew", Crew: "buy"}},
		{dashboard.ActionRequest{Action: "merge", Project: "shop", Crew: "buy"}, coreapi.ActionRequest{Action: coreapi.ActionMerge, Target: "shop", TargetKind: "crew", Crew: "buy"}},
		{dashboard.ActionRequest{Action: "restart_crew", Project: "shop", Crew: "buy"}, coreapi.ActionRequest{Action: coreapi.ActionRestartCrew, Target: "shop", TargetKind: "crew", Crew: "buy"}},
		{dashboard.ActionRequest{Action: "diff", Project: "shop", Crew: "buy"}, coreapi.ActionRequest{Action: coreapi.ActionDiff, Target: "shop", TargetKind: "crew", Crew: "buy"}},
		// The City's (CON-142): the new-project form, the box's [assign],
		// the project's remove, a create with a harness, a peek and Jev.
		{dashboard.ActionRequest{Action: "add_project", Project: "landing", Repo: "../landing"}, coreapi.ActionRequest{Action: coreapi.ActionOnboard, TargetKind: "workspace", Input: "landing", Repo: "../landing"}},
		{dashboard.ActionRequest{Action: "assign", Project: "shop", Crew: "buy", Key: "crews/buy.status@9", Line: "resolve: buy asks"}, coreapi.ActionRequest{Action: coreapi.ActionResolve, Target: "shop", TargetKind: "project", Crew: "buy", Input: "resolve: buy asks", Key: "crews/buy.status@9"}},
		{dashboard.ActionRequest{Action: "remove_project", Project: "shop"}, coreapi.ActionRequest{Action: coreapi.ActionRemoveProject, Target: "shop", TargetKind: "project"}},
		{dashboard.ActionRequest{Action: "onboard", Project: "shop", Harness: "codex"}, coreapi.ActionRequest{Action: coreapi.ActionOnboard, Target: "shop", TargetKind: "project-mate", Harness: "codex"}},
		{dashboard.ActionRequest{Action: "peek", Project: "shop", Crew: "buy"}, coreapi.ActionRequest{Action: coreapi.ActionPeek, Target: "shop", TargetKind: "crew", Crew: "buy"}},
		{dashboard.ActionRequest{Action: "explain_notice", Project: "shop", Crew: "buy"}, coreapi.ActionRequest{Action: coreapi.ActionNotice, Target: "shop", TargetKind: "crew", Crew: "buy"}},
	} {
		got := dashboardActionRequest(tc.in)
		if !got.HeadlessOnly {
			t.Errorf("%+v -> %+v, want HeadlessOnly: a page cannot show a pane Mate", tc.in, got)
		}
		got.HeadlessOnly = false
		if got != tc.want {
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
