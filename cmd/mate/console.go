package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"

	"github.com/nguyenngocanh94/mate/internal/core"
	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/host"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

// cmdConsole is `mate <workspace-dir>` (and `mate` with no arguments in
// a workspace): the interactive Bubble Tea console.
//
// This is the one place internal/ui/console's boundary (no store, no
// runtime, no Herdr - see internal/ui/console/doc.go) is bridged to real
// state. LoadFunc is the engine's snapshot (internal/core) plus the
// console's own keys (consoleLoad); everything that would need a live agent
// is refused with a message naming the mvp.md task that wires it, never a
// fake success.
func cmdConsole(dir string, stdout, stderr io.Writer) error {
	return runConsole(dir, stdout, stderr, false)
}

// cmdConsoleLaunch is `mate console [<workspace-dir>]`: the interactive
// console, and on a known host (WezTerm, Ghostty) it lays out the stage
// column before the TUI starts, so Enter fills it. Configured terminal
// commands open their tabs later, in the same window.
func cmdConsoleLaunch(args []string, stdout, stderr io.Writer) error {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		_, err := io.WriteString(stdout, consoleSetupHelp)
		return err
	}
	if len(args) > 1 {
		return newUsageError("usage: mate console [<workspace-dir>]")
	}
	dir := ""
	if len(args) == 1 {
		dir = args[0]
	}
	resolved, err := findWorkspaceDir(dir)
	if err != nil {
		return err
	}
	return runConsole(resolved, stdout, stderr, true)
}

const consoleSetupHelp = `Usage: mate console [<workspace-dir>]

Editor setup
1. Install your editor's CLI and check it in your terminal: code --version,
   zed --version, or your chosen editor's equivalent. For VS Code on macOS,
   use the Command Palette: "Shell Command: Install 'code' command in PATH".
2. Create or edit <workspace-dir>/.mate/console.yaml, preserving any existing
   bindings. This is the workspace root, not the Mate's own working directory.

bindings:
  - key: e
    scope: crew
    label: report
    command: [code, --reuse-window, "{report_path}"]
    mode: external

For Zed, use command: [zed, "{report_path}"] with mode: external.
For a terminal editor, use command: [nvim, "{report_path}"] with mode: terminal.
Use an absolute executable path if the CLI is not on PATH.

3. Reopen the console, select a Crew row and press e. Press ? for key help.
   If report.md does not exist yet, the binding reports it; use {crew_dir}
   instead of {report_path} to open the Crew's folder, or {worktree_dir}
   for the code it is changing.

Available paths: {workspace_dir}, {project_dir}, {crew_dir}, {report_path},
{worktree_dir}. The last three require scope: crew. scope: project opens on a project row.
cwd defaults to the crew/project directory; a relative cwd uses workspace root.
command is an argv list, passed without a shell or environment expansion.
terminal opens/reuses a managed tab in WezTerm/Ghostty.
external launches a GUI without a host tab; omit --wait (10-second timeout).
No editor binding is installed by default. Keys reserved by the console or
already bound in the same scope are refused. Configuration loads at startup.

Dashboard on the LAN (a phone on the same Wi-Fi): the console serves the
dashboard on 127.0.0.1:7777 only. To reach it from other devices add

dashboard:
  addr: 0.0.0.0:7777
  allow_remote: true

The dashboard has no login and quotes everything typed in the console, so
allow_remote is required for any non-loopback addr. The status line shows
the LAN URL to open.
Full reference: docs/console-commands.md in the Mate source repository.
`

func runConsole(dir string, stdout, stderr io.Writer, split bool) error {
	ws, err := store.Open(dir)
	if err != nil {
		return err
	}
	commands, err := loadConsoleCommands(ws)
	if err != nil {
		return err
	}
	links, pluginNotice, err := loadPluginLinks(ws, commands)
	if err != nil {
		return err
	}
	stdinFile, stdoutFile, err := consoleTerminalFiles(stdout)
	if err != nil {
		return err
	}
	if err := (runtime.TTYHandoff{Stdin: stdinFile, Stdout: stdoutFile}).CheckTerminal(); err != nil {
		return consoleTerminalRefusal(err)
	}
	// One workspace, one console (CON-162): before anything starts, so a
	// refused console has opened no observer, daemon or database.
	unlockConsole, err := liveConsoleClaim(stdinFile, stdoutFile).take(ws)
	if err != nil {
		return err
	}
	defer unlockConsole()

	ctx := context.Background()
	// One set of live dependencies for the whole Console run: the Herdr
	// adapter and the agent-name registry it shares, so a Mate started from
	// the action menu and the stream opened on it a keystroke later agree
	// about which names are reserved.
	//
	// Point the runtime at the `herdr` findTool resolves - the one the stage
	// column runs by absolute path, which can be in ~/.local/bin when the
	// Console's PATH cannot reach it. Without this, a session check would
	// consult a different executable than the attach and could refuse an
	// attach that would have worked (measured 2026-09-30).
	deps := baseDeps(spawn.WithHerdrBinary(findTool(os.Getenv, "herdr")))
	h := host.Open(host.Detect(os.Getenv), host.Options{Env: os.Getenv, SelfCols: func() int {
		cols, _, err := term.GetSize(stdoutFile.Fd())
		if err != nil {
			return 0
		}
		return cols
	}})
	// One observer for the whole run too, so the observer chain's one
	// minute memory of what Jev said is shared by the health column, the
	// sends and the settles of this console. Nil is the fixture observer.
	var observerErr error
	deps.Observer, observerErr = configuredObserver(ws)

	// The action function is built before the engine so the dashboard's
	// buttons run exactly what the action sheet runs (CON-141).
	noticeClient, noticeErr := consoleNoticeClient(ws)
	tuiAction, webAction := tuiAndWebActions(ws, deps, noticeClient, h)

	// The observer with the timeline it records, the delivery into the Mates,
	// recovery, the dashboard and the notifications run in this process for
	// as long as the workspace is open: the engine starts them in their order
	// and, when the console quits, stops them in the reverse (internal/core).
	// engineOptions says what each one is for. The dashboard comes up after
	// the observer has made sure the database exists; like the notifications
	// it is a convenience, and a failure is said on the status line and never
	// stops the console.
	opts, err := engineOptions(ws, deps)
	if err != nil {
		return err
	}
	opts.Dashboard = consoleDashboard(ws, commands.Dashboard.Addr, commands.Dashboard.AllowRemote, webAction, noticeClient != nil)
	opts.Notifier = core.NativeNotifier()
	engine := core.New(opts)
	if err := engine.Start(ctx); err != nil {
		return err
	}
	defer engine.Stop()

	// The console reads the engine's snapshot, the one Mate Office and Agent
	// City read, with its own keys and the notifications' word added. Every
	// action it runs drops the cached snapshot, so the next read shows what
	// the action changed.
	bindings := append(consoleCommandBindings(commands), links.bindings()...)
	load := consoleLoad(engine.Snapshot, bindings, func() string {
		line, _ := engine.Notifications()
		return line
	})
	tuiAction = invalidateAfter(tuiAction, engine.Invalidate)

	notice := ""
	dashboardURL := ""
	if url, err := engine.Dashboard(); err != nil {
		notice = err.Error()
	} else {
		notice = "dashboard " + url
		dashboardURL = url
	}
	// A console that cannot split says where its headless Mates are
	// instead of the bare dashboard URL.
	noHost := host.NoHostHint(os.Getenv)
	if h == nil {
		noHost = headlessHint(dashboardURL)
		if dashboardURL != "" {
			notice = noHost
		}
	}
	addNotice := func(text string) {
		if notice != "" {
			notice += "; "
		}
		notice += text
	}
	if _, err := engine.Notifications(); err != nil {
		addNotice("notifications: " + err.Error())
	}
	// A plugin configuration problem is said on the status line too; the
	// console runs on without plugin links.
	if pluginNotice != "" {
		addNotice(pluginNotice)
	}
	if h == nil && dashboardURL == "" {
		addNotice(noHost)
	}
	var columns *consoleColumns
	if h != nil {
		if columns, err = newConsoleColumns(h, os.Getenv); err != nil {
			addNotice("no next pane: " + err.Error())
		} else {
			defer columns.close()
			if err := columns.addCommandTabs(commands); err != nil {
				return err
			}
			// `mate console` lays the columns out now; `mate <dir>` at the
			// first Enter, which finds them gone and makes them.
			if split {
				if err := columns.layout(ctx); err != nil {
					addNotice("no next pane: " + err.Error())
				}
			}
		}
	}
	// A Jev configuration problem is said on the same status line, after
	// any pane message; the console stays usable either way.
	if noticeErr != nil {
		addNotice(noticeErr.Error())
	}
	if observerErr != nil && (noticeErr == nil || observerErr.Error() != noticeErr.Error()) {
		addNotice(observerErr.Error())
	}
	model := console.New(load, tuiAction).
		WithNoticeClassifier(noticeClient != nil).
		WithContext(ctx).
		WithStage(consoleStage(ws, deps, columns)).
		WithCommandView(consolePluginView(links, hostOpener(), runConsoleLauncher, consoleCommandView(ws, columns, commands, runConsoleLauncher))).
		WithNoHostHint(noHost).
		WithKindGlyphs(probeKindGlyphs(os.Getenv)).
		WithHarnessIcons(probeNerdIcons(os.Getenv, execOutput)).
		WithNotice(notice).
		WithClipboard(func(seq []byte) {
			// One write per sequence: the renderer also writes this file
			// from its own goroutine, one frame per write, so a single
			// write lands between two frames rather than inside one.
			_, _ = stdoutFile.Write(seq)
		})

	// Cell motion, not all motion: the Console answers presses and the
	// wheel, and nothing in it follows a bare pointer.
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithContext(ctx),
		tea.WithInput(stdinFile), tea.WithOutput(stdoutFile), tea.WithMouseCellMotion())
	final, err := program.Run()
	if err != nil {
		return fmt.Errorf("console exited with an error: %w", err)
	}
	if cm, ok := final.(console.Model); ok {
		if desc, abandoned := cm.AbandonedAction(); abandoned {
			fmt.Fprintf(stderr, "\nmate console: quit while %s was still running; waiting for it to undo what it started...\n", desc)
			waitAbandoned(stderr, desc, cm.AbandonedActionDone(), abandonedWait)
		}
	}
	return nil
}

// consoleLoad is the console's LoadFunc over the engine's snapshot (core's
// Engine.Snapshot): the same read Mate Office and Agent City get, with the
// console's own keys in Commands - from .mate/console.yaml and the plugins,
// which the core knows nothing of - and the notification loop's word, when
// it has one, on the runtime line.
//
// The snapshot is shared with every other reader of the engine, so only
// fields of this copy are set here; nothing it points to is changed.
func consoleLoad(snapshot coreapi.LoadFunc, bindings []query.CommandBinding, notifications func() string) coreapi.LoadFunc {
	return func(ctx context.Context) (query.Snapshot, error) {
		snap, err := snapshot(ctx)
		if err != nil {
			return snap, err
		}
		snap.Commands = append([]query.CommandBinding(nil), bindings...)
		if line := notifications(); line != "" {
			if snap.Runtime.Notice != "" {
				snap.Runtime.Notice += "; "
			}
			snap.Runtime.Notice += line
		}
		return snap, nil
	}
}

// invalidateAfter runs invalidate once act has returned, whatever it
// returned: an action that failed halfway can have changed something too.
func invalidateAfter(act coreapi.ActionFunc, invalidate func()) coreapi.ActionFunc {
	return func(ctx context.Context, req coreapi.ActionRequest) (string, error) {
		defer invalidate()
		return act(ctx, req)
	}
}

// consoleTerminalFiles requires the caller's own stdout to be a real
// *os.File, which an in-process test harness writing to a bytes.Buffer is
// not. That keeps the dispatch tests from ever reaching tea.NewProgram, and
// it checks the streams the Program is actually given rather than the
// process globals.
func consoleTerminalFiles(stdout io.Writer) (*os.File, *os.File, error) {
	out, ok := stdout.(*os.File)
	if !ok {
		return nil, nil, errNotATerminal
	}
	return os.Stdin, out, nil
}

// errNotATerminal is a usage error, so `mate <dir> > file` exits 2 with a
// sentence that says what to do instead of a wall of Bubble Tea noise.
var errNotATerminal = newUsageError(
	"mate console needs a real terminal on stdin and stdout; run it from an interactive shell, not a pipe or redirect")

// consoleTerminalRefusal rewords runtime.TTYHandoff's shared terminal check
// for this call site: that check runs the real tty ioctl (reused rather
// than duplicated), but its message is an attach client's and would name
// the wrong command.
func consoleTerminalRefusal(error) error { return errNotATerminal }

// abandonedWait bounds how long the Console waits, after the terminal is
// restored, for an action it quit in the middle of. It is longer than the
// spawn package's own 30s cleanup bound, so the action's compensation gets
// the chance to finish.
const abandonedWait = 45 * time.Second

// waitAbandoned blocks until the abandoned action has returned, or the
// bound passes. Exiting earlier would kill the goroutine mid-cleanup and
// leave a half-started agent behind (a Mate running with no mate.meta).
func waitAbandoned(stderr io.Writer, desc string, done <-chan struct{}, bound time.Duration) {
	if done == nil {
		return
	}
	select {
	case <-done:
		fmt.Fprintf(stderr, "mate console: %s has stopped.\n", desc)
	case <-time.After(bound):
		fmt.Fprintf(stderr, "mate console: %s had not stopped after %s; if it left an agent running, starting again adopts or refuses it by name.\n", desc, bound)
	}
}
