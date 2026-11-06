package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/box"
	"github.com/nguyenngocanh94/mate/internal/store"
)

func budgetWorkspace(t *testing.T) *store.Workspace {
	t.Helper()
	w := liveCrewWorkspace(t, "shop")
	if err := w.WriteCrewMeta("shop", "k3", map[string]string{
		"harness": "codex", "task": "add a Buy button", "state": "spawned", "agent": "crew-k3", "pane": "w1:p2",
		store.MetaBudget: "2000000", store.MetaBudgetLine: "2026-10-16T09:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{box.BudgetHalfPrefix + "1.1M tokens of 2M tokens, no hand-back yet", "2.1M tokens of 2M tokens"} {
		if err := w.AppendIncident("shop", store.IncidentEntry{Crew: "k3", Kind: string(box.IncidentBudget), State: store.IncidentOpen, Text: text}); err != nil {
			t.Fatal(err)
		}
	}
	return w
}

func runBudget(w *store.Workspace, args ...string) (string, string, error) {
	var stdout, stderr bytes.Buffer
	err := testCLI().run(append([]string{"crew", "budget", "shop", "k3", "--workspace", w.Root()}, args...), &stdout, &stderr)
	return stdout.String(), stderr.String(), err
}

// The first extension is the Mate's (CON-94): it records the new ceiling
// and the reason, re-arms the daemon's line, and answers the open budget
// items. The second is the captain's: refused without --captain.
func TestCrewBudgetExtendsOnceThenAsksForTheCaptain(t *testing.T) {
	w := budgetWorkspace(t)
	out, stderr, err := runBudget(w, "--extend", "3000000", "--reason", "tests left")
	if err != nil {
		t.Fatalf("first extension: %v\n%s", err, stderr)
	}
	if !strings.Contains(out, "budget 2M -> 3M tokens (extension 1: tests left)") {
		t.Fatalf("report = %q", out)
	}
	meta, err := w.ReadCrewMeta("shop", "k3")
	if err != nil {
		t.Fatal(err)
	}
	if meta[store.MetaBudget] != "3000000" || meta[store.MetaBudgetExtensions] != "1" ||
		meta[store.MetaBudgetReason] != "tests left" || meta[store.MetaBudgetOriginal] != "2000000" {
		t.Fatalf("meta = %v, want the new ceiling, one extension, its reason and the original", meta)
	}
	if _, ok := meta[store.MetaBudgetLine]; ok {
		t.Fatalf("budget_line= kept: %v", meta)
	}
	view, err := box.Load(w, "shop")
	if err != nil {
		t.Fatal(err)
	}
	if open := box.OpenIncidents(view, "k3"); len(open) != 0 {
		t.Fatalf("open incidents = %+v, want both budget items answered", open)
	}

	if _, stderr, err := runBudget(w, "--extend", "4000000", "--reason", "more tests"); err == nil || !strings.Contains(err.Error()+stderr, "decides: captain") {
		t.Fatalf("second extension without --captain: err %v, stderr %q; want a refusal naming decides: captain", err, stderr)
	}
	if _, stderr, err := runBudget(w, "--extend", "4000000", "--reason", "captain agreed", "--captain"); err != nil {
		t.Fatalf("second extension with --captain: %v\n%s", err, stderr)
	}
}

// --extend is the new ceiling: one that is not above the current one is
// refused and changes nothing.
func TestCrewBudgetRefusesACeilingThatIsNotHigher(t *testing.T) {
	w := budgetWorkspace(t)
	if _, _, err := runBudget(w, "--extend", "500000", "--reason", "x"); err == nil {
		t.Fatal("an extension below the current ceiling was accepted")
	}
	meta, err := w.ReadCrewMeta("shop", "k3")
	if err != nil {
		t.Fatal(err)
	}
	if meta[store.MetaBudget] != "2000000" || meta[store.MetaBudgetExtensions] != "" {
		t.Fatalf("meta changed on a refusal: %v", meta)
	}
}
