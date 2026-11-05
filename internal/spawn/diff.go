package spawn

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// CrewDiff is `mate diff <project> <crew>`'s answer (docs/mvp.md task 21):
// the review surface for one crew's branch, read-only. The command, the
// Console's `diff` action and the dashboard's diff all print exactly this
// text, so the overlay, the page and the terminal can never disagree about
// what a crew's branch contains.
//
// The shape of the answer, in the order it is printed:
//
//	<n> uncommitted file(s) not shown      only when the worktree is dirty
//	<sha> <subject>                        `git log --oneline <base>..<branch>`
//	                                       (blank line)
//	<the patch, or the --stat summary>     `git diff <base>...<branch>`
//
// The dirty line leads rather than trails because it changes how everything
// under it must be read: the patch is the committed work and nothing else,
// so a reader who is not told about the uncommitted files would take the
// diff for the whole of what the crew has done.
func CrewDiff(ctx context.Context, w *store.Workspace, git gitx.Git, project, crew string, stat bool) (string, error) {
	if w == nil {
		return "", observability.NewError(observability.CodeUsage, "mate diff: a workspace is required")
	}
	if err := store.ValidateProjectName(project); err != nil {
		return "", err
	}
	if err := store.ValidateCrewID(crew); err != nil {
		return "", err
	}
	meta, err := w.ReadCrewMeta(project, crew)
	if err != nil {
		return "", err
	}
	if len(meta) == 0 {
		return "", observability.NewError(observability.CodeNotFound,
			fmt.Sprintf("no crew %s is recorded for project %s; %s is empty", crew, project, w.CrewMeta(project, crew)))
	}
	cfg, err := w.LoadProject(project)
	if err != nil {
		return "", err
	}
	repoCfg, err := cfg.CrewRepo(meta)
	if err != nil {
		return "", observability.WrapError(observability.CodeStateConflict,
			fmt.Sprintf("crew %s/%s: nothing to diff (meta %s)", project, crew, w.CrewMeta(project, crew)), err)
	}
	repo := w.RepoDir(repoCfg.Path)
	base := repoCfg.DefaultBranch

	branch := meta[MetaBranch]
	if branch == "" {
		return "", observability.NewError(observability.CodeStateConflict,
			fmt.Sprintf("crew %s records no branch in %s; there is nothing to diff", crew, w.CrewMeta(project, crew)))
	}

	// A crew that has been stopped has no worktree left, but `crew stop`
	// keeps the branch whenever it carried work the default branch did not
	// have (crew_stop.go). The branch outliving the worktree is therefore
	// the normal shape of a crew waiting to be reviewed, and the primary
	// checkout can answer for it: a linked worktree never had an object
	// database of its own.
	dir := repo
	worktree := ""
	if rel := meta[MetaWorktree]; rel != "" {
		worktree = filepath.Join(w.Root(), filepath.FromSlash(rel))
		if fi, statErr := os.Stat(worktree); statErr == nil && fi.IsDir() {
			dir = worktree
		} else {
			worktree = ""
		}
	}

	branchExists, err := git.BranchExists(ctx, repo, branch)
	if err != nil {
		return "", err
	}
	if !branchExists {
		// Said plainly rather than raised as an error: a crew whose branch
		// was deleted is a crew whose work landed or was discarded, and
		// "there is nothing left to show" is the honest answer to the
		// question, not a failure of the command.
		return fmt.Sprintf("branch %s no longer exists in %s; crew %s was torn down and its branch is gone\n",
			branch, repo, crew), nil
	}
	baseExists, err := git.RevisionExists(ctx, repo, base)
	if err != nil {
		return "", err
	}
	if !baseExists {
		return "", observability.NewError(observability.CodeStateConflict,
			fmt.Sprintf("default branch %s does not exist in %s; %s cannot be compared against it",
				base, repo, branch))
	}

	var out strings.Builder
	if worktree != "" {
		dirty, err := git.IsDirty(ctx, worktree)
		if err != nil {
			return "", err
		}
		if dirty > 0 {
			fmt.Fprintf(&out, "%d uncommitted file(s) not shown\n", dirty)
		}
	}

	log, err := git.LogOneline(ctx, dir, base, branch)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(log) == "" {
		// No commits means no diff: `base...branch` with an empty range is
		// empty by construction, so git is not asked a second question whose
		// answer is already known.
		fmt.Fprintf(&out, "no commits on %s beyond %s\n", branch, base)
		return out.String(), nil
	}
	out.WriteString(ensureTrailingNewline(log))
	out.WriteString("\n")

	diff, err := git.Diff(ctx, dir, base, branch, stat)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(diff) == "" {
		// Commits with no net change: a revert pair, or a branch that only
		// touched a file back to what base already had. Saying so is not the
		// same as saying there are no commits, and the two must not read
		// alike.
		fmt.Fprintf(&out, "the commits above leave no net change against %s\n", base)
		return out.String(), nil
	}
	out.WriteString(ensureTrailingNewline(diff))
	return out.String(), nil
}

// ensureTrailingNewline lets git's own output end the way git ended it while
// guaranteeing the next section starts on its own line. `git diff` on a file
// with no final newline is the case that makes this necessary.
func ensureTrailingNewline(s string) string {
	if s == "" || strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}
