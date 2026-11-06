package main

// `mate diff <project> <crew>`'s command line. The text it prints is
// spawn.CrewDiff's, whose cases live in internal/spawn/diff_test.go; the
// fixture here is shared by the review and wake tests of this package.

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// diffFixture is a workspace with project "shop" on branch main and a crew
// "k3" in a real linked worktree on branch mate/k3, recorded in
// crews/k3.meta exactly the way spawn records it.
type diffFixture struct {
	w        *store.Workspace
	repo     string
	worktree string
}

func newDiffFixture(t *testing.T) diffFixture {
	t.Helper()
	w := liveCrewWorkspace(t, "shop")
	repo := filepath.Join(w.ProjectHome("shop"), "shop")
	// A file to change, so a diff has something to show that is not the
	// empty initial commit.
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("shop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitOrFatal(t, repo, "add", "README.md")
	runGitOrFatal(t, repo, "commit", "-m", "add README")

	worktree := w.WorktreeDir("shop", "k3")
	runGitOrFatal(t, repo, "worktree", "add", "-b", "mate/k3", worktree, "main")
	writeCrewMetaForDiff(t, w, worktree)
	return diffFixture{w: w, repo: repo, worktree: worktree}
}

func writeCrewMetaForDiff(t *testing.T, w *store.Workspace, worktree string) {
	t.Helper()
	rel, err := filepath.Rel(w.Root(), worktree)
	if err != nil {
		t.Fatal(err)
	}
	meta := map[string]string{
		spawn.MetaTask:     "add a line",
		spawn.MetaHarness:  "codex",
		spawn.MetaBranch:   "mate/k3",
		spawn.MetaWorktree: filepath.ToSlash(rel),
		spawn.MetaState:    spawn.CrewStateSpawned,
	}
	if err := w.WriteCrewMeta("shop", "k3", meta); err != nil {
		t.Fatalf("WriteCrewMeta: %v", err)
	}
}

// commitInWorktree makes one commit on the crew's branch.
func commitInWorktree(t *testing.T, worktree, name, body, subject string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(worktree, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitOrFatal(t, worktree, "add", name)
	runGitOrFatal(t, worktree, "commit", "-m", subject)
}

// TestDiffCommandParsesItsFlagsInAnyPosition is the CLI seam itself: the
// workspace flag after the positional arguments is the case reorderArgs
// exists for, and --stat is a boolean that must not swallow the next token.
func TestDiffCommandParsesItsFlagsInAnyPosition(t *testing.T) {
	t.Parallel()
	f := newDiffFixture(t)
	commitInWorktree(t, f.worktree, "README.md", "shop\nfirst\n", "add first line")

	var stdout, stderr bytes.Buffer
	if err := testCLI().run([]string{"diff", "shop", "k3", "--stat", "--workspace", f.w.Root()}, &stdout, &stderr); err != nil {
		t.Fatalf("mate diff: %v\n%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "1 file changed") {
		t.Fatalf("stdout = %q, want the --stat summary", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	err := testCLI().run([]string{"diff", "shop", "--workspace", f.w.Root()}, &stdout, &stderr)
	if err == nil {
		t.Fatal("diff with one argument must be a usage error")
	}
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Fatalf("error = %v (%T), want a usage error", err, err)
	}
}
