package teardown

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/nguyenngocanh94/mate/internal/platform"
	"github.com/nguyenngocanh94/mate/internal/process"
)

// interactive are the programs a person may have open in a crew's
// worktree - a shell, an editor. The worktree going away is all they need
// to notice; mate does not kill them.
var interactive = map[string]bool{
	"zsh": true, "bash": true, "sh": true, "fish": true, "nu": true, "tmux": true,
	"vim": true, "nvim": true, "emacs": true, "zed": true, "Zed": true, "Code": true, "code": true,
}

// Processes stops what still runs with its working directory inside the
// worktree once the crew's agent is gone: a dev server, a file watcher, an
// xcodebuild the crew started in the background. It asks with SIGTERM and
// ends it with SIGKILL after Grace.
type Processes struct {
	Runner process.Runner
	// Grace is how long a process gets after SIGTERM; zero is 5 s.
	Grace time.Duration
	// Signal sends a signal; nil is platform.Signal. Signal 0 probes.
	Signal func(pid int, sig syscall.Signal) error
}

// Sweep implements Sweep.
func (p Processes) Sweep(ctx context.Context, targets []Target, dry bool) ([]string, error) {
	run := p.Runner
	if run == nil {
		run = process.ExecRunner{}
	}
	// -d cwd alone lists every process's working directory; -F pcn prints
	// pid, command and path one field per line.
	res, err := run.Run(ctx, process.Spec{Name: "lsof", Args: []string{"-n", "-w", "-d", "cwd", "-F", "pcn"}})
	if err != nil {
		if missing(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("processes: %v", err)
	}
	self, parent := os.Getpid(), os.Getppid()
	type proc struct {
		pid  int
		name string
	}
	var owned []proc
	cur := proc{}
	for _, line := range strings.Split(string(res.Stdout), "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			cur = proc{}
			cur.pid, _ = strconv.Atoi(line[1:])
		case 'c':
			cur.name = line[1:]
		case 'n':
			if cur.pid > 1 && cur.pid != self && cur.pid != parent && !interactive[cur.name] && owns(targets, line[1:]) {
				owned = append(owned, cur)
			}
		}
	}
	signal := p.Signal
	if signal == nil {
		signal = platform.Signal
	}
	grace := p.Grace
	if grace <= 0 {
		grace = 5 * time.Second
	}
	var removed, failed []string
	for _, pr := range owned {
		if dry {
			removed = append(removed, fmt.Sprintf("process %s %d", pr.name, pr.pid))
			continue
		}
		if err := signal(pr.pid, syscall.SIGTERM); err != nil {
			continue // already gone
		}
		deadline := time.Now().Add(grace)
		for signal(pr.pid, 0) == nil && time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return removed, ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
		if signal(pr.pid, 0) == nil {
			if err := signal(pr.pid, syscall.SIGKILL); err != nil {
				failed = append(failed, fmt.Sprintf("process %s %d: %v", pr.name, pr.pid, err))
				continue
			}
		}
		removed = append(removed, fmt.Sprintf("process %s %d", pr.name, pr.pid))
	}
	if len(failed) > 0 {
		return removed, fmt.Errorf("%s", strings.Join(failed, "; "))
	}
	return removed, nil
}
