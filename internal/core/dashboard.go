package core

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync"

	"github.com/nguyenngocanh94/mate/internal/dashboard"
	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// DashboardOptions is the dashboard (docs/mvp.md M6) the engine serves while
// it runs: Mate Office and Agent City.
type DashboardOptions struct {
	// Addr is the bind address, the dashboard's default when empty; a test
	// passes its own so it never depends on what this machine already
	// serves.
	Addr string
	// AllowRemote is .mate/console.yaml's dashboard.allow_remote: without it
	// a routable Addr is refused the way `mate dashboard` refuses one.
	AllowRemote bool
	// Socket, when set, is a unix socket the same routes are served on as
	// well: `.mate/core.sock` for `mate core` (CON-211). The engine removes
	// a socket file left there, which only the holder of core.lock may do,
	// and does not start when it cannot serve the socket.
	Socket string
	// Deps are the answers only the caller holds (cmd/mate's dashboardDeps:
	// `mate diff`, the crew's branch, the harnesses' plugins). The engine
	// fills in the rest: Snapshot and FreshSnapshot are its own,
	// TimelineStale is its ingest's, Act is Engine.Act whatever the caller
	// put there (CON-209), and Notice, whether the pages offer Jev
	// (CON-141), is whether Options.Notice is set (CON-232).
	Deps dashboard.Deps
	// ReadOnly serves the pages with no snapshot and no action: they read
	// mate.db and the workspace's files alone. Only a test of the server
	// itself, which has no Herdr to read a snapshot through, asks for it.
	ReadOnly bool
}

// dashboardPart serves the dashboard as one of the engine's parts. Over
// TCP it is a convenience: when it cannot bind - the port is busy, most
// likely with another workspace's engine or `mate dashboard`, or the
// address is refused - it says why (err) and the engine runs on without
// it. The socket of `mate core` is not: when it cannot be served, the
// engine does not start (CON-211).
type dashboardPart struct {
	ws   *store.Workspace
	o    DashboardOptions
	deps func() dashboard.Deps

	// socket is the socket's listener from bindSocket until Start serves
	// it; release closes it when Start never came. Only the engine's Start
	// and stop touch it, under the engine's lock.
	socket net.Listener

	// cancel, done and handle are set by Start and read by Stop: the
	// engine calls each once, Stop on a goroutine it starts after Start
	// has returned.
	cancel context.CancelFunc
	done   chan struct{}
	handle *db.DB

	mu      sync.Mutex
	serving Serving
}

// Serving is where the engine's dashboard serves, once Start has returned.
type Serving struct {
	// URL is the loopback URL over TCP and LANURL the LAN one, when the
	// bind reaches the LAN; both are empty when TCP is not served.
	URL    string
	LANURL string
	// Socket is the unix socket served, when DashboardOptions.Socket asked
	// for one.
	Socket string
	// Err is why the dashboard is not served over TCP, or not at all.
	Err error
}

// bindSocket binds DashboardOptions.Socket, when one is asked for, before
// any part starts: the engine calls it right after it takes core.lock, which
// makes a socket file left there by an engine that died this one's to
// remove. A socket that cannot be bound - its path too long, a filesystem
// that has none (a WSL2 workspace under /mnt/c) - is the engine's error.
func (d *dashboardPart) bindSocket() error {
	if d.o.Socket == "" {
		return nil
	}
	ln, err := dashboard.ListenUnix(d.o.Socket)
	if err != nil {
		return err
	}
	d.socket = ln
	return nil
}

// release closes the socket bindSocket bound when Start never served it,
// which removes its file.
func (d *dashboardPart) release() {
	if d.socket != nil {
		_ = d.socket.Close()
		d.socket = nil
	}
}

// Start opens the database read-only, with db.OpenRead, which takes no lock,
// so it runs beside the observer that owns the writer, binds and serves.
// Only a socket it cannot serve fails the engine; any other error is kept
// for Engine.Dashboard.
func (d *dashboardPart) Start(ctx context.Context) error {
	serving := d.start(ctx)
	d.mu.Lock()
	d.serving = serving
	d.mu.Unlock()
	if d.o.Socket != "" && serving.Socket == "" {
		return serving.Err
	}
	return nil
}

func (d *dashboardPart) start(ctx context.Context) Serving {
	// The socket is this part's to serve or to close from here on.
	socket := d.socket
	d.socket = nil
	handle, err := db.OpenRead(d.ws)
	if err != nil {
		closeListener(socket)
		return Serving{Err: err}
	}
	opts := dashboard.Options{Workspace: d.ws, DB: handle, Deps: d.deps(), Addr: d.o.Addr, AllowRemote: d.o.AllowRemote}
	server, err := dashboard.New(opts)
	if err != nil {
		closeListener(socket)
		_ = handle.Close()
		return Serving{Err: err}
	}
	// listener is one bind and how it is served: the socket with the
	// client routes too (CON-212), TCP without them.
	type listener struct {
		ln    net.Listener
		serve func(context.Context, net.Listener) error
	}
	var (
		serving Serving
		lns     []listener
	)
	if socket != nil {
		lns = append(lns, listener{socket, server.ServeSocket})
		serving.Socket = d.o.Socket
	}
	if ln, err := dashboard.Listen(opts); err != nil {
		serving.Err = fmt.Errorf("dashboard not started: %w", err)
		if len(lns) == 0 {
			_ = handle.Close()
			return serving
		}
	} else {
		lns = append(lns, listener{ln, server.Serve})
		serving.URL = dashboard.URL(ln)
		serving.LANURL = dashboard.LANURL(ln)
	}
	// Where it serves is known before the first request: GET
	// coreapi.PathCore says it (Engine.Status).
	d.mu.Lock()
	d.serving = serving
	d.mu.Unlock()
	serveCtx, cancel := context.WithCancel(ctx)
	// The goroutines close channels made here, never d.done (the CON-207
	// race); done closes once every listener has stopped serving.
	var wg sync.WaitGroup
	for _, l := range lns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = l.serve(serveCtx, l.ln)
		}()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	d.cancel, d.done, d.handle = cancel, done, handle
	return serving
}

// Stop stops serving and waits for the requests in flight, five seconds at
// most (dashboard.Server.Serve), then closes the database. Closing the
// socket's listener removes its file; it is removed again in case serving
// ended before the listener was ever closed. A dashboard that did not
// start has nothing to stop.
func (d *dashboardPart) Stop() {
	if d.cancel == nil {
		return
	}
	d.cancel()
	<-d.done
	_ = d.handle.Close()
	if d.o.Socket != "" {
		_ = os.Remove(d.o.Socket)
	}
	d.cancel, d.done, d.handle = nil, nil, nil
}

func closeListener(ln net.Listener) {
	if ln != nil {
		_ = ln.Close()
	}
}

func (d *dashboardPart) read() Serving {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.serving
}

// dashboardDeps is the caller's dashboard.Deps with the engine's own answers
// in: the snapshot every UI reads, the fresh one a pressed button is checked
// against, the ingest's stale timelines, whether there is Jev to ask
// (Options.Notice), and Engine.Act, the one way in for an action from any
// UI: under the engine's locks, and followed by Invalidate so the next read
// sees what it changed. A ReadOnly dashboard gets none of the snapshots,
// Jev or Act.
func (e *Engine) dashboardDeps() dashboard.Deps {
	deps := e.o.Dashboard.Deps
	if ingest := e.o.Ingest; ingest != nil {
		// The ingest is this process's; an engine that is not the writer
		// has none and the page says nothing about staleness.
		deps.TimelineStale = ingest.Stale
	}
	deps.Core = e.Status
	deps.ClientSeen = e.clientSeen
	if e.o.Dashboard.ReadOnly {
		deps.Snapshot, deps.FreshSnapshot, deps.Act, deps.Notice = nil, nil, nil, false
		return deps
	}
	deps.Snapshot = e.Snapshot
	deps.FreshSnapshot = e.FreshSnapshot
	deps.Act = e.Act
	deps.Notice = e.o.Notice != nil
	return deps
}
