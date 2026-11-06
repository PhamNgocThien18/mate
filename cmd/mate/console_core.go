package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/core"
	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/coreclient"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// The console is a client of the workspace's core (CON-214): it runs no
// engine, reads the core's snapshot and sends its actions over
// `.mate/core.sock`. A console with no core to reach starts one, the way
// `mate core start` does; it never stops one, and it does not start one
// again when the core goes away while it is open, so it does not race a
// `mate core stop` that meant it.

// consoleCore is how a console reaches its core, so a test can stand in
// the core's start and the captain's answer.
type consoleCore struct {
	// ctl is `mate core start` and `restart` (CON-213).
	ctl coreControl
	// in is the captain's answer to the restart question, out where the
	// question and the start's lines go: the terminal, before the TUI.
	in  io.Reader
	out io.Writer
	// build is the sha256 of the binary this console runs (core.Build).
	build string
	// wait is how long the console waits for the core to answer.
	wait, poll time.Duration
}

func liveConsoleCore(in io.Reader, out io.Writer) consoleCore {
	_, build := core.Build()
	return consoleCore{ctl: liveCoreControl(), in: in, out: out, build: build, wait: coreStartWait, poll: 100 * time.Millisecond}
}

// errConsoleOtherBuild is the console's refusal of a core of another build:
// the snapshot crosses the socket as JSON by Go's field names (CON-212), so
// a field renamed between two builds would be lost without a word.
var errConsoleOtherBuild = errors.New("mate console: the console and mate core must be the same build; run `mate core restart` when ready")

// openConsole is a console's opening, in its order (CON-214): the console
// lock first (CON-162), so a console that is refused touches no core; then
// the core. unlock releases the console lock.
func openConsole(ws *store.Workspace, claim consoleClaim, link consoleCore) (client *coreclient.Client, status coreapi.CoreStatus, unlock func(), err error) {
	unlock, err = claim.take(ws)
	if err != nil {
		return nil, coreapi.CoreStatus{}, nil, err
	}
	client, status, err = link.connect(ws)
	if err != nil {
		unlock()
		return nil, coreapi.CoreStatus{}, nil, err
	}
	return client, status, unlock, nil
}

// connect is a client of ws's core, as the console, and the core's status:
// the core started when none answers, and restarted when the captain says
// so for one of another build.
func (c consoleCore) connect(ws *store.Workspace) (*coreclient.Client, coreapi.CoreStatus, error) {
	client, st, err := c.reach(ws)
	if err != nil || c.sameBuild(st) {
		return client, st, err
	}
	client.Close()
	fmt.Fprintf(c.out, "mate core (%s) runs a different build of mate. Restart it? [Y/n] ", pidWord(st.PID))
	if !c.restartYes() {
		return nil, st, errConsoleOtherBuild
	}
	if err := c.ctl.restart(ws, c.out); err != nil {
		return nil, coreapi.CoreStatus{}, c.startError(err)
	}
	if client, st, err = c.reach(ws); err != nil {
		return nil, st, err
	}
	if !c.sameBuild(st) {
		// The binary installed now is still not this console's.
		client.Close()
		return nil, st, errConsoleOtherBuild
	}
	return client, st, nil
}

// sameBuild is whether the core runs this console's build. A build either
// side could not read is not called different.
func (c consoleCore) sameBuild(st coreapi.CoreStatus) bool {
	return c.build == "" || st.Build == "" || st.Build == c.build
}

// restartYes reads the answer to the restart question: anything but n is
// yes, Enter included.
func (c consoleCore) restartYes() bool {
	line, _ := bufio.NewReader(c.in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "n", "no":
		return false
	}
	return true
}

// reach is a client of the core answering on ws's socket, started as
// `mate core start` starts one when none answers, within wait.
func (c consoleCore) reach(ws *store.Workspace) (*coreclient.Client, coreapi.CoreStatus, error) {
	if client, st, err := c.ask(ws); err == nil {
		return client, st, nil
	}
	// No core, or one that holds core.lock and is not answering yet: start
	// says which, and waits for the one it starts.
	if err := c.ctl.start(ws, c.out); err != nil {
		return nil, coreapi.CoreStatus{}, c.startError(err)
	}
	deadline := time.Now().Add(c.wait)
	for {
		client, st, err := c.ask(ws)
		if err == nil {
			return client, st, nil
		}
		if !time.Now().Before(deadline) {
			return nil, coreapi.CoreStatus{}, c.noAnswer()
		}
		time.Sleep(c.poll)
	}
}

// ask is one GET /api/core on ws's socket, as the console.
func (c consoleCore) ask(ws *store.Workspace) (*coreclient.Client, coreapi.CoreStatus, error) {
	client, err := coreclient.Dial(ws.CoreSocket(), coreclient.As(coreapi.ClientConsole))
	if err != nil {
		return nil, coreapi.CoreStatus{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	st, err := client.Core(ctx)
	if err != nil {
		client.Close()
		return nil, coreapi.CoreStatus{}, err
	}
	return client, st, nil
}

// startError words start's error for the console: a core that did not
// answer in time is the console's own sentence, anything else is start's.
func (c consoleCore) startError(err error) error {
	var none *coreNoAnswerError
	if errors.As(err, &none) {
		return c.noAnswer()
	}
	return err
}

func (c consoleCore) noAnswer() error {
	return fmt.Errorf("mate console: mate core did not answer within %s; see %s",
		durationWord(c.wait), filepath.Join(store.StateDirName, "core.log"))
}

// consoleDashboardURL is where the core serves Office/City over TCP, the
// loopback URL followed by the LAN one, as headlessHint reads it; "" when
// it serves neither.
func consoleDashboardURL(st coreapi.CoreStatus) string {
	if st.URL == "" {
		return ""
	}
	if st.LANURL != "" {
		return st.URL + " " + st.LANURL
	}
	return st.URL
}

// coreNotices is what the console's runtime line says about the core on
// each read (consoleLoad): the core's notices that the opening status line
// did not say already. The notification loop's word changes between two
// reads; the configuration errors said when the console opened are not
// repeated on every one.
func coreNotices(client *coreclient.Client, opened []string) func(context.Context) []string {
	return func(ctx context.Context) []string {
		st, err := client.Core(ctx)
		if err != nil {
			// The snapshot read just before reached the core; a read that
			// fails now is the next snapshot read's error to say.
			return nil
		}
		var out []string
		for _, n := range st.Notices {
			if !slices.Contains(opened, n) {
				out = append(out, n)
			}
		}
		return out
	}
}
