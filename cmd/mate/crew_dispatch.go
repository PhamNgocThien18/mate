package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/dispatch"
	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/quota"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// The crew dispatch table (.mate/crew-dispatch.json, internal/dispatch) is
// firstmate's: natural-language rules the Mate matches with its own
// judgment, each naming a concrete harness, model and effort. The app never
// matches a rule. It checks the table, prints it for the Mate, and fills a
// spawn that named no harness from the table's default. A workspace without
// the file is governed by the built-in table.

// applyDispatch is the spawn's gate on the table. A malformed table stops
// every spawn until it is corrected. A spawn that named its harness keeps
// its profile as given. One that did not gets the table's default profile
// on the workspace's default harness, and note says so; --model or --effort
// without --harness is refused, since a model name belongs to one harness.
func applyDispatch(w *store.Workspace, req spawn.SpawnCrewRequest) (spawn.SpawnCrewRequest, string, error) {
	t, err := dispatch.Resolve(w.StateDir(), harnesses)
	if errors.Is(err, dispatch.ErrInvalid) {
		// Exit 2 like the bad flags below: both are "fix the table or the
		// invocation and run it again", never a crash.
		return req, "", &usageError{err}
	}
	if err != nil {
		return req, "", err
	}
	if req.Harness != "" {
		return req, "", nil
	}
	if req.Model != "" || req.Effort != "" {
		return req, "", newUsageError("mate crew spawn: --model and --effort need --harness; pick a whole profile from `mate crew dispatch`")
	}
	def, err := defaultCrewHarness(w)
	if err != nil {
		return req, "", err
	}
	p, ok := t.DefaultFor(string(def))
	if !ok {
		return req, "", nil
	}
	req.Harness, req.Model, req.Effort = p.Harness, p.Model, p.Effort
	return req, fmt.Sprintf("no --harness; used the %s default: %s", tableName(t), p.Flags()), nil
}

// defaultCrewHarness is the harness a Crew runs on when nobody picks one:
// the workspace's `crew_harness`, else the registry's default for the role.
func defaultCrewHarness(w *store.Workspace) (harness.Kind, error) {
	if def := w.Defaults().CrewHarness; def != "" {
		return harness.Kind(def), nil
	}
	return harnesses.Default(harness.RoleCrew)
}

// kindList is a repeatable harness flag (`--skip codex --skip pi`). Each
// value must be a registered harness, so a typo is a usage error rather
// than a skip that never matches.
type kindList []harness.Kind

func (l *kindList) String() string {
	names := make([]string, len(*l))
	for i, k := range *l {
		names[i] = string(k)
	}
	return strings.Join(names, ",")
}

func (l *kindList) Set(s string) error {
	k, err := harnesses.Parse(s)
	if err != nil {
		return err
	}
	*l = append(*l, k)
	return nil
}

// rotationConsumed is how many rotation turns the project's spawns have
// taken: the highest `rotation_slot=` among its crews, open or closed
// (`crew stop` keeps the key). Slots are a running count, so the highest
// one is the count even after `--skip` passed over a turn. 0 when no
// spawn has taken one.
func rotationConsumed(w *store.Workspace, project string) (int, error) {
	ids, err := w.CrewIDs(project)
	if err != nil {
		return 0, err
	}
	consumed := 0
	for _, id := range ids {
		meta, err := w.ReadCrewMeta(project, id)
		if err != nil {
			return 0, err
		}
		raw := strings.TrimSpace(meta[spawn.MetaRotationSlot])
		if raw == "" {
			continue
		}
		n, err := strconv.Atoi(raw)
		if err != nil {
			return 0, fmt.Errorf("crew %s: %s=%q is not a number", id, spawn.MetaRotationSlot, raw)
		}
		consumed = max(consumed, n)
	}
	return consumed, nil
}

// rotationView is what `crew dispatch` knows about the rotation's position:
// whether a project was named (without one there is no position to read),
// the turns its spawns have taken, and the harnesses to skip.
type rotationView struct {
	known    bool
	consumed int
	skip     []harness.Kind
}

// writeRotation prints one rotation and its next turn, the lines the Mate
// reads before choosing a harness (CON-77).
func writeRotation(out io.Writer, indent string, r dispatch.Rotation, v rotationView) {
	fmt.Fprintf(out, "%srotation: %s\n", indent, r)
	if !v.known {
		fmt.Fprintf(out, "%snext in rotation: unknown; pass the project (mate crew dispatch <project>) to read the position from its spawns\n", indent)
		return
	}
	step, skipped, ok := r.Next(v.consumed, v.skip)
	for _, k := range skipped {
		fmt.Fprintf(out, "%sskipped %s: --skip\n", indent, k)
	}
	if !ok {
		fmt.Fprintf(out, "%snext in rotation: none (every harness in the rotation is skipped)\n", indent)
		return
	}
	fmt.Fprintf(out, "%snext in rotation: %s (position %d/%d)\n", indent, step.Harness, step.Position, len(r))
}

// applyRotation is the spawn's half of the rotation: the slot this spawn
// records, or 0 and a note when it records none. A spawn takes a turn when
// its harness is what a rotation in force names next (after `--skip`,
// which the Mate passes here as it did to `crew dispatch`); the first
// such rotation, rule order then the table's, is the one consumed. An
// explicit harness that is not the next leaves the position where it was,
// because the captain's words win over the table, and the note says so.
// A table without a rotation has nothing to say.
func applyRotation(w *store.Workspace, project string, kind harness.Kind, skip []harness.Kind) (int, string, error) {
	t, err := dispatch.Resolve(w.StateDir(), harnesses)
	if err != nil {
		return 0, "", err
	}
	rotations := t.Rotations()
	if len(rotations) == 0 {
		return 0, "", nil
	}
	if kind == "" {
		if kind, err = defaultCrewHarness(w); err != nil {
			return 0, "", err
		}
	}
	consumed, err := rotationConsumed(w, project)
	if err != nil {
		return 0, "", err
	}
	why := ""
	for _, r := range rotations {
		step, _, ok := r.Next(consumed, skip)
		if ok && step.Harness == kind {
			return step.Slot, "", nil
		}
		if why != "" {
			continue
		}
		if ok {
			why = fmt.Sprintf("%s is next (position %d/%d)", step.Harness, step.Position, len(r))
		} else {
			why = "every harness in it is skipped"
		}
	}
	return 0, fmt.Sprintf("rotation not advanced: harness %s is not the rotation's next, %s; the captain's words win", kind, why), nil
}

// tableName is how output names the table in force.
func tableName(t dispatch.Table) string {
	if t.BuiltIn {
		return "built-in dispatch table's"
	}
	return "dispatch table's (" + t.Path + ")"
}

// cmdCrewDispatch implements
// `mate crew dispatch [<project>] [--skip <harness>]... [--workspace <dir>] [--example]`.
// The project is what the rotation's position is read from (its spawns'
// `rotation_slot=`); without one the table still prints, and a rotation
// says its position is unknown.
func (c cli) cmdCrewDispatch(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("crew dispatch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate crew dispatch [<project>] [--skip <harness>]... [--workspace <dir>] [--example]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	exampleFlag := fs.Bool("example", false, "print the built-in table as JSON, to copy into .mate/crew-dispatch.json and edit")
	var skip kindList
	fs.Var(&skip, "skip", "a harness to pass over in the rotation, such as one a perishable lesson says fails to start (repeatable)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() > 1 {
		fs.Usage()
		return newUsageError("mate crew dispatch: takes at most one argument: <project>")
	}
	if *exampleFlag {
		_, err := io.WriteString(stdout, dispatch.BuiltInJSON)
		return err
	}
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	t, err := dispatch.Resolve(w.StateDir(), harnesses)
	if err != nil {
		return err
	}
	view := rotationView{skip: skip}
	if fs.NArg() == 1 {
		project := fs.Arg(0)
		if err := requireProject(w, project); err != nil {
			return err
		}
		if view.consumed, err = rotationConsumed(w, project); err != nil {
			return err
		}
		view.known = true
	}
	writeDispatchTable(stdout, t, view)
	snap, qerr := c.readQuota(context.Background())
	writeQuota(stdout, snap, qerr)
	rows, err := laneRows(w)
	if err != nil {
		return err
	}
	writeLanes(stdout, rows)
	return nil
}

// writeQuota prints the quota evidence for choosing between a rule's
// alternatives (skill new-task). No quota-axi, or an
// unreadable one, is said in one line and never fails the command.
func writeQuota(out io.Writer, snap quota.Snapshot, err error) {
	switch {
	case errors.Is(err, quota.ErrNotInstalled):
		fmt.Fprintln(out, "\nquota: quota-axi is not installed; alternatives are chosen without quota evidence (install: npm install -g quota-axi)")
		return
	case err != nil:
		fmt.Fprintf(out, "\nquota: unreadable (%v); alternatives are chosen without quota evidence\n", err)
		return
	}
	fmt.Fprintf(out, "\nquota (quota-axi %s, read-only, %s):\n", snap.Version, snap.Read.UTC().Format("2006-01-02 15:04 UTC"))
	for _, r := range snap.Readings {
		fmt.Fprintf(out, "  %s\n", r.Line())
	}
	if kind, why, ok := snap.Favoured(); ok {
		fmt.Fprintf(out, "  favours %s: %s\n", kind, why)
	} else {
		fmt.Fprintf(out, "  favours none: %s\n", why)
	}
}

// spawnQuotaBudget bounds the spawn's quota read; past it the spawn goes on
// with no warning.
const spawnQuotaBudget = 5 * time.Second

// quotaWarning is the spawn's one quota check: a harness quota-axi measures
// as exhausted, or at zero, is warned about and still launched, because the
// captain's words may have chosen it; "" when there is nothing to say.
func (c cli) quotaWarning(kind harness.Kind) string {
	// Advisory only, so it gets a short budget: a slow vendor must never
	// hold up a launch for the full dispatch read.
	ctx, cancel := context.WithTimeout(context.Background(), spawnQuotaBudget)
	defer cancel()
	snap, err := c.readQuota(ctx)
	if err != nil {
		return ""
	}
	r, ok := snap.Reading(kind)
	if !ok || r.Eligible() {
		return ""
	}
	return fmt.Sprintf("%s quota is used up (quota-axi: %s); the Crew may stop mid-task - another alternative of its rule avoids that", kind, r.Line())
}

// writeDispatchTable prints the table the way the Mate uses it: each rule's
// condition, then its profiles as the flags to pass, then why, and its own
// rotation when it has one; then the default and review profiles, and the
// table's rotation with the harness it names next.
func writeDispatchTable(out io.Writer, t dispatch.Table, v rotationView) {
	if t.BuiltIn {
		rel := filepath.Join(store.StateDirName, dispatch.FileName)
		fmt.Fprintf(out, "crew dispatch: built-in (to change it: mate crew dispatch --example > %s, then edit)\n", rel)
	} else {
		fmt.Fprintf(out, "crew dispatch: %s\n", t.Path)
	}
	for i, r := range t.Rules {
		fmt.Fprintf(out, "\nrule %d: %s\n", i+1, r.When)
		writeProfiles(out, r.Use, r.Budget)
		if r.Why != "" {
			fmt.Fprintf(out, "  why %s\n", r.Why)
		}
		if r.Rotate != nil {
			writeRotation(out, "  ", r.Rotate, v)
		}
	}
	if len(t.Default) > 0 {
		fmt.Fprintf(out, "\ndefault: %s\n", t.Default[0].Flags())
		for _, p := range t.Default[1:] {
			fmt.Fprintf(out, "  or  %s\n", p.Flags())
		}
	}
	if len(t.Review) > 0 {
		fmt.Fprintf(out, "\nreview (what `mate review` uses when you pass no --model or --effort): %s\n", profileFlags(t.Review[0], t.ReviewBudget))
		for _, p := range t.Review[1:] {
			fmt.Fprintf(out, "  or  %s\n", profileFlags(p, t.ReviewBudget))
		}
	}
	if len(t.ReviewSmall) > 0 {
		fmt.Fprintf(out, "\nreview_small (what `mate review` uses instead when the change is at most %d lines and touches no code file): %s\n", t.ReviewSmallMaxLines, profileFlags(t.ReviewSmall[0], t.ReviewBudget))
		for _, p := range t.ReviewSmall[1:] {
			fmt.Fprintf(out, "  or  %s\n", profileFlags(p, t.ReviewBudget))
		}
	}
	if t.Rotate != nil {
		fmt.Fprintln(out)
		writeRotation(out, "", t.Rotate, v)
	}
}

// reviewStat is the size of the change a review reads, from `git diff
// --numstat` over the reviewed range (CON-74): lines gained plus lost over
// every file, how many files, and which of them look like code
// (dispatch.IsCodePath). nil means the change was not measured.
type reviewStat struct {
	Lines int
	Files int
	Code  []string
}

func newReviewStat(entries []gitx.NumstatEntry) *reviewStat {
	st := &reviewStat{Files: len(entries)}
	for _, e := range entries {
		st.Lines += e.Insertions + e.Deletions
		if dispatch.IsCodePath(e.Path) {
			st.Code = append(st.Code, e.Path)
		}
	}
	return st
}

// small reports whether the change fits the small-change profile: at most
// max changed lines and no code file.
func (st *reviewStat) small(max int) bool {
	return st != nil && st.Lines <= max && len(st.Code) == 0
}

// String is the stat as a note says it: "12 lines in 2 files, no code".
func (st *reviewStat) String() string {
	code := "no code"
	if len(st.Code) == 1 {
		code = "code in " + st.Code[0]
	} else if len(st.Code) > 1 {
		code = fmt.Sprintf("code in %d of them", len(st.Code))
	}
	return fmt.Sprintf("%d line(s) in %d file(s), %s", st.Lines, st.Files, code)
}

// profileFlags is the profile as spawn flags, with the rule's budget on
// the same line (`--budget 2000000`) when the rule names one, so the Mate
// copies a whole line and the crew is held to the size its rule gave it
// (CON-76). A rule without a budget prints none: the project's `budget:`
// block, if any, then applies on its own.
func profileFlags(p dispatch.Profile, budget int64) string {
	if budget <= 0 {
		return p.Flags()
	}
	return p.Flags() + " --budget " + strconv.FormatInt(budget, 10)
}

// applyReviewDispatch is `mate review`'s gate on the table, as
// applyDispatch is a spawn's: a malformed table stops it. A review that
// named a model or an effort keeps its profile as given. One that named
// neither gets the table's review profile, on the harness it named or else
// the workspace's default, and note says so; a table with no review
// profile for that harness leaves the request as it was. Whatever the
// profile, a review that named no --budget is held to the table's
// `review_budget` when there is one (CON-76).
//
// stat is the size of the change (CON-74): a change that is small by the
// table's `review_small_max_lines`, with no code file in it, takes the
// table's `review_small` profile instead when the table has one for the
// harness, and the note says why. nil is a change that was not measured.
func applyReviewDispatch(w *store.Workspace, req spawn.SpawnCrewRequest, stat *reviewStat) (spawn.SpawnCrewRequest, string, error) {
	t, err := dispatch.Resolve(w.StateDir(), harnesses)
	if errors.Is(err, dispatch.ErrInvalid) {
		return req, "", &usageError{err}
	}
	if err != nil {
		return req, "", err
	}
	if req.Budget == 0 {
		req.Budget = t.ReviewBudget
	}
	if req.Model != "" || req.Effort != "" {
		return req, "", nil
	}
	kind, named := string(req.Harness), req.Harness != ""
	if !named {
		if kind = w.Defaults().CrewHarness; kind == "" {
			k, err := harnesses.Default(harness.RoleCrew)
			if err != nil {
				return req, "", err
			}
			kind = string(k)
		}
	}
	which := "review"
	p, ok := dispatch.Profile{}, false
	if stat.small(t.ReviewSmallMaxLines) {
		if p, ok = t.ReviewSmallFor(kind, named); ok {
			which = "review_small"
		}
	}
	if !ok {
		if p, ok = t.ReviewFor(kind, named); !ok {
			return req, "", nil
		}
	}
	note := fmt.Sprintf("no --model or --effort; used the %s %s profile: %s", tableName(t), which, p.Flags())
	if which == "review_small" {
		note = fmt.Sprintf("no --model or --effort; the change is small (%s, at most %d lines), so used the %s review_small profile: %s", stat, t.ReviewSmallMaxLines, tableName(t), p.Flags())
	}
	if string(p.Harness) != kind {
		note = fmt.Sprintf("no --model or --effort, and the %s table has no %s profile for the default harness %s; used %s", tableName(t), which, kind, p.Flags())
	}
	req.Harness, req.Model, req.Effort = p.Harness, p.Model, p.Effort
	return req, note, nil
}

func writeProfiles(out io.Writer, ps []dispatch.Profile, budget int64) {
	for i, p := range ps {
		lead := "  use "
		if i > 0 {
			lead = "  or  "
		}
		fmt.Fprintf(out, "%s%s\n", lead, profileFlags(p, budget))
	}
}
