package dashboard

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/timeline"
)

// DefaultAddr is what `mate dashboard` binds when nobody says otherwise.
// Loopback, and a port nothing else in this toolchain uses.
const DefaultAddr = "127.0.0.1:7777"

// Deps are the two answers the database cannot give, handed in rather than
// imported: the text of `mate diff` for a crew, and whether a crew's
// branch still exists. Both live in the CLI and in `internal/gitx`, and one
// seam keeps the dashboard's own package free of git and of `cmd/mate`.
//
// A nil Deps field is not an error: the endpoint that needs it says so in
// its `reason` rather than failing, which is the same answer a reader gets
// for a branch that was deleted.
type Deps struct {
	// Diff is `mate diff <project> <crew>`, verbatim - cmd/mate wires
	// its own crewDiffText here so the page and the terminal can never
	// disagree about what a branch contains.
	Diff func(ctx context.Context, project, crew string) (string, error)
	// BranchExists reports whether a crew's branch is still in the repo
	// that crew works in; the crew is passed because a project may own
	// several repos and only the crew's meta says which is its.
	BranchExists func(ctx context.Context, project, crew, branch string) (bool, error)
	// ChangedFiles lists the files a crew's work touched: its branch
	// against the default branch while the branch exists, else the merge
	// commit its meta recorded. nil leaves the task page's workflow check
	// unable to tell which steps apply.
	ChangedFiles func(ctx context.Context, project, crew string, branchExists bool, mergeCommit string) ([]string, error)
	// Now is the clock behind `generated_at`; tests freeze it.
	Now func() time.Time
	// PluginHarness says whether a Mate on harness kind can be given its
	// plugins: nil when it can, the reason when it cannot. It is a seam
	// because only a harness profile may know which harness that is. Nil
	// calls no harness unsupported.
	PluginHarness func(kind string) error
	// TimelineStale is the console's ingest saying which projects' timeline
	// stopped moving (CON-139). Nil - `mate dashboard` on its own, or a
	// console that is not the writer - says nothing.
	TimelineStale func() []timeline.Stale
	// Snapshot is the console's own read of the workspace (query.LoadLive
	// and what the console adds to it): the availability its action sheet
	// shows, and the one the action buttons show (CON-141).
	Snapshot coreapi.LoadFunc
	// Act runs one action exactly as the console's action sheet does, and
	// returns its one line. A page's press reaches it as the core's request
	// (coreRequest, CON-206). Act and Snapshot are wired only by the
	// console; with either nil - `mate dashboard` on its own - the
	// dashboard stays read-only and says so.
	Act coreapi.ActionFunc
	// Notice is whether the console has Jev to explain a crew's screen
	// (`explain_notice`); false greys the button out with the reason.
	Notice bool
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

// Options is everything Serve needs. The workspace and the database are
// both required: the workspace answers which projects exist and what mode
// each is in, the database answers everything else.
type Options struct {
	Workspace *store.Workspace
	// DB must be a read-only handle (db.OpenRead). The server never closes
	// it; whoever opened it owns it.
	DB *db.DB
	// Addr is the bind address, DefaultAddr when empty.
	Addr string
	// AllowRemote permits a non-loopback bind. Without it Listen refuses
	// one and says why.
	AllowRemote bool
	Deps        Deps
}

// Server is a bound-but-not-yet-serving dashboard.
type Server struct {
	ws     *store.Workspace
	db     *db.DB
	deps   Deps
	cache  *cache
	agents *agentIndex
	acting *actionLocks
	mux    *http.ServeMux
	// mateMemo holds one project's Mate exchanges for one event id, so
	// every task page's journey does not rebuild them (journey.go).
	mateMemo mateExchangesMemo
}

// New builds the server and its routes. It reads nothing yet: a dashboard
// that refused to start because a project's files were mid-write would be
// worse than one whose first page says so.
func New(opts Options) (*Server, error) {
	if opts.Workspace == nil {
		return nil, errors.New("dashboard: a workspace is required")
	}
	if opts.DB == nil {
		return nil, errors.New("dashboard: a database handle is required")
	}
	if opts.DB.Writable() {
		// Said rather than tolerated: the dashboard's whole claim is that it
		// cannot change the workspace, and a handle holding the writer lock
		// would also keep the console out of its own timeline.
		return nil, errors.New("dashboard: the database handle must be read-only (db.OpenRead)")
	}
	s := &Server{ws: opts.Workspace, db: opts.DB, deps: opts.Deps, cache: newCache(), agents: &agentIndex{}, acting: &actionLocks{}, mux: http.NewServeMux()}
	s.routes()
	return s, nil
}

// Handler is the whole site: the JSON API under /api/ and the embedded UI
// at /.
func (s *Server) Handler() http.Handler { return s.mux }

// Listen binds opts.Addr, refusing a non-loopback address unless
// AllowRemote was set. It is separate from Serve so a caller can print the
// URL a port of 0 actually resolved to.
func Listen(opts Options) (net.Listener, error) {
	addr := opts.Addr
	if strings.TrimSpace(addr) == "" {
		addr = DefaultAddr
	}
	if !opts.AllowRemote {
		if err := requireLoopback(addr); err != nil {
			return nil, err
		}
	}
	return net.Listen("tcp", addr)
}

// requireLoopback is the refusal of docs/mvp.md M6's "chỉ đọc, local": a
// workspace timeline quotes every line the captain typed and names every
// path a crew touched, so binding it to a routable address has to be an
// explicit decision and never a default or a typo.
func requireLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("dashboard: %s is not a host:port address: %w", addr, err)
	}
	host = strings.TrimSpace(host)
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		return fmt.Errorf("dashboard: %s binds every interface; pass --allow-remote if that is really what you want", addr)
	case "localhost":
		return nil
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return fmt.Errorf("dashboard: %s is not a loopback address; pass --allow-remote if that is really what you want", addr)
	}
	if !ip.IsLoopback() {
		return fmt.Errorf("dashboard: %s is not a loopback address; pass --allow-remote if that is really what you want", addr)
	}
	return nil
}

// Serve runs the site on ln until ctx is cancelled, then shuts down.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := &http.Server{
		Handler: s.Handler(),
		// No read or write timeout: /api/events is a long poll and a
		// deadline here would cut it off mid-wait. The wait itself is
		// bounded (maxWaitSeconds), which is the bound that belongs to the
		// protocol rather than to the socket.
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	done := make(chan struct{})
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
		close(done)
	}()
	err := srv.Serve(ln)
	if errors.Is(err, http.ErrServerClosed) {
		<-done
		return nil
	}
	return err
}

// LANURL is the address a device on the same network opens, or "" when the
// bind is not reachable from one: a loopback bind, or an unspecified bind on
// a machine with no private IPv4 address. It picks the first private IPv4
// of a non-loopback interface that is up, which on a laptop is the Wi-Fi
// address; a bind to one specific routable address is reported as itself.
func LANURL(ln net.Listener) string {
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok || addr.IP == nil || addr.IP.IsLoopback() {
		return ""
	}
	host := ""
	if addr.IP.IsUnspecified() {
		ip := firstPrivateIPv4()
		if ip == nil {
			return ""
		}
		host = ip.String()
	} else {
		host = addr.IP.String()
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
	}
	return fmt.Sprintf("http://%s:%d/", host, addr.Port)
}

func firstPrivateIPv4() net.IP {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if ip := ipn.IP.To4(); ip != nil && ip.IsPrivate() {
				return ip
			}
		}
	}
	return nil
}

// URL is the address a reader opens, for the line `mate dashboard` prints.
func URL(ln net.Listener) string {
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return "http://" + ln.Addr().String() + "/"
	}
	host := addr.IP.String()
	if addr.IP == nil || addr.IP.IsUnspecified() {
		host = "127.0.0.1"
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("http://%s:%d/", host, addr.Port)
}
