package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/nguyenngocanh94/mate/internal/dashboard"
	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/timeline"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
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
func startConsoleDashboard(ctx context.Context, ws *store.Workspace, addr string, allowRemote bool, ingest *timeline.Ingester, load console.LoadFunc, act console.ActionFunc, notice bool) (string, func(), error) {
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
		deps.Act = func(actCtx context.Context, req dashboard.ActionRequest) (string, error) {
			if line, ok, err := dashboardTalk(actCtx, ws, req); ok {
				return line, err
			}
			return act(actCtx, dashboardActionRequest(req))
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

// dashboardActionRequest is a page's button press in the shape the console's
// action sheet sends the same action: the target kinds are the ones its
// entries build (internal/ui/console's actionRequest, restartMateChoice,
// onboardChoice), so consoleAction cannot tell the two callers apart.
func dashboardActionRequest(req dashboard.ActionRequest) console.ActionRequest {
	out := console.ActionRequest{Action: console.Action(req.Action), Target: req.Project}
	switch {
	case req.Action == "add_project":
		// The new-project form's request (newProjectChoice).
		out.Action, out.TargetKind, out.Target, out.Input, out.Repo = console.ActionOnboard, "workspace", "", req.Project, req.Repo
	case req.Action == "assign":
		// The box's [assign] (boxAssignChoice); Line is the server's.
		out.Action, out.TargetKind, out.Crew, out.Input, out.Key = console.ActionResolve, "project", req.Crew, req.Line, req.Key
	case req.Crew != "":
		out.TargetKind, out.Crew = "crew", req.Crew
	case out.Action == console.ActionOnboard:
		out.TargetKind, out.Harness = "project-mate", query.HarnessKind(req.Harness)
	case out.Action == console.ActionRestartMate, out.Action == console.ActionClearComposer, out.Action == console.ActionMode,
		out.Action == console.ActionRemoveProject:
		out.TargetKind = "project"
	default:
		out.TargetKind = "mate"
	}
	return out
}

// dashboardTalk runs a page's chat actions (CON-142) on the headless Mate's
// host, the way its REPL runs them: ok is false for any other action. The
// host is in the Mate's own process, so it answers for itself; a host that
// is not there says so in its own error.
func dashboardTalk(ctx context.Context, ws *store.Workspace, req dashboard.ActionRequest) (string, bool, error) {
	if req.Action == "okay" {
		// The captain's tick on the work board (CON-158) is the app's own
		// write; no Mate takes part.
		if err := matehost.OkayWork(matehost.WorkPath(ws.MateDir(req.Project)), req.Work, time.Now()); err != nil {
			return "", true, err
		}
		return fmt.Sprintf("okayed work #%d of %s; it is off the board", req.Work, req.Project), true, nil
	}
	c := matehost.Dial(ws.MateDir(req.Project))
	switch req.Action {
	case "say":
		st, err := c.Submit(ctx, req.Text)
		if err != nil {
			return "", true, err
		}
		if st.Queued > 0 {
			return fmt.Sprintf("queued for the Mate of %s, behind the turn in flight (%d waiting)", req.Project, st.Queued), true, nil
		}
		return "sent to the Mate of " + req.Project, true, nil
	case "interrupt":
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
	case "answer":
		if err := c.AnswerNews(ctx, req.News, req.Text); err != nil {
			return "", true, err
		}
		return fmt.Sprintf("answer to news #%d sent to the Mate of %s", req.News, req.Project), true, nil
	case "fork":
		if err := c.AskSession(ctx, req.News, req.Text); err != nil {
			return "", true, err
		}
		return fmt.Sprintf("asked the session of news #%d; it answers read-only and changes nothing", req.News), true, nil
	case "fork_close":
		if err := c.CloseSession(ctx, req.News); err != nil {
			return "", true, err
		}
		return fmt.Sprintf("session of news #%d closed; what was said in it stays", req.News), true, nil
	}
	return "", false, nil
}
