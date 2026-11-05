package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

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
// (talk); every other action runs Options.Act, cmd/mate's. Once the action
// has returned, whatever it returned, the cached snapshot is dropped
// (Invalidate) - an action that failed halfway can have changed something
// too - and only then is the target released, so the next action on it
// reads what this one did.
//
// ctx is the caller's: the console cancels it when it quits in the middle
// of an action, and a page's press runs to the end (dashboard's handleAct).
func (e *Engine) Act(ctx context.Context, req coreapi.ActionRequest) (string, error) {
	key, shown := lockOf(req)
	if err := e.locks.take(key, shown, req.Action); err != nil {
		return "", err
	}
	defer e.locks.release(key)
	defer e.Invalidate()
	if chat[req.Action] {
		return e.talk(ctx, req)
	}
	if e.o.Act == nil {
		return "", fmt.Errorf("%s is not wired in this engine", req.Action)
	}
	return e.o.Act(ctx, req)
}

// chat are the actions that talk to a headless Mate (CON-142): they run on
// its host, in its own process, and never reach Options.Act.
var chat = map[coreapi.Action]bool{
	coreapi.ActionSay: true, coreapi.ActionInterrupt: true, coreapi.ActionAnswer: true,
	coreapi.ActionFork: true, coreapi.ActionForkClose: true,
}

// ownLock are the actions that hold a lock of their own on their target,
// `<target>#<action>`: talking to the Mate, reading a screen, asking Jev
// about it, ticking the work board and opening a news or the board in the
// Mate's REPL neither wait for nor hold up a stop or a merge on the same
// Mate or crew. Two of the same kind on one target still refuse each other,
// so a double click runs once.
var ownLock = map[coreapi.Action]bool{
	coreapi.ActionSay: true, coreapi.ActionInterrupt: true, coreapi.ActionAnswer: true,
	coreapi.ActionFork: true, coreapi.ActionForkClose: true,
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

// The two refusals of a chat to a Mate it cannot reach, in the words the
// pages show on a disabled chat (dashboard's mateMore).
var (
	errMateNotRunning  = errors.New("the Mate is not running · start it first")
	errMateNotHeadless = errors.New("this Mate is not headless · type in its pane")
)

// talk runs a chat action on the headless Mate's host, the way its REPL
// runs it. Whether the Mate runs is its record's word (mate.meta names an
// agent), not Herdr's: the host is the Mate's own process, so a host that
// is gone says so in its own error.
func (e *Engine) talk(ctx context.Context, req coreapi.ActionRequest) (string, error) {
	ws := e.o.Workspace
	if ws == nil {
		return "", errors.New("the engine has no workspace to talk in")
	}
	project := req.Target
	if _, ok := ws.Project(project); !ok {
		return "", fmt.Errorf("project %q is not in the workspace", project)
	}
	// A missing mate.meta reads empty; an error is a record that could not
	// be read, which is said as it is.
	meta, err := ws.ReadMateMeta(project)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(meta["agent"]) == "" {
		return "", errMateNotRunning
	}
	if !ws.MateHeadless(project) {
		return "", errMateNotHeadless
	}
	c := matehost.Dial(ws.MateDir(project))
	switch req.Action {
	case coreapi.ActionSay:
		st, err := c.Submit(ctx, req.Input)
		if err != nil {
			return "", err
		}
		if st.Queued > 0 {
			return fmt.Sprintf("queued for the Mate of %s, behind the turn in flight (%d waiting)", project, st.Queued), nil
		}
		return "sent to the Mate of " + project, nil
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
		if err := c.AnswerNews(ctx, req.News, req.Input); err != nil {
			return "", err
		}
		return fmt.Sprintf("answer to news #%d sent to the Mate of %s", req.News, project), nil
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
