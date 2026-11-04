package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nguyenngocanh94/mate/internal/autopilot"
	"github.com/nguyenngocanh94/mate/internal/box"
	"github.com/nguyenngocanh94/mate/internal/brief"
	"github.com/nguyenngocanh94/mate/internal/crewstate"
	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// cmdWake implements `mate wake <project> [--peek] [--workspace <dir>]` (CON-68): the
// one read a Mate makes when a digest or a `resolve:` line wakes it. On
// hellovietnam (2026-10-06..14) a Mate woke 247 times and spent 6.9 model
// calls per wake, most of them a chain of tail, cat, peek, crew list and
// python run to learn what had happened; each call re-read its ~65k token
// prefix. This prints that in one output:
//
//  1. every open item, in the order the Mate acts on them (blocked,
//     needs-decision, wait-mate - the wake skill's order), each with what
//     deciding it needs: a question in full, a hand-back's branch summary,
//     its `## For the next crew` section and its reviews;
//  2. the status lines written since the Mate's last wake;
//  3. each open Crew's state;
//  4. a repo whose harness friction log is over budget, and the news that
//     asks the captain about its review (CON-200), when there is one.
//
// It reads the same view the digest is built from (internal/box), so a
// wake never disagrees with the digest that caused it. Part 2's cursor is
// its own (`mate/.wake-cursor`), moved by every run but a --peek, so a
// captain who looks at a wake does not take its lines from the Mate's next
// one, nor opens part 4's news; part 1 has none, so an item stays listed until it is dealt with,
// whatever was shown before. It asks Herdr nothing: a pane's health is `mate state`'s, and `mate peek`
// is still how a Mate looks at a blocked Crew.
func cmdWake(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("wake", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate wake <project> [--peek] [--workspace <dir>]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	peek := fs.Bool("peek", false, "print the wake without moving the cursor of part 2 or opening part 4's news")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return newUsageError("mate wake: want exactly 1 argument: <project>")
	}
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	text, err := wake(context.Background(), w, gitx.New(), fs.Arg(0), time.Now(), !*peek)
	if err != nil {
		return err
	}
	_, err = io.WriteString(stdout, text)
	return err
}

// Bounds on one wake, so a busy project still prints a page rather than a
// log. Every count printed is the true one; what is cut is named.
const (
	// wakeNewLines bounds part 2.
	wakeNewLines = 20
	// wakeNoteRunes bounds a quoted status note in parts 2 and 3. Part 1
	// quotes a question in full: it is what the Mate decides on.
	wakeNoteRunes = 120
	// wakeNextCrewLines bounds the `## For the next crew` excerpt; the
	// brief asks for at most 10.
	wakeNextCrewLines = 12
	// wakeTableLines bounds the hand-back's acceptance table, and
	// wakeSectionLines each of its `## Deviations from Build` and `## Still
	// open` sections (CON-193): with them in the wake, review-delivery's
	// check of the hand-back against the brief needs no `cat` of the file.
	wakeTableLines   = 16
	wakeSectionLines = 8
)

func wake(ctx context.Context, w *store.Workspace, git gitx.Git, project string, now time.Time, advance bool) (string, error) {
	if err := requireProject(w, project); err != nil {
		return "", err
	}
	view, err := box.Load(w, project)
	if err != nil {
		return "", err
	}
	crews, err := spawn.ListCrews(w, project)
	if err != nil {
		return "", err
	}
	cursor, err := w.ReadWakeCursor(project)
	if err != nil {
		return "", err
	}

	var out strings.Builder
	fmt.Fprintf(&out, "# mate wake %s · %s\n", project, now.Format(recallTimeLayout+" -0700"))
	out.WriteString("Contract: this is what woke you, read once. Act on part 1 in its order; do not tail status files, cat hand-backs, peek or run crew list to find what is already here. Peek a blocked Crew; read a file below only for what its excerpt does not answer.\n")

	items := wakeItems(autopilot.Gather(view, nil, now))
	landed := landedCrews(crews, w, project)
	fmt.Fprintf(&out, "\n== 1. To act on (%d) ==\n", len(items)+len(landed))
	if len(items)+len(landed) == 0 {
		out.WriteString("nothing open\n")
	}
	reviews := reviewsByTarget(w, project, crews)
	for _, it := range items {
		writeWakeItem(ctx, &out, w, git, project, it, reviews)
	}
	for _, c := range landed {
		writeLanded(ctx, &out, w, git, project, c)
	}

	fresh, next := newStatusLines(view, cursor)
	fmt.Fprintf(&out, "\n== 2. New status since your last wake (%d) ==\n", len(fresh))
	if len(fresh) == 0 {
		out.WriteString("none\n")
	}
	shown := fresh
	if len(shown) > wakeNewLines {
		shown = shown[len(shown)-wakeNewLines:]
		fmt.Fprintf(&out, "(the %d oldest are not shown; they are in the status files under %s)\n", len(fresh)-wakeNewLines, w.CrewsDir(project))
	}
	for _, e := range shown {
		fmt.Fprintf(&out, "%s %s\n", e.Crew, clipRunes(e.Text, wakeNoteRunes))
	}

	var open []spawn.CrewSummary
	for _, c := range crews {
		if !c.Closed {
			open = append(open, c)
		}
	}
	fmt.Fprintf(&out, "\n== 3. Open crews (%d) ==\n", len(open))
	if len(open) == 0 {
		out.WriteString("none\n")
	}
	for _, c := range open {
		profile := dashIfEmpty(c.Harness)
		if c.Model != "" {
			profile += " " + c.Model
		}
		if c.Effort != "" {
			profile += "/" + c.Effort
		}
		fmt.Fprintf(&out, "%s %s · %s · %s\n", c.Crew, c.State, profile, dashIfEmpty(clipRunes(c.Note, wakeNoteRunes)))
	}

	// CON-200: only a repo over its friction budget is named, so a wake
	// with nothing to review stays three parts.
	if lines := frictionCheck(w, project, now, advance); len(lines) > 0 {
		out.WriteString("\n== 4. Harness friction over budget ==\n")
		for _, l := range lines {
			out.WriteString(l + "\n")
		}
	}

	if advance {
		if err := w.WriteWakeCursor(project, next); err != nil {
			return "", err
		}
	}
	return out.String(), nil
}

// wakeItems is a digest's items in the order a Mate works them: blocked
// first (a crew that never took a turn, CON-74, is in that group: it is as
// unable to speak for itself), then questions, then hand-backs, then
// budgets, then context-high items (CON-73: nothing to do until the
// crew's next hand-back), each kind oldest first as Gather gave them.
func wakeItems(items []autopilot.Item) []autopilot.Item {
	rank := map[autopilot.ItemKind]int{
		autopilot.ItemBlocked: 0, autopilot.ItemNoTurn: 0, autopilot.ItemNeedsDecision: 1, autopilot.ItemWaitMate: 2, autopilot.ItemBudget: 3,
		autopilot.ItemContextHigh: 4,
	}
	out := append([]autopilot.Item(nil), items...)
	sort.SliceStable(out, func(i, j int) bool { return rank[out[i].Kind] < rank[out[j].Kind] })
	return out
}

func writeWakeItem(ctx context.Context, out *strings.Builder, w *store.Workspace, git gitx.Git, project string, it autopilot.Item, reviews map[string][]string) {
	crew := dashIfEmpty(it.Crew)
	switch it.Kind {
	case autopilot.ItemBlocked:
		kind := dashIfEmpty(string(it.IncidentKind))
		fmt.Fprintf(out, "blocked %s: %s, open for %s - %s\n", crew, kind, formatAge(it.QuietFor), it.Text)
		fmt.Fprintf(out, "  next: mate state, then mate peek, then skill crew-recovery\n")
	case autopilot.ItemNoTurn:
		// The observer's text is "no turn in the ledger <d> after launch".
		// The crew is not blocked, it never started: look once, and
		// relaunch it if the pane shows nothing or a sign-in screen.
		fmt.Fprintf(out, "no turn %s: %s, open for %s\n", crew, it.Text, formatAge(it.QuietFor))
		fmt.Fprintf(out, "  next: mate peek %s; if the pane is empty or at a sign-in screen, mate crew relaunch %s\n", crew, crew)
	case autopilot.ItemBudget:
		// The observer's text is "<used> tokens of <budget> tokens"; the
		// half-budget one (CON-76) carries box.BudgetHalfPrefix and the
		// reason it was filed. The next lines are the handling: at half,
		// one look, and a line if the Crew is looping or polling; at the
		// budget the app has asked the Crew for a clean stop and the Mate
		// decides at its hand-back; at twice it the app has stopped it
		// (CON-94).
		switch percent, rest := box.BudgetThreshold(it.Text); percent {
		case 50:
			fmt.Fprintf(out, "budget 50%% %s: %s\n", crew, rest)
			fmt.Fprintf(out, "  next: mate peek %s once; if it is looping or polling, send it one line\n", crew)
			return
		case 200:
			// CON-94: the app stopped it at twice its budget, keeping
			// unlanded work.
			fmt.Fprintf(out, "budget hard ceiling %s: %s\n", crew, rest)
			fmt.Fprintf(out, "  next: skill wake, Budget; the app stopped the Crew and kept any unlanded work - extend and relaunch only with the captain, else narrow or drop it\n")
			return
		}
		fmt.Fprintf(out, "over budget %s: %s\n", crew, it.Text)
		fmt.Fprintf(out, "  next: skill wake, Budget; the app told the Crew to commit, hand back and stop - at its hand-back extend it once (mate crew budget %s %s --extend <tokens> --reason \"<why>\"), narrow it, or drop it\n", project, crew)
	case autopilot.ItemContextHigh:
		// CON-73: the crew is working on a bloated context. Nothing is
		// cut mid-turn; the fix round goes into a fresh session, which
		// `mate send` opens by itself once the crew has handed back.
		fmt.Fprintf(out, "context high %s: %s\n", crew, it.Text)
		fmt.Fprintf(out, "  next: do not cut it mid-turn; at its next hand-back open a fresh session - mate send does that for a resting crew, or mate crew relaunch %s %s --note \"<what it already tried>\"\n", project, crew)
	case autopilot.ItemNeedsDecision:
		fmt.Fprintf(out, "needs-decision %s: %s\n", crew, it.Text)
		writeDecides(out, w, project, it.Crew, it.Text)
	case autopilot.ItemWaitMate:
		fmt.Fprintf(out, "wait-mate %s: %s\n", crew, it.Text)
		writeHandBack(ctx, out, w, git, project, it.Crew, reviews[it.Crew])
	}
}

// writeDecides says whose call a Crew's question is, from the brief's
// `## Open decisions` (CON-93). On mihu 2026-10-14 a Mate decided a
// `decides: captain` question because the line that woke it said "decide":
// the rule was in its skill, the word was in the tool output it read. So the
// tool output carries the rule: the matching entry's decider, or that none
// matched and how many entries there are to read.
func writeDecides(out *strings.Builder, w *store.Workspace, project, crew, question string) {
	var decisions []brief.OpenDecision
	if data, err := os.ReadFile(w.CrewBrief(project, crew)); err == nil {
		decisions = brief.ParseOpenDecisions(string(data))
	}
	d, ok := brief.MatchDecision(decisions, question)
	if !ok || d.Decides == "" {
		fmt.Fprintf(out, "  decides: unknown (open decisions: %d)\n", len(decisions))
		return
	}
	fmt.Fprintf(out, "  decides: %s (%s)\n", d.Decides, clipRunes(d.Text, wakeNoteRunes))
	if d.Decides == brief.Deciders[0] {
		out.WriteString("  next: ask the captain as news; send the Crew nothing until they answer\n")
	}
}

// writeHandBack is what reviewing a hand-back starts from: the branch it
// left, the file it wrote with that file's `## For the next crew` section,
// and the reviews already recorded for it. A review Crew's own hand-back
// is its verdict.
func writeHandBack(ctx context.Context, out *strings.Builder, w *store.Workspace, git gitx.Git, project, crew string, reviewers []string) {
	meta, err := w.ReadCrewMeta(project, crew)
	if err != nil || len(meta) == 0 {
		return
	}
	scout := meta[spawn.MetaKind] == "scout"
	if target, ok := reviewTarget(w, project, crew); ok {
		fmt.Fprintf(out, "  review of %s: verdict %s (report %s)\n", target, reviewVerdictWord(w, project, crew), w.CrewReport(project, crew))
		return
	}
	if !scout {
		fmt.Fprintf(out, "  branch %s\n", branchSummary(ctx, w, git, project, meta))
	}
	file := w.CrewHandback(project, crew)
	if scout {
		file = w.CrewReport(project, crew)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintf(out, "  %s: not written\n", file)
	} else {
		lines := strings.Count(string(data), "\n")
		section := markdownSection(string(data), "For the next crew", wakeNextCrewLines)
		if section == "" {
			fmt.Fprintf(out, "  %s (%d lines), no ## For the next crew section\n", file, lines)
		} else {
			fmt.Fprintf(out, "  %s (%d lines), ## For the next crew:\n", file, lines)
			for _, l := range strings.Split(section, "\n") {
				fmt.Fprintf(out, "    %s\n", l)
			}
		}
		if !scout {
			writeHandBackChecks(out, string(data))
		}
	}
	if len(reviewers) == 0 {
		if !scout {
			out.WriteString("  reviews: none yet; " + projectReviewWord(w, project) + "\n")
		}
	} else {
		var parts []string
		for _, r := range reviewers {
			parts = append(parts, r+" "+reviewVerdictWord(w, project, r))
		}
		fmt.Fprintf(out, "  reviews: %s\n", strings.Join(parts, ", "))
	}
	fmt.Fprintf(out, "  learning: %s\n", learningWord(ctx, w, git, project, crew, meta, "review-delivery's next step"))
}

// writeHandBackChecks prints what review-delivery checks a ship's hand-back
// for, after `## For the next crew`: the acceptance table (one row per
// criterion, pass or fail, with the command and output that show it),
// then `## Deviations from Build` and `## Still open`. Each is bounded;
// an empty one is said to be missing, which is itself a finding.
func writeHandBackChecks(out *strings.Builder, data string) {
	rows := markdownTable(data, wakeTableLines)
	if len(rows) == 0 {
		out.WriteString("  acceptance table: none\n")
	} else {
		out.WriteString("  acceptance table:\n")
		for _, r := range rows {
			fmt.Fprintf(out, "    %s\n", r)
		}
	}
	for _, title := range []string{"Deviations from Build", "Still open"} {
		section := markdownSection(data, title, wakeSectionLines)
		if section == "" {
			fmt.Fprintf(out, "  ## %s: missing\n", title)
			continue
		}
		fmt.Fprintf(out, "  ## %s:\n", title)
		for _, l := range strings.Split(section, "\n") {
			fmt.Fprintf(out, "    %s\n", l)
		}
	}
}

// markdownTable is the first table of a markdown text: its contiguous
// `|` lines, at most max of them, the cut marked. The hand-back's
// acceptance table follows `## For the next crew` with no heading of its
// own (assets/crew/brief.md.tmpl).
func markdownTable(text string, max int) []string {
	var rows []string
	in := false
	cut := 0
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "|") {
			if in {
				break
			}
			continue
		}
		in = true
		if len(rows) >= max {
			cut++
			continue
		}
		rows = append(rows, t)
	}
	if cut > 0 {
		rows = append(rows, fmt.Sprintf("(+%d more rows in the hand-back)", cut))
	}
	return rows
}

// landedCrews are the open crews whose pull request the watcher saw merged
// (CON-129). Each is a part-1 item until `crew stop` closes it: the merge
// is the one event the learning skill catches up on, and on mihuv2
// (2026-10-27/28) nothing in a wake said whether that had happened.
func landedCrews(crews []spawn.CrewSummary, w *store.Workspace, project string) []spawn.CrewSummary {
	var out []spawn.CrewSummary
	for _, c := range crews {
		if c.Closed {
			continue
		}
		meta, err := w.ReadCrewMeta(project, c.Crew)
		if err != nil || meta[crewstate.MetaPRState] != crewstate.PRStateMerged {
			continue
		}
		out = append(out, c)
	}
	return out
}

func writeLanded(ctx context.Context, out *strings.Builder, w *store.Workspace, git gitx.Git, project string, c spawn.CrewSummary) {
	meta, err := w.ReadCrewMeta(project, c.Crew)
	if err != nil {
		return
	}
	fmt.Fprintf(out, "landed %s: pull request merged (%s), crew still open\n", c.Crew, dashIfEmpty(meta[crewstate.MetaPRURL]))
	fmt.Fprintf(out, "  learning: %s\n", learningWord(ctx, w, git, project, c.Crew, meta, "mate crew stop "+project+" "+c.Crew))
}

// learningWord is whether the Mate's learning record for a crew covers the
// crew's current evidence revision (skill task-learning): `none recorded`,
// `recorded for <rev>`, or recorded but naming no such revision. A ship's
// revision is its merge commit once merged, else its branch head; a scout
// has only its report, so its record is checked for existence alone. The
// record is the Mate's prose, so "covers" means the short revision appears
// in it. then is what follows the task-learning step in the `next:`.
func learningWord(ctx context.Context, w *store.Workspace, git gitx.Git, project, crew string, meta map[string]string, then string) string {
	path := w.CrewLearning(project, crew)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("none recorded; next: skill task-learning (writes %s), then %s", path, then)
	}
	rev := crewRevision(ctx, w, git, project, meta)
	if rev == "" {
		return fmt.Sprintf("recorded (%s); next: %s", path, then)
	}
	if !strings.Contains(string(data), rev) {
		return fmt.Sprintf("recorded, but not for %s (%s); next: skill task-learning for this revision, then %s", rev, path, then)
	}
	return fmt.Sprintf("recorded for %s (%s); next: %s", rev, path, then)
}

// crewRevision is the short revision a ship's evidence is about: the merge
// commit once its pull request merged, else the head of its branch; "" for
// a scout, a crew without a branch, or a branch git cannot resolve.
func crewRevision(ctx context.Context, w *store.Workspace, git gitx.Git, project string, meta map[string]string) string {
	if meta[spawn.MetaKind] == "scout" {
		return ""
	}
	if merge := meta[crewstate.MetaMergeCommit]; meta[crewstate.MetaPRState] == crewstate.PRStateMerged && merge != "" {
		return shortRevision(merge)
	}
	branch := meta[spawn.MetaBranch]
	cfg, err := w.LoadProject(project)
	if err != nil || branch == "" {
		return ""
	}
	repoCfg, err := cfg.CrewRepo(meta)
	if err != nil {
		return ""
	}
	head, err := git.HeadCommit(ctx, w.RepoDir(repoCfg.Path), branch)
	if err != nil {
		return ""
	}
	return shortRevision(head)
}

func shortRevision(rev string) string {
	rev = strings.TrimSpace(rev)
	if len(rev) > 7 {
		return rev[:7]
	}
	return rev
}

// branchSummary is one line on a ship's branch: commits beyond the default
// branch and git's diff summary. A failure is said in the line rather than
// failing the wake: the rest of what woke the Mate still matters.
func branchSummary(ctx context.Context, w *store.Workspace, git gitx.Git, project string, meta map[string]string) string {
	branch := meta[spawn.MetaBranch]
	cfg, err := w.LoadProject(project)
	if err != nil || branch == "" {
		return dashIfEmpty(branch) + ": unknown (no branch or project config)"
	}
	repoCfg, err := cfg.CrewRepo(meta)
	if err != nil {
		return branch + ": unknown (" + oneLineMsg(err) + ")"
	}
	repo := w.RepoDir(repoCfg.Path)
	base := repoCfg.DefaultBranch
	if ok, err := git.BranchExists(ctx, repo, branch); err != nil || !ok {
		return branch + ": no longer exists"
	}
	ahead, err := git.AheadCount(ctx, repo, branch, base)
	if err != nil {
		return branch + ": unknown (" + oneLineMsg(err) + ")"
	}
	if ahead == 0 {
		return fmt.Sprintf("%s: no commits beyond %s", branch, base)
	}
	stat, err := git.Diff(ctx, repo, base, branch, true)
	if err != nil {
		return fmt.Sprintf("%s: %d commit(s) beyond %s", branch, ahead, base)
	}
	lines := strings.Split(strings.TrimSpace(stat), "\n")
	return fmt.Sprintf("%s: %d commit(s) beyond %s;%s", branch, ahead, base, " "+strings.TrimSpace(lines[len(lines)-1]))
}

// projectReviewWord is the project's review setting as the hand-back item
// states it (CON-101), so the Mate knows from the item alone whether to
// start a reviewer, land, or ask the captain.
func projectReviewWord(w *store.Workspace, project string) string {
	cfg, err := w.LoadProject(project)
	if err != nil {
		return "project review unreadable: " + err.Error()
	}
	switch cfg.Review {
	case store.ReviewOn:
		return "project review on: start one"
	case store.ReviewOff:
		return "project review off: land once the hand-back checks out"
	default:
		return "project review undecided: ask the captain, record it with `mate project review " + project + " on|off`"
	}
}

// reviewsByTarget maps each reviewed Crew to its reviewers, oldest first by
// the order ListCrews gave.
func reviewsByTarget(w *store.Workspace, project string, crews []spawn.CrewSummary) map[string][]string {
	out := map[string][]string{}
	for _, c := range crews {
		if target, ok := reviewTarget(w, project, c.Crew); ok {
			out[target] = append(out[target], c.Crew)
		}
	}
	return out
}

// reviewTarget is the Crew a review Crew reviews, from the record `mate
// review` wrote.
func reviewTarget(w *store.Workspace, project, crew string) (string, bool) {
	rec, err := store.ReadMeta(w.CrewReviewMeta(project, crew))
	if err != nil || rec["crew"] == "" {
		return "", false
	}
	return rec["crew"], true
}

// reviewVerdictWord is a review's verdict as its report states it, or
// "pending" before the report has one. `mate review --check` is still the
// gate a merge takes; this only says where the review stands.
func reviewVerdictWord(w *store.Workspace, project, reviewer string) string {
	data, err := os.ReadFile(w.CrewReport(project, reviewer))
	if err != nil {
		return "pending"
	}
	if v := store.ReviewVerdict(string(data)); v != "" {
		return v
	}
	return "pending"
}

// newStatusLines is every status line past the cursor, oldest first, and
// the cursor that marks them read. A first wake, with no cursor, shows only
// each open Crew's latest line rather than every line ever written.
func newStatusLines(v box.View, cursor map[string]int64) ([]box.Entry, map[string]int64) {
	next := make(map[string]int64, len(cursor))
	for k, off := range cursor {
		next[k] = off
	}
	var fresh []box.Entry
	latest := map[string]box.Entry{}
	for _, e := range v.Entries {
		if e.Kind != box.KindStatus {
			continue
		}
		if off, ok := next[e.Ref.File]; !ok || e.Ref.Offset > off {
			next[e.Ref.File] = e.Ref.Offset
		}
		if v.Closed[e.Crew] {
			continue
		}
		if len(cursor) == 0 {
			latest[e.Crew] = e
			continue
		}
		if off, ok := cursor[e.Ref.File]; !ok || e.Ref.Offset > off {
			fresh = append(fresh, e)
		}
	}
	if len(cursor) == 0 {
		for _, e := range v.Entries {
			if l, ok := latest[e.Crew]; ok && l.Seq == e.Seq {
				fresh = append(fresh, e)
			}
		}
	}
	return fresh, next
}

// markdownSection is the body of the `## <title>` section of text, at most
// max lines, or "" when there is none. It ends at the next heading or at a
// table row.
func markdownSection(text, title string, max int) string {
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	in := false
	var lines []string
	cut := 0
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "# ") {
			if in {
				break
			}
			in = strings.TrimSpace(strings.TrimPrefix(line, "## ")) == title
			continue
		}
		if !in {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "|") {
			// A hand-back's acceptance table follows the section with no
			// heading of its own (assets/crew/brief.md.tmpl).
			break
		}
		if len(lines) >= max {
			cut++
			continue
		}
		lines = append(lines, line)
	}
	body := strings.TrimSpace(strings.Join(lines, "\n"))
	if body != "" && cut > 0 {
		body += fmt.Sprintf("\n(+%d more lines in the section)", cut)
	}
	return body
}

// clipRunes cuts s to n runes, marking the cut.
func clipRunes(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}
