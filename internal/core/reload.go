package core

import (
	"context"
	"time"

	"github.com/nguyenngocanh94/mate/internal/store"
)

// configEvery is how often the engine reads workspace.yaml back into the
// workspace it shares with Mate Office and Agent City.
const configEvery = 2 * time.Second

// configReload reads workspace.yaml into ws every configEvery, whether or
// not anybody reads a snapshot (CON-208): the dashboard's routes that list
// the projects (`/api/workspace`, `/api/board`, `/api/news`, `/api/work`)
// read the shared workspace and no snapshot, and before the engine did this
// only the console's refresh did it for them. It reads the file and asks
// Herdr nothing. A file that does not read (missing, broken, empty) leaves
// the configuration loaded before, as LoadConfig does.
func configReload(ws *store.Workspace) *tickLoop {
	return &tickLoop{every: configEvery, tick: func(context.Context) {
		_ = ws.LoadConfig()
	}}
}

// tickLoop runs tick every interval on a goroutine of its own, from one
// interval after Start until Stop.
type tickLoop struct {
	every time.Duration
	tick  func(context.Context)

	// cancel and done are set by Start and read by Stop, which the engine
	// calls one after the other under its own lock.
	cancel context.CancelFunc
	done   chan struct{}
}

func (l *tickLoop) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	// The goroutine closes the channel made here, never l.done: a Stop
	// right after Start must find it whatever the goroutine has done by
	// then (the CON-207 race).
	done := make(chan struct{})
	l.cancel, l.done = cancel, done
	go func() {
		defer close(done)
		ticker := time.NewTicker(l.every)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				l.tick(ctx)
			}
		}
	}()
	return nil
}

// Stop ends the loop and waits for a tick in flight. A loop that never
// started has nothing to stop.
func (l *tickLoop) Stop() {
	if l.cancel == nil {
		return
	}
	l.cancel()
	<-l.done
}
