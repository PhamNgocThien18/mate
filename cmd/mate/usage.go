package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/usage"
)

// cmdUsage is `mate usage <project> [crew] [--top <n>] [--why] [--workspace
// <dir>]` (mvp.md M5 task 27): the token/cost ledger `v_task_ledger`
// computes, read-only.
//
// With no crew it prints one row per task plus the Mate, oldest spawn
// first, and a totals footer; `--top <n>` keeps the Mate and the n largest
// tasks by TOTAL instead. With a crew it prints that crew's own turns - one
// row per model call, the unit `mate events --narrate` calls "ends a turn" -
// rather than the whole-task summary; `--why` prints what those calls were
// spent on instead.
//
// internal/usage builds every page (CON-168); this reads the flags, refuses
// a wrong combination before it opens the database, and hands the package
// the harness registry and the dashboard's deps `--why` reads through.
func cmdUsage(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("usage", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate usage <project> [--top <n>] | mate usage <project> <crew> [--why | --sessions] [--workspace <dir>]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	top := fs.Int("top", 0, "print only the Mate and the n largest tasks by TOTAL")
	why := fs.Bool("why", false, "print what a crew's tokens were spent on instead of its model calls")
	sessions := fs.Bool("sessions", false, "print a crew's tokens per session (one row per harness session, CON-73) instead of its model calls")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() < 1 || fs.NArg() > 2 {
		fs.Usage()
		return newUsageError("mate usage: want 1 or 2 arguments: <project> [crew]")
	}
	project := fs.Arg(0)
	if *top < 0 {
		return newUsageError("mate usage: --top wants a positive number of tasks")
	}
	if *top > 0 && fs.NArg() == 2 {
		return newUsageError("mate usage: --top ranks the project's tasks; drop the crew")
	}
	if *why && fs.NArg() == 1 {
		return newUsageError("mate usage: --why explains one task; name the crew")
	}
	if *sessions && fs.NArg() == 1 {
		return newUsageError("mate usage: --sessions splits one task; name the crew")
	}
	if *sessions && *why {
		return newUsageError("mate usage: --sessions and --why are two different pages; pick one")
	}

	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	if _, ok := w.Project(project); !ok {
		return newUsageErrorf("no project %q in this workspace", project)
	}

	handle, err := db.OpenRead(w)
	if err != nil {
		return err
	}
	defer handle.Close()

	deps := usage.Deps{Harnesses: harnesses, Dashboard: dashboardDeps(w)}
	return usage.Write(context.Background(), stdout, w, handle, deps, usage.Request{
		Project:  project,
		Crew:     fs.Arg(1),
		Top:      *top,
		Why:      *why,
		Sessions: *sessions,
	})
}
