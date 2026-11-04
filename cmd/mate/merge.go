package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/memory"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// cmdMerge implements `mate merge <project> <crew>`: docs/mvp.md task 22.
//
// It carries no `--force`, no `--no-ff` and no `--discard`. Every way a
// merge can go wrong is a refusal that changes nothing and says what to do
// instead, and a flag that turned any of them off would be a flag for
// landing work nobody reviewed.
//
// Who is asking is read from the pane environment rather than from a flag:
// spawn injects MATE_CALLER=mate into a Mate's pane and
// MATE_CALLER=crew into a Crew's, so a Mate cannot claim to be the
// captain by passing a different word on the command line.
func cmdMerge(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("merge", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate merge <project> <crew> [--review <reviewer> [--followups <backlog:<text>|<TRACKER-id>>]] [--workspace <dir>]")
	}
	reviewFlag := fs.String("review", "", "require a passing review of this exact branch and contract")
	followupsFlag := fs.String("followups", "", "where the review's non-blocking findings were recorded: a backlog:<text> line under ## Queued, or a tracker id")
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return newUsageError("mate merge: want exactly 2 arguments: <project> <crew>")
	}
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	project, crew := fs.Arg(0), fs.Arg(1)
	res, followups, err := mergeReviewed(context.Background(), w, project, crew, *reviewFlag, *followupsFlag)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, res.Line())
	// The record is written on every merge (CON-130); the merge's own
	// output stays its one line, and `wake` is where the Mate reads the
	// record's state.
	recordUsageAtClose(context.Background(), w, project, crew, "mate merge")
	if err := recordFollowups(stdout, w, project, crew, followups); err != nil {
		return err
	}
	// A landed task is the moment the Mate decides what runs next (CON-120):
	// the setting rides on the Mate's merge so it is in front of it then.
	if spawn.NormaliseCaller(spawn.CallerFromEnv()) != spawn.CallerMate {
		return nil
	}
	// CON-200: the merge may have grown a repo's friction log.
	for _, l := range frictionCheck(w, project, time.Now(), true) {
		fmt.Fprintln(stdout, l)
	}
	if cfg, err := w.LoadProject(project); err == nil {
		fmt.Fprintln(stdout, nextWorkLine(cfg))
	}
	return nil
}

// mergeReviewed is the merge `mate merge` and `mate land` share: the
// `--followups` reference resolved, the review checked and pinned when one
// is named, then MergeCrew with the caller the pane environment names.
// Every refusal happens before anything is touched, and the resolved
// followups come back for the caller to record on the closed crew.
func mergeReviewed(ctx context.Context, w *store.Workspace, project, crew, review, followupsRef string) (spawn.MergeResult, string, error) {
	followups, err := checkFollowups(w, project, followupsRef)
	if err != nil {
		return spawn.MergeResult{}, "", err
	}
	var reviewed []spawn.ReviewedCommit
	if review != "" {
		fp, _, err := checkReview(ctx, w, gitx.New(), project, crew, review)
		if err != nil {
			return spawn.MergeResult{}, "", err
		}
		if err := requireFollowups(w, project, review, followups); err != nil {
			return spawn.MergeResult{}, "", err
		}
		reviewed = append(reviewed, spawn.ReviewedCommit{Head: fp["head"], Base: fp["base"]})
	}
	res, err := spawn.MergeCrew(ctx, w, baseDeps(), project, crew, spawn.CallerFromEnv(), reviewed...)
	if err != nil {
		// Including the one failure that is not a refusal: a teardown that
		// failed after the fast-forward landed. MergeCrew's error already
		// says the merge is done and the crew is not closed, which is the
		// whole of what the caller needs, so nothing is added here.
		return spawn.MergeResult{}, "", err
	}
	return res, followups, nil
}

// recordFollowups writes a resolved `--followups` on the closed crew's
// meta, after the merge: a refused merge leaves no claim behind that the
// findings were followed up.
func recordFollowups(stdout io.Writer, w *store.Workspace, project, crew, followups string) error {
	if followups == "" {
		return nil
	}
	if err := w.UpdateCrewMeta(project, crew, map[string]string{store.MetaFollowups: followups}); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "followups: %s (recorded as %s= in %s)\n", followups, store.MetaFollowups, w.CrewMeta(project, crew))
	return nil
}

// trackerID is what `--followups` takes besides a backlog line: an issue id
// of the captain's tracker, `HVN-12`, `CON-74`.
var trackerID = regexp.MustCompile(`^[A-Z]+-[0-9]+$`)

// followupsBacklogPrefix marks a `--followups` that points at the
// project's backlog: `backlog:<text>`, where text is part of a line under
// `## Queued` in backlog.md.
const followupsBacklogPrefix = "backlog:"

// checkFollowups validates a `--followups` reference (CON-74) and returns
// it as recorded: "" when none was given, a tracker id, or the backlog
// reference once the line it names is found under `## Queued`. It checks
// the place exists, not what it says: the findings are the Mate's to
// record, the app only refuses a merge that claims a record it cannot find.
func checkFollowups(w *store.Workspace, project, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", nil
	}
	if trackerID.MatchString(ref) {
		return ref, nil
	}
	text, ok := strings.CutPrefix(ref, followupsBacklogPrefix)
	if !ok || strings.TrimSpace(text) == "" {
		return "", newUsageError("mate merge: --followups wants backlog:<text of a line under ## Queued in backlog.md> or a tracker id like HVN-12")
	}
	text = strings.TrimSpace(text)
	data, err := os.ReadFile(w.BacklogFile(project))
	if err != nil {
		return "", fmt.Errorf("mate merge: --followups %s: %w", ref, err)
	}
	for _, line := range backlogQueued(string(data)) {
		if strings.Contains(line, text) {
			return followupsBacklogPrefix + text, nil
		}
	}
	return "", fmt.Errorf("mate merge: --followups %s: no line under ## %s of %s contains %q; record the findings there first (mate backlog add)", ref, memory.BacklogQueued, w.BacklogFile(project), text)
}

// backlogQueued is each line under `## Queued` of a backlog, up to the next
// heading.
func backlogQueued(text string) []string {
	var out []string
	in := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "## ") {
			in = strings.TrimSpace(strings.TrimPrefix(line, "## ")) == memory.BacklogQueued
			continue
		}
		if in && strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

// requireFollowups is the merge's gate on a passing review's findings
// (CON-74): a report whose verdict is pass may still list material
// findings under `## Findings` (or leave a prior one open); those do not
// block the merge, but they must be recorded somewhere before it, or
// nobody follows them up. Without `--followups` the merge is refused with
// the findings printed; with one, which checkFollowups already resolved,
// it goes on.
func requireFollowups(w *store.Workspace, project, reviewer, followups string) error {
	data, err := os.ReadFile(w.CrewReport(project, reviewer))
	if err != nil {
		return err
	}
	findings := store.ReviewFindings(string(data))
	if len(findings) == 0 || followups != "" {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "merge refused: review %s passed but left %d finding(s) standing; record them and name where with --followups backlog:<text of the ## %s line> or --followups <TRACKER-id>, then merge again:", reviewer, len(findings), memory.BacklogQueued)
	for i, f := range findings {
		fmt.Fprintf(&b, "\n  F%d: %s", i+1, strings.Join(strings.Fields(f), " "))
	}
	return newUsageError(b.String())
}
