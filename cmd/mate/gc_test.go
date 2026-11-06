package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/teardown"
)

// gc collects for closed crews whose worktree is gone, never for a
// worktree that exists or a crew still open; without --apply it only lists
// (CON-114).
func TestGCTargetsOnlyWorktreesThatAreGone(t *testing.T) {
	t.Parallel()
	w := wakeFixture(t)
	live := filepath.Join(w.WorktreesDir(), "shop-live")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	for crew, meta := range map[string]map[string]string{
		"gone-1": {spawn.MetaState: spawn.CrewStateFinished, spawn.MetaWorktree: ".worktrees/shop-gone-1"},
		"rev-r1": {spawn.MetaState: spawn.CrewStateReviewed, spawn.MetaWorktree: ".worktrees/shop-rev-r1"},
		"live":   {spawn.MetaState: spawn.CrewStateFinished, spawn.MetaWorktree: ".worktrees/shop-live"},
		"open":   {spawn.MetaState: spawn.CrewStateSpawned, spawn.MetaWorktree: ".worktrees/shop-open"},
	} {
		if err := w.WriteCrewMeta("shop", crew, meta); err != nil {
			t.Fatal(err)
		}
	}
	var dry []bool
	var got []string
	d := gcDeps{
		Collect: func(_ context.Context, ts []teardown.Target, d bool) teardown.Report {
			dry = append(dry, d)
			for _, tg := range ts {
				got = append(got, tg.Name())
			}
			return teardown.Report{Removed: []string{"process sleep 4242"}}
		},
	}

	targets, r := runGC(context.Background(), w, d, false)
	slices.Sort(got)
	if want := []string{"shop-gone-1", "shop-rev-r1"}; !slices.Equal(got, want) {
		t.Fatalf("targets = %q, want %q", got, want)
	}
	if !slices.Equal(dry, []bool{true}) {
		t.Fatalf("dry = %v, want one dry run", dry)
	}
	out := gcReport(targets, r, false)
	if !strings.Contains(out, "dry run, nothing removed") || !strings.Contains(out, "would remove: process sleep 4242") {
		t.Fatalf("report:\n%s", out)
	}

	if _, _ = runGC(context.Background(), w, d, true); !slices.Equal(dry, []bool{true, false}) {
		t.Fatalf("dry = %v, want --apply to remove", dry)
	}
}

func TestGCScheduleRunsAtFirstTickThenEveryPeriodOneAtATime(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	var runs atomic.Int32
	release := make(chan struct{})
	g := &gcSchedule{every: time.Hour, now: func() time.Time { return now }, run: func(context.Context) {
		runs.Add(1)
		<-release
	}}
	g.Tick(context.Background())
	g.Tick(context.Background()) // still running
	now = now.Add(2 * time.Hour)
	g.Tick(context.Background()) // due, but the first is still running
	close(release)
	waitFor(t, func() bool { g.mu.Lock(); defer g.mu.Unlock(); return !g.running })
	if runs.Load() != 1 {
		t.Fatalf("runs = %d, want 1 while one was running", runs.Load())
	}
	g.Tick(context.Background()) // last was set at 9:00, now 11:00: due
	waitFor(t, func() bool { return runs.Load() == 2 })
	g.Tick(context.Background()) // just ran
	time.Sleep(20 * time.Millisecond)
	if runs.Load() != 2 {
		t.Fatalf("runs = %d, want 2", runs.Load())
	}
	var nilSchedule *gcSchedule
	nilSchedule.Tick(context.Background())
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
