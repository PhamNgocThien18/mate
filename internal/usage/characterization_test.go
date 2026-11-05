package usage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/catalog"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/harness/codex"
	"github.com/nguyenngocanh94/mate/internal/timeline"
)

// TestUsageCharacterizationOnTheFixtureCorpus pins what `mate usage` prints
// for the fixture corpus - internal/timeline's captured run, a Claude Code
// Mate and a codex-cli crew - after a reindex reads it, byte for byte.
// It was recorded before the transcript capability moved behind the harness
// registry (docs/plans/harness-registry-2026-09-30.md, PR 5): that change
// must leave these numbers, and the dashboard's on the same corpus
// (internal/dashboard/numbers_characterization_test.go), as it found them.
// The golden is the one cmd/mate recorded before the pages moved here
// (CON-168), unchanged.
func TestUsageCharacterizationOnTheFixtureCorpus(t *testing.T) {
	// The SPAWN→CLOSE column is local time.
	local := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = local })

	w := newWorkspace(t, "shop")
	corpus := func(name string) string {
		path, err := filepath.Abs(filepath.Join("..", "timeline", "testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		return path
	}
	if err := w.WriteMateMeta("shop", map[string]string{
		"harness":    "claude",
		"agent":      "mate-shop",
		"session_id": "8414030c-5d90-4925-94cc-c94e12aae4a9",
		"transcript": corpus("claude-2.1.278-transcript.jsonl"),
		"started_at": "2026-09-19T10:43:30Z",
	}); err != nil {
		t.Fatalf("WriteMateMeta: %v", err)
	}
	if err := w.WriteCrewMeta("shop", "buybtn", map[string]string{
		"harness":    "codex",
		"agent":      "crew-buybtn",
		"task":       "Add a Buy button to README.md linking to the checkout page",
		"branch":     "mate/buybtn",
		"worktree":   ".worktrees/shop-buybtn",
		"session_id": "01a0b944-33bb-7503-9db7-cd51ff61855a",
		"transcript": corpus("codex-0.154-rollout.jsonl"),
		"started_at": "2026-09-19T10:44:09Z",
		"state":      "spawned",
	}); err != nil {
		t.Fatalf("WriteCrewMeta: %v", err)
	}

	// `mate reindex`, without Herdr and without the machine's own
	// transcript directories: the meta names every transcript.
	writer, err := db.Open(w)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	none := t.TempDir()
	if err := timeline.New(w, writer, timeline.Deps{
		Harnesses: catalog.Default(),
		TranscriptRoots: map[harness.Kind]string{
			claude.KindClaude: filepath.Join(none, "claude-projects"),
			codex.KindCodex:   filepath.Join(none, "codex-sessions"),
		},
	}).Reindex(context.Background()); err != nil {
		writer.Close()
		t.Fatalf("Reindex: %v", err)
	}
	writer.Close()

	got := "== mate usage shop\n" + writePage(t, w, Request{Project: "shop"}) +
		"== mate usage shop buybtn\n" + writePage(t, w, Request{Project: "shop", Crew: "buybtn"})

	golden := filepath.Join("testdata", "usage-fixture-corpus.golden")
	if os.Getenv("MATE_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden: %v (re-run with MATE_UPDATE_GOLDEN=1 to create it)", err)
	}
	if got != string(want) {
		t.Fatalf("`mate usage` on the fixture corpus changed (%s):\n%s", golden, got)
	}
}
