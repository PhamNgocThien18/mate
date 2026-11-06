package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/outbox"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// The message box's three actions (mvp.md task 15, section 5), bridged from
// a UI's box keys to internal/send, internal/spawn and internal/store. They
// ran in cmd/mate's console_box.go until CON-215's B5 (CON-232). No UI
// imports those packages for a box key: the console's own boundary is
// enforced by internal/ui/console/boundary_test.go.

// peekLines is how much of a crew's pane `p` reads, matching mvp.md section
// 4's "mate peek <crew> đọc 40 dòng cuối pane" and send.DefaultLines, which
// is the window the composer classifier already judges a screen from.
const peekLines = 40

// resolve is Enter and the `[resolve]` button: hand one inbox item to the
// Mate and ask it to answer the crew.
//
// The line is query.BoxResolveLine's, built next to the merge that produced
// the item: the crew's own question, the absolute path of the status file to
// read, and the `mate send` that answers the crew. It replaces the old
// bare `signal: <path>`, which said only "here is a file" and left the Mate
// to infer that answering was its job. The path is absolute because the
// Mate's cwd is its own workspace directory, not the project's, so a path
// relative to the project resolves to nothing there. The line carries the
// from-app marker sentinel, which is what lets the Mate tell an
// app-generated line from something its human typed, and what the Mate's
// UserPromptSubmit hook checks before clearing `.auto`.
//
// Since task 30 the line is queued rather than refused. The Mate's composer
// is busy most of the time (mvp.md section 7: `[assign]` met `agent is
// mid-turn` four or five times in a row in task 24), so the action appends
// the line to the Mate's outbox, makes one immediate attempt so an idle Mate
// gets it with no delay, and returns either way; the engine's sender loop
// (Options.Sender) retries every two seconds until the composer is empty.
// sent.log is still written only once the composer cleared - by the outbox,
// in the same words as before - so the box never shows the Mate a message no
// agent received. The same inbox entry assigned twice is queued once
// (req.Key, query.BoxEntry.AssignKey), and the answer says when it was.
func (e *Engine) resolve(ctx context.Context, req coreapi.ActionRequest) (string, error) {
	ws, deps := e.o.Workspace, e.o.Spawn
	resolve := strings.TrimSpace(req.Input)
	if resolve == "" {
		return "", observability.NewError(observability.CodeUsage, "no resolve line was built for this entry")
	}
	if err := ws.RequireProject(req.Target); err != nil {
		return "", err
	}
	key := req.Key
	if key == "" {
		// A caller that did not name the entry (a script, an older test)
		// still gets dedup, on the words themselves.
		key = "line:" + resolve
	}
	sender := spawn.MateOutbox(ws, deps)
	queued, err := sender.Enqueue(req.Target, outbox.Request{
		Source: store.OutboxSourceAssign, Key: key, Text: resolve,
	})
	if err != nil {
		return "", err
	}
	item := queued.Item
	if item.Queued() {
		attempt, err := sender.Attempt(ctx, req.Target)
		if err != nil {
			return "", err
		}
		if attempt.Item.ID == item.ID {
			item = attempt.Item
			if attempt.Delivered && !queued.Duplicate {
				return fmt.Sprintf("sent to %s; the Mate answers the crew with mate send", attempt.Agent), nil
			}
			if attempt.Refusal != nil && !queued.Duplicate {
				return fmt.Sprintf("queued for the Mate (%s); it goes in when the composer is empty",
					refusalReason(attempt.Refusal)), nil
			}
		} else if !queued.Duplicate {
			return "queued for the Mate, behind a line it has not taken yet", nil
		}
	}
	switch item.State {
	case store.OutboxSent:
		return fmt.Sprintf("already assigned %s, sent %s; not queued again",
			item.At.UTC().Format("15:04"), item.SentAt.UTC().Format("15:04")), nil
	default:
		return fmt.Sprintf("already assigned %s, still queued; not queued again",
			item.At.UTC().Format("15:04")), nil
	}
}

// refusalReason is a refusal in the few words an outcome line has room for.
// The full observation is in the outbox item's last_refusal, and the inbox
// row keeps saying the line is queued until it is not.
func refusalReason(err error) string {
	switch {
	case errors.Is(err, send.ErrAgentBusy):
		return "the Mate is mid-turn"
	case errors.Is(err, send.ErrComposerPending):
		return "its composer holds unsubmitted text"
	case errors.Is(err, send.ErrComposerUnknown):
		return "its pane shows no composer"
	case errors.Is(err, send.ErrEnterSwallowed):
		return "enter did not submit it"
	case errors.Is(err, send.ErrDialogOpen):
		return "a dialog is open over its pane"
	case errors.Is(err, send.ErrPaneChanged):
		return "its pane changed while it was read"
	}
	return err.Error()
}

// reply is `r`: one line into the crew's own composer.
//
// No marker byte: this line is the user's, not the app's (mvp.md section 4 -
// "Ai đó gõ một dòng trả lời vào pane crew"), and to the crew it is simply a
// new prompt. It is recorded with Source: user for the same reason.
func (e *Engine) reply(ctx context.Context, req coreapi.ActionRequest) (string, error) {
	ws, deps := e.o.Workspace, e.o.Spawn
	text := strings.TrimSpace(req.Input)
	if text == "" {
		return "", observability.NewError(observability.CodeUsage, "a reply is one non-empty line")
	}
	if req.Crew == "" {
		return "", observability.NewError(observability.CodeUsage, "no crew was named for the reply")
	}
	handle, kind, err := spawn.CrewHandle(ctx, ws, deps, req.Target, req.Crew)
	if err != nil {
		return "", err
	}
	report, err := send.Send(ctx, send.Deps{Runtime: deps.Runtime, Harnesses: deps.Harnesses, Observer: deps.Observer}, handle, kind, text, send.Options{})
	if err != nil {
		return "", err
	}
	if err := ws.AppendSent(req.Target, store.SentEntry{
		Source: store.SourceUser,
		Target: store.CrewTarget(req.Crew),
		Text:   text,
	}); err != nil {
		return "", err
	}
	return fmt.Sprintf("replied to %s in %d enter(s)", report.Agent, report.Presses), nil
}

// peek is `p`: the crew's last peekLines of pane, returned as text for the
// overlay. It writes nothing - not to the pane, not to sent.log - because
// looking at a screen is not communication, and a peek in the box would be
// a record of the reader rather than of the crew.
func (e *Engine) peek(ctx context.Context, req coreapi.ActionRequest) (string, error) {
	ws, deps := e.o.Workspace, e.o.Spawn
	if req.Crew == "" {
		return "", observability.NewError(observability.CodeUsage, "no crew was named for the peek")
	}
	handle, kind, err := spawn.CrewHandle(ctx, ws, deps, req.Target, req.Crew)
	if err != nil {
		return "", err
	}
	screens, err := deps.Harnesses.Screen(kind)
	if err != nil {
		return "", err
	}
	screen, err := deps.Runtime.ReadAgent(ctx, handle, screens.ReadSource(), peekLines)
	if err != nil {
		return "", err
	}
	return screen, nil
}
