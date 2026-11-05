//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package platform

import (
	"os"
	"syscall"
)

// Lock takes an exclusive advisory lock on the whole of f, waiting while
// another lock is held on it. The lock is flock(2)'s: it belongs to the open
// file, not to the process, so a second open of the same path contends for
// it like another process would, and closing the last descriptor of the
// open file releases it.
func Lock(f *os.File) error { return flock(f, syscall.LOCK_EX) }

// RLock takes a shared advisory lock on the whole of f, waiting while an
// exclusive lock is held on it. Any number of shared locks are held at once.
func RLock(f *os.File) error { return flock(f, syscall.LOCK_SH) }

// TryLock is Lock without waiting: false and a nil error when another lock
// is held on f.
func TryLock(f *os.File) (bool, error) { return tryFlock(f, syscall.LOCK_EX) }

// TryRLock is RLock without waiting: false and a nil error when an
// exclusive lock is held on f.
func TryRLock(f *os.File) (bool, error) { return tryFlock(f, syscall.LOCK_SH) }

// Unlock releases the lock f holds.
func Unlock(f *os.File) error { return flock(f, syscall.LOCK_UN) }

// OpenNoFollow is os.OpenFile refusing a symlink at name, so a lock file
// someone replaced with a symlink can neither be opened through it nor, with
// os.O_CREATE, create the file it points to. The error for a symlink is the
// system's own: ELOOP on darwin and linux, EMLINK on freebsd.
func OpenNoFollow(name string, flag int, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(name, flag|syscall.O_NOFOLLOW, perm)
}

// flock retries a call a signal interrupted, so a waiting lock does not fail
// for a signal that had nothing to do with it.
func flock(f *os.File, how int) error {
	for {
		err := syscall.Flock(int(f.Fd()), how)
		if err != syscall.EINTR {
			return err
		}
	}
}

func tryFlock(f *os.File, how int) (bool, error) {
	err := flock(f, how|syscall.LOCK_NB)
	if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
		return false, nil
	}
	return err == nil, err
}
