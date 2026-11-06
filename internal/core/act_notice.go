package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/notice"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// NoticeClassifier is Jev (internal/notice's Client): it names what one
// captured screen shows. The engine asks it for explain_notice alone, and
// only when Options.Notice is set.
type NoticeClassifier interface {
	Classify(context.Context, string) (notice.Result, error)
}

// explainNotice is explain_notice (CON-141): Jev's suggestion for what the
// Mate's or a Crew's screen shows. It ran in cmd/mate's consoleNoticeAction
// until CON-215's B5 (CON-232). It receives no observer health and writes
// no state. One explicit action produces one capture and one request,
// bounded by a shared deadline.
func (e *Engine) explainNotice(ctx context.Context, req coreapi.ActionRequest) (string, error) {
	ws, deps, classifier := e.o.Workspace, e.o.Spawn, e.o.Notice
	if classifier == nil {
		return "", errors.New("Jev is not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, notice.Timeout)
	defer cancel()
	// Do not share Workspace's config cache with the periodic loader.
	fresh, err := store.Open(ws.Root())
	if err != nil {
		return "", err
	}
	var handle runtime.AgentHandle
	var kind harness.Kind
	var screen string
	switch {
	case req.TargetKind == "mate" && req.Crew == "" && fresh.MateHeadless(req.Target):
		// A headless Mate has no screen: its host's last turns are
		// what a screen would show (CON-225).
		screen, err = hostScreen(ctx, matehost.Dial(fresh.MateDir(req.Target)))
		if err != nil {
			return "", fmt.Errorf("cannot read the headless Mate's host for notice explanation: %w", err)
		}
	case req.TargetKind == "mate" && req.Crew == "":
		handle, kind, err = spawn.MateHandle(ctx, fresh, deps, req.Target)
	case req.TargetKind == "crew" && req.Crew != "":
		handle, kind, err = spawn.CrewHandle(ctx, fresh, deps, req.Target, req.Crew)
	default:
		return "", errors.New("notice explanation needs a Mate or Crew")
	}
	if err != nil {
		return "", err
	}
	if screen == "" {
		screens, err := deps.Harnesses.Screen(kind)
		if err != nil {
			return "", err
		}
		screen, err = deps.Runtime.ReadAgentStyled(ctx, handle, screens.ReadSource(), notice.MaxLines)
		if err != nil {
			return "", errors.New("cannot read terminal for notice explanation")
		}
	}
	captured := time.Now()
	result, err := classifier.Classify(ctx, screen)
	if err != nil {
		return "", err
	}
	label, ok := noticeLabels[result.Label]
	if !ok {
		return "", errors.New("unrecognized notice classification")
	}
	return fmt.Sprintf("Jev suggests: %s\n\nAdvisory only; inspect the current pane before acting.\nCaptured: %s\n\nModel confidence: %.2f (not measured accuracy)\nModel: %s\nInput tokens: %d\n\nNo automatic action is taken.",
		label, captured.Format(time.RFC3339), result.Confidence, notice.Model, result.InputTokens), nil
}

// hostScreenTurns is how many of a headless Mate's last turns stand in for
// its screen.
const hostScreenTurns = 5

// hostScreen is what a headless Mate's host shows of its last turns, as a
// screen would: each turn's message, its reply and its error, the last
// notice.MaxLines lines. A quota or sign-in failure of `claude -p` is in a
// turn's error. A host with no turn yet has nothing to explain.
func hostScreen(ctx context.Context, host matehost.Client) (string, error) {
	items, err := host.History(ctx, hostScreenTurns)
	if err != nil {
		return "", err
	}
	var lines []string
	for _, it := range items {
		lines = append(lines, "> "+it.Text)
		if it.Reply != "" {
			lines = append(lines, it.Reply)
		}
		if it.Error != "" {
			lines = append(lines, "error: "+it.Error)
		}
	}
	text := strings.TrimSpace(strings.Join(lines, "\n"))
	if text == "" {
		return "", errors.New("the headless Mate has run no turn yet")
	}
	if all := strings.Split(text, "\n"); len(all) > notice.MaxLines {
		text = strings.Join(all[len(all)-notice.MaxLines:], "\n")
	}
	return text, nil
}

var noticeLabels = map[string]string{
	"quota_warning":       "quota warning (may still continue)",
	"quota_exhausted":     "quota exhausted (requests appear blocked)",
	"auth_required":       "authentication required",
	"permission_required": "permission or trust requested",
	"update_notice":       "software update notice",
	"none":                "no current notice identified",
	"unknown":             "unclear notice; inspect the pane",
}
