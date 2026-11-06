package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// A headless Mate is refreshed in the host it runs in (CON-133): stopping
// and starting it once closed its pane and ended every attach on it, and
// today would end every REPL connected to its host. Its host runs outside
// Herdr (CON-225), and the refresh does not ask Herdr for anything either.
// Not parallel: it sets HOME, CLAUDE_CONFIG_DIR in the process environment.
func TestContextRefreshKeepsTheHeadlessMatesHost(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(claude.ClaudeConfigDirEnv, "")
	w, deps := consoleFixture(t, "shop")
	// The host's socket lives in the Mate directory, and a t.TempDir path
	// on macOS is longer than a unix socket path may be.
	root, err := os.MkdirTemp("/tmp", "rf")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	if w, err = store.Init(root, workspaceDefaults()); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(w.ProjectHome("shop"), "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)
	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: repo, DefaultBranch: "main"}}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.ProjectDoc("shop"), []byte("# Shop\nFixture project\n"), 0644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt := deps.Runtime.(*runtime.Fake)
	// playHosts plays `mate mate host`: a host on the Mate's socket whose
	// turns run a stand-in claude that answers every message.
	notes := make(chan string, 4)
	played := playHosts(t, w, &deps, "shop", func(l matehost.Launch, pid int) *matehost.Host {
		prog := filepath.Join(l.Dir, "fake-claude")
		script := "#!/bin/sh\ncat >/dev/null\necho '{\"type\":\"result\",\"result\":\"stowed\",\"session_id\":\"" + l.SessionID + "\"}'\n"
		if err := os.WriteFile(prog, []byte(script), 0o755); err != nil {
			t.Error(err)
		}
		l.Program = prog
		host := matehost.New(&matehost.Runner{
			Launch: l,
			Turns:  claude.Claude{},
			Lock:   func() (func(), bool, error) { return func() {}, true, nil },
			Opened: func(string, string) bool { return false },
		}, matehost.Options{PID: pid})
		host.Subscribe(func(ev matehost.Event) {
			if ev.Note != "" {
				notes <- ev.Note
			}
		})
		return host
	})
	before, err := spawn.StartMate(ctx, w, deps, spawn.StartRequest{Project: "shop", Mode: spawn.ModeHeadless})
	if err != nil {
		t.Fatal(err)
	}
	deps.NewSessionID = func() string { return "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" }
	finished := false
	deps.Sleep = func(ctx context.Context, d time.Duration) error {
		if !finished {
			items, err := w.ReadOutbox("shop")
			if err != nil {
				return err
			}
			for _, item := range items {
				if item.Source == store.OutboxSourceStow && item.State == store.OutboxSent {
					finished = true
					path, _ := refreshMeta(w, "shop", "refresh-request")
					req, _ := store.ReadMeta(path)
					if err := writeCheckpoint(w, "shop", req["nonce"]); err != nil {
						return err
					}
					_ = w.AppendSent("shop", store.SentEntry{Source: store.SourceMate, Target: store.SourceUser, Text: "stowed"})
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
			return nil
		}
	}
	calls := len(rt.Calls)

	changed, err := contextRefresh(ctx, w, deps, "shop", false)
	if err != nil || !changed {
		t.Fatalf("refresh: %v %v", changed, err)
	}
	for _, c := range rt.Calls[calls:] {
		if c == "RemoveTab" || c == "CreateAgentTab" || c == "RunInPane" || strings.HasPrefix(c, "StopAgent") || c == "InspectAgent" {
			t.Fatalf("the refresh called %s, want Herdr left alone: %q", c, rt.Calls[calls:])
		}
	}
	meta, err := w.ReadMateMeta("shop")
	if err != nil {
		t.Fatal(err)
	}
	if meta[spawn.MetaSessionID] != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" || meta[spawn.MetaPID] != fmt.Sprint(before.PID) || meta[spawn.MetaAgent] != before.Agent || played.count() != 1 {
		t.Fatalf("meta = %v, host starts %d, want a fresh session in host %d", meta, played.count(), before.PID)
	}
	archive, err := store.ReadMeta(filepath.Join(w.MateDir("shop"), "sessions", before.SessionID+".meta"))
	if err != nil || archive["session_id"] != before.SessionID {
		t.Fatalf("old session archive = %v, %v", archive, err)
	}
	select {
	case note := <-notes:
		if !strings.Contains(note, "aaaaaaaa") {
			t.Fatalf("host note = %q", note)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the host was not switched to the fresh session")
	}
}
