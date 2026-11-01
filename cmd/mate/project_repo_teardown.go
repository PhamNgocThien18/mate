package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
)

// cmdProjectRepoTeardown implements `mate project repo teardown <project>
// <repo> ["<command>"] [--clear]` (CON-113): the command a crew stop runs in
// the crew's worktree before removing it, for what the repo starts per
// worktree and knows how to stop. Without a command it prints the setting.
func cmdProjectRepoTeardown(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("project repo teardown", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, `usage: mate project repo teardown <project> <repo> ["<command>"] [--clear] [--workspace <dir>]`)
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	clearFlag := fs.Bool("clear", false, "remove the repo's teardown command")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() < 2 || fs.NArg() > 3 || (*clearFlag && fs.NArg() == 3) {
		fs.Usage()
		return newUsageError("mate project repo teardown: want <project> <repo>, then a command or --clear")
	}
	project, repo := fs.Arg(0), fs.Arg(1)
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	if err := requireProject(w, project); err != nil {
		return err
	}
	cfg, err := w.LoadProject(project)
	if err != nil {
		return err
	}
	i := -1
	for n, r := range cfg.Repos {
		if r.Name == repo {
			i = n
		}
	}
	if i < 0 {
		return newUsageErrorf("mate project repo teardown: project %s has no repo %q (it has: %s)", project, repo, strings.Join(cfg.RepoNames(), ", "))
	}
	if *clearFlag || fs.NArg() == 3 {
		cmd := ""
		if !*clearFlag {
			cmd = strings.TrimSpace(fs.Arg(2))
			if cmd == "" || strings.ContainsAny(cmd, "\n\r") {
				return newUsageError("mate project repo teardown: the command is one non-empty line")
			}
		}
		cfg.Repos[i].Teardown = cmd
		if err := w.SaveProject(project, cfg); err != nil {
			return err
		}
	}
	fmt.Fprintln(stdout, teardownSettingLine(project, repo, cfg.Repos[i].Teardown))
	return nil
}

// teardownSettingLine says what a crew stop will run for the repo.
func teardownSettingLine(project, repo, cmd string) string {
	if cmd == "" {
		return fmt.Sprintf("%s/%s teardown: none - a crew stop only ends the processes still running in the worktree; a stack, simulator or build folder the repo starts per worktree stays behind", project, repo)
	}
	return fmt.Sprintf("%s/%s teardown: %s - runs with sh -c in each crew's worktree before a stop removes it, then ends the processes still running there", project, repo, cmd)
}
