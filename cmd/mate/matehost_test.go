package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// `mate mate repl` is a subcommand of `mate mate`, with a project.
func TestMateREPLWantsAProject(t *testing.T) {
	t.Parallel()
	var out, errw bytes.Buffer
	err := testCLI().run([]string{"mate", "repl"}, &out, &errw)
	var usage *usageError
	if !errors.As(err, &usage) || !strings.Contains(err.Error(), "mate mate repl: want exactly 1 argument") {
		t.Fatalf("mate mate repl with no project: %v", err)
	}
	if !strings.Contains(errw.String(), "usage: mate mate repl <project>") {
		t.Fatalf("usage = %q", errw.String())
	}
	err = testCLI().run([]string{"mate"}, &out, &errw)
	if err == nil || !strings.Contains(err.Error(), "host|repl") {
		t.Fatalf("mate mate alone: %v", err)
	}
}

// The REPL process the host runs is told to quit when the host stops, and
// the host does not wait on it for longer than replQuitWait.
func TestREPLProcessEndsWithTheHost(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	bin := filepath.Join(dir, "mate")
	args := filepath.Join(dir, "args")
	script := "#!/bin/sh\necho \"$@\" > " + args + "\nexec sleep 30\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runREPLProcess(ctx, bin, "shop", "/ws") }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if data, err := os.ReadFile(args); err == nil && len(data) > 0 {
			if got := strings.TrimSpace(string(data)); got != "mate repl shop --workspace /ws --in-pane" {
				t.Fatalf("the REPL was run as %q", got)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the REPL never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(replQuitWait + 3*time.Second):
		t.Fatal("the REPL process outlived the host")
	}
}
