package refresh

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/catalog"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/timeline"
)

// The screens a Mate's composer is read from, as cmd/mate's console tests
// draw them: Claude's composer in its ruled box, empty, holding someone's
// half-typed line, or under the spinner of a turn; and Codex's, empty.
const (
	claudeRule          = "─────────────────────────────────────────────"
	claudeEmptyScreen   = claudeRule + "\n" + claude.ClaudeComposerMarker + "\n" + claudeRule + "\n"
	claudePendingScreen = claudeRule + "\n" + claude.ClaudeComposerMarker + " half typed\n" + claudeRule + "\n"
	claudeBusyScreen    = "✶ Pollinating… (3s · esc to interrupt)\n" + claudeEmptyScreen
	codexEmptyScreen    = "› Ask Codex to do anything\n\n  model · cwd\n"
)

// fixture is a workspace with one registered git project and a spawn.Deps
// over the fake Herdr adapter, built as cmd/mate's consoleFixture builds
// them: these tests ran on that fixture before the refresh moved here.
func fixture(t *testing.T, project string) (*store.Workspace, spawn.Deps) {
	t.Helper()
	reg := catalog.Default()
	var defaults store.Defaults
	if k, err := reg.Default(harness.RoleMate); err == nil {
		defaults.MateHarness = string(k)
	}
	if k, err := reg.Default(harness.RoleCrew); err == nil {
		defaults.CrewHarness = string(k)
	}
	w, err := store.Init(t.TempDir(), defaults)
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	repo := filepath.Join(w.ProjectHome(project), project)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, repo)
	if err := w.AddProject(project, store.ProjectConfig{Repos: []store.RepoConfig{{Path: repo, DefaultBranch: "main"}}}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	rt := runtime.NewFake()
	return w, spawn.Deps{
		Harnesses:            reg,
		Runtime:              rt,
		Names:                rt.Names,
		ConfigHome:           t.TempDir(),
		Binary:               filepath.Join(t.TempDir(), "mate"),
		ReadinessTimeout:     time.Second,
		StartupPromptTimeout: 50 * time.Millisecond,
		StartTimeout:         10 * time.Second,
		Sleep:                func(context.Context, time.Duration) error { return nil },
		Now:                  func() time.Time { return time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC) },
		NewSessionID:         func() string { return "11111111-2222-3333-4444-555555555555" },
	}
}

// writeProjectDoc writes the project doc a checkpoint receipt covers.
func writeProjectDoc(t *testing.T, w *store.Workspace) {
	t.Helper()
	if err := os.WriteFile(w.ProjectDoc("shop"), []byte("# Shop\nFixture project\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

// startMate starts the fixture's Mate and shows an empty composer in its
// pane: where every refresh here begins.
func startMate(t *testing.T, w *store.Workspace, deps spawn.Deps) (spawn.StartResult, *runtime.Fake, runtime.AgentHandle) {
	t.Helper()
	ctx := context.Background()
	started, err := spawn.StartMate(ctx, w, deps, spawn.StartRequest{Project: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	rt := deps.Runtime.(*runtime.Fake)
	handle, _, err := spawn.MateHandle(ctx, w, deps, "shop")
	if err != nil {
		t.Fatal(err)
	}
	rt.SetReadOutput(handle, claudeEmptyScreen)
	return started, rt, handle
}

// dueForRefresh makes the started Mate one an automatic refresh is due
// for, but for what the test changes after: the console's auto mode, an
// answer to the captain six minutes old, and the timeline measuring tokens
// of context in session. The timeline stays open until the test ends.
func dueForRefresh(t *testing.T, w *store.Workspace, deps spawn.Deps, tokens int64, session string) {
	t.Helper()
	if err := w.SetMode("shop", true); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendSent("shop", store.SentEntry{Source: store.SourceMate, Target: store.SourceUser, Time: deps.Now().Add(-6 * time.Minute), Text: "answered"}); err != nil {
		t.Fatal(err)
	}
	handle, err := db.Open(w)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { handle.Close() })
	seedMateContext(t, handle, "shop", tokens)
	if _, err := handle.SQL().Exec(`UPDATE session SET harness_session_id=?`, session); err != nil {
		t.Fatal(err)
	}
}

// assertFresh fails unless the Mate runs a session other than the one it
// started with, from a second start that did not resume.
func assertFresh(t *testing.T, w *store.Workspace, rt *runtime.Fake, before spawn.StartResult) {
	t.Helper()
	meta, err := w.ReadMateMeta("shop")
	if err != nil {
		t.Fatal(err)
	}
	if meta[spawn.MetaSessionID] == before.SessionID || meta[spawn.MetaResumed] == "true" {
		t.Fatalf("resumed old context: %v", meta)
	}
	if len(rt.StartArgv) != 2 || strings.Contains(strings.Join(rt.StartArgv[1], " "), "--resume") {
		t.Fatalf("starts: %v", rt.StartArgv)
	}
}

// assertKept fails unless the Mate still runs the session it started with,
// from its one start.
func assertKept(t *testing.T, w *store.Workspace, rt *runtime.Fake, before spawn.StartResult) {
	t.Helper()
	meta, err := w.ReadMateMeta("shop")
	if err != nil {
		t.Fatal(err)
	}
	if meta[spawn.MetaSessionID] != before.SessionID || len(rt.StartArgv) != 1 {
		t.Fatalf("old session lost: meta %v, starts %d", meta, len(rt.StartArgv))
	}
}

// assertQueued fails unless outbox item id still waits for the Mate.
func assertQueued(t *testing.T, w *store.Workspace, id int64) {
	t.Helper()
	items, err := w.ReadOutbox("shop")
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ID == id && item.Queued() {
			return
		}
	}
	t.Fatalf("item %d is no longer queued: %+v", id, items)
}

// gitInit makes dir a git repository with one commit on main.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "mate-test@example.com"},
		{"config", "user.name", "mate test"},
		{"commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// refresher is the refresher cmd/mate's mateRefresher builds, with the
// receipt played by c. Deps holds a copy of deps, so build it after the
// test has set deps' Sleep, NewSessionID and Harnesses.
func refresher(w *store.Workspace, deps spawn.Deps, c *receipt) *Refresher {
	return New(w, Deps{Spawn: deps, Outbox: spawn.MateOutbox(w, deps), RequestPath: c.requestPath, VerifyCheckpoint: c.verify})
}

// onStow is a deps.Sleep that plays the Mate's turn on the stow line: the
// first pause that finds the line typed runs turn.
func onStow(w *store.Workspace, project string, turn func() error) func(context.Context, time.Duration) error {
	finished := false
	return func(context.Context, time.Duration) error {
		if finished {
			return nil
		}
		items, err := w.ReadOutbox(project)
		if err != nil {
			return err
		}
		for _, item := range items {
			if item.Source == store.OutboxSourceStow && item.State == store.OutboxSent {
				finished = true
				return turn()
			}
		}
		return nil
	}
}

// receipt plays `mate checkpoint` (cmd/mate/checkpoint.go) as far as a
// refresh can tell: the request lives where cmd/mate keeps it, and the
// receipt names the request's nonce and session and holds the memory,
// backlog and project doc as they were when the Mate wrote it. What
// checkpoint.go checks besides - the backlog's sections, every open crew in
// flight - is its own, tested beside it in cmd/mate, where
// TestMateRefresherVerifiesTheCheckpointReceipt runs a refresh over it.
type receipt struct {
	ws      *store.Workspace
	written map[string]string
}

func (c *receipt) requestPath(project string) (string, error) {
	return filepath.Join(c.ws.MateDir(project), ".refresh-request.meta"), nil
}

// answer is the stowing Mate running the `mate checkpoint` its stow line
// names: a receipt for the request, whose session must still be the Mate's.
func (c *receipt) answer(project string) error {
	path, err := c.requestPath(project)
	if err != nil {
		return err
	}
	req, err := store.ReadMeta(path)
	if err != nil {
		return err
	}
	meta, err := c.ws.ReadMateMeta(project)
	if err != nil {
		return err
	}
	if req["nonce"] == "" || req["session"] != meta[spawn.MetaSessionID] {
		return errors.New("checkpoint: no matching refresh request for this session")
	}
	files, err := c.files(project)
	if err != nil {
		return err
	}
	files["nonce"], files["session"] = req["nonce"], req["session"]
	c.written = files
	return nil
}

func (c *receipt) verify(project, nonce, session string) error {
	if c.written["nonce"] != nonce || c.written["session"] != session {
		return errors.New("refresh: no checkpoint receipt for this turn")
	}
	files, err := c.files(project)
	if err != nil {
		return err
	}
	for key, data := range files {
		if c.written[key] != data {
			return fmt.Errorf("refresh: %s changed after checkpoint; keeping the current session", key)
		}
	}
	return nil
}

func (c *receipt) files(project string) (map[string]string, error) {
	out := map[string]string{}
	for key, path := range map[string]string{"memory": c.ws.MemoryFile(project), "backlog": c.ws.BacklogFile(project), "project": c.ws.ProjectDoc(project)} {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		out[key] = string(data)
	}
	return out, nil
}

// seedMateContext records one turn of the project's Mate carrying tokens of
// context, with a pricing row that knows no context window: cmd/mate's
// seedActorTurnAndPricing for a Mate. The test names the turn's harness
// session itself.
func seedMateContext(t *testing.T, handle *db.DB, project string, tokens int64) {
	t.Helper()
	actor := timeline.MateActorID(project)
	now := db.FormatTime(time.Now())
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO actor(id, project, kind, name) VALUES (?, ?, ?, ?)`, []any{actor, project, "mate", actor}},
		{`INSERT INTO session(id, actor_id, started_at) VALUES (?, ?, ?)`, []any{actor + "#s", actor, now}},
		{`INSERT INTO turn(id, actor_id, session_id, started_at, ended_at, model, input_tokens, cache_read_tokens, cache_write_tokens, output_tokens, context_tokens_after)
		  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, []any{actor + "#t", actor, actor + "#s", now, now, "unpriced", tokens, 0, 0, 1, tokens}},
		{`INSERT INTO pricing(model, context_window) VALUES (?, ?)
		  ON CONFLICT(model) DO UPDATE SET context_window = excluded.context_window`, []any{"unpriced", 0}},
	} {
		if _, err := handle.SQL().Exec(q.sql, q.args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}
