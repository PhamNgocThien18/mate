package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/autopilot"
	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/host"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/notice"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// consoleAction is the Console's ActionFunc: the only bridge from the
// action menu and the Mate keys to internal/spawn and internal/store.
//
// Every branch returns the one line the Console shows after the action, and
// every failure is propagated verbatim - a spawn error already says which
// step refused and what it observed, and rewording it here would lose the
// Herdr detail the operator needs. Nothing returns a success it did not
// establish: an action mvp.md has not reached yet refuses by name.
func consoleAction(ws *store.Workspace, deps spawn.Deps) coreapi.ActionFunc {
	holds := newRestartHolds()
	return func(ctx context.Context, req coreapi.ActionRequest) (string, error) {
		switch req.Action {
		case coreapi.ActionStart, coreapi.ActionResume:
			return startMateAction(ctx, ws, deps, req)
		case coreapi.ActionOnboard:
			if req.TargetKind == "workspace" {
				return addProjectAction(ws, req)
			}
			return startMateAction(ctx, ws, deps, req)
		case coreapi.ActionStop:
			if req.TargetKind == "crew" {
				return stopCrewAction(ctx, ws, deps, req)
			}
			if req.TargetKind != "mate" && req.TargetKind != "project-mate" {
				return "", observability.NewError(observability.CodeUsage,
					fmt.Sprintf("stop applies to a Mate or a Crew, not a %s", req.TargetKind))
			}
			res, err := spawn.StopMate(ctx, ws, deps, req.Target)
			if err != nil {
				return "", err
			}
			if res.AlreadyGone {
				return fmt.Sprintf("Mate %s was already gone; mate.meta cleared", res.Agent), nil
			}
			return fmt.Sprintf("Mate %s stop confirmed in session %s", res.Agent, res.Session), nil
		case coreapi.ActionRepair:
			// `mate status` is the repair: it re-asks Herdr about the agent
			// mate.meta names and reports what it found. It never rewrites
			// the meta, so the one line it returns is an observation, not a
			// claim that anything was fixed.
			if req.TargetKind != "mate" && req.TargetKind != "project-mate" && req.TargetKind != "project" {
				return "", observability.NewError(observability.CodeUsage,
					"repairing a Crew is not wired until mvp.md task 16")
			}
			status, err := spawn.MateStatus(ctx, ws, deps, req.Target)
			if err != nil {
				return "", err
			}
			return status.Line(), nil
		case coreapi.ActionMode:
			return toggleModeAction(ws, req.Target)
		case coreapi.ActionResolve:
			return boxResolveAction(ctx, ws, deps, req)
		case coreapi.ActionReply:
			return boxReplyAction(ctx, ws, deps, req)
		case coreapi.ActionPeek:
			return boxPeekAction(ctx, ws, deps, req)
		case coreapi.ActionDiff:
			return crewDiffAction(ctx, ws, req)
		case coreapi.ActionRestartMate:
			return restartMateAction(ctx, ws, deps, req, holds)
		case coreapi.ActionRestartCrew:
			return relaunchCrewAction(ctx, ws, deps, req)
		case coreapi.ActionClearComposer:
			return clearComposerAction(ctx, ws, deps, req)
		case coreapi.ActionRemoveProject:
			return removeProjectAction(ctx, ws, deps, req)
		case coreapi.ActionMerge:
			return mergeCrewAction(ctx, ws, deps, req)
		case coreapi.ActionOpenNews:
			return openNewsAction(ws, req)
		case coreapi.ActionOkayWork:
			return okayWorkAction(ws, req)
		case coreapi.ActionOpenWork:
			return openWorkAction(ws, req)
		default:
			return "", observability.NewError(observability.CodeUsage,
				fmt.Sprintf("%s is not wired in this build", req.Action))
		}
	}
}

// tuiAndWebActions is the console's one action function as its TUI and its
// dashboard pages press it (CON-141, CON-205). consoleAction is called
// once, so the two share one restartHolds: a restart held in one is
// confirmed in the other. A terminal the console cannot split (h is nil:
// neither WezTerm nor Ghostty) has no column to show a pane Mate in, so
// there every TUI request is HeadlessOnly; a page's request says so itself
// (internal/dashboard's coreRequest).
func tuiAndWebActions(ws *store.Workspace, deps spawn.Deps, noticeClient *notice.Client, h host.Host) (tui, web coreapi.ActionFunc) {
	web = consoleAction(ws, deps)
	if noticeClient != nil {
		web = consoleNoticeAction(ws, deps, noticeClient, web)
	}
	headlessOnly := h == nil
	tui = func(ctx context.Context, req coreapi.ActionRequest) (string, error) {
		req.HeadlessOnly = headlessOnly
		return web(ctx, req)
	}
	return tui, web
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
	line := crewStopReport(req.Target, req.Crew, res)
	if !res.AlreadyClosed {
		line += "\n" + recordUsageAtClose(ctx, ws, req.Target, req.Crew, "console stop")
	}
	return line, nil
}

// removeProjectAction is the Project row's `Remove project…` entry (mvp.md
// task 75). It is `mate project remove`: the crews and the Mate are stopped,
// the Mate after the captain's stow, and only then does the Project leave
// workspace.yaml. Like addProjectAction it works on a store it opens itself,
// because RemoveProject saves the whole config and the console's own copy
// may be older than the file.
func removeProjectAction(ctx context.Context, ws *store.Workspace, deps spawn.Deps, req coreapi.ActionRequest) (string, error) {
	if req.Target == "" || req.TargetKind != "project" {
		return "", observability.NewError(observability.CodeUsage,
			fmt.Sprintf("remove project needs a Project; the request named %s %q", req.TargetKind, req.Target))
	}
	fresh, err := store.Open(ws.Root())
	if err != nil {
		return "", err
	}
	var stderr strings.Builder
	res, err := spawn.RemoveProject(ctx, fresh, deps, req.Target, removeProjectOptions(fresh, deps, spawn.CallerUser, &stderr, nil))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Project %s removed: %d agent(s) stopped; its files stay on disk, `mate project add %s` brings it back",
		req.Target, len(res.Stopped), req.Target), nil
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
	return crewDiffText(ctx, ws, gitx.New(), req.Target, req.Crew, false)
}

// startMateAction starts the Mate of one Project. The harness is whatever
// the Console's picker chose; an empty one is not an error but "no choice
// was made", which StartMate reads as the workspace default.
func startMateAction(ctx context.Context, ws *store.Workspace, deps spawn.Deps, req coreapi.ActionRequest) (string, error) {
	if req.Target == "" {
		return "", observability.NewError(observability.CodeUsage, "no Project was named for "+string(req.Action))
	}
	kind, err := consoleHarness(req.Harness)
	if err != nil {
		return "", err
	}
	// Resume is the 's' on a stopped Mate: the conversation recorded in
	// mate.meta comes back, or a fresh one starts with a note saying why.
	res, err := spawn.StartMate(ctx, ws, deps, spawn.StartRequest{Project: req.Target, Harness: kind, Resume: req.Action == coreapi.ActionResume, HeadlessOnly: req.HeadlessOnly})
	if err != nil {
		return "", err
	}
	line := fmt.Sprintf("Mate %s is running on %s in pane %s", res.Agent, res.Harness, res.Pane)
	if res.Resumed {
		line += "; resumed session " + res.ResumedFrom
	}
	if res.ResumeNote != "" {
		line += "; " + res.ResumeNote
	}
	if res.Adopted {
		line += "; adopted: an interrupted start had left it running unrecorded"
	}
	if res.StaleMeta {
		line += "; the previous record was stale and has been replaced"
	}
	if res.UpdateDialog {
		line += "; a Codex update prompt was skipped"
	}
	if res.TrustDialog {
		line += "; a trust dialog was answered"
	}
	return line, nil
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
	return res.Line() + "\n" + recordUsageAtClose(ctx, ws, req.Target, req.Crew, "console merge"), nil
}

// toggleModeAction flips `mate/.auto` (mvp.md section 5). The flag is the
// whole of the mode: the daemon re-reads it every tick and at the moment it
// types, so the keystroke takes effect within one window in both directions
// without the Console telling the daemon anything.
//
// The line it returns names what the flag now permits, and the digest window
// with it, because "auto" on its own does not tell a reader when the first
// line might land in the Mate's pane.
func toggleModeAction(ws *store.Workspace, project string) (string, error) {
	if project == "" {
		return "", observability.NewError(observability.CodeUsage, "no Project was named for mode")
	}
	if err := store.ValidateProjectName(project); err != nil {
		return "", err
	}
	if _, ok := ws.Project(project); !ok {
		return "", fmt.Errorf("%w: %s", store.ErrNoProject, project)
	}
	next := !ws.Auto(project)
	if err := ws.SetMode(project, next); err != nil {
		return "", err
	}
	mode := query.ModeFor(next)
	if next {
		return fmt.Sprintf("%s is now %s; the daemon digests the inbox into the Mate's pane every %s",
			project, mode, autopilot.DefaultInterval), nil
	}
	return fmt.Sprintf("%s is now %s while you are at the Mate; %s without a prompt to it or another m turns auto back on", project, mode, autopilot.DefaultQuietAfter), nil
}

// consoleHarness parses the harness the Console picked. It is parsed here
// rather than trusted: the Console sends a string across a seam, and an
// unknown kind must be a refusal naming the field.
func consoleHarness(kind query.HarnessKind) (harness.Kind, error) {
	if kind == "" {
		return "", nil
	}
	parsed, err := harnesses.Parse(string(kind))
	if err != nil {
		return "", observability.WrapError(observability.CodeUsage, "harness", err)
	}
	return parsed, nil
}

// addProjectAction is the Console's new-project form: the same registration
// `mate project add <name> [<repo>]` performs, with the repo path resolved
// against the workspace root rather than a shell's working directory, since
// the Console has no cwd the reader chose. An empty repo registers a
// Project with no repo yet (docs/mvp.md M9).
//
// It registers through a freshly opened Workspace rather than the shared
// one: the snapshot loader re-reads workspace.yaml into that one on every
// refresh, from another goroutine, and the next refresh picks the new
// Project up from disk anyway.
func addProjectAction(ws *store.Workspace, req coreapi.ActionRequest) (string, error) {
	name, repo := strings.TrimSpace(req.Input), strings.TrimSpace(req.Repo)
	if name == "" {
		return "", observability.NewError(observability.CodeUsage, "project name is required")
	}
	fresh, err := store.Open(ws.Root())
	if err != nil {
		return "", err
	}
	if repo == "" {
		if _, err := addProject(fresh, name, projectAddOptions{}); err != nil {
			return "", err
		}
		return fmt.Sprintf("Project %s added with no repo yet; add one with `mate project repo add %s <path>`", name, name), nil
	}
	absRepo := repo
	if !filepath.IsAbs(absRepo) {
		absRepo = filepath.Join(fresh.Root(), repo)
	}
	repoCfg, err := repoConfigFor(absRepo, repo, "", "")
	if err != nil {
		return "", err
	}
	saved, err := addProject(fresh, name, projectAddOptions{Repos: []store.RepoConfig{repoCfg}})
	if err != nil {
		return "", err
	}
	added := saved.Repos[0]
	return fmt.Sprintf("Project %s added: repo %s, default branch %s", name, added.Path, added.DefaultBranch), nil
}

// openNewsAction is Enter on a news in the box (CON-86): it asks the
// Project's headless Mate REPL to open the news, which the console stages
// beside it. It writes the request and nothing else.
func openNewsAction(ws *store.Workspace, req coreapi.ActionRequest) (string, error) {
	id, err := strconv.Atoi(req.Input)
	if err != nil || id <= 0 || req.Target == "" {
		return "", observability.NewError(observability.CodeUsage,
			fmt.Sprintf("open news needs a Project and a news id; the request named %q #%q", req.Target, req.Input))
	}
	if err := requireProject(ws, req.Target); err != nil {
		return "", err
	}
	if err := matehost.RequestNewsFocus(ws.MateDir(req.Target), id, time.Now()); err != nil {
		return "", err
	}
	return fmt.Sprintf("news #%d opened in the Mate", id), nil
}

// openWorkAction is `w` on a Mate or Project row (CON-198): the Mate's
// REPL shows its work board as a page. A Mate that does not run headless
// has no REPL to ask, and the console shows its own sheet.
func openWorkAction(ws *store.Workspace, req coreapi.ActionRequest) (string, error) {
	if req.Target == "" {
		return "", observability.NewError(observability.CodeUsage, "open work needs a Project")
	}
	if err := requireProject(ws, req.Target); err != nil {
		return "", err
	}
	if !ws.MateHeadless(req.Target) {
		return "", coreapi.ErrNoMateREPL
	}
	if err := matehost.RequestWorkFocus(ws.MateDir(req.Target), time.Now()); err != nil {
		return "", err
	}
	return "work board opened in the Mate", nil
}

// okayWorkAction is the captain's tick on a done row of the work board
// (CON-158): Space on it in the console's work sheet. The tick is final;
// the row leaves the board and the Mate is never waiting on it.
func okayWorkAction(ws *store.Workspace, req coreapi.ActionRequest) (string, error) {
	id, err := strconv.Atoi(req.Input)
	if err != nil || id <= 0 || req.Target == "" {
		return "", observability.NewError(observability.CodeUsage,
			fmt.Sprintf("okay work needs a Project and a row id; the request named %q #%q", req.Target, req.Input))
	}
	if err := requireProject(ws, req.Target); err != nil {
		return "", err
	}
	if err := matehost.OkayWork(matehost.WorkPath(ws.MateDir(req.Target)), id, time.Now()); err != nil {
		return "", err
	}
	return fmt.Sprintf("okayed work #%d of %s; it is off the board", id, req.Target), nil
}
