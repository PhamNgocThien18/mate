package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/query"
)

func TestExternalTasksAreUntouchedByRecall(t *testing.T) {
	w, _ := consoleFixture(t, "shop")
	files := map[string]string{
		filepath.Join(w.ProjectHome("shop"), ".beads", "metadata.json"): "project tracker",
		filepath.Join(w.ProjectHome("shop"), ".beads", "issues.jsonl"):  "existing tasks",
		filepath.Join(w.ProjectDir("shop"), ".beads", "metadata.json"):  "legacy tracker",
		filepath.Join(w.ProjectDir("shop"), "tasks.yaml"):               "legacy plan",
	}
	for path, text := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	bin := t.TempDir()
	for _, name := range []string{"bd", "bv"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\necho invoked > \"$0.called\"\nexit 99\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var out, stderr bytes.Buffer
	if err := testCLI().run([]string{"recall", "shop", "--live", "--workspace", w.Root()}, &out, &stderr); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "== Tool:") || strings.Contains(out.String(), "Beads") {
		t.Fatalf("recall still loads the external tracker: %s", &out)
	}
	for path, want := range files {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("external task data changed: %s: %q, %v", path, got, err)
		}
	}
	for _, path := range []string{filepath.Join(bin, "bd.called"), filepath.Join(bin, "bv.called"), filepath.Join(w.ProjectDir("shop"), "locks")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("task integration left a trace at %s: %v", path, err)
		}
	}
	snap, err := query.Load(context.Background(), w, consoleHarnesses(), nil)
	if err != nil || len(snap.Commands) != 0 {
		t.Fatalf("default task shortcut still present: %+v, %v", snap.Commands, err)
	}
}

func TestTaskToolCommandsAreRemoved(t *testing.T) {
	for _, name := range []string{"tool", "beads", "tasks", "task-triage", "epic", "task"} {
		var out, stderr bytes.Buffer
		if err := testCLI().run([]string{name, "shop"}, &out, &stderr); err == nil || !strings.Contains(err.Error(), "unknown command") {
			t.Fatalf("%s still has a built-in task command: %v", name, err)
		}
	}
}
