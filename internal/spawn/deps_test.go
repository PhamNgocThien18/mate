package spawn

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/runtime"
)

// LiveDeps' runtime runs the herdr it is given, else the one on PATH, which
// is what every command but the console runs (CON-189).
func TestLiveDepsRunTheGivenHerdrElseTheOneOnPath(t *testing.T) {
	// A herdr that leaves <path>.ran behind when it runs. It answers nothing
	// Herdr would: what matters here is which executable ran.
	fake := func() string {
		path := filepath.Join(t.TempDir(), "herdr")
		if err := os.WriteFile(path, []byte("#!/bin/sh\n: > \"$0.ran\"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	ran := func(path string) bool {
		_, err := os.Stat(path + ".ran")
		return err == nil
	}
	listAgents := func(deps Deps) {
		_, _ = deps.Runtime.ListAgents(context.Background(), runtime.SessionHandle{Name: "con-189"})
	}
	onPath, given := fake(), fake()
	t.Setenv("PATH", filepath.Dir(onPath))

	listAgents(LiveDeps(harness.Registry{}, WithHerdrBinary(given)))
	if !ran(given) || ran(onPath) {
		t.Fatalf("with %s given: it ran %v, the herdr on PATH ran %v", given, ran(given), ran(onPath))
	}
	listAgents(LiveDeps(harness.Registry{}))
	if !ran(onPath) {
		t.Fatal("with no herdr given, the one on PATH did not run")
	}
}
