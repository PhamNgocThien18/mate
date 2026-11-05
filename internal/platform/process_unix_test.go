//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package platform_test

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/nguyenngocanh94/mate/internal/platform"
)

// TestStartNewSessionLeadsASessionOfItsOwn: the command is the leader of a
// new session and of its group, with no terminal of this process's.
func TestStartNewSessionLeadsASessionOfItsOwn(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	if err := platform.StartNewSession(cmd); err != nil {
		t.Fatalf("StartNewSession = %v", err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	if sid, err := unix.Getsid(pid); err != nil || sid != pid {
		t.Fatalf("Getsid(%d) = %d, %v; want the command's own pid", pid, sid, err)
	}
	if pgid, err := unix.Getpgid(pid); err != nil || pgid != pid {
		t.Fatalf("Getpgid(%d) = %d, %v; want the command's own pid", pid, pgid, err)
	}
}

// gone waits for pid to stop existing. A process killed with its parent is
// reparented and reaped by init, so it may outlast the signal by a moment.
func gone(t *testing.T, pid int) bool {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// TestSignalGroupReachesWhatTheLeaderStarted: a command started with
// StartNewGroup leads a group of its own, and SignalGroup reaches every
// process in it, a grandchild included.
func TestSignalGroupReachesWhatTheLeaderStarted(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 60 & echo $!; wait")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := platform.StartNewGroup(cmd); err != nil {
		t.Fatalf("StartNewGroup = %v", err)
	}
	leader := cmd.Process.Pid
	t.Cleanup(func() { _ = syscall.Kill(-leader, syscall.SIGKILL) })
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	sleeper, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	for _, pid := range []int{leader, sleeper} {
		if pgid, err := unix.Getpgid(pid); err != nil || pgid != leader {
			t.Fatalf("Getpgid(%d) = %d, %v; want the leader's pid %d", pid, pgid, err, leader)
		}
	}
	if pgid, _ := unix.Getpgid(os.Getpid()); pgid == leader {
		t.Fatal("the test runs in the new group; want the group to be the command's alone")
	}

	if err := platform.SignalGroup(leader, syscall.SIGKILL); err != nil {
		t.Fatalf("SignalGroup = %v", err)
	}
	var exit *exec.ExitError
	if err := cmd.Wait(); !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatalf("the leader's Wait = %v; want it killed by SIGKILL", err)
	}
	if !gone(t, sleeper) {
		t.Fatalf("the leader's child %d outlived SignalGroup", sleeper)
	}
}

// TestSignalGroupRefusesAGroupThatIsNotOne: kill(2) reads -1 as every
// process the user may signal and 0 as this process's group, so a pgid
// below 2 is refused, and nothing is sent. The probe is signal 0.
func TestSignalGroupRefusesAGroupThatIsNotOne(t *testing.T) {
	for _, pgid := range []int{1, 0, -os.Getpid()} {
		if err := platform.SignalGroup(pgid, 0); !errors.Is(err, syscall.EINVAL) {
			t.Errorf("SignalGroup(%d, 0) = %v; want EINVAL", pgid, err)
		}
	}
}

func TestSignalZeroTellsWhetherAProcessExists(t *testing.T) {
	if err := platform.Signal(os.Getpid(), 0); err != nil {
		t.Fatalf("Signal(self, 0) = %v; want nil", err)
	}
	reaped := exec.Command("true")
	if err := reaped.Run(); err != nil {
		t.Fatal(err)
	}
	if err := platform.Signal(reaped.Process.Pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("Signal(reaped child, 0) = %v; want ESRCH", err)
	}
}

// TestSignalRefusesAPidThatNamesNoProcess: kill(2) reads 0 as this process's
// group, -1 as every process the user may signal and -pid as a group.
// Signal names one process, so it refuses them all and sends nothing. The
// probe is signal 0, so a Signal that did send would still harm nothing.
func TestSignalRefusesAPidThatNamesNoProcess(t *testing.T) {
	for _, pid := range []int{0, -1, -os.Getpid()} {
		if err := platform.Signal(pid, 0); !errors.Is(err, syscall.EINVAL) {
			t.Errorf("Signal(%d, 0) = %v; want EINVAL", pid, err)
		}
	}
}
