//go:build !darwin && !dragonfly && !freebsd && !illumos && !linux && !netbsd && !openbsd

package platform

import "os"

// errNoFlock is every lock's answer here. A lock that took nothing would let
// a log append, a backlog edit or a second timeline writer lose its
// exclusion without a word, and mate does not run without its locks
// (CON-177).
const errNoFlock unsupported = "file locks need syscall.Flock, which Go lacks"

// errNoNoFollow is OpenNoFollow's: opening the file without the flag could
// follow a planted symlink, which is what the flag is there to refuse.
const errNoNoFollow unsupported = "opening a file without following a symlink is not supported"

func OpenNoFollow(string, int, os.FileMode) (*os.File, error) { return nil, errNoNoFollow }

func Lock(*os.File) error { return errNoFlock }

func RLock(*os.File) error { return errNoFlock }

func TryLock(*os.File) (bool, error) { return false, errNoFlock }

func TryRLock(*os.File) (bool, error) { return false, errNoFlock }

func Unlock(*os.File) error { return errNoFlock }
