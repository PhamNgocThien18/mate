package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/notice"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

type noticeFunc func(context.Context, string) (notice.Result, error)

func (f noticeFunc) Classify(ctx context.Context, screen string) (notice.Result, error) {
	return f(ctx, screen)
}

func TestConsoleNoticeReadsOnlySelectedAgent(t *testing.T) {
	t.Parallel()
	for _, target := range []string{"mate", "crew"} {
		t.Run(target, func(t *testing.T) {
			f := newBoxFixture(t)
			f.rt.SetReadOutput(f.mate, "Only 10% remains\n❯")
			f.rt.SetReadOutput(f.crew, "New update available\n›")
			before, _ := query.Load(context.Background(), f.ws, consoleHarnesses(), nil)
			callsBefore := len(f.rt.Calls)
			count := 0
			classifier := noticeFunc(func(ctx context.Context, screen string) (notice.Result, error) {
				count++
				want := "10%"
				if target == "crew" {
					want = "update"
				}
				if !strings.Contains(screen, want) {
					t.Errorf("read wrong agent: %q", screen)
				}
				return notice.Result{Label: "quota_warning", Confidence: .95}, nil
			})
			req := coreapi.ActionRequest{Action: coreapi.ActionNotice, Target: "shop", TargetKind: target}
			if target == "crew" {
				req.Crew = "k3"
			}
			text, err := engineNoticeAction(t, f.ws, f.deps, classifier)(context.Background(), req)
			if err != nil || count != 1 || !strings.Contains(text, "Advisory only") || !strings.Contains(text, "quota warning") {
				t.Fatalf("result %q, %v; calls=%d", text, err, count)
			}
			for _, call := range f.rt.Calls[callsBefore:] {
				if strings.Contains(call, "Send") || strings.Contains(call, "Prompt") || strings.Contains(call, "Stop") {
					t.Fatalf("mutating runtime call: %s", call)
				}
			}
			after, _ := query.Load(context.Background(), f.ws, consoleHarnesses(), nil)
			before.AsOf = after.AsOf
			if !reflect.DeepEqual(before, after) {
				t.Fatal("notice action changed workspace state")
			}
		})
	}
}

func TestConsoleNoticeFailureAndInvalidTarget(t *testing.T) {
	t.Parallel()
	f := newBoxFixture(t)
	count := 0
	c := noticeFunc(func(context.Context, string) (notice.Result, error) {
		count++
		return notice.Result{}, errors.New("unavailable")
	})
	action := engineNoticeAction(t, f.ws, f.deps, c)
	_, err := action(context.Background(), coreapi.ActionRequest{Action: coreapi.ActionNotice, Target: "shop", TargetKind: "workspace"})
	if err == nil || count != 0 {
		t.Fatal("invalid target reached classifier")
	}
	_, err = action(context.Background(), coreapi.ActionRequest{Action: coreapi.ActionNotice, Target: "shop", TargetKind: "mate"})
	if err == nil || count != 1 {
		t.Fatal("API failure hidden")
	}
}

// Not parallel: it sets MATE_JEV_API_KEY_FILE in the process environment.
func TestConsoleNoticeConfig(t *testing.T) {
	ws := noticeWorkspace(t)
	key := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(key, []byte("test-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := consoleNoticeClient(ws); c != nil || err != nil {
		t.Fatal("default is not off")
	}
	t.Setenv("MATE_JEV_API_KEY_FILE", key)
	if c, err := consoleNoticeClient(ws); c != nil || err != nil {
		t.Fatal("the process environment configured Jev")
	}
	writeEnv(t, ws, "MATE_JEV=on\n")
	if _, err := consoleNoticeClient(ws); err == nil || !strings.Contains(err.Error(), "MATE_JEV_API_KEY_FILE") {
		t.Fatalf("missing key path accepted: %v", err)
	}
	writeEnv(t, ws, "MATE_JEV=on\nMATE_JEV_API_KEY_FILE="+key+"\n")
	if c, err := consoleNoticeClient(ws); c == nil || err != nil {
		t.Fatalf("config: %v", err)
	}
	writeEnv(t, ws, "MATE_JEV=off\nMATE_JEV_API_KEY_FILE="+key+"\n")
	if c, err := consoleNoticeClient(ws); c != nil || err != nil {
		t.Fatalf("off with a key path still configured Jev: %v", err)
	}
	writeEnv(t, ws, "MATE_JEV=maybe\nMATE_JEV_API_KEY_FILE="+key+"\n")
	if _, err := consoleNoticeClient(ws); err == nil || !strings.Contains(err.Error(), "MATE_JEV") {
		t.Fatalf("unknown switch value accepted: %v", err)
	}
	writeEnv(t, ws, "MATE_JEV=on\nMATE_JEV_API_KEY_FILE="+filepath.Join(t.TempDir(), "missing")+"\n")
	if _, err := consoleNoticeClient(ws); err == nil {
		t.Fatal("missing key file accepted")
	}
	if err := os.WriteFile(key, []byte(" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeEnv(t, ws, "MATE_JEV=on\nMATE_JEV_API_KEY_FILE="+key+"\n")
	if _, err := consoleNoticeClient(ws); err == nil {
		t.Fatal("empty key accepted")
	}
	writeEnv(t, ws, "MATE_JEV=on\nnot a setting\n")
	if _, err := consoleNoticeClient(ws); err == nil || !strings.Contains(err.Error(), ".env") {
		t.Fatalf("malformed .env did not name itself: %v", err)
	}
}

// Not parallel: it sets HOME in the process environment.
func TestConsoleNoticeKeyPathResolvesHomeAndWorkspace(t *testing.T) {
	ws := noticeWorkspace(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, "jev-key"), []byte("k1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeEnv(t, ws, "MATE_JEV=on\nMATE_JEV_API_KEY_FILE=~/jev-key\n")
	if c, err := consoleNoticeClient(ws); c == nil || err != nil {
		t.Fatalf("~/ not expanded: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws.Root(), "local-key"), []byte("k2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeEnv(t, ws, "MATE_JEV=on\nMATE_JEV_API_KEY_FILE=local-key\n")
	if c, err := consoleNoticeClient(ws); c == nil || err != nil {
		t.Fatalf("relative path not resolved against the workspace root: %v", err)
	}
}

func noticeWorkspace(t *testing.T) *store.Workspace {
	t.Helper()
	ws, err := store.Init(t.TempDir(), workspaceDefaults())
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func writeEnv(t *testing.T, ws *store.Workspace, text string) {
	t.Helper()
	if err := os.WriteFile(ws.EnvFile(), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A headless Mate has no screen: the notice explanation reads its host's
// last turns instead, and asks Herdr nothing (CON-225).
// Not parallel: it sets HOME, CLAUDE_CONFIG_DIR in the process environment through headlessFixture.
func TestConsoleNoticeReadsAHeadlessMatesHost(t *testing.T) {
	w, deps, rt := headlessFixture(t)
	history := matehost.HistoryPath(w.MateDir("shop"))
	if err := os.MkdirAll(filepath.Dir(history), 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"at":"2026-11-05T10:00:00Z","source":"app","text":"wake","error":"API Error: 429 rate limit reached","done":true}` + "\n"
	if err := os.WriteFile(history, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	playHosts(t, w, &deps, "shop", func(l matehost.Launch, pid int) *matehost.Host {
		return matehost.New(&matehost.Runner{Launch: l, Turns: claude.Claude{}}, matehost.Options{PID: pid, History: history})
	})
	if _, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop", Mode: spawn.ModeHeadless}); err != nil {
		t.Fatal(err)
	}
	reads := len(rt.ReadCalls)
	classifier := noticeFunc(func(ctx context.Context, screen string) (notice.Result, error) {
		if !strings.Contains(screen, "429 rate limit") || !strings.Contains(screen, "> wake") {
			t.Errorf("screen = %q, want the host's last turn and its error", screen)
		}
		return notice.Result{Label: "quota_exhausted", Confidence: .9}, nil
	})
	text, err := engineNoticeAction(t, w, deps, classifier)(context.Background(), coreapi.ActionRequest{Action: coreapi.ActionNotice, Target: "shop", TargetKind: "mate"})
	if err != nil || !strings.Contains(text, "quota exhausted") {
		t.Fatalf("result %q, %v", text, err)
	}
	if len(rt.ReadCalls) != reads {
		t.Fatalf("Herdr was read: %q", rt.ReadCalls[reads:])
	}
}
