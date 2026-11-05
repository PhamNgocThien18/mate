package core

import (
	"context"
	"sync"
	"time"

	"github.com/nguyenngocanh94/mate/internal/recovery"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// recoveryWait bounds how long Stop waits for a cancelled recovery to undo
// an agent it had half started. It is longer than the spawn package's own
// 30s cleanup bound, so that cleanup gets the chance to finish; a recovery
// that is still busy after it is left to the process exit, and the next
// recovery adopts or refuses what it left by name.
const recoveryWait = 45 * time.Second

// Recovery is where the engine's recovery stands.
type Recovery struct {
	// Reported is true once recovery has told any progress, and Progress
	// is the latest it told.
	Reported bool
	Progress recovery.Progress
	// Done is true once recovery has returned: Result is what it did, and
	// DoneAt when it returned.
	Done   bool
	Result recovery.Result
	DoneAt time.Time
}

// recoveryRun runs recovery once, in the background, as the engine's last
// loop: it starts once the others run and stops before them.
type recoveryRun struct {
	run  RecoverFunc
	ws   *store.Workspace
	wait time.Duration
	now  func() time.Time

	// cancel and done are set by Start and read by Stop, which the engine
	// calls one after the other under its own lock.
	cancel context.CancelFunc
	done   chan struct{}

	mu    sync.Mutex
	state Recovery
}

// Start begins the run and returns at once; it cannot fail.
func (r *recoveryRun) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	r.cancel, r.done = cancel, done
	go func() {
		defer close(done)
		r.finish(r.run(ctx, r.ws, r.progress))
	}()
	return nil
}

// Stop cancels what is left of the run and waits for it to return, at most
// r.wait. A run that never started has nothing to stop.
func (r *recoveryRun) Stop() {
	if r.cancel == nil {
		return
	}
	r.cancel()
	timer := time.NewTimer(r.wait)
	defer timer.Stop()
	select {
	case <-r.done:
	case <-timer.C:
	}
}

func (r *recoveryRun) progress(p recovery.Progress) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state.Reported, r.state.Progress = true, p
}

func (r *recoveryRun) finish(res recovery.Result) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state.Done, r.state.Result, r.state.DoneAt = true, res, r.now()
}

func (r *recoveryRun) read() Recovery {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}
