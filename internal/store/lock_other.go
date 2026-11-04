//go:build !darwin && !dragonfly && !freebsd && !illumos && !linux && !netbsd && !openbsd

package store

import (
	"fmt"
	"os"
	"runtime"
)

// errNoFlock is every lock's answer on a platform whose syscall package has
// no Flock (the constraint above is lock_unix.go's, negated). A lock that
// took nothing would let a log append, a backlog edit or an outbox update
// lose its exclusion without a word, and mate does not run without its
// locks (CON-177).
var errNoFlock = fmt.Errorf("store: file locks need flock, which %s does not have", runtime.GOOS)

func lockFile(f *os.File, exclusive bool) error { return errNoFlock }

func unlockFile(f *os.File) error { return errNoFlock }

func tryLockFile(f *os.File, exclusive bool) (bool, error) { return false, errNoFlock }
