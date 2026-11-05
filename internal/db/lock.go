package db

import (
	"os"

	"github.com/nguyenngocanh94/mate/internal/platform"
)

// takeLock opens (creating) the lock file and takes a non-blocking exclusive
// flock on it. The lock is advisory and held for as long as the handle is
// open: a second writer is refused immediately rather than queued, because a
// writer that waits is a console that hangs on startup with no explanation.
//
// The file is never deleted. Unlinking it would let a second process create a
// fresh inode and lock that instead, which is a lock that locks nothing.
//
// On a platform without flock (internal/platform) the lock fails and so does
// the writer: taking nothing would let a second writer interleave with the
// first, which is the one thing the lock exists to refuse, and mate does not
// run without its locks (CON-177).
func takeLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	locked, err := platform.TryLock(f)
	if locked {
		return f, nil
	}
	_ = f.Close()
	if err == nil {
		return nil, ErrLocked
	}
	return nil, err
}

func releaseLock(f *os.File) {
	if f == nil {
		return
	}
	_ = platform.Unlock(f)
	_ = f.Close()
}
