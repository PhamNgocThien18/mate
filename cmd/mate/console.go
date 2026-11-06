package main

import (
	"context"
	"fmt"
	"io"
	"os"

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

// cmdConsoleLaunch is `mate console [<workspace-dir>]`: the interactive
// Bubble Tea console, and on a known host (WezTerm, Ghostty) it lays out
// the stage column before the TUI starts, so Enter fills it. Configured
// terminal commands open their tabs later, in the same window. Plain
// `mate` and `mate <dir>` opened it too until 2026-11-06; they run the
// core now (cmdCoreHere).
//
// This is the one place internal/ui/console's boundary (no store, no
// runtime, no Herdr - see internal/ui/console/doc.go) is bridged to real
// state. LoadFunc is the core's snapshot, read over `.mate/core.sock`
// (CON-214), plus the console's own keys (consoleLoad); everything that
// would need a live agent is refused with a message naming the mvp.md task
// that wires it, never a fake success.
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
	return runConsole(resolved, stdout, stderr)
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

Dashboard on the LAN (a phone on the same Wi-Fi): mate core serves the
dashboard on 127.0.0.1:7777 only. To reach it from other devices add

dashboard:
  addr: 0.0.0.0:7777
  allow_remote: true

then run mate core restart. The dashboard has no login and quotes everything
typed in the console, so allow_remote is required for any non-loopback addr.
The status line shows the LAN URL to open.
Full reference: docs/console-commands.md in the Mate source repository.
`

func runConsole(dir string, stdout, stderr io.Writer) error {
	// The build this console compares the core's with is the file this
	// process started from, read before anything can install over it.
	core.Build()
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
	// One workspace, one console (CON-162), before anything else: a refused
	// console has touched no core. Then the core, started when none answers
	// (CON-214). The console runs no engine of its own: the observer, the
	// timeline, the outbox and the auto daemon, recovery, Office/City and
	// the notifications run in `mate core`, and keep running after the
	// console quits.
	client, status, unlockConsole, err := openConsole(ws, liveConsoleClaim(stdinFile, stdoutFile), liveConsoleCore(stdinFile, stdoutFile))
	if err != nil {
		return err
	}
	defer unlockConsole()
	defer client.Close()

	ctx := context.Background()
	// The stage column's dependencies: the Herdr adapter, which finds the
	// workspace's session before an attach.
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

	// The console reads the core's snapshot, the one Mate Office and Agent
	// City read, with its own keys and what the core says about itself
	// added. It acts through the core's Act, as they do: an action on a
	// target another UI is acting on is refused (coreapi.ErrBusy), and
	// every action drops the core's cached snapshot, so the next read shows
	// what it changed.
	bindings := append(consoleCommandBindings(commands), links.bindings()...)
	load := boundedLoad(consoleLoad(client.Snapshot, bindings, coreNotices(client, status.Notices)), consoleReadWait)
	act := tuiAction(client.Act, h)

	notice := ""
	dashboardURL := consoleDashboardURL(status)
	if dashboardURL != "" {
		notice = "dashboard " + dashboardURL
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
	// What the core says about itself when the console opens: a dashboard
	// not served over TCP, the notifications', Jev's and the observer's
	// configuration errors. The console stays usable either way.
	for _, n := range status.Notices {
		addNotice(n)
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
			// The columns are laid out now, before the TUI starts.
			if err := columns.layout(ctx); err != nil {
				addNotice("no next pane: " + err.Error())
			}
		}
	}
	model := console.New(load, act).
		WithNoticeClassifier(status.Jev).
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
			sayAbandoned(stderr, desc)
		}
	}
	return nil
}

// sayAbandoned is the console's quit in the middle of an action, once the
// terminal is restored. The core runs the action (coreclient's Act: the
// console's cancel stops its wait, not the action), so the console neither
// cancels it nor waits for it, and says so.
func sayAbandoned(stderr io.Writer, desc string) {
	fmt.Fprintf(stderr, "\nmate console: %s keeps running in mate core\n", desc)
}

// consoleLoad is the console's LoadFunc over the core's snapshot
// (coreclient's Snapshot): the same read Mate Office and Agent City get,
// with the console's own keys in Commands - from .mate/console.yaml and the
// plugins, which the core knows nothing of - and, on the runtime line, what
// notices says about the core on this read.
//
// Only fields of this copy are set here; nothing the snapshot points to is
// changed.
func consoleLoad(snapshot coreapi.LoadFunc, bindings []query.CommandBinding, notices func(context.Context) []string) coreapi.LoadFunc {
	return func(ctx context.Context) (query.Snapshot, error) {
		snap, err := snapshot(ctx)
		if err != nil {
			return snap, err
		}
		snap.Commands = append([]query.CommandBinding(nil), bindings...)
		for _, line := range notices(ctx) {
			if snap.Runtime.Notice != "" {
				snap.Runtime.Notice += "; "
			}
			snap.Runtime.Notice += line
		}
		return snap, nil
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
