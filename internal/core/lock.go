package core

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/nguyenngocanh94/mate/internal/config"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// Who runs an engine (Options.Owner), as core.lock says it.
const (
	OwnerCore    = store.CoreOwnerCore
	OwnerConsole = store.CoreOwnerConsole
)

// RunningError is Start's refusal while another engine holds the
// workspace's core.lock (CON-211): one workspace runs one engine, in `mate
// core` or in a console. Holder is what that engine wrote there; its PID is
// zero when the record could not be read.
type RunningError struct {
	Holder store.CoreHolder
}

func (e *RunningError) Error() string {
	who := "another engine"
	if e.Holder.Owner == OwnerCore {
		who = "mate core"
	} else if e.Holder.Owner == OwnerConsole {
		who = "a mate console"
	}
	if e.Holder.PID > 0 {
		return fmt.Sprintf("core: %s (pid %d) already runs the engine on this workspace", who, e.Holder.PID)
	}
	return fmt.Sprintf("core: %s already runs the engine on this workspace", who)
}

// lock takes the workspace's core.lock for this engine, saying who runs it
// and which build.
func (e *Engine) lock() error {
	owner := e.o.Owner
	if owner == "" {
		owner = OwnerConsole
	}
	binary, build := Build()
	e.holder = store.CoreHolder{
		PID:     os.Getpid(),
		Owner:   owner,
		Started: e.now(),
		Version: config.Version,
		Binary:  binary,
		Build:   build,
	}
	lock, err := e.o.Workspace.LockCore(e.holder)
	if errors.Is(err, store.ErrCoreRunning) {
		h, _, _ := e.o.Workspace.CoreRunning()
		return &RunningError{Holder: h}
	}
	if err != nil {
		return fmt.Errorf("core: taking %s: %w", e.o.Workspace.CoreLockFile(), err)
	}
	e.coreLock = lock
	return nil
}

// recordServing writes into core.lock where the dashboard serves, or why
// it does not over TCP, once Start knows.
func (e *Engine) recordServing() {
	if e.coreLock == nil || e.dashboard == nil {
		return
	}
	s := e.dashboard.read()
	e.holder.Socket, e.holder.URL, e.holder.LANURL = s.Socket, s.URL, s.LANURL
	e.holder.TCPError = ""
	if s.Err != nil {
		e.holder.TCPError = s.Err.Error()
	}
	if err := e.coreLock.Write(e.holder); err != nil {
		fmt.Fprintf(e.stderr(), "mate core: recording where it serves in %s: %v\n", e.o.Workspace.CoreLockFile(), err)
	}
}

func (e *Engine) stderr() io.Writer {
	if e.o.Stderr != nil {
		return e.o.Stderr
	}
	return os.Stderr
}

var build struct {
	once   sync.Once
	binary string
	sum    string
}

// Build is the path of this process's executable and the sha256 of that
// file, read once per process: what core.lock records so a newer install
// can be told from the binary a running engine started from. Either is
// empty when it cannot be read.
func Build() (binary, sum string) {
	build.once.Do(func() {
		exe, err := os.Executable()
		if err != nil {
			return
		}
		build.binary = exe
		f, err := os.Open(exe)
		if err != nil {
			return
		}
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return
		}
		build.sum = hex.EncodeToString(h.Sum(nil))
	})
	return build.binary, build.sum
}
