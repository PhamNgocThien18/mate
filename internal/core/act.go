package core

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/matehost"
)

// Act runs one action a UI asks for and returns the one line that UI shows
// for it (CON-209). It is the one way in for every UI: the console's keys
// and sheet, and every press on a Mate Office or Agent City page, chat
// included.
//
// It takes the action's target first (lockOf). While another action holds
// that target, from whichever UI, Act refuses with a *coreapi.BusyError
// and starts nothing. The chat runs here, on the headless Mate's host
// (talk); the Mate's and the Project's actions run here too (ownAction,
// CON-231); every other action runs Options.Act, cmd/mate's. Once the action
// has returned, whatever it returned, the cached snapshot is dropped
// (Invalidate) - an action that failed halfway can have changed something
// too - and only then is the target released, so the next action on it
// reads what this one did.
//
// ctx is the caller's: the console cancels it when it quits in the middle
// of an action, and a page's press does not end with its request
// (dashboard's handleAct). Stop cancels it either way, and waits for the
// action within its bound (CON-210); once Stop has begun, Act refuses with
// coreapi.ErrStopping and starts nothing.
func (e *Engine) Act(ctx context.Context, req coreapi.ActionRequest) (string, error) {
	key, shown := lockOf(req)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	run, err := e.acts.begin("action "+string(req.Action)+" on "+shown, cancel)
	if err != nil {
		return "", err
	}
	defer e.acts.end(run)
	if err := e.locks.take(key, shown, req.Action); err != nil {
		return "", err
	}
	defer e.locks.release(key)
	defer e.Invalidate()
	if chat[req.Action] {
		return e.talk(ctx, req)
	}
	if req.Action == coreapi.ActionOkayWork {
		if line, held, err := e.okayOnHost(ctx, req); held {
			return line, err
		}
	}
	if run := e.own(req); run != nil {
		if e.o.Workspace == nil {
			return "", errNoWorkspace
		}
		return run(ctx, req)
	}
	if e.o.Act == nil {
		return "", fmt.Errorf("%s is not wired in this engine", req.Action)
	}
	return e.o.Act(ctx, req)
}

// errNoWorkspace is an action of the engine's own on an engine built with
// no workspace, refused the way the chat refuses (mateHost).
var errNoWorkspace = errors.New("the engine has no workspace to act in")

// ownAction is the engine's own body for req (CON-231): the Mate's actions
// (act_mate.go) and the Project's (act_project.go). It is nil for every
// other action, which Options.Act runs until CON-232 moves the rest in: a
// crew's actions, the box's and Jev's. An onboard of the workspace adds a
// Project; any other onboard starts the Project's Mate.
func (e *Engine) ownAction(req coreapi.ActionRequest) coreapi.ActionFunc {
	switch req.Action {
	case coreapi.ActionStart, coreapi.ActionResume:
		return e.startMate
	case coreapi.ActionOnboard:
		if req.TargetKind == "workspace" {
			return e.addProject
		}
		return e.startMate
	case coreapi.ActionStop:
		if req.TargetKind == "crew" {
			return nil
		}
		return e.stopMate
	case coreapi.ActionRepair:
		return e.repairMate
	case coreapi.ActionMode:
		return e.toggleMode
	case coreapi.ActionRestartMate:
		return e.restartMate
	case coreapi.ActionClearComposer:
		return e.clearComposer
	case coreapi.ActionRemoveProject:
		return e.removeProject
	case coreapi.ActionOpenNews:
		return e.openNews
	case coreapi.ActionOpenWork:
		return e.openWork
	case coreapi.ActionOkayWork:
		return e.okayWork
	}
	return nil
}

// chat are the actions that talk to a headless Mate (CON-142): they run on
// its host, in its own process, and never reach Options.Act.
var chat = map[coreapi.Action]bool{
	coreapi.ActionSay: true, coreapi.ActionInterrupt: true, coreapi.ActionAnswer: true,
	coreapi.ActionFork: true, coreapi.ActionForkClose: true, coreapi.ActionUndo: true,
}

// ownLock are the actions that hold a lock of their own on their target,
// `<target>#<action>`: talking to the Mate, reading a screen, asking Jev
// about it, ticking the work board and opening a news or the board in the
// Mate's REPL neither wait for nor hold up a stop or a merge on the same
// Mate or crew. Two of the same kind on one target still refuse each other,
// so a double click runs once.
var ownLock = map[coreapi.Action]bool{
	coreapi.ActionSay: true, coreapi.ActionInterrupt: true, coreapi.ActionAnswer: true,
	coreapi.ActionFork: true, coreapi.ActionForkClose: true, coreapi.ActionUndo: true,
	coreapi.ActionPeek: true, coreapi.ActionNotice: true, coreapi.ActionOkayWork: true,
	coreapi.ActionOpenNews: true, coreapi.ActionOpenWork: true,
}

// lockOf is the lock an action takes and how a refusal names its target.
// The target is the Project, or `project/crew` when the request names a
// crew; an onboard that adds a new Project to the workspace takes the one
// workspace lock, since the Project it names is not there yet. These are
// the targets the dashboard locked on before CON-209. The workspace's key
// starts with "#", which no Project name does (store.ValidateProjectName),
// so a Project named "workspace" has a lock of its own.
func lockOf(req coreapi.ActionRequest) (key, shown string) {
	if req.Action == coreapi.ActionOnboard && req.TargetKind == "workspace" {
		key, shown = "#workspace", "the workspace"
	} else {
		key = req.Target
		if req.Crew != "" {
			key += "/" + req.Crew
		}
		shown = key
	}
	if ownLock[req.Action] {
		key += "#" + string(req.Action)
	}
	return key, shown
}

// actionLocks is which targets have an action running, and which action.
type actionLocks struct {
	mu      sync.Mutex
	running map[string]coreapi.Action
}

// take holds key for action, or says which action already holds it.
func (l *actionLocks) take(key, shown string, action coreapi.Action) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if busy, ok := l.running[key]; ok {
		return &coreapi.BusyError{Running: busy, Target: shown}
	}
	if l.running == nil {
		l.running = map[string]coreapi.Action{}
	}
	l.running[key] = action
	return nil
}

func (l *actionLocks) release(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.running, key)
}

// actionRuns is every action in flight, for Stop to cancel and wait for.
type actionRuns struct {
	mu sync.Mutex
	// stopping is set by close: from then on begin refuses, so the set
	// close handed to Stop is all there will ever be.
	stopping bool
	next     uint64
	running  map[uint64]*actionRun
}

// actionRun is one action in flight: name is how Stop names it when it has
// not stopped in time, cancel ends its context, and done closes once it
// has returned.
type actionRun struct {
	id     uint64
	name   string
	cancel context.CancelFunc
	done   chan struct{}
}

// begin counts an action in, or refuses it once the engine is stopping.
func (a *actionRuns) begin(name string, cancel context.CancelFunc) (*actionRun, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stopping {
		return nil, coreapi.ErrStopping
	}
	if a.running == nil {
		a.running = map[uint64]*actionRun{}
	}
	a.next++
	run := &actionRun{id: a.next, name: name, cancel: cancel, done: make(chan struct{})}
	a.running[run.id] = run
	return run, nil
}

// end counts an action out once it has returned.
func (a *actionRuns) end(run *actionRun) {
	a.mu.Lock()
	delete(a.running, run.id)
	a.mu.Unlock()
	close(run.done)
}

// close refuses every action from now on, cancels the ones in flight and
// returns them, oldest first, for Stop to wait on.
func (a *actionRuns) close() []*actionRun {
	a.mu.Lock()
	a.stopping = true
	runs := make([]*actionRun, 0, len(a.running))
	for _, run := range a.running {
		runs = append(runs, run)
	}
	a.mu.Unlock()
	slices.SortFunc(runs, func(x, y *actionRun) int { return cmp.Compare(x.id, y.id) })
	for _, run := range runs {
		run.cancel()
	}
	return runs
}

// The two refusals of a chat to a Mate it cannot reach, in the words the
// pages show on a disabled chat (dashboard's mateMore).
var (
	errMateNotRunning  = errors.New("the Mate is not running · start it first")
	errMateNotHeadless = errors.New("this Mate is not headless · type in its pane")
)

// talk runs a chat action on the headless Mate's host, the way its REPL
// runs it. Whether the Mate runs is its record's word (mate.meta names an
// agent), not Herdr's: the host is the Mate's own process, so a host that
// is gone says so in its own error. The host holds a say and an answer
// for their undo (CON-222), and an undo takes one back.
func (e *Engine) talk(ctx context.Context, req coreapi.ActionRequest) (string, error) {
	c, err := e.mateHost(req.Target)
	if err != nil {
		return "", err
	}
	project := req.Target
	switch req.Action {
	case coreapi.ActionSay:
		held, err := c.Submit(ctx, req.Input)
		if err != nil {
			return "", err
		}
		if held.ID == 0 {
			// A host older than the hold ran or queued it at once.
			return "sent to the Mate of " + project, nil
		}
		return fmt.Sprintf("goes to the Mate of %s in %s · undo until then", project, undoIn()), nil
	case coreapi.ActionInterrupt:
		stopped, err := c.Interrupt(ctx, req.News)
		switch {
		case err != nil:
			return "", err
		case !stopped:
			return "nothing was running · nothing changed", nil
		case req.News > 0:
			return fmt.Sprintf("interrupted the session of news #%d", req.News), nil
		}
		return "interrupted the Mate's turn", nil
	case coreapi.ActionAnswer:
		held, err := c.AnswerNews(ctx, req.News, req.Input)
		if err != nil {
			return "", err
		}
		if held.ID == 0 {
			return fmt.Sprintf("answer to news #%d sent to the Mate of %s", req.News, project), nil
		}
		return fmt.Sprintf("answer to news #%d goes to the Mate of %s in %s · undo until then", req.News, project, undoIn()), nil
	case coreapi.ActionUndo:
		id, err := strconv.Atoi(req.Input)
		if err != nil || id <= 0 {
			return "", fmt.Errorf("undo names no held item: %q", req.Input)
		}
		held, err := c.Undo(ctx, id)
		if err != nil {
			return "", err
		}
		switch held.Kind {
		case matehost.HeldAnswer:
			return fmt.Sprintf("took back the answer to news #%d · nothing sent", held.News), nil
		case matehost.HeldOkay:
			return fmt.Sprintf("took back the OK of work #%d · the row is still done", held.Work), nil
		}
		return "took back the message to the Mate of " + project + " · nothing sent", nil
	case coreapi.ActionFork:
		if err := c.AskSession(ctx, req.News, req.Input); err != nil {
			return "", err
		}
		return fmt.Sprintf("asked the session of news #%d; it answers read-only and changes nothing", req.News), nil
	case coreapi.ActionForkClose:
		if err := c.CloseSession(ctx, req.News); err != nil {
			return "", err
		}
		return fmt.Sprintf("session of news #%d closed; what was said in it stays", req.News), nil
	}
	return "", fmt.Errorf("%s is not a chat action", req.Action)
}

// mateHost is the host of project's Mate, when the Mate runs headless.
func (e *Engine) mateHost(project string) (matehost.Client, error) {
	ws := e.o.Workspace
	if ws == nil {
		return matehost.Client{}, errors.New("the engine has no workspace to talk in")
	}
	if _, ok := ws.Project(project); !ok {
		return matehost.Client{}, fmt.Errorf("project %q is not in the workspace", project)
	}
	// A missing mate.meta reads empty; an error is a record that could not
	// be read, which is said as it is.
	meta, err := ws.ReadMateMeta(project)
	if err != nil {
		return matehost.Client{}, err
	}
	if strings.TrimSpace(meta["agent"]) == "" {
		return matehost.Client{}, errMateNotRunning
	}
	if !ws.MateHeadless(project) {
		return matehost.Client{}, errMateNotHeadless
	}
	return matehost.Dial(ws.MateDir(project)), nil
}

// okayOnHost hands a tick on the work board to the headless Mate's host,
// which holds it for its undo like a message (CON-222). held is false
// where no host takes it - no headless Mate, no host to dial, or a host
// older than work-okay - and the tick then runs at once through
// Options.Act, as it did before. A host that was reached and did not
// answer in time may hold it already, so that is an error, not a second
// tick.
func (e *Engine) okayOnHost(ctx context.Context, req coreapi.ActionRequest) (line string, held bool, err error) {
	id, convErr := strconv.Atoi(req.Input)
	if convErr != nil || id <= 0 || e.o.Workspace == nil {
		return "", false, nil
	}
	c, err := e.mateHost(req.Target)
	if err != nil {
		return "", false, nil
	}
	h, err := c.OkayWork(ctx, id)
	switch {
	case err != nil && noHost(err):
		return "", false, nil
	case err != nil:
		return "", true, err
	case h.ID == 0:
		return fmt.Sprintf("okayed work #%d of %s; it is off the board", id, req.Target), true, nil
	}
	return fmt.Sprintf("OK of work #%d of %s goes in %s · undo until then", id, req.Target, undoIn()), true, nil
}

// noHost reports whether err says the host took nothing: there was none
// to dial, or it does not know the op.
func noHost(err error) bool {
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return true
	}
	return matehost.Refused(err) && strings.Contains(err.Error(), "unknown op")
}

// undoIn is the undo's time, as a line says it.
func undoIn() string { return fmt.Sprintf("%d s", int(matehost.UndoDelay/time.Second)) }
