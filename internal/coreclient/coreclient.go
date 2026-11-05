// Package coreclient is a client of `mate core` over the workspace's
// `.mate/core.sock` (CON-212): the core's state, its snapshot and its
// actions, in internal/coreapi's types. The console becomes one in CON-214
// and `mate core status` uses one from CON-213.
//
// It is the socket's other end and nothing more: it holds no state of the
// workspace, and every call is one HTTP request. A refusal comes back as
// the error the engine returned where coreapi has one - errors.Is finds
// coreapi.ErrBusy, coreapi.ErrNoMateREPL and coreapi.ErrStopping, and
// errors.As a *coreapi.BusyError - so a UI reads a client's error as it
// reads the engine's.
package coreclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/query"
)

// dialWait bounds Dial's first connection: a socket a live core listens on
// answers at once.
const dialWait = 2 * time.Second

// Client reaches one core on its socket. Safe for concurrent use; each
// request opens a connection or reuses an idle one, so a core that
// restarted on the same socket is reached again by the next call.
type Client struct {
	socket string
	name   string
	http   *http.Client
}

// Option sets how a Client presents itself.
type Option func(*Client)

// As names the program the client is, in coreapi.ClientHeader on every
// request: coreapi.ClientConsole for the console, which is how the core
// knows one is open. A client named nothing (`mate core status`) is not
// counted as a console.
func As(client string) Option {
	return func(c *Client) { c.name = client }
}

// Dial is a client of the core serving socketPath, `.mate/core.sock`. It
// connects once to say whether a core is there: an error is a socket
// nothing listens on (no core runs, or one died and left the file) or one
// that cannot be reached at all.
func Dial(socketPath string, opts ...Option) (*Client, error) {
	conn, err := net.DialTimeout("unix", socketPath, dialWait)
	if err != nil {
		return nil, fmt.Errorf("mate core is not reachable on %s: %w", socketPath, err)
	}
	_ = conn.Close()
	c := &Client{socket: socketPath}
	for _, o := range opts {
		o(c)
	}
	c.http = &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
		// Proxy is nil: the socket is the only host, however the
		// environment sets a proxy.
	}}
	return c, nil
}

// Socket is the socket the client reaches.
func (c *Client) Socket() string { return c.socket }

// Close drops the idle connections. The client stays usable; a call after
// Close connects again.
func (c *Client) Close() { c.http.CloseIdleConnections() }

// Core is GET coreapi.PathCore: who runs the core, where it serves, and the
// notices a console's status line shows about it.
func (c *Client) Core(ctx context.Context) (coreapi.CoreStatus, error) {
	var status coreapi.CoreStatus
	err := c.do(ctx, http.MethodGet, coreapi.PathCore, nil, &status)
	return status, err
}

// Snapshot is GET coreapi.PathSnapshot: the engine's snapshot, with no
// Commands. It is a coreapi.LoadFunc. A snapshot the core could not read
// (a broken workspace.yaml) is that read's error, in its words.
func (c *Client) Snapshot(ctx context.Context) (query.Snapshot, error) {
	var snap query.Snapshot
	err := c.do(ctx, http.MethodGet, coreapi.PathSnapshot, nil, &snap)
	return snap, err
}

// Act is POST coreapi.PathAct: req run by the engine's Act, and the one
// line it returned. It is a coreapi.ActionFunc. Cancelling ctx stops the
// wait, not the action: the core runs it to its end.
func (c *Client) Act(ctx context.Context, req coreapi.ActionRequest) (string, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	var res coreapi.ActResult
	if err := c.do(ctx, http.MethodPost, coreapi.PathAct, body, &res); err != nil {
		return "", err
	}
	return res.Message, nil
}

// do sends one request and decodes a 200's body into out, or turns any
// other answer into the error the core said (decodeError).
func (c *Client) do(ctx context.Context, method, path string, body []byte, out any) error {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://core"+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.name != "" {
		req.Header.Set(coreapi.ClientHeader, c.name)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("mate core on %s: %w", c.socket, unwrapURL(err))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("mate core on %s: reading %s: %w", c.socket, path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return decodeError(resp.StatusCode, data)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("mate core on %s: %s answered what this mate cannot read: %w", c.socket, path, err)
	}
	return nil
}

// unwrapURL drops the *url.Error around a transport error, whose text
// repeats the made-up URL every request carries.
func unwrapURL(err error) error {
	var u *url.Error
	if errors.As(err, &u) {
		return u.Err
	}
	return err
}

// Error is an answer of the core other than a 200: Message is its sentence,
// verbatim, and Code the coreapi code that says which error it is (empty
// for an action that ran and failed).
type Error struct {
	Status  int
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

// Is maps Code back to coreapi's errors.
func (e *Error) Is(target error) bool {
	switch e.Code {
	case coreapi.CodeBusy:
		return target == coreapi.ErrBusy
	case coreapi.CodeNoMateREPL:
		return target == coreapi.ErrNoMateREPL
	case coreapi.CodeStopping:
		return target == coreapi.ErrStopping
	}
	return false
}

// decodeError reads a coreapi.ErrorBody. A busy target comes back as the
// engine's own *coreapi.BusyError when the body carries it; a body that is
// not one is said with its status.
func decodeError(status int, data []byte) error {
	var body coreapi.ErrorBody
	if err := json.Unmarshal(data, &body); err != nil || body.Error == "" {
		text := strings.TrimSpace(string(data))
		if text == "" {
			text = http.StatusText(status)
		}
		return &Error{Status: status, Message: fmt.Sprintf("mate core answered %d: %s", status, text)}
	}
	if body.Code == coreapi.CodeBusy && body.Busy != nil {
		return body.Busy
	}
	return &Error{Status: status, Code: body.Code, Message: body.Error}
}
