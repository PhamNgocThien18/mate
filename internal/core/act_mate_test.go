package core

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/coreapi"
)

// ownActions is one request for each action the engine runs itself
// (CON-231), on shop.
var ownActions = []coreapi.ActionRequest{
	{Action: coreapi.ActionStart, Target: "shop", TargetKind: "mate"},
	{Action: coreapi.ActionResume, Target: "shop", TargetKind: "mate"},
	{Action: coreapi.ActionOnboard, Target: "shop", TargetKind: "project-mate"},
	{Action: coreapi.ActionOnboard, TargetKind: "workspace", Input: "landing"},
	{Action: coreapi.ActionStop, Target: "shop", TargetKind: "mate"},
	{Action: coreapi.ActionRepair, Target: "shop", TargetKind: "mate"},
	{Action: coreapi.ActionMode, Target: "shop", TargetKind: "project"},
	{Action: coreapi.ActionRestartMate, Target: "shop", TargetKind: "project"},
	{Action: coreapi.ActionClearComposer, Target: "shop", TargetKind: "mate"},
	{Action: coreapi.ActionRemoveProject, Target: "shop", TargetKind: "project"},
	{Action: coreapi.ActionOpenNews, Target: "shop", TargetKind: "project", Input: "7"},
	{Action: coreapi.ActionOpenWork, Target: "shop", TargetKind: "project"},
	{Action: coreapi.ActionOkayWork, Target: "shop", TargetKind: "project", Input: "3"},
}

// The Mate's and the Project's actions run in the engine and never reach
// Options.Act; a crew's stop and the other actions still do (CON-232).
func TestTheEngineRunsTheMatesAndTheProjectsActions(t *testing.T) {
	h := newHeldActs()
	e := New(Options{Workspace: shopWorkspace(t), Act: h.act})
	defer e.Stop()
	for _, req := range ownActions {
		// What each one answers over no Herdr is not the point here;
		// that it is the engine that answers is.
		_, _ = e.Act(context.Background(), req)
	}
	if got := h.count(); got != 0 {
		t.Fatalf("the engine's own actions reached Options.Act %d times: %+v", got, h.ran)
	}
	for _, req := range []coreapi.ActionRequest{
		{Action: coreapi.ActionStop, Target: "shop", TargetKind: "crew", Crew: "buy"},
		mergeCrew,
		{Action: coreapi.ActionPeek, Target: "shop", TargetKind: "crew", Crew: "buy"},
	} {
		if line, err := e.Act(context.Background(), req); err != nil || line != "did "+string(req.Action) {
			t.Errorf("%s %s = %q, %v; want Options.Act's answer", req.Action, req.TargetKind, line, err)
		}
	}
}

// An engine with no workspace refuses every action of its own, the way it
// refuses the chat, and neither panics nor runs Options.Act.
func TestTheEnginesOwnActionsNeedAWorkspace(t *testing.T) {
	h := newHeldActs()
	e := New(Options{Act: h.act})
	defer e.Stop()
	for _, req := range ownActions {
		if _, err := e.Act(context.Background(), req); !errors.Is(err, errNoWorkspace) {
			t.Errorf("%s with no workspace = %v, want %q", req.Action, err, errNoWorkspace)
		}
	}
	if got := h.count(); got != 0 {
		t.Fatalf("Options.Act ran %d times", got)
	}
}

// Over a Spawn with no Runtime, an action that needs Herdr refuses in
// spawn's words rather than panicking; a harness is parsed against
// Spawn.Harnesses, so with none every kind is refused.
func TestTheEnginesOwnActionsWithoutHerdrOrHarnesses(t *testing.T) {
	w := shopWorkspace(t)
	if err := w.WriteMateMeta("shop", map[string]string{"harness": "claude", "agent": "mate-shop", "pane": "p1"}); err != nil {
		t.Fatal(err)
	}
	e := New(Options{Workspace: w})
	defer e.Stop()
	for _, req := range []coreapi.ActionRequest{
		{Action: coreapi.ActionStart, Target: "shop", TargetKind: "mate"},
		{Action: coreapi.ActionStop, Target: "shop", TargetKind: "mate"},
		{Action: coreapi.ActionRepair, Target: "shop", TargetKind: "mate"},
		{Action: coreapi.ActionClearComposer, Target: "shop", TargetKind: "mate"},
		{Action: coreapi.ActionRemoveProject, Target: "shop", TargetKind: "project"},
	} {
		if _, err := e.Act(context.Background(), req); err == nil || !strings.Contains(err.Error(), "a runtime adapter is required") {
			t.Errorf("%s with no runtime = %v, want spawn's refusal", req.Action, err)
		}
	}
	// The picked harness, and the one mate.meta records for a restart.
	for _, req := range []coreapi.ActionRequest{
		{Action: coreapi.ActionStart, Target: "shop", TargetKind: "mate", Harness: "claude"},
		{Action: coreapi.ActionRestartMate, Target: "shop", TargetKind: "project"},
	} {
		if _, err := e.Act(context.Background(), req); err == nil || !strings.Contains(err.Error(), `harness kind "claude"`) {
			t.Errorf("%s with no harnesses = %v, want the parse's refusal", req.Action, err)
		}
	}
}

// A held restart is confirmed by the next press within
// restartConfirmWindow, once: a later press, or a second one, is a new
// hold. Each project has its own.
func TestRestartHoldsConfirmWithinTheWindowOnce(t *testing.T) {
	var h restartHolds
	at := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	if h.take("shop", at) {
		t.Fatal("a press with no hold was taken as a confirmation")
	}
	h.put("shop", at)
	if h.take("docs", at.Add(time.Second)) {
		t.Fatal("docs confirmed shop's hold")
	}
	if !h.take("shop", at.Add(restartConfirmWindow)) {
		t.Fatal("a press at the end of the window was not a confirmation")
	}
	if h.take("shop", at.Add(restartConfirmWindow)) {
		t.Fatal("a hold confirmed twice")
	}
	h.put("shop", at)
	if h.take("shop", at.Add(restartConfirmWindow+time.Second)) {
		t.Fatal("a press after the window was taken as a confirmation")
	}
}
