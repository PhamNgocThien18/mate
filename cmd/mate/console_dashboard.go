package main

import (
	"strings"

	"github.com/nguyenngocanh94/mate/internal/core"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// consoleDashboard is the dashboard (docs/mvp.md M6) `mate core` serves
// for as long as it runs (internal/core): Mate Office and Agent City, on
// .mate/console.yaml's address (the dashboard's default when empty).
// allowRemote is that file's dashboard.allow_remote: without it a routable
// addr is refused the way `mate dashboard` refuses one.
//
// Its pages press the action sheet's buttons too (CON-141), through the
// engine's Act, the console's way in as well (CON-209, over the socket
// since CON-214), which the engine puts in itself, with whether it has Jev
// to explain a screen (core.Options.Notice, CON-232).
func consoleDashboard(ws *store.Workspace, addr string, allowRemote bool) *core.DashboardOptions {
	return &core.DashboardOptions{Addr: addr, AllowRemote: allowRemote, Deps: dashboardDeps(ws)}
}

// headlessHint is what a console in a terminal it cannot split says
// instead of a next pane (CON-150): its Mates run headless, and the
// captain talks to them in Mate Office or Agent City. dashboardURL is
// where the core serves them (GET /api/core, consoleDashboardURL), "" when
// it serves them over no TCP address.
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
