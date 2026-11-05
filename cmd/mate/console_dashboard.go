package main

import (
	"strings"

	"github.com/nguyenngocanh94/mate/internal/core"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// consoleDashboard is the dashboard (docs/mvp.md M6) the console's engine
// serves for as long as the console is open (internal/core): Mate Office
// and Agent City, on .mate/console.yaml's address (the dashboard's default
// when empty). allowRemote is that file's dashboard.allow_remote: without
// it a routable addr is refused the way `mate dashboard` refuses one.
//
// Its pages press the action sheet's buttons too (CON-141), through the
// engine's Act, the console's way in as well (CON-209), which the engine
// puts in itself. notice is whether the console has Jev to explain a
// crew's screen.
func consoleDashboard(ws *store.Workspace, addr string, allowRemote bool, notice bool) *core.DashboardOptions {
	deps := dashboardDeps(ws)
	deps.Notice = notice
	return &core.DashboardOptions{Addr: addr, AllowRemote: allowRemote, Deps: deps}
}

// headlessHint is what a console in a terminal it cannot split says
// instead of a next pane (CON-150): its Mates run headless, and the
// captain talks to them in Mate Office or Agent City. dashboardURL is
// the console engine's (core.Engine.Dashboard), "" when the dashboard did
// not start.
func headlessHint(dashboardURL string) string {
	fields := strings.Fields(dashboardURL)
	if len(fields) == 0 {
		return "Mates run headless here: run mate dashboard and open Mate Office or Agent City in a browser"
	}
	base := strings.TrimRight(fields[0], "/")
	hint := "Mates run headless here, open in a browser: Mate Office " + base + "/office/ · Agent City " + base + "/city/"
	if len(fields) > 1 {
		hint += " · LAN " + fields[1]
	}
	return hint
}
