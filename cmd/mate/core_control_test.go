package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/platform"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// `mate core start|stop|restart|status` (CON-213) over a fake core: this
// test binary started again (TestMain, fakeCoreEnv), which holds core.lock
// and answers GET /api/core on core.sock the way `mate core run` does, and
// runs no engine. Every workspace is a temporary one (coreWorkspace).

const (
	// fakeCoreEnv is the fake core's mode: serve, ignore-term (serves, and
	// SIGTERM does not stop it), mute (holds the lock, serves nothing) or
	// fail (ends at once).
	fakeCoreEnv = "MATE_TEST_FAKE_CORE"
	// fakeCoreWSEnv is the workspace, fakeCoreBinaryEnv the file it
	// records as its binary.
	fakeCoreWSEnv     = "MATE_TEST_FAKE_CORE_WS"
	fakeCoreBinaryEnv = "MATE_TEST_FAKE_CORE_BINARY"
	fakeCoreURL       = "http://127.0.0.1:7777/"
)

// fakeCoreMain is the fake core's whole life. What it prints goes to
// core.log.
func fakeCoreMain() int {
	mode := os.Getenv(fakeCoreEnv)
	if mode == "fail" {
		fmt.Println("fake core: failing at once")
		return 1
	}
	// A test that failed before it stopped the fake leaves nothing behind
	// for long.
	time.AfterFunc(2*time.Minute, func() { os.Exit(3) })
	ws, err := store.Open(os.Getenv(fakeCoreWSEnv))
	if err != nil {
		fmt.Println("fake core:", err)
		return 1
	}
	binary := os.Getenv(fakeCoreBinaryEnv)
	build := ""
	if data, err := os.ReadFile(binary); err == nil {
		sum := sha256.Sum256(data)
		build = hex.EncodeToString(sum[:])
	}
	holder := store.CoreHolder{PID: os.Getpid(), Owner: store.CoreOwnerCore, Started: time.Now(), Version: "test", Binary: binary, Build: build}
	lock, err := ws.LockCore(holder)
	if err != nil {
		fmt.Println("fake core:", err)
		return 1
	}
	term := make(chan os.Signal, 1)
	if mode == "ignore-term" {
		signal.Ignore(syscall.SIGTERM)
	} else {
		signal.Notify(term, syscall.SIGTERM)
	}
	if mode != "mute" {
		ln, err := net.Listen("unix", ws.CoreSocket())
		if err != nil {
			fmt.Println("fake core:", err)
			return 1
		}
		defer ln.Close()
		holder.Socket, holder.URL = ws.CoreSocket(), fakeCoreURL
		_ = lock.Write(holder)
		go func() {
			_ = http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != coreapi.PathCore {
					http.NotFound(w, r)
					return
				}
				_ = json.NewEncoder(w).Encode(coreapi.CoreStatus{
					PID: holder.PID, Owner: holder.Owner, Version: holder.Version, Build: holder.Build,
					Binary: holder.Binary, Started: holder.Started, URL: holder.URL, Notices: []string{"observer: fake"},
				})
			}))
		}()
	}
	fmt.Println("fake core up")
	<-term
	fmt.Println("fake core stopping")
	lock.Unlock()
	return 0
}

// fakeCores is a coreControl whose `start` runs the fake core in mode, with
// short waits; it records the pids it started, and kills them at the end
// of the test whatever happened.
type fakeCores struct {
	coreControl
	mu     sync.Mutex
	procs  []*os.Process
	binary string
}

func newFakeCores(t *testing.T, mode string) *fakeCores {
	t.Helper()
	f := &fakeCores{coreControl: liveCoreControl()}
	f.command = func(root string) (*exec.Cmd, error) {
		cmd := exec.Command(os.Args[0], "-test.run=^$")
		cmd.Env = append(os.Environ(), fakeCoreEnv+"="+mode, fakeCoreWSEnv+"="+root, fakeCoreBinaryEnv+"="+f.binary)
		return cmd, nil
	}
	f.startDetached = func(cmd *exec.Cmd) error {
		if err := platform.StartNewSession(cmd); err != nil {
			return err
		}
		f.mu.Lock()
		f.procs = append(f.procs, cmd.Process)
		f.mu.Unlock()
		return nil
	}
	f.startWait, f.stopWait, f.killWait, f.poll = 10*time.Second, 5*time.Second, 5*time.Second, 20*time.Millisecond
	// Killed through the *os.Process, which knows when start's Wait has
	// reaped it: a raw pid could be another process's by then.
	t.Cleanup(func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, p := range f.procs {
			_ = p.Kill()
		}
	})
	return f
}

func (f *fakeCores) started() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	pids := make([]int, len(f.procs))
	for i, p := range f.procs {
		pids[i] = p.Pid
	}
	return pids
}

func (f *fakeCores) startOK(t *testing.T, ws *store.Workspace) (pid int, out string) {
	t.Helper()
	before := len(f.started())
	var b bytes.Buffer
	if err := f.start(ws, &b); err != nil {
		t.Fatalf("start: %v\n%s", err, b.String())
	}
	pids := f.started()
	if len(pids) != before+1 {
		t.Fatalf("start ran %d processes, want 1", len(pids)-before)
	}
	return pids[len(pids)-1], b.String()
}

func (f *fakeCores) statusOut(t *testing.T, ws *store.Workspace) (string, error) {
	t.Helper()
	var b bytes.Buffer
	err := f.status(ws, &b)
	return b.String(), err
}

// start runs a core in a session of its own that answers on the socket;
// a second start runs nothing; status reads it; stop ends it with SIGTERM
// and leaves no socket; then status says not running, and stop says so
// with exit 0.
func TestCoreStartStatusStop(t *testing.T) {
	w := coreWorkspace(t)
	f := newFakeCores(t, "serve")

	pid, out := f.startOK(t, w)
	if want := fmt.Sprintf("mate core: started (pid %d) · %s\n", pid, fakeCoreURL); out != want {
		t.Fatalf("start said %q, want %q", out, want)
	}
	// Detached: the leader of a session and group of its own, so neither
	// the terminal's hangup nor a signal to this process's group reaches
	// it.
	if pgid, err := syscall.Getpgid(pid); err != nil || pgid != pid {
		t.Fatalf("core's process group = %d, %v; want its own (%d)", pgid, err, pid)
	}
	if own, _ := syscall.Getpgid(os.Getpid()); own == pid {
		t.Fatal("the core shares this process's group")
	}

	var again bytes.Buffer
	if err := f.start(w, &again); err != nil {
		t.Fatalf("second start: %v", err)
	}
	if want := fmt.Sprintf("mate core: already running (pid %d, since ", pid); !strings.HasPrefix(again.String(), want) {
		t.Fatalf("second start said %q, want %q…", again.String(), want)
	}
	if n := len(f.started()); n != 1 {
		t.Fatalf("two starts ran %d processes, want 1", n)
	}

	status, err := f.statusOut(t, w)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	for _, want := range []string{
		fmt.Sprintf("mate core · pid %d · ", pid),
		"  owner    core\n",
		"  version  test\n",
		"  socket   " + w.CoreSocket() + "\n",
		"  notice   observer: fake\n",
		"Office  http://127.0.0.1:7777/office/\n",
		"City    http://127.0.0.1:7777/city/\n",
		"Herdr   " + w.Session() + "\n",
		"Log     " + w.CoreLogFile() + "\n",
	} {
		if !strings.Contains(status, want) {
			t.Errorf("status lacks %q:\n%s", want, status)
		}
	}
	if strings.Contains(status, "stale") {
		t.Errorf("status says stale with no build recorded:\n%s", status)
	}

	var stop bytes.Buffer
	if err := f.stop(w, &stop); err != nil {
		t.Fatalf("stop: %v\n%s", err, stop.String())
	}
	if want := fmt.Sprintf("mate core: stopping pid %d (at most 5s)...\nmate core: stopped\n", pid); stop.String() != want {
		t.Fatalf("stop said %q, want %q", stop.String(), want)
	}
	if _, held, _ := w.CoreRunning(); held {
		t.Fatal("core.lock still held after stop")
	}
	if _, err := os.Stat(w.CoreSocket()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("socket after stop: %v", err)
	}
	if log, _ := os.ReadFile(w.CoreLogFile()); !strings.Contains(string(log), "fake core up") || !strings.Contains(string(log), "fake core stopping") {
		t.Errorf("core.log = %q, want the core's output", log)
	}

	if _, err := f.statusOut(t, w); !errors.Is(err, errCoreNotRunning) {
		t.Fatalf("status after stop = %v, want not running", err)
	}
	stop.Reset()
	if err := f.stop(w, &stop); err != nil || stop.String() != "mate core: not running\n" {
		t.Fatalf("second stop = %v, %q; want not running and no error", err, stop.String())
	}
}

// Through the command as typed: status exits 1 with no core, 0 with one;
// stop exits 0 with none. Nothing here starts a process.
func TestCoreStatusExitCodes(t *testing.T) {
	w := coreWorkspace(t)
	var stdout, stderr bytes.Buffer
	if code := mainRun([]string{"core", "status", w.Root()}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "mate core: not running") {
		t.Fatalf("status with no core: exit %d, stderr %q", code, stderr.String())
	}
	stdout.Reset()
	if code := mainRun([]string{"core", "stop", "--workspace", w.Root()}, &stdout, &stderr); code != 0 || stdout.String() != "mate core: not running\n" {
		t.Fatalf("stop with no core: exit %d, stdout %q", code, stdout.String())
	}
	l, err := w.LockCore(store.CoreHolder{PID: 4242, Owner: store.CoreOwnerCore, URL: fakeCoreURL})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Unlock()
	stdout.Reset()
	if code := mainRun([]string{"core", "status", w.Root()}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "mate core · pid 4242") {
		t.Fatalf("status with a core: exit %d, stdout %q", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "(not answering: ") {
		t.Errorf("status of a core whose socket is gone: %q, want it said", stdout.String())
	}
}

// A core.lock whose record names a live pid nobody holding the lock -
// here this test's own - is a core that died: status says not running,
// stop signals nobody, and start runs a new core and writes its own
// record over the old one.
func TestCoreLockOfADeadCoreIsNotRunning(t *testing.T) {
	w := coreWorkspace(t)
	old, _ := json.Marshal(store.CoreHolder{PID: os.Getpid(), Owner: store.CoreOwnerCore, Started: time.Now().Add(-time.Hour)})
	if err := os.WriteFile(w.CoreLockFile(), old, 0o644); err != nil {
		t.Fatal(err)
	}
	f := newFakeCores(t, "serve")
	var signalled []int
	f.signal = func(pid int, _ syscall.Signal) error {
		signalled = append(signalled, pid)
		return nil
	}

	if _, err := f.statusOut(t, w); !errors.Is(err, errCoreNotRunning) {
		t.Fatalf("status = %v, want not running", err)
	}
	var out bytes.Buffer
	if err := f.stop(w, &out); err != nil || out.String() != "mate core: not running\n" {
		t.Fatalf("stop = %v, %q; want not running", err, out.String())
	}
	if len(signalled) != 0 {
		t.Fatalf("stop signalled %v; a lock nobody holds names nobody to stop", signalled)
	}

	pid, _ := f.startOK(t, w)
	if h, held, err := w.CoreRunning(); err != nil || !held || h.PID != pid {
		t.Fatalf("core.lock = %+v, held %v, %v; want the new core's record", h, held, err)
	}
}

// A core that does not stop on SIGTERM is killed when the wait is over,
// and the socket it left is removed.
func TestCoreStopKillsACoreThatIgnoresSIGTERM(t *testing.T) {
	w := coreWorkspace(t)
	f := newFakeCores(t, "ignore-term")
	f.stopWait = 500 * time.Millisecond
	pid, _ := f.startOK(t, w)

	var out bytes.Buffer
	if err := f.stop(w, &out); err != nil {
		t.Fatalf("stop: %v\n%s", err, out.String())
	}
	if want := fmt.Sprintf("mate core: pid %d did not stop within 500ms; killing it\nmate core: killed\n", pid); !strings.HasSuffix(out.String(), want) {
		t.Fatalf("stop said %q, want …%q", out.String(), want)
	}
	if _, held, _ := w.CoreRunning(); held {
		t.Fatal("core.lock still held after SIGKILL")
	}
	if _, err := os.Stat(w.CoreSocket()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the killed core's socket is still there: %v", err)
	}
}

// A new binary installed over the one a core started from makes status
// say stale; restart runs the new one and the line is gone.
func TestCoreStaleAfterInstallUntilRestart(t *testing.T) {
	w := coreWorkspace(t)
	f := newFakeCores(t, "serve")
	f.binary = w.StateDir() + "/fake-mate"
	if err := os.WriteFile(f.binary, []byte("build 1"), 0o755); err != nil {
		t.Fatal(err)
	}
	const stale = "stale: the mate binary changed after this core started; run `mate core restart`"
	f.startOK(t, w)
	if out, err := f.statusOut(t, w); err != nil || strings.Contains(out, stale) {
		t.Fatalf("status before the install = %v:\n%s", err, out)
	}
	if err := os.WriteFile(f.binary, []byte("build 2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := f.statusOut(t, w); err != nil || !strings.Contains(out, stale+"\n") {
		t.Fatalf("status after the install = %v, want %q:\n%s", err, stale, out)
	}
	var out bytes.Buffer
	if err := f.restart(w, &out); err != nil {
		t.Fatalf("restart: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "mate core: stopped\n") || !strings.Contains(out.String(), "mate core: started (pid ") {
		t.Fatalf("restart said %q, want a stop then a start", out.String())
	}
	if st, err := f.statusOut(t, w); err != nil || strings.Contains(st, stale) {
		t.Fatalf("status after restart = %v:\n%s", err, st)
	}
}

// A core.log past 10 MB becomes core.log.1 at start, replacing the one
// there, and the new core writes a fresh core.log.
func TestCoreStartRotatesABigLog(t *testing.T) {
	w := coreWorkspace(t)
	big, err := os.Create(w.CoreLogFile())
	if err != nil {
		t.Fatal(err)
	}
	if err := big.Truncate(coreLogMax + 1); err != nil {
		t.Fatal(err)
	}
	big.Close()
	if err := os.WriteFile(w.CoreLogFile()+".1", []byte("older still"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := newFakeCores(t, "serve")
	f.startOK(t, w)
	if fi, err := os.Stat(w.CoreLogFile() + ".1"); err != nil || fi.Size() != coreLogMax+1 {
		t.Fatalf("core.log.1 = %v, %v; want the big log", fi, err)
	}
	waitFor(t, func() bool {
		data, _ := os.ReadFile(w.CoreLogFile())
		return strings.Contains(string(data), "fake core up")
	})
	if fi, _ := os.Stat(w.CoreLogFile()); fi.Size() > 1024 {
		t.Fatalf("core.log is %d bytes, want a fresh one", fi.Size())
	}

	// A log at the limit is kept.
	var stop bytes.Buffer
	if err := f.stop(w, &stop); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(w.CoreLogFile(), coreLogMax); err != nil {
		t.Fatal(err)
	}
	f.startOK(t, w)
	if fi, _ := os.Stat(w.CoreLogFile()); fi.Size() < coreLogMax {
		t.Fatalf("a log of exactly 10 MB was moved (core.log is %d bytes)", fi.Size())
	}
}

// A core that never answers is given up on after the wait, and one that
// ends at once is said at once; both point at core.log, and exit 1.
func TestCoreStartSaysWhenTheCoreDoesNotComeUp(t *testing.T) {
	t.Run("no answer", func(t *testing.T) {
		w := coreWorkspace(t)
		f := newFakeCores(t, "mute")
		f.startWait = 500 * time.Millisecond
		var out bytes.Buffer
		err := f.start(w, &out)
		if want := "mate core: no answer within 500ms; see .mate/core.log"; err == nil || err.Error() != want {
			t.Fatalf("start = %v, want %q", err, want)
		}
	})
	t.Run("ends at once", func(t *testing.T) {
		w := coreWorkspace(t)
		f := newFakeCores(t, "fail")
		var out bytes.Buffer
		err := f.start(w, &out)
		if err == nil || !strings.Contains(err.Error(), "ended at once") || !strings.HasSuffix(err.Error(), "see .mate/core.log") {
			t.Fatalf("start = %v, want it ended at once", err)
		}
		if log, _ := os.ReadFile(w.CoreLogFile()); !strings.Contains(string(log), "fake core: failing at once") {
			t.Errorf("core.log = %q, want the core's last words", log)
		}
	})
}

// An engine inside a console: start refuses (exit 1), stop refuses and
// signals nothing, status shows it; neither starts a process.
func TestCoreCommandsBesideAConsoleEngine(t *testing.T) {
	w := coreWorkspace(t)
	l, err := w.LockCore(store.CoreHolder{PID: 4343, Owner: store.CoreOwnerConsole})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Unlock()
	f := newFakeCores(t, "serve")
	f.signal = func(pid int, _ syscall.Signal) error {
		t.Errorf("signalled pid %d", pid)
		return nil
	}
	var out bytes.Buffer
	if err := f.start(w, &out); err == nil || err.Error() != "mate core: the engine runs inside mate console (pid 4343); quit that console first" {
		t.Fatalf("start = %v", err)
	}
	if err := f.stop(w, &out); err == nil || err.Error() != "mate core: the engine runs inside mate console (pid 4343); quit that console" {
		t.Fatalf("stop = %v", err)
	}
	if st, err := f.statusOut(t, w); err != nil || !strings.Contains(st, "  owner    console\n") || strings.Contains(st, "Log     ") {
		t.Fatalf("status = %v, want the console's engine and no core.log:\n%s", err, st)
	}
	if n := len(f.started()); n != 0 {
		t.Fatalf("started %d processes", n)
	}
}

// `mate <dir>` with the workspace's core running prints its status and
// the URLs and exits 0, starting nothing; with the engine in a console it
// refuses with exit 1; with none it runs the core.
func TestBareMateBesideARunningEngine(t *testing.T) {
	w := coreWorkspace(t)
	l, err := w.LockCore(store.CoreHolder{PID: 4242, Owner: store.CoreOwnerCore, URL: fakeCoreURL, Started: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := mainRun([]string{w.Root()}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
	for _, want := range []string{"mate core: already running\n", "mate core · pid 4242 · ", "Office  http://127.0.0.1:7777/office/\n", "City    http://127.0.0.1:7777/city/\n",
		"Herdr   " + w.Session() + "\n", "Log     " + w.CoreLogFile() + "\n"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout.String())
		}
	}
	l.Unlock()

	l, err = w.LockCore(store.CoreHolder{PID: 4343, Owner: store.CoreOwnerConsole})
	if err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := mainRun([]string{w.Root()}, &stdout, &stderr); code != 1 || stderr.String() != "mate: the engine runs inside mate console (pid 4343); quit that console first\n" {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
	l.Unlock()

	ran := 0
	if err := coreHere(w, liveCoreControl(), io.Discard, func() error { ran++; return nil }); err != nil || ran != 1 {
		t.Fatalf("coreHere with no engine = %v, ran %d; want the core run", err, ran)
	}
}

// The foreground block says the busy address instead of the two URLs
// when the dashboard's port is taken, and the core runs on its socket.
func TestCoreRunSaysABusyPort(t *testing.T) {
	w := coreWorkspace(t)
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	addr := busy.Addr().String()
	if err := os.WriteFile(w.StateDir()+"/console.yaml", []byte("dashboard:\n  addr: "+addr+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout, stderr lockedBuffer
	done := make(chan error, 1)
	go func() { done <- runCore(ctx, w, fakeCoreDeps(t), nil, nil, &stdout, &stderr) }()
	waitFor(t, func() bool { return strings.Contains(stdout.String(), "Ctrl+C to stop") })
	out := stdout.String()
	if want := "\nOffice/City: " + addr + " is in use\n"; !strings.Contains(out, want) {
		t.Errorf("stdout = %q, want %q", out, want)
	}
	if strings.Contains(out, "Office  ") {
		t.Errorf("stdout = %q, want no URL lines", out)
	}
	if h, _, _ := w.CoreRunning(); !strings.Contains(h.TCPError, "address already in use") {
		t.Errorf("core.lock tcp_error = %q", h.TCPError)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("runCore did not return")
	}
}

func TestOfficeCityLines(t *testing.T) {
	for _, tc := range []struct{ url, lan, tcpErr, want string }{
		{"http://127.0.0.1:7777/", "", "", "Office  http://127.0.0.1:7777/office/\nCity    http://127.0.0.1:7777/city/\n"},
		{"http://127.0.0.1:7777/", "http://192.168.1.5:7777/", "", "Office  http://127.0.0.1:7777/office/\nCity    http://127.0.0.1:7777/city/\nLAN     http://192.168.1.5:7777/office/\n"},
		{"", "", "dashboard not started: listen tcp 127.0.0.1:7777: bind: address already in use", "Office/City: 127.0.0.1:7777 is in use\n"},
		{"", "", "dashboard: 0.0.0.0:7777 is not a loopback address", "Office/City: not served: dashboard: 0.0.0.0:7777 is not a loopback address\n"},
		{"", "", "", ""},
	} {
		if got := officeCityLines(tc.url, tc.lan, tc.tcpErr); got != tc.want {
			t.Errorf("officeCityLines(%q, %q, %q) = %q, want %q", tc.url, tc.lan, tc.tcpErr, got, tc.want)
		}
	}
	if got := strconv.Quote(homeShort("/nowhere/ws")); got != `"/nowhere/ws"` {
		t.Errorf("homeShort = %s", got)
	}
}

// The foreground core writes its output to the terminal and to core.log,
// rotated past 10 MB as start does; the core start ran, whose output
// already is core.log, adds nothing, nor does a `mate core run` beside a
// running engine, whose log is not rotated under it.
func TestForegroundCoreAlsoWritesCoreLog(t *testing.T) {
	w := coreWorkspace(t)
	if w.Session() == "" {
		t.Fatal("the workspace names no Herdr session")
	}
	if err := os.WriteFile(w.CoreLogFile(), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(w.CoreLogFile(), coreLogMax+1); err != nil {
		t.Fatal(err)
	}
	var term, termErr bytes.Buffer
	stdout, stderr, closeLog, err := coreLogTee(w, &term, &termErr, nil)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(stdout, "out line")
	fmt.Fprintln(stderr, "err line")
	closeLog()
	if term.String() != "out line\n" || termErr.String() != "err line\n" {
		t.Fatalf("terminal got %q and %q", term.String(), termErr.String())
	}
	log, _ := os.ReadFile(w.CoreLogFile())
	if !strings.Contains(string(log), "--- mate core run ") || !strings.HasSuffix(string(log), "out line\nerr line\n") {
		t.Fatalf("core.log = %q, want both lines", log)
	}
	if fi, err := os.Stat(w.CoreLogFile() + ".1"); err != nil || fi.Size() != coreLogMax+1 {
		t.Fatalf("core.log.1 = %v, %v; want the big log moved", fi, err)
	}

	// The detached core: its standard output is core.log itself.
	self, err := os.OpenFile(w.CoreLogFile(), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer self.Close()
	if out, errw, _, err := coreLogTee(w, &term, &termErr, self); err != nil || out != io.Writer(&term) || errw != io.Writer(&termErr) {
		t.Fatalf("tee over core.log itself = %v; want the writers unchanged", err)
	}

	// Beside a running engine: nothing rotated, nothing added.
	if err := os.Truncate(w.CoreLogFile(), coreLogMax+1); err != nil {
		t.Fatal(err)
	}
	l, err := w.LockCore(store.CoreHolder{PID: 4242, Owner: store.CoreOwnerCore})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Unlock()
	if out, _, _, err := coreLogTee(w, &term, &termErr, nil); err != nil || out != io.Writer(&term) {
		t.Fatalf("tee beside an engine = %v; want the writers unchanged", err)
	}
	if fi, _ := os.Stat(w.CoreLogFile()); fi.Size() != coreLogMax+1 {
		t.Fatal("core.log rotated under a running engine")
	}
}

// A core that lets go of core.lock while another engine takes it at once,
// between two looks of stop's: stop sees the holder change and sends no
// SIGKILL to the old pid, which may be some other process's by then.
func TestCoreStopNeverKillsAPidThatNoLongerHoldsTheLock(t *testing.T) {
	w := coreWorkspace(t)
	old, err := w.LockCore(store.CoreHolder{PID: 5151, Owner: store.CoreOwnerCore})
	if err != nil {
		t.Fatal(err)
	}
	f := newFakeCores(t, "serve")
	f.stopWait = 300 * time.Millisecond
	var next *store.CoreLock
	t.Cleanup(func() {
		if next != nil {
			next.Unlock()
		}
	})
	var sent []syscall.Signal
	f.signal = func(pid int, sig syscall.Signal) error {
		sent = append(sent, sig)
		if pid != 5151 {
			t.Errorf("signalled pid %d", pid)
		}
		if sig == syscall.SIGTERM {
			old.Unlock()
			if next, err = w.LockCore(store.CoreHolder{PID: 6262, Owner: store.CoreOwnerCore}); err != nil {
				t.Error(err)
			}
		}
		return nil
	}
	var out bytes.Buffer
	if err := f.stop(w, &out); err != nil || !strings.HasSuffix(out.String(), "mate core: stopped\n") {
		t.Fatalf("stop = %v, %q", err, out.String())
	}
	if len(sent) != 1 || sent[0] != syscall.SIGTERM {
		t.Fatalf("signals = %v, want SIGTERM alone", sent)
	}
}

// Two starts at once: the one whose core lost the lock says the other's
// runs, exit 0, rather than that its own ended.
func TestCoreStartBesideAStartThatWon(t *testing.T) {
	w := coreWorkspace(t)
	f := newFakeCores(t, "fail")
	command := f.command
	var won *store.CoreLock
	t.Cleanup(func() {
		if won != nil {
			won.Unlock()
		}
	})
	f.command = func(root string) (*exec.Cmd, error) {
		var err error
		if won, err = w.LockCore(store.CoreHolder{PID: 7373, Owner: store.CoreOwnerCore, Started: time.Now()}); err != nil {
			return nil, err
		}
		return command(root)
	}
	var out bytes.Buffer
	if err := f.start(w, &out); err != nil || !strings.HasPrefix(out.String(), "mate core: already running (pid 7373, since ") {
		t.Fatalf("start = %v, %q", err, out.String())
	}
}
