package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/memory"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// contextRefresh runs one refresh as `mate mate refresh` (automatic false)
// and the auto daemon (true) run it, through mateRefresher. The refresh's
// own tests live in internal/refresh; the ones here and in the other
// context_refresh_*_test.go files drive it end to end over the real
// checkpoint receipt.
func contextRefresh(ctx context.Context, w *store.Workspace, deps spawn.Deps, project string, automatic bool) (bool, error) {
	return mateRefresher(w, deps).Run(ctx, project, automatic)
}

// mateRefresher hands the refresh the receipt `mate checkpoint` writes: a
// Mate that stowed without running it, or whose memory changed after it,
// keeps its session.
func TestMateRefresherVerifiesTheCheckpointReceipt(t *testing.T) {
	for _, tc := range []struct{ scenario, wantErr string }{
		{"receipt", ""},
		{"no-receipt", "refresh: no checkpoint receipt for this turn"},
		{"changed-after-checkpoint", "refresh: memory changed after checkpoint; keeping the current session"},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			w, deps := consoleFixture(t, "shop")
			ctx := context.Background()
			if err := os.WriteFile(w.ProjectDoc("shop"), []byte("# Shop\nFixture project\n"), 0644); err != nil {
				t.Fatal(err)
			}
			before, err := spawn.StartMate(ctx, w, deps, spawn.StartRequest{Project: "shop"})
			if err != nil {
				t.Fatal(err)
			}
			deps.NewSessionID = func() string { return "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" }
			rt := deps.Runtime.(*runtime.Fake)
			handle, _, err := spawn.MateHandle(ctx, w, deps, "shop")
			if err != nil {
				t.Fatal(err)
			}
			rt.SetReadOutput(handle, claudeEmptyScreen)
			finished := false
			deps.Sleep = func(context.Context, time.Duration) error {
				if finished {
					return nil
				}
				items, err := w.ReadOutbox("shop")
				if err != nil {
					return err
				}
				for _, item := range items {
					if item.Source == store.OutboxSourceStow && item.State == store.OutboxSent {
						finished = true
						path, _ := refreshMeta(w, "shop", "refresh-request")
						req, _ := store.ReadMeta(path)
						if tc.scenario != "no-receipt" {
							if err := writeCheckpoint(w, "shop", req["nonce"]); err != nil {
								return err
							}
						}
						if tc.scenario == "changed-after-checkpoint" {
							_ = os.WriteFile(w.MemoryFile("shop"), []byte("changed"), 0644)
						}
						_ = w.AppendSent("shop", store.SentEntry{Source: store.SourceMate, Target: store.SourceUser, Text: "stowed"})
					}
				}
				return nil
			}

			changed, err := contextRefresh(ctx, w, deps, "shop", false)
			meta, readErr := w.ReadMateMeta("shop")
			if readErr != nil {
				t.Fatal(readErr)
			}
			if tc.wantErr == "" {
				if err != nil || !changed || meta[spawn.MetaSessionID] == before.SessionID {
					t.Fatalf("refresh = %v, %v; session %s", changed, err, meta[spawn.MetaSessionID])
				}
				return
			}
			if changed || err == nil || err.Error() != tc.wantErr || meta[spawn.MetaSessionID] != before.SessionID {
				t.Fatalf("refresh = %v, %v; want %q with session %s kept", changed, err, tc.wantErr, before.SessionID)
			}
		})
	}
}

func TestCheckpointRequiresEveryOpenCrewInFlight(t *testing.T) {
	w, deps := consoleFixture(t, "shop")
	ctx := context.Background()
	if _, err := spawn.StartMate(ctx, w, deps, spawn.StartRequest{Project: "shop"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.ProjectDoc("shop"), []byte("# Shop\n"), 0644); err != nil {
		t.Fatal(err)
	}
	meta, _ := w.ReadMateMeta("shop")
	path, _ := refreshMeta(w, "shop", "refresh-request")
	if err := store.WriteMeta(path, map[string]string{"nonce": "test-nonce", "session": meta[spawn.MetaSessionID]}); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteCrewMeta("shop", "auth", map[string]string{spawn.MetaState: spawn.CrewStateSpawned}); err != nil {
		t.Fatal(err)
	}
	if err := w.EditBacklog("shop", "add", "author", memory.BacklogInFlight, "unrelated item"); err != nil {
		t.Fatal(err)
	}
	if err := writeCheckpoint(w, "shop", "test-nonce"); err == nil || !strings.Contains(err.Error(), "auth absent") {
		t.Fatalf("missing crew accepted: %v", err)
	}
	if err := w.EditBacklog("shop", "add", "auth", memory.BacklogInFlight, "crew remains in flight"); err != nil {
		t.Fatal(err)
	}
	if err := writeCheckpoint(w, "shop", "test-nonce"); err != nil {
		t.Fatal(err)
	}
}
