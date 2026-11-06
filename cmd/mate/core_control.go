package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/nguyenngocanh94/mate/internal/core"
	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/coreclient"
	"github.com/nguyenngocanh94/mate/internal/platform"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// `mate core start|stop|restart|status` (CON-213): the core in the
// background, asked through core.lock and core.sock.
//
// Whether a core runs is core.lock's answer, never a pid's: a pid of a
// process that died may be some other process's by now. A signal is sent
// only to the pid the holder of the lock wrote, while the lock is held.

const (
	// coreStartWait is how long `start` waits for the new core to answer
	// on its socket.
	coreStartWait = 10 * time.Second
	// coreStopWait is how long `stop` waits after SIGTERM before SIGKILL:
	// longer than the engine's own bound (core.StopWait plus the closing of
	// mate.db, CON-210).
	coreStopWait = 60 * time.Second
	// coreKillWait is how long `stop` waits for the lock after SIGKILL.
	coreKillWait = 5 * time.Second
	// coreLogMax is the size past which `start` moves core.log to
	// core.log.1, the one old log kept.
	coreLogMax = 10 << 20
)

// coreControl is what start, stop and status reach outside the workspace's
// files, so a test can stand each in.
type coreControl struct {
	// command is the process `start` runs in the background: `mate core
	// run --workspace <root>` of this binary.
	command func(root string) (*exec.Cmd, error)
	// startDetached starts cmd in a session of its own.
	startDetached func(cmd *exec.Cmd) error
	// signal sends sig to pid.
	signal func(pid int, sig syscall.Signal) error
	// ask is GET /api/core on socket, within ctx.
	ask func(ctx context.Context, socket string) (coreapi.CoreStatus, error)
	now func() time.Time

	startWait, stopWait, killWait, poll time.Duration
}

func liveCoreControl() coreControl {
	return coreControl{
		command:       selfCoreRun,
		startDetached: platform.StartNewSession,
		signal:        platform.Signal,
		ask:           askCore,
		now:           time.Now,
		startWait:     coreStartWait,
		stopWait:      coreStopWait,
		killWait:      coreKillWait,
		poll:          100 * time.Millisecond,
	}
}

// selfCoreRun is `mate core run --workspace <root>` of the binary this
// process runs: after an install, the new one.
func selfCoreRun(root string) (*exec.Cmd, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("finding the mate binary: %w", err)
	}
	return exec.Command(exe, "core", "run", "--workspace", root), nil
}

// askCore is GET /api/core on the core's socket.
func askCore(ctx context.Context, socket string) (coreapi.CoreStatus, error) {
	client, err := coreclient.Dial(socket)
	if err != nil {
		return coreapi.CoreStatus{}, err
	}
	defer client.Close()
	return client.Core(ctx)
}

// coreWorkspaceArgs reads `[<workspace>] [--workspace <dir>]`, the
// arguments every `mate core` subcommand takes, and opens the workspace.
func coreWorkspaceArgs(name string, args []string, stderr io.Writer) (*store.Workspace, error) {
	fs := flag.NewFlagSet("core "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	workspace := fs.String("workspace", "", "workspace directory")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return nil, &usageError{err}
	}
	dir := *workspace
	switch fs.NArg() {
	case 0:
	case 1:
		if dir != "" {
			return nil, newUsageError(coreUsage)
		}
		dir = fs.Arg(0)
	default:
		return nil, newUsageError(coreUsage)
	}
	return resolveWorkspace(dir)
}

// start is `mate core start`: a core in the background, unless an engine
// already runs on ws.
func (c coreControl) start(ws *store.Workspace, stdout io.Writer) error {
	if h, held, err := ws.CoreRunning(); err != nil {
		return fmt.Errorf("mate core: reading %s: %w", ws.CoreLockFile(), err)
	} else if held {
		if h.Owner == core.OwnerConsole {
			return fmt.Errorf("mate core: the engine runs inside mate console (%s); quit that console first", pidWord(h.PID))
		}
		_, err := fmt.Fprintf(stdout, "mate core: already running (%s, since %s)\n", pidWord(h.PID), c.clock(h.Started))
		return err
	}
	// The child would refuse beside a console older than CON-211; say it
	// now rather than after ten seconds.
	if h, held, err := ws.ConsoleRunning(); err == nil && held && !h.CoreLock {
		return fmt.Errorf("mate core: console %s runs an older mate with its own engine; quit it first", pidWord(h.PID))
	}

	logFile, err := openCoreLog(ws)
	if err != nil {
		return err
	}
	defer logFile.Close()
	fmt.Fprintf(logFile, "--- mate core start %s ---\n", c.now().Format(time.RFC3339))
	cmd, err := c.command(ws.Root())
	if err != nil {
		return err
	}
	cmd.Dir = ws.Root()
	cmd.Stdin = nil // /dev/null
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := c.startDetached(cmd); err != nil {
		return fmt.Errorf("mate core: starting %s: %w", cmd.Path, err)
	}
	pid := cmd.Process.Pid
	// Waited for only so a core that ends at once is told from one that is
	// slow to answer; it is in a session of its own and outlives this
	// process either way.
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	logName := filepath.Join(store.StateDirName, filepath.Base(ws.CoreLogFile()))
	deadline := c.now().Add(c.startWait)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		st, err := c.ask(ctx, ws.CoreSocket())
		cancel()
		if err == nil && st.PID == pid {
			line := "mate core: started (pid " + fmt.Sprint(pid) + ")"
			if st.URL != "" {
				line += " · " + st.URL
			}
			fmt.Fprintln(stdout, line)
			if st.TCPError != "" {
				fmt.Fprintln(stdout, officeCityError(st.TCPError))
			}
			return nil
		}
		select {
		case err := <-exited:
			return fmt.Errorf("mate core: pid %d ended at once (%v); see %s", pid, err, logName)
		default:
		}
		if !c.now().Before(deadline) {
			return fmt.Errorf("mate core: no answer within %s; see %s", c.startWait, logName)
		}
		time.Sleep(c.poll)
	}
}

// openCoreLog opens core.log to append to, after moving one past
// coreLogMax to core.log.1 (the one old log kept). The core's output goes
// to the file this process opens; rotating while a core writes it is not
// done (CON-213).
func openCoreLog(ws *store.Workspace) (*os.File, error) {
	path := ws.CoreLogFile()
	if fi, err := os.Stat(path); err == nil && fi.Size() > coreLogMax {
		if err := os.Rename(path, path+".1"); err != nil {
			return nil, fmt.Errorf("mate core: moving the old log: %w", err)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("mate core: opening the log: %w", err)
	}
	return f, nil
}

// stop is `mate core stop`: SIGTERM to the core holding core.lock, and
// SIGKILL when it still holds it after stopWait.
func (c coreControl) stop(ws *store.Workspace, stdout io.Writer) error {
	h, held, err := ws.CoreRunning()
	if err != nil {
		return fmt.Errorf("mate core: reading %s: %w", ws.CoreLockFile(), err)
	}
	if !held {
		_, err := fmt.Fprintln(stdout, "mate core: not running")
		return err
	}
	if h.Owner == core.OwnerConsole {
		return fmt.Errorf("mate core: the engine runs inside mate console (%s); quit that console", pidWord(h.PID))
	}
	if h.PID <= 0 {
		return fmt.Errorf("mate core: %s is held but names no process to stop", ws.CoreLockFile())
	}
	fmt.Fprintf(stdout, "mate core: stopping pid %d (at most %s)...\n", h.PID, c.stopWait)
	if err := c.signal(h.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("mate core: stopping pid %d: %w", h.PID, err)
	}
	if c.released(ws, c.stopWait) {
		_, err := fmt.Fprintln(stdout, "mate core: stopped")
		return err
	}
	fmt.Fprintf(stdout, "mate core: pid %d did not stop within %s; killing it\n", h.PID, c.stopWait)
	if err := c.signal(h.PID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("mate core: killing pid %d: %w", h.PID, err)
	}
	if !c.released(ws, c.killWait) {
		return fmt.Errorf("mate core: pid %d still holds %s after SIGKILL", h.PID, ws.CoreLockFile())
	}
	// A killed core leaves its socket; the next core removes it too, but
	// a client should not find a socket nothing listens on.
	if err := os.Remove(ws.CoreSocket()); err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(stdout, "mate core: removing %s: %v\n", ws.CoreSocket(), err)
	}
	_, err = fmt.Fprintln(stdout, "mate core: killed")
	return err
}

// restart is `mate core restart`: stop, then start, so the binary
// installed now is the one that runs.
func (c coreControl) restart(ws *store.Workspace, stdout io.Writer) error {
	if err := c.stop(ws, stdout); err != nil {
		return err
	}
	return c.start(ws, stdout)
}

// released waits up to d for core.lock to be free.
func (c coreControl) released(ws *store.Workspace, d time.Duration) bool {
	deadline := c.now().Add(d)
	for {
		if _, held, err := ws.CoreRunning(); err == nil && !held {
			return true
		}
		if !c.now().Before(deadline) {
			return false
		}
		time.Sleep(c.poll)
	}
}

// errCoreNotRunning is `mate core status` with no engine: exit 1.
var errCoreNotRunning = errors.New("mate core: not running")

// status is `mate core status`: what core.lock and the core's socket say
// about the engine running on ws, and whether the binary changed since it
// started. errCoreNotRunning when none runs.
func (c coreControl) status(ws *store.Workspace, stdout io.Writer) error {
	h, held, err := ws.CoreRunning()
	if err != nil {
		return fmt.Errorf("mate core: reading %s: %w", ws.CoreLockFile(), err)
	}
	if !held {
		return errCoreNotRunning
	}
	c.printStatus(ws, h, stdout)
	return nil
}

// printStatus says who runs the engine, its build and where it serves. The
// lock's record says it; the core's own answer on its socket, when it
// gives one, adds its notices.
func (c coreControl) printStatus(ws *store.Workspace, h store.CoreHolder, stdout io.Writer) {
	socketLine := ws.CoreSocket()
	var notices []string
	if h.Owner == core.OwnerConsole {
		socketLine = "none (the console serves no socket)"
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		st, err := c.ask(ctx, ws.CoreSocket())
		cancel()
		if err != nil {
			socketLine += " (not answering: " + err.Error() + ")"
		} else {
			notices = st.Notices
		}
	}
	fmt.Fprintf(stdout, "mate core · %s · %s\n", pidWord(h.PID), homeShort(ws.Root()))
	owner := h.Owner
	if owner == "" {
		owner = "unknown"
	}
	fmt.Fprintf(stdout, "  owner    %s\n", owner)
	version := h.Version
	if version == "" {
		version = "unknown"
	}
	if h.Build != "" {
		version += " (build " + shortBuild(h.Build) + ")"
	}
	fmt.Fprintf(stdout, "  version  %s\n", version)
	if h.Binary != "" {
		fmt.Fprintf(stdout, "  binary   %s\n", h.Binary)
	}
	if !h.Started.IsZero() {
		fmt.Fprintf(stdout, "  started  %s (%s ago)\n", c.clock(h.Started), formatAge(c.now().Sub(h.Started)))
	}
	fmt.Fprintf(stdout, "  socket   %s\n", socketLine)
	for _, n := range notices {
		fmt.Fprintf(stdout, "  notice   %s\n", n)
	}
	fmt.Fprint(stdout, officeCityLines(h.URL, h.LANURL, h.TCPError))
	fmt.Fprint(stdout, herdrLogLines(ws))
	if binaryChanged(h) {
		fmt.Fprintln(stdout, "stale: the mate binary changed after this core started; run `mate core restart`")
	}
}

// officeCityLines are the Office and City URLs, one line each, then the LAN
// one when the dashboard reaches the LAN; or the one line saying why
// neither is served over TCP.
func officeCityLines(url, lanURL, tcpErr string) string {
	if url == "" {
		if tcpErr == "" {
			return ""
		}
		return officeCityError(tcpErr) + "\n"
	}
	base := strings.TrimRight(url, "/")
	s := fmt.Sprintf("Office  %s/office/\nCity    %s/city/\n", base, base)
	if lanURL != "" {
		s += fmt.Sprintf("LAN     %s/office/\n", strings.TrimRight(lanURL, "/"))
	}
	return s
}

// herdrLogLines are the block's last two lines (decided 2026-11-06): the
// workspace's Herdr session, the one `herdr session attach` takes, and the
// absolute path of core.log.
func herdrLogLines(ws *store.Workspace) string {
	s := ""
	if session := ws.Session(); session != "" {
		s += "Herdr   " + session + "\n"
	}
	return s + "Log     " + ws.CoreLogFile() + "\n"
}

// coreLogTee is the core's output in the foreground: stdout and stderr,
// each also appended to core.log, which is opened as `start` opens it
// (openCoreLog, rotated past 10 MB). Nothing is added when self, this
// process's standard output, already is core.log - the core `start` ran,
// whose output goes there - nor when an engine runs, which writes core.log
// and is not rotated under it (runCore then refuses). close closes the log.
func coreLogTee(ws *store.Workspace, stdout, stderr io.Writer, self *os.File) (io.Writer, io.Writer, func(), error) {
	none := func() {}
	if _, held, err := ws.CoreRunning(); err != nil || held {
		return stdout, stderr, none, nil
	}
	if self != nil {
		if out, err := self.Stat(); err == nil {
			if log, err := os.Stat(ws.CoreLogFile()); err == nil && os.SameFile(out, log) {
				return stdout, stderr, none, nil
			}
		}
	}
	log, err := openCoreLog(ws)
	if err != nil {
		return nil, nil, nil, err
	}
	fmt.Fprintf(log, "--- mate core run %s ---\n", time.Now().Format(time.RFC3339))
	return bothWriter{stdout, log}, bothWriter{stderr, log}, func() { _ = log.Close() }, nil
}

// bothWriter writes to a and to b, and fails only when both do: a terminal
// that went away does not stop the log, nor a full disk the terminal.
type bothWriter struct{ a, b io.Writer }

func (w bothWriter) Write(p []byte) (int, error) {
	_, errA := w.a.Write(p)
	_, errB := w.b.Write(p)
	if errA != nil && errB != nil {
		return 0, errA
	}
	return len(p), nil
}

// listenAddr finds the address in a bind error: "listen tcp <addr>: ...".
var listenAddr = regexp.MustCompile(`listen tcp (\S+): `)

// officeCityError is the line for a dashboard not served over TCP: "<addr>
// is in use" for a busy port, else the bind's own error.
func officeCityError(tcpErr string) string {
	if strings.Contains(tcpErr, syscall.EADDRINUSE.Error()) {
		if m := listenAddr.FindStringSubmatch(tcpErr); m != nil {
			return "Office/City: " + m[1] + " is in use"
		}
	}
	return "Office/City: not served: " + tcpErr
}

// binaryChanged reports whether the file the core started from is no
// longer the build it recorded: an install over it, or none there at all.
// A core that recorded no build is not called stale.
func binaryChanged(h store.CoreHolder) bool {
	if h.Binary == "" || h.Build == "" {
		return false
	}
	f, err := os.Open(h.Binary)
	if err != nil {
		return true
	}
	defer f.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return false
	}
	return hex.EncodeToString(sum.Sum(nil)) != h.Build
}

func shortBuild(sum string) string {
	if len(sum) > 12 {
		return sum[:12]
	}
	return sum
}

// clock is t as HH:MM, with the day when it is not today.
func (c coreControl) clock(t time.Time) string {
	if t.IsZero() {
		return "an unknown time"
	}
	at, now := t.Local(), c.now()
	if y, m, d := at.Date(); y != now.Year() || m != now.Month() || d != now.Day() {
		return at.Format("2 Jan 15:04")
	}
	return at.Format("15:04")
}

// homeShort is path with the home directory written as ~.
func homeShort(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if rel, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
		return "~" + string(filepath.Separator) + rel
	}
	return path
}
