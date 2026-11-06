package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/dispatch"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/harness/codex"
	"github.com/nguyenngocanh94/mate/internal/quota"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

func dispatchWorkspace(t *testing.T, table string) *store.Workspace {
	t.Helper()
	w, err := store.Init(t.TempDir(), workspaceDefaults())
	if err != nil {
		t.Fatal(err)
	}
	if table != "" {
		if err := os.WriteFile(dispatch.Path(w.StateDir()), []byte(table), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return w
}

const sampleTable = `{
  "rules": [
    {"when": "a trivial mechanical edit", "use": {"harness": "claude", "model": "haiku", "effort": "low"}, "why": "cheap"},
    {"when": "a big or ambiguous feature", "use": [
      {"harness": "claude", "model": "opus", "effort": "high"},
      {"harness": "codex", "model": "gpt-5.5", "effort": "xhigh"}
    ]}
  ],
  "default": {"harness": "codex", "effort": "medium"}
}`

// claudeReviewTable has a review profile on Claude only,
// while the workspace's default harness is Codex.
const claudeReviewTable = `{
  "rules": [{"when": "anything", "use": {"harness": "codex", "effort": "medium"}}],
  "review": {"harness": "claude", "model": "sonnet", "effort": "medium"},
  "default": {"harness": "codex", "effort": "medium"}
}`

func TestCrewSpawnParsesModelAndEffort(t *testing.T) {
	for _, args := range [][]string{
		{"crew", "spawn", "shop", "k3", "--brief", "b", "--effort", "hgih"},
		{"crew", "spawn", "shop", "k3", "--brief", "b", "--model", "-c"},
	} {
		var out, errw bytes.Buffer
		var ue *usageError
		if err := testCLI().run(args, &out, &errw); !errors.As(err, &ue) {
			t.Fatalf("%v: err = %v, want a usage error", args, err)
		}
	}
}

// A spawn that names its harness keeps its profile exactly as given,
// whatever the table says.
func TestAnExplicitProfileIsKept(t *testing.T) {
	want := spawn.SpawnCrewRequest{Harness: claude.KindClaude, Model: "haiku", Effort: harness.EffortLow}
	for _, table := range []string{sampleTable, ""} {
		got, note, err := applyDispatch(dispatchWorkspace(t, table), want)
		if err != nil || note != "" || got != want {
			t.Fatalf("table %q: got %+v note %q err %v, want the request unchanged", table, got, note, err)
		}
	}
}

// A spawn with no --harness runs the table's default profile on the
// workspace's default harness (codex unless the captain changed it), and
// says so; without a file of its own the workspace uses the built-in table.
func TestNoHarnessTakesTheTablesDefault(t *testing.T) {
	for _, tc := range []struct {
		name, table string
		want        spawn.SpawnCrewRequest
		source      string
	}{
		{"built-in", "", spawn.SpawnCrewRequest{Harness: codex.KindCodex, Model: "gpt-6-luna", Effort: harness.EffortHigh}, "built-in"},
		{"workspace", sampleTable, spawn.SpawnCrewRequest{Harness: codex.KindCodex, Effort: harness.EffortMedium}, dispatch.FileName},
	} {
		got, note, err := applyDispatch(dispatchWorkspace(t, tc.table), spawn.SpawnCrewRequest{})
		if err != nil || got != tc.want {
			t.Fatalf("%s: got %+v err %v, want %+v", tc.name, got, err, tc.want)
		}
		if !strings.Contains(note, tc.source) || !strings.Contains(note, "--harness codex") {
			t.Fatalf("%s: note %q does not name the table and the profile", tc.name, note)
		}
	}
}

// The default alternative on the workspace's own default harness wins, so a
// captain who set crew_harness: claude gets the Claude default.
func TestTheDefaultFollowsTheWorkspaceHarness(t *testing.T) {
	w := dispatchWorkspace(t, "")
	raw, err := os.ReadFile(w.WorkspaceFile())
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(raw), "crew_harness: codex", "crew_harness: claude", 1)
	if edited == string(raw) {
		t.Fatalf("workspace.yaml has no crew_harness: codex line:\n%s", raw)
	}
	if err := os.WriteFile(w.WorkspaceFile(), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := w.LoadConfig(); err != nil {
		t.Fatal(err)
	}
	got, _, err := applyDispatch(w, spawn.SpawnCrewRequest{})
	want := spawn.SpawnCrewRequest{Harness: claude.KindClaude, Model: "sonnet", Effort: harness.EffortHigh}
	if err != nil || got != want {
		t.Fatalf("got %+v err %v, want %+v", got, err, want)
	}
}

// A model name belongs to one harness, so half a profile is refused.
func TestModelOrEffortWithoutHarnessIsRefused(t *testing.T) {
	for _, req := range []spawn.SpawnCrewRequest{{Model: "opus"}, {Effort: harness.EffortHigh}} {
		_, _, err := applyDispatch(dispatchWorkspace(t, ""), req)
		var ue *usageError
		if !errors.As(err, &ue) || !strings.Contains(err.Error(), "--harness") {
			t.Fatalf("%+v: err = %v, want a usage error naming --harness", req, err)
		}
	}
}

// A malformed table is reported and corrected, never launched around, and
// never quietly replaced by the built-in one.
func TestAMalformedDispatchTableStopsEverySpawn(t *testing.T) {
	w := dispatchWorkspace(t, `{"rules": [{"when": "x", "use": {"harness": "codex", "effort": "max"}}]}`)
	_, _, err := applyDispatch(w, spawn.SpawnCrewRequest{Harness: codex.KindCodex})
	var ue *usageError
	if !errors.Is(err, dispatch.ErrInvalid) || !errors.As(err, &ue) {
		t.Fatalf("err = %v, want the table's own refusal, exit 2", err)
	}
}

// `mate crew dispatch` is how the Mate reads the table: every rule in
// order, each profile as the exact flags to pass.
func TestCrewDispatchPrintsTheTableAsFlags(t *testing.T) {
	w := dispatchWorkspace(t, sampleTable)
	var out, errw bytes.Buffer
	if err := testCLI().run([]string{"crew", "dispatch", "--workspace", w.Root()}, &out, &errw); err != nil {
		t.Fatalf("crew dispatch: %v\n%s", err, errw.String())
	}
	got := out.String()
	for _, want := range []string{
		"rule 1: a trivial mechanical edit",
		"use --harness claude --model haiku --effort low",
		"why cheap",
		"rule 2: a big or ambiguous feature",
		"or  --harness codex --model gpt-5.5 --effort xhigh",
		"default: --harness codex --effort medium",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "review") {
		t.Errorf("a table with no review profile printed one:\n%s", got)
	}
}

// Without a file of its own the workspace shows the built-in table, says
// how to replace it, and --example prints that table as JSON that loads.
func TestCrewDispatchWithoutATableShowsTheBuiltIn(t *testing.T) {
	w := dispatchWorkspace(t, "")
	var out, errw bytes.Buffer
	if err := testCLI().run([]string{"crew", "dispatch", "--workspace", w.Root()}, &out, &errw); err != nil {
		t.Fatalf("crew dispatch: %v", err)
	}
	for _, want := range []string{
		"crew dispatch: built-in",
		filepath.Join(".mate", "crew-dispatch.json"),
		"rule 1: A ship that is a small change",
		// A rule's budget (CON-76) is on each profile line, so the Mate
		// copies a whole line of flags.
		"use --harness claude --model sonnet --effort medium --budget 2000000\n",
		"or  --harness codex --model gpt-6-luna --effort medium --budget 2000000\n",
		"use --harness claude --model sonnet --effort high --budget 8000000\n",
		"use --harness claude --model opus --effort high --budget 20000000\n",
		"default: --harness codex --model gpt-6-luna --effort high\n",
		"rule 6: A review of another Crew's branch whose own task was a large change",
		"review (what `mate review` uses when you pass no --model or --effort): --harness claude --model sonnet --effort medium --budget 3000000\n",
		"  or  --harness codex --model gpt-6-luna --effort medium --budget 3000000\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	// A rule without a budget prints none: the light scout runs on the
	// project's budget, if any.
	if strings.Contains(out.String(), "use --harness claude --model sonnet --effort medium --budget 2000000\n  or  --harness codex --model gpt-6-luna --effort medium --budget 2000000\n  why A narrow question") {
		t.Errorf("the light scout printed a budget it does not have:\n%s", out.String())
	}
	out.Reset()
	if err := testCLI().run([]string{"crew", "dispatch", "--example"}, &out, &errw); err != nil {
		t.Fatalf("crew dispatch --example: %v", err)
	}
	p := filepath.Join(t.TempDir(), dispatch.FileName)
	if err := os.WriteFile(p, out.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := dispatch.Load(p, harnesses); err != nil || !ok {
		t.Fatalf("the example does not load: %v\n%s", err, out.String())
	}
}

func TestHarnessCellNamesEachAxisThatWasSet(t *testing.T) {
	for _, tc := range []struct {
		c    spawn.CrewSummary
		want string
	}{
		{spawn.CrewSummary{Harness: "codex"}, "codex"},
		{spawn.CrewSummary{Harness: "codex", Model: "gpt-5.5", Effort: "high"}, "codex gpt-5.5/high"},
		{spawn.CrewSummary{Harness: "claude", Model: "haiku"}, "claude haiku"},
		{spawn.CrewSummary{Harness: "codex", Effort: "medium"}, "codex default/medium"},
	} {
		if got := harnessCell(tc.c); got != tc.want {
			t.Errorf("harnessCell(%+v) = %q, want %q", tc.c, got, tc.want)
		}
	}
}

// withQuota is the test CLI with a quota-axi that reads snap and err.
func withQuota(snap quota.Snapshot, err error) cli {
	c := testCLI()
	c.readQuota = func(context.Context) (quota.Snapshot, error) { return snap, err }
	return c
}

func sampleQuota() quota.Snapshot {
	sp := 1.5
	return quota.Snapshot{Version: "0.1.54", Read: time.Date(2026, 9, 26, 8, 0, 0, 0, time.UTC), Readings: []quota.Reading{
		{Harness: codex.KindCodex, Provider: "codex", Known: true, PercentLeft: 93, SpendPriority: &sp, Runway: quota.RunwayThroughReset, Status: "fresh"},
		{Harness: claude.KindClaude, Provider: "claude", PercentLeft: -1, Runway: quota.RunwayUnknown, Status: "auth_required", Remedy: "quota-axi --allow-keychain-prompt"},
	}}
}

// `crew dispatch` ends with the quota evidence the skill ranks alternatives
// by, and which harness it favours.
func TestCrewDispatchShowsQuota(t *testing.T) {
	c := withQuota(sampleQuota(), nil)
	w := dispatchWorkspace(t, "")
	var out, errw bytes.Buffer
	if err := c.run([]string{"crew", "dispatch", "--workspace", w.Root()}, &out, &errw); err != nil {
		t.Fatalf("crew dispatch: %v", err)
	}
	for _, want := range []string{
		"quota (quota-axi 0.1.54, read-only, 2026-09-26 08:00 UTC):",
		"  codex   93% left · spendPriority 1.5 · runway through_reset",
		"  claude  unknown (auth_required); the captain can run `quota-axi --allow-keychain-prompt`",
		"  favours codex: highest spendPriority 1.5 among eligible harnesses",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

// Without quota-axi the table still prints, with one line saying so.
func TestCrewDispatchWithoutQuotaAxiSaysSo(t *testing.T) {
	w := dispatchWorkspace(t, "")
	var out, errw bytes.Buffer
	if err := testCLI().run([]string{"crew", "dispatch", "--workspace", w.Root()}, &out, &errw); err != nil {
		t.Fatalf("crew dispatch: %v", err)
	}
	if !strings.Contains(out.String(), "quota: quota-axi is not installed") || !strings.Contains(out.String(), "rule 1:") {
		t.Fatalf("output = %s", out.String())
	}
	broken := withQuota(quota.Snapshot{}, errors.New("quota-axi --json: exit status 2"))
	out.Reset()
	if err := broken.run([]string{"crew", "dispatch", "--workspace", w.Root()}, &out, &errw); err != nil {
		t.Fatalf("crew dispatch with a broken quota-axi failed: %v", err)
	}
	if !strings.Contains(out.String(), "quota: unreadable (quota-axi --json: exit status 2)") {
		t.Fatalf("output = %s", out.String())
	}
}

// A spawn onto a used-up harness is warned about, never refused: the
// captain's words may have chosen it.
func TestQuotaWarningNamesAUsedUpHarness(t *testing.T) {
	snap := sampleQuota()
	snap.Readings[0].Runway = quota.RunwayExhausted
	c := withQuota(snap, nil)
	if w := c.quotaWarning(codex.KindCodex); !strings.Contains(w, "codex quota is used up") {
		t.Fatalf("warning = %q", w)
	}
	if w := c.quotaWarning(claude.KindClaude); w != "" {
		t.Fatalf("an unmeasured harness was warned about: %q", w)
	}
}

// A review that names no model or effort takes the table's review profile,
// not the default a ship gets: on the workspace's default harness, or on
// the harness the Mate named (CON-68). One that names either keeps it; a
// table without a review profile leaves the request alone.
func TestAReviewWithoutAProfileTakesTheReviewProfile(t *testing.T) {
	for _, tc := range []struct {
		name, table string
		req, want   spawn.SpawnCrewRequest
		noted       bool
	}{
		// The built-in table's review_budget (3M, CON-76) rides along with
		// its profile, and with a profile the Mate named too.
		{"default harness", "", spawn.SpawnCrewRequest{},
			spawn.SpawnCrewRequest{Harness: codex.KindCodex, Model: "gpt-6-luna", Effort: harness.EffortMedium, Budget: 3_000_000}, true},
		{"named harness", "", spawn.SpawnCrewRequest{Harness: claude.KindClaude},
			spawn.SpawnCrewRequest{Harness: claude.KindClaude, Model: "sonnet", Effort: harness.EffortMedium, Budget: 3_000_000}, true},
		{"explicit large-change reviewer", "", spawn.SpawnCrewRequest{Harness: claude.KindClaude, Model: "opus", Effort: harness.EffortMedium},
			spawn.SpawnCrewRequest{Harness: claude.KindClaude, Model: "opus", Effort: harness.EffortMedium, Budget: 3_000_000}, false},
		{"named budget kept", "", spawn.SpawnCrewRequest{Budget: 1_000_000},
			spawn.SpawnCrewRequest{Harness: codex.KindCodex, Model: "gpt-6-luna", Effort: harness.EffortMedium, Budget: 1_000_000}, true},
		{"no review profile", sampleTable, spawn.SpawnCrewRequest{},
			spawn.SpawnCrewRequest{}, false},
		{"no review profile for the default harness", claudeReviewTable, spawn.SpawnCrewRequest{},
			spawn.SpawnCrewRequest{Harness: claude.KindClaude, Model: "sonnet", Effort: harness.EffortMedium}, true},
	} {
		got, note, err := applyReviewDispatch(dispatchWorkspace(t, tc.table), tc.req, nil)
		if err != nil || got != tc.want {
			t.Fatalf("%s: got %+v err %v, want %+v", tc.name, got, err, tc.want)
		}
		if (note != "") != tc.noted || (tc.noted && !strings.Contains(note, "review profile")) {
			t.Fatalf("%s: note %q", tc.name, note)
		}
		// A reviewer moved off the default harness says so.
		if tc.table == claudeReviewTable && !strings.Contains(note, "no review profile for the default harness codex; used --harness claude") {
			t.Fatalf("%s: note %q does not say the harness changed", tc.name, note)
		}
	}
	_, _, err := applyReviewDispatch(dispatchWorkspace(t, `{"rules": [{"when": "x", "use": {"harness": "codex", "effort": "max"}}]}`), spawn.SpawnCrewRequest{Model: "opus"}, nil)
	var ue *usageError
	if !errors.Is(err, dispatch.ErrInvalid) || !errors.As(err, &ue) {
		t.Fatalf("malformed table: err = %v, want the table's own refusal, exit 2", err)
	}
}

// rotationTable is a table with the captain's rotation (CON-77): three
// turns of claude, then codex, then pi; rule 1 has a rotation of its own.
const rotationTable = `{
  "rules": [
    {"when": "a small change", "use": {"harness": "claude", "model": "sonnet"}, "rotate": ["claude", "codex"]},
    {"when": "anything else", "use": {"harness": "codex"}}
  ],
  "default": {"harness": "codex"},
  "rotate": ["claude", "claude", "claude", "codex", "pi"]
}`

// rotationWorkspace is dispatchWorkspace with a project "shop" whose crews
// carry the given rotation slots (a closed one among them, since the
// rotation counts on after `crew stop`).
func rotationWorkspace(t *testing.T, table string, slots ...int) *store.Workspace {
	t.Helper()
	w := dispatchWorkspace(t, table)
	repo := filepath.Join(w.ProjectHome("shop"), "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)
	if err := w.AddProject("shop", store.ProjectConfig{Repos: []store.RepoConfig{{Path: repo, DefaultBranch: "main"}}}); err != nil {
		t.Fatal(err)
	}
	for i, slot := range slots {
		id := "k" + strconv.Itoa(i+1)
		meta := map[string]string{"harness": "claude", "branch": "mate/" + id, "task": "t", spawn.MetaRotationSlot: strconv.Itoa(slot)}
		if i == 0 {
			meta["state"] = "finished"
		}
		if err := w.WriteCrewMeta("shop", id, meta); err != nil {
			t.Fatal(err)
		}
	}
	// A crew with no slot: a spawn the captain steered off the rotation.
	if err := w.WriteCrewMeta("shop", "steered", map[string]string{"harness": "pi", "branch": "mate/steered", "task": "t"}); err != nil {
		t.Fatal(err)
	}
	return w
}

// `crew dispatch <project>` prints each rotation in force and the harness
// it names next, read from the slots the project's spawns recorded; `--skip`
// passes over a harness and says so.
func TestCrewDispatchPrintsTheNextInRotation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		slots []int
		args  []string
		want  []string
	}{
		{"no prior slot", nil, nil, []string{
			"  rotation: claude, codex\n  next in rotation: claude (position 1/2)\n",
			"\nrotation: claude, claude, claude, codex, pi\nnext in rotation: claude (position 1/5)\n",
		}},
		{"one prior slot", []int{1}, nil, []string{
			"  next in rotation: codex (position 2/2)\n",
			"\nrotation: claude, claude, claude, codex, pi\nnext in rotation: claude (position 2/5)\n",
		}},
		{"four prior slots", []int{1, 2, 3, 4}, nil, []string{
			"  next in rotation: claude (position 1/2)\n",
			"\nrotation: claude, claude, claude, codex, pi\nnext in rotation: pi (position 5/5)\n",
		}},
		{"skip", []int{1, 2, 3}, []string{"--skip", "codex"}, []string{
			"  rotation: claude, codex\n  skipped codex: --skip\n  next in rotation: claude (position 1/2)\n",
			"\nrotation: claude, claude, claude, codex, pi\nskipped codex: --skip\nnext in rotation: pi (position 5/5)\n",
		}},
		{"everything skipped", []int{1}, []string{"--skip", "codex", "--skip", "claude", "--skip", "pi"}, []string{
			"\nrotation: claude, claude, claude, codex, pi\nskipped claude: --skip\nskipped codex: --skip\nskipped pi: --skip\nnext in rotation: none (every harness in the rotation is skipped)\n",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := rotationWorkspace(t, rotationTable, tc.slots...)
			var out, errw bytes.Buffer
			args := append([]string{"crew", "dispatch", "shop", "--workspace", w.Root()}, tc.args...)
			if err := testCLI().run(args, &out, &errw); err != nil {
				t.Fatalf("crew dispatch: %v\n%s", err, errw.String())
			}
			for _, want := range tc.want {
				if !strings.Contains(out.String(), want) {
					t.Errorf("output lacks %q:\n%s", want, out.String())
				}
			}
			// The rotation lines come after the rules and profiles, before quota.
			if i, j := strings.Index(out.String(), "\nrotation: claude, claude"), strings.Index(out.String(), "\nquota"); i < 0 || j < i {
				t.Errorf("the table rotation is not between the profiles and the quota block:\n%s", out.String())
			}
		})
	}

	// Without a project there is no position to read, and the table still prints.
	w := rotationWorkspace(t, rotationTable, 1, 2)
	var out, errw bytes.Buffer
	if err := testCLI().run([]string{"crew", "dispatch", "--workspace", w.Root()}, &out, &errw); err != nil {
		t.Fatalf("crew dispatch without a project: %v", err)
	}
	if !strings.Contains(out.String(), "rotation: claude, claude, claude, codex, pi\nnext in rotation: unknown; pass the project (mate crew dispatch <project>)") || !strings.Contains(out.String(), "rule 2:") {
		t.Fatalf("output = %s", out.String())
	}

	// A table without a rotation prints no rotation line; an unknown
	// --skip or an unknown project is refused.
	out.Reset()
	if err := testCLI().run([]string{"crew", "dispatch", "shop", "--workspace", rotationWorkspace(t, "").Root()}, &out, &errw); err != nil || strings.Contains(out.String(), "rotation") {
		t.Fatalf("built-in table: err %v, output = %s", err, out.String())
	}
	var ue *usageError
	if err := testCLI().run([]string{"crew", "dispatch", "shop", "--skip", "gemini", "--workspace", w.Root()}, &out, &errw); !errors.As(err, &ue) {
		t.Fatalf("--skip gemini: err = %v, want a usage error", err)
	}
	if err := testCLI().run([]string{"crew", "dispatch", "nope", "--workspace", w.Root()}, &out, &errw); err == nil {
		t.Fatal("an unknown project was accepted")
	}
}

// A spawn takes the rotation's turn when its harness is the one named
// next, recording the slot after the ones taken (through the skipped
// turns with --skip); any other harness takes no turn and the note says
// so. A table without a rotation has nothing to record.
func TestApplyRotationConsumesOnlyTheNextHarness(t *testing.T) {
	for _, tc := range []struct {
		name     string
		slots    []int
		kind     harness.Kind
		skip     []harness.Kind
		wantSlot int
		wantNote string
	}{
		{"first turn", nil, claude.KindClaude, nil, 1, ""},
		{"fourth turn is codex", []int{1, 2, 3}, codex.KindCodex, nil, 4, ""},
		{"the rule's rotation counts too", []int{1}, codex.KindCodex, nil, 2, ""},
		{"captain's words win", []int{1, 2, 3}, "pi", nil, 0, "rotation not advanced: harness pi is not the rotation's next, codex is next (position 2/2); the captain's words win"},
		{"skip passes over codex", []int{1, 2, 3}, "pi", []harness.Kind{codex.KindCodex}, 5, ""},
		{"everything skipped", []int{1}, "pi", []harness.Kind{claude.KindClaude, codex.KindCodex, "pi"}, 0, "rotation not advanced: harness pi is not the rotation's next, every harness in it is skipped; the captain's words win"},
		{"no harness takes the default", []int{1, 2, 3}, "", nil, 4, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := rotationWorkspace(t, rotationTable, tc.slots...)
			slot, note, err := applyRotation(w, "shop", tc.kind, tc.skip)
			if err != nil || slot != tc.wantSlot || note != tc.wantNote {
				t.Fatalf("applyRotation = %d %q %v; want %d %q", slot, note, err, tc.wantSlot, tc.wantNote)
			}
		})
	}
	slot, note, err := applyRotation(rotationWorkspace(t, "", 1), "shop", "pi", nil)
	if err != nil || slot != 0 || note != "" {
		t.Fatalf("built-in table: %d %q %v; want nothing", slot, note, err)
	}
}

// A review of a small change - at most the table's review_small_max_lines
// changed lines, no code file - takes the table's review_small profile
// (CON-74); a change with a code file in it, or over the threshold, or
// one that was not measured, takes the review profile; the Mate's own
// --model/--effort win over both; a table with no review_small reviews
// every change on review.
func TestASmallChangeTakesTheReviewSmallProfile(t *testing.T) {
	docs := &reviewStat{Lines: 12, Files: 2}
	code := &reviewStat{Lines: 12, Files: 2, Code: []string{"cmd/mate/main.go"}}
	big := &reviewStat{Lines: 51, Files: 1}
	small := spawn.SpawnCrewRequest{Harness: codex.KindCodex, Model: "gpt-6-luna", Effort: harness.EffortLow, Budget: 3_000_000}
	full := spawn.SpawnCrewRequest{Harness: codex.KindCodex, Model: "gpt-6-luna", Effort: harness.EffortMedium, Budget: 3_000_000}
	for _, tc := range []struct {
		name, table string
		req         spawn.SpawnCrewRequest
		stat        *reviewStat
		want        spawn.SpawnCrewRequest
		note        string
	}{
		{"docs under the threshold", "", spawn.SpawnCrewRequest{}, docs, small, "the change is small (12 line(s) in 2 file(s), no code, at most 50 lines), so used the built-in dispatch table's review_small profile: --harness codex --model gpt-6-luna --effort low"},
		{"code under the threshold", "", spawn.SpawnCrewRequest{}, code, full, "used the built-in dispatch table's review profile"},
		{"docs over the threshold", "", spawn.SpawnCrewRequest{}, big, full, "used the built-in dispatch table's review profile"},
		{"not measured", "", spawn.SpawnCrewRequest{}, nil, full, "used the built-in dispatch table's review profile"},
		{"named harness", "", spawn.SpawnCrewRequest{Harness: claude.KindClaude}, docs,
			spawn.SpawnCrewRequest{Harness: claude.KindClaude, Model: "haiku", Effort: harness.EffortLow, Budget: 3_000_000}, "review_small profile: --harness claude --model haiku --effort low"},
		{"the Mate's own profile wins", "", spawn.SpawnCrewRequest{Harness: claude.KindClaude, Model: "opus", Effort: harness.EffortHigh}, docs,
			spawn.SpawnCrewRequest{Harness: claude.KindClaude, Model: "opus", Effort: harness.EffortHigh, Budget: 3_000_000}, ""},
		{"no review_small in the table", claudeReviewTable, spawn.SpawnCrewRequest{}, docs,
			spawn.SpawnCrewRequest{Harness: claude.KindClaude, Model: "sonnet", Effort: harness.EffortMedium}, "no review profile for the default harness codex; used --harness claude"},
	} {
		got, note, err := applyReviewDispatch(dispatchWorkspace(t, tc.table), tc.req, tc.stat)
		if err != nil || got != tc.want {
			t.Fatalf("%s: got %+v err %v, want %+v", tc.name, got, err, tc.want)
		}
		if !strings.Contains(note, tc.note) {
			t.Fatalf("%s: note %q lacks %q", tc.name, note, tc.note)
		}
	}
}

// `crew dispatch` prints the review_small profile and its threshold next
// to the review profile, so the Mate sees both.
func TestCrewDispatchPrintsTheReviewSmallProfile(t *testing.T) {
	w := dispatchWorkspace(t, "")
	var out, errw bytes.Buffer
	if err := testCLI().run([]string{"crew", "dispatch", "--workspace", w.Root()}, &out, &errw); err != nil {
		t.Fatalf("crew dispatch: %v", err)
	}
	want := "review (what `mate review` uses when you pass no --model or --effort): --harness claude --model sonnet --effort medium --budget 3000000\n" +
		"  or  --harness codex --model gpt-6-luna --effort medium --budget 3000000\n\n" +
		"review_small (what `mate review` uses instead when the change is at most 50 lines and touches no code file): --harness claude --model haiku --effort low --budget 3000000\n" +
		"  or  --harness codex --model gpt-6-luna --effort low --budget 3000000\n"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("output lacks %q:\n%s", want, out.String())
	}
}

// `crew dispatch` reports every lane some repo declares: who holds it
// (crews spawned or working, not those waiting), its cap from
// workspace.yaml `lanes:` or the default, and what to do when it is full
// (CON-118). A workspace with no lane prints no lanes block.
func TestCrewDispatchReportsLanes(t *testing.T) {
	w := wakeFixture(t)
	var out, errw bytes.Buffer
	if err := testCLI().run([]string{"crew", "dispatch", "--workspace", w.Root()}, &out, &errw); err != nil {
		t.Fatalf("crew dispatch: %v\n%s", err, errw.String())
	}
	if strings.Contains(out.String(), "lanes") {
		t.Fatalf("a workspace with no lane printed a lanes block:\n%s", out.String())
	}

	cfg, err := w.LoadProject("shop")
	if err != nil {
		t.Fatal(err)
	}
	if code, _, _ := runMate(t, "project", "repo", "lane", "shop", cfg.Repos[0].Name, "xcode", "--workspace", w.Root()); code != 0 {
		t.Fatal("lane not set")
	}
	// The fixture: rev-k3 and busy-k5 are working; k3 is wait-mate;
	// done-old is finished. A crew spawned with --lane none holds nothing.
	// Two hold the lane, cap two.
	if err := w.WriteCrewMeta("shop", "api-1", map[string]string{spawn.MetaHarness: "codex", spawn.MetaState: "working", spawn.MetaTask: "api", spawn.MetaLane: ""}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := testCLI().run([]string{"crew", "dispatch", "--workspace", w.Root()}, &out, &errw); err != nil {
		t.Fatalf("crew dispatch: %v\n%s", err, errw.String())
	}
	for _, want := range []string{
		"lanes (a crew holds its repo's lane while spawned or working",
		"xcode: 2/2 busy - shop/busy-k5 (working), shop/rev-k3 (working); full: queue the next xcode task with `blocked-by: lane xcode`",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	for _, bad := range []string{"shop/k3", "done-old", "api-1"} {
		if strings.Contains(out.String(), bad) {
			t.Errorf("a crew that holds no lane is listed (%q):\n%s", bad, out.String())
		}
	}

	// workspace.yaml resizes the lane.
	path := filepath.Join(w.StateDir(), "workspace.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, []byte("lanes:\n  xcode: 4\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := testCLI().run([]string{"crew", "dispatch", "--workspace", w.Root()}, &out, &errw); err != nil {
		t.Fatalf("crew dispatch: %v\n%s", err, errw.String())
	}
	if !strings.Contains(out.String(), "xcode: 2/4 busy") || !strings.Contains(out.String(), "; 2 free") {
		t.Errorf("cap from workspace.yaml not applied:\n%s", out.String())
	}
}

// A spawn into a full lane is refused with exit 2 and the line the Mate
// acts on; --lane none and a lane with room pass (CON-118).
func TestLaneGuardRefusesAFullLane(t *testing.T) {
	w := wakeFixture(t)
	req := spawn.SpawnCrewRequest{Project: "shop", Crew: "ui-9"}
	if err := laneGuard(w, req); err != nil {
		t.Fatalf("no lane declared: %v", err)
	}
	cfg, err := w.LoadProject("shop")
	if err != nil {
		t.Fatal(err)
	}
	if code, _, _ := runMate(t, "project", "repo", "lane", "shop", cfg.Repos[0].Name, "xcode", "--workspace", w.Root()); code != 0 {
		t.Fatal("lane not set")
	}
	err = laneGuard(w, req)
	var ue *usageError
	if err == nil || !errors.As(err, &ue) || !strings.Contains(err.Error(), "lane xcode is full (2/2: shop/busy-k5 (working), shop/rev-k3 (working)); queue the task with `blocked-by: lane xcode`") {
		t.Fatalf("full lane: err = %v, want a usage error naming the holders", err)
	}
	req.Lane = "none"
	if err := laneGuard(w, req); err != nil {
		t.Fatalf("--lane none: %v", err)
	}
	req.Lane = ""
	path := filepath.Join(w.StateDir(), "workspace.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, []byte("lanes:\n  xcode: 3\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	w2, err := store.Open(w.Root())
	if err != nil {
		t.Fatal(err)
	}
	if err := laneGuard(w2, req); err != nil {
		t.Fatalf("lane with room: %v", err)
	}
}
