package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/nguyenngocanh94/mate/internal/core"
	"github.com/nguyenngocanh94/mate/internal/notifications"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

const coreUsage = "usage: mate core run [<workspace>] [--workspace <dir>]"

// cmdCore is `mate core <subcommand>`: the workspace's engine as a process
// of its own (CON-203). `run` keeps it in the foreground (CON-211); start,
// stop, restart and status are CON-213's.
func cmdCore(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return newUsageError(coreUsage)
	}
	switch args[0] {
	case "run":
		return cmdCoreRun(args[1:], stdout, stderr)
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
	fs := flag.NewFlagSet("core run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	workspace := fs.String("workspace", "", "workspace directory")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	dir := *workspace
	switch fs.NArg() {
	case 0:
	case 1:
		if dir != "" {
			return newUsageError(coreUsage)
		}
		dir = fs.Arg(0)
	default:
		return newUsageError(coreUsage)
	}
	ws, err := resolveWorkspace(dir)
	if err != nil {
		return err
	}

	// The live dependencies the console builds (runConsole): the `herdr`
	// findTool resolves, and the observer `.mate/.env` asks for.
	deps := baseDeps(spawn.WithHerdrBinary(findTool(os.Getenv, "herdr")))
	var observerErr error
	deps.Observer, observerErr = configuredObserver(ws)
	if observerErr != nil {
		fmt.Fprintf(stderr, "mate core: %v; the fixture observer reads the panes\n", observerErr)
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
	return runCore(ctx, ws, deps, core.NativeNotifier(), stdout, stderr)
}

// runCore runs the engine of ws over deps until ctx ends, then stops it
// within its bound (core.StopWait) and returns. It serves Office/City on
// `.mate/core.sock` and on console.yaml's dashboard address; a busy or
// refused TCP address is said and the core runs on the socket alone.
//
// No console is open beside it (CON-214 has not made one a client yet), so
// the outbox loop types nothing into a Manual project's Mate; an Auto
// project gets its digests, refreshes and queued lines (CON-203, decided
// 2026-11-05).
func runCore(ctx context.Context, ws *store.Workspace, deps spawn.Deps, notifier notifications.Sender, stdout, stderr io.Writer) error {
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

	// core.lock before the parts: the observer opens mate.db as it is
	// built, and an engine built while another one still held mate.db
	// would run without the timeline for its whole life.
	claim, err := core.Lock(ws, core.OwnerCore, time.Now())
	if err != nil {
		return coreEngineRefusal(err)
	}
	opts, err := engineOptions(ws, deps, func() bool { return false })
	if err != nil {
		claim.Release()
		return err
	}
	opts.Claim = claim
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

	serving := engine.Serving()
	fmt.Fprintf(stdout, "mate core on %s (pid %d)\n  socket %s\n", ws.Root(), os.Getpid(), serving.Socket)
	if serving.URL != "" {
		fmt.Fprintf(stdout, "  dashboard %s\n", strings.TrimSpace(serving.URL+" "+serving.LANURL))
	}
	if serving.Err != nil {
		fmt.Fprintf(stderr, "mate core: %v; serving on the socket only\n", serving.Err)
	}
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
