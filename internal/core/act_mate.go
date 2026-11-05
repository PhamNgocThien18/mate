package core

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nguyenngocanh94/mate/internal/autopilot"
	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/outbox"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/recall"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// The Mate's actions (CON-231): start, resume and onboard it, stop it,
// repair (re-read) its record, flip its mode, restart it and clear its
// composer. They ran in cmd/mate's consoleAction until CON-215's B4. Every
// one returns the one line a UI shows after it and propagates a failure
// verbatim - a spawn error already says which step refused and what it
// observed, and rewording it here would lose the Herdr detail the operator
// needs. Every one runs over Options.Workspace and Options.Spawn: a spawn
// function refuses a Spawn with no Runtime in its own words, and a harness
// is parsed against Spawn.Harnesses, which refuses every kind when empty.

// startMate starts the Mate of one Project. The harness is whatever the
// UI's picker chose; an empty one is not an error but "no choice was
// made", which StartMate reads as the workspace default.
func (e *Engine) startMate(ctx context.Context, req coreapi.ActionRequest) (string, error) {
	ws, deps := e.o.Workspace, e.o.Spawn
	if req.Target == "" {
		return "", observability.NewError(observability.CodeUsage, "no Project was named for "+string(req.Action))
	}
	kind, err := e.consoleHarness(req.Harness)
	if err != nil {
		return "", err
	}
	// Resume is the 's' on a stopped Mate: the conversation recorded in
	// mate.meta comes back, or a fresh one starts with a note saying why.
	res, err := spawn.StartMate(ctx, ws, deps, spawn.StartRequest{Project: req.Target, Harness: kind, Resume: req.Action == coreapi.ActionResume, HeadlessOnly: req.HeadlessOnly})
	if err != nil {
		return "", err
	}
	line := fmt.Sprintf("Mate %s is running on %s in pane %s", res.Agent, res.Harness, res.Pane)
	if res.ModeNote != "" {
		// First: the console's status line is one row (CON-220).
		line = res.ModeNote + "; " + line
	}
	if res.Resumed {
		line += "; resumed session " + res.ResumedFrom
	}
	if res.ResumeNote != "" {
		line += "; " + res.ResumeNote
	}
	if res.Adopted {
		line += "; adopted: an interrupted start had left it running unrecorded"
	}
	if res.StaleMeta {
		line += "; the previous record was stale and has been replaced"
	}
	if res.UpdateDialog {
		line += "; a Codex update prompt was skipped"
	}
	if res.TrustDialog {
		line += "; a trust dialog was answered"
	}
	return line, nil
}

// stopMate is stop on a Mate row. A crew's stop is Options.Act's until
// CON-232.
func (e *Engine) stopMate(ctx context.Context, req coreapi.ActionRequest) (string, error) {
	if req.TargetKind != "mate" && req.TargetKind != "project-mate" {
		return "", observability.NewError(observability.CodeUsage,
			fmt.Sprintf("stop applies to a Mate or a Crew, not a %s", req.TargetKind))
	}
	res, err := spawn.StopMate(ctx, e.o.Workspace, e.o.Spawn, req.Target)
	if err != nil {
		return "", err
	}
	if res.AlreadyGone {
		return fmt.Sprintf("Mate %s was already gone; mate.meta cleared", res.Agent), nil
	}
	return fmt.Sprintf("Mate %s stop confirmed in session %s", res.Agent, res.Session), nil
}

// repairMate is repair. `mate status` is the repair: it re-asks Herdr
// about the agent mate.meta names and reports what it found. It never
// rewrites the meta, so the one line it returns is an observation, not a
// claim that anything was fixed.
func (e *Engine) repairMate(ctx context.Context, req coreapi.ActionRequest) (string, error) {
	if req.TargetKind != "mate" && req.TargetKind != "project-mate" && req.TargetKind != "project" {
		return "", observability.NewError(observability.CodeUsage,
			"repairing a Crew is not wired until mvp.md task 16")
	}
	status, err := spawn.MateStatus(ctx, e.o.Workspace, e.o.Spawn, req.Target)
	if err != nil {
		return "", err
	}
	return status.Line(), nil
}

// toggleMode flips `mate/.auto` (mvp.md section 5). The flag is the whole
// of the mode: the daemon re-reads it every tick and at the moment it
// types, so the keystroke takes effect within one window in both
// directions without anyone telling the daemon anything.
//
// The line it returns names what the flag now permits, and the digest
// window with it, because "auto" on its own does not tell a reader when
// the first line might land in the Mate's pane.
func (e *Engine) toggleMode(_ context.Context, req coreapi.ActionRequest) (string, error) {
	ws, project := e.o.Workspace, req.Target
	if project == "" {
		return "", observability.NewError(observability.CodeUsage, "no Project was named for mode")
	}
	if err := ws.RequireProject(project); err != nil {
		return "", err
	}
	next := !ws.Auto(project)
	if err := ws.SetMode(project, next); err != nil {
		return "", err
	}
	mode := query.ModeFor(next)
	if next {
		return fmt.Sprintf("%s is now %s; the daemon digests the inbox into the Mate's pane every %s",
			project, mode, autopilot.DefaultInterval), nil
	}
	return fmt.Sprintf("%s is now %s while you are at the Mate; %s without a prompt to it or another m turns auto back on", project, mode, autopilot.DefaultQuietAfter), nil
}

// consoleHarness parses the harness a UI picked. It is parsed here rather
// than trusted: the UI sends a string across a seam, and an unknown kind
// must be a refusal naming the field.
func (e *Engine) consoleHarness(kind query.HarnessKind) (harness.Kind, error) {
	if kind == "" {
		return "", nil
	}
	parsed, err := e.o.Spawn.Harnesses.Parse(string(kind))
	if err != nil {
		return "", observability.WrapError(observability.CodeUsage, "harness", err)
	}
	return parsed, nil
}

// restartMate is the Actions menu's restart_mate entry on the Mate row:
// stow, stop the Mate, then start it again, through the same spawn seams
// the action menu's own Stop and Start use. It is one action rather than
// two keystrokes because the state it exists for - a Mate that no longer
// answers - is one a reader wants out of in one gesture, and a stop that
// is not followed by a start leaves the project with no Mate at all.
//
// Since task 37 (B7) the restart first asks the Mate to file what exists
// only in its conversation: the `⟦mate⟧ stow:` line goes through the
// outbox like every other line the app types into a Mate, and the restart
// waits for that turn to end, up to outbox.DefaultStowCeiling, then goes
// ahead either way. The captain's own unsent text in the composer is never
// typed over: the first press is held with a question on the outcome line,
// and a second press within restartConfirmWindow, from any UI, restarts
// without stowing (Engine.holds). The outcome line opens with "stowed" or
// "not stowed: <reason>".
//
// A Mate that was already gone is not an error: StopMate reports it and
// the start proceeds, which is exactly the case a reader reaching for a
// restart is most often in. The returned line names every step, so the
// outcome line says what actually happened rather than only that
// something did.
func (e *Engine) restartMate(ctx context.Context, req coreapi.ActionRequest) (string, error) {
	ws, deps := e.o.Workspace, e.o.Spawn
	if req.Target == "" {
		return "", observability.NewError(observability.CodeUsage, "no Project was named for the restart")
	}
	if err := store.ValidateProjectName(req.Target); err != nil {
		return "", err
	}
	confirmed := e.holds.take(req.Target, e.now())
	stow, err := spawn.MateOutbox(ws, deps).Stow(ctx, req.Target, outbox.StowOptions{})
	if err != nil {
		return "", err
	}
	if stow.Held {
		if !confirmed {
			e.holds.put(req.Target, e.now())
			return fmt.Sprintf("held: the Mate's composer holds unsent text (%s), so mate typed nothing and restarted nothing; choose Restart again within %s to restart without stowing, which discards that text",
				strconv.Quote(recall.Clip(stow.Pending, 80)), outbox.Span(restartConfirmWindow)), nil
		}
		stow.Reason = "the composer held unsent text; restarted on your confirmation"
	}
	// A restart keeps the harness the Mate was running: StartMate otherwise
	// launches the workspace default, and a Codex Mate restarted from the
	// console came back as a fresh Claude Mate (found 2026-09-24, task 38).
	kind, err := e.restartHarness(req.Target)
	if err != nil {
		return "", err
	}
	stopped, err := spawn.StopMate(ctx, ws, deps, req.Target)
	gone := mateNotRecorded(err)
	if err != nil && !gone {
		return "", err
	}
	res, err := spawn.StartMate(ctx, ws, deps, spawn.StartRequest{Project: req.Target, Resume: true, Harness: kind, HeadlessOnly: req.HeadlessOnly})
	if err != nil {
		return "", err
	}
	was := "stopped " + stopped.Agent
	if gone || stopped.AlreadyGone {
		was = "the previous Mate was already gone"
	}
	line := fmt.Sprintf("%s; %s; Mate %s is running on %s in pane %s", stow.Outcome(), was, res.Agent, res.Harness, res.Pane)
	if res.ModeNote != "" {
		// First: the console's status line is one row (CON-220).
		line = res.ModeNote + "; " + line
	}
	return line, nil
}

// restartHarness is the harness mate.meta records for the project's Mate,
// or "" (the workspace default) when there is no record of one.
func (e *Engine) restartHarness(project string) (harness.Kind, error) {
	meta, err := e.o.Workspace.ReadMateMeta(project)
	if err != nil {
		return "", err
	}
	recorded := strings.TrimSpace(meta[spawn.MetaHarness])
	if recorded == "" {
		return "", nil
	}
	return e.o.Spawn.Harnesses.Parse(recorded)
}

// mateNotRecorded reports the one StopMate refusal a restart absorbs:
// `mate.meta` names no agent, so there was nothing to stop. Every other
// failure is propagated - a stop that could not be confirmed must not be
// followed by a start that would race the agent still running.
func mateNotRecorded(err error) bool {
	var coded *observability.Error
	return errors.As(err, &coded) && coded.Code == observability.CodeNotFound
}

// restartConfirmWindow is how long a held restart waits for the captain's
// second press.
const restartConfirmWindow = 2 * time.Minute

// restartHolds remembers, per project, a restart held because the Mate's
// composer had unsent text in it, so the next press within
// restartConfirmWindow is the captain's confirmation. It is the engine's,
// so a hold made from one UI is confirmed from another (CON-141, CON-209).
// It lives in the engine's memory and nothing writes it to disk: an engine
// restarted in between asks again, which is the safe side (CON-231).
type restartHolds struct {
	mu sync.Mutex
	at map[string]time.Time
}

func (h *restartHolds) put(project string, now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.at == nil {
		h.at = map[string]time.Time{}
	}
	h.at[project] = now
}

// take reports whether a hold for project is still open, and clears it.
func (h *restartHolds) take(project string, now time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	at, ok := h.at[project]
	delete(h.at, project)
	return ok && now.Sub(at) <= restartConfirmWindow
}

// clearComposer is the Actions menu's clear_composer entry: one Ctrl+U
// into the Mate's pane, or the draft of a headless Mate cleared on its
// host.
//
// It goes through runtime.SendKeys, not internal/send: send.Send types a
// line and verifies the composer cleared afterwards, which is the wrong
// shape entirely for a key whose whole purpose is that the composer is in
// a state nobody can classify. Nothing is typed, nothing is submitted and
// nothing is recorded in sent.log - a keypress that removes half-typed
// text is not a message the Mate was sent.
func (e *Engine) clearComposer(ctx context.Context, req coreapi.ActionRequest) (string, error) {
	ws, deps := e.o.Workspace, e.o.Spawn
	if req.Target == "" {
		return "", observability.NewError(observability.CodeUsage, "no Project was named for the composer clear")
	}
	if host, ok := spawn.HostClient(ws, req.Target); ok {
		if err := host.ClearDraft(ctx); err != nil {
			return "", err
		}
		return fmt.Sprintf("cleared the draft in %s's headless Mate; nothing was sent", req.Target), nil
	}
	handle, _, err := spawn.MateHandle(ctx, ws, deps, req.Target)
	if err != nil {
		return "", err
	}
	if err := deps.Runtime.SendKeys(ctx, handle, []string{ClearComposerKey}); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s pressed in %s's pane; nothing was sent", ClearComposerKey, handle.Name), nil
}

// ClearComposerKey is the one key [clear composer] presses. Ctrl+U is the
// readline kill-line every harness composer inherits (Claude Code and
// Codex both run their input through one), so it clears the line without
// submitting it.
const ClearComposerKey = "ctrl+u"
