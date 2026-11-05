package spawn

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/outbox"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// MateOutboxDeps are the outbox sender's collaborators over deps: its
// runtime, harnesses and observer, and the Mate's handle resolved by
// MateHandle. A caller that needs more of outbox.Deps sets it on the result
// before outbox.New.
//
// deps' clock and sleep carry over, so a stow before a restart runs at
// memory speed under a test's fake adapter; the live deps set neither.
func MateOutboxDeps(ws *store.Workspace, deps Deps) outbox.Deps {
	od := outbox.Deps{
		Runtime:   deps.Runtime,
		Handle:    mateOutboxHandle(ws, deps),
		Harnesses: deps.Harnesses,
		Observer:  deps.Observer,
	}
	if deps.Now != nil {
		od.Clock = clockFunc(deps.Now)
	}
	if deps.Sleep != nil {
		od.Sleeper = sleepFunc(deps.Sleep)
	}
	return od
}

// MateOutbox is an outbox sender over deps' runtime. Its state is all on
// disk, so any number of them agree: the console's loop, the one an
// `[assign]` uses for its immediate attempt, and the daemon's.
func MateOutbox(ws *store.Workspace, deps Deps) *outbox.Sender {
	return outbox.New(ws, MateOutboxDeps(ws, deps))
}

// mateOutboxHandle is outbox.HandleFunc over MateHandle: the recorded
// `mate/mate.meta` resolved against the session Herdr currently answers for.
// The sender takes it as a function so it imports nothing that could start
// or stop an agent.
func mateOutboxHandle(ws *store.Workspace, deps Deps) outbox.HandleFunc {
	return func(ctx context.Context, project string) (runtime.AgentHandle, harness.Kind, error) {
		return MateHandle(ctx, ws, deps, project)
	}
}

type clockFunc func() time.Time

func (f clockFunc) Now() time.Time { return f() }

type sleepFunc func(context.Context, time.Duration) error

func (f sleepFunc) Sleep(ctx context.Context, d time.Duration) error { return f(ctx, d) }

// StowBeforeStop is the stow half of `mate mate stop`, also run by `mate
// project remove` and the console's remove before they stop the Mate. It
// returns the words the stop reports for it: what the stow did, or why none
// was asked for. The wait it starts is announced on stderr.
func StowBeforeStop(ctx context.Context, w *store.Workspace, deps Deps, project, caller string, noStow bool, stderr io.Writer) (string, error) {
	switch {
	case noStow:
		return "not stowed: --no-stow", nil
	case caller == CallerMate:
		return "not stowed: the Mate stopped itself", nil
	case caller != CallerUser:
		return "not stowed: only the captain's stop asks the Mate to stow", nil
	}
	fmt.Fprintf(stderr, "stowing: asking the Mate to record what exists only in its conversation, waiting up to %s for its turn (--no-stow skips this)\n",
		outbox.Span(outbox.DefaultStowCeiling))
	res, err := MateOutbox(w, deps).Stow(ctx, project, outbox.StowOptions{RequireEmpty: true})
	if err != nil {
		return "", err
	}
	return res.Outcome(), nil
}
