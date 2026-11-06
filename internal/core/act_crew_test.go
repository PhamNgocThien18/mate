package core

import (
	"context"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/notice"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/send"
)

// The bodies of a Crew's, the box's and Jev's actions are cmd/mate's tests'
// (console_box_test.go, console_crew_stop_test.go, console_notice_test.go
// and the live tests), which press them through Engine.Act over the fake
// Herdr. These are what the engine adds: the refusals before any Herdr.

// jevSays is a Jev that names every screen label.
type jevSays string

func (j jevSays) Classify(context.Context, string) (notice.Result, error) {
	return notice.Result{Label: string(j), Confidence: 1}, nil
}

// Each of a Crew's actions refuses a request that names no Crew, in its own
// words, before it asks anything.
func TestACrewsActionsNeedACrew(t *testing.T) {
	e := New(Options{Workspace: shopWorkspace(t)})
	defer e.Stop()
	for _, tc := range []struct {
		action coreapi.Action
		want   string
	}{
		{coreapi.ActionStop, "usage: stop crew needs both a Project and one of its Crews; the request named shop/"},
		{coreapi.ActionRestartCrew, "usage: restart crew needs both a Project and one of its Crews; the request named shop/"},
		{coreapi.ActionMerge, "usage: merge needs both a Project and one of its Crews; the request named shop/"},
		{coreapi.ActionDiff, `usage: diff needs both a Project and a Crew; the Console sent target="shop" crew=""`},
		{coreapi.ActionReply, "usage: no crew was named for the reply"},
		{coreapi.ActionPeek, "usage: no crew was named for the peek"},
	} {
		req := coreapi.ActionRequest{Action: tc.action, Target: "shop", TargetKind: "crew", Input: "A"}
		if _, err := e.Act(context.Background(), req); err == nil || err.Error() != tc.want {
			t.Errorf("%s with no crew = %v, want %q", tc.action, err, tc.want)
		}
	}
	if _, err := e.Act(context.Background(), coreapi.ActionRequest{Action: coreapi.ActionResolve, Target: "shop"}); err == nil || err.Error() != "usage: no resolve line was built for this entry" {
		t.Errorf("resolve with no line = %v", err)
	}
	if _, err := e.Act(context.Background(), coreapi.ActionRequest{Action: coreapi.ActionResolve, Target: "nowhere", Input: "resolve: A"}); err == nil || !strings.Contains(err.Error(), "nowhere") {
		t.Errorf("resolve for a project not in the workspace = %v", err)
	}
}

// explain_notice asks Jev about a Mate or a Crew, nothing else.
func TestExplainNoticeNeedsAMateOrACrew(t *testing.T) {
	e := New(Options{Workspace: shopWorkspace(t), Notice: jevSays("none")})
	defer e.Stop()
	_, err := e.Act(context.Background(), coreapi.ActionRequest{Action: coreapi.ActionNotice, Target: "shop", TargetKind: "workspace"})
	if err == nil || err.Error() != "notice explanation needs a Mate or Crew" {
		t.Fatalf("explain_notice of the workspace = %v", err)
	}
}

// Every refusal of internal/send has a few words of its own on the
// outcome line, wrapped or not; any other error is said as it is.
func TestRefusalReasonIsShortForEverySendRefusal(t *testing.T) {
	for _, err := range []error{send.ErrAgentBusy, send.ErrComposerPending, send.ErrComposerUnknown,
		send.ErrEnterSwallowed, send.ErrDialogOpen, send.ErrPaneChanged} {
		coded := observability.WrapError(observability.CodeStateConflict, "a send refusal", err)
		if reason := refusalReason(coded); reason == coded.Error() || reason == "" {
			t.Errorf("%v has no short reason", err)
		}
	}
	other := observability.NewError(observability.CodeUsage, "something else")
	if reason := refusalReason(other); reason != other.Error() {
		t.Errorf("refusalReason(%v) = %q, want it as it is", other, reason)
	}
}
