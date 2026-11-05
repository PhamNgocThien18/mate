package core

import (
	"sync"
	"time"

	"github.com/nguyenngocanh94/mate/internal/coreapi"
)

// What the core tells its clients about itself on its socket (CON-212):
// GET coreapi.PathCore, and whether a console is reaching it.

// ConsoleWindow is how long a console counts as open after its last request
// on the core's socket. The console reads the snapshot every 2 seconds, so
// a console that is open is seen well inside it.
const ConsoleWindow = 10 * time.Second

// ConsoleSeen is when a console last reached the core over its socket: a
// request with coreapi.ClientHeader set to coreapi.ClientConsole. Present
// is `mate core`'s Options.ConsolePresent: the outbox loop types nothing
// into a Manual project's Mate while it says false (CON-203, decided
// 2026-11-05). Safe for any goroutine; the zero value has seen nothing.
type ConsoleSeen struct {
	// now is time.Now; a test moves it.
	now func() time.Time

	mu   sync.Mutex
	last time.Time
}

// See records a console's request now.
func (c *ConsoleSeen) See() {
	t := c.clock()
	c.mu.Lock()
	defer c.mu.Unlock()
	if t.After(c.last) {
		c.last = t
	}
}

// Present is true while a console was seen within the last ConsoleWindow.
func (c *ConsoleSeen) Present() bool {
	c.mu.Lock()
	last := c.last
	c.mu.Unlock()
	return !last.IsZero() && c.clock().Sub(last) < ConsoleWindow
}

func (c *ConsoleSeen) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// clientSeen is told the client a socket request names
// (dashboard.Deps.ClientSeen).
func (e *Engine) clientSeen(client string) {
	if client == coreapi.ClientConsole && e.o.ConsoleSeen != nil {
		e.o.ConsoleSeen.See()
	}
}

// Status is the core's own state, GET coreapi.PathCore: who runs it, as
// core.lock records it, where its dashboard serves, whether Jev is there
// (the pages' Deps.Notice, which offers explain_notice), and the lines a
// console's status line says about it. Its record is zero for an engine
// that holds no core.lock.
func (e *Engine) Status() coreapi.CoreStatus {
	h := e.holder
	s := coreapi.CoreStatus{
		PID: h.PID, Owner: h.Owner, Version: h.Version, Build: h.Build,
		Binary: h.Binary, Started: h.Started, Notices: []string{},
	}
	if e.o.Dashboard != nil {
		s.Jev = e.o.Dashboard.Deps.Notice
	}
	serving := e.Serving()
	s.URL, s.LANURL = serving.URL, serving.LANURL
	if serving.Err != nil {
		s.TCPError = serving.Err.Error()
		s.Notices = append(s.Notices, serving.Err.Error())
	}
	if line, err := e.Notifications(); err != nil {
		s.Notices = append(s.Notices, "notifications: "+err.Error())
	} else if line != "" {
		s.Notices = append(s.Notices, line)
	}
	s.Notices = append(s.Notices, e.o.Notices...)
	return s
}
