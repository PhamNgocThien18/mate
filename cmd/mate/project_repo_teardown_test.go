package main

import (
	"strings"
	"testing"
)

// `mate project repo teardown` prints, records and clears the command a
// crew stop runs in the worktree (CON-113), and refuses an unknown repo.
func TestProjectRepoTeardownRecordsTheCommand(t *testing.T) {
	t.Parallel()
	w := wakeFixture(t)
	cfg, err := w.LoadProject("shop")
	if err != nil {
		t.Fatal(err)
	}
	repo := cfg.Repos[0].Name
	code, out, _ := runMate(t, "project", "repo", "teardown", "shop", repo, "--workspace", w.Root())
	if code != 0 || !strings.Contains(out, "teardown: none") {
		t.Fatalf("unset: exit %d, %q", code, out)
	}

	code, out, _ = runMate(t, "project", "repo", "teardown", "shop", repo, "make worktree-destroy", "--workspace", w.Root())
	if code != 0 || !strings.Contains(out, "teardown: make worktree-destroy - runs with sh -c in each crew's worktree") {
		t.Fatalf("set: exit %d, %q", code, out)
	}
	if cfg, err := w.LoadProject("shop"); err != nil || cfg.Repos[0].Teardown != "make worktree-destroy" {
		t.Fatalf("project.yaml teardown = %q, %v", cfg.Repos[0].Teardown, err)
	}

	if code, _, _ = runMate(t, "project", "repo", "teardown", "shop", "nope", "make down", "--workspace", w.Root()); code == 0 {
		t.Fatal("an unknown repo was accepted")
	}

	code, out, _ = runMate(t, "project", "repo", "teardown", "shop", repo, "--clear", "--workspace", w.Root())
	if code != 0 || !strings.Contains(out, "teardown: none") {
		t.Fatalf("clear: exit %d, %q", code, out)
	}
	if cfg, _ := w.LoadProject("shop"); cfg.Repos[0].Teardown != "" {
		t.Fatalf("--clear left %q", cfg.Repos[0].Teardown)
	}
}
