package core

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/recovery"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// shortWorkspace is shopWorkspace under /tmp: a unix socket path holds 103
// bytes on macOS, and t.TempDir() there is longer than `.mate/core.sock`
// leaves room for.
func shortWorkspace(t *testing.T) *store.Workspace {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "mc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	w, err := store.Init(dir, store.Defaults{MateHarness: "claude", CrewHarness: "codex"})
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	if err := w.AddProject("shop", store.ProjectConfig{}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	return w
}

// coreEngine is an engine as `mate core run` builds one, over w: owned by
// the core, serving the dashboard on a free port and on `.mate/core.sock`.
func coreEngine(w *store.Workspace, addr string) *Engine {
	return New(Options{
		Workspace: w, Harnesses: testHarnesses, Owner: OwnerCore,
		Dashboard: &DashboardOptions{Addr: addr, Socket: w.CoreSocket()},
		Stderr:    io.Discard,
	})
}

// socketGet is GET path over the unix socket at sock.
func socketGet(t *testing.T, sock, path string) (int, string) {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		},
	}}
	defer client.CloseIdleConnections()
	resp, err := client.Get("http://core" + path)
	if err != nil {
		t.Fatalf("GET %s over %s: %v", path, sock, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

// `mate core run`'s engine answers one route on the socket and over TCP
// alike; core.lock says who it is and where it serves; Stop releases the
// lock and removes the socket.
func TestCoreServesTheSameRoutesOnTheSocketAndTCP(t *testing.T) {
	w := shortWorkspace(t)
	withTimelineDB(t, w)
	e := coreEngine(w, freeAddr)
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(e.Stop)

	serving := e.Serving()
	if serving.Socket != w.CoreSocket() || serving.URL == "" || serving.Err != nil {
		t.Fatalf("Serving = %+v, want the socket and a URL", serving)
	}
	status, overSocket := socketGet(t, w.CoreSocket(), "/api/workspace")
	if status != http.StatusOK {
		t.Fatalf("socket: status %d", status)
	}
	status, overTCP := get(t, serving.URL, "/api/workspace")
	if status != http.StatusOK {
		t.Fatalf("tcp: status %d", status)
	}
	if !strings.Contains(overSocket, `"shop"`) || !strings.Contains(overTCP, `"shop"`) {
		t.Fatalf("socket %q, tcp %q; want both to name the workspace's project", overSocket, overTCP)
	}
	info, err := os.Stat(w.CoreSocket())
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("socket mode %v, want 0600", perm)
	}

	h, held, err := w.CoreRunning()
	if err != nil || !held {
		t.Fatalf("CoreRunning = %v, %v while the engine runs", held, err)
	}
	exe, sum := Build()
	if h.PID != os.Getpid() || h.Owner != OwnerCore || h.Started.IsZero() || h.Binary != exe || h.Build != sum || len(sum) != 64 ||
		h.Socket != w.CoreSocket() || h.URL != serving.URL || h.TCPError != "" {
		t.Fatalf("core.lock = %+v, want this process, its build and where it serves", h)
	}

	e.Stop()
	if _, err := os.Stat(w.CoreSocket()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("socket after Stop: %v, want it removed", err)
	}
	if _, held, _ := w.CoreRunning(); held {
		t.Error("core.lock still held after Stop")
	}
}

// One workspace runs one engine: a second one, a console's or a core's, is
// refused with who holds it, and starts nothing - not even the socket.
func TestASecondEngineOnTheWorkspaceIsRefused(t *testing.T) {
	w := shortWorkspace(t)
	withTimelineDB(t, w)
	first := coreEngine(w, freeAddr)
	if err := first.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(first.Stop)

	recovered := false
	second := New(Options{
		Workspace: w, Owner: OwnerConsole, Stderr: io.Discard,
		Dashboard: &DashboardOptions{Addr: freeAddr},
		Recover: func(context.Context, *store.Workspace, func(recovery.Progress)) recovery.Result {
			recovered = true
			return recovery.Result{}
		},
	})
	err := second.Start(context.Background())
	var running *RunningError
	if !errors.As(err, &running) || running.Holder.Owner != OwnerCore || running.Holder.PID != os.Getpid() {
		t.Fatalf("second Start = %v, want a RunningError naming the core", err)
	}
	if recovered {
		t.Error("the refused engine ran recovery")
	}
	if url, _ := second.Dashboard(); url != "" {
		t.Errorf("the refused engine serves %s", url)
	}
	if status, _ := socketGet(t, w.CoreSocket(), "/api/workspace"); status != http.StatusOK {
		t.Errorf("the first engine's socket after the refusal: %d", status)
	}

	first.Stop()
	third := coreEngine(w, freeAddr)
	if err := third.Start(context.Background()); err != nil {
		t.Fatalf("Start after the first stopped: %v", err)
	}
	third.Stop()
}

// A socket left by a core that died is removed by the next one, which holds
// the lock; one whose path is too long for a unix socket stops the core
// before any part starts, and releases the lock.
func TestCoreSocketLeftoversAndLimits(t *testing.T) {
	w := shortWorkspace(t)
	withTimelineDB(t, w)
	stale, err := net.Listen("unix", w.CoreSocket())
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = stale.Close()
	if _, err := os.Stat(w.CoreSocket()); err != nil {
		t.Fatalf("the stale socket is gone already: %v", err)
	}
	e := coreEngine(w, freeAddr)
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("Start over a stale socket: %v", err)
	}
	if status, _ := socketGet(t, w.CoreSocket(), "/api/workspace"); status != http.StatusOK {
		t.Fatalf("status %d over the socket that replaced the stale one", status)
	}
	e.Stop()

	long := filepath.Join(w.StateDir(), strings.Repeat("s", 120)+".sock")
	ran := false
	tooLong := New(Options{
		Workspace: w, Owner: OwnerCore, Stderr: io.Discard,
		Dashboard: &DashboardOptions{Addr: freeAddr, Socket: long},
		Recover: func(context.Context, *store.Workspace, func(recovery.Progress)) recovery.Result {
			ran = true
			return recovery.Result{}
		},
	})
	err = tooLong.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "a unix socket path holds at most") {
		t.Fatalf("Start with a %d-byte socket path = %v, want the limit named", len(long), err)
	}
	if ran {
		t.Error("recovery ran for a core that could not open its socket")
	}
	if _, held, _ := w.CoreRunning(); held {
		t.Error("core.lock still held after a refused Start")
	}
}

// A busy TCP port does not stop the core: it serves on the socket and says
// why not over TCP, in core.lock too.
func TestCoreRunsOnTheSocketWhenTheTCPPortIsBusy(t *testing.T) {
	busy, err := net.Listen("tcp", freeAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	w := shortWorkspace(t)
	withTimelineDB(t, w)
	e := coreEngine(w, busy.Addr().String())
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("Start with the TCP port busy: %v", err)
	}
	t.Cleanup(e.Stop)
	serving := e.Serving()
	if serving.URL != "" || serving.Err == nil || serving.Socket == "" {
		t.Fatalf("Serving = %+v, want the socket alone and why not TCP", serving)
	}
	if status, _ := socketGet(t, w.CoreSocket(), "/api/workspace"); status != http.StatusOK {
		t.Fatalf("socket status %d", status)
	}
	h, _, _ := w.CoreRunning()
	if h.TCPError == "" || h.URL != "" || h.Socket != w.CoreSocket() {
		t.Fatalf("core.lock = %+v, want the TCP error recorded", h)
	}
}

// A claim taken ahead (core.Lock, as the console and `mate core run` take
// it before they build the parts) is the engine's from New on: it is not
// taken twice, and it is released when the engine stops, even one that
// never started.
func TestAClaimTakenAheadIsTheEngines(t *testing.T) {
	w := shopWorkspace(t)
	claim, err := Lock(w, OwnerCore, time.Now())
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	if _, err := Lock(w, OwnerConsole, time.Now()); err == nil {
		t.Fatal("a second Lock while the claim is held succeeded")
	}
	e := New(Options{Workspace: w, Claim: claim, Stderr: io.Discard})
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("Start over its claim: %v", err)
	}
	if h, held, _ := w.CoreRunning(); !held || h.Owner != OwnerCore {
		t.Fatalf("core.lock = %+v, held %v; want the claim's", h, held)
	}
	e.Stop()
	if _, held, _ := w.CoreRunning(); held {
		t.Fatal("core.lock held after Stop")
	}

	unstarted, err := Lock(w, OwnerConsole, time.Now())
	if err != nil {
		t.Fatalf("Lock after Stop: %v", err)
	}
	New(Options{Workspace: w, Claim: unstarted}).Stop()
	if _, held, _ := w.CoreRunning(); held {
		t.Fatal("core.lock held after an engine that never started stopped")
	}
}

// A console's engine takes core.lock too, as the console, and serves no
// socket; with no ConsolePresent the engine says a console is there.
func TestAConsoleEngineHoldsCoreLockWithoutASocket(t *testing.T) {
	w := shopWorkspace(t)
	e := New(Options{Workspace: w, Stderr: io.Discard})
	if !e.ConsolePresent() {
		t.Error("ConsolePresent with none given = false, want a console")
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	h, held, err := w.CoreRunning()
	if err != nil || !held || h.Owner != OwnerConsole || h.Socket != "" {
		t.Fatalf("core.lock = %+v, held %v, %v; want the console's, with no socket", h, held, err)
	}
	e.Stop()
	if _, held, _ := w.CoreRunning(); held {
		t.Error("core.lock still held after Stop")
	}
	if absent := New(Options{ConsolePresent: func() bool { return false }}); absent.ConsolePresent() {
		t.Error("ConsolePresent = true with a function saying false")
	}
}
