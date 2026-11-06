package spawn

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/platform"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// A headless Mate's host runs as a background process of its own, outside
// Herdr (CON-225): no tab, no pane, no Herdr agent. Its socket says whether
// it is alive, its host lock (matehost.HostLock) whether its process is
// still there, and its pid in mate.meta is what a stop signals when the
// socket's stop op does not end it.

// HostSpec is one start of a host process.
type HostSpec struct {
	// Argv is the command, the mate binary first.
	Argv []string
	// Dir is its working directory, the Mate directory.
	Dir string
	// Env is its whole environment (hostEnv).
	Env []string
	// Log is the file its stdout and stderr go to (matehost.LogPath).
	Log string
}

// HostProcesses starts a headless Mate's host and signals it. ExecHosts is
// the live one; tests play the host in-process.
type HostProcesses interface {
	// StartHost starts the host detached from this process and returns
	// its pid, and a channel closed when it exits, nil when the starter
	// cannot tell.
	StartHost(spec HostSpec) (pid int, exited <-chan struct{}, err error)
	// SignalHost sends sig to the host's pid.
	SignalHost(pid int, sig syscall.Signal) error
}

// HostFuncs is HostProcesses over two functions, for a caller that plays
// the host itself (tests run it in-process on the Mate's socket). A nil
// Signal sends nothing.
type HostFuncs struct {
	Start  func(spec HostSpec) (pid int, exited <-chan struct{}, err error)
	Signal func(pid int, sig syscall.Signal) error
}

// StartHost implements HostProcesses.
func (f HostFuncs) StartHost(spec HostSpec) (int, <-chan struct{}, error) { return f.Start(spec) }

// SignalHost implements HostProcesses.
func (f HostFuncs) SignalHost(pid int, sig syscall.Signal) error {
	if f.Signal == nil {
		return nil
	}
	return f.Signal(pid, sig)
}

// HostLogMax is the size past which a host's log is rotated when the next
// host starts: the log becomes host.log.1, replacing the one before, and
// the new host writes a fresh host.log. A host writes a line when it
// starts and its errors, so two files of this size hold many runs.
const HostLogMax = 1 << 20

// ExecHosts starts the host with os/exec as the leader of a session of its
// own, with no terminal (platform.StartNewSession): it outlives the
// console, the CLI command or the Herdr pane that started it, and their
// hangup does not reach it.
type ExecHosts struct{}

// StartHost implements HostProcesses.
func (ExecHosts) StartHost(spec HostSpec) (int, <-chan struct{}, error) {
	if len(spec.Argv) == 0 {
		return 0, nil, errors.New("spawn: a host start names no command")
	}
	if err := rotateHostLog(spec.Log, HostLogMax); err != nil {
		return 0, nil, err
	}
	log, err := os.OpenFile(spec.Log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, nil, err
	}
	defer log.Close()
	cmd := exec.Command(spec.Argv[0], spec.Argv[1:]...)
	cmd.Dir, cmd.Env = spec.Dir, spec.Env
	cmd.Stdout, cmd.Stderr = log, log
	if err := platform.StartNewSession(cmd); err != nil {
		return 0, nil, fmt.Errorf("spawn: start the headless host: %w", err)
	}
	// Waited for, so a host that exits while the console that started it
	// runs is reaped rather than left a zombie whose pid still answers.
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	return cmd.Process.Pid, exited, nil
}

// SignalHost implements HostProcesses.
func (ExecHosts) SignalHost(pid int, sig syscall.Signal) error { return platform.Signal(pid, sig) }

// rotateHostLog moves a log of max bytes or more to path.1.
func rotateHostLog(path string, max int64) error {
	info, err := os.Stat(path)
	if err != nil || info.Size() < max {
		return nil
	}
	return os.Rename(path, path+".1")
}

// hostEnv is the host's environment: this process's, less what named
// another agent or a pane, plus the Mate's own (mateEnv). A host in its
// Herdr pane got the Mate's identity from the pane; one started from the
// console must not take the console's caller, a Crew's identity when a
// Crew ran the start, or the pane id of a Herdr pane it does not run in.
func hostEnv(base []string, mate []runtime.EnvVar) []string {
	set := map[string]bool{}
	for _, v := range mate {
		set[v.Key] = true
	}
	out := make([]string, 0, len(base)+len(mate)+1)
	var path, home, xdgBin string
	for _, kv := range base {
		key, value, _ := strings.Cut(kv, "=")
		switch key {
		case "PATH":
			path = value
			continue
		case "HOME":
			home = value
		case "XDG_BIN_HOME":
			xdgBin = value
		}
		if set[key] || refusedHostEnvKey(key) {
			continue
		}
		out = append(out, kv)
	}
	if !set["PATH"] {
		out = append(out, "PATH="+hostPath(path, home, xdgBin))
	}
	for _, v := range mate {
		out = append(out, v.Key+"="+v.Value)
	}
	return out
}

// hostPath is the host's PATH: its starter's, then where installers put a
// CLI that PATH may not reach (the console's toolDirs). A pane's shell
// read the captain's profile; a host started from a console opened from
// the Dock has only login's PATH, and its turns must still find the
// harness and mate's tools.
func hostPath(path, home, xdgBin string) string {
	dirs := filepath.SplitList(path)
	extra := []string{xdgBin}
	if home != "" {
		extra = append(extra, filepath.Join(home, ".local", "bin"))
	}
	for _, d := range append(extra, "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin") {
		if d != "" && !slices.Contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	return strings.Join(dirs, string(filepath.ListSeparator))
}

// refusedHostEnvKey is a variable the host is not given: every MATE_ one
// (the Herdr server refuses them for its panes too), Herdr's per-pane
// ones, and a parent Claude Code session's.
func refusedHostEnvKey(key string) bool {
	switch {
	case strings.HasPrefix(key, "MATE_"):
		return true
	case strings.HasPrefix(key, "HERDR_"):
		return key != "HERDR_CONFIG_PATH" && key != "HERDR_BIN_PATH"
	}
	return harness.IsNestedSessionEnv(key)
}

func (d Deps) hosts() HostProcesses {
	if d.Hosts != nil {
		return d.Hosts
	}
	return ExecHosts{}
}

func (d Deps) hostStopWait() time.Duration {
	if d.HostStopWait > 0 {
		return d.HostStopWait
	}
	return HostStopWait
}

// hostPingTimeout bounds one liveness question to a host.
const hostPingTimeout = time.Second

// HostLiveness asks the host of every headless Mate that runs as a process
// of its own (store.MateHostDetached) whether it is alive, all at once:
// project -> alive (hostAlive). A project whose Mate is not such a host,
// or whose question ctx cut short, is left out, which query.Liveness
// reads as not asked.
func HostLiveness(ctx context.Context, w *store.Workspace) map[string]bool {
	out := map[string]bool{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, ref := range w.Projects() {
		meta, err := w.ReadMateMeta(ref.Name)
		if err != nil || !store.MateHostDetached(meta) {
			continue
		}
		wg.Add(1)
		go func(project string) {
			defer wg.Done()
			alive, asked := hostAlive(ctx, w, project)
			if !asked {
				return
			}
			mu.Lock()
			out[project] = alive
			mu.Unlock()
		}(ref.Name)
	}
	wg.Wait()
	return out
}

// hostAlive is whether project's host of its own is alive: it answers on
// its socket, or, wedged, its process still holds the host lock (a stop
// ends it; a start would be refused). A host that died lets the lock go
// with it. asked is false when ctx ended before the socket answered: that
// says nothing about the host.
func hostAlive(ctx context.Context, w *store.Workspace, project string) (alive, asked bool) {
	if pingHost(ctx, w.MateDir(project)) == nil {
		return true, true
	}
	if ctx.Err() != nil {
		return false, false
	}
	held, err := hostLockHeld(w, project)
	if err != nil {
		return false, false
	}
	return held, true
}

func pingHost(ctx context.Context, mateDir string) error {
	c := matehost.Dial(mateDir)
	c.Timeout = hostPingTimeout
	return c.Ping(ctx)
}

// hostLockHeld reports whether a host process holds project's host lock:
// whether one is still there, answering or not.
func hostLockHeld(w *store.Workspace, project string) (bool, error) {
	release, ok, err := w.TryMateLock(project, matehost.HostLock)
	if err != nil {
		return false, err
	}
	if ok {
		release()
		return false, nil
	}
	return true, nil
}

// hostPID is the pid mate.meta records for the host, 0 for none.
func hostPID(meta map[string]string) int {
	pid, err := strconv.Atoi(strings.TrimSpace(meta[store.MetaMatePID]))
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}

// Polls of hostGone, 100 ms apart: the wait for a host to leave after its
// stop op (hostExitPolls, 10 s) and after each signal (2 s).
const (
	hostExitPolls   = 100
	hostSignalPolls = 20
)

// hostGone waits up to polls for project's host process to let its lock go.
func hostGone(ctx context.Context, w *store.Workspace, deps Deps, project string, polls int) bool {
	for range polls {
		if held, err := hostLockHeld(w, project); err == nil && !held {
			return true
		}
		if deps.sleep()(ctx, 100*time.Millisecond) != nil {
			break
		}
	}
	held, err := hostLockHeld(w, project)
	return err == nil && !held
}

// endHost ends a host that runs as a process of its own: the socket's stop
// op first, when the host answers, which lets a turn in flight finish
// within the stop wait; then, when the process stays, SIGTERM, on which
// the host kills its turn and exits, and at last SIGKILL, to pid. A signal
// goes only to a process that still holds the host lock, and not when the
// host that answers names another pid; a pid reused by a process that
// holds no lock is never signalled. forced says why the end was not the
// host's own clean exit, empty when it was; an error means the process is
// still there.
func endHost(ctx context.Context, w *store.Workspace, deps Deps, project string, pid int, answers bool) (string, error) {
	wait := deps.hostStopWait()
	client := matehost.Dial(w.MateDir(project))
	forced := "the headless host did not answer on its socket"
	if answers {
		forced = ""
		killed, err := client.Stop(ctx, wait, false)
		switch {
		case err != nil:
			forced = fmt.Sprintf("the headless host did not answer its stop (%s)", oneLineErr(err))
		case killed:
			forced = fmt.Sprintf("the headless Mate's turn was still running after %s and was killed", wait)
		}
	}
	// The rest runs to its end even once ctx is done: a stop cut short
	// must still give the host its SIGTERM, and the time to kill its turn,
	// before a SIGKILL that would leave the turn running on its own.
	ctx, cancel := cleanupContext(ctx)
	defer cancel()
	if hostGone(ctx, w, deps, project, hostExitPolls) {
		return forced, nil
	}
	if pid <= 0 {
		return forced, observability.NewError(observability.CodeUnknown,
			"the headless host still runs after its stop and mate.meta records no pid to signal; stop is not confirmed")
	}
	client.Timeout = hostPingTimeout
	if st, err := client.State(ctx); err == nil && st.PID > 0 && st.PID != pid {
		return forced, observability.NewError(observability.CodeUnknown, fmt.Sprintf(
			"the headless host that answers is pid %d, not pid %d as mate.meta records; nothing was signalled, stop is not confirmed", st.PID, pid))
	}
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL} {
		if err := deps.hosts().SignalHost(pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
			return forced, observability.WrapError(observability.CodeUnknown,
				fmt.Sprintf("the headless host pid %d could not be sent %s", pid, sigName(sig)), err)
		}
		forced = fmt.Sprintf("the headless host did not exit after its stop; sent %s to pid %d", sigName(sig), pid)
		if hostGone(ctx, w, deps, project, hostSignalPolls) {
			return forced, nil
		}
	}
	return forced, observability.NewError(observability.CodeUnknown,
		fmt.Sprintf("the headless host pid %d still runs after SIGKILL; stop is not confirmed", pid))
}

func sigName(sig syscall.Signal) string {
	switch sig {
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGKILL:
		return "SIGKILL"
	}
	return sig.String()
}

// waitHostUp waits, within the readiness timeout, for the host just
// started as pid to answer on its socket, under its own pid: another host
// answering there (a start beside this one) is not it. A host that exits
// first fails at once, with the end of its log.
func waitHostUp(ctx context.Context, deps Deps, mateDir string, pid int, exited <-chan struct{}) error {
	ctx, cancel := context.WithTimeout(ctx, deps.readinessTimeout())
	defer cancel()
	client := matehost.Dial(mateDir)
	client.Timeout = hostPingTimeout
	var last error
	for {
		st, err := client.State(ctx)
		switch {
		case err != nil:
			last = err
		case pid > 0 && st.PID > 0 && st.PID != pid:
			last = fmt.Errorf("the host answering on its socket is pid %d, not the one just started (pid %d)", st.PID, pid)
		default:
			return nil
		}
		select {
		case <-exited:
			return observability.NewError(observability.CodeUnknown, fmt.Sprintf(
				"the headless Mate's host exited before it answered on its socket; the end of %s:\n%s",
				matehost.LogPath(mateDir), logTail(matehost.LogPath(mateDir), 10)))
		default:
		}
		if err := deps.sleep()(ctx, 100*time.Millisecond); err != nil {
			return observability.WrapError(observability.CodeUnknown, fmt.Sprintf(
				"the headless Mate's host did not answer on its socket within %s (its log: %s)",
				deps.readinessTimeout(), matehost.LogPath(mateDir)), last)
		}
	}
}

// logTail is the last n lines of the file at path, for an error.
func logTail(path string, n int) string {
	f, err := os.Open(path)
	if err != nil {
		return "(no log: " + err.Error() + ")"
	}
	defer f.Close()
	const most = 8 << 10
	if info, err := f.Stat(); err == nil && info.Size() > most {
		_, _ = f.Seek(info.Size()-most, io.SeekStart)
	}
	data, _ := io.ReadAll(f)
	lines := bytes.Split(bytes.TrimRight(data, "\n"), []byte("\n"))
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return string(bytes.Join(lines, []byte("\n")))
}
