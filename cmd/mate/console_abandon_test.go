package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/coreclient"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// lockedBuffer is a stderr two goroutines write at once, as a core's parts
// do (os.Stderr takes that; a bytes.Buffer does not).
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// socketCore is a fake core on w's socket: h answers every request, and
// the client header of each one is kept. It holds no core.lock.
type socketCore struct {
	mu      sync.Mutex
	clients map[string][]string // path -> X-Mate-Client of each request
}

func newSocketCore(t *testing.T, w *store.Workspace, h http.HandlerFunc) *socketCore {
	t.Helper()
	ln, err := net.Listen("unix", w.CoreSocket())
	if err != nil {
		t.Fatal(err)
	}
	s := &socketCore{clients: map[string][]string{}}
	srv := &http.Server{Handler: http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.clients[r.URL.Path] = append(s.clients[r.URL.Path], r.Header.Get(coreapi.ClientHeader))
		s.mu.Unlock()
		h(rw, r)
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return s
}

func (s *socketCore) seen(path string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.clients[path]...)
}

// connectTo is the console's connect to the core already answering on w's
// socket; it starts no core.
func connectTo(t *testing.T, w *store.Workspace) *coreclient.Client {
	t.Helper()
	ctl := liveCoreControl()
	ctl.command = func(string) (*exec.Cmd, error) {
		t.Error("the console started a core beside the one answering")
		return nil, errors.New("no core in this test")
	}
	client, _, err := consoleLink(ctl, "", "", &bytes.Buffer{}).connect(w)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(client.Close)
	return client
}

// Every request of the console's client says it is the console: the core
// counts a console as open by that header, and only then types a Manual
// project's queued lines into its Mate (CON-212, CON-214).
func TestTheConsoleSaysItIsTheConsole(t *testing.T) {
	t.Parallel()
	w := coreWorkspace(t)
	core := newSocketCore(t, w, func(rw http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case coreapi.PathCore:
			_ = json.NewEncoder(rw).Encode(coreapi.CoreStatus{PID: 1})
		case coreapi.PathSnapshot:
			_ = json.NewEncoder(rw).Encode(query.Snapshot{})
		case coreapi.PathAct:
			_ = json.NewEncoder(rw).Encode(coreapi.ActResult{Message: "ok"})
		}
	})
	client := connectTo(t, w)
	load := boundedLoad(consoleLoad(client.Snapshot, nil, coreNotices(client, nil)), consoleReadWait)
	if _, err := load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := tuiAction(client.Act, nil)(context.Background(), coreapi.ActionRequest{Action: coreapi.ActionMode, Target: "shop", TargetKind: "project"}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{coreapi.PathCore, coreapi.PathSnapshot, coreapi.PathAct} {
		got := core.seen(path)
		if len(got) == 0 {
			t.Errorf("%s: no request", path)
		}
		for _, c := range got {
			if c != coreapi.ClientConsole {
				t.Errorf("%s: %s %q, want %q", path, coreapi.ClientHeader, c, coreapi.ClientConsole)
			}
		}
	}
}

// A core that accepts on its socket and answers nothing: each read of the
// console ends after its wait with an error, so the header says stale and
// the next tick reads again, rather than one read hanging for ever.
func TestAConsoleReadOfAHungCoreEnds(t *testing.T) {
	t.Parallel()
	w := coreWorkspace(t)
	hang := make(chan struct{})
	t.Cleanup(func() { close(hang) })
	answer := true
	var mu sync.Mutex
	newSocketCore(t, w, func(rw http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ok := answer
		mu.Unlock()
		if !ok {
			select {
			case <-hang:
			case <-r.Context().Done():
			}
			return
		}
		_ = json.NewEncoder(rw).Encode(coreapi.CoreStatus{PID: 1})
	})
	client := connectTo(t, w)
	mu.Lock()
	answer = false
	mu.Unlock()

	load := boundedLoad(consoleLoad(client.Snapshot, nil, coreNotices(client, nil)), 200*time.Millisecond)
	start := time.Now()
	_, err := load(context.Background())
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("the read took %s", took)
	}
	if err == nil || err.Error() != "mate core did not answer within 200ms" {
		t.Fatalf("read = %v, want the wait said", err)
	}
}

// The captain quits the console while an action runs in the core
// (CON-214): the console's abandon cancels the context its Act waits
// under, which ends the console's wait at once, and once the terminal is
// back the console says the core runs it. That the core runs the action
// to its end after its client went is the core's (internal/core's
// client_test.go); this fake core just goes on.
func TestQuittingDuringAnActionLeavesItToTheCore(t *testing.T) {
	t.Parallel()
	w := coreWorkspace(t)
	release := make(chan struct{})
	finished := make(chan struct{})
	newSocketCore(t, w, func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path != coreapi.PathAct {
			_ = json.NewEncoder(rw).Encode(coreapi.CoreStatus{PID: 1})
			return
		}
		<-release
		close(finished)
		_ = json.NewEncoder(rw).Encode(coreapi.ActResult{Message: "merged"})
	})
	client := connectTo(t, w)
	ctx, abandon := context.WithCancel(context.Background())
	returned := make(chan error, 1)
	go func() {
		_, err := tuiAction(client.Act, splitTerminal)(ctx, coreapi.ActionRequest{Action: coreapi.ActionMerge, Target: "shop", TargetKind: "crew", Crew: "buy"})
		returned <- err
	}()
	time.Sleep(50 * time.Millisecond)
	abandon()
	select {
	case err := <-returned:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Act after the abandon = %v, want the cancel", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the console waited for the action after it quit")
	}
	close(release)
	<-finished

	var said bytes.Buffer
	sayAbandoned(&said, "merge shop/buy")
	if want := "\nmate console: merge shop/buy keeps running in mate core\n"; said.String() != want {
		t.Fatalf("said %q, want %q", said.String(), want)
	}
	if strings.Contains(said.String(), "waiting") {
		t.Error("the console says it waits")
	}
}
