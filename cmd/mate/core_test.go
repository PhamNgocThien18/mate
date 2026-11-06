package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/core"
	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/coreclient"
	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/notice"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// `mate core run` (CON-211): the engine with no console, on a socket and
// over TCP, one per workspace.

// coreWorkspace is a workspace under /tmp - a unix socket path holds 103
// bytes on macOS, and t.TempDir() there leaves `.mate/core.sock` no room -
// whose dashboard binds a free port, never the 7777 a real console holds.
func coreWorkspace(t *testing.T) *store.Workspace {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "mc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	w, err := store.Init(dir, workspaceDefaults())
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	if err := w.AddProject("shop", store.ProjectConfig{}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	if err := os.WriteFile(w.StateDir()+"/console.yaml", []byte("dashboard:\n  addr: 127.0.0.1:0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return w
}

// fakeCoreDeps are deps over the fake Herdr, with no Sleep: the sender loop
// sleeps its interval through it.
func fakeCoreDeps(t *testing.T) spawn.Deps {
	rt := runtime.NewFake()
	return spawn.Deps{Harnesses: harnesses, Runtime: rt, Names: rt.Names, ConfigHome: t.TempDir()}
}

func unixGet(t *testing.T, sock, path string) int {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", sock)
	}}}
	defer client.CloseIdleConnections()
	resp, err := client.Get("http://core" + path)
	if err != nil {
		t.Fatalf("GET %s over the socket: %v", path, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

// The core answers GET /api/workspace on its socket and over TCP; a second
// core on the workspace is refused with the first one's pid; and when its
// context ends - Ctrl+C - it stops cleanly: core.lock released, socket gone.
func TestCoreRunServesSocketAndTCPThenStopsCleanly(t *testing.T) {
	w := coreWorkspace(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout, stderr lockedBuffer
	done := make(chan error, 1)
	go func() {
		done <- runCore(ctx, w, fakeCoreDeps(t), nil, []string{"observer: broken"}, &stdout, &stderr)
	}()
	waitFor(t, func() bool { return strings.Contains(stdout.String(), "Ctrl+C to stop") })

	h, held, err := w.CoreRunning()
	if err != nil || !held || h.Owner != core.OwnerCore || h.PID != os.Getpid() || h.URL == "" || h.Socket != w.CoreSocket() {
		t.Fatalf("core.lock = %+v, held %v, %v", h, held, err)
	}
	// The block a dev server prints (decided 2026-11-06): who, where, and
	// the two pages, before Ctrl+C.
	if want := "mate core · pid " + strconv.Itoa(os.Getpid()) + " · " + homeShort(w.Root()) + "\nOffice  " + h.URL + "office/\nCity    " + h.URL + "city/\nHerdr   " + w.Session() + "\nLog     " + w.CoreLogFile() + "\nCtrl+C to stop\n"; !strings.HasPrefix(stdout.String(), want) {
		t.Errorf("stdout = %q, want it to start with %q", stdout.String(), want)
	}
	if status := unixGet(t, w.CoreSocket(), "/api/workspace"); status != http.StatusOK {
		t.Fatalf("socket: %d", status)
	}
	resp, err := http.Get(h.URL + "api/workspace")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tcp: %d", resp.StatusCode)
	}

	// Its clients' API (CON-212): the core says what the caller found
	// wrong, and its snapshot and actions are the engine's.
	client, err := coreclient.Dial(w.CoreSocket(), coreclient.As(coreapi.ClientConsole))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if st, err := client.Core(ctx); err != nil || st.PID != os.Getpid() || st.URL != h.URL ||
		len(st.Notices) != 1 || st.Notices[0] != "observer: broken" {
		t.Fatalf("Core = %+v, %v; want this process and the observer's notice", st, err)
	}
	if snap, err := client.Snapshot(ctx); err != nil || len(snap.Projects) != 1 {
		t.Fatalf("Snapshot = %+v, %v; want shop", snap.Projects, err)
	}

	err = runCore(context.Background(), w, fakeCoreDeps(t), nil, nil, io.Discard, io.Discard)
	if want := "mate core: already running (pid " + strconv.Itoa(os.Getpid()) + ")"; err == nil || err.Error() != want {
		t.Fatalf("second core = %v, want %q", err, want)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runCore: %v", err)
		}
	case <-time.After(core.StopWait + 5*time.Second):
		t.Fatal("runCore did not return after its context ended")
	}
	if _, held, _ := w.CoreRunning(); held {
		t.Error("core.lock held after the core stopped")
	}
	if _, err := os.Stat(w.CoreSocket()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("socket after the core stopped: %v", err)
	}
	if !strings.Contains(stdout.String(), "mate core: stopped") {
		t.Errorf("stdout = %q, want the stop said", stdout.String())
	}
}

// GET /api/core says Jev's error before the observer's, and one error both
// report once, as the console's status line does.
func TestCoreNoticesPutJevFirst(t *testing.T) {
	jev := errors.New("jev: no API key")
	for _, tc := range []struct {
		jev     error
		notices []string
		want    []string
	}{
		{nil, nil, nil},
		{nil, []string{"observer: broken"}, []string{"observer: broken"}},
		{jev, nil, []string{"jev: no API key"}},
		{jev, []string{"observer: broken"}, []string{"jev: no API key", "observer: broken"}},
		{jev, []string{"jev: no API key"}, []string{"jev: no API key"}},
	} {
		if got := jevNoticeFirst(tc.jev, tc.notices); strings.Join(got, "|") != strings.Join(tc.want, "|") || len(got) != len(tc.want) {
			t.Errorf("jevNoticeFirst(%v, %q) = %q, want %q", tc.jev, tc.notices, got, tc.want)
		}
	}
}

// The core has Jev only when `.mate/.env` turns it on (CON-232). Off,
// consoleNoticeClient's nil client must not reach core.Options.Notice as a
// non-nil interface: the core says it has no Jev, so its pages offer none,
// and explain_notice is refused as it was before the action moved into the
// core. On, the core says it has one.
func TestCoreWiresJevOnlyWhenItIsOn(t *testing.T) {
	if engineNotice(nil) != nil {
		t.Fatal("engineNotice(nil) is a non-nil Jev")
	}
	if engineNotice(notice.New("k")) == nil {
		t.Fatal("engineNotice dropped a client")
	}

	run := func(t *testing.T, w *store.Workspace) (coreapi.CoreStatus, error) {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		var stdout lockedBuffer
		done := make(chan error, 1)
		go func() { done <- runCore(ctx, w, fakeCoreDeps(t), nil, nil, &stdout, io.Discard) }()
		defer func() {
			cancel()
			if err := <-done; err != nil {
				t.Errorf("runCore: %v", err)
			}
		}()
		waitFor(t, func() bool { return strings.Contains(stdout.String(), "Ctrl+C to stop") })
		client, err := coreclient.Dial(w.CoreSocket(), coreclient.As(coreapi.ClientConsole))
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		st, err := client.Core(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, actErr := client.Act(ctx, coreapi.ActionRequest{Action: coreapi.ActionNotice, Target: "shop", TargetKind: "crew", Crew: "buy"})
		return st, actErr
	}

	w := coreWorkspace(t)
	st, err := run(t, w)
	if st.Jev {
		t.Error("a core with Jev off says it has Jev")
	}
	if err == nil || err.Error() != "usage: explain_notice is not wired in this build" {
		t.Errorf("explain_notice with Jev off = %v, want it not wired", err)
	}

	key := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(key, []byte("test-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeEnv(t, w, "MATE_JEV_API_KEY_FILE="+key+"\n")
	st, err = run(t, w)
	if !st.Jev {
		t.Error("a core with Jev on says it has none")
	}
	// The core's own action: it looks the crew up first, and buy is not
	// recorded, so it stops there and Jev is never asked over the network.
	if err == nil || !strings.Contains(err.Error(), "crew buy of shop is not running") {
		t.Errorf("explain_notice with Jev on = %v, want the core's own action refusing a crew that is not running", err)
	}
}

// The refusals of `mate core run`, through the command as typed: exit 1 and
// the sentence. Each one stops before the engine starts, so nothing here
// reaches the live Herdr the command builds its deps over.
func TestCoreRunRefusesBesideAnotherEngine(t *testing.T) {
	for _, tc := range []struct {
		name string
		hold func(t *testing.T, w *store.Workspace) func()
		want string
	}{
		{"another core", func(t *testing.T, w *store.Workspace) func() {
			l, err := w.LockCore(store.CoreHolder{PID: 4242, Owner: store.CoreOwnerCore})
			if err != nil {
				t.Fatal(err)
			}
			return l.Unlock
		}, "mate core: already running (pid 4242)"},
		{"a console's engine", func(t *testing.T, w *store.Workspace) func() {
			unlockConsole, err := w.LockConsole(store.ConsoleHolder{PID: 4343, CoreLock: true})
			if err != nil {
				t.Fatal(err)
			}
			l, err := w.LockCore(store.CoreHolder{PID: 4343, Owner: store.CoreOwnerConsole})
			if err != nil {
				t.Fatal(err)
			}
			return func() { l.Unlock(); unlockConsole() }
		}, "mate core: the engine runs inside mate console (pid 4343); quit that console first"},
		{"a console built before CON-211", func(t *testing.T, w *store.Workspace) func() {
			unlock, err := w.LockConsole(store.ConsoleHolder{PID: 4444})
			if err != nil {
				t.Fatal(err)
			}
			return unlock
		}, "mate core: console pid 4444 runs an older mate with its own engine; quit it first"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := coreWorkspace(t)
			defer tc.hold(t, w)()
			var stdout, stderr bytes.Buffer
			if code := mainRun([]string{"core", "run", w.Root()}, &stdout, &stderr); code != 1 {
				t.Fatalf("exit %d, want 1; stderr %q", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), tc.want)
			}
			if _, err := os.Stat(w.CoreSocket()); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("a refused core left a socket: %v", err)
			}
			// Refused before its parts were built: it never opened the
			// timeline another engine writes.
			if _, err := os.Stat(filepath.Join(w.StateDir(), "mate.db")); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("a refused core opened mate.db: %v", err)
			}
		})
	}
}

func TestCoreUsage(t *testing.T) {
	for _, args := range [][]string{{"core"}, {"core", "bogus"}, {"core", "run", "a", "b"}, {"core", "status", "a", "--workspace", "b"}} {
		var stdout, stderr bytes.Buffer
		if code := mainRun(args, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), coreUsage) {
			t.Errorf("mate %v: exit %d, stderr %q; want the usage", args, code, stderr.String())
		}
	}
}

// `mate reindex` with mate.db held names the process holding the engine;
// with no engine it keeps the database's own refusal.
func TestReindexNamesTheEngineHoldingTheTimeline(t *testing.T) {
	for _, tc := range []struct {
		owner string
		want  string
	}{
		{store.CoreOwnerCore, "mate reindex: mate core (pid 5151) holds mate.db; run `mate core stop` first"},
		{store.CoreOwnerConsole, "mate reindex: mate console (pid 5151) holds mate.db; quit it first"},
		{"", db.ErrLocked.Error()},
	} {
		t.Run("owner="+tc.owner, func(t *testing.T) {
			w := coreWorkspace(t)
			writer, err := db.Open(w)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			if tc.owner != "" {
				l, err := w.LockCore(store.CoreHolder{PID: 5151, Owner: tc.owner})
				if err != nil {
					t.Fatal(err)
				}
				defer l.Unlock()
			}
			var stdout, stderr bytes.Buffer
			if code := mainRun([]string{"reindex", w.Root()}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("exit %d, stderr %q; want 1 and %q", code, stderr.String(), tc.want)
			}
		})
	}
}

// The engine of `mate core run` says no console is open (ConsolePresent
// false), and the auto daemon goes on regardless (CON-203, decided
// 2026-11-05): a Manual project the captain left five minutes ago is turned
// back to Auto (M21), and its crew's hand-back reaches the Mate as a digest.
func TestEngineWithNoConsoleRearmsAndDigests(t *testing.T) {
	w, deps := consoleFixture(t, "shop")
	if _, err := spawn.StartMate(context.Background(), w, deps, spawn.StartRequest{Project: "shop"}); err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	rt := deps.Runtime.(*runtime.Fake)
	handle, _, err := spawn.MateHandle(context.Background(), w, deps, "shop")
	if err != nil {
		t.Fatalf("MateHandle: %v", err)
	}
	rt.SetReadOutput(handle, claudeEmptyScreen)
	if err := w.SetMode("shop", false); err != nil {
		t.Fatal(err)
	}
	away := time.Now().Add(-store.QuietAfter - time.Minute)
	if err := os.Chtimes(w.ManualHold("shop"), away, away); err != nil {
		t.Fatal(err)
	}
	if err := w.AppendStatus("shop", "k3", "needs-decision: pick A or B"); err != nil {
		t.Fatal(err)
	}
	// The live deps set no Sleep; the fixture's instant one would make the
	// sender loop spin.
	deps.Sleep = nil
	digested := make(chan struct{})
	var once sync.Once
	rt.OnSendText = func(_ runtime.AgentHandle, text string) {
		if strings.HasPrefix(text, send.Marker+"digest: 1 item(s)") {
			once.Do(func() { close(digested) })
		}
	}

	opts, err := engineOptions(w, deps, func() bool { return false })
	if err != nil {
		t.Fatalf("engineOptions: %v", err)
	}
	opts.Owner = core.OwnerCore
	engine := core.New(opts)
	if engine.ConsolePresent() {
		t.Fatal("the core's engine says a console is open")
	}
	if err := engine.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(engine.Stop)

	select {
	case <-digested:
	case <-time.After(10 * time.Second):
		t.Fatal("no digest typed into the Mate")
	}
	if !w.Auto("shop") {
		t.Error("the Manual project the captain left was not turned back to Auto")
	}
}
