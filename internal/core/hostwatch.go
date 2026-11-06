package core

import (
	"context"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/notifications"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/recovery"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// The core watches the host of every headless Mate that runs as a process
// of its own (CON-226): no Herdr is there to say it died (CON-225). A host
// found dead is started again on its session through recovery, a few times
// at most; then the captain is told and the core leaves it.

// HostWatchEvery is how often the core asks each host whether it is alive.
const HostWatchEvery = 5 * time.Second

// A Mate's host is started again at most hostRestartLimit times within
// hostRestartWindow; the next death within it is left to the captain.
const (
	hostRestartLimit  = 3
	hostRestartWindow = 10 * time.Minute
)

// hostWatch is the engine's loop over the hosts. What tells a death from a
// stop is mate.meta: a stop drops the host's `agent`, `pid` and `socket`
// (spawn.StopMate), and a Mate whose meta no longer records a host of its
// own (store.MateHostDetached) is not watched. A stop's own window - its
// host gone, its meta not yet rewritten - is why a host must be found dead
// twice in a row, under the same pid, before anything is started.
type hostWatch struct {
	ws    *store.Workspace
	every time.Duration
	now   func() time.Time
	// alive asks the host of project (spawn.HostAlive): asked is false
	// when the question was cut short.
	alive func(ctx context.Context, project string) (alive, asked bool)
	// restart starts project's Mate again when mate.meta still records the
	// dead host pid (recovery.RestartHost); acted is false when there was
	// nothing left to do.
	restart func(ctx context.Context, project, pid string) (recovery.Item, bool)
	// hold takes the engine's action lock on project for the restart; ok
	// is false while an action of a UI holds it, which then decides.
	hold func(project string) (release func(), ok bool)
	// changed drops the engine's cached snapshot once a restart returned.
	changed func()
	// tell sends the captain the notification for a Mate given up on.
	tell func(ctx context.Context, project, line string)
	// log is core.log: one line for every restart and every give-up.
	log io.Writer

	// cancel and done are set by Start and read by Stop: the engine calls
	// each once, Stop on a goroutine it starts after Start has returned.
	cancel context.CancelFunc
	done   chan struct{}

	// mates is the loop's own: only its goroutine touches it.
	mates map[string]*watchedHost

	mu      sync.Mutex
	notices map[string]string
}

// watchedHost is what the loop keeps of one Mate's host.
type watchedHost struct {
	// pid is the host mate.meta records; another one is another host.
	pid string
	// suspect is true once the host was found dead, until it is found
	// alive or started again.
	suspect bool
	// restarts are when the loop started this Mate again, within the
	// window.
	restarts []time.Time
	// notice is the line a UI says about this Mate: why it was given up
	// on, or why its last restart failed. Empty while all is well.
	notice string
	// gaveUp is true once the limit was reached, until mate.meta records
	// another host (a start of the captain's) or none.
	gaveUp bool
}

// Start begins the loop and returns at once; it cannot fail.
func (h *hostWatch) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	h.cancel, h.done = cancel, done
	go func() {
		defer close(done)
		timer := time.NewTimer(0)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			h.check(ctx)
			timer.Reset(h.every)
		}
	}()
	return nil
}

// Stop ends the loop and waits for the check in flight; a restart it
// cancels halfway is undone by spawn and put right by the next recovery.
// It sends nothing to any host: a host outlives the core (CON-216), and
// the next core finds it alive on its socket.
func (h *hostWatch) Stop() {
	if h.cancel == nil {
		return
	}
	h.cancel()
	<-h.done
}

// check looks at every project's Mate once.
func (h *hostWatch) check(ctx context.Context) {
	if h.mates == nil {
		h.mates = map[string]*watchedHost{}
	}
	watched := map[string]bool{}
	for _, ref := range h.ws.Projects() {
		if ctx.Err() != nil {
			return
		}
		meta, err := h.ws.ReadMateMeta(ref.Name)
		if err != nil {
			// Unread is not stopped: what the loop kept stays.
			watched[ref.Name] = h.mates[ref.Name] != nil
			continue
		}
		if !store.MateHostDetached(meta) {
			continue
		}
		watched[ref.Name] = true
		h.checkOne(ctx, ref.Name, meta[store.MetaMatePID])
	}
	for project := range h.mates {
		if !watched[project] {
			// Stopped, removed, or no longer a host of its own.
			delete(h.mates, project)
		}
	}
	h.publish()
}

// checkOne looks at project's host, which mate.meta records as pid.
func (h *hostWatch) checkOne(ctx context.Context, project, pid string) {
	m := h.mates[project]
	if m == nil {
		m = &watchedHost{pid: pid}
		h.mates[project] = m
	}
	if m.pid != pid {
		// Another host: the loop's own restart, recovery's, or a start of
		// the captain's, which gives a Mate given up on a new count.
		if m.gaveUp {
			*m = watchedHost{}
		}
		m.pid, m.suspect, m.notice = pid, false, ""
	}
	if m.gaveUp {
		return
	}
	alive, asked := h.alive(ctx, project)
	if !asked {
		return
	}
	if alive {
		m.suspect = false
		return
	}
	if !m.suspect {
		m.suspect = true
		return
	}
	m.suspect = false
	now := h.now()
	m.restarts = slices.DeleteFunc(m.restarts, func(at time.Time) bool { return now.Sub(at) >= hostRestartWindow })
	if len(m.restarts) >= hostRestartLimit {
		m.gaveUp = true
		m.notice = fmt.Sprintf("Mate %s: host died %d times in %dm; not restarting", project, len(m.restarts)+1, int(hostRestartWindow/time.Minute))
		h.say(m.notice + "; start it again by hand")
		h.tell(ctx, project, m.notice+"; start it again by hand")
		return
	}
	release, ok := h.hold(project)
	if !ok {
		return
	}
	item, acted := h.restart(ctx, project, pid)
	release()
	h.changed()
	if !acted {
		return
	}
	m.restarts = append(m.restarts, now)
	if item.Err != nil {
		m.notice = fmt.Sprintf("Mate %s: host pid %s died; restart failed: %s", project, pid, firstLine(item.Err.Error()))
		h.say(m.notice)
		return
	}
	m.notice = ""
	resumed := "resumed its session"
	switch {
	case item.Note != "":
		resumed = item.Note
	case !item.Resumed:
		resumed = "started a fresh session"
	}
	h.say(fmt.Sprintf("Mate %s: host pid %s died; started again (%d of %d in %dm), %s", project, pid,
		len(m.restarts), hostRestartLimit, int(hostRestartWindow/time.Minute), resumed))
}

func (h *hostWatch) say(line string) {
	if h.log != nil {
		fmt.Fprintln(h.log, "mate core: "+line)
	}
}

// publish hands the notices to the readers.
func (h *hostWatch) publish() {
	notices := map[string]string{}
	for project, m := range h.mates {
		if m.notice != "" {
			notices[project] = m.notice
		}
	}
	h.mu.Lock()
	h.notices = notices
	h.mu.Unlock()
}

// read is the notice of each Mate that has one, by project.
func (h *hostWatch) read() map[string]string {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.notices
}

// newHostWatch is the engine's watch over its workspace's hosts: liveness
// and the restart through spawn and recovery over Options.Spawn, the
// restart under the engine's action lock on the Project.
func (e *Engine) newHostWatch() *hostWatch {
	ws, deps := e.o.Workspace, e.o.Spawn
	return &hostWatch{
		ws:    ws,
		every: HostWatchEvery,
		now:   time.Now,
		alive: func(ctx context.Context, project string) (bool, bool) {
			return spawn.HostAlive(ctx, ws, project)
		},
		restart: func(ctx context.Context, project, pid string) (recovery.Item, bool) {
			return recovery.RestartHost(ctx, ws, deps, project, pid)
		},
		hold: func(project string) (func(), bool) {
			if e.locks.take(project, project, coreapi.ActionRestartMate) != nil {
				return nil, false
			}
			return func() { e.locks.release(project) }, true
		},
		changed: e.Invalidate,
		tell:    e.tellHostGivenUp,
		log:     e.stderr(),
	}
}

// tellHostGivenUp sends the captain the native notification that a Mate's
// host is no longer started again, unless the workspace turned
// notifications off. A click opens the captain's desk in the Office.
func (e *Engine) tellHostGivenUp(ctx context.Context, project, line string) {
	if e.o.Notifier == nil {
		return
	}
	if on, err := notificationsOn(e.o.Workspace); err != nil || !on {
		return
	}
	msg := notifications.Message{ID: "mate-host/" + project, Title: "Mate " + project, Body: line}
	if fields := strings.Fields(e.dashboardURL()); len(fields) > 0 {
		msg.URL = strings.TrimRight(fields[0], "/") + "/office/#captain"
	}
	if err := e.o.Notifier.Send(ctx, msg); err != nil {
		fmt.Fprintf(e.stderr(), "mate core: the notification for Mate %s could not be sent: %v\n", project, err)
	}
}

// withHostWatch puts what the watch says about each Mate into a snapshot:
// on the Mate's row as its error, and on the status line beside the
// observer's word about Herdr.
func withHostWatch(snap query.Snapshot, notices map[string]string) query.Snapshot {
	if len(notices) == 0 {
		return snap
	}
	var lines []string
	for p := range snap.Projects {
		project := &snap.Projects[p]
		line, ok := notices[project.ProjectID]
		if !ok {
			continue
		}
		project.Mate.Error = query.KnownField(query.ErrorReason(line))
		lines = append(lines, line)
	}
	if len(lines) > 0 {
		if snap.Runtime.Notice != "" {
			snap.Runtime.Notice += "; "
		}
		snap.Runtime.Notice += strings.Join(lines, "; ")
	}
	return snap
}

// hostNotices are the watch's lines for core status, by project name.
func hostNotices(notices map[string]string) []string {
	var out []string
	for _, p := range slices.Sorted(maps.Keys(notices)) {
		out = append(out, notices[p])
	}
	return out
}
