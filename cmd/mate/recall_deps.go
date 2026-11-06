package main

import (
	"context"
	"io"
	"time"

	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/memory"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/recall"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// recallDeps are what `mate recall` and the SessionStart hook hand
// internal/recall: a real git and tracker runner, and the commands whose
// output the digest quotes - `mate backlog`'s crews table, `mate project
// facts`, `mate memory check` and its anchor warnings, the next-work line
// and a `mate work list` row - so the digest prints them in those
// commands' own words.
func recallDeps() recall.Deps {
	return recall.Deps{
		Git:     gitx.New(),
		Tracker: process.ExecRunner{},
		Crews: func(w *store.Workspace, project string, now time.Time, out io.Writer) error {
			return printBacklog(w, project, false, now, out)
		},
		NextWork: nextWorkLine,
		WorkLine: workListLine,
		Facts: func(ctx context.Context, w *store.Workspace, git gitx.Git, project string, cfg store.ProjectConfig) []string {
			lines, _ := projectFactsLines(ctx, w, git, project, cfg, true)
			return lines
		},
		AnchorWarnings: func(ctx context.Context, git gitx.Git, repo string, r store.RepoConfig, anchors []memory.Anchor) ([]string, error) {
			return anchorWarnings(ctx, git, repo, r, anchors, staleAnchorCommits)
		},
		MemoryCheck: func(ctx context.Context, w *store.Workspace, git gitx.Git, project string, now time.Time) (recall.MemoryReport, error) {
			rep, err := memoryCheck(ctx, w, git, project, now, staleAnchorCommits)
			return recall.MemoryReport{
				Measured:   rep.Budget.Budget != 0,
				BudgetLine: rep.Budget.Line(),
				Problems:   rep.Problems,
				Captain:    rep.Captain,
				Lessons:    rep.Lessons,
				Anchored:   rep.Anchored,
			}, err
		},
	}
}
