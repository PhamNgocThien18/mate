package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/mate/internal/core"
	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/coreclient"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

// The console as a client of the core (CON-214), over the fake cores of
// CON-213's tests (this test binary started again, which holds core.lock
// and answers GET /api/core) and over runCore in this process, on temporary
// workspaces.

// sha256Hex is the build of a binary whose content is data.
func sha256Hex(data string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

// fakeCoreBinary makes f's cores record content as their binary, and is the
// build a console of that binary has.
func fakeCoreBinary(t *testing.T, w *store.Workspace, f *fakeCores, content string) string {
	t.Helper()
	f.binary = w.StateDir() + "/fake-mate"
	if err := os.WriteFile(f.binary, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return sha256Hex(content)
}

// consoleLink is a console of build reaching w's core through ctl, the
// captain answering answer.
func consoleLink(ctl coreControl, build, answer string, out io.Writer) consoleCore {
	return consoleCore{ctl: ctl, in: strings.NewReader(answer), out: out, build: build, wait: 10 * time.Second, poll: 20 * time.Millisecond}
}

// A console opened with no core starts one, as `mate core start` does, and
// reaches it; once the console has gone, the core still runs.
func TestConsoleStartsTheCoreWhenNoneRuns(t *testing.T) {
	w := coreWorkspace(t)
	f := newFakeCores(t, "serve")
	build := fakeCoreBinary(t, w, f, "build 1")
	var out bytes.Buffer
	client, st, err := consoleLink(f.coreControl, build, "", &out).connect(w)
	if err != nil {
		t.Fatalf("connect: %v\n%s", err, out.String())
	}
	pids := f.started()
	if len(pids) != 1 || st.PID != pids[0] || st.Build != build {
		t.Fatalf("status %+v, started %v; want the one core it started", st, pids)
	}
	if !strings.Contains(out.String(), "mate core: started (pid ") {
		t.Errorf("the console said %q, want the start's line", out.String())
	}
	// The console quits: it closes its client and stops nothing.
	client.Close()
	if status, err := f.statusOut(t, w); err != nil || !strings.Contains(status, "mate core · pid ") {
		t.Fatalf("mate core status after the console = %v:\n%s", err, status)
	}

	// A second console finds that core and starts none.
	out.Reset()
	client, st, err = consoleLink(f.coreControl, build, "", &out).connect(w)
	if err != nil {
		t.Fatalf("second connect: %v", err)
	}
	defer client.Close()
	if len(f.started()) != 1 || st.PID != pids[0] || out.Len() != 0 {
		t.Fatalf("second console: status %+v, started %v, said %q; want the running core, nothing started or said", st, f.started(), out.String())
	}
}

// A core of another build is asked about before the TUI draws: n leaves it
// running and the console exits 1; Enter or y restarts it with the binary
// installed now, and the console goes on with it.
func TestConsoleAsksBeforeItRunsBesideAnotherBuild(t *testing.T) {
	const question = "mate core (pid %d) runs a different build of mate. Restart it? [Y/n] "
	for _, tc := range []struct {
		answer  string
		restart bool
	}{{"n\n", false}, {"N\n", false}, {"\n", true}, {"y\n", true}} {
		t.Run(strings.TrimSpace(tc.answer), func(t *testing.T) {
			w := coreWorkspace(t)
			f := newFakeCores(t, "serve")
			fakeCoreBinary(t, w, f, "build 1")
			old, _ := f.startOK(t, w)
			// A new mate installed over it; the console is that build.
			build := fakeCoreBinary(t, w, f, "build 2")

			var out bytes.Buffer
			client, st, err := consoleLink(f.coreControl, build, tc.answer, &out).connect(w)
			if !strings.HasPrefix(out.String(), fmt.Sprintf(question, old)) {
				t.Errorf("the console said %q, want the question first", out.String())
			}
			if !tc.restart {
				if !errors.Is(err, errConsoleOtherBuild) || client != nil {
					t.Fatalf("connect = %v, want %v", err, errConsoleOtherBuild)
				}
				if want := "mate console: the console and mate core must be the same build; run `mate core restart` when ready"; err.Error() != want {
					t.Errorf("refusal %q, want %q", err, want)
				}
				var ue *usageError
				if errors.As(err, &ue) {
					t.Error("the refusal is a usage error, exit 2; want exit 1")
				}
				if h, held, _ := w.CoreRunning(); !held || h.PID != old || len(f.started()) != 1 {
					t.Fatalf("core.lock %+v held %v, started %v; want the old core left running", h, held, f.started())
				}
				return
			}
			if err != nil {
				t.Fatalf("connect: %v\n%s", err, out.String())
			}
			defer client.Close()
			pids := f.started()
			if len(pids) != 2 || st.PID != pids[1] || st.PID == old || st.Build != build {
				t.Fatalf("status %+v, started %v; want a new core of the console's build", st, pids)
			}
			if !strings.Contains(out.String(), "mate core: stopped\n") || !strings.Contains(out.String(), "mate core: started (pid ") {
				t.Errorf("the console said %q, want the restart's stop and start", out.String())
			}
		})
	}
}

// A second console whose captain answers N to CON-162's question exits
// before it reaches the core: nothing is dialled, started or restarted.
func TestASecondConsoleThatSaysNoTouchesNoCore(t *testing.T) {
	h := newClaimHarness(t)
	var out bytes.Buffer
	ctl := liveCoreControl()
	ctl.command = func(string) (*exec.Cmd, error) {
		t.Error("the refused console started a core")
		return nil, errors.New("no core in this test")
	}
	ctl.signal = func(int, syscall.Signal) error {
		t.Error("the refused console signalled a core")
		return nil
	}
	client, _, unlock, err := openConsole(h.ws, h.claim("N\n", 0), consoleLink(ctl, "", "y\n", &out))
	if err == nil || client != nil || unlock != nil {
		t.Fatalf("openConsole = %v, %v; want the claim's refusal", client, err)
	}
	if !strings.Contains(err.Error(), "console 43674 is still running") {
		t.Errorf("refusal %q, want the claim's", err)
	}
	if strings.Contains(out.String()+h.out.String(), "mate core") {
		t.Errorf("the refused console said %q%q; want nothing about the core", h.out.String(), out.String())
	}
	if _, held, _ := h.ws.CoreRunning(); held {
		t.Error("core.lock is held after a refused console")
	}
	if _, err := os.Stat(h.ws.CoreSocket()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("core.sock after a refused console: %v", err)
	}
}

// A core that does not answer within the wait: the console says so in its
// words and exits 1, whether it started that core or found it holding
// core.lock.
func TestConsoleSaysACoreThatDoesNotAnswer(t *testing.T) {
	const want = "mate console: mate core did not answer within 300ms; see .mate/core.log"
	t.Run("it started it", func(t *testing.T) {
		w := coreWorkspace(t)
		f := newFakeCores(t, "mute")
		f.startWait = 300 * time.Millisecond
		link := consoleLink(f.coreControl, "", "", io.Discard)
		link.wait = 300 * time.Millisecond
		if _, _, err := link.connect(w); err == nil || err.Error() != want {
			t.Fatalf("connect = %v, want %q", err, want)
		}
	})
	t.Run("it found it", func(t *testing.T) {
		w := coreWorkspace(t)
		f := newFakeCores(t, "mute")
		f.startWait = 300 * time.Millisecond
		_ = f.start(w, io.Discard) // a core that holds core.lock and serves nothing
		var out bytes.Buffer
		link := consoleLink(f.coreControl, "", "", &out)
		link.wait = 300 * time.Millisecond
		if _, _, err := link.connect(w); err == nil || err.Error() != want {
			t.Fatalf("connect = %v, want %q", err, want)
		}
		if len(f.started()) != 1 || !strings.Contains(out.String(), "mate core: already running") {
			t.Errorf("started %v, said %q; want the running core waited for, none started", f.started(), out.String())
		}
	})
}

// The core goes away while the console is open: the header says stale on
// the next read, and live again once a core answers on the socket - one the
// console did not start.
func TestConsoleHeaderComesBackLiveWithTheCore(t *testing.T) {
	w := coreWorkspace(t)
	runOne := func() (stop func()) {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		var stdout lockedBuffer
		done := make(chan error, 1)
		go func() { done <- runCore(ctx, w, fakeCoreDeps(t), nil, nil, &stdout, io.Discard) }()
		waitFor(t, func() bool { return strings.Contains(stdout.String(), "Ctrl+C to stop") })
		return func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("runCore: %v", err)
				}
			case <-time.After(core.StopWait + 5*time.Second):
				t.Fatal("the core did not stop")
			}
		}
	}
	stop := runOne()
	client, err := coreclient.Dial(w.CoreSocket(), coreclient.As(coreapi.ClientConsole))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	t.Setenv("MATE_ASCII", "0")
	var model tea.Model = console.New(consoleLoad(client.Snapshot, nil, coreNotices(client, nil)), nil)
	step := func(msg tea.Msg) {
		t.Helper()
		var cmd tea.Cmd
		model, cmd = model.Update(msg)
		if cmd != nil {
			if next := cmd(); next != nil {
				model, _ = model.Update(next)
			}
		}
	}
	header := func() string { return strings.Split(model.View(), "\n")[0] }
	refresh := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")}
	model, _ = model.Update(tea.WindowSizeMsg{Width: 40, Height: 36})
	step(model.Init()())
	if strings.Contains(header(), "stale") || !strings.Contains(model.View(), "shop") {
		t.Fatalf("first read:\n%s", model.View())
	}

	stop()
	step(refresh)
	if !strings.Contains(header(), "stale") || !strings.Contains(model.View(), "Refresh failed") {
		t.Fatalf("with the core gone the header does not say stale:\n%s", model.View())
	}
	if _, held, _ := w.CoreRunning(); held {
		t.Fatal("the console started a core when the one it had went away")
	}

	stop = runOne()
	defer stop()
	step(refresh)
	if strings.Contains(header(), "stale") {
		t.Fatalf("with the core back the header still says stale:\n%s", model.View())
	}
}
