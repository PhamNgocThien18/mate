package teardown

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/process"
)

const wt = "/ws/.worktrees/plantrip-pla-6"

func TestOwnsOnlyTheWorktreeAndBelow(t *testing.T) {
	tg := Target{Worktree: wt}
	for path, want := range map[string]bool{
		wt:                             true,
		wt + "/HelloVietnam.xcodeproj": true,
		wt + "-be-r2":                  false,
		"/ws/plantrip/plantrip_api":    false,
		"":                             false,
	} {
		if got := tg.Owns(path); got != want {
			t.Errorf("Owns(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestProcessesStopsWhatRunsInTheWorktreeButNotAShell(t *testing.T) {
	lsof := "p100\ncphp\nfcwd\nn" + wt + "\n" +
		"p101\nczsh\nfcwd\nn" + wt + "/app\n" +
		"p102\ncnode\nfcwd\nn" + wt + "-be-r2\n" +
		"p103\ncvite\nfcwd\nn" + wt + "/web\n"
	run := &process.FakeRunner{Default: process.Result{Stdout: []byte(lsof)}}
	alive := map[int]bool{100: true, 103: true}
	var sent []string
	signal := func(pid int, sig syscall.Signal) error {
		if !alive[pid] {
			return syscall.ESRCH
		}
		if sig != 0 {
			sent = append(sent, fmt.Sprintf("%d:%d", pid, sig))
		}
		if sig == syscall.SIGTERM && pid == 100 {
			alive[pid] = false // php exits on TERM
		}
		if sig == syscall.SIGKILL {
			alive[pid] = false
		}
		return nil
	}
	got, err := Processes{Runner: run, Signal: signal, Grace: 50 * time.Millisecond}.Sweep(context.Background(), []Target{Target{Worktree: wt}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"process php 100", "process vite 103"}; !slices.Equal(got, want) {
		t.Fatalf("removed = %q, want %q", got, want)
	}
	want := []string{fmt.Sprintf("100:%d", syscall.SIGTERM), fmt.Sprintf("103:%d", syscall.SIGTERM), fmt.Sprintf("103:%d", syscall.SIGKILL)}
	if !slices.Equal(sent, want) {
		t.Fatalf("signals = %q, want %q", sent, want)
	}
}

type sweepFunc func(context.Context, []Target, bool) ([]string, error)

func (f sweepFunc) Sweep(ctx context.Context, ts []Target, dry bool) ([]string, error) {
	return f(ctx, ts, dry)
}

func TestCleanRunsTheHookInTheWorktreeThenEverySweep(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "crew", "teardown.log")
	var order []string
	run := &process.FakeRunner{Handler: func(_ context.Context, s process.Spec) (process.Result, error) {
		order = append(order, "hook")
		if s.Name != "sh" || !slices.Equal(s.Args, []string{"-c", "make down"}) || s.Dir != dir {
			t.Fatalf("hook ran as %s in %s", process.CommandKey(s), s.Dir)
		}
		if !slices.Contains(s.Env, "MATE_CREW=pla-6") {
			t.Fatalf("hook env lacks MATE_CREW: %q", s.Env)
		}
		return process.Result{Stdout: []byte("stack down\n")}, nil
	}}
	sweep := func(name string, err error) Sweep {
		return sweepFunc(func(context.Context, []Target, bool) ([]string, error) {
			order = append(order, name)
			return []string{name + " gone"}, err
		})
	}
	c := Cleaner{Runner: run, Sweeps: []Sweep{sweep("a", nil), sweep("b", errors.New("b: daemon down")), sweep("c", nil)}}
	r := c.Clean(context.Background(), Target{Worktree: dir, Hook: "make down", Env: []string{"MATE_CREW=pla-6"}, Log: logPath})

	if want := []string{"hook", "a", "b", "c"}; !slices.Equal(order, want) {
		t.Fatalf("order = %q, want %q", order, want)
	}
	if r.Hook != "ok" || !slices.Equal(r.Pending, []string{"b: daemon down"}) {
		t.Fatalf("report = %+v", r)
	}
	if got, want := r.Meta(), "pending: b: daemon down (hook ok, a gone, b gone, c gone)"; got != want {
		t.Fatalf("Meta() = %q, want %q", got, want)
	}
	log, err := os.ReadFile(logPath)
	if err != nil || !strings.Contains(string(log), "$ make down\nstack down") {
		t.Fatalf("log = %q, %v", log, err)
	}
}

func TestCleanReportsAFailedHookAndStillSweeps(t *testing.T) {
	run := &process.FakeRunner{Default: process.Result{ExitCode: 2, Stderr: []byte("make: *** No rule to make target 'down'.")}}
	swept := false
	c := Cleaner{Runner: run, Sweeps: []Sweep{sweepFunc(func(context.Context, []Target, bool) ([]string, error) { swept = true; return nil, nil })}}
	r := c.Clean(context.Background(), Target{Worktree: t.TempDir(), Hook: "make down"})
	if !swept || r.Hook != "failed" || len(r.Pending) != 1 || !strings.Contains(r.Pending[0], "No rule to make target") {
		t.Fatalf("swept %v, report %+v", swept, r)
	}
}

func TestCleanSkipsTheHookWhenTheWorktreeIsGone(t *testing.T) {
	run := &process.FakeRunner{}
	r := Cleaner{Runner: run}.Clean(context.Background(), Target{Worktree: filepath.Join(t.TempDir(), "gone"), Hook: "make down"})
	if len(run.Calls) != 0 || r.Ran() {
		t.Fatalf("calls %d, report %+v; want nothing", len(run.Calls), r)
	}
	if got := r.Meta(); got != "done" {
		t.Fatalf("Meta() = %q, want done", got)
	}
}

func TestCleanStopsAHookAtItsTimeout(t *testing.T) {
	c := Cleaner{Runner: process.ExecRunner{}, HookTimeout: 200 * time.Millisecond}
	start := time.Now()
	r := c.Clean(context.Background(), Target{Worktree: t.TempDir(), Hook: "sleep 30"})
	if time.Since(start) > 10*time.Second || len(r.Pending) != 1 || !strings.Contains(r.Pending[0], "did not finish") {
		t.Fatalf("report %+v after %s", r, time.Since(start))
	}
}
