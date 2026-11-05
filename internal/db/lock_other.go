//go:build !darwin && !dragonfly && !freebsd && !illumos && !linux && !netbsd && !openbsd

package db

import (
	"fmt"
	"os"
	"runtime"
)

// takeLock refuses on a platform whose syscall package has no Flock (the
// constraint above is lock_unix.go's, negated). Opening the file and taking
// nothing would let a second writer interleave with the first, which is the
// one thing the lock exists to refuse, and mate does not run without its
// locks (CON-177).
func takeLock(path string) (*os.File, error) {
	return nil, fmt.Errorf("db: the writer lock needs syscall.Flock, which Go lacks on %s", runtime.GOOS)
}

func releaseLock(f *os.File) {
	if f != nil {
		_ = f.Close()
	}
}
