package main

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nguyenngocanh94/mate/internal/core"
	"github.com/nguyenngocanh94/mate/internal/coreapi"
)

// TestWaitAbandonedWaitsForTheActionThenReports: the Console waits for an
// action it quit in the middle of, and says whether it stopped in time.
func TestWaitAbandonedWaitsForTheActionThenReports(t *testing.T) {
	done := make(chan struct{})
	go func() {
		time.Sleep(30 * time.Millisecond)
		close(done)
	}()
	var errw bytes.Buffer
	start := time.Now()
	waitAbandoned(&errw, "onboard shop", done, 5*time.Second)
	if time.Since(start) < 25*time.Millisecond {
		t.Fatalf("returned after %s, before the action had stopped", time.Since(start))
	}
	if !strings.Contains(errw.String(), "onboard shop has stopped") {
		t.Fatalf("stderr = %q", errw.String())
	}

	errw.Reset()
	waitAbandoned(&errw, "onboard shop", make(chan struct{}), 20*time.Millisecond)
	if !strings.Contains(errw.String(), "had not stopped after 20ms") {
		t.Fatalf("stderr on timeout = %q", errw.String())
	}
}

// lockedBuffer is a stderr two goroutines write at once, as the console and
// the engine's Stop do (os.Stderr takes that; a bytes.Buffer does not).
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestQuittingDuringAnActionExitsWithinTheEnginesBound: the captain quits
// the console while an action runs through the engine (CON-210). The
// console says what it abandoned, as before, and the engine's Stop waits for
// that action alongside everything else it stops, so the console is gone
// within the engine's one bound, under 50 seconds, rather than after 45
// seconds of its own wait and then the engine's. In a synctest bubble the
// bound passes at once and is measured exactly.
func TestQuittingDuringAnActionExitsWithinTheEnginesBound(t *testing.T) {
	for _, tc := range []struct {
		name string
		// cleanup is how long the action takes to undo what it started
		// once its context is cancelled; deaf ignores the context.
		cleanup time.Duration
		deaf    bool
		took    time.Duration
		said    string
	}{
		{name: "it undoes in time", cleanup: 10 * time.Second, took: 10 * time.Second,
			said: "mate console: onboard shop has stopped.\n"},
		{name: "it never stops", deaf: true, took: core.StopWait,
			said: "mate console: onboard shop had not stopped after 45s; if it left an agent running, starting again adopts or refuses it by name.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				release := make(chan struct{})
				defer close(release)
				var stderr lockedBuffer
				engine := core.New(core.Options{
					Act: func(ctx context.Context, _ coreapi.ActionRequest) (string, error) {
						if !tc.deaf {
							<-ctx.Done()
							time.Sleep(tc.cleanup)
							return "", ctx.Err()
						}
						<-release
						return "", nil
					},
					Stderr: &stderr,
				})
				if err := engine.Start(context.Background()); err != nil {
					t.Fatal(err)
				}
				// The console's action and its abandon: the context it ran
				// under is cancelled, and done closes once it has returned.
				ctx, abandon := context.WithCancel(context.Background())
				done := make(chan struct{})
				go func() {
					defer close(done)
					_, _ = tuiAction(engine.Act, splitTerminal)(ctx, coreapi.ActionRequest{Action: coreapi.ActionOnboard, Target: "shop", TargetKind: "project-mate"})
				}()
				synctest.Wait()
				abandon()

				start := time.Now()
				quitAbandoned(&stderr, engine.Stop, "onboard shop", done, core.StopWait)
				took := time.Since(start)
				if took != tc.took || took >= 50*time.Second {
					t.Errorf("the console quit after %s, want %s, under 50s", took, tc.took)
				}
				out := stderr.String()
				if want := "\nmate console: quit while onboard shop was still running; waiting for it to undo what it started...\n"; !strings.HasPrefix(out, want) {
					t.Errorf("stderr %q, want it to start with %q", out, want)
				}
				if !strings.Contains(out, tc.said) {
					t.Errorf("stderr %q, want %q", out, tc.said)
				}
				if tc.deaf && !strings.Contains(out, "mate core: action onboard on shop had not stopped 45s after the stop began") {
					t.Errorf("stderr %q, want the engine to name the action it left", out)
				}
			})
		})
	}
}
