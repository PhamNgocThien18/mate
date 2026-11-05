//go:build !darwin && !dragonfly && !freebsd && !illumos && !linux && !netbsd && !openbsd

package platform_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/platform"
)

// TestEveryCallRefusesOffTheList: off the flock list every call fails,
// names the platform and takes, sends or starts nothing (CON-177: mate does
// not run without its locks). Run it with GOOS=js GOARCH=wasm, or on the
// platform itself.
func TestEveryCallRefusesOffTheList(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "x.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	refused := func(call string, err error) {
		t.Helper()
		if !errors.Is(err, errors.ErrUnsupported) || !strings.Contains(err.Error(), runtime.GOOS) {
			t.Errorf("%s = %v; want an error naming %s that matches errors.ErrUnsupported", call, err, runtime.GOOS)
		}
	}
	refused("Lock", platform.Lock(f))
	refused("RLock", platform.RLock(f))
	refused("Unlock", platform.Unlock(f))
	for call, try := range map[string]func(*os.File) (bool, error){"TryLock": platform.TryLock, "TryRLock": platform.TryRLock} {
		ok, err := try(f)
		refused(call, err)
		if ok {
			t.Errorf("%s took a lock", call)
		}
	}
	g, err := platform.OpenNoFollow(f.Name(), os.O_RDWR, 0)
	refused("OpenNoFollow", err)
	if g != nil {
		t.Error("OpenNoFollow opened the file")
	}
	refused("Signal", platform.Signal(os.Getpid(), 0))
	refused("SignalGroup", platform.SignalGroup(os.Getpid(), syscall.SIGTERM))
	for call, start := range map[string]func(*exec.Cmd) error{"StartNewGroup": platform.StartNewGroup, "StartNewSession": platform.StartNewSession, "StartOnTerminal": platform.StartOnTerminal} {
		cmd := exec.Command("true")
		refused(call, start(cmd))
		if cmd.Process != nil {
			t.Errorf("%s started the command", call)
		}
	}
}
