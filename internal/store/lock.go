package store

import (
	"os"

	"github.com/nguyenngocanh94/mate/internal/platform"
)

// lockFile takes an advisory whole-file lock, exclusive for a writer and
// shared for a reader, and holds it for the duration of one append or one
// read. Appends are O_APPEND so the kernel already keeps a single write
// atomic; the lock is what keeps a reader from seeing a torn line and what
// serialises the append that a crew's `echo` makes from a shell.
//
// Every lock here fails on a platform without flock (internal/platform): a
// lock that took nothing would let a log append, a backlog edit or an outbox
// update lose its exclusion without a word, and mate does not run without
// its locks (CON-177).
func lockFile(f *os.File, exclusive bool) error {
	if exclusive {
		return platform.Lock(f)
	}
	return platform.RLock(f)
}

func unlockFile(f *os.File) error {
	return platform.Unlock(f)
}

// tryLockFile never waits behind an active refresh or delivery.
func tryLockFile(f *os.File, exclusive bool) (bool, error) {
	if exclusive {
		return platform.TryLock(f)
	}
	return platform.TryRLock(f)
}
