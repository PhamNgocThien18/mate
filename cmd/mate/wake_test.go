package main

import (
	"os"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// wakeFixture is a project with a ship that handed back on a branch one
// commit ahead (k3), its reviewer, a Crew asking a question, and one still
// working.
func wakeFixture(t *testing.T) *store.Workspace {
	t.Helper()
	f := newDiffFixture(t)
	w := f.w
	commitInWorktree(t, f.worktree, "README.md", "shop\nmore\n", "add a line")
	if err := os.MkdirAll(w.CrewDir("shop", "k3"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.CrewHandback("shop", "k3"), []byte("## For the next crew\nREADME now has two lines.\nThe build is `make`.\n\n| criterion | pass/fail | command | output |\n| --- | --- | --- | --- |\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendStatus("shop", "k3", "working: adding the line"); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendStatus("shop", "k3", "wait-mate: ready in branch mate/k3"); err != nil {
		t.Fatal(err)
	}
	for crew, meta := range map[string]map[string]string{
		"rev-k3":   {spawn.MetaHarness: "claude", spawn.MetaModel: "sonnet", spawn.MetaEffort: "medium", spawn.MetaKind: "scout", spawn.MetaState: spawn.CrewStateSpawned, spawn.MetaTask: "Review k3"},
		"ask-k4":   {spawn.MetaHarness: "codex", spawn.MetaState: spawn.CrewStateSpawned, spawn.MetaTask: "ask"},
		"busy-k5":  {spawn.MetaHarness: "codex", spawn.MetaState: spawn.CrewStateSpawned, spawn.MetaTask: "busy"},
		"done-old": {spawn.MetaHarness: "codex", spawn.MetaState: "finished", spawn.MetaTask: "old"},
	} {
		if err := w.WriteCrewMeta("shop", crew, meta); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(w.CrewDir("shop", "rev-k3"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteMeta(w.CrewReviewMeta("shop", "rev-k3"), map[string]string{"crew": "k3"}); err != nil {
		t.Fatal(err)
	}
	for crew, line := range map[string]string{
		"rev-k3":   "working: reading the diff",
		"ask-k4":   "needs-decision: keep the old endpoint, or remove it now that nothing calls it? Removing it breaks the mobile app v1.2.",
		"busy-k5":  "working: running the test suite",
		"done-old": "wait-mate: long done",
	} {
		if err := w.AppendStatus("shop", crew, line); err != nil {
			t.Fatal(err)
		}
	}
	return w
}

// runWake runs `mate wake shop` as a Mate does, and fails the test on a
// non-zero exit. What the wake says is tested in internal/wake.
func runWake(t *testing.T, w *store.Workspace, args ...string) string {
	t.Helper()
	code, out, errOut := runMate(t, append([]string{"wake", "shop", "--workspace", w.Root()}, args...)...)
	if code != 0 {
		t.Fatalf("mate wake: exit %d\n%s", code, errOut)
	}
	return out
}

// The command prints internal/wake's text for the project named, its
// three parts, and a wake moves part 2's cursor.
func TestWakeCommandPrintsTheWake(t *testing.T) {
	t.Parallel()
	w := wakeFixture(t)
	text := runWake(t, w)
	for _, want := range []string{
		"# mate wake shop · ",
		"== 1. To act on (2) ==\n",
		"needs-decision ask-k4: keep the old endpoint",
		"== 2. New status since your last wake (4) ==\n",
		"== 3. Open crews (4) ==\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("mate wake lacks %q:\n%s", want, text)
		}
	}
	if cursor, err := w.ReadWakeCursor("shop"); err != nil || len(cursor) == 0 {
		t.Fatalf("a wake left the cursor %v, %v; want it moved", cursor, err)
	}
	if text := runWake(t, w); !strings.Contains(text, "== 2. New status since your last wake (0) ==\nnone\n") {
		t.Errorf("the second wake showed part 2's lines again:\n%s", text)
	}
}

// `--peek` reaches internal/wake as a wake that moves nothing: the cursor
// stays where it was, and the Mate's next wake still shows the lines.
func TestWakeCommandPeekLeavesTheCursor(t *testing.T) {
	t.Parallel()
	w := wakeFixture(t)
	peeked := runWake(t, w, "--peek")
	if !strings.Contains(peeked, "== 2. New status since your last wake (4) ==") {
		t.Fatalf("peek did not show the new lines:\n%s", peeked)
	}
	if cursor, err := w.ReadWakeCursor("shop"); err != nil || len(cursor) != 0 {
		t.Fatalf("a peek moved the cursor to %v, %v", cursor, err)
	}
	if text := runWake(t, w); !strings.Contains(text, "== 2. New status since your last wake (4) ==") {
		t.Errorf("the wake after a peek lost its lines:\n%s", text)
	}
}

func TestWakeCommandWantsOneProject(t *testing.T) {
	t.Parallel()
	w := wakeFixture(t)
	if code, _, errOut := runMate(t, "wake", "--workspace", w.Root()); code == 0 || !strings.Contains(errOut, "usage: mate wake <project> [--peek] [--workspace <dir>]") {
		t.Fatalf("no project: exit %d, %q", code, errOut)
	}
	if code, _, _ := runMate(t, "wake", "nope", "--workspace", w.Root()); code == 0 {
		t.Fatal("an unknown project was woken")
	}
}
