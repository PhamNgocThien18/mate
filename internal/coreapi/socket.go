package coreapi

import "time"

// The routes a client of the core reaches on `.mate/core.sock` (CON-212),
// and only there: over TCP each answers 404. A client is a program of
// this user's on this machine (the socket is 0600): the console from
// CON-214, `mate core status` from CON-213. The wire is HTTP with JSON
// bodies; the bodies are this package's types in Go's default encoding,
// and a client and a core of different builds are not promised to agree
// (CON-214 refuses to run against a core of another build).
const (
	// PathCore is GET: the core's own state, a CoreStatus.
	PathCore = "/api/core"
	// PathSnapshot is GET: the engine's snapshot, a query.Snapshot with
	// no Commands (those are the console's own keys).
	PathSnapshot = "/api/snapshot"
	// PathAct is POST: an ActionRequest, run to its end even when the
	// client goes away; answered with an ActResult.
	PathAct = "/api/act"
)

// ClientHeader names the program a request on the core's socket comes
// from. A request saying ClientConsole tells the core a console is open
// (core's ConsoleSeen): a Manual project's Mate gets the outbox's lines
// only while one is.
const ClientHeader = "X-Mate-Client"

// ClientConsole is ClientHeader's value for `mate console`.
const ClientConsole = "console"

// CoreStatus is the answer to GET PathCore: who runs the engine and where
// it serves, as core.lock records it, and what the console's status line
// would say about it.
type CoreStatus struct {
	PID   int    `json:"pid"`
	Owner string `json:"owner"`
	// Version is the mate version, Build the sha256 of the executable the
	// core started from and Binary its path: a newer install is told from
	// the build that runs by Build.
	Version string    `json:"version"`
	Build   string    `json:"build"`
	Binary  string    `json:"binary"`
	Started time.Time `json:"started"`
	// URL and LANURL are where Office and City are served over TCP, empty
	// when they are not; TCPError is why not.
	URL      string `json:"url"`
	LANURL   string `json:"lan_url"`
	TCPError string `json:"tcp_error"`
	// Jev is whether Jev is configured, to explain a crew's screen.
	Jev bool `json:"jev"`
	// Notices are the lines a console shows on its status line about the
	// core, in its order: the dashboard's error, the notifications', then
	// Jev's and the observer's configuration errors. Empty when all is well.
	Notices []string `json:"notices"`
}

// ActResult is the answer to a POST PathAct that ran: the one line a UI
// shows for it.
type ActResult struct {
	Message string `json:"message"`
}

// ErrorBody is every refusal and failure on the core's socket. Error is
// the sentence a UI shows, verbatim; Code says which of this package's
// errors it is, so a client hands errors.Is the same error the engine
// returned. Busy is the refusal's detail when Code is CodeBusy.
type ErrorBody struct {
	Error string     `json:"error"`
	Code  string     `json:"code"`
	Busy  *BusyError `json:"busy,omitempty"`
}

// The codes of ErrorBody. An action that ran and failed for any other
// reason has none.
const (
	// CodeBusy is ErrBusy, as a 409.
	CodeBusy = "busy"
	// CodeNoMateREPL is ErrNoMateREPL, as a 422.
	CodeNoMateREPL = "no_mate_repl"
	// CodeStopping is ErrStopping: an action or a snapshot asked while the
	// core stops, as a 409 or a 503.
	CodeStopping = "stopping"
)
