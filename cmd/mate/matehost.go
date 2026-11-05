package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// newsPoll is how often the host looks for news the Mate wrote.
const newsPoll = 700 * time.Millisecond

// replQuitWait is how long a REPL the host runs has to end once told to,
// before it is killed.
const replQuitWait = 2 * time.Second

// cmdMateHost implements `mate mate host <project>` (CON-78): the headless
// Mate. spawn.StartMate starts it as a background process of its own,
// outside Herdr, with no terminal and its output in host.log (CON-225);
// run by hand in a terminal it works the same.
//
// The host runs until it is told to stop: the socket's stop op, SIGTERM or
// SIGHUP. In a terminal it runs the REPL in the foreground as a process of
// its own (`mate mate repl --in-pane`), a client of its socket like any
// other (CON-223): a REPL that ends, or crashes, is started again and the
// Mate's turn goes on.
func cmdMateHost(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("mate host", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate mate host <project> [--workspace <dir>] [--no-repl]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	noREPL := fs.Bool("no-repl", false, "run no REPL in this terminal; open one with `mate mate repl <project>`")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return newUsageError("mate mate host: want exactly 1 argument: <project>")
	}
	project := fs.Arg(0)
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	release, ok, err := w.TryMateLock(project, matehost.HostLock)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("mate mate host: the Mate of %s already has a host running", project)
	}
	defer release()
	mateDir := w.MateDir(project)
	launch, err := matehost.ReadLaunch(mateDir)
	if err != nil {
		return fmt.Errorf("mate mate host: %w (start it with `mate mate start %s`, which prepares the launch)", err, project)
	}
	profile, err := harnesses.Lookup(harness.Kind(launch.Harness))
	if err != nil {
		return err
	}
	turns, ok := profile.(harness.HeadlessTurns)
	if !ok {
		return fmt.Errorf("mate mate host: the %s harness cannot run a headless Mate", launch.Harness)
	}
	runner := &matehost.Runner{
		Launch: launch,
		Turns:  turns,
		Lock:   func() (func(), bool, error) { return w.TryMateLock(project, matehost.TurnLock) },
	}
	opts := matehost.Options{History: matehost.HistoryPath(mateDir), News: matehost.NewsPath(mateDir), Topics: matehost.TopicsPath(mateDir), PID: os.Getpid()}
	bin, _ := os.Executable()
	if forks, ok := profile.(harness.ForkTurns); ok {
		// A topic's session (CON-104) runs the binary the Mate's manual
		// names, which is this one.
		opts.Forks, opts.MateBin, opts.Allow = forks, bin, matehost.ForkAllow(bin, project)
	}
	host := matehost.New(runner, opts)
	// spawn starts the host as a process of its own, outside Herdr, with
	// this output in host.log (CON-225): a line per start says which run
	// the lines below it belong to.
	fmt.Fprintf(stderr, "%s mate mate host %s: pid %d, session %s, socket %s\n",
		time.Now().UTC().Format(time.RFC3339), project, os.Getpid(), launch.SessionID, matehost.SocketPath(mateDir))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGHUP)
	// ^C belongs to the REPL, which reads it as a key. One typed while no
	// REPL holds the terminal (between two) reaches this process too, and
	// must not end the host: it is taken here and dropped.
	signal.Notify(make(chan os.Signal, 1), os.Interrupt)

	live := matehost.LiveOptions{Socket: matehost.SocketPath(mateDir), NewsPoll: newsPoll, Signals: sigs, Log: stderr}
	if !*noREPL && term.IsTerminal(os.Stdin.Fd()) && bin != "" {
		live.REPL = func(ctx context.Context) error {
			return runREPLProcess(ctx, bin, project, w.Root())
		}
	}
	return host.Live(ctx, live)
}

// runREPLProcess runs `mate mate repl --in-pane` on this terminal until it
// ends or ctx does; then it is told to quit, and killed if it will not.
func runREPLProcess(ctx context.Context, bin, project, root string) error {
	cmd := exec.CommandContext(ctx, bin, "mate", "repl", project, "--workspace", root, "--in-pane")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = replQuitWait
	return cmd.Run()
}

// cmdMateREPL implements `mate mate repl <project>` (CON-223): the Mate's
// REPL as a client of its host's socket, in this terminal. The host runs
// one in its own pane, and the console's stage one in its column (both
// --in-pane, CON-224); another terminal can open one beside them, and
// all show the same chat and the same draft.
func cmdMateREPL(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("mate repl", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate mate repl <project> [--workspace <dir>]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	inPane := fs.Bool("in-pane", false, "run in a pane mate keeps open, which ^C never closes (mate mate host and the console's stage pass it)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return newUsageError("mate mate repl: want exactly 1 argument: <project>")
	}
	project := fs.Arg(0)
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	if _, ok := w.Project(project); !ok {
		return fmt.Errorf("mate mate repl: no project %s in %s", project, w.Root())
	}
	mateDir := w.MateDir(project)
	launch, err := matehost.ReadLaunch(mateDir)
	if err != nil {
		return fmt.Errorf("mate mate repl: the Mate of %s has no headless host: %w", project, err)
	}
	cfg := replConfig(w, project, launch)
	cfg.Quit = !*inPane
	return matehost.RunREPL(context.Background(), matehost.REPLConn(matehost.Dial(mateDir)), cfg)
}

// replConfig is what the REPL shows of the Mate besides its chat.
func replConfig(w *store.Workspace, project string, launch matehost.Launch) matehost.Config {
	return matehost.Config{
		Project: project,
		Model:   launchFlag(launch.Args, "--model"),
		Crews: func() (int, error) {
			crews, err := spawn.ListCrews(w, project)
			open := 0
			for _, c := range crews {
				if !c.Closed {
					open++
				}
			}
			return open, err
		},
	}
}

// launchFlag is the value after flag in a launch's args, or "".
func launchFlag(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
