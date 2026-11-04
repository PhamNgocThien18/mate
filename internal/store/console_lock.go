package store

import (
	"encoding/json"
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
	data, err := json.Marshal(holder)
	if err == nil {
		err = f.Truncate(0)
	}
	if err == nil {
		_, err = f.WriteAt(append(data, '\n'), 0)
	}
	if err != nil {
		unlock()
		return nil, err
	}
	return unlock, nil
}

// ConsoleHolder reads who wrote the console lock last. It says nothing
// about whether that console still runs; LockConsole is the one that knows.
func (w *Workspace) ConsoleHolder() (ConsoleHolder, error) {
	var h ConsoleHolder
	data, err := os.ReadFile(w.ConsoleLockFile())
	if err != nil {
		return h, err
	}
	err = json.Unmarshal(data, &h)
	return h, err
}
