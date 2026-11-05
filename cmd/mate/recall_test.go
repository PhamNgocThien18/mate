package main

// The digest's text is internal/recall's, tested there (CON-167). These
// tests cover what cmd/mate adds: the flags, the exit codes, and the real
// commands recallDeps hands the digest to quote.

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/memory"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// recallFixture is backlogFixtureWorkspace (six crews, one per state, k5
// closed, k6 blocked by an open stale incident) plus every file recall
// frames: a PROJECT.md with one anchored and one stale-anchored repo-state
// line, a backlog.md whose In flight names a crew that is not open and
// leaves out one that is, a memory.md with a stale lesson, WORKSPACE.md,
// and the project's CREW.md with a rule. The repository has two commits, so
// the anchor at the first one is behind main. internal/recall's fixture is
// a copy of it.
func recallFixture(t *testing.T) (*store.Workspace, time.Time, string) {
	t.Helper()
	w, now := backlogFixtureWorkspace(t)
	repo := filepath.Join(w.ProjectHome("shop"), "shop")
	first := strings.TrimSpace(gitOutput(t, repo, "rev-parse", "--short", "HEAD"))
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# shop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitOrFatal(t, repo, "add", "README.md")
	runGitOrFatal(t, repo, "commit", "-m", "readme")
	if err := os.MkdirAll(w.MateDir("shop"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The captain holds this project in manual, so the hand-backs below are
	// the captain's inbox items the recall lists.
	if err := w.SetMode("shop", false); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, w.ProjectDoc("shop"), "# shop\n\n## What this project is\n- A landing page for the ESP32 kit. (captain, 2026-09-18)\n\n## Layout and state\n- main has one README and no build. (crews/k1/report.md §Durable facts, main@"+first+", 2026-09-19)\n\n## How to work here\n\n## Research on file\n\n## Captain's decisions\n")
	writeFixture(t, w.BacklogFile("shop"), "# Backlog\n\n## In flight\n- k2 (ship, mate/k2): fix the flaky test\n- k3: pick a colour\n- k4 ship the button\n- k6 (scout): crash\n- buyesp32 (ship): stopped at needs-decision\n\n## Held for the captain\n- k3, asked 2026-09-19: \"red or blue?\" Waiting on: the captain.\n\n## Queued\n- none\n\n## Done (10 most recent)\n- k5 (ship, 2026-09-18): merged\n- rpi35 (scout, 2026-09-17): crews/rpi35/report.md\n")
	writeFixture(t, w.MemoryFile("shop"), "# Memory\n"+memory.HeaderPointer+"\n\n## Captain\n- Speaks Vietnamese. (captain, 2026-09-17)\n\n## Lessons\n- Scouts commit their reports unless told not to. (sent.log to rpi35 2026-09-18) <!--a:2026-08-01-->\n- The canary lesson is HERON-7. (captain, 2026-09-19) <!--a:2026-09-19-->\n")
	writeFixture(t, w.WorkspaceDoc(), "# Workspace\n- Never merge on a Friday.\n")
	writeFixture(t, w.ProjectCrewDoc("shop"), "<!-- rules for every crew of shop -->\n- Run the tests before wait-mate.\n")
	return w, now, first
}

func writeFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}

// partHeadings returns the `== N. title ==` lines in the order printed.
func partHeadings(out string) []string {
	var got []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "== ") && strings.HasSuffix(l, " ==") {
			got = append(got, l)
		}
	}
	return got
}

// mustOrder fails unless every needle is in out, each after the one before.
func mustOrder(t *testing.T, out string, needles ...string) {
	t.Helper()
	at := 0
	for _, n := range needles {
		i := strings.Index(out[at:], n)
		if i < 0 {
			t.Fatalf("%q missing, or out of order after byte %d, in:\n%s", n, at, out)
		}
		at += i + len(n)
	}
}

// TestRecallQuotesTheRealCommands runs `mate recall` on recallFixture and
// finds, in each part, the words of the command recallDeps names for it:
// `mate backlog`'s table, the next-work line, a `mate work list` row,
// `mate project facts`, `mate memory check`'s anchor warning, budget line
// and problems, and this binary's path in the cut notice.
func TestRecallQuotesTheRealCommands(t *testing.T) {
	w, now, first := recallFixture(t)
	path := matehost.WorkPath(w.MateDir("shop"))
	hook, err := matehost.AddWork(path, matehost.WorkAssigned, "Pre-push hook", "", now)
	if err != nil {
		t.Fatal(err)
	}
	if err := matehost.WaitWork(path, hook, 3, "which hook?", now); err != nil {
		t.Fatal(err)
	}
	var out, errw strings.Builder
	if code := mainRun([]string{"recall", "shop", "--workspace", w.Root()}, &out, &errw); code != 0 {
		t.Fatalf("exit %d: %s", code, errw.String())
	}
	var table bytes.Buffer
	if err := printBacklog(w, "shop", false, time.Now(), &table); err != nil {
		t.Fatal(err)
	}
	header, _, _ := strings.Cut(table.String(), "\n")
	cfg, err := w.LoadProject("shop")
	if err != nil {
		t.Fatal(err)
	}
	mustOrder(t, out.String(),
		"== 1. Live state ==", nextWorkLine(cfg)+"\n", "crews (mate backlog shop):\n"+header+"\n", "5 open, 1 closed\nattention: ",
		fmt.Sprintf("- #%d waiting on news #3 Pre-push hook\n    which hook?\n", hook),
		"== 2. ", "commits: 2 on main",
		"== 3. ", "warning: PROJECT.md line 7 is anchored at shop:main@"+first+", and shop:main has 1 newer commit(s); treat it as a hint",
		"== 7. ", "budget: ", " estimated tokens (memory.md ", "memory.md:8: aging entry last reinforced 2026-08-01", " problem(s): load skill stow",
	)

	out.Reset()
	if code := mainRun([]string{"recall", "shop", "--max-bytes", "10", "--workspace", w.Root()}, &out, &errw); code != 0 {
		t.Fatalf("--max-bytes 10: exit %d: %s", code, errw.String())
	}
	if !strings.Contains(out.String(), "Run `"+selfBinary()+" recall shop --part 2,") {
		t.Fatalf("the cut notice does not name this binary (%s):\n%s", selfBinary(), out.String())
	}
}

func TestRecallCLI(t *testing.T) {
	w, _, _ := recallFixture(t)
	var out, errw strings.Builder
	if code := mainRun([]string{"recall", "shop", "--live", "--workspace", w.Root()}, &out, &errw); code != 0 {
		t.Fatalf("exit %d: %s", code, errw.String())
	}
	if !strings.Contains(out.String(), "== 1. Live state ==") || strings.Contains(out.String(), "== 2.") {
		t.Fatalf("recall --live printed:\n%s", out.String())
	}
	for _, args := range [][]string{
		{"recall"}, {"recall", "shop", "extra"}, {"recall", "shop", "--max-bytes", "-1"},
		// --part: not a part number, a part the digest does not have, and
		// together with --live (CON-68).
		{"recall", "shop", "--part", "3,x"}, {"recall", "shop", "--part", "42"}, {"recall", "shop", "--live", "--part", "1"},
	} {
		if code := mainRun(append(args, "--workspace", w.Root()), &out, &errw); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	if code := mainRun([]string{"recall", "nosuch", "--workspace", w.Root()}, &out, &errw); code != 1 {
		t.Errorf("an unknown project: exit %d, want 1", code)
	}
	out.Reset()
	if code := mainRun([]string{"recall", "shop", "--part", "5,3", "--workspace", w.Root()}, &out, &errw); code != 0 {
		t.Fatalf("--part 5,3: exit %d: %s", code, errw.String())
	}
	if got := partHeadings(out.String()); strings.Join(got, "|") != "== 3. PROJECT.md ==|== 5. memory.md ==" {
		t.Errorf("--part 5,3 printed parts %q", got)
	}
}
