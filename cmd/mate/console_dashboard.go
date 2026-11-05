package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/core"
	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// consoleDashboard is the dashboard (docs/mvp.md M6) the console's engine
// serves for as long as the console is open (internal/core): Mate Office
// and Agent City, on .mate/console.yaml's address (the dashboard's default
// when empty). allowRemote is that file's dashboard.allow_remote: without
// it a routable addr is refused the way `mate dashboard` refuses one.
//
// Its pages press the action sheet's buttons too (CON-141): act is the
// console's own action function, behind the page's chat (pageAction).
// notice is whether the console has Jev to explain a crew's screen.
func consoleDashboard(ws *store.Workspace, addr string, allowRemote bool, act coreapi.ActionFunc, notice bool) *core.DashboardOptions {
	deps := dashboardDeps(ws)
	deps.Act = pageAction(ws, act)
	deps.Notice = notice
	return &core.DashboardOptions{Addr: addr, AllowRemote: allowRemote, Deps: deps}
}

// pageAction is what a page's press runs: its chat on the headless Mate's
// host (dashboardTalk), anything else through act, the console's.
func pageAction(ws *store.Workspace, act coreapi.ActionFunc) coreapi.ActionFunc {
	return func(ctx context.Context, req coreapi.ActionRequest) (string, error) {
		if line, ok, err := dashboardTalk(ctx, ws, req); ok {
			return line, err
		}
		return act(ctx, req)
	}
}

// dashboardTalk runs a page's chat actions (CON-142) on the headless Mate's
// host, the way its REPL runs them: ok is false for any other action, the
// work board's tick included, which is the console's okay_work (CON-206).
// The host is in the Mate's own process, so it answers for itself; a host
// that is not there says so in its own error.
func dashboardTalk(ctx context.Context, ws *store.Workspace, req coreapi.ActionRequest) (string, bool, error) {
	project := req.Target
	c := matehost.Dial(ws.MateDir(project))
	switch req.Action {
	case coreapi.ActionSay:
		st, err := c.Submit(ctx, req.Input)
		if err != nil {
			return "", true, err
		}
		if st.Queued > 0 {
			return fmt.Sprintf("queued for the Mate of %s, behind the turn in flight (%d waiting)", project, st.Queued), true, nil
		}
		return "sent to the Mate of " + project, true, nil
	case coreapi.ActionInterrupt:
		stopped, err := c.Interrupt(ctx, req.News)
		switch {
		case err != nil:
			return "", true, err
		case !stopped:
			return "nothing was running · nothing changed", true, nil
		case req.News > 0:
			return fmt.Sprintf("interrupted the session of news #%d", req.News), true, nil
		}
		return "interrupted the Mate's turn", true, nil
	case coreapi.ActionAnswer:
		if err := c.AnswerNews(ctx, req.News, req.Input); err != nil {
			return "", true, err
		}
		return fmt.Sprintf("answer to news #%d sent to the Mate of %s", req.News, project), true, nil
	case coreapi.ActionFork:
		if err := c.AskSession(ctx, req.News, req.Input); err != nil {
			return "", true, err
		}
		return fmt.Sprintf("asked the session of news #%d; it answers read-only and changes nothing", req.News), true, nil
	case coreapi.ActionForkClose:
		if err := c.CloseSession(ctx, req.News); err != nil {
			return "", true, err
		}
		return fmt.Sprintf("session of news #%d closed; what was said in it stays", req.News), true, nil
	}
	return "", false, nil
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
