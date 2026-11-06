package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/query"
)

// The Console's harness catalog is the registry's: every kind in its order,
// each with a mark in every alphabet, and the registry's default for a Mate.
func TestConsoleHarnessesAreTheCatalogs(t *testing.T) {
	t.Parallel()
	got := consoleHarnesses()
	kinds := harnesses.Kinds()
	if len(got.List) != len(kinds) {
		t.Fatalf("catalog has %d harnesses, registry %d", len(got.List), len(kinds))
	}
	for i, h := range got.List {
		if h.Kind != query.HarnessKind(kinds[i]) {
			t.Errorf("catalog[%d] = %q, registry %q", i, h.Kind, kinds[i])
		}
		if h.Icon.Nerd == "" || h.Icon.Unicode == "" || h.Icon.ASCII == "" {
			t.Errorf("%s draws no mark in some alphabet: %+v", h.Kind, h.Icon)
		}
	}
	def, err := harnesses.Default(harness.RoleMate)
	if err != nil || got.MateDefault != query.HarnessKind(def) {
		t.Errorf("MateDefault = %q, registry %q (%v)", got.MateDefault, def, err)
	}
}

// A new workspace records the registry's defaults, so its file says what it
// runs; and a usage line lists the role's default first.
func TestNewWorkspaceRecordsTheRegistryDefaults(t *testing.T) {
	t.Parallel()
	d := workspaceDefaults()
	mate, _ := harnesses.Default(harness.RoleMate)
	crew, _ := harnesses.Default(harness.RoleCrew)
	if d.MateHarness != string(mate) || d.CrewHarness != string(crew) {
		t.Fatalf("defaults = %+v, want %s and %s", d, mate, crew)
	}
	if !strings.HasPrefix(harnessChoices(harness.RoleCrew, "|"), string(crew)+"|") {
		t.Fatalf("crew choices %q do not start with %s", harnessChoices(harness.RoleCrew, "|"), crew)
	}
}

// A harness offered for a Mate - in the Console's catalog and on `mate mate
// start`'s usage line - is exactly one whose Hooks are verified, the rule
// StartMate refuses by. Every harness is still in the catalog, because a
// Crew runs on it either way.
func TestOnlyAHarnessWithHooksIsOfferedForAMate(t *testing.T) {
	t.Parallel()
	mates := strings.Split(harnessChoices(harness.RoleMate, "|"), "|")
	for _, h := range consoleHarnesses().List {
		p, err := harnesses.Lookup(harness.Kind(h.Kind))
		if err != nil {
			t.Fatal(err)
		}
		hooks := p.Capabilities().Hooks.Verified()
		if h.Mate != hooks {
			t.Errorf("%s: offered for a Mate = %v, Hooks verified = %v", h.Kind, h.Mate, hooks)
		}
		if got := slices.Contains(mates, string(h.Kind)); got != hooks {
			t.Errorf("%s: in the Mate usage choices %q = %v, Hooks verified = %v", h.Kind, mates, got, hooks)
		}
	}
}

// The dashboard's "harness unsupported" is the harness profile's MCPConfig
// capability, wired through dashboardDeps: no harness is named, and a kind
// this binary does not have is unknown rather than supported.
func TestDashboardAsksTheProfileWhetherAMateTakesPlugins(t *testing.T) {
	t.Parallel()
	ask := dashboardDeps(nil).PluginHarness
	if ask == nil {
		t.Fatal("dashboardDeps sets no PluginHarness; the dashboard would call no harness unsupported")
	}
	for _, k := range harnesses.Kinds() {
		p, _ := harnesses.Lookup(k)
		c := p.Capabilities().MCPConfig
		err := ask(string(k))
		switch {
		case c.Verified() && err != nil:
			t.Errorf("%s: MCPConfig is verified but PluginHarness says %v", k, err)
		case !c.Verified() && (err == nil || !strings.Contains(err.Error(), c.Reason)):
			t.Errorf("%s: MCPConfig is %s; PluginHarness = %v, want the capability's reason %q", k, c.Status, err, c.Reason)
		}
	}
	if err := ask("no-such-harness"); err == nil || !strings.Contains(err.Error(), `unknown harness "no-such-harness"`) {
		t.Errorf("PluginHarness(no-such-harness) = %v, want it named as unknown", err)
	}
}
