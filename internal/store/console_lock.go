package store

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

// consoleLockName is the lock one console holds for as long as it runs:
// a workspace has one console at a time (CON-162). Two consoles over one
// workspace ran two observers, two auto daemons and two refresh loops, and
// only one of them could write the timeline.
const consoleLockName = "console.lock"

// ErrConsoleRunning is what LockConsole returns while another console
// holds the lock.
var ErrConsoleRunning = errors.New("store: another console is running on this workspace")

// ConsoleHolder is what the console holding the lock wrote into it, so the
// next one can say which console that is and ask it to quit.
type ConsoleHolder struct {
	PID     int       `json:"pid"`
	Started time.Time `json:"started"`
	// CoreLock says the console takes core.lock when it runs the engine
	// (CON-211). A console built before that writes no such field, and runs
	// an engine `mate core` cannot see: `mate core run` refuses beside it.
	CoreLock bool `json:"core_lock,omitempty"`
}

// ConsoleLockFile is `.mate/console.lock`.
func (w *Workspace) ConsoleLockFile() string {
	return filepath.Join(w.StateDir(), consoleLockName)
}

// LockConsole takes the console lock without waiting and writes holder into
// it. While another console holds it the answer is ErrConsoleRunning, and
// nothing is written. The lock is advisory and dies with the process, so a
// console that crashed leaves nothing to clean up. The file is never
// deleted: unlinking it would let the next console lock a fresh inode while
// the old one still holds the first.
func (w *Workspace) LockConsole(holder ConsoleHolder) (unlock func(), err error) {
	f, err := os.OpenFile(w.ConsoleLockFile(), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	ok, err := tryLockFile(f, true)
	if err != nil {
		f.Close()
		return nil, err
	}
	if !ok {
		f.Close()
		return nil, ErrConsoleRunning
	}
	unlock = func() {
		_ = unlockFile(f)
		_ = f.Close()
	}
	if err := writeHolder(f, holder); err != nil {
		unlock()
		return nil, err
	}
	return unlock, nil
}

// ConsoleHolder reads who wrote the console lock last. It says nothing
// about whether that console still runs; ConsoleRunning is the one that knows.
func (w *Workspace) ConsoleHolder() (ConsoleHolder, error) {
	var h ConsoleHolder
	err := readHolder(w.ConsoleLockFile(), &h)
	return h, err
}
