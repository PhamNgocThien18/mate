package spawn_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
)

// The sender's clock and sleep are deps' when set, so a stow under a fake
// adapter runs at memory speed, and the sender's own defaults otherwise.
func TestMateOutboxDepsCarriesClockAndSleep(t *testing.T) {
	w := crewWorkspace(t, "shop")
	deps := fakeDeps(t, runtime.NewFake())
	at := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	slept := time.Duration(0)
	deps.Now = func() time.Time { return at }
	deps.Sleep = func(_ context.Context, d time.Duration) error { slept += d; return nil }

	od := spawn.MateOutboxDeps(w, deps)
	if od.Runtime != deps.Runtime || od.Handle == nil {
		t.Fatalf("MateOutboxDeps = %+v, want deps' runtime and the Mate's handle", od)
	}
	if od.Clock == nil || !od.Clock.Now().Equal(at) {
		t.Fatalf("Clock = %v, want deps.Now", od.Clock)
	}
	if od.Sleeper == nil {
		t.Fatal("Sleeper is nil, want deps.Sleep")
	}
	if err := od.Sleeper.Sleep(context.Background(), time.Second); err != nil || slept != time.Second {
		t.Fatalf("Sleeper.Sleep: err %v, slept %s; want deps.Sleep called", err, slept)
	}

	deps.Now, deps.Sleep = nil, nil
	od = spawn.MateOutboxDeps(w, deps)
	if od.Clock != nil || od.Sleeper != nil {
		t.Fatalf("Clock %v, Sleeper %v; want both unset when deps set neither", od.Clock, od.Sleeper)
	}
}

// Only the captain's stop asks the Mate to stow; every other stop says why
// none was asked for, and announces no wait.
func TestStowBeforeStopOnlyForTheCaptain(t *testing.T) {
	w := crewWorkspace(t, "shop")
	deps := fakeDeps(t, runtime.NewFake())
	for _, tc := range []struct {
		caller string
		noStow bool
		want   string
	}{
		{spawn.CallerUser, true, "not stowed: --no-stow"},
		{spawn.CallerMate, false, "not stowed: the Mate stopped itself"},
		{"crew", false, "not stowed: only the captain's stop asks the Mate to stow"},
	} {
		var stderr strings.Builder
		got, err := spawn.StowBeforeStop(context.Background(), w, deps, "shop", tc.caller, tc.noStow, &stderr)
		if err != nil || got != tc.want {
			t.Errorf("StowBeforeStop(%q, noStow %v) = %q, %v; want %q", tc.caller, tc.noStow, got, err, tc.want)
		}
		if stderr.Len() != 0 {
			t.Errorf("StowBeforeStop(%q) wrote %q to stderr, want nothing", tc.caller, stderr.String())
		}
	}
}
