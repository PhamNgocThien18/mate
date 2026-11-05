package matehost

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// LiveOptions are what Live runs the host with.
type LiveOptions struct {
	// Socket is where the host answers (SocketPath).
	Socket string
	// NewsPoll is how often the news log and the work board are looked at
	// (WatchNews).
	NewsPoll time.Duration
	// Signals end the host: SIGTERM and SIGHUP for `mate mate host`.
	Signals <-chan os.Signal
	// REPL runs a REPL client in the foreground, a process of its own in
	// `mate mate host`, until ctx ends. Live starts it again whenever it
	// ends: the host does not end with it (CON-223). Nil runs none.
	REPL func(ctx context.Context) error
	// Restart is the least time between two starts of the REPL; zero
	// means a second. A REPL that keeps ending at once waits longer each
	// time, up to replRestartMax.
	Restart time.Duration
	// Log is told when the REPL ends on an error; nil tells no one.
	Log io.Writer
}

// replStopWait bounds how long a stopping host waits for its REPL.
const replStopWait = 5 * time.Second

// A REPL that ran for less than replShortRun ended at once (its binary
// gone mid-install, its project missing): the next start waits twice as
// long as the last, up to replRestartMax.
const (
	replShortRun   = 5 * time.Second
	replRestartMax = 30 * time.Second
)

// Live runs the host until it is asked to stop: the socket's stop op, a
// signal on Signals, or ctx ending. The REPL is one of its clients, run
// beside it; the host lives on when the REPL ends. Then it stops the
// REPL, the host (a turn in flight is killed, as on a closed pane), and
// the socket.
func (h *Host) Live(ctx context.Context, opts LiveOptions) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopped := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(stopped) }) }

	go h.WatchNews(ctx, opts.NewsPoll)
	// The socket outlives ctx until the host has stopped, so the REPL and
	// the subscribers hear it stopping.
	serveCtx, endServe := context.WithCancel(context.Background())
	defer endServe()
	served := make(chan error, 1)
	go func() { served <- h.Serve(serveCtx, opts.Socket, stop) }()

	replCtx, endREPL := context.WithCancel(ctx)
	defer endREPL()
	replDone := make(chan struct{})
	go func() {
		defer close(replDone)
		superviseREPL(replCtx, opts)
	}()

	var err error
	select {
	case <-stopped:
	case <-opts.Signals:
	case <-ctx.Done():
	case err = <-served:
		// The socket could not be served (its path, a bind): no client
		// could reach this host, so it does not run.
		served = nil
		if err == nil {
			err = fmt.Errorf("matehost: the socket %s stopped", opts.Socket)
		}
	}
	// The turn first, as when the REPL ran in this process: a pane that
	// closes may not leave the host long. Then the REPL, which hears the
	// host stopping and gives the terminal back.
	h.Stop(0, true)
	endREPL()
	select {
	case <-replDone:
	case <-time.After(replStopWait):
	}
	cancel()
	endServe()
	if served != nil {
		select {
		case serr := <-served:
			if err == nil {
				err = serr
			}
		case <-time.After(2 * time.Second):
		}
	}
	return err
}

// superviseREPL runs opts.REPL again each time it ends, at most once per
// opts.Restart, until ctx ends.
func superviseREPL(ctx context.Context, opts LiveOptions) {
	if opts.REPL == nil {
		return
	}
	every := opts.Restart
	if every <= 0 {
		every = time.Second
	}
	wait := every
	for ctx.Err() == nil {
		started := time.Now()
		err := opts.REPL(ctx)
		if ctx.Err() != nil {
			return
		}
		ran := time.Since(started)
		if ran < replShortRun {
			wait = min(wait*2, max(replRestartMax, every))
		} else {
			wait = every
		}
		if err != nil && opts.Log != nil {
			fmt.Fprintf(opts.Log, "mate: the REPL ended (%v); starting it again\n", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait - ran):
		}
	}
}
