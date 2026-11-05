package core

import (
	"context"

	"github.com/nguyenngocanh94/mate/internal/autopilot"
	"github.com/nguyenngocanh94/mate/internal/outbox"
	"github.com/nguyenngocanh94/mate/internal/watch"
)

// The loops cmd/mate builds start without a word: each Start spawns its
// goroutine and returns. These adapt them to Loop, whose Start may fail
// (core.lock will, CON-211).

// WatchLoop is the observer as the engine's Watcher.
func WatchLoop(w *watch.Watcher) Watcher { return watchLoop{w} }

// SenderLoop is the outbox sender loop as one of the engine's loops.
func SenderLoop(s *outbox.Sender) Loop { return senderLoop{s} }

// PilotLoop is the auto daemon as the engine's Pilot.
func PilotLoop(p *autopilot.Pilot) Pilot { return pilotLoop{p} }

type watchLoop struct{ *watch.Watcher }

func (l watchLoop) Start(ctx context.Context) error {
	l.Watcher.Start(ctx)
	return nil
}

type senderLoop struct{ *outbox.Sender }

func (l senderLoop) Start(ctx context.Context) error {
	l.Sender.Start(ctx)
	return nil
}

type pilotLoop struct{ *autopilot.Pilot }

func (l pilotLoop) Start(ctx context.Context) error {
	l.Pilot.Start(ctx)
	return nil
}
