package main

import (
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/matehost"
)

// cmdWork implements `mate work <add|progress|wait|done|reopen|list> <project> ...`
// (CON-158): the board of what the captain asked the Mate for and what
// the Mate proposed, where each stands. It is the Mate's report, not a
// question: what needs the captain's answer is a news, and a row that
// waits on one names it with `wait`. The captain ticks a done row in the
// Office, the City or the console; there is no `okay` here, because the
// Mate never okays its own work.
func cmdWork(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return newUsageError("usage: mate work <add|progress|wait|done|reopen|list> <project> ...")
	}
	switch args[0] {
	case "add":
		return cmdWorkAdd(args[1:], stdout, stderr)
	case "progress":
		return cmdWorkLine(args[1:], stdout, stderr, "progress", matehost.ProgressWork)
	case "wait":
		return cmdWorkWait(args[1:], stdout, stderr)
	case "done":
		return cmdWorkLine(args[1:], stdout, stderr, "done", matehost.DoneWork)
	case "reopen":
		return cmdWorkLine(args[1:], stdout, stderr, "reopen", matehost.ReopenWork)
	case "list":
		return cmdWorkList(args[1:], stdout, stderr)
	default:
		return newUsageErrorf("unknown work subcommand %q (want add, progress, wait, done, reopen or list)", args[0])
	}
}

func workLog(workspaceFlag, project string) (string, error) {
	w, err := resolveWorkspace(workspaceFlag)
	if err != nil {
		return "", err
	}
	if err := requireProject(w, project); err != nil {
		return "", err
	}
	return matehost.WorkPath(w.MateDir(project)), nil
}

func workID(s string) (int, error) {
	id, err := strconv.Atoi(strings.TrimPrefix(s, "#"))
	if err != nil || id <= 0 {
		return 0, newUsageErrorf("want a work id like 3 or #3, got %q", s)
	}
	return id, nil
}

func cmdWorkAdd(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("work add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, `usage: mate work add <project> "<title, one line>" [--proposed] [--note "<where it starts>"] [--workspace <dir>]`)
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	proposed := fs.Bool("proposed", false, "the Mate proposed it; the captain did not ask for it")
	note := fs.String("note", "", "a first line: who asked, or why it is proposed")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return newUsageError("mate work add: want exactly 2 arguments: <project> <title>")
	}
	if strings.TrimSpace(fs.Arg(1)) == "" {
		return newUsageError("mate work add: the title is empty")
	}
	kind := matehost.WorkAssigned
	if *proposed {
		kind = matehost.WorkProposed
	}
	path, err := workLog(*workspaceFlag, fs.Arg(0))
	if err != nil {
		return err
	}
	id, err := matehost.AddWork(path, kind, fs.Arg(1), *note, time.Now())
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "#%d\n", id)
	return nil
}

// cmdWorkLine is progress, done and reopen: `<project> <id> ["<line>"]`,
// the line required for progress (it is what moved) and optional
// otherwise.
func cmdWorkLine(args []string, stdout, stderr io.Writer, name string, op func(path string, id int, text string, now time.Time) error) error {
	fs := flag.NewFlagSet("work "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		line := ` ["<one line>"]`
		if name == "progress" {
			line = ` "<what moved, one line>"`
		}
		fmt.Fprintf(stderr, "usage: mate work %s <project> <id>%s [--workspace <dir>]\n", name, line)
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() < 2 || fs.NArg() > 3 {
		fs.Usage()
		return newUsageErrorf("mate work %s: want <project> <id> and at most one line", name)
	}
	id, err := workID(fs.Arg(1))
	if err != nil {
		return err
	}
	path, err := workLog(*workspaceFlag, fs.Arg(0))
	if err != nil {
		return err
	}
	return op(path, id, fs.Arg(2), time.Now())
}

func cmdWorkWait(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("work wait", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, `usage: mate work wait <project> <id> --topic <news id> ["<what the captain is asked>"] [--workspace <dir>]`)
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	topic := fs.String("topic", "", "the news (topic) the row waits on")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() < 2 || fs.NArg() > 3 {
		fs.Usage()
		return newUsageError("mate work wait: want <project> <id> and at most one line")
	}
	id, err := workID(fs.Arg(1))
	if err != nil {
		return err
	}
	if *topic == "" {
		return newUsageError("mate work wait: --topic is required: a row waits on a news, never on nothing")
	}
	news, err := newsID(*topic)
	if err != nil {
		return err
	}
	path, err := workLog(*workspaceFlag, fs.Arg(0))
	if err != nil {
		return err
	}
	return matehost.WaitWork(path, id, news, fs.Arg(2), time.Now())
}

func cmdWorkList(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("work list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate work list <project> [--all] [--workspace <dir>]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	all := fs.Bool("all", false, "okayed rows too")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return newUsageError("mate work list: want exactly 1 argument: <project>")
	}
	path, err := workLog(*workspaceFlag, fs.Arg(0))
	if err != nil {
		return err
	}
	list, err := matehost.LoadWork(path)
	if err != nil {
		return err
	}
	shown := 0
	for _, w := range list {
		if !w.Open() && !*all {
			continue
		}
		shown++
		fmt.Fprint(stdout, workListLine(w))
	}
	if shown == 0 {
		fmt.Fprintln(stdout, "nothing on the board")
	}
	return nil
}

// workListLine is one row as `mate work list` prints it: id, status (with
// the topic a waiting row waits on), kind when proposed, title; then the
// Mate's last line indented.
func workListLine(w matehost.Work) string {
	status := w.Status
	if w.Status == matehost.WorkWaiting && w.Topic > 0 {
		status = fmt.Sprintf("waiting on news #%d", w.Topic)
	}
	kind := ""
	if w.Kind == matehost.WorkProposed {
		kind = " proposed:"
	}
	s := fmt.Sprintf("#%d %s%s %s\n", w.ID, status, kind, w.Title)
	if w.Last != "" {
		s += "    " + w.Last + "\n"
	}
	return s
}
