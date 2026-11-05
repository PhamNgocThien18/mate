//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package platform_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/platform"
)

// openLock opens path once more. A flock lock belongs to the open file, so
// two opens of one path contend for it the way two processes do.
func openLock(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestAnExclusiveLockShutsOutEveryOtherLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	held, other := openLock(t, path), openLock(t, path)
	if err := platform.Lock(held); err != nil {
		t.Fatalf("Lock = %v", err)
	}
	if ok, err := platform.TryLock(other); ok || err != nil {
		t.Fatalf("TryLock while another holds the lock = %v, %v; want false, nil", ok, err)
	}
	if ok, err := platform.TryRLock(other); ok || err != nil {
		t.Fatalf("TryRLock while another holds the lock = %v, %v; want false, nil", ok, err)
	}
	if err := platform.Unlock(held); err != nil {
		t.Fatalf("Unlock = %v", err)
	}
	if ok, err := platform.TryLock(other); !ok || err != nil {
		t.Fatalf("TryLock once it is released = %v, %v; want true, nil", ok, err)
	}
}

func TestSharedLocksShutOutOnlyAnExclusiveOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	reader, second, writer := openLock(t, path), openLock(t, path), openLock(t, path)
	if err := platform.RLock(reader); err != nil {
		t.Fatalf("RLock = %v", err)
	}
	if ok, err := platform.TryRLock(second); !ok || err != nil {
		t.Fatalf("TryRLock beside a shared lock = %v, %v; want true, nil", ok, err)
	}
	if ok, err := platform.TryLock(writer); ok || err != nil {
		t.Fatalf("TryLock beside shared locks = %v, %v; want false, nil", ok, err)
	}
	for _, f := range []*os.File{reader, second} {
		if err := platform.Unlock(f); err != nil {
			t.Fatalf("Unlock = %v", err)
		}
	}
	if ok, err := platform.TryLock(writer); !ok || err != nil {
		t.Fatalf("TryLock once the shared locks are released = %v, %v; want true, nil", ok, err)
	}
}

// TestOpenNoFollowRefusesASymlink: a lock file planted as a symlink must not
// let mate open, or create, the file it points to.
func TestOpenNoFollowRefusesASymlink(t *testing.T) {
	dir := t.TempDir()
	target, link := filepath.Join(dir, "target"), filepath.Join(dir, "x.lock")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if f, err := platform.OpenNoFollow(link, os.O_CREATE|os.O_RDWR, 0o600); err == nil {
		f.Close()
		t.Fatal("OpenNoFollow opened a symlink to a missing file; want an error")
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("the symlink's target exists after OpenNoFollow (Lstat = %v); want it never created", err)
	}
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if f, err := platform.OpenNoFollow(link, os.O_RDWR, 0); err == nil {
		f.Close()
		t.Fatal("OpenNoFollow opened a symlink to a file; want an error")
	}
	f, err := platform.OpenNoFollow(target, os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("OpenNoFollow of a plain file = %v", err)
	}
	f.Close()
}

func TestLockWaitsForTheHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")
	held, waiter := openLock(t, path), openLock(t, path)
	if err := platform.Lock(held); err != nil {
		t.Fatalf("Lock = %v", err)
	}
	got := make(chan error, 1)
	go func() { got <- platform.Lock(waiter) }()
	select {
	case err := <-got:
		t.Fatalf("Lock returned %v while another held the lock; want it to wait", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := platform.Unlock(held); err != nil {
		t.Fatalf("Unlock = %v", err)
	}
	select {
	case err := <-got:
		if err != nil {
			t.Fatalf("Lock once the holder let go = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Lock still waits after the holder let go")
	}
}
