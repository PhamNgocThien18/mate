package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/nguyenngocanh94/mate/internal/dashboard"
	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// cmdDashboard is `mate dashboard [<workspace>] [--addr 127.0.0.1:7777]
// [--open] [--allow-remote]` (docs/mvp.md M6 task 28): the workspace's
// timeline in a browser, read-only.
//
// It opens the database with db.OpenRead, which takes no lock, so it runs
// beside an open console rather than instead of it. It prints the URL and
// serves until Ctrl+C.
func cmdDashboard(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("dashboard", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: mate dashboard [<workspace>] [--addr host:port] [--open] [--allow-remote]")
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	addr := fs.String("addr", dashboard.DefaultAddr, "address to bind")
	open := fs.Bool("open", false, "open the dashboard in the platform browser")
	allowRemote := fs.Bool("allow-remote", false, "permit a non-loopback bind (the timeline quotes everything the captain typed)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	dir := *workspaceFlag
	switch fs.NArg() {
	case 0:
	case 1:
		if dir != "" {
			fs.Usage()
			return newUsageError("mate dashboard: give the workspace once, as an argument or as --workspace")
		}
		dir = fs.Arg(0)
	default:
		fs.Usage()
		return newUsageError("mate dashboard: want at most 1 argument: [<workspace>]")
	}

	w, err := resolveWorkspace(dir)
	if err != nil {
		return err
	}
	handle, err := db.OpenRead(w)
	if err != nil {
		return err
	}
	defer handle.Close()

	server, err := dashboard.New(dashboard.Options{
		Workspace: w, DB: handle, Addr: *addr, AllowRemote: *allowRemote,
		Deps: dashboardDeps(w),
	})
	if err != nil {
		return err
	}
	ln, err := dashboard.Listen(dashboard.Options{Addr: *addr, AllowRemote: *allowRemote})
	if err != nil {
		return err
	}
	defer ln.Close()

	url := dashboard.URL(ln)
	fmt.Fprintf(stdout, "mate dashboard on %s (read-only; Ctrl+C to stop)\n", url)
	if *open {
		if err := openInBrowser(url); err != nil {
			// Not fatal: the URL is already on the terminal, and a machine
			// with no opener is not a reason to refuse to serve.
			fmt.Fprintf(stderr, "could not open a browser (%v); the address is above\n", err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return server.Serve(ctx, ln)
}

// dashboardDeps wires the answers the database does not hold. Diff is
// the CLI's own crewDiffText, so the page shows byte for byte what
// `mate diff` prints; BranchExists is `internal/gitx` over the primary
// checkout of the crew's own repo, which is where a branch outliving its
// worktree lives (see crewDiffText's own note). PluginHarness is the
// harness profile's MCPConfig capability (pluginHarness), so the dashboard
// names no harness.
func dashboardDeps(w *store.Workspace) dashboard.Deps {
	git := gitx.New()
	return dashboard.Deps{
		PluginHarness: pluginHarness,
		Diff: func(ctx context.Context, project, crew string) (string, error) {
			return crewDiffText(ctx, w, git, project, crew, false)
		},
		BranchExists: func(ctx context.Context, project, crew, branch string) (bool, error) {
			// The crew's meta names its repo (docs/mvp.md M9); asking every
			// repo of the project could find a same-named branch another
			// crew of that id left in a different repo.
			cfg, err := w.LoadProject(project)
			if err != nil {
				return false, err
			}
			meta, err := w.ReadCrewMeta(project, crew)
			if err != nil {
				return false, err
			}
			repoCfg, err := cfg.CrewRepo(meta)
			if err != nil {
				return false, err
			}
			return git.BranchExists(ctx, w.RepoDir(repoCfg.Path), branch)
		},
		ChangedFiles: func(ctx context.Context, project, crew string, branchExists bool, mergeCommit string) ([]string, error) {
			return crewChangedFiles(ctx, w, git, project, crew, branchExists, mergeCommit)
		},
	}
}

// openInBrowser is `--open`. One command per platform, the same three every
// tool uses; a machine where none of them exists says so rather than
// pretending it opened something.
func openInBrowser(url string) error {
	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name = "open"
	case "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler"}
	default:
		name = "xdg-open"
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return fmt.Errorf("no %s on PATH", name)
	}
	return exec.Command(path, append(args, url)...).Start()
}

// crewChangedFiles is the task page's "which files did this crew touch":
// the branch against its base while the branch exists, otherwise the merge
// commit the crew's meta recorded (`git show --name-only`), otherwise an
// error saying there is nothing left to read.
func crewChangedFiles(ctx context.Context, w *store.Workspace, git gitx.Git, project, crew string, branchExists bool, mergeCommit string) ([]string, error) {
	cfg, err := w.LoadProject(project)
	if err != nil {
		return nil, err
	}
	meta, err := w.ReadCrewMeta(project, crew)
	if err != nil {
		return nil, err
	}
	repoCfg, err := cfg.CrewRepo(meta)
	if err != nil {
		return nil, err
	}
	dir := w.RepoDir(repoCfg.Path)
	if branchExists && meta["branch"] != "" {
		base, err := git.BranchBase(ctx, dir, repoCfg.DefaultBranch, meta["branch"])
		if err != nil {
			return nil, err
		}
		entries, err := git.DiffNumstat(ctx, dir, base, meta["branch"])
		if err != nil {
			return nil, err
		}
		files := make([]string, 0, len(entries))
		for _, e := range entries {
			files = append(files, e.Path)
		}
		return files, nil
	}
	if mergeCommit == "" {
		return nil, fmt.Errorf("the branch is gone and the crew's meta records no merge commit")
	}
	// A merge commit's own `--name-only` is empty; its files are the diff
	// from its first parent (main before) to its second (the branch tip).
	// A fast-forward has one parent and keeps only its last commit's
	// files: the branch's base is not recorded anywhere once it is gone.
	parents, err := git.Log(ctx, dir, mergeCommit, "-1", "--format=%P")
	if err != nil {
		return nil, err
	}
	if fields := strings.Fields(parents); len(fields) >= 2 {
		entries, err := git.DiffNumstat(ctx, dir, fields[0], fields[1])
		if err != nil {
			return nil, err
		}
		files := make([]string, 0, len(entries))
		for _, e := range entries {
			files = append(files, e.Path)
		}
		return files, nil
	}
	out, err := git.Log(ctx, dir, mergeCommit, "-1", "--name-only", "--format=")
	if err != nil {
		return nil, err
	}
	files := []string{}
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}
