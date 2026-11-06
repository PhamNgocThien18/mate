package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/coreclient"
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

// The captain quits the console while an action runs in the core
// (CON-214): the console's abandon cancels the context its Act waits
// under, which ends the wait at once and not the action, which the core
// runs to its end; once the terminal is back the console says so.
func TestQuittingDuringAnActionLeavesItToTheCore(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "mc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := dir + "/core.sock"
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	finished := make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != coreapi.PathAct {
			http.NotFound(w, r)
			return
		}
		// A core's action, which does not end with its client.
		<-release
		close(finished)
		_ = json.NewEncoder(w).Encode(coreapi.ActResult{Message: "merged"})
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	client, err := coreclient.Dial(sock, coreclient.As(coreapi.ClientConsole))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
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
	select {
	case <-finished:
		t.Fatal("the action ended with the console's wait")
	default:
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("the action did not run to its end")
	}

	var said bytes.Buffer
	sayAbandoned(&said, "merge shop/buy")
	if want := "\nmate console: merge shop/buy keeps running in mate core\n"; said.String() != want {
		t.Fatalf("said %q, want %q", said.String(), want)
	}
}
