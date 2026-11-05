// Package recall builds the text `mate recall <project>` prints (docs/mvp.md
// M8 task 37, B1): the one ordered digest a Mate reads at a session start,
// live state first and memory last. cmd/mate's cmdRecall reads the flags
// and prints what Render returns, and the SessionStart hook prints the same
// digest sized to its harness; cmdRecall's comment says why the parts come
// in their order.
//
// The parts are: 1 the live state (mode, next work, the crews, the inbox,
// the outbox, the open news, the work board), 2 the project facts, 3
// PROJECT.md, 4 backlog.md without Done and its tracker cross-check, 5
// memory.md, 6 WORKSPACE.md, 7 the memory check, and 8, only with mate.db,
// the timeline hints.
//
// The package reads the workspace through internal/store and the timeline
// through internal/timeline/read; it holds no SQL. What another command
// prints - the crews table of `mate backlog`, `mate project facts`, the
// anchor warnings and the report of `mate memory check`, the next-work
// line, a `mate work list` row - comes from the caller in Deps, so the
// digest quotes those commands in their own words.
package recall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/memory"
	"github.com/nguyenngocanh94/mate/internal/names"
	"github.com/nguyenngocanh94/mate/internal/plugin"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/timeline/read"
)

// Deps are what a digest needs from its caller. Every field is required.
type Deps struct {
	// Git is handed to Facts, AnchorWarnings and MemoryCheck.
	Git gitx.Git
	// Tracker runs a tracker plugin's read command for part 4's
	// cross-check.
	Tracker process.Runner
	// Crews writes the crews table of part 1, as `mate backlog <project>`
	// prints it.
	Crews func(w *store.Workspace, project string, now time.Time, out io.Writer) error
	// NextWork is part 1's next-work line, as `mate project next-work`
	// prints it.
	NextWork func(cfg store.ProjectConfig) string
	// WorkLine is one open row of the work board, as `mate work list`
	// prints it, ending in a newline.
	WorkLine func(x matehost.Work) string
	// Facts is part 2, as `mate project facts` prints it, with a repo git
	// cannot read reported in its block's place.
	Facts func(ctx context.Context, w *store.Workspace, git gitx.Git, project string, cfg store.ProjectConfig) []string
	// AnchorWarnings says which PROJECT.md anchors on one repo are behind
	// its default branch, as `mate memory check` does; repo is the
	// repository's directory.
	AnchorWarnings func(ctx context.Context, git gitx.Git, repo string, r store.RepoConfig, anchors []memory.Anchor) ([]string, error)
	// MemoryCheck is what `mate memory check` finds, for part 7.
	MemoryCheck func(ctx context.Context, w *store.Workspace, git gitx.Git, project string, now time.Time) (MemoryReport, error)
}

// MemoryReport is the part of `mate memory check`'s report part 7 prints.
type MemoryReport struct {
	// Measured is whether the check got as far as measuring the budget: a
	// check that fails after that, comparing anchors with a repository,
	// still has its budget and its problems to print.
	Measured bool
	// BudgetLine is the budget line `mate memory check` always prints.
	BudgetLine string
	Problems   []string
	Captain    int
	Lessons    int
	Anchored   int
}

// Options shape one digest.
type Options struct {
	// LiveOnly prints only part 1: a resumed conversation still holds the
	// files, but the crews may have moved while the Mate was stopped.
	LiveOnly bool
	// Parts, when set, are the only parts printed, by number: a Mate that
	// needs one file again, or the parts a cut digest left out, reads those
	// rather than the whole digest a second time (CON-68: a whole recall
	// was 31KB on hellovietnam).
	Parts []int
	// MaxBytes, when positive, cuts the digest from the bottom. Part 1 is
	// always printed whole, whatever it costs.
	MaxBytes int
	// Occasion is what the header says the digest is for, e.g.
	// "session start: compact". Empty for a digest the Mate asked for.
	Occasion string
	// Binary is the mate path the cut notice tells the Mate to run.
	Binary string
}

// NoPartError is a --part that names no part the digest has: the caller's
// usage mistake, not a failure of the digest.
type NoPartError struct{ Parts []int }

func (e *NoPartError) Error() string {
	return "mate recall: this digest has no part " + joinInts(e.Parts)
}

// timeLayout is how every time in the digest is written: the Mate
// compares ages, and a date is part of an age once a project runs for days.
const timeLayout = "2006-01-02 15:04"

// The digest's framing. A part is a `== N. title ==` line; a file inside it
// is framed by begin and end lines naming it, because a file's own `#`
// headings would otherwise read as the digest's.
func partHeading(n int, title string) string { return fmt.Sprintf("\n== %d. %s ==\n", n, title) }

// part is one numbered part of the digest.
type part struct {
	n     int
	title string
	body  string
}

// Render builds the digest of project at now.
func Render(ctx context.Context, w *store.Workspace, deps Deps, project string, now time.Time, opts Options) (string, error) {
	if err := requireProject(w, project); err != nil {
		return "", err
	}
	cfg, err := w.LoadProject(project)
	if err != nil {
		return "", err
	}
	var head strings.Builder
	fmt.Fprintf(&head, "# mate recall %s · %s", project, now.Format(timeLayout+" -0700"))
	if opts.Occasion != "" {
		fmt.Fprintf(&head, " · %s", opts.Occasion)
	}
	head.WriteString("\n")
	if len(opts.Parts) > 0 {
		fmt.Fprintf(&head, "Contract: only the part(s) you asked for (%s); the rest of your digest stands as you read it.\n", joinInts(opts.Parts))
	} else if opts.LiveOnly {
		head.WriteString("Contract: live state only. Your conversation was resumed and still holds the files you read before; this is what the crews, the inbox and your outbox are now. Act on it, and reconcile backlog.md against it.\n")
	} else {
		head.WriteString("Contract: read this once; it is your state, in the order you act on it; act on it. Do not re-read these files this session unless a part says ABSENT, looks corrupt, or was cut.\n")
	}

	live, err := livePart(w, deps, project, cfg, now)
	if err != nil {
		return "", err
	}
	parts := []part{live}
	if !opts.LiveOnly {
		parts = append(parts, factsPart(ctx, w, deps, project, cfg))
		projectPart, err := projectDocPart(ctx, w, deps, project, cfg)
		if err != nil {
			return "", err
		}
		backlogPart, err := backlogPart(ctx, w, deps, project)
		if err != nil {
			return "", err
		}
		parts = append(parts, projectPart, backlogPart,
			part{5, "memory.md", framedFile("memory.md", w.MemoryFile(project))},
			workspacePart(w, project))
		checkPart, err := memoryCheckPart(ctx, w, deps, project, now)
		if err != nil {
			return "", err
		}
		parts = append(parts, checkPart)
		if hints, ok := timelinePart(ctx, w, project, now); ok {
			parts = append(parts, hints)
		}
	}
	if len(opts.Parts) > 0 {
		var kept []part
		for _, p := range parts {
			if slices.Contains(opts.Parts, p.n) {
				kept = append(kept, p)
			}
		}
		if len(kept) == 0 {
			return "", &NoPartError{Parts: opts.Parts}
		}
		parts = kept
	}
	return assemble(head.String(), parts, project, opts), nil
}

// ParseParts reads --part: comma-separated part numbers, each at least 1.
func ParseParts(raw string) ([]int, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var out []int
	for _, f := range strings.Split(raw, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil || n < 1 {
			return nil, fmt.Errorf("mate recall: --part wants part numbers like 4 or 3,5, got %q", raw)
		}
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out, nil
}

func joinInts(ns []int) string {
	parts := make([]string, len(ns))
	for i, n := range ns {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ",")
}

// assemble joins the parts, cutting from the bottom to fit opts.MaxBytes:
// part 1 whole, then every later part that fits whole, then as many whole
// lines of the first one that does not, then one line saying what is
// missing and how to get it.
func assemble(head string, parts []part, project string, opts Options) string {
	var out strings.Builder
	out.WriteString(head)
	out.WriteString(partHeading(parts[0].n, parts[0].title))
	out.WriteString(parts[0].body)
	if opts.MaxBytes <= 0 {
		for _, p := range parts[1:] {
			out.WriteString(partHeading(p.n, p.title))
			out.WriteString(p.body)
		}
		return out.String()
	}
	// Room for the notice itself, which names every missing part.
	const reserve = 420
	budget := opts.MaxBytes - reserve
	var missing []string
	var missingParts []int
	for i, p := range parts[1:] {
		block := partHeading(p.n, p.title) + p.body
		if out.Len()+len(block) <= budget {
			out.WriteString(block)
			continue
		}
		// The first part that does not fit: its whole lines while they do,
		// but only if at least one line of its body goes with its heading.
		lines := strings.SplitAfter(block, "\n")
		kept, size := 0, out.Len()
		for _, l := range lines {
			if size+len(l) > budget {
				break
			}
			size += len(l)
			kept++
		}
		// lines[0] is the blank line before the heading, lines[1] the heading.
		if kept > 2 {
			for _, l := range lines[:kept] {
				out.WriteString(l)
			}
			missing = append(missing, fmt.Sprintf("the rest of part %d (%s)", p.n, p.title))
		} else {
			missing = append(missing, fmt.Sprintf("part %d (%s)", p.n, p.title))
		}
		missingParts = append(missingParts, p.n)
		for _, q := range parts[i+2:] {
			missing = append(missing, fmt.Sprintf("part %d (%s)", q.n, q.title))
			missingParts = append(missingParts, q.n)
		}
		break
	}
	if len(missing) == 0 {
		return out.String()
	}
	bin := opts.Binary
	if bin == "" {
		bin = "mate"
	}
	fmt.Fprintf(&out, "\nrecall cut to fit %d bytes; not shown: %s. Run `%s recall %s --part %s` once before acting on anything those parts cover.\n",
		opts.MaxBytes, strings.Join(missing, ", "), bin, project, joinInts(missingParts))
	return out.String()
}

// livePart is part 1: the mode, the next-work setting, the crews, the
// inbox, the outbox, the open news and the work board.
func livePart(w *store.Workspace, deps Deps, project string, cfg store.ProjectConfig, now time.Time) (part, error) {
	var b strings.Builder
	mode := "manual"
	if w.Auto(project) {
		mode = "auto"
	}
	fmt.Fprintf(&b, "mode: %s\n", mode)
	b.WriteString(deps.NextWork(cfg) + "\n")

	fmt.Fprintf(&b, "crews (mate backlog %s):\n", project)
	if err := deps.Crews(w, project, now, &b); err != nil {
		return part{}, err
	}

	box := query.LoadBox(w, project)
	switch {
	case !box.IsKnown():
		fmt.Fprintf(&b, "inbox: unreadable (%s)\n", box.Reason)
	case len(box.Value.Inbox) == 0:
		b.WriteString("inbox: nothing unresolved\n")
	default:
		fmt.Fprintf(&b, "inbox: %d unresolved\n", len(box.Value.Inbox))
		for _, e := range box.Value.Inbox {
			b.WriteString(inboxLine(w, project, e))
		}
	}

	items, err := w.ReadOutbox(project)
	if err != nil {
		return part{}, err
	}
	var queued []store.OutboxItem
	for _, item := range items {
		if item.Queued() {
			queued = append(queued, item)
		}
	}
	if len(queued) == 0 {
		b.WriteString("outbox: nothing queued for your composer\n")
	} else {
		fmt.Fprintf(&b, "outbox: %d queued for your composer (the app types each when it is empty)\n", len(queued))
		for _, item := range queued {
			fmt.Fprintf(&b, "- %s %s: %s\n", item.At.Local().Format(timeLayout), item.Source, Clip(item.Text, 200))
		}
	}
	b.WriteString(newsLines(w, project))
	b.WriteString(workLines(w, deps, project))
	return part{1, "Live state", b.String()}, nil
}

// workLines lists the work board's open rows (CON-158): what the captain
// asked for and what the Mate proposed, where each stands, so a restarted
// Mate keeps reporting on the same rows instead of adding them again.
func workLines(w *store.Workspace, deps Deps, project string) string {
	list, err := matehost.LoadWork(matehost.WorkPath(w.MateDir(project)))
	if err != nil {
		return fmt.Sprintf("work: unreadable (%v)\n", err)
	}
	var open []matehost.Work
	for _, x := range list {
		if x.Open() {
			open = append(open, x)
		}
	}
	if len(open) == 0 {
		return "work: nothing on the board\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "work: %d on the board (mate work list %s)\n", len(open), project)
	for _, x := range open {
		b.WriteString("- " + deps.WorkLine(x))
	}
	return b.String()
}

// newsLines lists the open news: the questions the Mate put to the
// captain and still holds (CON-136), each with its question and the
// captain's last answer, so a restarted Mate knows what it asked.
func newsLines(w *store.Workspace, project string) string {
	list, err := matehost.LoadNews(matehost.NewsPath(w.MateDir(project)))
	if err != nil {
		return fmt.Sprintf("news: unreadable (%v)\n", err)
	}
	var open []matehost.News
	for _, n := range list {
		if !n.Closed() {
			open = append(open, n)
		}
	}
	if len(open) == 0 {
		return "news: none open\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "news: %d open (mate news list %s)\n", len(open), project)
	for _, n := range open {
		state := "waits on the captain"
		if !n.Owed() {
			state = "answered, yours to act on"
		}
		fmt.Fprintf(&b, "- #%d %s, %s: %s\n", n.ID, n.OpenedAt.Local().Format(timeLayout), state, Clip(n.Title, 200))
		if n.Question != "" {
			fmt.Fprintf(&b, "  asked: %s\n", Clip(n.Question, 300))
		}
		if n.Answer != "" {
			fmt.Fprintf(&b, "  captain: %s\n", Clip(n.Answer, 300))
		}
	}
	return b.String()
}

// inboxLine is one unresolved item: when, who, what it needs, what it
// said, where to read it, and what [assign] has done with it.
func inboxLine(w *store.Workspace, project string, e query.BoxEntry) string {
	who := e.Crew
	if who == "" {
		who = "-"
	}
	need := e.Verb
	where := ""
	switch e.Kind {
	case query.BoxIncident:
		switch e.Verb {
		case "stale":
			need = "stuck, quiet too long"
		case "runtime_lost":
			need = "agent gone"
		case "wedged":
			need = "send wedged"
		case "budget":
			need = "over budget"
			if e.BudgetHalf() {
				need = "half the budget, no hand-back"
			}
		case "no_turn":
			need = "no turn yet"
		case "context_high":
			need = "context high"
		default:
			need = "incident " + e.Verb
		}
	case query.BoxStatus:
		if e.Verb == "needs-decision" || e.Verb == "blocked" {
			need = "needs an answer"
		}
		if e.Crew != "" {
			where = " (" + relToProject(w, project, w.CrewStatus(project, e.Crew)) + ")"
		}
	}
	line := fmt.Sprintf("- %s %s %s", e.At.Local().Format(timeLayout), who, need)
	if e.Text != "" {
		line += ": " + strconv.Quote(Clip(e.Text, 200))
	}
	line += where
	switch e.Assigned.State {
	case query.BoxAssignQueued:
		line += " · assigned, queued"
	case query.BoxAssignSent:
		line += " · assigned " + e.Assigned.SentAt.Local().Format("15:04")
	}
	return line + "\n"
}

// factsPart is part 2. A git failure is reported in place: the rest of the
// digest is still worth reading.
func factsPart(ctx context.Context, w *store.Workspace, deps Deps, project string, cfg store.ProjectConfig) part {
	title := "Project facts (mate project facts " + project + ")"
	lines := deps.Facts(ctx, w, deps.Git, project, cfg)
	return part{2, title, strings.Join(lines, "\n") + "\n"}
}

// projectDocPart is part 3: PROJECT.md, and which of its anchors their own
// repo's default branch has moved past.
func projectDocPart(ctx context.Context, w *store.Workspace, deps Deps, project string, cfg store.ProjectConfig) (part, error) {
	body := framedFile(memory.ProjectFileName, w.ProjectDoc(project))
	doc, err := readOptional(w.ProjectDoc(project))
	if err != nil {
		return part{}, err
	}
	anchors, _ := memory.CheckProject(doc, anchorRepos(cfg))
	for _, r := range cfg.Repos {
		warnings, err := deps.AnchorWarnings(ctx, deps.Git, w.RepoDir(r.Path), r, anchorsIn(anchors, r.Name))
		if err != nil {
			warnings = []string{fmt.Sprintf("warning: could not compare %s's anchors with %s:%s: %v", memory.ProjectFileName, r.Name, r.DefaultBranch, err)}
		}
		for _, line := range warnings {
			body += line + "\n"
		}
	}
	return part{3, memory.ProjectFileName, body}, nil
}

// framedFile prints one file between begin and end lines, or says ABSENT or
// (empty): a missing file and an empty one mean different things (the
// first was never created, or was deleted; the second exists and says
// nothing), so they never print alike. A file holding only its headings
// says so too, because a seeded file is how every file starts.
func framedFile(name, path string) string {
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return fmt.Sprintf("----- %s: ABSENT (%s) -----\n", name, path)
	case err != nil:
		return fmt.Sprintf("----- %s: unreadable (%v) -----\n", name, err)
	case len(bytes.TrimSpace(data)) == 0:
		return fmt.Sprintf("----- %s: (empty) (%s) -----\n", name, path)
	}
	return framed(name, path, string(data), "")
}

func framed(name, path, text, endNote string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "----- begin %s (%s) -----\n", name, path)
	b.WriteString(text)
	if !strings.HasSuffix(text, "\n") {
		b.WriteString("\n")
	}
	end := "----- end " + name
	if endNote != "" {
		end += " (" + endNote + ")"
	}
	b.WriteString(end + " -----\n")
	if headingsOnly(text) {
		fmt.Fprintf(&b, "note: %s holds its headings and nothing else yet\n", name)
	}
	return b.String()
}

// headingsOnly reports a file whose every non-blank line is a heading or an
// HTML comment: a seed nobody has written into.
func headingsOnly(text string) bool {
	for _, l := range strings.Split(text, "\n") {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "#") || (strings.HasPrefix(t, "<!--") && strings.HasSuffix(t, "-->")) {
			continue
		}
		return false
	}
	return true
}

// backlogPart is part 4: backlog.md without its Done section, the In
// flight ids that do not match the live table, and, when the project has a
// tracker plugin, what the tracker says about each In flight pointer.
func backlogPart(ctx context.Context, w *store.Workspace, deps Deps, project string) (part, error) {
	const title = "backlog.md, without Done"
	path := w.BacklogFile(project)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return part{4, title, fmt.Sprintf("----- backlog.md: ABSENT (%s) -----\n", path)}, nil
	}
	if err != nil {
		return part{}, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return part{4, title, fmt.Sprintf("----- backlog.md: (empty) (%s) -----\n", path)}, nil
	}
	kept, done, inFlight := SplitBacklog(string(data))
	note := ""
	if done > 0 {
		note = fmt.Sprintf("## %s: %d entr%s not shown", memory.BacklogDone, done, plural(done, "y", "ies"))
	}
	var body strings.Builder
	body.WriteString(framed("backlog.md", path, kept, note))

	summaries, err := spawn.ListCrews(w, project)
	if err != nil {
		return part{}, err
	}
	open := map[string]bool{}
	var openOrder []string
	for _, s := range summaries {
		if !s.Closed {
			open[s.Crew] = true
			openOrder = append(openOrder, s.Crew)
		}
	}
	listed := map[string]bool{}
	var stale []string
	for _, id := range inFlight {
		listed[id] = true
		if !open[id] {
			stale = append(stale, id)
		}
	}
	var missing []string
	for _, id := range openOrder {
		if !listed[id] {
			missing = append(missing, id)
		}
	}
	if len(stale) > 0 {
		fmt.Fprintf(&body, "In flight in backlog.md but not an open crew in part 1: %s\n", strings.Join(stale, ", "))
	}
	if len(missing) > 0 {
		fmt.Fprintf(&body, "Open crews in part 1 with no `- <id>` line under In flight: %s\n", strings.Join(missing, ", "))
	}
	if len(stale) == 0 && len(missing) == 0 {
		body.WriteString("In flight matches the open crews in part 1.\n")
	}
	if hasSection(kept, memory.BacklogHeld) {
		fmt.Fprintf(&body, "## %s is retired: open a news for each question in it the captain has not answered, move each promise to ## %s, drop the rest, then delete the section.\n", memory.BacklogHeld, memory.BacklogQueued)
	}
	body.WriteString(trackerLines(ctx, w, deps.Tracker, project, kept, inFlight))
	return part{4, title, body.String()}, nil
}

// Bounds on the tracker cross-check: it runs at session start, inside the
// SessionStart hook's own cap, so it must end whether or not the tracker
// answers.
const (
	trackerReadEach  = 3 * time.Second
	trackerReadTotal = 10 * time.Second
	trackerReadMax   = 20
)

// trackerPointer is the structural match of an In flight pointer line,
// `- <crew-id>: <tracker-id>` and nothing else on the line.
var trackerPointer = regexp.MustCompile(`^- (` + names.CrewPattern + `): (\S+)$`)

// inFlightPointers maps each In flight pointer line's crew id to its
// tracker id, in file order.
func inFlightPointers(text string) (ids []string, pointers map[string]string) {
	pointers = map[string]string{}
	section := ""
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimRight(line, "\r")
		if strings.HasPrefix(trimmed, "## ") {
			section = strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
		} else if strings.HasPrefix(trimmed, "# ") {
			section = ""
		}
		if section != memory.BacklogInFlight {
			continue
		}
		if m := trackerPointer.FindStringSubmatch(trimmed); m != nil {
			ids = append(ids, m[1])
			pointers[m[1]] = m[2]
		}
	}
	return ids, pointers
}

// trackerLines is part 4's tracker cross-check (CON-77): nothing when the
// project has no tracker plugin; otherwise one line per In flight pointer
// with what the tracker's read command says about its id, the In flight
// entries that are not pointers (content backlog.md should no longer
// hold), and one line instead when the plugin has no read command.
func trackerLines(ctx context.Context, w *store.Workspace, runner process.Runner, project, backlog string, inFlight []string) string {
	cfg, err := plugin.ReadConfig(w.Root())
	if err != nil {
		return fmt.Sprintf("tracker: %s unreadable (%v); In flight was not cross-checked\n", plugin.ConfigFile, err)
	}
	in, ok := cfg.TrackerFor(project)
	if !ok {
		return ""
	}
	ids, pointers := inFlightPointers(backlog)
	var b strings.Builder
	var plain []string
	for _, id := range inFlight {
		if _, ok := pointers[id]; !ok {
			plain = append(plain, id)
		}
	}
	if len(plain) > 0 {
		fmt.Fprintf(&b, "In flight entries that are not a `- <id>: <tracker-id>` pointer, with %s holding the tasks: %s\n", in.ID, strings.Join(plain, ", "))
	}
	if len(in.Tracker.Read) == 0 {
		fmt.Fprintf(&b, "tracker %s: no read command (tracker.read in %s), so %d In flight pointer(s) were not cross-checked\n", in.ID, plugin.ConfigFile, len(ids))
		return b.String()
	}
	if len(ids) == 0 {
		fmt.Fprintf(&b, "tracker %s: no In flight pointers to cross-check\n", in.ID)
		return b.String()
	}
	fmt.Fprintf(&b, "tracker %s (%s):\n", in.ID, in.Tracker.Read[0])
	ctx, cancel := context.WithTimeout(ctx, trackerReadTotal)
	defer cancel()
	for i, id := range ids {
		if i == trackerReadMax {
			fmt.Fprintf(&b, "- %d more pointer(s) not read; the cross-check stops at %d\n", len(ids)-i, trackerReadMax)
			break
		}
		fmt.Fprintf(&b, "- %s: %s · %s\n", id, pointers[id], trackerRead(ctx, runner, in, pointers[id]))
	}
	return b.String()
}

// trackerRead runs the instance's read command for one tracker id and
// returns the one line part 4 prints: the first line of its stdout, or why
// there is none. It never fails the digest.
func trackerRead(ctx context.Context, runner process.Runner, in plugin.Instance, trackerID string) string {
	argv := make([]string, 0, len(in.Tracker.Read)+1)
	substituted := false
	for _, a := range in.Tracker.Read {
		if strings.Contains(a, "{id}") {
			substituted = true
		}
		argv = append(argv, strings.ReplaceAll(a, "{id}", trackerID))
	}
	if !substituted {
		argv = append(argv, trackerID)
	}
	ctx, cancel := context.WithTimeout(ctx, trackerReadEach)
	defer cancel()
	res, err := runner.Run(ctx, process.Spec{Name: argv[0], Args: argv[1:], Dir: in.Path})
	if err != nil {
		return fmt.Sprintf("unreadable: %v", err)
	}
	if res.ExitCode != 0 {
		line := firstLine(string(res.Stderr))
		if line == "" {
			line = firstLine(string(res.Stdout))
		}
		if line == "" {
			return fmt.Sprintf("unknown to the tracker (exit %d)", res.ExitCode)
		}
		return fmt.Sprintf("unknown to the tracker (exit %d: %s)", res.ExitCode, Clip(line, 160))
	}
	line := firstLine(string(res.Stdout))
	if line == "" {
		return "(no output)"
	}
	return Clip(line, 160)
}

// firstLine is the first non-blank line of s, trimmed.
func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(l); t != "" {
			return t
		}
	}
	return ""
}

// inFlightID is the structural match of an In flight entry: `- <id>` where
// <id> is a crew id, followed by the end of the line, a space, a colon, a
// comma or a parenthesis. What the rest of the line says is not read.
var inFlightID = regexp.MustCompile(`^- (` + names.CrewPattern + `)(?:$|[\s:,(])`)

// SplitBacklog returns backlog.md without its Done section, how many
// entries that section held, and the ids the In flight section names, in
// file order. `mate checkpoint` reads the ids the same way, so an open
// crew a checkpoint accepts is one recall finds.
func SplitBacklog(text string) (kept string, done int, inFlight []string) {
	var b strings.Builder
	section := ""
	for _, line := range strings.SplitAfter(text, "\n") {
		trimmed := strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(trimmed, "## ") {
			section = strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
		} else if strings.HasPrefix(trimmed, "# ") {
			section = ""
		}
		if isDoneSection(section) {
			if strings.HasPrefix(trimmed, "- ") {
				done++
			}
			continue
		}
		if section == memory.BacklogInFlight {
			if m := inFlightID.FindStringSubmatch(trimmed); m != nil {
				inFlight = append(inFlight, m[1])
			}
		}
		b.WriteString(line)
	}
	return b.String(), done, inFlight
}

// hasSection reports whether text has the `## <name>` heading.
func hasSection(text, name string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "## "+name {
			return true
		}
	}
	return false
}

// isDoneSection matches `## Done` and the seeded `## Done (10 most recent)`.
func isDoneSection(section string) bool {
	return section == memory.BacklogDone || strings.HasPrefix(section, memory.BacklogDone+" ")
}

// workspacePart is part 6: the captain's rules for every Mate, then one
// pointer line to the two crew-rule files the app appends to every brief.
func workspacePart(w *store.Workspace, project string) part {
	body := framedFile("WORKSPACE.md", w.WorkspaceDoc())
	body += fmt.Sprintf("Crew rules, appended by the app to every brief and not shown here (never copy them into a brief): %s (%s), %s (%s)\n",
		w.WorkspaceCrewDoc(), crewDocState(w.WorkspaceCrewDoc()), w.ProjectCrewDoc(project), crewDocState(w.ProjectCrewDoc(project)))
	return part{6, "WORKSPACE.md", body}
}

func crewDocState(path string) string {
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "ABSENT"
	case err != nil:
		return "unreadable"
	case strings.TrimSpace(store.StripComments(string(data))) == "":
		return "no rules yet"
	}
	return "has rules"
}

// memoryCheckPart is part 7: what `mate memory check` says, minus the
// anchor warnings part 3 already carries.
func memoryCheckPart(ctx context.Context, w *store.Workspace, deps Deps, project string, now time.Time) (part, error) {
	title := "Memory check (mate memory check " + project + ")"
	// The memory check compares anchors with the repository last, so a
	// failure there still leaves the budget and the shape problems filled
	// in; part 3 already says the comparison failed.
	rep, err := deps.MemoryCheck(ctx, w, deps.Git, project, now)
	if err != nil && !rep.Measured {
		return part{7, title, fmt.Sprintf("memory check failed: %v\n", err)}, nil
	}
	var b strings.Builder
	b.WriteString(rep.BudgetLine + "\n")
	for _, p := range rep.Problems {
		b.WriteString(p + "\n")
	}
	if len(rep.Problems) == 0 {
		fmt.Fprintf(&b, "memory ok: %d captain entr%s, %d lesson(s), %d anchored %s line(s)\n",
			rep.Captain, plural(rep.Captain, "y", "ies"), rep.Lessons, rep.Anchored, memory.ProjectFileName)
	} else {
		fmt.Fprintf(&b, "%d problem(s): load skill stow and fix them before the files grow further\n", len(rep.Problems))
	}
	return part{7, title, b.String()}, nil
}

// Part 8's bounds.
const (
	hintDays  = 14
	hintLimit = 10
)

// timelinePart is part 8, present only when `mate.db` exists: what the
// timeline saw that might be worth remembering, and how full the Mate's
// context is. Hints only: the database is derived (docs/mvp.md decision 6)
// and holds no memory, so nothing here is ever a record.
func timelinePart(ctx context.Context, w *store.Workspace, project string, now time.Time) (part, bool) {
	path, err := db.Path(w)
	if err != nil {
		return part{}, false
	}
	if _, err := os.Stat(path); err != nil {
		return part{}, false
	}
	const title = "Timeline hints (mate.db: hints only, never memory)"
	d, err := db.OpenReadPath(path)
	if err != nil {
		return part{8, title, fmt.Sprintf("timeline unreadable: %v\n", err)}, true
	}
	defer d.Close()
	var b strings.Builder
	since := db.FormatTime(now.Add(-hintDays * 24 * time.Hour))
	if err := timelineHints(ctx, d.SQL(), project, since, &b); err != nil {
		b.WriteString(fmt.Sprintf("timeline query failed: %v\n", err))
	}
	b.WriteString("Nothing above is memory: decide yourself whether any of it is a lesson worth filing (skill stow).\n")
	return part{8, title, b.String()}, true
}

func timelineHints(ctx context.Context, q read.Querier, project, since string, b *strings.Builder) error {
	pct, err := read.MateContextPct(ctx, q, project)
	switch {
	case err != nil:
		return err
	case !pct.Valid:
		b.WriteString("Mate context: not measured yet\n")
	default:
		fmt.Fprintf(b, "Mate context: %.0f%% of the window after your latest turn\n", pct.Float64)
	}

	sent, err := read.SentToCrewsSince(ctx, q, project, since, hintLimit)
	if err != nil {
		return err
	}
	fmt.Fprintf(b, "lines you sent crews after their spawn, last %d days: %d\n", hintDays, len(sent))
	for _, r := range sent {
		fmt.Fprintf(b, "- %s to %s: %s\n", hintTime(r.At), r.Crew, strconv.Quote(Clip(r.Text, 160)))
	}

	asked, err := read.AskedSince(ctx, q, project, since, hintLimit)
	if err != nil {
		return err
	}
	fmt.Fprintf(b, "questions crews asked, last %d days: %d\n", hintDays, len(asked))
	for _, r := range asked {
		state := "answered"
		if r.AnsweredAt == "" {
			state = "unanswered"
		}
		fmt.Fprintf(b, "- %s %s (%s): %s\n", hintTime(r.AskedAt), r.Crew, state, strconv.Quote(Clip(r.Text, 160)))
	}
	return nil
}

// hintTime is a stored time as the local clock the hints print.
func hintTime(at string) string {
	return db.ParseTime(at).Local().Format(timeLayout)
}

// Clip flattens s to one line and bounds it to n runes, the way the digest
// quotes a line.
func Clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

// relToProject writes path relative to the project directory when it is
// inside it, the way every source in the Mate's memory is written.
func relToProject(w *store.Workspace, project, path string) string {
	rel, err := filepath.Rel(w.ProjectDir(project), path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return filepath.ToSlash(rel)
}

// The helpers below are private copies of cmd/mate's, kept here so the
// digest does not depend on cmd/mate.

func requireProject(w *store.Workspace, project string) error {
	if err := store.ValidateProjectName(project); err != nil {
		return err
	}
	if _, ok := w.Project(project); !ok {
		return fmt.Errorf("%w: %s", store.ErrNoProject, project)
	}
	return nil
}

// readOptional reads path, and an absent file as empty.
func readOptional(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(data), err
}

// anchorRepos are the repos a PROJECT.md anchor may name, in the
// project's order.
func anchorRepos(cfg store.ProjectConfig) []memory.Repo {
	out := make([]memory.Repo, 0, len(cfg.Repos))
	for _, r := range cfg.Repos {
		out = append(out, memory.Repo{Name: r.Name, Branch: r.DefaultBranch})
	}
	return out
}

// anchorsIn are the anchors that name repo.
func anchorsIn(anchors []memory.Anchor, repo string) []memory.Anchor {
	var out []memory.Anchor
	for _, a := range anchors {
		if a.Repo == repo {
			out = append(out, a)
		}
	}
	return out
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
