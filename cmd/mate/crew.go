package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/nguyenngocanh94/mate/internal/crewstate"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// cmdCrew dispatches `mate crew <spawn|list|stop|relaunch|dispatch|budget>`.
func (c cli) cmdCrew(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return newUsageError("usage: mate crew <spawn|list|stop|relaunch|dispatch|budget> ...")
	}
	switch args[0] {
	case "spawn":
		return c.cmdCrewSpawn(args[1:], stdin, stdout, stderr)
	case "dispatch":
		return c.cmdCrewDispatch(args[1:], stdout, stderr)
	case "list":
		return cmdCrewList(args[1:], stdout, stderr)
	case "stop":
		return c.cmdCrewStop(args[1:], stdout, stderr)
	case "relaunch", "restart":
		return cmdCrewRelaunch(args[1:], stdout, stderr)
	case "budget":
		return cmdCrewBudget(args[1:], stdout, stderr)
	default:
		return newUsageErrorf("unknown crew subcommand %q", args[0])
	}
}

// cmdCrewSpawn implements
// `mate crew spawn <project> <id> --brief <file> [--repo <name>] [--scout] [--deliver local|pr] [--size small|medium|large] [--harness codex|claude] [--model <name>] [--effort <level>] [--budget <tokens>] [--skip <harness>]... [--task "<one line>"]`.
func (c cli) cmdCrewSpawn(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("crew spawn", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, `usage: mate crew spawn <project> <id> --brief <file|-> [--repo <name>] [--scout] [--deliver local|pr] [--size small|medium|large] [--workspace <dir>] [--harness `+harnessChoices(harness.RoleCrew, "|")+`] [--model <name>] [--effort low|medium|high|xhigh|max] [--budget <tokens>] [--skip <harness>]... [--task "<one line>"]`)
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	scoutFlag := fs.Bool("scout", false, "a scout: the brief has ## Deliverable and the crew writes a report instead of committing")
	var skip kindList
	fs.Var(&skip, "skip", "a harness passed over in the dispatch table's rotation, as passed to `crew dispatch` (repeatable)")
	// --size (CON-72) may later imply a budget when none is passed; this
	// command records only what it is told.
	budgetFlag := fs.Int64("budget", 0, "token budget from the dispatch rule the task matched; the observer reports the crew at half of it without a hand-back and at all of it (default: the project's budget.crew_tokens, if any)")
	harnessFlag := fs.String("harness", "", "harness to launch ("+harnessChoices(harness.RoleCrew, " or ")+"; default: the dispatch table's default profile)")
	modelFlag := fs.String("model", "", "model the harness runs, as it names it (default: the harness's own)")
	effortFlag := fs.String("effort", "", "reasoning effort: low, medium, high, xhigh or max (default: the harness's own)")
	briefFlag := fs.String("brief", "", "file holding the task text, or - to read it from stdin")
	repoFlag := fs.String("repo", "", "the project repo the crew works in (required when the project has several)")
	deliverFlag := fs.String("deliver", "", "how a ship's work lands: local (fast-forward on the primary checkout, the default) or pr (push the branch and open a pull request; needs gh logged in and origin on GitHub)")
	sizeFlag := fs.String("size", "", "how big the task is: small, medium (the default) or large; picks the hand-back shape and evidence level the brief asks of a ship")
	taskFlag := fs.String("task", "", "one line recorded as task= (default: the brief's first line)")
	laneFlag := fs.String("lane", "", "the lane the crew holds while it works (default: its repo's; none for a task that never touches the lane's resource, such as a backend change in an iOS repo)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return newUsageError("mate crew spawn: want exactly 2 arguments: <project> <id>")
	}
	if *briefFlag == "" {
		fs.Usage()
		return newUsageError("mate crew spawn: --brief is required")
	}
	if *budgetFlag < 0 {
		return newUsageError("mate crew spawn: --budget wants a positive number of tokens")
	}
	req := spawn.SpawnCrewRequest{Project: fs.Arg(0), Crew: fs.Arg(1), Task: *taskFlag, Scout: *scoutFlag, Repo: *repoFlag, Delivery: *deliverFlag, Budget: *budgetFlag, Size: *sizeFlag, Lane: *laneFlag}
	if *briefFlag == "-" {
		text, err := spawn.ReadBriefStdin(stdin)
		if err != nil {
			return err
		}
		req.BriefText = text
	} else {
		req.BriefFile = *briefFlag
	}
	if *harnessFlag != "" {
		kind, err := harnesses.Parse(*harnessFlag)
		if err != nil {
			return &usageError{err}
		}
		req.Harness = kind
	}
	model, err := harness.ParseModel(*modelFlag)
	if err != nil {
		return &usageError{err}
	}
	effort, err := harness.ParseEffort(*effortFlag)
	if err != nil {
		return &usageError{err}
	}
	req.Model, req.Effort = model, effort
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	req, note, err := applyDispatch(w, req)
	if err != nil {
		return err
	}
	if note != "" {
		fmt.Fprintf(stderr, "note: %s\n", note)
	}
	slot, rotationNote, err := applyRotation(w, req.Project, req.Harness, skip)
	if err != nil {
		return err
	}
	req.RotationSlot = slot
	if rotationNote != "" {
		fmt.Fprintf(stderr, "note: %s\n", rotationNote)
	}
	if warn := c.quotaWarning(req.Harness); warn != "" {
		fmt.Fprintf(stderr, "warning: %s\n", warn)
	}
	if err := laneGuard(w, req); err != nil {
		return err
	}
	res, err := spawn.SpawnCrew(context.Background(), w, liveDeps(w, stderr), req)
	if errors.Is(err, spawn.ErrRepoRefused) {
		// Which repo a crew works in is the caller's argument to get right,
		// so it exits 2 like any other bad argument.
		return &usageError{err}
	}
	if err != nil {
		return err
	}
	writeCrewSpawnReport(stdout, stderr, w, res)
	return nil
}

// writeCrewSpawnReport prints what a successful spawn prints: notes and
// warnings on stderr, the three lines the manual tells the Mate to keep on
// stdout, and, in auto mode, the line telling it to end its turn now
// (auto_turn.go). Kept apart from flag parsing so a test can drive it with
// a result from a fake-runtime spawn.
func writeCrewSpawnReport(stdout, stderr io.Writer, w *store.Workspace, res spawn.CrewResult) {
	if res.StaleMeta {
		fmt.Fprintf(stderr, "note: crew %s had a recorded agent Herdr no longer knew; the stale record was replaced\n", res.Crew)
	}
	if res.UpdateDialog {
		fmt.Fprintf(stderr, "note: skipped the %s release-update prompt; it returns at the next release\n", res.Harness)
	}
	if res.TrustDialog {
		fmt.Fprintf(stderr, "note: answered the %s directory-trust dialog for %s\n", res.Harness, res.Worktree)
	}
	if res.DeliveryWarning != "" {
		fmt.Fprintf(stderr, "warning: %s\nlast pane lines:\n%s\n", res.DeliveryWarning, res.PaneTail)
	}
	if res.EffortOmitted {
		fmt.Fprintf(stderr, "note: %s does not take effort %s; it was recorded and left out of the launch\n", res.Harness, res.Effort)
	}
	fmt.Fprintf(stdout, "spawned %s/%s: agent %s in pane %s (harness %s%s%s, repo %s, branch %s%s, worktree %s)\n",
		res.Project, res.Crew, res.Agent, res.Pane, res.Harness, profileNote(res), budgetNote(res.Budget), res.Repo, res.Branch, deliveryNote(res.Delivery), res.Worktree)
	fmt.Fprintf(stdout, "brief %s\nstatus %s\n", res.BriefPath, res.StatusPath)
	printTurnEnd(stdout, turnSpawnLine(res.Crew))
}

// cmdCrewList implements `mate crew list <project>`.
func cmdCrewList(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("crew list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate crew list <project> [--all] [--workspace <dir>]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	allFlag := fs.Bool("all", false, "include wait-mate and closed crews")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return newUsageError("mate crew list: want exactly 1 argument: <project>")
	}
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	all, err := spawn.ListCrews(w, fs.Arg(0))
	if err != nil {
		return err
	}
	crews := all
	closed := 0
	waitMate := 0
	if !*allFlag {
		crews = crews[:0:0]
		for _, c := range all {
			if c.Closed {
				closed++
				continue
			}
			if c.State == string(crewstate.StateWaitMate) {
				waitMate++
				continue
			}
			crews = append(crews, c)
		}
	}
	if len(crews) == 0 {
		switch {
		case waitMate > 0 && closed > 0:
			fmt.Fprintf(stdout, "no active crews for %s (%d wait-mate, %d closed; --all lists them)\n", fs.Arg(0), waitMate, closed)
		case closed > 0:
			fmt.Fprintf(stdout, "no open crews for %s (%d closed; --all lists them)\n", fs.Arg(0), closed)
		case waitMate > 0:
			fmt.Fprintf(stdout, "no active crews for %s (%d wait-mate; --all lists them)\n", fs.Arg(0), waitMate)
		default:
			fmt.Fprintf(stdout, "no crews recorded for %s\n", fs.Arg(0))
		}
		return nil
	}
	// STATE is the app's word for the crew (mvp.md section 4b); NOTE is the
	// crew's own last line. Two columns, because they answer two questions:
	// a `wait-mate` crew with the note "could not build without a db" is not
	// the same row as one that says "ready in branch mate/k3".
	tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tHARNESS\tBRANCH\tSTATE\tNOTE\tPANE")
	for _, c := range crews {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			c.Crew, harnessCell(c), c.Branch, dashIfEmpty(c.State), dashIfEmpty(c.Note), dashIfEmpty(c.Pane))
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if closed > 0 {
		fmt.Fprintf(stdout, "(%d closed crew(s) not shown; --all lists them)\n", closed)
	}
	if waitMate > 0 {
		fmt.Fprintf(stdout, "(%d wait-mate crew(s) not shown; --all lists them)\n", waitMate)
	}
	return nil
}

// dashIfEmpty keeps a table cell from reading as a missing column.
func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// cmdCrewStop implements `mate crew stop <project> <id> [--discard]`. It
// refuses up front when the branch carries unlanded work and --discard was
// not given, and otherwise stops the agent, closes the tab, tears down the
// worktree and branch, and records the crew as finished or failed
// (task 16, mvp.md section 4b).
func (c cli) cmdCrewStop(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("crew stop", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate crew stop <project> <id> [--discard] [--workspace <dir>]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	discardFlag := fs.Bool("discard", false, "remove the worktree and branch even if the branch is unlanded or the worktree is dirty")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return newUsageError("mate crew stop: want exactly 2 arguments: <project> <id>")
	}
	w, err := resolveStopWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	project, crew := fs.Arg(0), fs.Arg(1)
	res, err := spawn.StopCrew(context.Background(), w, liveDeps(w, stderr), project, crew, *discardFlag)
	if err != nil {
		// Including ErrUnlandedWork, which is now a refusal that changed
		// nothing: there is no outcome to report, only the reason.
		return err
	}
	writeCrewStopReport(stdout, project, crew, res, c.caller())
	if !res.AlreadyClosed {
		writeLearningUsageLine(stdout, context.Background(), w, project, crew, "crew stop")
	}
	return nil
}

// cmdCrewRelaunch implements `mate crew relaunch <project> <id> [--note <text>]`.
// `restart` is accepted as the same verb. It is the recovery path for a
// crew whose Herdr pane or agent is gone: the branch and worktree are kept,
// the recorded harness is started again in a fresh session, and the brief is
// pointed at once more (mvp.md M13, "Herdr chết").
func cmdCrewRelaunch(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("crew relaunch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate crew relaunch <project> <id> [--note \"<one line>\"] [--workspace <dir>]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	noteFlag := fs.String("note", "", "one line of progress to carry into the new agent's first prompt")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return newUsageError("mate crew relaunch: want exactly 2 arguments: <project> <id>")
	}
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	project, crew := fs.Arg(0), fs.Arg(1)
	res, err := spawn.RelaunchCrew(context.Background(), w, liveDeps(w, stderr), project, crew, *noteFlag)
	if err != nil {
		return err
	}
	writeCrewRelaunchReport(stdout, res)
	return nil
}

// writeCrewRelaunchReport prints what a relaunch established: the facts a
// reader needs to attach to the new pane, and the turn-end reminder a Mate
// in auto mode needs (auto_turn.go). A delivery that could not be confirmed
// is a warning, never a failure - the agent is real and the line can be
// re-sent.
func writeCrewRelaunchReport(stdout io.Writer, res spawn.RelaunchResult) {
	old := "no live agent was recorded"
	if res.Stopped {
		old = "the previous agent was stopped"
	}
	fmt.Fprintf(stdout, "relaunched %s/%s: agent %s in pane %s (harness %s%s, repo %s, branch %s, worktree %s; %s)\n",
		res.Project, res.Crew, res.Agent, res.Pane, res.Harness, relaunchProfileNote(res), res.Repo, res.Branch, res.Worktree, old)
	if res.DeliveryWarning != "" {
		fmt.Fprintf(stdout, "warning: %s\nlast pane lines:\n%s\n", res.DeliveryWarning, res.PaneTail)
	}
	fmt.Fprintf(stdout, "brief %s\n", res.BriefPath)
	if res.InputPath != "" {
		fmt.Fprintf(stdout, "input %s\n", res.InputPath)
	}
	printTurnEnd(stdout, turnRelaunchLine(res.Crew))
}

// relaunchProfileNote is profileNote's small sibling: the model and effort
// the relaunch launched with, read back from the crew's own meta.
func relaunchProfileNote(res spawn.RelaunchResult) string {
	out := ""
	if res.Model != "" {
		out += ", model " + res.Model
	}
	if res.Effort != "" {
		out += ", effort " + string(res.Effort)
	}
	return out
}

// profileNote is ", model m, effort e" for the axes a spawn set.
// deliveryNote names a pull request delivery in the spawn line; the local
// one is the default and goes unsaid.
func deliveryNote(delivery string) string {
	if delivery == crewstate.DeliveryPR {
		return ", delivers a pull request"
	}
	return ""
}

// budgetNote is ", budget 2M tokens" when the crew was recorded with one.
func budgetNote(budget int64) string {
	if budget <= 0 {
		return ""
	}
	return ", budget " + query.HumanizeTokens(budget) + " tokens"
}

func profileNote(res spawn.CrewResult) string {
	out := ""
	if res.Model != "" {
		out += ", model " + res.Model
	}
	if res.Effort != "" {
		out += ", effort " + string(res.Effort)
	}
	return out
}

// harnessCell is the HARNESS column: the harness, then the model and
// effort it was spawned with when either was set - "codex gpt-5.5/high",
// "codex gpt-5.5", or "codex default/high" when only the effort was.
func harnessCell(c spawn.CrewSummary) string {
	cell := c.Harness
	switch {
	case c.Model != "" && c.Effort != "":
		cell += " " + c.Model + "/" + c.Effort
	case c.Model != "":
		cell += " " + c.Model
	case c.Effort != "":
		cell += " default/" + c.Effort
	}
	return cell
}
