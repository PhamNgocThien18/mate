package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/autopilot"
	"github.com/nguyenngocanh94/mate/internal/box"
	"github.com/nguyenngocanh94/mate/internal/crewstate"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/usage"
)

// cmdCrewBudget implements
// `mate crew budget <project> <id> --extend <tokens> --reason "<why>" [--captain]`
// (CON-94): the Mate's answer to a crew that reached its budget and handed
// back close to done. --extend is the new ceiling, not an increment. The
// first extension is the Mate's; a second one is the captain's decision,
// so it is refused without --captain.
func cmdCrewBudget(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("crew budget", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, `usage: mate crew budget <project> <id> --extend <tokens> --reason "<why>" [--captain] [--workspace <dir>]`)
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	extendFlag := fs.Int64("extend", 0, "the new token ceiling for the task, above the current one")
	reasonFlag := fs.String("reason", "", "why the task gets more: what the hand-back says is left")
	captainFlag := fs.Bool("captain", false, "the captain approved this extension (required for the second and later)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return newUsageError("mate crew budget: want exactly 2 arguments: <project> <id>")
	}
	if *extendFlag <= 0 {
		return newUsageError("mate crew budget: --extend wants the new ceiling, a positive number of tokens")
	}
	reason := strings.TrimSpace(*reasonFlag)
	if reason == "" {
		return newUsageError("mate crew budget: --reason is required: say what the hand-back shows is left")
	}
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	res, err := extendCrewBudget(w, fs.Arg(0), fs.Arg(1), *extendFlag, reason, *captainFlag)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "%s/%s: budget %s -> %s tokens (extension %d: %s); the next budget line and hard ceiling count against the new ceiling\n",
		fs.Arg(0), fs.Arg(1), budgetWord(res.Old), query.HumanizeTokens(res.New), res.Extensions, reason)
	return nil
}

// budgetExtension is what one extension recorded.
type budgetExtension struct {
	Old, New   int64
	Extensions int
}

func budgetWord(tokens int64) string {
	if tokens <= 0 {
		return "none"
	}
	return query.HumanizeTokens(tokens)
}

// extendCrewBudget writes the new ceiling into the crew's meta, counts the
// extension, deletes the meta keys that made the daemon act once at the
// old ceiling, and resolves the crew's open `budget` incidents: the Mate
// answered them, and the observer files each threshold again against the
// new ceiling.
func extendCrewBudget(w *store.Workspace, project, crew string, ceiling int64, reason string, captain bool) (budgetExtension, error) {
	meta, err := w.ReadCrewMeta(project, crew)
	if err != nil {
		return budgetExtension{}, err
	}
	if len(meta) == 0 {
		return budgetExtension{}, observability.NewError(observability.CodeNotFound,
			fmt.Sprintf("no crew %s is recorded for project %s", crew, project))
	}
	if crewstate.Declare(crewstate.Declaration{Meta: meta}).Closed() {
		return budgetExtension{}, observability.NewError(observability.CodeStateConflict,
			fmt.Sprintf("crew %s/%s is closed; a closed task has nothing left to spend", project, crew))
	}
	old := int64(0)
	if raw := strings.TrimSpace(meta[store.MetaBudget]); raw != "" {
		old, _ = strconv.ParseInt(raw, 10, 64)
	}
	if old <= 0 {
		cfg, err := w.LoadProject(project)
		if err != nil {
			return budgetExtension{}, err
		}
		if cfg.Budget != nil {
			old = cfg.Budget.CrewTokens
		}
	}
	if old > 0 && ceiling <= old {
		return budgetExtension{}, observability.NewError(observability.CodeStateConflict,
			fmt.Sprintf("--extend %s is not above the current ceiling %s; pass the new ceiling, not the increment",
				query.HumanizeTokens(ceiling), query.HumanizeTokens(old)))
	}
	done, _ := strconv.Atoi(meta[store.MetaBudgetExtensions])
	if done >= 1 && !captain {
		return budgetExtension{}, observability.NewError(observability.CodeStateConflict,
			fmt.Sprintf("crew %s/%s was already extended once (%s); a second extension is decides: captain - ask the captain, then pass --captain",
				project, crew, meta[store.MetaBudgetReason]))
	}

	if meta[store.MetaBudgetOriginal] == "" && old > 0 {
		meta[store.MetaBudgetOriginal] = strconv.FormatInt(old, 10)
	}
	meta[store.MetaBudget] = strconv.FormatInt(ceiling, 10)
	meta[store.MetaBudgetExtensions] = strconv.Itoa(done + 1)
	meta[store.MetaBudgetReason] = reason
	delete(meta, store.MetaBudgetLine)
	delete(meta, store.MetaBudgetStop)
	if err := w.WriteCrewMeta(project, crew, meta); err != nil {
		return budgetExtension{}, err
	}

	view, err := box.Load(w, project)
	if err != nil {
		return budgetExtension{}, err
	}
	for _, inc := range box.OpenIncidents(view, crew) {
		if inc.Kind != box.IncidentBudget {
			continue
		}
		// One resolved line closes the nearest open one above it for the
		// same (crew, kind), so one per open incident closes them all.
		if err := w.AppendIncident(project, store.IncidentEntry{
			Crew: crew, Kind: string(box.IncidentBudget), State: store.IncidentResolved,
			Text: fmt.Sprintf("budget extended to %s tokens: %s", query.HumanizeTokens(ceiling), reason),
		}); err != nil {
			return budgetExtension{}, err
		}
	}
	return budgetExtension{Old: old, New: ceiling, Extensions: done + 1}, nil
}

// consoleBudgetActions is the daemon's budget enforcement over the live
// runtime (CON-94): the "budget reached" line goes through the same
// verified send as `mate send`, from the app, queued behind a running turn;
// the hard ceiling is `crew stop`, and where that refuses because the work
// is not landed, the agent alone is stopped and the work kept.
func consoleBudgetActions(ws *store.Workspace, deps spawn.Deps) *autopilot.BudgetActions {
	return &autopilot.BudgetActions{
		Line: func(ctx context.Context, project, crew, text string) error {
			_, err := sendToCrew(ctx, ws, deps, project, crew, text, store.SourceApp,
				send.Options{Marker: true, QueueWhileBusy: true})
			return err
		},
		Stop: func(ctx context.Context, project, crew string) (string, error) {
			return budgetStop(ctx, ws, deps, project, crew)
		},
	}
}

// budgetStop is the hard ceiling's stop: `crew stop` when nothing would be
// lost, else the agent stopped with the worktree and branch kept. It never
// discards: throwing work away is the Mate's or the captain's call.
func budgetStop(ctx context.Context, ws *store.Workspace, deps spawn.Deps, project, crew string) (string, error) {
	res, err := spawn.StopCrew(ctx, ws, deps, project, crew, false)
	if err == nil {
		return "crew stopped, state " + res.State + "; " + usage.RecordAtClose(ctx, ws, project, crew, "budget stop"), nil
	}
	if !errors.Is(err, spawn.ErrUnlandedWork) {
		return "", err
	}
	if _, err := spawn.HaltCrew(ctx, ws, deps, project, crew); err != nil {
		return "", err
	}
	return "agent stopped, unlanded work kept", nil
}
