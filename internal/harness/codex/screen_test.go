package codex

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
)

func TestStartupTrustDialogV167(t *testing.T) {
	t.Parallel()
	screen := startupFixture(t, "codex-0.167.0-trust-dialog.txt")
	for _, tc := range []struct {
		name     string
		screen   string
		want     harness.StartupScreen
		selected bool
	}{
		{"accept selected", screen, harness.StartupScreenTrustDialog, true},
		{"back selected", strings.ReplaceAll(strings.ReplaceAll(screen, "› 1.", "  1."), "  2.", "› 2."), harness.StartupScreenTrustDialog, false},
		{"unknown option", strings.ReplaceAll(screen, "Back to Agent Command Center", "Something else"), harness.StartupScreenUnrecognized, false},
		{"unknown footer", strings.ReplaceAll(screen, "esc back", "esc cancel"), harness.StartupScreenUnrecognized, false},
		{"missing highlight", strings.ReplaceAll(screen, "› 1.", "  1."), harness.StartupScreenUnrecognized, false},
		{"quoted above composer", screen + "\n› " + CodexComposerPlaceholder + "\n", harness.StartupScreenReady, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := (Codex{}).Screen()
			if got := p.ClassifyStartup(tc.screen); got != tc.want {
				t.Errorf("ClassifyStartup = %s, want %s", got, tc.want)
			}
			if got := p.StartupTargetSelected(harness.StartupScreenTrustDialog, tc.screen); got != tc.selected {
				t.Errorf("StartupTargetSelected = %v, want %v", got, tc.selected)
			}
		})
	}
}

func startupFixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "startup", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// TestStartupDialogLayoutsOfOneScreenShareTheirAnswer holds the rule that
// lets one screen have several measured layouts: the settle presses one
// answer for the screen, so every layout of it must take the same keys to
// the same option position.
func TestStartupDialogLayoutsOfOneScreenShareTheirAnswer(t *testing.T) {
	t.Parallel()
	p := codexStartup()
	first := map[harness.StartupScreen]harness.StartupDialog{}
	for _, d := range p.Dialogs {
		f, ok := first[d.Screen]
		if !ok {
			first[d.Screen] = d
			continue
		}
		if !slices.Equal(d.SelectKeys, f.SelectKeys) || d.Target != f.Target {
			t.Fatalf("%s layouts disagree: %v/%d vs %v/%d", d.Screen, d.SelectKeys, d.Target, f.SelectKeys, f.Target)
		}
	}
}
