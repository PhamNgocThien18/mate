package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/crewstate"
	"github.com/nguyenngocanh94/mate/internal/plugin"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/usage"
)

// landWorkspace is mergeCLIWorkspace with what a hand-back turn has on
// file: the crew's task named by the tracker's id, its hand-back with a
// Learning: line and a screenshot, a backlog entry under In flight, and the
// record the app opened with `Learning: pending`.
func landWorkspace(t *testing.T) (*store.Workspace, string, string) {
	t.Helper()
	w, root := mergeCLIWorkspace(t)
	if err := w.UpdateCrewMeta("shop", "k3", map[string]string{spawn.MetaTask: "SH-12: add a feature"}); err != nil {
		t.Fatal(err)
	}
	shot := filepath.Join(root, "shot.png")
	if err := os.WriteFile(shot, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	handback := "Part of SH-12\n\n## For the next crew\n- The feature lives in feature.txt.\n- Learning: captured (feature.txt @abc).\n- Screenshot: " + shot + " and /nowhere/missing.png\n\n| criterion | pass/fail | command | output |\n| --- | --- | --- | --- |\n| feature exists | pass | `cat feature.txt` | feature |\n\n## Deviations from Build\nNone.\n\n## Still open\nNone.\n"
	if err := os.MkdirAll(w.CrewDir("shop", "k3"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.CrewHandback("shop", "k3"), []byte(handback), 0o644); err != nil {
		t.Fatal(err)
	}
	backlog := "## In flight\n- k3: shop PR: add a feature\n\n## Queued\n- k4: the next feature\n\n## Done\n"
	if err := os.WriteFile(w.BacklogFile("shop"), []byte(backlog), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := usage.UpsertLearningSection(w.CrewLearning("shop", "k3"), "k3", usage.LearningHeading, "tokens: 1 total\n"); err != nil {
		t.Fatal(err)
	}
	return w, root, shot
}

func TestCmdLandMergesAndRecordsEverythingInOrder(t *testing.T) {
	w, root, shot := landWorkspace(t)
	fake := &process.FakeRunner{}
	fake.Responses = map[string]process.Result{"land-sh SH-12": {ExitCode: 0, Stdout: []byte("SH-12 → Done, 1 comment, 1 attachment\n")}}
	c := testCLI()
	c.tracker = fake
	writePlugins(t, w, "linear", "tracker:\n      land: [land-sh, \"{id}\"]\n")

	var out, errw bytes.Buffer
	if err := c.cmdLand([]string{"--workspace", root, "shop", "k3"}, &out, &errw); err != nil {
		t.Fatalf("land: %v\nstderr: %s\nstdout: %s", err, errw.String(), out.String())
	}
	text := out.String()
	lines := strings.Split(strings.TrimSpace(text), "\n")
	wantOrder := []string{
		"shop/k3: merged 1 commit(s) into main (",
		"learning: usage ",
		"learning: " + w.CrewLearning("shop", "k3") + " written from the hand-back (Learning: captured (feature.txt @abc))",
		"backlog: k3 → Done",
		"tracker: SH-12 SH-12 → Done, 1 comment, 1 attachment (linear)",
		"next: no crew open · queued: 1 (first: k4: the next feature) · next work: pick",
	}
	at := 0
	for _, want := range wantOrder {
		found := false
		for ; at < len(lines); at++ {
			if strings.Contains(lines[at], want) {
				found = true
				at++
				break
			}
		}
		if !found {
			t.Fatalf("land output lacks %q in order:\n%s", want, text)
		}
	}
	// The record keeps the app's section and opens with the hand-back's line.
	record, err := os.ReadFile(w.CrewLearning("shop", "k3"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# k3 learning", "Source: SH-12: add a feature; branch mate/k3 merged as ", "Learning: captured (feature.txt @abc)", "## Predictions\nnone", usage.LearningHeading + "\n", "measured at mate land"} {
		if !strings.Contains(string(record), want) {
			t.Fatalf("record lacks %q:\n%s", want, record)
		}
	}
	if strings.Contains(string(record), landLearningPending) {
		t.Fatalf("record still pending:\n%s", record)
	}
	// Backlog moved the entry, and the tracker command got the facts.
	backlog, _ := os.ReadFile(w.BacklogFile("shop"))
	if !strings.Contains(string(backlog), "## Done\n- k3:") || strings.Contains(string(backlog), "## In flight\n- k3") {
		t.Fatalf("backlog entry not moved to Done:\n%s", backlog)
	}
	if len(fake.Calls) != 1 {
		t.Fatalf("tracker command ran %d times, want 1", len(fake.Calls))
	}
	call := fake.Calls[0]
	if call.Name != "land-sh" || len(call.Args) != 1 || call.Args[0] != "SH-12" {
		t.Fatalf("tracker command was %s %v, want land-sh [SH-12]", call.Name, call.Args)
	}
	env := strings.Join(call.Env, "\n")
	for _, want := range []string{"MATE_TRACKER_ID=SH-12", "MATE_CREW=k3", "MATE_PROJECT=shop", "MATE_HANDBACK=" + w.CrewHandback("shop", "k3"), "MATE_SHOTS=" + shot + "\n", "MATE_MERGE_COMMIT="} {
		if !strings.Contains(env+"\n", want) {
			t.Fatalf("tracker env lacks %q:\n%s", want, env)
		}
	}
	if strings.Contains(env, "/nowhere/missing.png") {
		t.Fatalf("a screenshot that does not exist was passed:\n%s", env)
	}
	if meta, _ := w.ReadCrewMeta("shop", "k3"); meta[spawn.MetaState] != spawn.CrewStateFinished {
		t.Fatalf("crew state %q, want finished", meta[spawn.MetaState])
	}
}

func TestCmdLandRefusesLikeMergeAndRecordsNothing(t *testing.T) {
	w, root, _ := landWorkspace(t)
	var out, errw bytes.Buffer
	err := testCLI().withEnv("MATE_CALLER", "crew").cmdLand([]string{"--workspace", root, "shop", "k3"}, &out, &errw)
	if err == nil || !strings.Contains(err.Error(), "merge refused:") {
		t.Fatalf("land by a crew: err %v, want a merge refusal", err)
	}
	if out.Len() != 0 {
		t.Fatalf("a refused land printed:\n%s", out.String())
	}
	backlog, _ := os.ReadFile(w.BacklogFile("shop"))
	if !strings.Contains(string(backlog), "## In flight\n- k3") {
		t.Fatalf("a refused land moved the backlog entry:\n%s", backlog)
	}
	record, _ := os.ReadFile(w.CrewLearning("shop", "k3"))
	if !strings.Contains(string(record), landLearningPending) {
		t.Fatalf("a refused land wrote the record:\n%s", record)
	}
}

func TestCmdLandLeavesTheTrackerToTheMateWithoutALandCommand(t *testing.T) {
	w, root, _ := landWorkspace(t)
	writePlugins(t, w, "linear", "tracker:\n      read: [read-sh]\n")
	var out, errw bytes.Buffer
	if err := testCLI().cmdLand([]string{"--workspace", root, "shop", "k3"}, &out, &errw); err != nil {
		t.Fatalf("land: %v\nstderr: %s", err, errw.String())
	}
	if !strings.Contains(out.String(), "tracker: SH-12 not updated ("+plugin.ConfigFile+" has no tracker.land command for linear); mark it done yourself") {
		t.Fatalf("land did not hand the tracker back to the Mate:\n%s", out.String())
	}
}

func TestCmdLandKeepsARecordTheMateWrote(t *testing.T) {
	w, root, _ := landWorkspace(t)
	own := "# k3 learning\nSource: the Mate wrote this.\nLearning: follow-up\n- a real finding\n\n## Predictions\n1. a claim; baseline x; next check y — outcome: open\n\n" + usage.LearningHeading + "\ntokens: 1 total\n"
	if err := os.WriteFile(w.CrewLearning("shop", "k3"), []byte(own), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if err := testCLI().cmdLand([]string{"--workspace", root, "shop", "k3"}, &out, &errw); err != nil {
		t.Fatalf("land: %v\nstderr: %s", err, errw.String())
	}
	if !strings.Contains(out.String(), "learning: record kept as you wrote it") {
		t.Fatalf("land did not say it kept the record:\n%s", out.String())
	}
	record, _ := os.ReadFile(w.CrewLearning("shop", "k3"))
	if !strings.Contains(string(record), "- a real finding") || !strings.Contains(string(record), "1. a claim") {
		t.Fatalf("the Mate's record was rewritten:\n%s", record)
	}
}

func TestCmdLandClosesAPullRequestCrewTheWatcherSawMerged(t *testing.T) {
	w, root, _ := landWorkspace(t)
	// Land the branch the way GitHub would, then record the watcher's facts:
	// the crew is then a `crew stop`, not a merge.
	repo := filepath.Join(w.ProjectHome("shop"), "shop")
	runGitOrFatal(t, repo, "merge", "--ff-only", "mate/k3")
	if err := w.UpdateCrewMeta("shop", "k3", map[string]string{crewstate.MetaPRState: crewstate.PRStateMerged, crewstate.MetaPRURL: "https://github.com/x/shop/pull/1", crewstate.MetaMergeCommit: "deadbeef"}); err != nil {
		t.Fatal(err)
	}
	var out, errw bytes.Buffer
	if err := testCLI().cmdLand([]string{"--workspace", root, "shop", "k3"}, &out, &errw); err != nil {
		t.Fatalf("land: %v\nstderr: %s\nstdout: %s", err, errw.String(), out.String())
	}
	if strings.Contains(out.String(), "merged 1 commit(s)") {
		t.Fatalf("a merged pull request was merged again:\n%s", out.String())
	}
	if meta, _ := w.ReadCrewMeta("shop", "k3"); meta[spawn.MetaState] != spawn.CrewStateFinished {
		t.Fatalf("crew state %q, want finished:\n%s", meta[spawn.MetaState], out.String())
	}
	if !strings.Contains(out.String(), "backlog: k3 → Done") {
		t.Fatalf("land did not go on to the records:\n%s", out.String())
	}
}

func TestCmdLandWantsTwoArguments(t *testing.T) {
	var out, errw bytes.Buffer
	err := testCLI().cmdLand([]string{"shop"}, &out, &errw)
	if err == nil || !strings.Contains(err.Error(), "want exactly 2 arguments") {
		t.Fatalf("err %v", err)
	}
}

func TestRunDispatchesLand(t *testing.T) {
	_, root := mergeCLIWorkspace(t)
	var out, errw bytes.Buffer
	if err := testCLI().run([]string{"land", "shop", "k3", "--workspace", root}, &out, &errw); err != nil {
		t.Fatalf("run land: %v\nstderr: %s", err, errw.String())
	}
	if !strings.Contains(out.String(), "shop/k3: merged") || !strings.Contains(out.String(), "next: ") {
		t.Fatalf("run land printed %q", out.String())
	}
}

// writePlugins writes a plugins.yaml with one enabled instance bound to the
// workspace, its package a directory under the workspace, plus the extra
// lines given under the instance.
func writePlugins(t *testing.T, w *store.Workspace, id, extra string) {
	t.Helper()
	pkg := filepath.Join(w.Root(), "pkg-"+id)
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	text := "apiVersion: mate.plugins/v1\ninstances:\n  " + id + ":\n    path: " + pkg + "\n    " + strings.TrimSpace(extra) + "\n"
	if err := os.WriteFile(filepath.Join(w.Root(), plugin.ConfigFile), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := plugin.ReadConfig(w.Root()); err != nil {
		t.Fatalf("plugins.yaml: %v\n%s", err, text)
	}
}

func TestCmdLandNamesTheTrackerIdEvenWithoutATrackerPlugin(t *testing.T) {
	_, root, _ := landWorkspace(t)
	var out, errw bytes.Buffer
	if err := testCLI().cmdLand([]string{"--workspace", root, "shop", "k3"}, &out, &errw); err != nil {
		t.Fatalf("land: %v\nstderr: %s", err, errw.String())
	}
	if !strings.Contains(out.String(), "tracker: SH-12 not updated (no `tracker:` block in "+plugin.ConfigFile+"); mark it done yourself") {
		t.Fatalf("land did not name the task's tracker id:\n%s", out.String())
	}
}
