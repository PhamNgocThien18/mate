package main

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/dashboard"
	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/timeline"
)

// startConsoleDashboard serves the dashboard (docs/mvp.md M6) for as long as
// the console is open, on the default address. Given the console's own
// snapshot loader and action function, its pages can press the action
// sheet's buttons too (CON-141); with either nil it is read-only. It lives in the
// console's process like the observer does, so quitting the console stops it.
//
// The database is opened with db.OpenRead, which takes no lock, so it runs
// beside the observer that owns the writer. It returns the URL being served
// and a stop func that is safe to call more than once.
//
// A busy port is not an error worth stopping the console for: another
// console (or `mate dashboard`) is most likely already serving the same
// workspace there. The caller says so on the status line and carries on.
//
// addr is the bind address, the dashboard's default when empty; a test
// passes its own so it never depends on what this machine already serves.
// allowRemote is .mate/console.yaml's dashboard.allow_remote: without it a
// routable addr is refused the way `mate dashboard` refuses one.
//
// The returned URL is the loopback one; when the bind is reachable from the
// LAN it is followed by that address too, so the status line shows what a
// phone on the same network opens.
func startConsoleDashboard(ctx context.Context, ws *store.Workspace, addr string, allowRemote bool, ingest *timeline.Ingester, load coreapi.LoadFunc, act coreapi.ActionFunc, notice bool) (string, func(), error) {
	handle, err := db.OpenRead(ws)
	if err != nil {
		return "", nil, err
	}
	deps := dashboardDeps(ws)
	if ingest != nil {
		// The ingest is this process's; a console that is not the writer
		// has none and the page says nothing about staleness.
		deps.TimelineStale = ingest.Stale
	}
	if load != nil && act != nil {
		deps.Snapshot = load
		deps.Act = func(actCtx context.Context, req coreapi.ActionRequest) (string, error) {
			if line, ok, err := dashboardTalk(actCtx, ws, req); ok {
				return line, err
			}
			return act(actCtx, req)
		}
		deps.Notice = notice
	}
	opts := dashboard.Options{Workspace: ws, DB: handle, Deps: deps, Addr: addr, AllowRemote: allowRemote}
	server, err := dashboard.New(opts)
	if err != nil {
		_ = handle.Close()
		return "", nil, err
	}
	ln, err := dashboard.Listen(opts)
	if err != nil {
		_ = handle.Close()
		return "", nil, fmt.Errorf("dashboard not started: %w", err)
	}

	serveCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve(serveCtx, ln)
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			<-done
			_ = handle.Close()
		})
	}
	url := dashboard.URL(ln)
	if lan := dashboard.LANURL(ln); lan != "" {
		url += " " + lan
	}
	return url, stop, nil
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
// startConsoleDashboard's, "" when the dashboard did not start.
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
