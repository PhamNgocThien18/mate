package main

import (
	"flag"
	"fmt"
	"io"
	"strings"
)

// cmdProjectRepoLane implements `mate project repo lane <project> <repo>
// [<lane>] [--clear]` (CON-118): the machine resource the repo's crews
// contend for, named by the captain. Without a lane it prints the setting.
func cmdProjectRepoLane(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("project repo lane", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, `usage: mate project repo lane <project> <repo> [<lane>] [--clear] [--workspace <dir>]`)
	}
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	clearFlag := fs.Bool("clear", false, "take the repo out of its lane")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() < 2 || fs.NArg() > 3 || (*clearFlag && fs.NArg() == 3) {
		fs.Usage()
		return newUsageError("mate project repo lane: want <project> <repo>, then a lane or --clear")
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
		return newUsageErrorf("mate project repo lane: project %s has no repo %q (it has: %s)", project, repo, strings.Join(cfg.RepoNames(), ", "))
	}
	if *clearFlag || fs.NArg() == 3 {
		lane := ""
		if !*clearFlag {
			lane = strings.TrimSpace(fs.Arg(2))
			if lane == "" || strings.ContainsAny(lane, " \t\n\r:") {
				return newUsageError("mate project repo lane: a lane is one word, such as xcode")
			}
		}
		cfg.Repos[i].Lane = lane
		if err := w.SaveProject(project, cfg); err != nil {
			return err
		}
	}
	fmt.Fprintln(stdout, laneSettingLine(project, repo, cfg.Repos[i].Lane, w.LaneCap(cfg.Repos[i].Lane)))
	return nil
}

// laneSettingLine says which lane the repo's crews hold.
func laneSettingLine(project, repo, lane string, capacity int) string {
	if lane == "" {
		return fmt.Sprintf("%s/%s lane: none - its crews hold no lane", project, repo)
	}
	return fmt.Sprintf("%s/%s lane: %s - its crews hold the %s lane while spawned or working, %d at a time across the workspace (workspace.yaml lanes: resizes it)", project, repo, lane, lane, capacity)
}
