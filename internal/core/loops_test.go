package core

import (
	"context"
	goruntime "runtime"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/autopilot"
	"github.com/nguyenngocanh94/mate/internal/outbox"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/watch"
)

// The loops cmd/mate builds, stopped right after they start: Engine.Start
// does that to the loops before a part that fails to start. With one P a
// loop's goroutine runs only once Stop blocks, after Stop has cleared the
// loop's fields, so it must close the channel Start made for it rather than
// whatever the field holds by then. On the nil one it found there it
// panicked (close of nil channel), and Stop waited for the old channel
// forever.
func TestTheRealLoopsStopRightAfterTheyStart(t *testing.T) {
	defer goruntime.GOMAXPROCS(goruntime.GOMAXPROCS(1))
	ws, err := store.Init(t.TempDir(), store.Defaults{})
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	rt := runtime.NewFake()
	sender := outbox.New(ws, outbox.Deps{Runtime: rt})
	loops := []struct {
		name string
		loop Loop
	}{
		{"watcher", WatchLoop(watch.New(ws, watch.Deps{Runtime: rt}))},
		{"sender", SenderLoop(sender)},
		{"pilot", PilotLoop(autopilot.New(ws, autopilot.Deps{Outbox: sender}))},
	}
	for range 20 {
		for _, l := range loops {
			if err := l.loop.Start(context.Background()); err != nil {
				t.Fatalf("%s: Start: %v", l.name, err)
			}
			l.loop.Stop()
		}
	}
}
