package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/observability"
)

// ownActions is one request for each action the engine runs itself but
// the chat, on shop: the Mate's and the Project's (CON-231), a Crew's, the
// box's and Jev's (CON-232).
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
	{Action: coreapi.ActionOpenNews, Target: "shop", TargetKind: "project", Input: "7"},
	{Action: coreapi.ActionOpenWork, Target: "shop", TargetKind: "project"},
	{Action: coreapi.ActionOkayWork, Target: "shop", TargetKind: "project", Input: "3"},
	{Action: coreapi.ActionStop, Target: "shop", TargetKind: "crew", Crew: "buy"},
	{Action: coreapi.ActionRestartCrew, Target: "shop", TargetKind: "crew", Crew: "buy"},
	{Action: coreapi.ActionDiff, Target: "shop", TargetKind: "crew", Crew: "buy"},
	{Action: coreapi.ActionMerge, Target: "shop", TargetKind: "crew", Crew: "buy"},
	{Action: coreapi.ActionResolve, Target: "shop", TargetKind: "project", Input: "resolve: A"},
	{Action: coreapi.ActionReply, Target: "shop", TargetKind: "project", Crew: "buy", Input: "A"},
	{Action: coreapi.ActionPeek, Target: "shop", TargetKind: "project", Crew: "buy"},
	{Action: coreapi.ActionNotice, Target: "shop", TargetKind: "crew", Crew: "buy"},
	// Last: once it has run, shop is not there for the others.
	{Action: coreapi.ActionRemoveProject, Target: "shop", TargetKind: "project"},
}

// The engine runs every action itself, with no Options.Act (CON-232): none
// is refused as not wired. explain_notice is only with Jev
// (Options.Notice); without it, it is refused in the words it was before.
func TestTheEngineRunsEveryActionItself(t *testing.T) {
	w := shopWorkspace(t)
	withJev := New(Options{Workspace: w, Notice: jevSays("none")})
	defer withJev.Stop()
	for _, req := range ownActions {
		// What each one answers over no Herdr is not the point here;
		// that it is the engine that answers is.
		if _, err := withJev.Act(context.Background(), req); err != nil && strings.Contains(err.Error(), "not wired") {
			t.Errorf("%s %s = %v, want the engine's own body", req.Action, req.TargetKind, err)
		}
	}
	noJev := New(Options{Workspace: w})
	defer noJev.Stop()
	_, err := noJev.Act(context.Background(), coreapi.ActionRequest{Action: coreapi.ActionNotice, Target: "shop", TargetKind: "crew", Crew: "buy"})
	var coded *observability.Error
	if !errors.As(err, &coded) || coded.Code != observability.CodeUsage || err.Error() != "usage: explain_notice is not wired in this build" {
		t.Fatalf("explain_notice with no Jev = %v, want it not wired, a usage error", err)
	}
}

// An engine with no workspace refuses every action of its own, the way it
// refuses the chat, and does not panic.
func TestTheEnginesOwnActionsNeedAWorkspace(t *testing.T) {
	e := New(Options{Notice: jevSays("none")})
	defer e.Stop()
	for _, req := range ownActions {
		if _, err := e.Act(context.Background(), req); !errors.Is(err, errNoWorkspace) {
			t.Errorf("%s with no workspace = %v, want %q", req.Action, err, errNoWorkspace)
		}
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

// Each request reaches its own body: the two onboards differ by target
// kind, and the REPL's requests and the mode answer in their own words
// over a Mate that does not run headless and no Herdr.
func TestEachOwnActionReachesItsBody(t *testing.T) {
	w := shopWorkspace(t)
	e := New(Options{Workspace: w})
	defer e.Stop()
	ctx := context.Background()

	line, err := e.Act(ctx, coreapi.ActionRequest{Action: coreapi.ActionOnboard, TargetKind: "workspace", Input: "landing"})
	if want := "Project landing added with no repo yet; add one with `mate project repo add landing <path>`"; err != nil || line != want {
		t.Errorf("onboard of the workspace = %q, %v; want %q", line, err, want)
	}
	if _, err := e.Act(ctx, coreapi.ActionRequest{Action: coreapi.ActionOnboard, Target: "shop", TargetKind: "project-mate"}); err == nil || !strings.Contains(err.Error(), "a runtime adapter is required") {
		t.Errorf("onboard of a Project's Mate = %v, want StartMate's refusal", err)
	}
	line, err = e.Act(ctx, coreapi.ActionRequest{Action: coreapi.ActionMode, Target: "shop", TargetKind: "project"})
	if err != nil || !strings.HasPrefix(line, "shop is now ") {
		t.Errorf("mode = %q, %v; want the flipped mode", line, err)
	}
	line, err = e.Act(ctx, coreapi.ActionRequest{Action: coreapi.ActionOpenNews, Target: "shop", TargetKind: "project", Input: "7"})
	if err != nil || line != "news #7 opened in the Mate" {
		t.Errorf("open news = %q, %v", line, err)
	}
	if _, err := os.Stat(filepath.Join(w.MateDir("shop"), matehost.NewsFocusName)); err != nil {
		t.Errorf("open news wrote no request: %v", err)
	}
	if _, err := e.Act(ctx, coreapi.ActionRequest{Action: coreapi.ActionOpenWork, Target: "shop", TargetKind: "project"}); !errors.Is(err, coreapi.ErrNoMateREPL) {
		t.Errorf("open work of a pane Mate = %v, want ErrNoMateREPL", err)
	}
	if _, err := e.Act(ctx, coreapi.ActionRequest{Action: coreapi.ActionOkayWork, Target: "shop", TargetKind: "project", Input: "x"}); err == nil || !strings.Contains(err.Error(), "okay work needs a Project and a row id") {
		t.Errorf("okay work of no row = %v, want its usage refusal", err)
	}
	if _, err := e.Act(ctx, coreapi.ActionRequest{Action: coreapi.ActionStop, Target: "shop", TargetKind: "project"}); err == nil || err.Error() != "usage: stop applies to a Mate or a Crew, not a project" {
		t.Errorf("stop of a project = %v, want the stop's refusal", err)
	}
	if _, err := e.Act(ctx, coreapi.ActionRequest{Action: coreapi.ActionRepair, Target: "shop", TargetKind: "crew", Crew: "buy"}); err == nil || !strings.Contains(err.Error(), "repairing a Crew is not wired") {
		t.Errorf("repair of a crew = %v, want the repair's refusal", err)
	}
}
