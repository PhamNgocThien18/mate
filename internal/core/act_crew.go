package core

import (
	"context"
	"fmt"

	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/usage"
)

// A Crew's actions (CON-232): stop it, restart it, read its diff and merge
// it. They ran in cmd/mate's consoleAction until CON-215's B5. Every one
// returns the one line a UI shows after it and propagates a failure
// verbatim - a spawn error already says which step refused and what it
// observed, and rewording it here would lose the Herdr detail the operator
// needs.

// stopCrew is the Crew row's `Stop crew…` entry (mvp.md task 16). It
// calls the same function `mate crew stop` calls and reports in the same
// words. It never discards: unlanded work is refused before anything
// changes, and throwing work away stays a deliberate `--discard` on the
// command line.
func (e *Engine) stopCrew(ctx context.Context, req coreapi.ActionRequest) (string, error) {
	ws, deps := e.o.Workspace, e.o.Spawn
	if req.Target == "" || req.Crew == "" {
		return "", observability.NewError(observability.CodeUsage,
			"stop crew needs both a Project and one of its Crews; the request named "+req.Target+"/"+req.Crew)
	}
	res, err := spawn.StopCrew(ctx, ws, deps, req.Target, req.Crew, false)
	if err != nil {
		return "", err
	}
	line := res.CrewLine(req.Target, req.Crew)
	if !res.AlreadyClosed {
		line += "\n" + usage.RecordAtClose(ctx, ws, req.Target, req.Crew, "console stop")
	}
	return line, nil
}

// relaunchCrew is the Console's `Restart crew…` entry (mvp.md M13). It
// calls the same function `mate crew relaunch` calls, so a UI and the
// command line report one event in the same words, and it passes no note:
// a UI has no place to type one, and the crew's `.status` file already
// carries what it said before the pane died.
func (e *Engine) relaunchCrew(ctx context.Context, req coreapi.ActionRequest) (string, error) {
	ws, deps := e.o.Workspace, e.o.Spawn
	if req.Target == "" || req.Crew == "" {
		return "", observability.NewError(observability.CodeUsage,
			"restart crew needs both a Project and one of its Crews; the request named "+req.Target+"/"+req.Crew)
	}
	res, err := spawn.RelaunchCrew(ctx, ws, deps, req.Target, req.Crew, "")
	if err != nil {
		return "", err
	}
	old := "no live agent was recorded"
	if res.Stopped {
		old = "the previous agent was stopped"
	}
	return fmt.Sprintf("crew %s is running again in pane %s (%s); %s", res.Crew, res.Pane, res.Worktree, old), nil
}

// crewDiff is the Console's `diff` entry (mvp.md task 21). It calls the
// same function `mate diff <project> <crew>` calls, so the overlay and the
// terminal show one text and cannot drift: a reader who runs the command
// after reading the overlay sees what they already saw.
//
// It uses no Options.Spawn because it asks Herdr nothing. A diff is git and
// the workspace's own files, and a crew whose agent is gone still has a
// branch worth reading.
func (e *Engine) crewDiff(ctx context.Context, req coreapi.ActionRequest) (string, error) {
	if req.Target == "" || req.Crew == "" {
		return "", observability.NewError(observability.CodeUsage,
			"diff needs both a Project and a Crew; the Console sent "+
				fmt.Sprintf("target=%q crew=%q", req.Target, req.Crew))
	}
	return spawn.CrewDiff(ctx, e.o.Workspace, gitx.New(), req.Target, req.Crew, false)
}

// mergeCrew is the Console's `merge` entry on a `wait-mate` Crew row
// (mvp.md task 22). The caller is CallerUser and is passed as a literal,
// never read from the environment: a UI is the captain's own, and a core
// that inherited MATE_CALLER=mate - which it does whenever the captain
// starts it from inside a Mate's pane - would otherwise refuse the
// captain's own keystroke for want of a review.
//
// The line it returns is the command's own one line, so a UI and `mate
// merge` report the same event in the same words, and a refusal is
// propagated verbatim for the reason every other action here does.
func (e *Engine) mergeCrew(ctx context.Context, req coreapi.ActionRequest) (string, error) {
	ws, deps := e.o.Workspace, e.o.Spawn
	if req.Target == "" || req.Crew == "" {
		return "", observability.NewError(observability.CodeUsage,
			"merge needs both a Project and one of its Crews; the request named "+req.Target+"/"+req.Crew)
	}
	res, err := spawn.MergeCrew(ctx, ws, deps, req.Target, req.Crew, spawn.CallerUser)
	if err != nil {
		return "", err
	}
	return res.Line() + "\n" + usage.RecordAtClose(ctx, ws, req.Target, req.Crew, "console merge"), nil
}
