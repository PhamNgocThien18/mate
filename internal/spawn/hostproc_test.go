package spawn

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/runtime"
)

func TestHostEnvKeepsTheMatesOwnAndDropsWhatNamedAnotherAgent(t *testing.T) {
	base := []string{
		"PATH=/usr/bin", "HOME=/h",
		"HERDR_PANE_ID=w1:p9", "HERDR_SESSION=s", "HERDR_CONFIG_PATH=/c", "HERDR_BIN_PATH=/b/herdr",
		"MATE_CREW_ID=k1", "MATE_CALLER=crew", "MATE_LIVE=1",
		"CLAUDECODE=1", "MATE_PROJECT_ID=other",
	}
	got := hostEnv(base, []runtime.EnvVar{{Key: "MATE_PROJECT_ID", Value: "shop"}, {Key: "MATE_CALLER", Value: "mate"}})
	want := []string{"HOME=/h", "HERDR_CONFIG_PATH=/c", "HERDR_BIN_PATH=/b/herdr",
		"PATH=/usr/bin:/h/.local/bin:/opt/homebrew/bin:/usr/local/bin:/bin",
		"MATE_PROJECT_ID=shop", "MATE_CALLER=mate"}
	if !slices.Equal(got, want) {
		t.Fatalf("env = %q, want %q", got, want)
	}
}

func TestHostPathReachesWhereInstallersPutACLI(t *testing.T) {
	if got, want := hostPath("", "", "/x/bin"), "/x/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin"; got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	if got, want := hostPath("/usr/local/bin:/a", "/h", ""), "/usr/local/bin:/a:/h/.local/bin:/opt/homebrew/bin:/usr/bin:/bin"; got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

func TestRotateHostLogKeepsOneRunBefore(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "host.log")
	if err := rotateHostLog(log, 10); err != nil {
		t.Fatalf("rotate with no log: %v", err)
	}
	if err := os.WriteFile(log, []byte("short\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := rotateHostLog(log, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(log + ".1"); !os.IsNotExist(err) {
		t.Fatalf("a log under the limit was rotated: %v", err)
	}
	if err := os.WriteFile(log, []byte("0123456789 and more\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(log+".1", []byte("older\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := rotateHostLog(log, 10); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(log + ".1"); !strings.HasPrefix(string(data), "0123456789") {
		t.Fatalf("host.log.1 = %q, want the log that was over the limit", data)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("host.log still there after rotating: %v", err)
	}
}

// ExecHosts starts a real process in a session of its own, its output in
// the log, and SignalHost ends it.
func TestExecHostsStartsADetachedProcessWithItsLog(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "host.log")
	pid, exited, err := ExecHosts{}.StartHost(HostSpec{
		Argv: []string{"/bin/sh", "-c", `echo "started in $(pwd) as $MATE_PROJECT_ID"; exec sleep 30`},
		Dir:  dir,
		Env:  []string{"MATE_PROJECT_ID=shop", "PATH=/usr/bin:/bin"},
		Log:  log,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ExecHosts{}.SignalHost(pid, syscall.SIGKILL) })
	deadline := time.Now().Add(5 * time.Second)
	for {
		data, _ := os.ReadFile(log)
		if strings.Contains(string(data), "as shop") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("log = %q", data)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := (ExecHosts{}).SignalHost(pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("the process did not exit on SIGTERM")
	}
}
