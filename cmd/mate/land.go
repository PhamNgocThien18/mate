package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/crewstate"
	"github.com/nguyenngocanh94/mate/internal/learnings"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/memory"
	"github.com/nguyenngocanh94/mate/internal/plugin"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/recall"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/usage"
	"github.com/nguyenngocanh94/mate/internal/wake"
)

// cmdLand implements `mate land <project> <crew>` (CON-193): everything
// mechanical that follows the Mate's decision to accept a hand-back, in
// one command. On mihuv2 (2026-10-17..11-04) a hand-back turn cost the Mate
// 8.8 model calls at ~156k context each, and most of them were bookkeeping
// it did by hand after `mate merge`: the learning record, `backlog done`,
// the tracker's state and comment, its screenshots in three steps, then
// five reads to see what to dispatch next. Each call re-read the whole
// context. So the command does the bookkeeping, and the Mate's turn is
// wake, land, brief, spawn.
//
// It lands first - the same merge `mate merge` makes, with the same
// refusals before anything is touched, or `crew stop` when the pull
// request was merged on GitHub already - and only then records: the
// learning record's head from the hand-back's `Learning:` line (never over
// a record the Mate wrote), the backlog entry to Done, the tracker through
// its `tracker.land` command when the captain configured one, a Work row's
// progress with --work. A record step that fails after the merge is
// printed, not raised: the merge is done, and the Mate should hear what is
// left rather than see the command fail. The last line is what new-task
// needs: open crews, lanes, the queue, and the next-work setting.
func cmdLand(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("land", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate land <project> <crew> [--review <reviewer> [--followups <backlog:<text>|<TRACKER-id>>]] [--learning \"<one line>\"] [--work <id>] [--workspace <dir>]")
	}
	reviewFlag := fs.String("review", "", "require a passing review of this exact branch and contract (as mate merge --review)")
	followupsFlag := fs.String("followups", "", "where the review's non-blocking findings were recorded: a backlog:<text> line under ## Queued, or a tracker id")
	learningFlag := fs.String("learning", "", "your own learning line for the record, when the hand-back's Learning: line is not the whole of it")
	workFlag := fs.String("work", "", "a Work row to note the landing on, as progress")
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return newUsageError("mate land: want exactly 2 arguments: <project> <crew>")
	}
	workID := 0
	if *workFlag != "" {
		id, err := landWorkID(*workFlag)
		if err != nil {
			return err
		}
		workID = id
	}
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	project, crew := fs.Arg(0), fs.Arg(1)
	if err := requireProject(w, project); err != nil {
		return err
	}
	ctx := context.Background()
	meta, err := w.ReadCrewMeta(project, crew)
	if err != nil {
		return err
	}
	if len(meta) == 0 {
		return newUsageErrorf("mate land: %s has no crew %s", project, crew)
	}

	// 1. Land. A pull request the watcher already saw merged is closed the
	// way review-delivery closes it, with crew stop; everything else is
	// the merge, refusals included.
	var followups, landed string
	if meta[crewstate.MetaPRState] == crewstate.PRStateMerged {
		landed = meta[crewstate.MetaMergeCommit]
		res, err := spawn.StopCrew(ctx, w, liveDeps(w, stderr), project, crew, false)
		if err != nil {
			return err
		}
		writeCrewStopReport(stdout, project, crew, res, spawn.CallerFromEnv())
		if !res.AlreadyClosed {
			fmt.Fprintln(stdout, usage.RecordAtClose(ctx, w, project, crew, "mate land"))
		}
	} else {
		res, f, err := mergeReviewed(ctx, w, project, crew, *reviewFlag, *followupsFlag)
		if err != nil {
			return err
		}
		followups, landed = f, res.After
		fmt.Fprintln(stdout, res.Line())
		fmt.Fprintln(stdout, usage.RecordAtClose(ctx, w, project, crew, "mate land"))
		if err := recordFollowups(stdout, w, project, crew, followups); err != nil {
			fmt.Fprintf(stdout, "followups: not recorded: %v\n", err)
		}
	}
	// The meta changed under the land: read it again for the record.
	if m, err := w.ReadCrewMeta(project, crew); err == nil && len(m) > 0 {
		meta = m
	}

	// 2. The learning record's head, from the hand-back.
	handback := landReadFile(w.CrewHandback(project, crew))
	fmt.Fprintln(stdout, landLearning(w, project, crew, meta, landed, handback, *learningFlag, time.Now()))

	// 3. Backlog.
	fmt.Fprintln(stdout, landBacklog(w, project, crew))

	// 4. Tracker.
	trackerID := landTrackerID(w, project, crew, meta)
	fmt.Fprintln(stdout, landTracker(ctx, w, project, crew, trackerID, meta, handback))

	// 5. Work.
	if workID > 0 {
		path := matehost.WorkPath(w.MateDir(project))
		if err := matehost.ProgressWork(path, workID, crew+" landed", time.Now()); err != nil {
			fmt.Fprintf(stdout, "work: #%d not noted: %v\n", workID, err)
		} else {
			fmt.Fprintf(stdout, "work: #%d progress noted (%s landed)\n", workID, crew)
		}
	}

	// 6. Predictions the brief carried and the record did not close: the
	// one judgment task-learning leaves to the Mate, named so it is not
	// forgotten rather than refused, since the merge is done.
	if open := landOpenPredictions(w, project, crew); len(open) > 0 {
		fmt.Fprintf(stdout, "predictions: %d from the brief still open (%s); close each with mate learnings close %s <crew> <id> %s --evidence \"...\"\n",
			len(open), strings.Join(open, ", "), project, strings.Join(learnings.Outcomes, "|"))
	}

	// 7. A landing is what grows a repo's friction log (CON-200).
	for _, l := range frictionCheck(w, project, time.Now(), true) {
		fmt.Fprintln(stdout, l)
	}

	// 8. What next-work needs.
	fmt.Fprintln(stdout, landNext(w, project))
	return nil
}

// landWorkID is workID with the command's own prefix in the usage error.
func landWorkID(s string) (int, error) {
	id, err := workID(s)
	if err != nil {
		return 0, newUsageErrorf("mate land: --work %v", err)
	}
	return id, nil
}

func landReadFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

// landLearningPending is how the app opens a record it wrote the usage
// section into before the Mate wrote anything (usage.RecordAtClose).
const landLearningPending = "Learning: pending"

// learningLine is a hand-back's `Learning:` line, bulleted or not,
// anywhere in the file (the brief template asks for it under `## For the
// next crew`).
var learningLine = regexp.MustCompile(`(?mi)^\s*(?:[-*]\s*)?Learning:\s*(.+?)\s*$`)

// landLearning writes the head of the crew's learning record from the
// hand-back: source, the crew's `Learning:` line (or the Mate's --learning),
// and an empty `## Predictions`. It keeps whatever `## ` sections the record
// already has - `## Usage (app)` above all - and never touches a record the
// Mate wrote itself: only a missing record or one still at
// `Learning: pending` is written.
func landLearning(w *store.Workspace, project, crew string, meta map[string]string, landed, handback, own string, now time.Time) string {
	path := w.CrewLearning(project, crew)
	existing := landReadFile(path)
	if existing != "" && !strings.Contains(existing, landLearningPending) {
		return fmt.Sprintf("learning: record kept as you wrote it (%s)", path)
	}
	crewLine := ""
	if m := learningLine.FindStringSubmatch(handback); m != nil {
		crewLine = strings.TrimSuffix(strings.TrimSpace(m[1]), ".")
	}
	line, note := crewLine, "from the crew's hand-back; the Mate recorded no finding of its own"
	if own = strings.TrimSpace(own); own != "" {
		line, note = own, "the Mate's line; the crew's hand-back said: "+dashIfEmpty(crewLine)
	} else if crewLine == "" {
		line, note = "none", "the hand-back carried no Learning: line"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s learning\n\n", crew)
	fmt.Fprintf(&b, "Source: %s; %s", dashIfEmpty(meta[spawn.MetaTask]), landRevision(meta, landed))
	if url := meta[crewstate.MetaPRURL]; url != "" {
		fmt.Fprintf(&b, "; pull request %s", url)
	}
	fmt.Fprintf(&b, "; reviews: %s; observed %s (mate land).\n", landReviews(w, project, crew), now.Format("2006-01-02 15:04"))
	b.WriteString("Metrics: see ## Usage (app).\n\n")
	fmt.Fprintf(&b, "Learning: %s\n- %s\n\n## Predictions\nnone\n", line, note)
	// Keep every section the record already has, in order; the head
	// replaces only what stood before the first one.
	if existing != "" {
		if i := strings.Index(existing, "\n## "); i >= 0 {
			rest := strings.TrimSpace(existing[i+1:])
			if rest != "" {
				b.WriteString("\n" + rest + "\n")
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Sprintf("learning: not written: %v", err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Sprintf("learning: not written: %v", err)
	}
	return fmt.Sprintf("learning: %s written from the hand-back (Learning: %s)", path, line)
}

// landRevision is the delivered revision as the record names it: the
// commit the landing put on the default branch (the fast-forward's tip, or
// the pull request's merge commit), else the branch alone.
func landRevision(meta map[string]string, landed string) string {
	branch := dashIfEmpty(meta[spawn.MetaBranch])
	if landed != "" {
		return fmt.Sprintf("branch %s merged as %s", branch, wake.ShortRevision(landed))
	}
	return "branch " + branch
}

// landReviews names the reviews recorded for the crew, or why there is
// none.
func landReviews(w *store.Workspace, project, crew string) string {
	crews, err := spawn.ListCrews(w, project)
	if err != nil {
		return "unknown"
	}
	reviewers := wake.ReviewsByTarget(w, project, crews)[crew]
	if len(reviewers) == 0 {
		return "none (" + wake.ProjectReviewWord(w, project) + ")"
	}
	var parts []string
	for _, r := range reviewers {
		parts = append(parts, r+" "+wake.ReviewVerdictWord(w, project, r))
	}
	return strings.Join(parts, ", ")
}

// landBacklog moves the crew's backlog entry to Done, when it has one. A
// backlog without the entry is not an error: not every task was promised
// there.
func landBacklog(w *store.Workspace, project, crew string) string {
	data := landReadFile(w.BacklogFile(project))
	if !backlogHasEntry(data, crew) {
		return fmt.Sprintf("backlog: no entry %s", crew)
	}
	if err := w.EditBacklog(project, "done", crew, memory.BacklogDone, ""); err != nil {
		return fmt.Sprintf("backlog: %s not moved: %v", crew, err)
	}
	return fmt.Sprintf("backlog: %s → %s", crew, memory.BacklogDone)
}

// backlogHasEntry is whether a backlog line starts an entry for id, under
// any section, so a move is tried only where it can succeed.
func backlogHasEntry(text, id string) bool {
	prefix := "- " + id + ":"
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			return true
		}
	}
	return false
}

// trackerIDPrefix is a task line that opens with the tracker's id, as
// crews named by the issue carry it (CON-89): `HU-402: ...`.
var trackerIDPrefix = regexp.MustCompile(`^\s*([A-Z]+-[0-9]+)\b`)

// landTrackerID is the tracker's id for the crew's task: the `task=` line's
// leading id, else the backlog pointer `- <crew>: <ID>`. Empty when
// neither names one.
func landTrackerID(w *store.Workspace, project, crew string, meta map[string]string) string {
	if m := trackerIDPrefix.FindStringSubmatch(meta[spawn.MetaTask]); m != nil {
		return m[1]
	}
	prefix := "- " + crew + ":"
	for _, line := range strings.Split(landReadFile(w.BacklogFile(project)), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, prefix))
		if trackerID.MatchString(rest) {
			return rest
		}
	}
	return ""
}

// landTrackerTimeout bounds the tracker's land command: it talks to a
// service, and the Mate's turn is waiting.
const landTrackerTimeout = 60 * time.Second

// landTracker runs the tracker plugin's `land` command for the task, with
// the task's facts in its environment, and returns the one line printed
// for it. Without a tracker, an id or a command it says so and leaves the
// tracker to the Mate: the command cannot reach the MCP server the Mate
// logs in to (docs/plugins.md), and a tracker left silently stale is
// worse than one more call.
func landTracker(ctx context.Context, w *store.Workspace, project, crew, trackerID string, meta map[string]string, handback string) string {
	cfg, err := plugin.ReadConfig(w.Root())
	if err != nil {
		return fmt.Sprintf("tracker: %s unreadable (%v); update the tracker yourself", plugin.ConfigFile, err)
	}
	in, ok := cfg.TrackerFor(project)
	if !ok {
		if trackerID == "" {
			return "tracker: none configured"
		}
		return fmt.Sprintf("tracker: %s not updated (no `tracker:` block in %s); mark it done yourself", trackerID, plugin.ConfigFile)
	}
	if trackerID == "" {
		return fmt.Sprintf("tracker: %s, but no id for %s (none leads task=, no `- %s: <ID>` pointer in backlog.md); update it yourself if the task has one", in.ID, crew, crew)
	}
	if len(in.Tracker.Land) == 0 {
		return fmt.Sprintf("tracker: %s not updated (%s has no tracker.land command for %s); mark it done yourself", trackerID, plugin.ConfigFile, in.ID)
	}
	argv := make([]string, 0, len(in.Tracker.Land)+1)
	substituted := false
	for _, a := range in.Tracker.Land {
		if strings.Contains(a, "{id}") {
			substituted = true
		}
		argv = append(argv, strings.ReplaceAll(a, "{id}", trackerID))
	}
	if !substituted {
		argv = append(argv, trackerID)
	}
	env := append(os.Environ(),
		"MATE_PROJECT="+project,
		"MATE_CREW="+crew,
		"MATE_TRACKER_ID="+trackerID,
		"MATE_TASK="+meta[spawn.MetaTask],
		"MATE_BRANCH="+meta[spawn.MetaBranch],
		"MATE_MERGE_COMMIT="+meta[crewstate.MetaMergeCommit],
		"MATE_PR_URL="+meta[crewstate.MetaPRURL],
		"MATE_HANDBACK="+w.CrewHandback(project, crew),
		"MATE_BRIEF="+w.CrewBrief(project, crew),
		"MATE_LEARNING="+w.CrewLearning(project, crew),
		"MATE_SHOTS="+strings.Join(handbackShots(handback), "\n"),
	)
	ctx, cancel := context.WithTimeout(ctx, landTrackerTimeout)
	defer cancel()
	res, err := trackerRunner.Run(ctx, process.Spec{Name: argv[0], Args: argv[1:], Dir: in.Path, Env: env})
	if err != nil {
		return fmt.Sprintf("tracker: %s not updated (%s failed: %v); update it yourself", trackerID, argv[0], err)
	}
	if res.ExitCode != 0 {
		line := firstLine(string(res.Stderr))
		if line == "" {
			line = firstLine(string(res.Stdout))
		}
		return fmt.Sprintf("tracker: %s not updated (%s exit %d: %s); update it yourself", trackerID, argv[0], res.ExitCode, recall.Clip(line, 160))
	}
	line := firstLine(string(res.Stdout))
	if line == "" {
		line = "done"
	}
	return fmt.Sprintf("tracker: %s %s (%s)", trackerID, recall.Clip(line, 200), in.ID)
}

// trackerRunner runs the tracker's land command; a test may replace it.
// `mate recall` reads the tracker through recall.Deps instead.
var trackerRunner process.Runner = process.ExecRunner{}

// firstLine is the first non-blank line of s, trimmed.
func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			return t
		}
	}
	return ""
}

// shotPath is an absolute path to an image a hand-back mentions, as
// `make shot` and the hand-back template leave them.
var shotPath = regexp.MustCompile(`(/[^\s"'` + "`" + `|<>()]+\.(?i:png|jpe?g|webp))`)

// handbackShots is every image the hand-back names that exists on disk, in
// order, once each: what the tracker's land command may attach.
func handbackShots(handback string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range shotPath.FindAllStringSubmatch(handback, -1) {
		p := m[1]
		if seen[p] {
			continue
		}
		seen[p] = true
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

// briefPrediction is a `Prediction <crew> <id>:` line new-task puts in a
// brief for task-learning to close.
var briefPrediction = regexp.MustCompile(`(?m)^\s*(?:[-*]\s*)?Prediction\s+(\S+)\s+#?(\S+?):`)

// landOpenPredictions is each prediction the crew's brief carried that is
// still open in its record, as `<crew> <id>`.
func landOpenPredictions(w *store.Workspace, project, crew string) []string {
	brief := landReadFile(w.CrewBrief(project, crew))
	matches := briefPrediction.FindAllStringSubmatch(brief, -1)
	if len(matches) == 0 {
		return nil
	}
	open, err := learnings.Open(w, project)
	if err != nil {
		return nil
	}
	isOpen := map[string]bool{}
	for _, p := range open {
		isOpen[p.Crew+" "+p.ID] = true
	}
	var out []string
	for _, m := range matches {
		key := m[1] + " " + m[2]
		if isOpen[key] {
			out = append(out, key)
		}
	}
	return out
}

// landNext is the one line new-task needs after a landing: the crews
// still open, each lane's load, how much is queued, and the project's
// next-work setting. It replaces the reads a Mate made to learn the same
// before dispatching (crew list, lanes, backlog, project next-work).
func landNext(w *store.Workspace, project string) string {
	var parts []string
	crews, err := spawn.ListCrews(w, project)
	if err == nil {
		var open []string
		for _, c := range crews {
			if !c.Closed {
				open = append(open, c.Crew+" ("+c.State+")")
			}
		}
		if len(open) == 0 {
			parts = append(parts, "no crew open")
		} else {
			parts = append(parts, fmt.Sprintf("%d crew(s) open: %s", len(open), strings.Join(open, ", ")))
		}
	}
	if rows, err := laneRows(w); err == nil {
		for _, r := range rows {
			parts = append(parts, fmt.Sprintf("lane %s %d/%d", r.Name, len(r.Holders), r.Cap))
		}
	}
	queued := backlogQueued(landReadFile(w.BacklogFile(project)))
	if len(queued) == 0 {
		parts = append(parts, "queued: none")
	} else {
		parts = append(parts, fmt.Sprintf("queued: %d (first: %s)", len(queued), wake.ClipRunes(strings.TrimPrefix(strings.TrimSpace(queued[0]), "- "), 80)))
	}
	if cfg, err := w.LoadProject(project); err == nil {
		parts = append(parts, nextWorkLine(cfg))
	}
	return "next: " + strings.Join(parts, " · ")
}
