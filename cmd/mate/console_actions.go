package main

import (
	"context"
	"fmt"

	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/host"
	"github.com/nguyenngocanh94/mate/internal/notice"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/usage"
)

// consoleAction is the engine's Options.Act for every action the engine
// does not run itself (internal/core's ownAction, CON-231): a crew's stop,
// restart, diff and merge, and the box's resolve, reply and peek, until
// CON-232 moves them into the engine too.
//
// Every branch returns the one line the Console shows after the action, and
// every failure is propagated verbatim - a spawn error already says which
// step refused and what it observed, and rewording it here would lose the
// Herdr detail the operator needs. Nothing returns a success it did not
// establish: an action mvp.md has not reached yet refuses by name.
func consoleAction(ws *store.Workspace, deps spawn.Deps) coreapi.ActionFunc {
	return func(ctx context.Context, req coreapi.ActionRequest) (string, error) {
		switch req.Action {
		case coreapi.ActionStop:
			if req.TargetKind == "crew" {
				return stopCrewAction(ctx, ws, deps, req)
			}
		case coreapi.ActionResolve:
			return boxResolveAction(ctx, ws, deps, req)
		case coreapi.ActionReply:
			return boxReplyAction(ctx, ws, deps, req)
		case coreapi.ActionPeek:
			return boxPeekAction(ctx, ws, deps, req)
		case coreapi.ActionDiff:
			return crewDiffAction(ctx, ws, req)
		case coreapi.ActionRestartCrew:
			return relaunchCrewAction(ctx, ws, deps, req)
		case coreapi.ActionMerge:
			return mergeCrewAction(ctx, ws, deps, req)
		}
		return "", observability.NewError(observability.CodeUsage,
			fmt.Sprintf("%s is not wired in this build", req.Action))
	}
}

// engineActions is the engine's Options.Act (internal/core): every action
// the engine does not run itself (consoleAction), with Jev's explain_notice
// in front when there is a Jev. Engine.Act is its one caller, for the
// console's TUI and its dashboard pages alike (CON-141, CON-209).
func engineActions(ws *store.Workspace, deps spawn.Deps, noticeClient *notice.Client) coreapi.ActionFunc {
	act := consoleAction(ws, deps)
	if noticeClient != nil {
		act = consoleNoticeAction(ws, deps, noticeClient, act)
	}
	return act
}

// tuiAction is what the console's TUI presses: act, the engine's Act, with
// every request HeadlessOnly in a terminal the console cannot split (h is
// nil: neither WezTerm nor Ghostty), which has no column to show a pane
// Mate in (CON-205). A page's request says so itself (internal/dashboard's
// coreRequest).
func tuiAction(act coreapi.ActionFunc, h host.Host) coreapi.ActionFunc {
	headlessOnly := h == nil
	return func(ctx context.Context, req coreapi.ActionRequest) (string, error) {
		req.HeadlessOnly = headlessOnly
		return act(ctx, req)
	}
}

// stopCrewAction is the Crew row's `Stop crew…` entry (mvp.md task 16). It
// calls the same function `mate crew stop` calls and reports in the same
// words. It never discards: unlanded work is refused before anything
// changes, and throwing work away stays a deliberate `--discard` on the
// command line.
func stopCrewAction(ctx context.Context, ws *store.Workspace, deps spawn.Deps, req coreapi.ActionRequest) (string, error) {
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

// relaunchCrewAction is the Console's `Restart crew…` entry (mvp.md M13).
// It calls the same function `mate crew relaunch` calls, so the console and
// the command line report one event in the same words, and it passes no
// note: the console has no place to type one, and the crew's `.status` file
// already carries what it said before the pane died.
func relaunchCrewAction(ctx context.Context, ws *store.Workspace, deps spawn.Deps, req coreapi.ActionRequest) (string, error) {
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

// crewDiffAction is the Console's `diff` entry (mvp.md task 21). It calls
// the same function `mate diff <project> <crew>` calls, so the overlay
// and the terminal show one text and cannot drift: a reader who runs the
// command after reading the overlay sees what they already saw.
//
// It takes no spawn.Deps because it asks Herdr nothing. A diff is git and
// the workspace's own files, and a crew whose agent is gone still has a
// branch worth reading.
func crewDiffAction(ctx context.Context, ws *store.Workspace, req coreapi.ActionRequest) (string, error) {
	if req.Target == "" || req.Crew == "" {
		return "", observability.NewError(observability.CodeUsage,
			"diff needs both a Project and a Crew; the Console sent "+
				fmt.Sprintf("target=%q crew=%q", req.Target, req.Crew))
	}
	return spawn.CrewDiff(ctx, ws, gitx.New(), req.Target, req.Crew, false)
}

// mergeCrewAction is the Console's `merge` entry on a `wait-mate` Crew row
// (mvp.md task 22). The caller is CallerUser and is passed as a literal,
// never read from the environment: the console is the captain's own
// program, and a console that inherited MATE_CALLER=mate - which it does
// whenever the captain opens it from inside a Mate's pane - would otherwise
// refuse the captain's own keystroke for want of a review.
//
// The line it returns is the command's own one line, so the console and
// `mate merge` report the same event in the same words, and a refusal is
// propagated verbatim for the reason every other branch here does.
func mergeCrewAction(ctx context.Context, ws *store.Workspace, deps spawn.Deps, req coreapi.ActionRequest) (string, error) {
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
