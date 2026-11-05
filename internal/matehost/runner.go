package matehost

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/platform"
)

// ErrBusy is a turn refused because another one holds the turn lock.
var ErrBusy = errors.New("the Mate is busy with another message")

// LockFunc takes the turn lock without waiting.
type LockFunc func() (release func(), acquired bool, err error)

// Runner starts one harness run per message.
type Runner struct {
	Launch Launch
	// Turns is the harness's headless contract (harness.HeadlessTurns).
	Turns harness.HeadlessTurns
	// Lock is the turn lock (store.TryMateLock(project, TurnLock)).
	Lock LockFunc
	// Opened reports whether the session already exists; nil means
	// Turns.SessionOpened.
	Opened func(dir, sessionID string) bool
	// Grace is how long an interrupted run has to end before it is killed;
	// zero means 10 seconds.
	Grace time.Duration

	// turns counts the runs this runner started; Start is called by one
	// host, under its lock.
	turns int
}

// Reply is how one run ended.
type Reply struct {
	Text        string
	SessionID   string
	IsError     bool
	Interrupted bool
	// DurationMS and Tokens are the result's (harness.TurnEvent).
	DurationMS int64
	Tokens     int
	// Err is why the run produced no result: it failed to start, exited
	// without one, or was killed.
	Err error
}

// Run is one harness run in flight. Its lock is held until Wait returns.
type Run struct {
	cmd     *exec.Cmd
	stdout  io.ReadCloser
	stderr  *tailBuffer
	release func()
	grace   time.Duration
	turns   harness.HeadlessTurns

	mu          sync.Mutex
	interrupted bool
}

// Start takes the turn lock and starts the run for text. ErrBusy means the
// lock is held: nothing was started.
func (r *Runner) Start(text string) (*Run, error) {
	if r.Lock == nil {
		return nil, errors.New("matehost: the runner has no turn lock")
	}
	if r.Turns == nil {
		return nil, errors.New("matehost: the runner has no harness to run")
	}
	release, ok, err := r.Lock()
	if err != nil {
		return nil, fmt.Errorf("matehost: turn lock: %w", err)
	}
	if !ok {
		return nil, ErrBusy
	}
	l := r.Launch
	r.turns++
	return startRun(l, r.Turns.TurnArgs(l.Args, l.SessionID, r.opened(l.Dir, l.SessionID)), r.turns, text, r.Turns, r.Grace, release)
}

// startRun starts one harness run of args in the launch's directory and
// environment, with text on its stdin. release is called when the run's
// Wait returns, or now when it fails to start.
func startRun(l Launch, args []string, turn int, text string, turns harness.HeadlessTurns, grace time.Duration, release func()) (*Run, error) {
	cmd := exec.Command(l.Program, args...)
	cmd.Dir = l.Dir
	cmd.Env = turnEnv(os.Environ(), l, turn)
	cmd.Stdin = strings.NewReader(text)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		release()
		return nil, err
	}
	stderr := &tailBuffer{max: 4096}
	cmd.Stderr = stderr
	if err := platform.StartNewGroup(cmd); err != nil {
		release()
		return nil, fmt.Errorf("matehost: start %s: %w", l.Program, err)
	}
	if grace <= 0 {
		grace = 10 * time.Second
	}
	return &Run{cmd: cmd, stdout: stdout, stderr: stderr, release: release, grace: grace, turns: turns}, nil
}

// opened reports whether the harness has created the session sessionID
// in dir.
func (r *Runner) opened(dir, sessionID string) bool {
	if r.Opened != nil {
		return r.Opened(dir, sessionID)
	}
	return r.Turns != nil && r.Turns.SessionOpened(dir, sessionID)
}

// turnEnv is the run's environment: the host's own, without what the
// launch unsets and without the nested-session variables of whatever
// Claude Code session started the Herdr server (harness.NestedSessionEnv:
// measured 2026-10-15, a `claude -p` that inherits them fails with 401),
// then the launch's own variables and EnvHostTurn, the run's number in
// this host.
func turnEnv(base []string, l Launch, turn int) []string {
	drop := func(key string) bool {
		if harness.IsNestedSessionEnv(key) || slices.Contains(l.UnsetEnv, key) || key == EnvHostTurn {
			return true
		}
		for _, kv := range l.Env {
			if k, _, _ := strings.Cut(kv, "="); k == key {
				return true
			}
		}
		return false
	}
	out := make([]string, 0, len(base)+len(l.Env)+1)
	for _, kv := range base {
		if k, _, _ := strings.Cut(kv, "="); !drop(k) {
			out = append(out, kv)
		}
	}
	out = append(out, l.Env...)
	return append(out, fmt.Sprintf("%s=%d", EnvHostTurn, turn))
}

// Interrupt asks the run to stop (SIGINT to its process group), and kills
// it if it is still running after the grace period.
func (r *Run) Interrupt() {
	r.mu.Lock()
	first := !r.interrupted
	r.interrupted = true
	r.mu.Unlock()
	if !first || r.cmd.Process == nil {
		return
	}
	pid := r.cmd.Process.Pid
	_ = platform.SignalGroup(pid, syscall.SIGINT)
	time.AfterFunc(r.grace, func() { _ = platform.SignalGroup(pid, syscall.SIGKILL) })
}

// Kill ends the run now.
func (r *Run) Kill() {
	r.mu.Lock()
	r.interrupted = true
	r.mu.Unlock()
	if r.cmd.Process != nil {
		_ = platform.SignalGroup(r.cmd.Process.Pid, syscall.SIGKILL)
	}
}

// Wait reads the run's events to the end, handing each to on (which may be
// nil), and releases the turn lock.
func (r *Run) Wait(on func(harness.TurnEvent)) Reply {
	defer r.release()
	var reply Reply
	got := false
	sc := bufio.NewScanner(r.stdout)
	sc.Buffer(make([]byte, 64*1024), 32*1024*1024)
	for sc.Scan() {
		ev, ok := r.turns.ParseTurnEvent(sc.Bytes())
		if !ok {
			continue
		}
		if on != nil {
			on(ev)
		}
		if ev.Final {
			got = true
			reply.Text, reply.SessionID, reply.IsError = ev.Text, ev.SessionID, ev.IsError
			reply.DurationMS, reply.Tokens = ev.DurationMS, ev.Tokens
		}
	}
	_, _ = io.Copy(io.Discard, r.stdout)
	err := r.cmd.Wait()
	r.mu.Lock()
	reply.Interrupted = r.interrupted
	r.mu.Unlock()
	if !got && !reply.Interrupted {
		msg := strings.TrimSpace(r.stderr.String())
		switch {
		case err != nil && msg != "":
			reply.Err = fmt.Errorf("%v: %s", err, lastLine(msg))
		case err != nil:
			reply.Err = err
		default:
			reply.Err = errors.New("the run ended without a result")
		}
	}
	return reply
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf bytes.Buffer
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf.Write(p)
	if over := t.buf.Len() - t.max; over > 0 {
		t.buf.Next(over)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.buf.String()
}
