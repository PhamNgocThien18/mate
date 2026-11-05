package spawn

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// AddProjectOptions are the settings `mate project add` takes beyond the
// name. The Console's new-project form passes only Repos.
type AddProjectOptions struct {
	Repos     []store.RepoConfig // zero or more, each from RepoConfigFor
	BudgetUSD float64
}

// AddProject registers a Project the one way both the CLI and the Console
// do it, and seeds its PROJECT.md.
func AddProject(w *store.Workspace, name string, opts AddProjectOptions) (store.ProjectConfig, error) {
	cfg := store.ProjectConfig{Repos: opts.Repos}
	if opts.BudgetUSD > 0 {
		cfg.Budget = &store.BudgetConfig{ProjectUSD: opts.BudgetUSD}
	}
	if err := w.AddProject(name, cfg); err != nil {
		return store.ProjectConfig{}, fmt.Errorf("project add %s: %w", name, err)
	}
	if err := w.EnsureProjectDoc(name); err != nil {
		return store.ProjectConfig{}, fmt.Errorf("project add %s: %w", name, err)
	}
	return w.LoadProject(name)
}

// RepoConfigFor checks a repo the one way both the CLI and the Console do
// it - absRepo must be an existing directory that is the root of its git
// work tree - and fills its default branch from the repo when none is
// given. The store later proves the path is inside the workspace and owned
// by no other project. shownRepo is the path as the caller typed it, for
// error messages.
//
// The default branch is the branch HEAD points at (git.SymbolicHead), which
// a repository with no commit yet still has (docs/mvp.md M7); a detached
// HEAD falls back to store.DefaultBranch.
func RepoConfigFor(ctx context.Context, git gitx.Git, absRepo, shownRepo, name, defaultBranch string) (store.RepoConfig, error) {
	fi, err := os.Stat(absRepo)
	if err != nil {
		return store.RepoConfig{}, fmt.Errorf("repo path %s: %w", shownRepo, err)
	}
	if !fi.IsDir() {
		return store.RepoConfig{}, fmt.Errorf("repo path %s is not a directory", shownRepo)
	}
	top, err := git.Toplevel(ctx, absRepo)
	if err != nil {
		return store.RepoConfig{}, fmt.Errorf("repo path %s is not a git repository: %w", shownRepo, err)
	}
	resolvedRepo, err := filepath.EvalSymlinks(absRepo)
	if err != nil {
		return store.RepoConfig{}, err
	}
	resolvedTop, err := filepath.EvalSymlinks(top)
	if err != nil {
		return store.RepoConfig{}, err
	}
	if resolvedRepo != resolvedTop {
		return store.RepoConfig{}, fmt.Errorf("repo path %s is not the root of its git work tree (root is %s)", shownRepo, top)
	}
	if defaultBranch == "" {
		defaultBranch = store.DefaultBranch
		if head, err := git.SymbolicHead(ctx, absRepo); err == nil && head != "" {
			defaultBranch = head
		}
	}
	return store.RepoConfig{Name: name, Path: absRepo, DefaultBranch: defaultBranch}, nil
}
