package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/store"
)

// usageError marks an error as a CLI usage mistake: bad flags, wrong number
// of arguments, an unknown subcommand. mainRun exits 2 for these and 1 for
// everything else.
type usageError struct{ err error }

func (e *usageError) Error() string { return e.err.Error() }
func (e *usageError) Unwrap() error { return e.err }

func newUsageError(msg string) error { return &usageError{errors.New(msg)} }

func newUsageErrorf(format string, a ...any) error { return &usageError{fmt.Errorf(format, a...)} }

// command is one row of the top-level command table. The table is the only
// place a top-level command is named: run picks the handler from it, and the
// usage text is printed from it, so the two cannot drift apart.
type command struct {
	// names are the words that select the command; usually one, and
	// --version answers to -V as well.
	names []string
	// summary is the one line the usage text prints beside the names.
	summary string
	// run is the handler, given the arguments after the command's name.
	run func(args []string, stdout, stderr io.Writer) error
	// internal marks a command mate starts itself (a hook, a pane's
	// program), never typed by a person. Usage lists it on its own line.
	internal bool
}

// commands is the top-level command table, in the order usage prints it.
// A new command is one row here. It is a function rather than a package
// variable so a handler that reads the table (or calls run) never makes an
// initialization cycle.
func commands() []command {
	return []command{
		{names: []string{"init"}, summary: "create a workspace, or open the one there", run: cmdInit},
		{names: []string{"project"}, summary: "add, list, remove and configure projects", run: cmdProject},
		{names: []string{"mate"}, summary: "start, stop, refresh or check a project's Mate", run: cmdMate},
		{names: []string{"crew"}, summary: "spawn, list, stop, relaunch and budget crews", run: withStdin(cmdCrew)},
		{names: []string{"brief"}, summary: "check, append to or revise a crew's brief", run: withStdin(cmdBrief)},
		{names: []string{"remember"}, summary: "add one entry to a project's memory", run: cmdRemember},
		{names: []string{"memory"}, summary: "check a project's memory file", run: cmdMemory},
		{names: []string{"recall"}, summary: "print the digest a Mate reads at a session start", run: cmdRecall},
		{names: []string{"learnings"}, summary: "list open predictions, or close one", run: cmdLearnings},
		{names: []string{"wake"}, summary: "print what woke the Mate, in one read", run: cmdWake},
		{names: []string{"send"}, summary: "type one line into a crew's pane", run: cmdSend},
		{names: []string{"news"}, summary: "open, update, close, list and propose news for the captain", run: cmdNews},
		{names: []string{"notifications"}, summary: "send a test native notification", run: cmdNotifications},
		{names: []string{"work"}, summary: "the board of what the captain asked for and the Mate proposed", run: cmdWork},
		{names: []string{"peek"}, summary: "a read-only snapshot of a crew's pane", run: cmdPeek},
		{names: []string{"state"}, summary: "one line on a crew's state and health", run: cmdState},
		{names: []string{"diff"}, summary: "a crew's branch, for review", run: cmdDiff},
		{names: []string{"report"}, summary: "print a crew's report", run: cmdReport},
		{names: []string{"review"}, summary: "spawn a reviewer crew, or check a review", run: cmdReview},
		{names: []string{"merge"}, summary: "merge a reviewed crew's branch", run: cmdMerge},
		{names: []string{"land"}, summary: "accept a hand-back: merge and record it", run: cmdLand},
		{names: []string{"backlog"}, summary: "read the crew table or edit a backlog entry", run: cmdBacklog},
		{names: []string{"checkpoint"}, summary: "record the receipt of a Mate's stow", run: cmdCheckpoint},
		{names: []string{"pr"}, summary: "watch a crew's pull request", run: cmdPR},
		{names: []string{"events"}, summary: "the timeline as JSON lines, sentences or a scene", run: cmdEvents},
		{names: []string{"reindex"}, summary: "rebuild the timeline from files and transcripts", run: cmdReindex},
		{names: []string{"usage"}, summary: "the token ledger per task or per crew", run: cmdUsage},
		{names: []string{"gc"}, summary: "list processes left in closed tasks' worktrees, or end them", run: cmdGC},
		{names: []string{"dashboard"}, summary: "the workspace timeline in a browser", run: cmdDashboard},
		{names: []string{"console"}, summary: "open the console and lay out its columns", run: cmdConsoleLaunch},
		{names: []string{"migrate"}, summary: "move a workspace onto layout 2", run: cmdMigrate},
		{names: []string{"plugin"}, summary: "add, check and list plugins", run: cmdPlugin},
		{names: []string{"--version", "-V"}, summary: "print the version", run: cmdVersion},
		{names: []string{"hook"}, summary: "the Claude Code hooks of a Mate's pane", run: withStdin(cmdHook), internal: true},
		{names: []string{"pane"}, summary: "the program the console's columns run", run: cmdPane, internal: true},
	}
}

// withStdin adapts a handler that reads standard input to the table's
// handler shape.
func withStdin(fn func(args []string, stdin io.Reader, stdout, stderr io.Writer) error) func([]string, io.Writer, io.Writer) error {
	return func(args []string, stdout, stderr io.Writer) error {
		return fn(args, os.Stdin, stdout, stderr)
	}
}

// dispatcher is run's decision, with what it reaches outside the table
// passed in so a test can stand each piece in.
type dispatcher struct {
	commands []command
	// findWorkspace resolves the workspace a bare `mate` opens.
	findWorkspace func() (string, error)
	// isDir reports whether an argument names an existing directory.
	isDir func(path string) bool
	// console opens the console on a workspace directory.
	console func(dir string, stdout, stderr io.Writer) error
}

// run dispatches the top-level subcommand. Each subcommand is a small,
// independently testable function of the form
// func(args []string, stdout, stderr io.Writer) error.
func run(args []string, stdout, stderr io.Writer) error {
	return dispatcher{
		commands:      commands(),
		findWorkspace: func() (string, error) { return findWorkspaceDir("") },
		isDir:         isDir,
		console:       cmdConsole,
	}.run(args, stdout, stderr)
}

func (d dispatcher) run(args []string, stdout, stderr io.Writer) error {
	// `mate` with no arguments opens the console on the workspace the
	// current directory sits in. A subcommand always wins over a directory
	// of the same name: the subcommand names are reserved words, and an
	// explicit `./init` still reaches the console.
	if len(args) == 0 {
		dir, err := d.findWorkspace()
		if err != nil {
			return newUsageError(usageText(d.commands))
		}
		return d.console(dir, stdout, stderr)
	}
	if c, ok := lookupCommand(d.commands, args[0]); ok {
		return c.run(args[1:], stdout, stderr)
	}
	// A single argument naming an existing directory is a workspace to open.
	if len(args) == 1 && d.isDir(args[0]) {
		return d.console(args[0], stdout, stderr)
	}
	return newUsageErrorf("unknown command %q", args[0])
}

// lookupCommand finds the row one of whose names is name.
func lookupCommand(cmds []command, name string) (command, bool) {
	for _, c := range cmds {
		if slices.Contains(c.names, name) {
			return c, true
		}
	}
	return command{}, false
}

// usageText is the usage of the top-level CLI, printed from the table: the
// commands people type on the main line, the ones mate runs itself on a
// line of their own, then each command's summary.
func usageText(cmds []command) string {
	var public, internal []string
	width := 0
	for _, c := range cmds {
		label := strings.Join(c.names, "|")
		if c.internal {
			internal = append(internal, label)
		} else {
			public = append(public, label)
		}
		width = max(width, len(strings.Join(c.names, ", ")))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "usage: mate <workspace-dir> | mate <%s> ...", strings.Join(public, "|"))
	if len(internal) > 0 {
		fmt.Fprintf(&b, "\ninternal, run by mate itself: mate <%s> ...", strings.Join(internal, "|"))
	}
	b.WriteString("\n")
	for _, c := range cmds {
		fmt.Fprintf(&b, "\n  %-*s  %s", width, strings.Join(c.names, ", "), c.summary)
		if c.internal {
			b.WriteString(" (internal)")
		}
	}
	return b.String()
}

// isDir reports whether path names an existing directory, following
// symlinks the way every other path in the CLI does.
func isDir(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// cmdProject dispatches `mate project <add|list|remove|repo|facts|review|next-work>`.
func cmdProject(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return newUsageError("usage: mate project <add|list|remove|repo|facts|review|next-work> ...")
	}
	switch args[0] {
	case "facts":
		return cmdProjectFacts(args[1:], stdout, stderr)
	case "add":
		return cmdProjectAdd(args[1:], stdout, stderr)
	case "list":
		return cmdProjectList(args[1:], stdout, stderr)
	case "remove":
		return cmdProjectRemove(args[1:], stdout, stderr)
	case "repo":
		return cmdProjectRepo(args[1:], stdout, stderr)
	case "review":
		return cmdProjectReview(args[1:], stdout, stderr)
	case "next-work":
		return cmdProjectNextWork(args[1:], stdout, stderr)
	case "yolo", "mode":
		// Removed by docs/mvp.md M19; a Mate still quoting an old manual is
		// told what replaced them rather than "unknown subcommand".
		return newUsageErrorf("mate project %s was removed: the Mate picks each crew's delivery with `mate crew spawn --deliver local|pr` and merges reviewed work with `mate merge <project> <crew> --review <reviewer>`", args[0])
	default:
		return newUsageErrorf("unknown project subcommand %q", args[0])
	}
}

// reorderArgs moves every flag (and its value, if it takes one) in front of
// the positional arguments, in the order the caller wrote them, so a
// subcommand accepts its flags before, after, or interspersed with its
// positional arguments. The standard flag package otherwise stops parsing at
// the first non-flag token, which would make `mate project add <name>
// <repo-path> --workspace <dir>` silently ignore --workspace.
func reorderArgs(fs *flag.FlagSet, args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(a) < 2 || a[0] != '-' {
			positional = append(positional, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			continue // value is already part of this token
		}
		fl := fs.Lookup(name)
		if fl != nil {
			if bf, ok := fl.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
				continue // boolean flags take no separate value
			}
		}
		if i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, positional...)
}

// resolveWorkspace opens the workspace named by flagVal, or, if flagVal is
// empty, the one found by findWorkspaceDir.
func resolveWorkspace(flagVal string) (*store.Workspace, error) {
	dir, err := findWorkspaceDir(flagVal)
	if err != nil {
		return nil, err
	}
	return store.Open(dir)
}

// resolveStopWorkspace is resolveWorkspace for `mate crew stop` and `mate
// mate stop` alone: it opens a workspace on the old layout too
// (store.OpenForMigrate), because mate migrate refuses while a crew is open
// or a Mate runs and names these two commands as the way out. Neither
// writes a repo path. Every other command stays on resolveWorkspace.
func resolveStopWorkspace(flagVal string) (*store.Workspace, error) {
	dir, err := findWorkspaceDir(flagVal)
	if err != nil {
		return nil, err
	}
	return store.OpenForMigrate(dir)
}

// findWorkspaceDir resolves the workspace directory per docs/mvp.md task 04:
// the --workspace flag if given, else MATE_WORKSPACE, else the nearest
// ancestor of the current directory that contains `.mate/`.
func findWorkspaceDir(flagVal string) (string, error) {
	if flagVal != "" {
		return flagVal, nil
	}
	if env := os.Getenv("MATE_WORKSPACE"); env != "" {
		return env, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := cwd
	for {
		if fi, statErr := os.Stat(filepath.Join(dir, store.StateDirName)); statErr == nil && fi.IsDir() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no %s workspace found in %s or any parent directory (use --workspace or set MATE_WORKSPACE)", store.StateDirName, cwd)
		}
		dir = parent
	}
}
