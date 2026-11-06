package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/nguyenngocanh94/mate/internal/platform"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// consoleClaim is how a console takes its workspace (CON-162): one
// workspace runs one console. When another console holds
// `.mate/console.lock`, the captain is told which one and asked whether to
// quit it. Yes sends it SIGTERM, which Bubble Tea turns into a quit, so the
// old console stops its observer and daemons, closes the database and its
// columns as `q` would; one that has not let go after grace is killed.
// Anything but yes leaves it running and opens nothing.
type consoleClaim struct {
	in  io.Reader
	out io.Writer
	now func() time.Time
	// signal sends sig to the old console.
	signal func(pid int, sig syscall.Signal) error
	// where says which terminal pid runs in, "" when that is unknown.
	where    func(pid int) string
	grace    time.Duration
	killWait time.Duration
	poll     time.Duration
}

func liveConsoleClaim(in io.Reader, out io.Writer) consoleClaim {
	return consoleClaim{
		in:       in,
		out:      out,
		now:      time.Now,
		signal:   platform.Signal,
		where:    consoleTTY,
		grace:    10 * time.Second,
		killWait: 3 * time.Second,
		poll:     100 * time.Millisecond,
	}
}

// take returns what releases the console lock once this console holds it.
// The record says core_lock: this console's engine takes core.lock, so
// `mate core run` can tell it from a console built before CON-211.
func (c consoleClaim) take(ws *store.Workspace) (unlock func(), err error) {
	self := store.ConsoleHolder{PID: os.Getpid(), Started: c.now(), CoreLock: true}
	unlock, err = ws.LockConsole(self)
	if !errors.Is(err, store.ErrConsoleRunning) {
		return unlock, err
	}
	holder, herr := ws.ConsoleHolder()
	if herr != nil || holder.PID <= 0 || holder.PID == self.PID {
		return nil, fmt.Errorf("another console is running on %s and its lock names no process to ask; quit it by hand", ws.Root())
	}
	fmt.Fprintf(c.out, "mate: another console is already running on %s\n  %s\nOne workspace runs one console. Quit that console and open this one? [y/N] ", ws.Root(), c.describe(holder))
	if !c.yes() {
		return nil, fmt.Errorf("console %d is still running on %s; quit it or answer y", holder.PID, ws.Root())
	}

	fmt.Fprintf(c.out, "asking console %d to quit...\n", holder.PID)
	if err := c.signal(holder.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return nil, fmt.Errorf("asking console %d to quit: %w", holder.PID, err)
	}
	if unlock, err := c.wait(ws, self, c.grace); unlock != nil || err != nil {
		return unlock, err
	}
	fmt.Fprintf(c.out, "console %d did not quit within %s; killing it\n", holder.PID, c.grace)
	if err := c.signal(holder.PID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return nil, fmt.Errorf("killing console %d: %w", holder.PID, err)
	}
	if unlock, err := c.wait(ws, self, c.killWait); unlock != nil || err != nil {
		return unlock, err
	}
	return nil, fmt.Errorf("console %d still holds %s after SIGKILL", holder.PID, ws.ConsoleLockFile())
}

// wait retries the lock until it is free or d has passed. Both results nil
// means the old console still holds it.
func (c consoleClaim) wait(ws *store.Workspace, self store.ConsoleHolder, d time.Duration) (func(), error) {
	deadline := c.now().Add(d)
	for {
		unlock, err := ws.LockConsole(self)
		if !errors.Is(err, store.ErrConsoleRunning) {
			return unlock, err
		}
		if !c.now().Before(deadline) {
			return nil, nil
		}
		time.Sleep(c.poll)
	}
}

func (c consoleClaim) yes() bool {
	line, _ := bufio.NewReader(c.in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

// describe is the line naming the old console: its pid, when it opened and,
// when known, the terminal it runs in.
func (c consoleClaim) describe(h store.ConsoleHolder) string {
	s := "pid " + strconv.Itoa(h.PID)
	if !h.Started.IsZero() {
		now := c.now()
		at := h.Started.Local()
		layout := "15:04"
		if y, m, d := at.Date(); y != now.Year() || m != now.Month() || d != now.Day() {
			layout = "2 Jan 15:04"
		}
		s += ", opened " + at.Format(layout) + " (" + formatAge(now.Sub(h.Started)) + " ago)"
	}
	if c.where != nil {
		if tty := c.where(h.PID); tty != "" {
			s += ", on " + tty
		}
	}
	return s
}

// consoleTTY asks ps for the terminal pid runs in.
func consoleTTY(pid int) string {
	out, err := exec.Command("ps", "-o", "tty=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	tty := strings.TrimSpace(string(out))
	if tty == "" || strings.HasPrefix(tty, "?") {
		return ""
	}
	return tty
}
