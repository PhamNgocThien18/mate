package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nguyenngocanh94/mate/internal/core"
	"github.com/nguyenngocanh94/mate/internal/notifications"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

const coreUsage = `usage: mate core <run|start|stop|restart|status> [<workspace>] [--workspace <dir>]
  run      the engine in the foreground until Ctrl+C, as plain "mate" does
  start    the engine in the background, its output in .mate/core.log
  stop     SIGTERM to the engine, SIGKILL after 60s
  restart  stop, then start: the binary installed now runs
  status   pid, owner, build, socket and Office/City; exit 1 when none runs`

// cmdCore is `mate core <subcommand>`: the workspace's engine as a process
// of its own (CON-203). `run` keeps it in the foreground (CON-211); start,
// stop, restart and status run it in the background (CON-213).
func cmdCore(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return newUsageError(coreUsage)
	}
	switch args[0] {
	case "run":
		return cmdCoreRun(args[1:], stdout, stderr)
	case "start", "stop", "restart", "status":
		ws, err := coreWorkspaceArgs(args[0], args[1:], stderr)
		if err != nil {
			return err
		}
		ctl := liveCoreControl()
		switch args[0] {
		case "start":
			return ctl.start(ws, stdout)
		case "stop":
			return ctl.stop(ws, stdout)
		case "restart":
			return ctl.restart(ws, stdout)
		}
		return ctl.status(ws, stdout)
	case "-h", "--help", "help":
		_, err := fmt.Fprintln(stdout, coreUsage)
		return err
	}
	return newUsageErrorf("mate core: unknown subcommand %q\n%s", args[0], coreUsage)
}

// cmdCoreRun is `mate core run [<workspace>] [--workspace <dir>]`: the
// engine a console runs - observer and timeline, outbox and auto daemon,
// recovery, Office/City, notifications - with no console, in the
// foreground, until SIGINT, SIGTERM or SIGHUP.
func cmdCoreRun(args []string, stdout, stderr io.Writer) error {
	// The build core.lock records is the file this process started from.
	core.Build()
	ws, err := coreWorkspaceArgs("run", args, stderr)
	if err != nil {
		return err
	}
	return runCoreForeground(ws, stdout, stderr)
}

// cmdCoreHere is `mate` and `mate <dir>` (decided 2026-11-06): the core in
// the foreground, like a dev server, as `mate core run`. When the
// workspace's core already runs it says so in `mate core status`'s lines
// and starts nothing; an engine inside a console is refused. It opens no
// browser.
func cmdCoreHere(dir string, stdout, stderr io.Writer) error {
	core.Build()
	ws, err := store.Open(dir)
	if err != nil {
		return err
	}
	return coreHere(ws, liveCoreControl(), stdout, func() error {
		return runCoreForeground(ws, stdout, stderr)
	})
}

// coreHere is cmdCoreHere past opening the workspace: run starts the core
// when no engine runs on ws.
func coreHere(ws *store.Workspace, ctl coreControl, stdout io.Writer, run func() error) error {
	h, held, err := ws.CoreRunning()
	if err != nil {
		return fmt.Errorf("reading %s: %w", ws.CoreLockFile(), err)
	}
	if !held {
		return run()
	}
	if h.Owner == core.OwnerConsole {
		return fmt.Errorf("the engine runs inside mate console (%s); quit that console first", pidWord(h.PID))
	}
	fmt.Fprintln(stdout, "mate core: already running")
	ctl.printStatus(ws, h, stdout)
	return nil
}

// runCoreForeground runs ws's engine over the live dependencies until
// SIGINT, SIGTERM or SIGHUP. Its output goes to core.log as well as to the
// terminal (decided 2026-11-06), so a core's log is in one place however
// it was started.
func runCoreForeground(ws *store.Workspace, stdout, stderr io.Writer) error {
	stdout, stderr, closeLog, err := coreLogTee(ws, stdout, stderr, os.Stdout)
	if err != nil {
		return err
	}
	defer closeLog()

	// The live dependencies the console builds (runConsole): the `herdr`
	// findTool resolves, and the observer `.mate/.env` asks for.
	deps := baseDeps(spawn.WithHerdrBinary(findTool(os.Getenv, "herdr")))
	var (
		observerErr error
		notices     []string
	)
	deps.Observer, observerErr = configuredObserver(ws)
	if observerErr != nil {
		fmt.Fprintf(stderr, "mate core: %v; the fixture observer reads the panes\n", observerErr)
		notices = append(notices, observerErr.Error())
	}

	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stopSignals()
	// After the first signal the next one ends the process at once, as
	// it would with no handler: the stop is bounded, but a captain who
	// asks twice means it.
	go func() {
		<-ctx.Done()
		stopSignals()
	}()
	return runCore(ctx, ws, deps, core.NativeNotifier(), notices, stdout, stderr)
}

// runCore runs the engine of ws over deps until ctx ends, then stops it
// within its bound (core.StopWait) and returns. It serves Office/City on
// `.mate/core.sock` and on console.yaml's dashboard address; a busy or
// refused TCP address is said and the core runs on the socket alone.
// notices are what the caller found wrong already (the observer's
// configuration), which GET /api/core reports after the core's own.
//
// A console is open while one reaches the socket (core.ConsoleSeen,
// CON-212): with none in the last ten seconds the outbox loop types nothing
// into a Manual project's Mate; an Auto project gets its digests, refreshes
// and queued lines either way (CON-203, decided 2026-11-05).
func runCore(ctx context.Context, ws *store.Workspace, deps spawn.Deps, notifier notifications.Sender, notices []string, stdout, stderr io.Writer) error {
	// A console built before CON-211 runs an engine of its own without
	// core.lock: the lock alone would let a second engine start beside it.
	if h, held, err := ws.ConsoleRunning(); err != nil {
		return fmt.Errorf("mate core: reading %s: %w", ws.ConsoleLockFile(), err)
	} else if held && !h.CoreLock {
		return fmt.Errorf("mate core: console %s runs an older mate with its own engine; quit it first", pidWord(h.PID))
	}
	commands, err := loadConsoleCommands(ws)
	if err != nil {
		return err
	}
	noticeClient, noticeErr := consoleNoticeClient(ws)
	if noticeErr != nil {
		fmt.Fprintf(stderr, "mate core: %v\n", noticeErr)
	}
	notices = jevNoticeFirst(noticeErr, notices)

	// core.lock before the parts: the observer opens mate.db as it is
	// built, and an engine built while another one still held mate.db
	// would run without the timeline for its whole life.
	claim, err := core.Lock(ws, core.OwnerCore, time.Now())
	if err != nil {
		return coreEngineRefusal(err)
	}
	seen := &core.ConsoleSeen{}
	opts, err := engineOptions(ws, deps, seen.Present)
	if err != nil {
		claim.Release()
		return err
	}
	opts.Claim = claim
	opts.ConsoleSeen = seen
	opts.Notices = notices
	opts.Act = engineActions(ws, deps, noticeClient)
	opts.Dashboard = consoleDashboard(ws, commands.Dashboard.Addr, commands.Dashboard.AllowRemote, noticeClient != nil)
	opts.Dashboard.Socket = ws.CoreSocket()
	opts.Notifier = notifier
	opts.Stderr = stderr
	engine := core.New(opts)
	// The engine's own context, not ctx: a signal stops it through Stop,
	// which bounds the stop, rather than by cancelling every part at once.
	if err := engine.Start(context.Background()); err != nil {
		return coreEngineRefusal(err)
	}
	defer engine.Stop()

	// The block a dev server prints first: who runs, where, and the pages
	// to open. A browser is the captain's to open.
	serving := engine.Serving()
	fmt.Fprintf(stdout, "mate core · pid %d · %s\n", os.Getpid(), homeShort(ws.Root()))
	tcpErr := ""
	if serving.Err != nil {
		tcpErr = serving.Err.Error()
	}
	fmt.Fprint(stdout, officeCityLines(serving.URL, serving.LANURL, tcpErr))
	fmt.Fprint(stdout, herdrLogLines(ws))
	if line, err := engine.Notifications(); err != nil {
		fmt.Fprintf(stderr, "mate core: notifications: %v\n", err)
	} else if line != "" {
		fmt.Fprintf(stderr, "mate core: %s\n", line)
	}
	fmt.Fprintln(stdout, "Ctrl+C to stop")

	<-ctx.Done()
	fmt.Fprintf(stdout, "mate core: stopping (at most %s)...\n", core.StopWait)
	engine.Stop()
	fmt.Fprintln(stdout, "mate core: stopped")
	return nil
}

// jevNoticeFirst puts Jev's configuration error before the notices the
// caller had (the observer's), as the console's status line says them; an
// error both report is said once.
func jevNoticeFirst(jevErr error, notices []string) []string {
	if jevErr == nil {
		return notices
	}
	line := jevErr.Error()
	out := []string{line}
	for _, n := range notices {
		if n != line {
			out = append(out, n)
		}
	}
	return out
}

// coreEngineRefusal words Start's refusal for `mate core run`.
func coreEngineRefusal(err error) error {
	var running *core.RunningError
	if !errors.As(err, &running) {
		return fmt.Errorf("mate core: %w", err)
	}
	if running.Holder.Owner == core.OwnerConsole {
		return fmt.Errorf("mate core: the engine runs inside mate console (%s); quit that console first", pidWord(running.Holder.PID))
	}
	return fmt.Errorf("mate core: already running (%s)", pidWord(running.Holder.PID))
}

// consoleEngineRefusal words Start's refusal for a console: until the
// console is a client of the core (CON-214), it runs no engine of its own
// beside one.
func consoleEngineRefusal(err error) error {
	var running *core.RunningError
	if !errors.As(err, &running) {
		return err
	}
	if running.Holder.Owner == core.OwnerCore {
		return fmt.Errorf("mate console: mate core (%s) is running; stop it with `mate core stop` to open this console", pidWord(running.Holder.PID))
	}
	return fmt.Errorf("mate console: another mate console (%s) runs the engine on this workspace; quit it first", pidWord(running.Holder.PID))
}

// engineHolderRefusal is `mate reindex`'s word when mate.db is locked: the
// process holding core.lock, which runs the engine that writes mate.db.
// It is nil when no engine holds core.lock, so the caller keeps the error
// it had.
func engineHolderRefusal(ws *store.Workspace) error {
	h, held, err := ws.CoreRunning()
	if err != nil || !held {
		return nil
	}
	switch h.Owner {
	case core.OwnerCore:
		return fmt.Errorf("mate reindex: mate core (%s) holds mate.db; run `mate core stop` first", pidWord(h.PID))
	case core.OwnerConsole:
		return fmt.Errorf("mate reindex: mate console (%s) holds mate.db; quit it first", pidWord(h.PID))
	}
	return nil
}

// pidWord is "pid N", or "pid unknown" for a lock whose record could not be
// read.
func pidWord(pid int) string {
	if pid <= 0 {
		return "pid unknown"
	}
	return fmt.Sprintf("pid %d", pid)
}
