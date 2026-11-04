package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/nguyenngocanh94/mate/internal/learnings"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// frictionNewsKind is the kind of the news that asks the captain whether
// a repo's friction log gets a `/harness-review` (CON-200).
const frictionNewsKind = "harness-review"

// frictionAskAgain is how long a closed harness-review question keeps the
// same repo from being asked again: a captain who said "not now" is not
// asked at the next wake.
const frictionAskAgain = 3 * 24 * time.Hour

// frictionCheck is the detection half of the harness review (CON-200).
// The kit's learn loop writes friction into each repo's
// docs/harness/friction.md and leaves turning it into rules to
// `/harness-review`, but nothing ran the review: on mihuv2 (2026-11-04)
// the log held 29 open entries in 250 lines against a budget of 5 / 120,
// crossed since about 10-29, and the review had run once, by hand. So the
// wake and every landing count each repo's log against the budgets of its
// review.md, and a repo over budget gets one news asking the captain
// whether to spawn a crew for the review. Counting is all the app does;
// the captain decides, and the review crew's pull request proposes every
// close, archive and promotion.
//
// ask false (a wake --peek) counts and opens nothing. A repo without a
// friction log, or within budget, prints nothing.
func frictionCheck(w *store.Workspace, project string, now time.Time, ask bool) []string {
	cfg, err := w.LoadProject(project)
	if err != nil {
		return nil
	}
	var out []string
	for _, r := range cfg.Repos {
		dir := w.RepoDir(r.Path)
		file := filepath.Join(dir, learnings.FrictionPath)
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		budget := learnings.DefaultFrictionBudget
		if review, err := os.ReadFile(filepath.Join(dir, learnings.ReviewPath)); err == nil {
			budget = learnings.ParseFrictionBudget(string(review))
		}
		count := learnings.CountFriction(string(data))
		if !count.Over(budget) {
			continue
		}
		name := r.Name
		if name == "" {
			name = r.Path
		}
		out = append(out, fmt.Sprintf("friction %s: %d open / %d lines, budget %d / %d (%s)", name, count.Open, count.Lines, budget.Open, budget.Lines, file))
		out = append(out, "  "+frictionAsk(w, project, name, file, count, budget, now, ask))
	}
	return out
}

// frictionAsk opens the repo's harness-review news, unless one is still
// open or closed less than frictionAskAgain ago, and says which.
func frictionAsk(w *store.Workspace, project, repo, file string, count learnings.Friction, budget learnings.FrictionBudget, now time.Time, ask bool) string {
	if !ask {
		return "peek: no news opened"
	}
	repoLine := "repo: " + repo
	n := matehost.News{
		Kind:  frictionNewsKind,
		Title: fmt.Sprintf("friction %s over budget (%d open / %d lines)", repo, count.Open, count.Lines),
		Context: []string{
			repoLine,
			fmt.Sprintf("open entries: %d of %d, budget %d", count.Open, count.Entries, budget.Open),
			fmt.Sprintf("lines: %d, budget %d", count.Lines, budget.Lines),
			"file: " + file,
		},
		Question: fmt.Sprintf("Spawn a crew to run /harness-review on %s? It opens one pull request proposing what to close, archive and promote to a rule; nothing changes until you approve it.", repo),
		Options: []matehost.NewsOption{
			{Key: "a", Text: "yes, spawn the review crew", Recommended: true},
			{Key: "b", Text: "not now"},
		},
	}
	same := func(x matehost.News) bool {
		if x.Kind != frictionNewsKind || len(x.Context) == 0 || x.Context[0] != repoLine {
			return false
		}
		return !x.Closed() || now.Sub(x.At) < frictionAskAgain
	}
	got, opened, err := matehost.OpenNewsUnless(matehost.NewsPath(w.MateDir(project)), n, same, now)
	switch {
	case err != nil:
		return "news not opened: " + oneLineMsg(err)
	case opened:
		return fmt.Sprintf("news #%d opened: asks the captain whether to spawn a /harness-review crew; next: nothing until they answer, then skill task-learning, Harness review", got.ID)
	case got.Closed():
		return fmt.Sprintf("news #%d closed %s ago; not asked again before %s", got.ID, formatAge(now.Sub(got.At)), formatAge(frictionAskAgain))
	default:
		return fmt.Sprintf("news #%d already asks the captain (%s); next: nothing until they answer", got.ID, got.Status)
	}
}
