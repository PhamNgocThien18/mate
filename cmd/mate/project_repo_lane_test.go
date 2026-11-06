package main

import (
	"strings"
	"testing"
)

// `mate project repo lane` prints, records and clears the lane a repo's
// crews hold (CON-118), and refuses an unknown repo or a lane that is not
// one word.
func TestProjectRepoLaneRecordsTheLane(t *testing.T) {
	t.Parallel()
	w := wakeFixture(t)
	cfg, err := w.LoadProject("shop")
	if err != nil {
		t.Fatal(err)
	}
	repo := cfg.Repos[0].Name
	code, out, _ := runMate(t, "project", "repo", "lane", "shop", repo, "--workspace", w.Root())
	if code != 0 || !strings.Contains(out, "lane: none") {
		t.Fatalf("unset: exit %d, %q", code, out)
	}

	code, out, _ = runMate(t, "project", "repo", "lane", "shop", repo, "xcode", "--workspace", w.Root())
	if code != 0 || !strings.Contains(out, "lane: xcode - its crews hold the xcode lane while spawned or working, 2 at a time") {
		t.Fatalf("set: exit %d, %q", code, out)
	}
	if cfg, err := w.LoadProject("shop"); err != nil || cfg.Repos[0].Lane != "xcode" {
		t.Fatalf("project.yaml lane = %q, %v", cfg.Repos[0].Lane, err)
	}

	if code, _, _ = runMate(t, "project", "repo", "lane", "shop", "nope", "xcode", "--workspace", w.Root()); code == 0 {
		t.Fatal("an unknown repo was accepted")
	}
	if code, _, _ = runMate(t, "project", "repo", "lane", "shop", repo, "two words", "--workspace", w.Root()); code == 0 {
		t.Fatal("a two-word lane was accepted")
	}

	code, out, _ = runMate(t, "project", "repo", "lane", "shop", repo, "--clear", "--workspace", w.Root())
	if code != 0 || !strings.Contains(out, "lane: none") {
		t.Fatalf("clear: exit %d, %q", code, out)
	}
	if cfg, _ := w.LoadProject("shop"); cfg.Repos[0].Lane != "" {
		t.Fatalf("--clear left %q", cfg.Repos[0].Lane)
	}
}
