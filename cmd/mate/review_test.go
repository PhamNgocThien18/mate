package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/brief"
	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

func TestReviewRejectsChangedCommitOrAcceptanceContract(t *testing.T) {
	t.Parallel()
	for _, change := range []string{"commit", "brief", "handback", "base", "dirty", "verdict"} {
		t.Run(change, func(t *testing.T) {
			f := newDiffFixture(t)
			commitInWorktree(t, f.worktree, "README.md", "review this\n", "change")
			dir := filepath.Dir(f.w.CrewBrief("shop", "k3"))
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"brief.md", "handback.md"} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte("original acceptance evidence"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			fp, err := reviewFingerprint(context.Background(), f.w, gitx.New(), "shop", "k3")
			if err != nil {
				t.Fatal(err)
			}
			if problems := brief.Check(reviewBrief(f.w, "shop", "k3", fp, nil, nil), brief.Scout); len(problems) > 0 {
				t.Fatal(problems)
			}
			path, err := reviewRecordPath(f.w, "shop", "review-k3")
			if err != nil {
				t.Fatal(err)
			}
			if err := store.WriteMeta(path, fp); err != nil {
				t.Fatal(err)
			}
			if err := f.w.WriteCrewMeta("shop", "review-k3", map[string]string{spawn.MetaState: spawn.CrewStateSpawned, spawn.MetaKind: "scout"}); err != nil {
				t.Fatal(err)
			}
			if err := f.w.AppendStatus("shop", "review-k3", "wait-mate: review done"); err != nil {
				t.Fatal(err)
			}
			report := f.w.CrewReport("shop", "review-k3")
			if err := os.WriteFile(report, []byte("## Summary\nAll criteria checked.\n## Verdict\npass\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if _, _, err := checkReview(context.Background(), f.w, gitx.New(), "shop", "k3", "review-k3"); err != nil {
				t.Fatalf("valid review: %v", err)
			}
			switch change {
			case "commit":
				commitInWorktree(t, f.worktree, "README.md", "changed again\n", "later")
			case "base":
				// The base is the merge base (CON-91), so it changes when
				// the crew rebases onto a default branch that moved on.
				commitInWorktree(t, f.repo, "new.md", "new base\n", "base changes")
				runGitOrFatal(t, f.worktree, "rebase", "-q", "main")
			case "brief", "handback":
				_ = os.WriteFile(filepath.Join(dir, change+".md"), []byte("new contract"), 0644)
			case "dirty":
				_ = os.WriteFile(filepath.Join(f.worktree, "dirty.md"), []byte("unreviewed"), 0644)
			case "verdict":
				_ = os.WriteFile(report, []byte("## Summary\nMissing test.\n## Verdict\nincomplete\n"), 0644)
			}
			if _, _, err := checkReview(context.Background(), f.w, gitx.New(), "shop", "k3", "review-k3"); err == nil {
				t.Fatal("changed review accepted")
			}
		})
	}
}

func TestReviewVerdictDoesNotInferApprovalFromProse(t *testing.T) {
	t.Parallel()
	for _, text := range []string{"tests pass", "## Verdict\npass with reservations", "## Verdict\nchanges-requested"} {
		if store.ReviewVerdict(text) == "pass" {
			t.Fatal(text)
		}
	}
	full := reviewBrief(nilSafeReviewWorkspace(t), "shop", "k3", map[string]string{}, nil, nil)
	if !strings.Contains(full, "FULL committed change") {
		t.Fatal("missing full review")
	}
	// CON-106: a later review passes only when every finding is closed, so
	// the full brief keeps what should not block out of Findings.
	if !strings.Contains(full, "section named Notes") || !strings.Contains(full, "indented sub-bullets") {
		t.Fatalf("full brief does not separate blocking findings from notes:\n%s", full)
	}
	assertMaterialRubric(t, full)
}

// assertMaterialRubric pins CON-109: a brief defines a material finding and
// keeps Notes nonblocking once findings and material verification gaps are
// resolved, so a reviewer does not return a delivery over a doc nit.
func assertMaterialRubric(t *testing.T, brief string) {
	t.Helper()
	for _, want := range []string{
		"material only when an Acceptance line or the captain's words are not met",
		"a gate cannot be trusted (flaky, or not running what it claims)",
		"data loss or a security hole",
		"doc nits, hardening or refactors nobody asked for, hypothetical risks",
		"Findings and Verification gaps both say none. Notes never block a pass.",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("brief lacks %q:\n%s", want, brief)
		}
	}
}
func nilSafeReviewWorkspace(t *testing.T) *store.Workspace {
	w, err := store.Init(t.TempDir(), workspaceDefaults())
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// sinceFixture is a crew reviewed once at its first commit, with the
// reviewer's record and report in place, then a second commit on the
// branch: what `mate review --since` is for.
type sinceFixture struct {
	diffFixture
	first, second string
}

func newSinceFixture(t *testing.T, priorReport string) sinceFixture {
	t.Helper()
	f := newDiffFixture(t)
	commitInWorktree(t, f.worktree, "README.md", "review this\n", "change")
	dir := filepath.Dir(f.w.CrewBrief("shop", "k3"))
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"brief.md", "handback.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("original acceptance evidence"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	first := recordReview(t, f, "review-k3", priorReport)
	commitInWorktree(t, f.worktree, "README.md", "review this, fixed\n", "fix the findings")
	second, err := gitx.New().HeadCommit(context.Background(), f.worktree, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return sinceFixture{diffFixture: f, first: first, second: second}
}

// recordReview writes what `mate review` writes for a reviewer of k3 at the
// branch's current head, marks it handed back and gives it a report. It
// returns the head it recorded.
func recordReview(t *testing.T, f diffFixture, reviewer, report string) string {
	t.Helper()
	fp, err := reviewFingerprint(context.Background(), f.w, gitx.New(), "shop", "k3")
	if err != nil {
		t.Fatal(err)
	}
	path, err := reviewRecordPath(f.w, "shop", reviewer)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WriteMeta(path, fp); err != nil {
		t.Fatal(err)
	}
	if err := f.w.WriteCrewMeta("shop", reviewer, map[string]string{spawn.MetaState: spawn.CrewStateSpawned, spawn.MetaKind: "scout"}); err != nil {
		t.Fatal(err)
	}
	if err := f.w.AppendStatus("shop", reviewer, "wait-mate: review done"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.w.CrewReport("shop", reviewer), []byte(report), 0644); err != nil {
		t.Fatal(err)
	}
	return fp["head"]
}

const priorReportWithFindings = `## Summary
Two problems.
## Findings
- cmd/app/main.go:12 ignores the error from Open
- README.md:1 still says "review this"
## Verdict
changes-requested
`

func TestReviewSinceBriefCoversOnlyTheChangeAndListsTheFindings(t *testing.T) {
	t.Parallel()
	f := newSinceFixture(t, priorReportWithFindings)
	fp, err := reviewFingerprint(context.Background(), f.w, gitx.New(), "shop", "k3")
	if err != nil {
		t.Fatal(err)
	}
	scope, err := reviewSince(context.Background(), f.w, gitx.New(), "shop", "k3", "review-k3", fp)
	if err != nil {
		t.Fatal(err)
	}
	if scope.Reviewed != f.first || fp["head"] != f.second {
		t.Fatalf("scope reviewed %s head %s, want %s and %s", scope.Reviewed, fp["head"], f.first, f.second)
	}
	scope.record(fp)
	for key, want := range map[string]string{"since": "review-k3", "reviewed": f.first, "head": f.second} {
		if fp[key] != want {
			t.Errorf("record %s = %q, want %q", key, fp[key], want)
		}
	}
	text := reviewBrief(f.w, "shop", "k3", fp, scope, nil)
	if problems := brief.Check(text, brief.Scout); len(problems) > 0 {
		t.Fatal(problems)
	}
	for _, want := range []string{
		"git diff " + f.first + "..." + f.second,
		"F1: cmd/app/main.go:12 ignores the error from Open",
		"F2: README.md:1 still says \"review this\"",
		"Rerun a gate",
		"section named Prior findings",
		"F<n> closed or F<n> open",
		"section named Notes",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("brief lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "FULL committed change") || strings.Contains(text, "git diff "+fp["base"]+"...") {
		t.Fatalf("scoped brief still asks for the full diff:\n%s", text)
	}
	assertMaterialRubric(t, text)
}

func TestReviewCheckAcceptsAScopedReviewOnlyWhenEveryFindingIsClosed(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		report string
		want   string // "" for accepted, else a substring of the error
	}{
		"all closed":  {"## Summary\nBoth fixed.\n## Prior findings\n- F1 closed: main.go:12 now returns err\n- F2 closed: README.md:1 reads fixed\n## Findings\nnone\n## Verdict\npass\n", ""},
		"one open":    {"## Summary\nOne left.\n## Prior findings\n- F1 closed: main.go:12 now returns err\n- F2 open: README.md:1 unchanged\n## Findings\nnone\n## Verdict\npass\n", "incomplete: prior finding F2"},
		"one missing": {"## Summary\nForgot one.\n## Prior findings\n- F1 closed: main.go:12 now returns err\n## Findings\nnone\n## Verdict\npass\n", "incomplete: prior finding F2"},
		"no section":  {"## Summary\nAll good.\n## Findings\nnone\n## Verdict\npass\n", "incomplete: prior finding F1"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newSinceFixture(t, priorReportWithFindings)
			fp, err := reviewFingerprint(context.Background(), f.w, gitx.New(), "shop", "k3")
			if err != nil {
				t.Fatal(err)
			}
			scope, err := reviewSince(context.Background(), f.w, gitx.New(), "shop", "k3", "review-k3", fp)
			if err != nil {
				t.Fatal(err)
			}
			scope.record(fp)
			path, err := reviewRecordPath(f.w, "shop", "review2-k3")
			if err != nil {
				t.Fatal(err)
			}
			if err := store.WriteMeta(path, fp); err != nil {
				t.Fatal(err)
			}
			if err := f.w.WriteCrewMeta("shop", "review2-k3", map[string]string{spawn.MetaState: spawn.CrewStateSpawned, spawn.MetaKind: "scout"}); err != nil {
				t.Fatal(err)
			}
			if err := f.w.AppendStatus("shop", "review2-k3", "wait-mate: review done"); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.w.CrewReport("shop", "review2-k3"), []byte(tc.report), 0644); err != nil {
				t.Fatal(err)
			}
			got, _, err := checkReview(context.Background(), f.w, gitx.New(), "shop", "k3", "review2-k3")
			if tc.want == "" {
				if err != nil {
					t.Fatalf("closed findings rejected: %v", err)
				}
				if got["head"] != f.second {
					t.Fatalf("check pins %s, want the new head %s", got["head"], f.second)
				}
				// The earlier report is part of the contract: editing it
				// after the scoped review started makes the review stale.
				if err := os.WriteFile(f.w.CrewReport("shop", "review-k3"), []byte(priorReportWithFindings+"- a third finding\n"), 0644); err != nil {
					t.Fatal(err)
				}
				if _, _, err := checkReview(context.Background(), f.w, gitx.New(), "shop", "k3", "review2-k3"); err == nil || !strings.Contains(err.Error(), "stale") {
					t.Fatalf("edited prior report accepted: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

func TestReviewSinceRefusesWhatItCannotScope(t *testing.T) {
	t.Parallel()
	t.Run("no record", func(t *testing.T) {
		f := newSinceFixture(t, priorReportWithFindings)
		fp, _ := reviewFingerprint(context.Background(), f.w, gitx.New(), "shop", "k3")
		_, err := reviewSince(context.Background(), f.w, gitx.New(), "shop", "k3", "nobody", fp)
		if err == nil || !strings.Contains(err.Error(), "no review record") {
			t.Fatal(err)
		}
	})
	t.Run("other crew", func(t *testing.T) {
		f := newSinceFixture(t, priorReportWithFindings)
		rec, _ := reviewRecordPath(f.w, "shop", "review-k3")
		meta, _ := store.ReadMeta(rec)
		meta["crew"] = "k9"
		if err := store.WriteMeta(rec, meta); err != nil {
			t.Fatal(err)
		}
		fp, _ := reviewFingerprint(context.Background(), f.w, gitx.New(), "shop", "k3")
		_, err := reviewSince(context.Background(), f.w, gitx.New(), "shop", "k3", "review-k3", fp)
		if err == nil || !strings.Contains(err.Error(), "reviewed crew k9, not k3") {
			t.Fatal(err)
		}
	})
	t.Run("no verdict yet", func(t *testing.T) {
		f := newSinceFixture(t, "## Summary\nstill reading\n")
		fp, _ := reviewFingerprint(context.Background(), f.w, gitx.New(), "shop", "k3")
		_, err := reviewSince(context.Background(), f.w, gitx.New(), "shop", "k3", "review-k3", fp)
		if err == nil || !strings.Contains(err.Error(), "no ## Verdict") {
			t.Fatal(err)
		}
	})
	t.Run("nothing new", func(t *testing.T) {
		f := newDiffFixture(t)
		commitInWorktree(t, f.worktree, "README.md", "review this\n", "change")
		dir := filepath.Dir(f.w.CrewBrief("shop", "k3"))
		_ = os.MkdirAll(dir, 0755)
		for _, name := range []string{"brief.md", "handback.md"} {
			_ = os.WriteFile(filepath.Join(dir, name), []byte("original acceptance evidence"), 0644)
		}
		recordReview(t, f, "review-k3", priorReportWithFindings)
		fp, _ := reviewFingerprint(context.Background(), f.w, gitx.New(), "shop", "k3")
		_, err := reviewSince(context.Background(), f.w, gitx.New(), "shop", "k3", "review-k3", fp)
		if err == nil || !strings.Contains(err.Error(), "nothing to review") {
			t.Fatal(err)
		}
	})
	t.Run("rewritten branch", func(t *testing.T) {
		f := newSinceFixture(t, priorReportWithFindings)
		runGitOrFatal(t, f.worktree, "reset", "--hard", "main")
		commitInWorktree(t, f.worktree, "README.md", "rewritten\n", "start over")
		fp, _ := reviewFingerprint(context.Background(), f.w, gitx.New(), "shop", "k3")
		_, err := reviewSince(context.Background(), f.w, gitx.New(), "shop", "k3", "review-k3", fp)
		if err == nil || !strings.Contains(err.Error(), "does not descend") {
			t.Fatal(err)
		}
	})
}

// reviewDiffStat measures the range the reviewer will read with git's
// numstat (CON-74): a two-line README change is small, one that touches a
// Go file is not, and neither is one over the threshold; a --since review
// measures only the change since the commit the prior review saw.
func TestReviewDiffStatSizesTheChange(t *testing.T) {
	t.Parallel()
	f := newDiffFixture(t)
	commitInWorktree(t, f.worktree, "README.md", "shop\nmore\n", "add a line")
	dir := filepath.Dir(f.w.CrewBrief("shop", "k3"))
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"brief.md", "handback.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("contract"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	stat := func(scope *reviewScope) *reviewStat {
		t.Helper()
		fp, err := reviewFingerprint(context.Background(), f.w, gitx.New(), "shop", "k3")
		if err != nil {
			t.Fatal(err)
		}
		st, err := reviewDiffStat(context.Background(), f.w, gitx.New(), "shop", "k3", fp, scope)
		if err != nil {
			t.Fatal(err)
		}
		return st
	}
	if st := stat(nil); st.Lines != 1 || st.Files != 1 || len(st.Code) != 0 || !st.small(50) || st.String() != "1 line(s) in 1 file(s), no code" {
		t.Fatalf("docs change: %+v (%s)", st, st)
	}
	first, err := gitx.New().HeadCommit(context.Background(), f.worktree, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	commitInWorktree(t, f.worktree, "main.go", "package main\n", "add code")
	st := stat(nil)
	if st.Lines != 2 || st.Files != 2 || strings.Join(st.Code, ",") != "main.go" || st.small(50) || st.String() != "2 line(s) in 2 file(s), code in main.go" {
		t.Fatalf("code change: %+v (%s)", st, st)
	}
	// Scoped to the change since the first commit: only main.go.
	if st := stat(&reviewScope{Reviewed: first}); st.Lines != 1 || st.Files != 1 || st.small(50) {
		t.Fatalf("scoped change: %+v", st)
	}
	commitInWorktree(t, f.worktree, "NOTES.md", strings.Repeat("a line\n", 60), "long notes")
	if st := stat(&reviewScope{Reviewed: first}); st.Lines != 61 || len(st.Code) != 1 || st.small(50) {
		t.Fatalf("scoped change with notes: %+v", st)
	}
	if st := (&reviewStat{Lines: 60, Files: 1}); st.small(50) || !st.small(60) {
		t.Fatalf("threshold: %+v", st)
	}
	var none *reviewStat
	if none.small(50) {
		t.Fatal("an unmeasured change counted as small")
	}
}

// TestReviewBaseIsTheMergeBaseWhenTheCrewRebasedOntoOrigin is CON-91 as it
// happened in project mihu: two pull requests landed on origin/main, the
// crew rebased its one-file change onto origin/main, and the local main was
// never pulled. The base the review records is the commit the branch grew
// from, the diff the brief asks for is one file, and the brief says so.
func TestReviewBaseIsTheMergeBaseWhenTheCrewRebasedOntoOrigin(t *testing.T) {
	t.Parallel()
	f := newDiffFixture(t)
	ctx := context.Background()
	localMain, err := gitx.New().HeadCommit(ctx, f.repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	// What origin/main gained from two merged pull requests, built on a
	// throwaway branch and published only as the remote-tracking ref.
	runGitOrFatal(t, f.repo, "checkout", "-q", "-b", "upstream", "main")
	commitInWorktree(t, f.repo, "pr1.md", "one\n", "merged PR 1")
	commitInWorktree(t, f.repo, "pr2.md", "two\n", "merged PR 2")
	runGitOrFatal(t, f.repo, "update-ref", "refs/remotes/origin/main", "upstream")
	runGitOrFatal(t, f.repo, "checkout", "-q", "main")
	runGitOrFatal(t, f.repo, "branch", "-q", "-D", "upstream")
	upstream, err := gitx.New().HeadCommit(ctx, f.repo, "refs/remotes/origin/main")
	if err != nil {
		t.Fatal(err)
	}
	runGitOrFatal(t, f.worktree, "reset", "-q", "--hard", "refs/remotes/origin/main")
	commitInWorktree(t, f.worktree, "README.md", "shop\nthe crew's line\n", "the crew's change")
	dir := filepath.Dir(f.w.CrewBrief("shop", "k3"))
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"brief.md", "handback.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("original acceptance evidence"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	fp, err := reviewFingerprint(ctx, f.w, gitx.New(), "shop", "k3")
	if err != nil {
		t.Fatal(err)
	}
	if fp["base"] != upstream {
		t.Fatalf("base = %s, want the merge base %s (local main is %s)", fp["base"], upstream, localMain)
	}
	stat, err := reviewDiffStat(ctx, f.w, gitx.New(), "shop", "k3", fp, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stat.Files != 1 || stat.Lines != 1 {
		t.Fatalf("diff stat = %d file(s), %d line(s); want 1 and 1", stat.Files, stat.Lines)
	}
	text := reviewBrief(f.w, "shop", "k3", fp, nil, stat)
	if problems := brief.Check(text, brief.Scout); len(problems) > 0 {
		t.Fatal(problems)
	}
	for _, want := range []string{
		"Base " + upstream + " is the merge base",
		"git diff " + upstream + "..." + fp["head"] + " is 1 file(s), 1 changed line(s)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("brief lacks %q:\n%s", want, text)
		}
	}
}

// TestReviewCheckRefusesABaseTheHeadDoesNotDescendFrom: a record whose base
// is not an ancestor of its head (one written from a default branch the
// crew never grew from) is refused by --check, whatever the verdict says.
func TestReviewCheckRefusesABaseTheHeadDoesNotDescendFrom(t *testing.T) {
	t.Parallel()
	f := newDiffFixture(t)
	commitInWorktree(t, f.worktree, "README.md", "review this\n", "change")
	dir := filepath.Dir(f.w.CrewBrief("shop", "k3"))
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"brief.md", "handback.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("original acceptance evidence"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	recordReview(t, f, "review-k3", "## Summary\nAll criteria checked.\n## Verdict\npass\n")
	commitInWorktree(t, f.repo, "other.md", "not the crew's\n", "unrelated work on main")
	other, err := gitx.New().HeadCommit(context.Background(), f.repo, "main")
	if err != nil {
		t.Fatal(err)
	}
	path, err := reviewRecordPath(f.w, "shop", "review-k3")
	if err != nil {
		t.Fatal(err)
	}
	rec, err := store.ReadMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	// main moving on alone does not stale the review: the diff is the same.
	if _, _, err := checkReview(context.Background(), f.w, gitx.New(), "shop", "k3", "review-k3"); err != nil {
		t.Fatalf("review rejected after an unrelated commit on main: %v", err)
	}
	rec["base"] = other
	if err := store.WriteMeta(path, rec); err != nil {
		t.Fatal(err)
	}
	_, _, err = checkReview(context.Background(), f.w, gitx.New(), "shop", "k3", "review-k3")
	if err == nil || !strings.Contains(err.Error(), "is not an ancestor of head") {
		t.Fatalf("check with a base off the branch's history: %v", err)
	}
}
