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

// cmdDuty implements `mate duty <add|progress|wait|done|reopen|list> <project> ...`
// (CON-158): the board of what the captain asked the Mate for and what
// the Mate proposed, where each stands. It is the Mate's report, not a
// question: what needs the captain's answer is a news, and a row that
// waits on one names it with `wait`. The captain ticks a done row in the
// Office, the City or the console; there is no `okay` here, because the
// Mate never okays its own duty.
func cmdDuty(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return newUsageError("usage: mate duty <add|progress|wait|done|reopen|list> <project> ...")
	}
	switch args[0] {
	case "add":
		return cmdDutyAdd(args[1:], stdout, stderr)
	case "progress":
		return cmdDutyLine(args[1:], stdout, stderr, "progress", matehost.ProgressDuty)
	case "wait":
		return cmdDutyWait(args[1:], stdout, stderr)
	case "done":
		return cmdDutyLine(args[1:], stdout, stderr, "done", matehost.DoneDuty)
	case "reopen":
		return cmdDutyLine(args[1:], stdout, stderr, "reopen", matehost.ReopenDuty)
	case "list":
		return cmdDutyList(args[1:], stdout, stderr)
	default:
		return newUsageErrorf("unknown duty subcommand %q (want add, progress, wait, done, reopen or list)", args[0])
	}
}

func dutyLog(workspaceFlag, project string) (string, error) {
	w, err := resolveWorkspace(workspaceFlag)
	if err != nil {
		return "", err
	}
	if err := requireProject(w, project); err != nil {
		return "", err
	}
	return matehost.DutyPath(w.MateDir(project)), nil
}

func dutyID(s string) (int, error) {
	id, err := strconv.Atoi(strings.TrimPrefix(s, "#"))
	if err != nil || id <= 0 {
		return 0, newUsageErrorf("want a duty id like 3 or #3, got %q", s)
	}
	return id, nil
}

func cmdDutyAdd(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("duty add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, `usage: mate duty add <project> "<title, one line>" [--proposed] [--note "<where it starts>"] [--workspace <dir>]`)
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	proposed := fs.Bool("proposed", false, "the Mate proposed it; the captain did not ask for it")
	note := fs.String("note", "", "a first line: who asked, or why it is proposed")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return newUsageError("mate duty add: want exactly 2 arguments: <project> <title>")
	}
	if strings.TrimSpace(fs.Arg(1)) == "" {
		return newUsageError("mate duty add: the title is empty")
	}
	kind := matehost.DutyAssigned
	if *proposed {
		kind = matehost.DutyProposed
	}
	path, err := dutyLog(*workspaceFlag, fs.Arg(0))
	if err != nil {
		return err
	}
	id, err := matehost.AddDuty(path, kind, fs.Arg(1), *note, time.Now())
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "#%d\n", id)
	return nil
}

// cmdDutyLine is progress, done and reopen: `<project> <id> ["<line>"]`,
// the line required for progress (it is what moved) and optional
// otherwise.
func cmdDutyLine(args []string, stdout, stderr io.Writer, name string, op func(path string, id int, text string, now time.Time) error) error {
	fs := flag.NewFlagSet("duty "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		line := ` ["<one line>"]`
		if name == "progress" {
			line = ` "<what moved, one line>"`
		}
		fmt.Fprintf(stderr, "usage: mate duty %s <project> <id>%s [--workspace <dir>]\n", name, line)
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() < 2 || fs.NArg() > 3 {
		fs.Usage()
		return newUsageErrorf("mate duty %s: want <project> <id> and at most one line", name)
	}
	id, err := dutyID(fs.Arg(1))
	if err != nil {
		return err
	}
	path, err := dutyLog(*workspaceFlag, fs.Arg(0))
	if err != nil {
		return err
	}
	return op(path, id, fs.Arg(2), time.Now())
}

func cmdDutyWait(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("duty wait", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, `usage: mate duty wait <project> <id> --topic <news id> ["<what the captain is asked>"] [--workspace <dir>]`)
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	topic := fs.String("topic", "", "the news (topic) the row waits on")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() < 2 || fs.NArg() > 3 {
		fs.Usage()
		return newUsageError("mate duty wait: want <project> <id> and at most one line")
	}
	id, err := dutyID(fs.Arg(1))
	if err != nil {
		return err
	}
	if *topic == "" {
		return newUsageError("mate duty wait: --topic is required: a row waits on a news, never on nothing")
	}
	news, err := newsID(*topic)
	if err != nil {
		return err
	}
	path, err := dutyLog(*workspaceFlag, fs.Arg(0))
	if err != nil {
		return err
	}
	return matehost.WaitDuty(path, id, news, fs.Arg(2), time.Now())
}

func cmdDutyList(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("duty list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate duty list <project> [--all] [--workspace <dir>]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	all := fs.Bool("all", false, "okayed rows too")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return newUsageError("mate duty list: want exactly 1 argument: <project>")
	}
	path, err := dutyLog(*workspaceFlag, fs.Arg(0))
	if err != nil {
		return err
	}
	list, err := matehost.LoadDuty(path)
	if err != nil {
		return err
	}
	shown := 0
	for _, w := range list {
		if !w.Open() && !*all {
			continue
		}
		shown++
		fmt.Fprint(stdout, dutyListLine(w))
	}
	if shown == 0 {
		fmt.Fprintln(stdout, "nothing on the board")
	}
	return nil
}

// dutyListLine is one row as `mate duty list` prints it: id, status (with
// the topic a waiting row waits on), kind when proposed, title; then the
// Mate's last line indented.
func dutyListLine(w matehost.Duty) string {
	status := w.Status
	if w.Status == matehost.DutyWaiting && w.Topic > 0 {
		status = fmt.Sprintf("waiting on news #%d", w.Topic)
	}
	kind := ""
	if w.Kind == matehost.DutyProposed {
		kind = " proposed:"
	}
	s := fmt.Sprintf("#%d %s%s %s\n", w.ID, status, kind, w.Title)
	if w.Last != "" {
		s += "    " + w.Last + "\n"
	}
	return s
}
