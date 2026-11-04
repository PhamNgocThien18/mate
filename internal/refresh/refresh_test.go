package refresh

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/harness/codex"
	"github.com/nguyenngocanh94/mate/internal/outbox"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

func TestContextRefreshRequiresReceiptAndStartsFresh(t *testing.T) {
	// spoke-before and no-sent-log succeed: the captain-spoke check reads
	// sent.log only from where it ended when the stow was asked for, which
	// is zero when the file does not exist yet.
	succeeds := map[string]bool{"success": true, "spoke-before": true, "no-sent-log": true}
	for _, scenario := range []string{"success", "spoke-before", "no-sent-log", "no-receipt", "captain-returned", "changed-after-checkpoint", "no-stop"} {
		t.Run(scenario, func(t *testing.T) {
			w, deps := fixture(t, "shop")
			writeProjectDoc(t, w)
			before, rt, _ := startMate(t, w, deps)
			deps.NewSessionID = func() string { return "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" }
			cp := &receipt{ws: w}
			deps.Sleep = onStow(w, "shop", receiptTurn(w, cp, scenario))
			switch scenario {
			case "spoke-before":
				if err := w.AppendSent("shop", store.SentEntry{Source: store.SourceUser, Target: store.TargetMate, Text: "an earlier request"}); err != nil {
					t.Fatal(err)
				}
			case "no-sent-log":
				if err := os.Remove(w.SentLog("shop")); err != nil && !os.IsNotExist(err) {
					t.Fatal(err)
				}
			}
			changed, err := refresher(w, deps, cp).Run(context.Background(), "shop", false)
			if succeeds[scenario] {
				if err != nil || !changed {
					t.Fatalf("refresh: %v %v", changed, err)
				}
				assertFresh(t, w, rt, before)
			} else {
				if err == nil || changed {
					t.Fatalf("unsafe refresh accepted: %v %v", changed, err)
				}
				assertKept(t, w, rt, before)
			}
		})
	}
}

// receiptTurn is the Mate's turn on the stow line in each scenario of
// TestContextRefreshRequiresReceiptAndStartsFresh.
func receiptTurn(w *store.Workspace, cp *receipt, scenario string) func() error {
	return func() error {
		if scenario != "no-receipt" {
			if err := cp.answer("shop"); err != nil {
				return err
			}
		}
		if scenario == "captain-returned" {
			_ = w.AppendSent("shop", store.SentEntry{Source: store.SourceUser, Target: store.TargetMate, Text: "new request"})
		}
		if scenario == "changed-after-checkpoint" {
			_ = os.WriteFile(w.MemoryFile("shop"), []byte("changed"), 0644)
		}
		if scenario != "no-stop" {
			_ = w.AppendSent("shop", store.SentEntry{Source: store.SourceMate, Target: store.SourceUser, Text: "stowed"})
		}
		return nil
	}
}

func TestRefreshQuietRequiresAnAnsweredCaptainAndFiveMinutes(t *testing.T) {
	w, _ := fixture(t, "shop")
	now := time.Now()
	appendEntry := func(source, target string, at time.Time) {
		t.Helper()
		if err := w.AppendSent("shop", store.SentEntry{Source: source, Target: target, Time: at, Text: "line"}); err != nil {
			t.Fatal(err)
		}
	}
	appendEntry(store.SourceUser, store.TargetMate, now.Add(-10*time.Minute))
	appendEntry(store.SourceMate, store.CrewTarget("k3"), now.Add(-9*time.Minute))
	if ok, _ := mateQuiet(w, "shop", now); ok {
		t.Fatal("crew steer counted as answer")
	}
	appendEntry(store.SourceMate, store.SourceUser, now.Add(-6*time.Minute))
	if ok, err := mateQuiet(w, "shop", now); err != nil || !ok {
		t.Fatalf("quiet: %v %v", ok, err)
	}
	appendEntry(store.SourceUser, store.TargetMate, now.Add(-time.Minute))
	if ok, _ := mateQuiet(w, "shop", now); ok {
		t.Fatal("unanswered captain discarded")
	}
}

// claudeWithoutTurnEnd is Claude declaring no turn-end evidence.
type claudeWithoutTurnEnd struct{ claude.Claude }

func (c claudeWithoutTurnEnd) Capabilities() harness.Capabilities {
	caps := c.Claude.Capabilities()
	caps.TurnEnd = harness.Cap[harness.TurnEndEvidence]{Status: harness.CapUnknown, Reason: "a test harness that never measured it"}
	return caps
}

func TestAutomaticRefreshUsesOnlyCurrentSessionAndHonorsThreshold(t *testing.T) {
	for _, scenario := range []string{"old-session", "below-limit", "disabled", "held", "queued", "busy", "codex-meta", "no-turn-end", "eligible"} {
		t.Run(scenario, func(t *testing.T) {
			w, deps := fixture(t, "shop")
			started, rt, h := startMate(t, w, deps)
			tokens, session := int64(160000), started.SessionID
			if scenario == "below-limit" {
				tokens = 149999
			}
			if scenario == "old-session" {
				session = "prior-session"
			}
			dueForRefresh(t, w, deps, tokens, session)
			deps = spoilAutomatic(t, w, deps, rt, h, scenario)
			_, err := refresher(w, deps, &receipt{ws: w}).Run(context.Background(), "shop", true)
			if scenario == "eligible" {
				if err == nil || len(rt.SentText) != 1 {
					t.Fatalf("eligible refresh did not attempt strict stow: %v %v", err, rt.SentText)
				}
			} else if err != nil || len(rt.SentText) != 0 {
				t.Fatalf("ineligible refresh spoke: %v %v", err, rt.SentText)
			}
		})
	}
}

// spoilAutomatic is what keeps an automatic refresh from being due in each
// scenario of TestAutomaticRefreshUsesOnlyCurrentSessionAndHonorsThreshold
// that dueForRefresh's tokens and session alone do not.
func spoilAutomatic(t *testing.T, w *store.Workspace, deps spawn.Deps, rt *runtime.Fake, h runtime.AgentHandle, scenario string) spawn.Deps {
	t.Helper()
	switch scenario {
	case "disabled":
		cfg, _ := w.LoadProject("shop")
		cfg.Mate.RefreshContext = -1
		if err := w.SaveProject("shop", cfg); err != nil {
			t.Fatal(err)
		}
	case "held":
		if err := w.SetMode("shop", false); err != nil {
			t.Fatal(err)
		}
	case "busy":
		rt.SetReadOutput(h, claudeBusyScreen)
	case "codex-meta":
		// Quietness is judged by answers the Stop hook logs; a harness
		// whose turn-end evidence is not that log is never refreshed
		// automatically, whether it is Codex or a Claude declaring none
		// (no-turn-end). The Codex Mate shows an empty Codex composer, so
		// the turn-end gate is the only thing that keeps it from being
		// refreshed.
		meta, _ := w.ReadMateMeta("shop")
		meta[spawn.MetaHarness] = string(codex.KindCodex)
		if err := w.WriteMateMeta("shop", meta); err != nil {
			t.Fatal(err)
		}
		rt.SetReadOutput(h, codexEmptyScreen)
	case "no-turn-end":
		reg, err := harness.NewRegistry(nil, claudeWithoutTurnEnd{})
		if err != nil {
			t.Fatal(err)
		}
		deps.Harnesses = reg
	case "queued":
		if _, err := testOutbox(w, deps).Enqueue("shop", outbox.Request{Source: store.OutboxSourceAssign, Key: "new-work", Text: "resolve: new work"}); err != nil {
			t.Fatal(err)
		}
	}
	return deps
}

// A manual refresh over a Mate composer holding a draft is postponed in the
// words it has always used: "pending", not the draft state's own value.
func TestContextRefreshPostponesOverAPendingComposer(t *testing.T) {
	w, deps := fixture(t, "shop")
	_, rt, handle := startMate(t, w, deps)
	rt.SetReadOutput(handle, claudePendingScreen)

	changed, err := refresher(w, deps, &receipt{ws: w}).Run(context.Background(), "shop", false)
	if changed || err == nil || err.Error() != "refresh postponed: Mate composer is pending" {
		t.Fatalf("refresh = %v, %v; want postponed with the composer called pending", changed, err)
	}
}

// A captain's refresh over a Mate mid-turn is put off before anything is
// typed, saying why: busy is a deferral, not a failed stow. The console's
// automatic refresh is put off without a word (the busy scenario of
// TestAutomaticRefreshUsesOnlyCurrentSessionAndHonorsThreshold).
func TestContextRefreshPostponesOverABusyMate(t *testing.T) {
	w, deps := fixture(t, "shop")
	before, rt, handle := startMate(t, w, deps)
	rt.SetReadOutput(handle, claudeBusyScreen)

	changed, err := refresher(w, deps, &receipt{ws: w}).Run(context.Background(), "shop", false)
	if changed || err == nil || err.Error() != "refresh postponed: Mate composer is busy" {
		t.Fatalf("refresh = %v, %v; want postponed over a busy composer", changed, err)
	}
	if len(rt.SentText) != 0 {
		t.Fatalf("typed into a busy Mate: %q", rt.SentText)
	}
	assertKept(t, w, rt, before)
}

// claudeWithoutMCP is Claude declaring that it cannot be given MCP servers.
type claudeWithoutMCP struct{ claude.Claude }

func (c claudeWithoutMCP) Capabilities() harness.Capabilities {
	caps := c.Claude.Capabilities()
	caps.MCPConfig = harness.Cap[harness.MCPConfigurer]{Status: harness.CapUnsupported, Reason: "a test harness that cannot take them"}
	return caps
}

// brokenPlugins is a plugins.yaml naming a plugin that is not there.
const brokenPlugins = "apiVersion: mate.plugins/v1\ninstances:\n  broken:\n    path: plugins/missing\n    projects:\n      shop: {}\n"

// A refresh whose fresh start could not come up - the project's plugins do
// not resolve, or its harness cannot be given their MCP servers - keeps the
// running Mate: nothing is stowed for the first two, and when plugins.yaml
// breaks while the stow runs, nothing is stopped.
func TestContextRefreshKeepsTheMateWhenItsPluginsCannotStart(t *testing.T) {
	for _, tc := range []struct {
		scenario, want string
		stowed         bool
	}{
		{"broken-plugins", "plugin configuration is invalid: ", false},
		{"harness-without-mcp", "a test harness that cannot take them", false},
		{"broken-during-stow", "plugin configuration is invalid: ", true},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			w, deps := fixture(t, "shop")
			writeProjectDoc(t, w)
			before, rt, _ := startMate(t, w, deps)
			deps = unstartablePlugins(t, w, deps, tc.scenario)
			cp := &receipt{ws: w}
			deps.Sleep = onStow(w, "shop", func() error {
				if err := cp.answer("shop"); err != nil {
					return err
				}
				_ = w.AppendSent("shop", store.SentEntry{Source: store.SourceMate, Target: store.SourceUser, Text: "stowed"})
				if tc.scenario == "broken-during-stow" {
					return os.WriteFile(filepath.Join(w.StateDir(), "plugins.yaml"), []byte(brokenPlugins), 0o600)
				}
				return nil
			})

			changed, err := refresher(w, deps, cp).Run(context.Background(), "shop", false)
			if changed || err == nil || !strings.HasPrefix(err.Error(), "refresh postponed: a fresh Mate could not start: ") || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("refresh = %v, %v; want postponed naming %q", changed, err, tc.want)
			}
			if got := len(rt.SentText) == 1; got != tc.stowed {
				t.Fatalf("stow sent = %v (%q), want %v", got, rt.SentText, tc.stowed)
			}
			for _, c := range rt.Calls {
				if strings.HasPrefix(c, "StopAgent") {
					t.Fatalf("the Mate was stopped: %v", rt.Calls)
				}
			}
			assertKept(t, w, rt, before)
		})
	}
}

// unstartablePlugins sets up what keeps a fresh Mate from starting before
// the stow, in each scenario of
// TestContextRefreshKeepsTheMateWhenItsPluginsCannotStart that has it.
func unstartablePlugins(t *testing.T, w *store.Workspace, deps spawn.Deps, scenario string) spawn.Deps {
	t.Helper()
	pluginsYAML := filepath.Join(w.StateDir(), "plugins.yaml")
	switch scenario {
	case "broken-plugins":
		if err := os.WriteFile(pluginsYAML, []byte(brokenPlugins), 0o600); err != nil {
			t.Fatal(err)
		}
	case "harness-without-mcp":
		pkg := filepath.Join(w.Root(), "plugins", "tracker")
		if err := os.MkdirAll(pkg, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pkg, "mate-plugin.yaml"), []byte("apiVersion: mate.plugin/v1\nname: Tracker\nmcp:\n  type: http\n  url: https://mcp.tracker.example/mcp\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(pluginsYAML, []byte("apiVersion: mate.plugins/v1\ninstances:\n  tracker:\n    path: plugins/tracker\n    projects:\n      shop: {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		reg, err := harness.NewRegistry(nil, claudeWithoutMCP{})
		if err != nil {
			t.Fatal(err)
		}
		deps.Harnesses = reg
	}
	return deps
}

// Work queued for the Mate remains in the durable outbox: a refresh that
// finds some, before it asks for the stow or after, keeps the session and
// leaves the item queued for the Mate. Only the captain's refresh says why.
func TestContextRefreshLeavesQueuedWorkInTheOutbox(t *testing.T) {
	for _, tc := range []struct {
		scenario  string
		automatic bool
		// stowed is whether the stow line was typed before the queued
		// item was found.
		stowed bool
	}{
		{"queued-before-captain", false, false},
		{"queued-before-automatic", true, false},
		{"queued-during-stow", false, true},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			w, deps := fixture(t, "shop")
			writeProjectDoc(t, w)
			before, rt, _ := startMate(t, w, deps)
			if tc.automatic {
				// Everything else an automatic refresh waits for holds.
				dueForRefresh(t, w, deps, 160000, before.SessionID)
			}
			var queued int64
			enqueue := func() error {
				q, err := testOutbox(w, deps).Enqueue("shop", outbox.Request{Source: store.OutboxSourceAssign, Key: "new-work", Text: "resolve: new work"})
				queued = q.Item.ID
				return err
			}
			if !tc.stowed {
				if err := enqueue(); err != nil {
					t.Fatal(err)
				}
			}
			cp := &receipt{ws: w}
			deps.Sleep = onStow(w, "shop", func() error {
				if err := cp.answer("shop"); err != nil {
					return err
				}
				_ = w.AppendSent("shop", store.SentEntry{Source: store.SourceMate, Target: store.SourceUser, Text: "stowed"})
				return enqueue()
			})

			changed, err := refresher(w, deps, cp).Run(context.Background(), "shop", tc.automatic)
			want := fmt.Sprintf("refresh postponed: assign item %d is queued", queued)
			if tc.automatic && (changed || err != nil) {
				t.Fatalf("refresh = %v, %v; want put off without an error", changed, err)
			}
			if !tc.automatic && (changed || err == nil || err.Error() != want) {
				t.Fatalf("refresh = %v, %v; want %q", changed, err, want)
			}
			if got := len(rt.SentText) == 1; got != tc.stowed {
				t.Fatalf("stow sent = %v (%q), want %v", got, rt.SentText, tc.stowed)
			}
			assertKept(t, w, rt, before)
			assertQueued(t, w, queued)
		})
	}
}
