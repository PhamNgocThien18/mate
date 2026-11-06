package coreclient

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/coreapi"
)

// The client against the core is tested end to end in internal/core
// (client_test.go). These are its own edges: no core to dial, and answers
// a core of this build would not give.

// serve answers on a unix socket under /tmp (a socket path holds 103 bytes
// on macOS) with h, until the test ends, and returns the socket's path.
func serve(t *testing.T, h http.HandlerFunc) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "cc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "core.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return sock
}

// With no core on the socket Dial says so, and returns no client.
func TestDialWithNoCore(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "core.sock")
	c, err := Dial(sock)
	if err == nil || c != nil || !strings.Contains(err.Error(), "mate core is not reachable on "+sock) {
		t.Fatalf("Dial = %v, %v; want the socket named in the refusal", c, err)
	}
}

// Every code comes back as coreapi's error, the sentence verbatim; a busy
// refusal with no detail still is ErrBusy; an answer that is not an
// ErrorBody is said with its status.
func TestErrorsMapBackToCoreapi(t *testing.T) {
	answers := map[string]struct {
		status int
		body   string
	}{
		"stopping": {http.StatusConflict, `{"error":"mate core is stopping · nothing started","code":"stopping"}`},
		"busy":     {http.StatusConflict, `{"error":"Action refused: stop is still running on shop · nothing started","code":"busy"}`},
		"norepl":   {http.StatusUnprocessableEntity, `{"error":"the Mate runs without the REPL","code":"no_mate_repl"}`},
		"plain":    {http.StatusBadGateway, "upstream gone\n"},
	}
	sock := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(coreapi.ClientHeader) != coreapi.ClientConsole {
			http.Error(w, "no client named", http.StatusBadRequest)
			return
		}
		a := answers[r.URL.Query().Get("case")]
		w.WriteHeader(a.status)
		_, _ = w.Write([]byte(a.body))
	})
	c, err := Dial(sock, As(coreapi.ClientConsole))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ask := func(name string) error {
		return c.do(context.Background(), http.MethodGet, "/?case="+name, nil, &struct{}{})
	}
	if err := ask("stopping"); !errors.Is(err, coreapi.ErrStopping) || err.Error() != coreapi.ErrStopping.Error() {
		t.Errorf("stopping = %v, want ErrStopping", err)
	}
	if err := ask("busy"); !errors.Is(err, coreapi.ErrBusy) || errors.Is(err, coreapi.ErrStopping) ||
		err.Error() != "Action refused: stop is still running on shop · nothing started" {
		t.Errorf("busy = %v, want ErrBusy in its words", err)
	}
	if err := ask("norepl"); !errors.Is(err, coreapi.ErrNoMateREPL) {
		t.Errorf("no_mate_repl = %v, want ErrNoMateREPL", err)
	}
	err = ask("plain")
	var cerr *Error
	if !errors.As(err, &cerr) || cerr.Status != http.StatusBadGateway || cerr.Code != "" ||
		err.Error() != "mate core answered 502: upstream gone" {
		t.Errorf("plain = %v, want the status and the text", err)
	}
}

// A core that goes away after Dial is the transport's error, naming the
// socket, not the made-up URL each request carries.
func TestACoreGoneAfterDial(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "cc")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "core.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	c, err := Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()
	_, err = c.Snapshot(context.Background())
	if err == nil || !strings.HasPrefix(err.Error(), "mate core on "+sock+": ") || strings.Contains(err.Error(), "http://core") {
		t.Fatalf("Snapshot with the core gone = %v", err)
	}
}
