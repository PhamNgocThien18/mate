package wake

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// fixture is a project "shop" with a ship k3 that handed back on a branch
// one commit ahead of main, its reviewer, a Crew asking a question, one
// still working and one closed. It is cmd/mate's wakeFixture (over its
// newDiffFixture), which these tests ran on before the wake moved here.
func fixture(t *testing.T) *store.Workspace {
	t.Helper()
	w, err := store.Init(t.TempDir(), store.Defaults{})
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	repo := filepath.Join(w.ProjectHome("shop"), "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "config", "user.email", "mate-test@example.com")
	runGit(t, repo, "config", "user.name", "mate test")
	runGit(t, repo, "commit", "--allow-empty", "-m", "init")
	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: repo, DefaultBranch: "main"}}}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	commit(t, repo, "README.md", "shop\n", "add README")

	worktree := w.WorktreeDir("shop", "k3")
	runGit(t, repo, "worktree", "add", "-b", "mate/k3", worktree, "main")
	rel, err := filepath.Rel(w.Root(), worktree)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WriteCrewMeta("shop", "k3", map[string]string{
		spawn.MetaTask:     "add a line",
		spawn.MetaHarness:  "codex",
		spawn.MetaBranch:   "mate/k3",
		spawn.MetaWorktree: filepath.ToSlash(rel),
		spawn.MetaState:    spawn.CrewStateSpawned,
	}); err != nil {
		t.Fatalf("WriteCrewMeta: %v", err)
	}
	commit(t, worktree, "README.md", "shop\nmore\n", "add a line")

	if err := os.MkdirAll(w.CrewDir("shop", "k3"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.CrewHandback("shop", "k3"), []byte("## For the next crew\nREADME now has two lines.\nThe build is `make`.\n\n| criterion | pass/fail | command | output |\n| --- | --- | --- | --- |\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendStatus("shop", "k3", "working: adding the line"); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendStatus("shop", "k3", "wait-mate: ready in branch mate/k3"); err != nil {
		t.Fatal(err)
	}
	for crew, meta := range map[string]map[string]string{
		"rev-k3":   {spawn.MetaHarness: "claude", spawn.MetaModel: "sonnet", spawn.MetaEffort: "medium", spawn.MetaKind: "scout", spawn.MetaState: spawn.CrewStateSpawned, spawn.MetaTask: "Review k3"},
		"ask-k4":   {spawn.MetaHarness: "codex", spawn.MetaState: spawn.CrewStateSpawned, spawn.MetaTask: "ask"},
		"busy-k5":  {spawn.MetaHarness: "codex", spawn.MetaState: spawn.CrewStateSpawned, spawn.MetaTask: "busy"},
		"done-old": {spawn.MetaHarness: "codex", spawn.MetaState: "finished", spawn.MetaTask: "old"},
	} {
		if err := w.WriteCrewMeta("shop", crew, meta); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(w.CrewDir("shop", "rev-k3"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteMeta(w.CrewReviewMeta("shop", "rev-k3"), map[string]string{"crew": "k3"}); err != nil {
		t.Fatal(err)
	}
	for crew, line := range map[string]string{
		"rev-k3":   "working: reading the diff",
		"ask-k4":   "needs-decision: keep the old endpoint, or remove it now that nothing calls it? Removing it breaks the mobile app v1.2.",
		"busy-k5":  "working: running the test suite",
		"done-old": "wait-mate: long done",
	} {
		if err := w.AppendStatus("shop", crew, line); err != nil {
			t.Fatal(err)
		}
	}
	return w
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func commit(t *testing.T, dir, name, body, subject string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", name)
	runGit(t, dir, "commit", "-m", subject)
}

func run(t *testing.T, w *store.Workspace) string {
	t.Helper()
	return runAdvancing(t, w, true)
}

func runAdvancing(t *testing.T, w *store.Workspace, advance bool) string {
	t.Helper()
	text, err := Render(context.Background(), w, Deps{Git: gitx.New()}, "shop", time.Date(2026, 10, 14, 22, 0, 0, 0, time.UTC), advance)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return text
}
