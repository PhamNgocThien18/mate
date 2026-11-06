package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// mergeCLIWorkspace builds a workspace with one project and one crew that
// Herdr has no record of: `agent=` is absent, which is the state a crew
// reaches after a crash and the state StopCrew handles without asking Herdr
// anything. That is what lets `mate merge` - which wires spawn.LiveDeps()
// on purpose, so the CLI path under test is the real one - run in a unit
// test without a live session.
func mergeCLIWorkspace(t *testing.T) (*store.Workspace, string) {
	t.Helper()
	root := t.TempDir()
	w, err := store.Init(root, workspaceDefaults())
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	repo := filepath.Join(w.ProjectHome("shop"), "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)
	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: repo, DefaultBranch: "main"}}}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	worktree := filepath.Join(w.Root(), ".worktrees", "shop-k3")
	if err := gitx.New().AddWorktree(context.Background(), repo, worktree, "mate/k3", "main"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitOrFatal(t, worktree, "add", "feature.txt")
	runGitOrFatal(t, worktree, "commit", "-m", "crew commit")

	if err := w.WriteCrewMeta("shop", "k3", map[string]string{
		spawn.MetaTask:     "add a feature",
		spawn.MetaHarness:  "codex",
		spawn.MetaBranch:   "mate/k3",
		spawn.MetaWorktree: ".worktrees/shop-k3",
		spawn.MetaState:    spawn.CrewStateSpawned,
	}); err != nil {
		t.Fatalf("WriteCrewMeta: %v", err)
	}
	return w, root
}

func TestCmdMergePrintsOneLineAndFinishesTheCrew(t *testing.T) {
	w, root := mergeCLIWorkspace(t)

	var out, errw bytes.Buffer
	if err := testCLI().cmdMerge([]string{"--workspace", root, "shop", "k3"}, &out, &errw); err != nil {
		t.Fatalf("merge: %v\nstderr: %s", err, errw.String())
	}
	line := strings.TrimSpace(out.String())
	for _, want := range []string{
		"shop/k3: merged 1 commit(s) into main (",
		"); crew finished, worktree and branch removed",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("merge printed %q, want it to contain %q", line, want)
		}
	}
	if n := len(strings.Split(line, "\n")); n != 1 {
		t.Fatalf("merge printed %d lines, want exactly one:\n%s", n, line)
	}
	// The learning record gets its usage section on the merge (CON-130),
	// silently: without a timeline it says unknown.
	if rec, err := os.ReadFile(w.CrewLearning("shop", "k3")); err != nil || !strings.Contains(string(rec), "## Usage (app)\nunknown: db: no timeline") {
		t.Fatalf("learning record after merge: %v\n%s", err, rec)
	}

	meta, err := w.ReadCrewMeta("shop", "k3")
	if err != nil {
		t.Fatal(err)
	}
	if meta[spawn.MetaState] != spawn.CrewStateFinished {
		t.Fatalf("crew state = %q, want finished", meta[spawn.MetaState])
	}
}

// TestCmdMergeReadsTheCallerFromThePaneEnvironment is the whole of how a
// Mate is told apart from the captain: MATE_CALLER, which spawn injects
// into the Mate's pane, and which no flag can override. A Mate merges only
// reviewed work (docs/mvp.md M19); the captain is never gated.
func TestCmdMergeReadsTheCallerFromThePaneEnvironment(t *testing.T) {
	_, root := mergeCLIWorkspace(t)
	var out, errw bytes.Buffer
	err := testCLI().withEnv("MATE_CALLER", "mate").cmdMerge([]string{"--workspace", root, "shop", "k3"}, &out, &errw)
	if err == nil {
		t.Fatal("a Mate must be refused without --review")
	}
	if !strings.Contains(err.Error(), "merge refused: the Mate merges only reviewed work") {
		t.Fatalf("err = %v, want the review refusal", err)
	}
	if out.String() != "" {
		t.Fatalf("a refused merge printed %q", out.String())
	}

	if err := testCLI().withEnv("MATE_CALLER", "crew").cmdMerge([]string{"--workspace", root, "shop", "k3"}, &out, &errw); err == nil || !strings.Contains(err.Error(), "a crew cannot merge its own branch") {
		t.Fatalf("a crew's merge: err = %v, want the crew refusal", err)
	}

	out.Reset()
	if err := testCLI().cmdMerge([]string{"--workspace", root, "shop", "k3"}, &out, &errw); err != nil {
		t.Fatalf("the captain's merge: %v", err)
	}
	if !strings.Contains(out.String(), "crew finished") {
		t.Fatalf("the captain's merge printed %q", out.String())
	}
}

func TestCmdMergeWantsTwoArguments(t *testing.T) {
	_, root := mergeCLIWorkspace(t)
	var out, errw bytes.Buffer
	err := testCLI().cmdMerge([]string{"--workspace", root, "shop"}, &out, &errw)
	var ue *usageError
	if err == nil || !errors.As(err, &ue) {
		t.Fatalf("err = %v, want a usage error", err)
	}
}

func TestRunDispatchesMerge(t *testing.T) {
	_, root := mergeCLIWorkspace(t)
	var out, errw bytes.Buffer
	if err := testCLI().run([]string{"merge", "shop", "k3", "--workspace", root}, &out, &errw); err != nil {
		t.Fatalf("run merge: %v\nstderr: %s", err, errw.String())
	}
	if !strings.Contains(out.String(), "shop/k3: merged") {
		t.Fatalf("run merge printed %q", out.String())
	}
}

// `project yolo` and `project mode` were removed by docs/mvp.md M19; a Mate
// quoting an old manual is told what replaced them.
func TestCmdProjectYoloAndModeAreGone(t *testing.T) {
	_, root := mergeCLIWorkspace(t)
	for _, sub := range []string{"yolo", "mode"} {
		var out, errw bytes.Buffer
		err := testCLI().cmdProject([]string{sub, "--workspace", root, "shop", "on"}, &out, &errw)
		var ue *usageError
		if err == nil || !errors.As(err, &ue) || !strings.Contains(err.Error(), "--deliver local|pr") || !strings.Contains(err.Error(), "--review") {
			t.Fatalf("project %s: err = %v, want a usage error naming --deliver and --review", sub, err)
		}
	}
}

// recordPassingReview writes what `mate review` and the reviewer leave
// behind for k3 of mergeCLIWorkspace: the review record at the branch's
// head, a handed-back reviewer, and a report with the given body.
func recordPassingReview(t *testing.T, w *store.Workspace, reviewer, report string) {
	t.Helper()
	dir := w.CrewDir("shop", "k3")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"brief.md", "handback.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("contract"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fp, err := reviewFingerprint(context.Background(), w, gitx.New(), "shop", "k3")
	if err != nil {
		t.Fatal(err)
	}
	path, err := reviewRecordPath(w, "shop", reviewer)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WriteMeta(path, fp); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteCrewMeta("shop", reviewer, map[string]string{spawn.MetaState: spawn.CrewStateSpawned, spawn.MetaKind: "scout"}); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendStatus("shop", reviewer, "wait-mate: review done"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.CrewReport("shop", reviewer), []byte(report), 0o644); err != nil {
		t.Fatal(err)
	}
}

const passWithFindings = "## Summary\nFine, two things to tidy.\n## Findings\n- feature.txt:1 the word is misspelt\n- no test covers the empty case\n## Verdict\npass\n"

// A passing review that left findings standing does not merge until the
// findings are recorded somewhere `--followups` can name (CON-74): the
// refusal prints them; a backlog line under ## Queued or a tracker id is
// accepted, checked, and recorded in the crew's meta.
func TestCmdMergeWantsFollowupsForAPassingReviewsFindings(t *testing.T) {
	refuse := func(t *testing.T, w *store.Workspace, root string, args []string, want ...string) {
		t.Helper()
		var out, errw bytes.Buffer
		err := testCLI().cmdMerge(append([]string{"--workspace", root, "shop", "k3"}, args...), &out, &errw)
		if err == nil {
			t.Fatalf("merge went through:\n%s", out.String())
		}
		for _, s := range want {
			if !strings.Contains(err.Error(), s) {
				t.Errorf("error %q lacks %q", err.Error(), s)
			}
		}
		if meta, _ := w.ReadCrewMeta("shop", "k3"); meta[spawn.MetaState] != spawn.CrewStateSpawned {
			t.Fatalf("a refused merge changed the crew: %v", meta)
		}
	}

	t.Run("refused without --followups", func(t *testing.T) {
		w, root := mergeCLIWorkspace(t)
		recordPassingReview(t, w, "rev", passWithFindings)
		refuse(t, w, root, []string{"--review", "rev"},
			"merge refused: review rev passed but left 2 finding(s) standing; record them and name where with --followups backlog:<text of the ## Queued line> or --followups <TRACKER-id>, then merge again:",
			"\n  F1: feature.txt:1 the word is misspelt\n  F2: no test covers the empty case")
	})
	t.Run("refused when the backlog line is not there", func(t *testing.T) {
		w, root := mergeCLIWorkspace(t)
		recordPassingReview(t, w, "rev", passWithFindings)
		if err := os.WriteFile(w.BacklogFile("shop"), []byte("# Backlog\n\n## In flight\n- k3: tidy feature.txt\n\n## Held for the captain\n\n## Queued\n- other: unrelated\n\n## Done\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		refuse(t, w, root, []string{"--review", "rev", "--followups", "backlog:tidy feature.txt"}, "no line under ## Queued", `"tidy feature.txt"`)
		refuse(t, w, root, []string{"--review", "rev", "--followups", "hvn-12"}, "--followups wants backlog:<text")
	})
	for name, ref := range map[string]string{"backlog line": "backlog:tidy feature.txt", "tracker id": "HVN-12"} {
		t.Run(name, func(t *testing.T) {
			w, root := mergeCLIWorkspace(t)
			recordPassingReview(t, w, "rev", passWithFindings)
			if err := os.WriteFile(w.BacklogFile("shop"), []byte("# Backlog\n\n## In flight\n\n## Held for the captain\n\n## Queued\n- k3-tidy: tidy feature.txt and cover the empty case (from review rev)\n\n## Done\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			var out, errw bytes.Buffer
			if err := testCLI().cmdMerge([]string{"--workspace", root, "shop", "k3", "--review", "rev", "--followups", ref}, &out, &errw); err != nil {
				t.Fatalf("merge: %v\n%s", err, errw.String())
			}
			lines := strings.Split(strings.TrimSpace(out.String()), "\n")
			if len(lines) != 2 || !strings.HasPrefix(lines[0], "shop/k3: merged 1 commit(s) into main (") || lines[1] != "followups: "+ref+" (recorded as followups= in "+w.CrewMeta("shop", "k3")+")" {
				t.Fatalf("merge printed:\n%s", out.String())
			}
			meta, err := w.ReadCrewMeta("shop", "k3")
			if err != nil {
				t.Fatal(err)
			}
			if meta[store.MetaFollowups] != ref || meta[spawn.MetaState] != "finished" {
				t.Fatalf("meta after merge = %v", meta)
			}
		})
	}
	t.Run("a review with no findings needs none", func(t *testing.T) {
		w, root := mergeCLIWorkspace(t)
		recordPassingReview(t, w, "rev", "## Summary\nClean.\n## Findings\nnone\n## Verdict\npass\n")
		var out, errw bytes.Buffer
		if err := testCLI().cmdMerge([]string{"--workspace", root, "shop", "k3", "--review", "rev"}, &out, &errw); err != nil {
			t.Fatalf("merge: %v\n%s", err, errw.String())
		}
		if meta, _ := w.ReadCrewMeta("shop", "k3"); meta[store.MetaFollowups] != "" || meta[spawn.MetaState] != "finished" {
			t.Fatalf("meta after merge = %v", meta)
		}
	})
}
