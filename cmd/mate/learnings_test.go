package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `mate learnings open` lists the records' open predictions with the close
// command; `close` rewrites one and says so. Both read only the records.
func TestLearningsOpenAndClose(t *testing.T) {
	w := liveCrewWorkspace(t, "shop")
	dir := w.LearningsDir("shop")
	out := runCLI(t, "learnings", "open", "shop", "--workspace", w.Root())
	if out != "no open predictions in "+dir+"\n" {
		t.Fatalf("empty open = %q", out)
	}
	path := w.CrewLearning("shop", "hu-110")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# hu-110\nLearning: captured\n\n## Predictions\n1. router-level acceptance avoids the UI-test dispute; next check the next integration brief — outcome: open\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out = runCLI(t, "learnings", "open", "shop", "--workspace", w.Root())
	want := "open predictions in " + dir + " (1); close one with mate learnings close shop <crew> <id> improved|unchanged|inconclusive|not comparable --evidence \"<task/revision: what it showed>\"\n" +
		"hu-110 #1: router-level acceptance avoids the UI-test dispute; next check the next integration brief\n"
	if out != want {
		t.Fatalf("open = %q, want %q", out, want)
	}
	out = runCLI(t, "learnings", "close", "shop", "hu-110", "1", "improved", "--evidence", "HU-114 r1: pass, no dispute", "--workspace", w.Root())
	if !strings.HasPrefix(out, "closed hu-110 #1: outcome improved (HU-114 r1: pass, no dispute, ") || !strings.HasSuffix(out, ") in "+path+"\n") {
		t.Fatalf("close = %q", out)
	}
	if out := runCLI(t, "learnings", "open", "shop", "--workspace", w.Root()); !strings.HasPrefix(out, "no open predictions") {
		t.Fatalf("open after close = %q", out)
	}
	for _, args := range [][]string{
		{"learnings", "close", "shop", "hu-110", "1", "better", "--evidence", "x", "--workspace", w.Root()},
		{"learnings", "close", "shop", "hu-110", "7", "improved", "--evidence", "x", "--workspace", w.Root()},
		{"learnings", "close", "shop", "hu-110", "1", "improved", "--workspace", w.Root()},
		{"learnings", "list", "shop", "--workspace", w.Root()},
	} {
		var ue *usageError
		if err := testCLI().run(args, &strings.Builder{}, &strings.Builder{}); !errors.As(err, &ue) {
			t.Errorf("%v: err = %v, want a usage error", args, err)
		}
	}
}
