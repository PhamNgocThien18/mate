package store

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// The engine's files (CON-211). A workspace runs one engine, in `mate core`
// or in a console, and that engine holds core.lock for as long as it runs.
const (
	coreLockName   = "core.lock"
	coreSocketName = "core.sock"
	coreLogName    = "core.log"
)

// Who runs the engine that holds core.lock.
const (
	// CoreOwnerCore is `mate core`, the engine's own process.
	CoreOwnerCore = "core"
	// CoreOwnerConsole is a console running the engine itself, until the
	// console becomes a client of the core (CON-214).
	CoreOwnerConsole = "console"
)

// ErrCoreRunning is what LockCore returns while another engine holds the
// lock.
var ErrCoreRunning = errors.New("store: another engine is running on this workspace")

// CoreHolder is what the engine holding core.lock wrote into it: which
// process, which binary, and where it serves. The engine writes it again
// once it knows where it serves (Socket, URL, LANURL) or why the TCP bind
// failed (TCPError).
type CoreHolder struct {
	PID int `json:"pid"`
	// Owner is CoreOwnerCore or CoreOwnerConsole.
	Owner   string    `json:"owner"`
	Started time.Time `json:"started"`
	// Version is config.Version of the binary.
	Version string `json:"version"`
	// Binary is the path of the executable the process runs.
	Binary string `json:"binary"`
	// Build is the sha256 of that file when the process started: two builds
	// of one commit with uncommitted changes share a Version, not a Build.
	Build string `json:"build"`
	// Socket is the unix socket the engine serves on, empty when none.
	Socket string `json:"socket,omitempty"`
	// URL is the dashboard's loopback URL, LANURL its LAN one when the bind
	// reaches the LAN; TCPError is why the TCP bind failed.
	URL      string `json:"url,omitempty"`
	LANURL   string `json:"lan_url,omitempty"`
	TCPError string `json:"tcp_error,omitempty"`
}

// CoreLockFile is `.mate/core.lock`.
func (w *Workspace) CoreLockFile() string { return filepath.Join(w.StateDir(), coreLockName) }

// CoreSocket is `.mate/core.sock`, where `mate core` serves its HTTP routes.
func (w *Workspace) CoreSocket() string { return filepath.Join(w.StateDir(), coreSocketName) }

// CoreLogFile is `.mate/core.log`, the output of a `mate core` started in
// the background (CON-213).
func (w *Workspace) CoreLogFile() string { return filepath.Join(w.StateDir(), coreLogName) }

// CoreLock is core.lock held by this process.
type CoreLock struct {
	f *os.File
}

// LockCore takes the engine's lock without waiting and writes holder into
// it. While another engine holds it the answer is ErrCoreRunning, and
// nothing is written. Like the console lock it is advisory and dies with
// the process, and the file is never deleted.
func (w *Workspace) LockCore(holder CoreHolder) (*CoreLock, error) {
	f, err := os.OpenFile(w.CoreLockFile(), os.O_CREATE|os.O_RDWR, 0o644)
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
		return nil, ErrCoreRunning
	}
	l := &CoreLock{f: f}
	if err := l.Write(holder); err != nil {
		l.Unlock()
		return nil, err
	}
	return l, nil
}

// Write replaces what the lock says about its holder.
func (l *CoreLock) Write(holder CoreHolder) error {
	return writeHolder(l.f, holder)
}

// Unlock releases the lock. The file stays, saying who held it last.
func (l *CoreLock) Unlock() {
	_ = unlockFile(l.f)
	_ = l.f.Close()
}

// CoreHolder reads who wrote core.lock last. It says nothing about whether
// that engine still runs; CoreRunning does. While the lock is held the
// record may be read in the instant its holder writes it; readHeldRecord
// tries again then.
func (w *Workspace) CoreHolder() (CoreHolder, error) {
	var h CoreHolder
	err := readHolder(w.CoreLockFile(), &h)
	return h, err
}

// CoreRunning reports whether an engine holds core.lock now, and what it
// wrote there. The holder is zero when the lock is held but its record
// cannot be read.
func (w *Workspace) CoreRunning() (CoreHolder, bool, error) {
	held, err := lockHeld(w.CoreLockFile())
	if err != nil || !held {
		return CoreHolder{}, false, err
	}
	var h CoreHolder
	readHeldRecord(w.CoreLockFile(), &h)
	return h, true, nil
}

// ConsoleRunning reports whether a console holds console.lock now, and what
// it wrote there; the holder is zero when its record cannot be read.
func (w *Workspace) ConsoleRunning() (ConsoleHolder, bool, error) {
	held, err := lockHeld(w.ConsoleLockFile())
	if err != nil || !held {
		return ConsoleHolder{}, false, err
	}
	var h ConsoleHolder
	readHeldRecord(w.ConsoleLockFile(), &h)
	return h, true, nil
}

// readHeldRecord reads the record of a lock another process holds. The
// holder writes it right after it takes the lock, so a read in that instant
// finds it empty and is tried again, for a tenth of a second at most;
// holder is left zero when it still cannot be read.
func readHeldRecord(path string, holder any) {
	for try := 0; try < 5; try++ {
		if try > 0 {
			time.Sleep(20 * time.Millisecond)
		}
		if readHolder(path, holder) == nil {
			return
		}
	}
}

// lockHeld asks whether another open file holds an exclusive lock on path,
// by taking a shared one for an instant. A missing file is held by nobody.
func lockHeld(path string) (bool, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	ok, err := tryLockFile(f, false)
	if err != nil {
		return false, err
	}
	if ok {
		_ = unlockFile(f)
	}
	return !ok, nil
}

// writeHolder replaces the content of a held lock file with holder's JSON.
func writeHolder(f *os.File, holder any) error {
	data, err := json.Marshal(holder)
	if err != nil {
		return err
	}
	if err := f.Truncate(0); err != nil {
		return err
	}
	_, err = f.WriteAt(append(data, '\n'), 0)
	return err
}

func readHolder(path string, holder any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, holder)
}
