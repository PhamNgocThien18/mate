package core

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

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

// Claim is the workspace's core.lock, held by this process for an engine
// it is about to build. The console and `mate core run` take it before
// they build the engine's parts (Options.Claim): the observer opens
// mate.db as it is built, and an engine built while another one was still
// letting go of mate.db would run without the timeline for its whole life.
type Claim struct {
	ws     *store.Workspace
	lock   *store.CoreLock
	holder store.CoreHolder
}

// Lock takes ws's core.lock for an engine owned by owner (OwnerCore or
// OwnerConsole), recording this process and its build. While another
// engine holds it the answer is a *RunningError naming that engine.
func Lock(ws *store.Workspace, owner string, now time.Time) (*Claim, error) {
	if owner == "" {
		owner = OwnerConsole
	}
	binary, build := Build()
	holder := store.CoreHolder{
		PID:     os.Getpid(),
		Owner:   owner,
		Started: now,
		Version: config.Version,
		Binary:  binary,
		Build:   build,
	}
	lock, err := ws.LockCore(holder)
	if errors.Is(err, store.ErrCoreRunning) {
		h, _, _ := ws.CoreRunning()
		return nil, &RunningError{Holder: h}
	}
	if err != nil {
		return nil, fmt.Errorf("core: taking %s: %w", ws.CoreLockFile(), err)
	}
	return &Claim{ws: ws, lock: lock, holder: holder}, nil
}

// Release lets core.lock go, for a claim no engine was built over; an
// engine releases its own when it stops.
func (c *Claim) Release() {
	if c != nil && c.lock != nil {
		c.lock.Unlock()
		c.lock = nil
	}
}

// lock is the engine's claim on core.lock: the one Options.Claim handed
// in, or one taken now.
func (e *Engine) lock() error {
	claim := e.o.Claim
	if claim == nil {
		var err error
		if claim, err = Lock(e.o.Workspace, e.o.Owner, e.now()); err != nil {
			return err
		}
	}
	e.claim, e.holder = claim, claim.holder
	return nil
}

// recordServing writes into core.lock where the dashboard serves, or why
// it does not over TCP, once Start knows.
func (e *Engine) recordServing() {
	c := e.claim
	if c == nil || c.lock == nil || e.dashboard == nil {
		return
	}
	s := e.dashboard.read()
	c.holder.Socket, c.holder.URL, c.holder.LANURL = s.Socket, s.URL, s.LANURL
	c.holder.TCPError = ""
	if s.Err != nil {
		c.holder.TCPError = s.Err.Error()
	}
	if err := c.lock.Write(c.holder); err != nil {
		fmt.Fprintf(e.stderr(), "mate core: recording where it serves in %s: %v\n", c.ws.CoreLockFile(), err)
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
// empty when it cannot be read. The console and `mate core run` call it
// first thing, so the sum is of the file the process started from, not of
// one installed over it while the console waited at a prompt.
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
