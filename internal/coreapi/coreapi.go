// Package coreapi is the contract between mate's core and the UIs that
// drive it (CON-206): the console, Mate Office and Agent City. A UI reads
// the workspace through a LoadFunc and asks for an action through an
// ActionFunc; the core is whatever implements the two. Today the engine of
// internal/core does, inside the console's process: its Snapshot, and its
// Act for every UI's actions (CON-209). Once the core runs as a
// process of its own (CON-203), its engine and the client that reaches it
// over a socket import this package as well.
//
// It holds types, constants and errors only: no logic, no I/O, no network.
// That is what lets internal/ui/console import it, and why it imports
// nothing but the standard library and internal/query's read types
// (boundary_test.go keeps it that way).
package coreapi

import (
	"context"
	"errors"

	"github.com/nguyenngocanh94/mate/internal/query"
)

// LoadFunc reads the workspace as a UI shows it (query.Snapshot): the
// Projects, their Mates and Crews, and each field in the state it was read
// in. A UI calls it for every fresh picture it draws.
type LoadFunc func(context.Context) (query.Snapshot, error)

// Action names one thing a UI asks the core to do. A row's actions are
// also the words query.ActionAvailability publishes for it, so a UI offers
// one only while the snapshot says it can run.
type Action string

const (
	ActionStart  Action = "start"
	ActionStop   Action = "stop"
	ActionResume Action = "resume"
	ActionRepair Action = "repair"
	// ActionMode flips a Project's communication mode (mvp.md section 5).
	// Today it only moves the flag and the label: the daemon that acts on
	// auto mode is mvp.md task 19, which is why the console's key line says
	// "Mode" and not "Auto reply".
	ActionMode Action = "mode"
	// ActionOnboard adds a Project to the workspace, or creates and starts
	// a Project's Mate.
	ActionOnboard Action = "onboard"
	// The message-box actions (mvp.md task 15, section 5). They act on a box
	// entry rather than on a snapshot row, which is why ActionRequest
	// carries a Crew of its own: the entry names a crew, and the Project
	// frame's selected row usually does not.
	//
	// ActionResolve hands one inbox item to the Mate as a `resolve:` line,
	// typed into its composer behind the from-app sentinel: the crew's
	// question, the status file to read, and the `mate send` that answers
	// the crew. It is the console's `[assign]` button and `a` key - the word
	// on the button is the reader's ("give this away"), the word on the wire
	// is the Mate's manual's, and they are deliberately not the same. A
	// page's `assign` is the same request.
	//
	// ActionReply and ActionPeek are the CLI's, not the console's: no
	// console surface issues either one since 2026-09-19 - a reader who
	// wants to talk to a crew or read its screen opens that crew's pane,
	// which is what a box row now does. They stay in the enum because
	// cmd/mate implements them and an ActionFunc is free to be asked.
	// Agent City's Peek button asks for ActionPeek (CON-142) in the same
	// word, `peek`, so the contract has no second constant for it.
	ActionResolve Action = "resolve"
	ActionReply   Action = "reply"
	ActionPeek    Action = "peek"
	// ActionNotice asks the optional classifier for an advisory only.
	ActionNotice Action = "explain_notice"
	// The two recovery actions. They exist because a Mate is a live
	// interactive agent sharing its composer with the reader: a key
	// sequence that went astray can leave junk half-typed in it, and an
	// agent can wedge outright. Both are entries in the Mate row's Actions
	// menu (the console's actions.go), which the box zone opens with `o`:
	// they are rare, and a menu is where a reader looks for something they
	// do not do every day.
	//
	// ActionRestartMate stops the Mate and starts it again through the same
	// seams the action menu uses. ActionClearComposer presses Ctrl+U in the
	// Mate's pane - it types nothing and sends nothing, so it can never
	// become a message the Mate answers.
	ActionRestartMate   Action = "restart_mate"
	ActionClearComposer Action = "clear_composer"
	// ActionDiff is a Crew row's review surface (mvp.md task 21): what
	// `mate diff <project> <crew>` prints - the commits the crew's branch
	// carries beyond the project's default branch, then the patch. It writes
	// nothing, so it is the one action with no confirmation, and its result
	// is a screenful rather than a line: the console puts it in the
	// scrolling overlay of its diff.go instead of on the message line.
	ActionDiff Action = "diff"
	// ActionMerge lands a Crew's branch in the Project's default branch and
	// finishes the Crew (mvp.md task 22). It is offered on a Crew row only
	// while that Crew reads `wait-mate` - the one state in which the Crew
	// has said it is done and handed back - and it is absent, not disabled,
	// anywhere else: a menu entry that could never apply to this row is not
	// a refusal worth reading. It is dangerous, so the menu's own
	// confirmation stands in front of it, exactly as it does for
	// ActionRestartMate.
	//
	// ActionDiff is its read-only counterpart and deliberately has neither
	// restriction: a reader looks at a branch to decide whether it is worth
	// landing, which is before, not after, the Crew says `wait-mate`.
	ActionMerge Action = "merge"
	// ActionRestartCrew starts a crew's recorded harness again in the
	// worktree it already has, replacing an agent that died with its Herdr
	// pane (a Herdr server lost to a machine restart, a harness process
	// that exited). It is the console's counterpart of `mate crew relaunch`
	// and, unlike `crew spawn`, it does not need a new branch or worktree.
	// It is dangerous - a live crew would be stopped first - so the menu's
	// confirmation stands in front of it, like ActionRestartMate.
	ActionRestartCrew Action = "restart_crew"
	// ActionRemoveProject takes a Project out of the workspace: it stops the
	// Project's crews and Mate, then unregisters it (mvp.md task 75). Nothing
	// on disk is deleted, so adding the Project again brings its history
	// back. It is dangerous only in that it stops agents, so the confirmation
	// is a plain `y`, not a typed name.
	ActionRemoveProject Action = "remove_project"
	// ActionOpenNews asks a Project's headless Mate REPL to open one news
	// (Input is its id): Enter on a news in the box. It writes only the
	// request the REPL reads, so it has no confirmation.
	ActionOpenNews Action = "open_news"
	// ActionOkayWork is the captain's tick on a done row of a Project's work
	// board (CON-158): Space on it in the console's work sheet, or a page's
	// `okay`. Input is the row id. The tick is final (matehost.OkayWork),
	// and it asks nothing: a done row leaving the board is the whole of its
	// effect.
	ActionOkayWork Action = "okay_work"
	// ActionOpenWork asks a Project's headless Mate REPL to show its work
	// board as a page (CON-198): `w` on a Mate or Project row. A Mate with
	// no REPL refuses with ErrNoMateREPL, and the console shows its own
	// sheet instead.
	ActionOpenWork Action = "open_work"
	// The chat's actions (CON-142): what the captain says to a headless
	// Mate, and to the private session of one of its news (CON-104), from a
	// page. Input is what the captain typed, as a reply's line is, and News
	// the news; a say has no news, and an interrupt or a fork_close no
	// text.
	//
	// ActionSay is a message to the Mate. ActionInterrupt stops the Mate's
	// turn in flight or, with News, the session of that news instead.
	// ActionAnswer answers the news, ActionFork asks its session a question
	// the session answers read-only, and ActionForkClose closes the
	// session; what was said in it stays.
	ActionSay       Action = "say"
	ActionInterrupt Action = "interrupt"
	ActionAnswer    Action = "answer"
	ActionFork      Action = "fork"
	ActionForkClose Action = "fork_close"
	// TODO: v1 also had retry, discard and switch_harness. mate has no
	// retry (a Crew runs once), and no discard action: throwing work away
	// is `mate crew stop --discard`, on the captain's explicit word.
)

// ActionRequest is the identity selected from the snapshot. TargetKind is
// deliberately explicit: a Project id and a Crew id are both strings, and
// guessing which service to call from the string would assert more than the
// snapshot established.
//
// The JSON names are snake_case: the form a request takes on the core's
// socket, once there is one (CON-212).
type ActionRequest struct {
	Action     Action `json:"action"`
	Target     string `json:"target,omitempty"`
	TargetKind string `json:"target_kind,omitempty"`
	// Input is the request's text: the project name a workspace onboard
	// registers, the line a reply sends or a resolve hands to the Mate, the
	// news id of open_news, the row id of okay_work, and what the captain
	// typed for a say, an answer or a fork.
	Input string `json:"input,omitempty"`
	// News is the news a chat action is about: the one an answer answers,
	// a fork asks and a fork_close closes, and on an interrupt the session
	// to stop instead of the Mate's turn. 0 on every other request.
	News int `json:"news,omitempty"`
	// Repo is the repository path a workspace onboard registers, as typed
	// into the new-project form: absolute, or relative to the workspace
	// root. Empty on a workspace onboard means the Project starts with no
	// repo (docs/mvp.md M9), and it is empty on every other request.
	Repo string `json:"repo,omitempty"`
	// Crew is the crew a box action names (mvp.md task 15). It is separate
	// from Target because those actions are addressed to a Project *and* one
	// of its crews, and folding the two into one string would make the
	// bridge guess which it had been given.
	Crew string `json:"crew,omitempty"`
	// Key is the outbox dedup key of the box entry an ActionResolve hands
	// to the Mate (query.BoxEntry.AssignKey, mvp.md task 30), so a second
	// press on the same question is recognised rather than queued again.
	Key string `json:"key,omitempty"`
	// Harness is the agent chosen for this request: on an onboard it is the
	// harness the new Mate is created with. Empty everywhere else - an
	// existing Mate keeps its recorded harness.
	Harness query.HarnessKind `json:"harness,omitempty"`
	// HeadlessOnly says the UI that sent the request cannot show a pane
	// Mate: the console in a terminal it cannot split, or a dashboard page
	// (CON-205). A start, resume, onboard or restart_mate it asks for then
	// runs the Mate headless when its harness can. The console's Model
	// never sets it: cmd/mate does for the console's TUI, and the dashboard
	// for every press on a page.
	HeadlessOnly bool `json:"headless_only,omitempty"`
}

// ActionFunc is the application-service seam: it runs one action and
// returns the one line a UI shows for it, or the error that refused or
// failed it. A nil function keeps a UI read-only (useful for callers and
// tests that only need navigation).
type ActionFunc func(context.Context, ActionRequest) (string, error)

// ErrNoMateREPL is an open-work request for a Mate that runs without the
// REPL: the console shows the board in its own sheet.
var ErrNoMateREPL = errors.New("the Mate runs without the REPL")

// ErrBusy is an action the core refused because another action, from this
// UI or another one, is still running on the same target (CON-209):
// nothing of it started. errors.Is finds it in a *BusyError, whose text is
// the whole line a UI shows, verbatim.
var ErrBusy = errors.New("another action is still running on the target")

// BusyError is ErrBusy with what is running and where: Running is the
// action that holds the target, Target the target as a UI names it (a
// Project, `project/crew`, or "the workspace").
type BusyError struct {
	Running Action `json:"running"`
	Target  string `json:"target"`
}

func (e *BusyError) Error() string {
	return "Action refused: " + string(e.Running) + " is still running on " + e.Target + " · nothing started"
}

// Is makes errors.Is(err, ErrBusy) true for a *BusyError.
func (e *BusyError) Is(target error) bool { return target == ErrBusy }
