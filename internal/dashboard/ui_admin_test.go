package dashboard

import (
	"os"
	"os/exec"
	"path"
	"strings"
	"testing"
)

// TestAdminUIBehaviour runs the admin page's node tests against app.js and
// task.js: ui_observability_test.cjs for the Trace tab and
// ui_journey_test.cjs for the Journey tab (docs/design/task-page.md). As
// with TestOfficeBehaviour, node is not a dependency of this repository, so
// the subtests are skipped where it is absent rather than failing the build.
func TestAdminUIBehaviour(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Log("no node on PATH, so the admin UI node tests are not run here")
		return
	}
	for _, file := range []string{"ui_observability_test.cjs", "ui_journey_test.cjs"} {
		t.Run(file, func(t *testing.T) {
			cmd := exec.Command(node, "--test", "--test-timeout=30000", path.Join(".", file))
			cmd.Env = append(os.Environ(), "TZ=UTC")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s failed:\n%s", file, out)
			}
			if !strings.Contains(string(out), "fail 0") {
				t.Fatalf("unexpected node output:\n%s", out)
			}
		})
	}
}
