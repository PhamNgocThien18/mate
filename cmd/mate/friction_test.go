package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/learnings"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// overBudgetFriction is a log in mihuv2's shape (2026-11-04): one entry
// above `## Archive`, six open ones appended below it, so only a count
// that ignores position sees it over the default budget of 5.
func overBudgetFriction() string {
	var b strings.Builder
	b.WriteString("# Harness friction log\n\n```markdown\n## YYYY-MM-DD — short title\nOutcome: open | YYYY-MM-DD — what was observed.\n```\n\n")
	b.WriteString("## 2026-11-03 — above the archive\nFriction: x.\nOutcome: 2026-11-04 — closed.\n\n## Archive\n- 2026-10-17 — archived → verify.sh.\n\n---\n\n")
	for range 6 {
		b.WriteString("## 2026-10-29 — below the archive\nFriction: y.\nOutcome: open\n\n")
	}
	return b.String()
}

func writeFriction(t *testing.T, w *store.Workspace, project, text string) string {
	t.Helper()
	cfg, err := w.LoadProject(project)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(w.RepoDir(cfg.Repos[0].Path), learnings.FrictionPath)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return file
}

func harnessReviewNews(t *testing.T, w *store.Workspace) []matehost.News {
	t.Helper()
	list, err := matehost.LoadNews(matehost.NewsPath(w.MateDir("shop")))
	if err != nil {
		t.Fatal(err)
	}
	var out []matehost.News
	for _, n := range list {
		if n.Kind == frictionNewsKind {
			out = append(out, n)
		}
	}
	return out
}

func wakeAt(t *testing.T, w *store.Workspace, now time.Time, advance bool) string {
	t.Helper()
	text, err := wake(context.Background(), w, gitx.New(), "shop", now, advance)
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	return text
}

// CON-200: a repo over its friction budget gets exactly one news asking
// the captain about /harness-review, however many wakes see it; a peek
// opens none, and a closed one is not asked again for frictionAskAgain.
func TestWakeAsksOnceForAHarnessReviewOverBudget(t *testing.T) {
	w := wakeFixture(t)
	writeFriction(t, w, "shop", overBudgetFriction())
	now := time.Date(2026, 11, 4, 20, 0, 0, 0, time.UTC)

	peek := wakeAt(t, w, now, false)
	if !strings.Contains(peek, "== 4. Harness friction over budget ==\nfriction shop: 6 open / ") || !strings.Contains(peek, "peek: no news opened") {
		t.Fatalf("peek lacks the friction count:\n%s", peek)
	}
	if got := harnessReviewNews(t, w); len(got) != 0 {
		t.Fatalf("a peek opened %d news", len(got))
	}

	first := wakeAt(t, w, now, true)
	if !strings.Contains(first, "budget 5 / 120") || !strings.Contains(first, "news #1 opened: asks the captain") {
		t.Fatalf("first wake lacks the opened news:\n%s", first)
	}
	second := wakeAt(t, w, now.Add(time.Hour), true)
	if !strings.Contains(second, "news #1 already asks the captain (new)") {
		t.Fatalf("second wake lacks the open news:\n%s", second)
	}
	got := harnessReviewNews(t, w)
	if len(got) != 1 {
		t.Fatalf("two wakes opened %d news, want 1", len(got))
	}
	if n := got[0]; n.Context[0] != "repo: shop" || len(n.Options) != 2 || !strings.Contains(n.Question, "/harness-review") {
		t.Fatalf("news = %+v", n)
	}

	path := matehost.NewsPath(w.MateDir("shop"))
	if err := matehost.CloseNews(path, 1, "captain: not now", false, now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if text := wakeAt(t, w, now.Add(3*time.Hour), true); !strings.Contains(text, "news #1 closed 1h ago; not asked again") {
		t.Fatalf("wake after the captain declined:\n%s", text)
	}
	if text := wakeAt(t, w, now.Add(2*time.Hour+frictionAskAgain), true); !strings.Contains(text, "news #2 opened") {
		t.Fatalf("wake after frictionAskAgain asked nothing:\n%s", text)
	}
}

func TestWakeSaysNothingOfFrictionWithinBudgetOrWithoutALog(t *testing.T) {
	w := wakeFixture(t)
	now := time.Date(2026, 11, 4, 20, 0, 0, 0, time.UTC)
	if text := wakeAt(t, w, now, true); strings.Contains(text, "friction") {
		t.Fatalf("a repo without a friction log got a friction part:\n%s", text)
	}
	writeFriction(t, w, "shop", "# Harness friction log\n\n## 2026-11-03 — one\nFriction: x.\nOutcome: open\n")
	if text := wakeAt(t, w, now, true); strings.Contains(text, "friction") {
		t.Fatalf("a repo within budget got a friction part:\n%s", text)
	}
	if got := harnessReviewNews(t, w); len(got) != 0 {
		t.Fatalf("opened %d news within budget", len(got))
	}
}

// The repo's review.md may raise the budget at onboarding; the check
// reads it rather than the kit's default.
func TestFrictionCheckReadsTheReposBudget(t *testing.T) {
	w := wakeFixture(t)
	file := writeFriction(t, w, "shop", overBudgetFriction())
	review := filepath.Join(filepath.Dir(file), "workflows", "review.md")
	if err := os.MkdirAll(filepath.Dir(review), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(review, []byte("| What | Budget | Why |\n| --- | --- | --- |\n| Open friction entries | ≤ 10 | x |\n| Live friction log | ≤ ~200 lines | y |\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if lines := frictionCheck(w, "shop", time.Now(), true); len(lines) != 0 {
		t.Fatalf("frictionCheck within the repo's budget = %q", lines)
	}
}
