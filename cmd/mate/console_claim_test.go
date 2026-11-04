package main

import (
	"bytes"
	"errors"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/store"
)

// claimHarness is a workspace whose console lock an "old console" holds,
// taken through a second file handle so flock refuses this process's claim
// exactly as it refuses another process's.
type claimHarness struct {
	ws      *store.Workspace
	release func()
	signals []syscall.Signal
	out     bytes.Buffer
}

func newClaimHarness(t *testing.T) *claimHarness {
	t.Helper()
	ws, err := store.Init(t.TempDir(), workspaceDefaults())
	if err != nil {
		t.Fatal(err)
	}
	h := &claimHarness{ws: ws}
	started := time.Now().Add(-97 * time.Minute)
	h.release, err = ws.LockConsole(store.ConsoleHolder{PID: 43674, Started: started})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.releaseOnce() })
	return h
}

func (h *claimHarness) releaseOnce() {
	if h.release != nil {
		h.release()
		h.release = nil
	}
}

// claim answers the prompt with answer; quitsOn is the signal the old
// console lets go of the lock on (0: it never does).
func (h *claimHarness) claim(answer string, quitsOn syscall.Signal) consoleClaim {
	return consoleClaim{
		in:  strings.NewReader(answer),
		out: &h.out,
		now: time.Now,
		signal: func(pid int, sig syscall.Signal) error {
			if pid != 43674 {
				return errors.New("signalled the wrong process")
			}
			h.signals = append(h.signals, sig)
			if sig == quitsOn {
				h.releaseOnce()
			}
			return nil
		},
		where:    func(int) string { return "ttys012" },
		grace:    50 * time.Millisecond,
		killWait: 50 * time.Millisecond,
		poll:     time.Millisecond,
	}
}

func TestConsoleClaimTakesAFreeWorkspaceWithoutAsking(t *testing.T) {
	ws, err := store.Init(t.TempDir(), workspaceDefaults())
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	c := consoleClaim{in: strings.NewReader(""), out: &out, now: time.Now}
	unlock, err := c.take(ws)
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	defer unlock()
	if out.Len() != 0 {
		t.Fatalf("a free workspace printed %q; want nothing", out.String())
	}
}

func TestConsoleClaimLeavesTheOldConsoleWhenTheCaptainSaysNo(t *testing.T) {
	for _, answer := range []string{"n\n", "\n", ""} {
		h := newClaimHarness(t)
		_, err := h.claim(answer, syscall.SIGTERM).take(h.ws)
		if err == nil || !strings.Contains(err.Error(), "console 43674 is still running") {
			t.Fatalf("answer %q: err = %v; want the old console named as still running", answer, err)
		}
		if len(h.signals) != 0 {
			t.Fatalf("answer %q signalled %v; want nothing sent", answer, h.signals)
		}
		for _, want := range []string{"another console is already running on " + h.ws.Root(), "pid 43674, opened ", "(1h ago)", "on ttys012", "[y/N]"} {
			if !strings.Contains(h.out.String(), want) {
				t.Fatalf("answer %q: prompt %q lacks %q", answer, h.out.String(), want)
			}
		}
		h.releaseOnce()
	}
}

func TestConsoleClaimQuitsTheOldConsoleAndTakesTheLock(t *testing.T) {
	h := newClaimHarness(t)
	unlock, err := h.claim("y\n", syscall.SIGTERM).take(h.ws)
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	defer unlock()
	if len(h.signals) != 1 || h.signals[0] != syscall.SIGTERM {
		t.Fatalf("signals = %v; want one SIGTERM", h.signals)
	}
	if holder, err := h.ws.ConsoleHolder(); err != nil || holder.PID == 43674 {
		t.Fatalf("holder after takeover = %+v, %v; want this console", holder, err)
	}
}

func TestConsoleClaimKillsAnOldConsoleThatDoesNotQuit(t *testing.T) {
	h := newClaimHarness(t)
	unlock, err := h.claim("yes\n", syscall.SIGKILL).take(h.ws)
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	defer unlock()
	if len(h.signals) != 2 || h.signals[0] != syscall.SIGTERM || h.signals[1] != syscall.SIGKILL {
		t.Fatalf("signals = %v; want SIGTERM then SIGKILL", h.signals)
	}
	if !strings.Contains(h.out.String(), "did not quit within") {
		t.Fatalf("output %q does not say the old console was killed", h.out.String())
	}
}

func TestConsoleClaimGivesUpWhenTheLockOutlivesSIGKILL(t *testing.T) {
	h := newClaimHarness(t)
	_, err := h.claim("y\n", 0).take(h.ws)
	if err == nil || !strings.Contains(err.Error(), "still holds") {
		t.Fatalf("err = %v; want the lock named as still held", err)
	}
}
