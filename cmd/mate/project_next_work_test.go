package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/store"
)

// `mate project next-work` prints the setting, picking when project.yaml
// says nothing, records pick or ask, and refuses anything else (CON-120).
func TestProjectNextWorkRecordsTheCaptainsChoice(t *testing.T) {
	w := wakeFixture(t)
	code, out, _ := runMate(t, "project", "next-work", "shop", "--workspace", w.Root())
	if code != 0 || !strings.HasPrefix(out, "next work: pick - ") {
		t.Fatalf("unset: exit %d, %q", code, out)
	}

	code, out, _ = runMate(t, "project", "next-work", "shop", "ask", "--workspace", w.Root())
	if code != 0 || !strings.HasPrefix(out, "next work: ask - ") {
		t.Fatalf("ask: exit %d, %q", code, out)
	}
	if cfg, err := w.LoadProject("shop"); err != nil || cfg.NextWork != store.NextWorkAsk {
		t.Fatalf("project.yaml next_work = %q, %v; want ask", cfg.NextWork, err)
	}
	if code, _, _ = runMate(t, "project", "next-work", "shop", "always", "--workspace", w.Root()); code == 0 {
		t.Fatal("a value other than pick or ask was accepted")
	}
	if cfg, _ := w.LoadProject("shop"); cfg.NextWork != store.NextWorkAsk {
		t.Fatalf("a refused value changed the setting to %q", cfg.NextWork)
	}
}

// The Mate's merge ends with the next-work setting, because a landed task
// is when it decides what runs next; the captain's merge prints one line.
func TestCmdMergeTellsTheMateWhatComesNext(t *testing.T) {
	w, root := mergeCLIWorkspace(t)
	cfg, err := w.LoadProject("shop")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Review = store.ReviewOff
	if err := w.SaveProject("shop", cfg); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if err := testCLI().withEnv("MATE_CALLER", "mate").cmdMerge([]string{"--workspace", root, "shop", "k3"}, &out, &errw); err != nil {
		t.Fatalf("merge: %v\nstderr: %s", err, errw.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "crew finished") || !strings.HasPrefix(lines[1], "next work: pick - ") {
		t.Fatalf("the Mate's merge printed %q, want the merge line then the next-work line", out.String())
	}
}
