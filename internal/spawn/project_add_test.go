package spawn_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/memory"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// emptyWorkspace is a workspace with no project yet.
func emptyWorkspace(t *testing.T) *store.Workspace {
	t.Helper()
	w, err := store.Init(t.TempDir(), store.Defaults{})
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	return w
}

// TestRepoConfigForReadsTheDefaultBranchOfARepoWithNoCommit: a project may
// point at a repository nobody has committed to yet (docs/mvp.md M7). HEAD
// still names the branch the first commit will land on, and that is the
// project's default branch.
func TestRepoConfigForReadsTheDefaultBranchOfARepoWithNoCommit(t *testing.T) {
	w := emptyWorkspace(t)
	repo := filepath.Join(w.ProjectHome("shop"), "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "init", "--quiet", "-b", "trunk")

	cfg, err := spawn.RepoConfigFor(context.Background(), gitx.New(), repo, "shop/shop", "", "")
	if err != nil {
		t.Fatalf("RepoConfigFor: %v", err)
	}
	if cfg != (store.RepoConfig{Path: repo, DefaultBranch: "trunk"}) {
		t.Fatalf("RepoConfigFor = %+v, want trunk read from the empty repo's HEAD", cfg)
	}

	saved, err := spawn.AddProject(w, "shop", spawn.AddProjectOptions{Repos: []store.RepoConfig{cfg}})
	if err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	if len(saved.Repos) != 1 || saved.Repos[0].DefaultBranch != "trunk" {
		t.Fatalf("saved repos = %+v, want one on trunk", saved.Repos)
	}
}

// TestRepoConfigForFallsBackWhenHEADIsDetached: a detached HEAD names no
// branch, so the workspace default stands in.
func TestRepoConfigForFallsBackWhenHEADIsDetached(t *testing.T) {
	w := crewWorkspace(t, "shop")
	repo := filepath.Join(w.ProjectHome("shop"), "shop")
	git(t, repo, "checkout", "--quiet", "--detach", "HEAD")

	cfg, err := spawn.RepoConfigFor(context.Background(), gitx.New(), repo, "shop/shop", "", "")
	if err != nil {
		t.Fatalf("RepoConfigFor: %v", err)
	}
	if cfg.DefaultBranch != store.DefaultBranch {
		t.Fatalf("DefaultBranch = %q, want %q", cfg.DefaultBranch, store.DefaultBranch)
	}
}

// TestRepoConfigForKeepsAGivenDefaultBranchAndName: what the caller passed
// wins over what the repo says.
func TestRepoConfigForKeepsAGivenDefaultBranchAndName(t *testing.T) {
	w := crewWorkspace(t, "shop")
	repo := filepath.Join(w.ProjectHome("shop"), "shop")

	cfg, err := spawn.RepoConfigFor(context.Background(), gitx.New(), repo, "shop/shop", "api", "release")
	if err != nil {
		t.Fatalf("RepoConfigFor: %v", err)
	}
	if cfg != (store.RepoConfig{Name: "api", Path: repo, DefaultBranch: "release"}) {
		t.Fatalf("RepoConfigFor = %+v, want the given name and branch", cfg)
	}
}

// TestRepoConfigForRefusals: each refusal names the path as the caller
// typed it.
func TestRepoConfigForRefusals(t *testing.T) {
	w := crewWorkspace(t, "shop")
	repo := filepath.Join(w.ProjectHome("shop"), "shop")
	sub := filepath.Join(repo, "web")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(w.Root(), "notes.txt")
	if err := os.WriteFile(file, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A directory under the workspace root, with git's repository
	// discovery stopped above the workspace so a TMPDIR inside a checkout
	// does not make it part of one.
	plain := filepath.Join(w.Root(), "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(w.Root()))

	for _, tc := range []struct {
		name, abs, shown, want string
	}{
		{"missing", filepath.Join(w.Root(), "missing"), "missing", "repo path missing: "},
		{"file", file, "notes.txt", "repo path notes.txt is not a directory"},
		{"plain", plain, "plain", "repo path plain is not a git repository: unknown: git rev-parse --show-toplevel failed in "},
		{"subdir", sub, "shop/shop/web", "repo path shop/shop/web is not the root of its git work tree (root is "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := spawn.RepoConfigFor(context.Background(), gitx.New(), tc.abs, tc.shown, "", "")
			if err == nil || !strings.HasPrefix(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to start %q", err, tc.want)
			}
		})
	}
}

// TestAddProjectSeedsPROJECTmdOnceAndSetsTheBudget: the template is written
// for a new project, and a PROJECT.md already on disk - left by a project
// that was removed and is being added back - is kept as it is.
func TestAddProjectSeedsPROJECTmdOnceAndSetsTheBudget(t *testing.T) {
	w := emptyWorkspace(t)
	saved, err := spawn.AddProject(w, "shop", spawn.AddProjectOptions{BudgetUSD: 12.5})
	if err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	if len(saved.Repos) != 0 || saved.Budget == nil || saved.Budget.ProjectUSD != 12.5 {
		t.Fatalf("saved = %+v, want no repo and a 12.5 USD project budget", saved)
	}
	doc, err := os.ReadFile(w.ProjectDoc("shop"))
	if err != nil {
		t.Fatalf("PROJECT.md not seeded: %v", err)
	}
	if string(doc) != memory.ProjectTemplate("shop") {
		t.Fatalf("PROJECT.md = %q, want the template", doc)
	}

	if err := w.RemoveProject("shop"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.ProjectDoc("shop"), []byte("# shop\nedited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := spawn.AddProject(w, "shop", spawn.AddProjectOptions{}); err != nil {
		t.Fatalf("AddProject again: %v", err)
	}
	if doc, _ := os.ReadFile(w.ProjectDoc("shop")); string(doc) != "# shop\nedited\n" {
		t.Fatalf("PROJECT.md = %q, want the edited file kept", doc)
	}
}

// TestAddProjectNamesTheProjectInItsRefusal: a refusal from the store is
// prefixed the way `mate project add` has always printed it.
func TestAddProjectNamesTheProjectInItsRefusal(t *testing.T) {
	w := emptyWorkspace(t)
	if _, err := spawn.AddProject(w, "shop", spawn.AddProjectOptions{}); err != nil {
		t.Fatal(err)
	}
	_, err := spawn.AddProject(w, "shop", spawn.AddProjectOptions{})
	if err == nil || !strings.HasPrefix(err.Error(), "project add shop: ") {
		t.Fatalf("err = %v, want it prefixed with the project", err)
	}
}
