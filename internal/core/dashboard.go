package core

import (
	"context"
	"fmt"
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
	// Deps are the answers only the caller holds (cmd/mate's dashboardDeps:
	// `mate diff`, the crew's branch, the harnesses' plugins), and Notice,
	// whether the pages offer Jev (CON-141). The engine fills in the rest:
	// Snapshot and FreshSnapshot are its own, TimelineStale is its ingest's,
	// and Act is Engine.Act whatever the caller put there (CON-209). With no
	// Options.Act the dashboard is read-only and reads no snapshot.
	Deps dashboard.Deps
}

// dashboardPart serves the dashboard as one of the engine's parts. It is a
// convenience: when it cannot start - the port is busy, most likely with
// another console or `mate dashboard` on the same workspace, or the address
// is refused - it says why (err) and the engine runs on without it.
type dashboardPart struct {
	ws   *store.Workspace
	o    DashboardOptions
	deps func() dashboard.Deps

	// cancel, done and handle are set by Start and read by Stop: the
	// engine calls each once, Stop on a goroutine it starts after Start
	// has returned.
	cancel context.CancelFunc
	done   chan struct{}
	handle *db.DB

	mu  sync.Mutex
	url string
	err error
}

// Start opens the database read-only, with db.OpenRead, which takes no lock,
// so it runs beside the observer that owns the writer, binds and serves. It
// never fails the engine: an error is kept for Engine.Dashboard.
func (d *dashboardPart) Start(ctx context.Context) error {
	url, err := d.start(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.url, d.err = url, err
	return nil
}

func (d *dashboardPart) start(ctx context.Context) (string, error) {
	handle, err := db.OpenRead(d.ws)
	if err != nil {
		return "", err
	}
	opts := dashboard.Options{Workspace: d.ws, DB: handle, Deps: d.deps(), Addr: d.o.Addr, AllowRemote: d.o.AllowRemote}
	server, err := dashboard.New(opts)
	if err != nil {
		_ = handle.Close()
		return "", err
	}
	ln, err := dashboard.Listen(opts)
	if err != nil {
		_ = handle.Close()
		return "", fmt.Errorf("dashboard not started: %w", err)
	}
	serveCtx, cancel := context.WithCancel(ctx)
	// The goroutine closes the channel made here, never d.done (the
	// CON-207 race).
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve(serveCtx, ln)
	}()
	d.cancel, d.done, d.handle = cancel, done, handle
	// The loopback URL; when the bind is reachable from the LAN it is
	// followed by that address too, so the console's status line shows what
	// a phone on the same network opens.
	url := dashboard.URL(ln)
	if lan := dashboard.LANURL(ln); lan != "" {
		url += " " + lan
	}
	return url, nil
}

// Stop stops serving and waits for the requests in flight, five seconds at
// most (dashboard.Server.Serve), then closes the database. A dashboard that
// did not start has nothing to stop.
func (d *dashboardPart) Stop() {
	if d.cancel == nil {
		return
	}
	d.cancel()
	<-d.done
	_ = d.handle.Close()
	d.cancel, d.done, d.handle = nil, nil, nil
}

func (d *dashboardPart) read() (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.url, d.err
}

// dashboardDeps is the caller's dashboard.Deps with the engine's own answers
// in: the snapshot every UI reads, the fresh one a pressed button is checked
// against, the ingest's stale timelines, and Engine.Act, the one way in for
// an action from any UI: under the engine's locks, and followed by
// Invalidate so the next read sees what it changed.
func (e *Engine) dashboardDeps() dashboard.Deps {
	deps := e.o.Dashboard.Deps
	if ingest := e.o.Ingest; ingest != nil {
		// The ingest is this process's; an engine that is not the writer
		// has none and the page says nothing about staleness.
		deps.TimelineStale = ingest.Stale
	}
	if e.o.Act != nil {
		deps.Snapshot = e.Snapshot
		deps.FreshSnapshot = e.FreshSnapshot
		deps.Act = e.Act
	} else {
		deps.Snapshot, deps.FreshSnapshot, deps.Act, deps.Notice = nil, nil, nil, false
	}
	return deps
}
