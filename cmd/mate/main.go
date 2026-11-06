// Command mate is the single binary for the mate workspace orchestrator.
//
// `mate` and `mate <workspace-dir>` run the workspace's core in the
// foreground and print its Office and City URLs; `mate console` opens the
// console. Most other subcommands are the agent-facing CLI that Mates and
// Crews call from inside their Herdr panes.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/nguyenngocanh94/mate/internal/config"
	"github.com/nguyenngocanh94/mate/internal/github"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/prwatch"
	"github.com/nguyenngocanh94/mate/internal/quota"
	"github.com/nguyenngocanh94/mate/internal/spawn"
)

func main() {
	os.Exit(newCLI().mainRun(os.Args[1:], os.Stdout, os.Stderr))
}

// cli is what the command handlers reach outside their arguments: the
// process environment and the programs a test stands in for. Like
// spawn.Deps, newCLI fills in the real ones and a test passes its own in
// the struct, so no test swaps a package variable and the package's tests
// can run in parallel (CON-172).
type cli struct {
	// getenv reads the process environment; who is at the keyboard
	// (MATE_CALLER) is read through it.
	getenv func(string) string
	// getwd is the current directory a hook finds its workspace and
	// project from.
	getwd func() (string, error)
	// readQuota is the one quota-axi read a dispatch or a spawn makes.
	readQuota func(context.Context) (quota.Snapshot, error)
	// gh is the `gh` that `mate pr watch` reads pull requests with.
	gh github.Client
	// prStarter is how `mate pr watch` starts the detached watcher.
	prStarter prwatch.Starter
	// tracker runs a tracker's land command for `mate land`.
	tracker process.Runner
}

// newCLI is the CLI over this process's environment and the real programs.
func newCLI() cli {
	return cli{
		getenv: os.Getenv,
		getwd:  os.Getwd,
		readQuota: func(ctx context.Context) (quota.Snapshot, error) {
			return quota.Read(ctx, process.ExecRunner{}, harnesses, time.Now())
		},
		gh:        github.New(),
		prStarter: prwatch.ExecStarter{},
		tracker:   process.ExecRunner{},
	}
}

// caller is who typed the command, read from the pane environment rather
// than from a flag: spawn injects MATE_CALLER=mate into a Mate's pane and
// MATE_CALLER=crew into a Crew's, and anything else is the captain
// (spawn.NormaliseCaller).
func (c cli) caller() string {
	return spawn.NormaliseCaller(c.getenv(config.EnvCaller))
}

// mainRun runs the CLI and returns the process exit code: 0 on success, 2 for
// a usage error, 1 for any other error.
func (c cli) mainRun(args []string, stdout, stderr io.Writer) int {
	err := c.run(args, stdout, stderr)
	if err == nil {
		return 0
	}
	fmt.Fprintln(stderr, "mate:", err)
	var ue *usageError
	if errors.As(err, &ue) {
		return 2
	}
	return 1
}
