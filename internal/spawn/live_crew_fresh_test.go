package spawn_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/brief/brieftest"
	"github.com/nguyenngocanh94/mate/internal/harness/catalog"
	"github.com/nguyenngocanh94/mate/internal/harness/codex"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/runtime/herdr"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// TestLiveCrewFreshSessionAfterFindings is CON-73's live proof: a real
// Codex crew is spawned, hands back (`pr-open`), and the Mate's findings
// open a fresh session for it - a new agent in the same worktree on the
// same branch, whose opening input on disk is exactly the four parts in
// order and whose first prompt points at that file. Nothing here starts a
// Herdr server: the runner provisions the lab.
func TestLiveCrewFreshSessionAfterFindings(t *testing.T) {
	requireLive(t)
	session, configHome := liveLabSession(t)

	root := t.TempDir()
	w, err := store.Init(root, store.Defaults{})
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	useLabSession(t, w, session)
	if w, err = store.Open(root); err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	repo := filepath.Join(w.ProjectHome("shop"), "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	liveGit(t, repo, "init", "-b", "main")
	liveGit(t, repo, "config", "user.email", "mate-test@example.com")
	liveGit(t, repo, "config", "user.name", "mate test")
	liveGit(t, repo, "commit", "--allow-empty", "-m", "init")
	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: repo, DefaultBranch: "main"}}}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	names := runtime.NewMemoryNameRegistry()
	rt := herdr.NewHerdr(process.ExecRunner{})
	rt.Names = names
	rt.StartServer = func(context.Context, string) error {
		t.Fatal("the lab session is provisioned by the runner; this test must not start a Herdr server")
		return nil
	}
	deps := spawn.Deps{
		Harnesses:            catalog.Default(),
		Runtime:              rt,
		Names:                names,
		ConfigHome:           configHome,
		Binary:               binaryPath(t),
		ReadinessTimeout:     90 * time.Second,
		StartupPromptTimeout: 60 * time.Second,
		StartTimeout:         60 * time.Second,
	}
	marker := filepath.Join(configHome, "mate", "session-owners", session)
	_ = os.Remove(marker)
	t.Cleanup(func() { _ = os.Remove(marker) })

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Minute)
		defer stopCancel()
		_, _ = spawn.StopCrew(stopCtx, w, deps, "shop", "k9", true)
	})

	res, err := spawn.SpawnCrew(ctx, w, deps, spawn.SpawnCrewRequest{
		Project: "shop", Crew: "k9", Harness: codex.KindCodex,
		BriefText: brieftest.Ship("Reply with the single word ok and do nothing else."),
	})
	if err != nil {
		t.Fatalf("SpawnCrew: %v", err)
	}
	// The first session's work: a commit, a hand-back, a PR line.
	if err := os.WriteFile(filepath.Join(res.Worktree, "round1.txt"), []byte("first round\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	liveGit(t, res.Worktree, "add", "round1.txt")
	liveGit(t, res.Worktree, "-c", "user.email=k9@example.com", "-c", "user.name=k9", "commit", "-q", "-m", "round one")
	if err := os.WriteFile(w.CrewHandback("shop", "k9"), []byte("## For the next crew\n- round1.txt is the deliverable\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendStatus("shop", "k9", "pr-open: https://github.com/example/shop/pull/1"); err != nil {
		t.Fatal(err)
	}

	findings := "Review: F1 round1.txt should end with a newline twice. Already tried: nothing, first round."
	again, err := spawn.RelaunchCrew(ctx, w, deps, "shop", "k9", "Already tried: nothing, first round",
		spawn.RelaunchOptions{Fresh: &spawn.FreshSession{Message: findings}})
	if err != nil {
		t.Fatalf("RelaunchCrew (fresh): %v", err)
	}
	if again.Pane == res.Pane || again.Agent == "" || !again.Stopped {
		t.Fatalf("fresh relaunch = %+v, want the resting agent stopped and a new live pane", again)
	}
	if again.Ordinal != 2 {
		t.Fatalf("Ordinal = %d, want 2", again.Ordinal)
	}
	live := runtime.AgentHandle{
		Session: runtime.SessionHandle{Name: session},
		Name:    again.Agent,
		RawID:   "k9",
		Kind:    codex.KindCodex,
		Tab: runtime.TabHandle{
			Session: runtime.SessionHandle{Name: session}, WorkspaceID: again.Workspace,
			TabID: again.Tab, PaneID: again.Pane, Label: spawn.CrewTabLabelPrefix + "k9",
		},
	}
	observed, err := rt.InspectAgent(ctx, live)
	if err != nil {
		t.Fatalf("the fresh agent is not live: %v", err)
	}
	if !observed.LiveHandleOK {
		t.Fatalf("the fresh agent's pane is stale: %+v", observed)
	}
	if got := liveGitOut(t, again.Worktree, "rev-parse", "--abbrev-ref", "HEAD"); got != "mate/k9" {
		t.Fatalf("the fresh session moved the worktree off its branch: HEAD = %q", got)
	}

	data, err := os.ReadFile(again.InputPath)
	if err != nil {
		t.Fatalf("session input: %v", err)
	}
	text := string(data)
	briefText, _ := os.ReadFile(res.BriefPath)
	at := -1
	for _, part := range []string{
		"# Part 1: your brief", strings.TrimSpace(string(briefText)),
		"# Part 2: your last hand-back", "round1.txt is the deliverable",
		"# Part 3: the Mate's message", findings,
		"# Part 4: change since the last review (git diff --stat main...HEAD)", "round1.txt",
	} {
		i := strings.Index(text, part)
		if i < 0 || i < at {
			t.Fatalf("session input lacks %q or has it out of order:\n%s", part, text)
		}
		at = i
	}
	meta, err := w.ReadCrewMeta("shop", "k9")
	if err != nil {
		t.Fatal(err)
	}
	if meta[spawn.MetaAgent] != again.Agent || meta[spawn.MetaSessions] != "2" {
		t.Fatalf("meta agent %q sessions %q, want %q and 2", meta[spawn.MetaAgent], meta[spawn.MetaSessions], again.Agent)
	}
}
