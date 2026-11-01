package teardown

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestLiveTeardownRemovesWhatAWorktreeStarted is the proof on a real
// machine (CON-111, CON-146): the repo's teardown command runs in the
// worktree, and a process still running in it is gone after one Clean.
// What a toolchain starts - a stack, a simulator - is that command's job.
func TestLiveTeardownRemovesWhatAWorktreeStarted(t *testing.T) {
	if os.Getenv("MATE_LIVE") != "1" {
		t.Skip("set MATE_LIVE=1 to run live teardown proofs (lsof, signals)")
	}
	ctx := context.Background()
	worktree := filepath.Join(t.TempDir(), "mate-live-teardown-"+time.Now().Format("150405"))
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	worktree, _ = filepath.EvalSymlinks(worktree)

	sleeper := exec.Command("sleep", "600")
	sleeper.Dir = worktree
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() { _ = sleeper.Wait(); close(exited) }()

	r := Live().Clean(ctx, Target{Worktree: worktree, Hook: "touch torn-down"})
	t.Logf("cleanup: %s", r.Meta())
	if len(r.Pending) > 0 || r.Hook != "ok" {
		t.Fatalf("report %+v", r)
	}
	if _, err := os.Stat(filepath.Join(worktree, "torn-down")); err != nil {
		t.Errorf("the teardown command did not run in the worktree: %v", err)
	}
	select {
	case <-exited:
	case <-time.After(10 * time.Second):
		_ = sleeper.Process.Kill()
		t.Error("the process in the worktree is still running")
	}
}
