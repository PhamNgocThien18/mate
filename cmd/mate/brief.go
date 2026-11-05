package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/brief"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// cmdBrief dispatches `mate brief <check|append|revise>` (docs/mvp.md M7 task 32).
func cmdBrief(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return newUsageError("usage: mate brief <check|append|revise> ...")
	}
	switch args[0] {
	case "check":
		return cmdBriefCheck(args[1:], stdout, stderr)
	case "append":
		return cmdBriefAppend(args[1:], stdin, stdout, stderr)
	case "revise":
		return cmdBriefRevise(args[1:], stdout, stderr)
	default:
		return newUsageErrorf("unknown brief subcommand %q", args[0])
	}
}

// cmdBriefCheck implements `mate brief check <file> [--scout]`: the same
// shape check `crew spawn` runs, on its own, so the Mate can fix a brief
// before it spawns anything. It prints one line per problem on stderr and
// exits 1 on any; it judges shape, never meaning.
func cmdBriefCheck(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("brief check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate brief check <file> [--scout]")
	}
	scoutFlag := fs.Bool("scout", false, "check it as a scout brief, which needs ## Deliverable")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return newUsageError("mate brief check: want exactly 1 argument: <file>")
	}
	path := fs.Arg(0)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("brief check: %w", err)
	}
	kind := brief.Ship
	if *scoutFlag {
		kind = brief.Scout
	}
	problems := brief.CheckFile(string(data), kind)
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(stderr, p.String())
		}
		return fmt.Errorf("brief check: %d shape problem(s) in %s as a %s brief", len(problems), path, kind)
	}
	fmt.Fprintf(stdout, "brief ok: %s has every section of a %s brief (%s)\n",
		path, kind, strings.Join(brief.Sections(kind), ", "))
	return nil
}

// cmdBriefAppend implements `mate brief append <project> <crew> [-]`: the
// captain's later words, read from stdin, appended verbatim to the end of
// `## Captain's words` in `crews/<id>/brief.md`, and the Crew told in one
// verified line where to read them. It exists because the brief the Crew
// reads lives under `crews/`, where the Mate may not write.
func cmdBriefAppend(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("brief append", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate brief append <project> <crew> [-] [--revision <file>] [--fresh] [--workspace <dir>]   (the captain's words on stdin)")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	revisionPath := fs.String("revision", "", "complete revised task, with existing captain words unchanged; saved together with the new words")
	fresh := fs.Bool("fresh", false, "open a fresh session for the crew with the appended words as the Mate's message, even if it is still working")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if n := fs.NArg(); n < 2 || n > 3 || (n == 3 && fs.Arg(2) != "-") {
		fs.Usage()
		return newUsageError("mate brief append: want <project> <crew> and optionally -, with the captain's words on stdin")
	}
	words, err := spawn.ReadBriefStdin(stdin)
	if err != nil {
		return err
	}
	if strings.TrimSpace(words) == "" {
		return newUsageError("mate brief append: stdin is empty; pipe the captain's words in")
	}
	revision, err := readBriefRevision(*revisionPath)
	if err != nil {
		return err
	}
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	source, err := resolveSendSource("")
	if err != nil {
		return err
	}
	res, err := briefUpdate(context.Background(), w, liveDeps(w, stderr), fs.Arg(0), fs.Arg(1), words, revision, source, time.Now(), *fresh)
	return reportBriefUpdate(stdout, stderr, fs.Arg(0), fs.Arg(1), source, res, err)
}

func reportBriefUpdate(stdout, stderr io.Writer, project, crew, source string, res briefAppendResult, err error) error {
	if res.Revised {
		fmt.Fprintf(stdout, "revised the task in %s\n", res.BriefPath)
	}
	if res.Appended {
		fmt.Fprintf(stdout, "appended the captain's words to %s %s\n", res.BriefPath, res.Added)
	}
	if err != nil {
		if res.Appended || res.Revised {
			printSendRefusalDetails(stderr, err)
			fmt.Fprintf(stderr, "the update is in the brief but the crew was not told; once it is free, run: mate send %s %s %q\n", project, crew, res.Line)
		}
		return err
	}
	if res.Fresh != nil {
		writeFreshSessionReport(stdout, project, crew, res.Verb, source, *res.Fresh)
		return nil
	}
	fmt.Fprintln(stdout, sendSummaryLine(res.Report))
	return nil
}

// briefAppendResult is what briefAppend did, reported even on failure so
// the caller can say that the brief changed while the send did not.
type briefAppendResult struct {
	Appended  bool
	Revised   bool
	BriefPath string
	Added     string
	Line      string
	Report    send.Report
	// Fresh is the fresh session the words opened (CON-73) - on a resting
	// crew, or on any crew with --fresh - and Verb the hand-back verb that
	// made the crew resting ("spec change" with --fresh on a working
	// crew). Nil when the words were typed into the pane.
	Fresh *spawn.RelaunchResult
	Verb  string
}

// briefAppend is brief append's core, separate from flag parsing so a test
// can drive it over a fake runtime. It refuses before touching the brief
// when the crew has no live agent to tell, because words nobody is told
// about are a brief that disagrees with the work in flight.
//
// A crew that has handed back is resting (CON-73): the words are a spec
// change for a fix round, so after the append the crew gets a fresh
// session whose third part is the appended text, not a line in its old
// pane; fresh forces the same on a crew still working.
func briefAppend(ctx context.Context, w *store.Workspace, deps spawn.Deps, project, crew, words, source string, now time.Time, fresh bool) (briefAppendResult, error) {
	return briefUpdate(ctx, w, deps, project, crew, words, "", source, now, fresh)
}

// briefUpdate validates the whole change before saving once, then notifies
// the crew. A failed notification leaves a saved update and a retry pointer.
func briefUpdate(ctx context.Context, w *store.Workspace, deps spawn.Deps, project, crew, words, revision, source string, now time.Time, fresh bool) (briefAppendResult, error) {
	res := briefAppendResult{BriefPath: w.CrewBrief(project, crew)}
	if err := store.ValidateProjectName(project); err != nil {
		return res, err
	}
	if err := store.ValidateCrewID(crew); err != nil {
		return res, err
	}
	resolved, err := resolveCrewHandle(ctx, w, deps, project, crew)
	if err != nil {
		return res, err
	}
	if !resolved.AgentRecorded || !resolved.SessionRunning {
		return res, observability.NewError(observability.CodeStateConflict,
			fmt.Sprintf("crew %s/%s has no live agent to tell; the brief was not changed", project, crew))
	}
	current, err := os.ReadFile(res.BriefPath)
	if err != nil {
		return res, fmt.Errorf("brief append: %w", err)
	}
	meta, err := w.ReadCrewMeta(project, crew)
	if err != nil {
		return res, err
	}
	updated := string(current)
	if revision != "" {
		kind := brief.Ship
		if w.CrewIsScout(project, crew, meta) {
			kind = brief.Scout
		}
		updated, err = brief.ReviseTask(updated, revision, kind)
		if err != nil {
			return res, err
		}
	}
	if words != "" || revision == "" {
		updated, err = brief.AppendCaptainsWords(updated, words, now)
		if err != nil {
			return res, fmt.Errorf("brief append: %s: %w", res.BriefPath, err)
		}
	}
	// Only crews/<id>/brief.md changes. A Codex crew's AGENTS.override.md
	// copy in the worktree is its launch context and stays as launched; the
	// line below points the crew at the brief, which is the record, exactly
	// as the first prompt did (spawn.BriefPrompt).
	if err := w.ReplaceCrewBrief(project, crew, []byte(updated)); err != nil {
		return res, err
	}
	res.Appended, res.Revised = words != "", revision != ""
	res.Added = brief.AddedLine(now)
	res.Line = briefAppendLine(res.BriefPath, res.Added)
	if res.Revised {
		res.Line = fmt.Sprintf("The task specification was revised in %s; reread the whole # Task, including Captain's words, Build, Acceptance and Open decisions, before continuing.", res.BriefPath)
	}
	resting, verb, err := restingCrew(w, project, crew, meta)
	if err != nil {
		return res, err
	}
	if fresh && !resting {
		verb = "spec change"
	}
	if fresh || resting {
		message := strings.TrimSpace(words)
		if res.Revised {
			message = res.Line
		}
		session, err := freshSession(ctx, w, deps, project, crew, message, source)
		if err != nil {
			return res, err
		}
		res.Fresh, res.Verb = &session, verb
		return res, nil
	}
	res.Report, err = sendToCrew(ctx, w, deps, project, crew, res.Line, source, send.Options{})
	return res, err
}

// briefAppendLine is the one line a Crew receives after `brief append`.
func briefAppendLine(briefPath, added string) string {
	return fmt.Sprintf("The captain added to your task: read the words marked %s at the end of ## %s in %s, then carry on with them.",
		added, brief.CaptainsWords, briefPath)
}

// cmdBriefRevise updates the Mate's derived specification without attributing
// the Mate's decision to the captain. Provenance belongs in the task's facts.
func cmdBriefRevise(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("brief revise", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate brief revise <project> <crew> <file> [--fresh] [--workspace <dir>]")
	}
	workspace := fs.String("workspace", "", "workspace directory")
	fresh := fs.Bool("fresh", false, "open a fresh session even if the crew is still working")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 3 {
		fs.Usage()
		return newUsageError("mate brief revise: want <project> <crew> <file>")
	}
	revision, err := readBriefRevision(fs.Arg(2))
	if err != nil {
		return err
	}
	if revision == "" {
		return newUsageError("mate brief revise: revision file is required")
	}
	w, err := resolveWorkspace(*workspace)
	if err != nil {
		return err
	}
	source, err := resolveSendSource("")
	if err != nil {
		return err
	}
	res, err := briefUpdate(context.Background(), w, liveDeps(w, stderr), fs.Arg(0), fs.Arg(1), "", revision, source, time.Now(), *fresh)
	return reportBriefUpdate(stdout, stderr, fs.Arg(0), fs.Arg(1), source, res, err)
}

func readBriefRevision(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("brief revision: %w", err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return "", newUsageError("brief revision: file is empty")
	}
	return string(data), nil
}
