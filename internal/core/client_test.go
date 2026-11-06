package core

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/coreclient"
	"github.com/nguyenngocanh94/mate/internal/dashboard"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// The core's API for its clients on `.mate/core.sock` (CON-212), end to
// end: an engine as `mate core run` builds one, and internal/coreclient.

// socketEngine starts an engine over w as `mate core run` does, serving
// the dashboard (o's, when it has one) on a free port (or addr) and on
// core.sock, with o's other options, until the test ends.
func socketEngine(t *testing.T, w *store.Workspace, o Options, addr ...string) *Engine {
	t.Helper()
	o.Workspace, o.Harnesses, o.Owner, o.Stderr = w, testHarnesses, OwnerCore, io.Discard
	bind := freeAddr
	if len(addr) > 0 {
		bind = addr[0]
	}
	if o.Dashboard == nil {
		o.Dashboard = &DashboardOptions{}
	}
	o.Dashboard.Addr, o.Dashboard.Socket = bind, w.CoreSocket()
	e := New(o)
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(e.Stop)
	return e
}

// dial is a client of w's core, as the console (coreclient.As) when asked.
func dial(t *testing.T, w *store.Workspace, opts ...coreclient.Option) *coreclient.Client {
	t.Helper()
	c, err := coreclient.Dial(w.CoreSocket(), opts...)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(c.Close)
	return c
}

// tcpDo is one request over TCP; it returns the status.
func tcpDo(t *testing.T, method, url string, header ...string) int {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(`{"action":"mode","target":"shop"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode
}

// The three client routes answer on the socket and 404 over TCP, where a
// page could reach them; nothing runs for a POST over TCP.
func TestClientRoutesAreTheSocketsAlone(t *testing.T) {
	w := shortWorkspace(t)
	withTimelineDB(t, w)
	h := newHeldActs()
	e := socketEngine(t, w, Options{Act: h.act})
	base := strings.TrimRight(e.Serving().URL, "/")
	for _, r := range []struct{ method, path string }{
		{http.MethodGet, coreapi.PathCore},
		{http.MethodGet, coreapi.PathSnapshot},
		{http.MethodPost, coreapi.PathAct},
	} {
		if got := tcpDo(t, r.method, base+r.path); got != http.StatusNotFound {
			t.Errorf("%s %s over TCP = %d, want 404", r.method, r.path, got)
		}
	}
	if got := h.count(); got != 0 {
		t.Fatalf("a POST over TCP ran %d actions", got)
	}

	c := dial(t, w)
	ctx := context.Background()
	if st, err := c.Core(ctx); err != nil || st.PID != os.Getpid() {
		t.Fatalf("Core over the socket = %+v, %v", st, err)
	}
	if snap, err := c.Snapshot(ctx); err != nil || len(snap.Projects) != 1 || snap.Projects[0].ProjectID != "shop" {
		t.Fatalf("Snapshot over the socket = %+v, %v; want shop", snap.Projects, err)
	}
	if line, err := c.Act(ctx, coreapi.ActionRequest{Action: coreapi.ActionMode, Target: "shop"}); err != nil || line != "did mode" {
		t.Fatalf("Act over the socket = %q, %v", line, err)
	}
	// The pages' routes are on the socket too, as before.
	if status, _ := socketGet(t, w.CoreSocket(), "/api/workspace"); status != http.StatusOK {
		t.Fatalf("/api/workspace over the socket = %d", status)
	}
}

// Client.Core is everything core.lock records of the engine, where it
// serves, whether Jev is there and the notices, the engine's first and the
// caller's after.
func TestClientCoreIsTheEnginesRecord(t *testing.T) {
	w := shortWorkspace(t)
	withTimelineDB(t, w)
	socketEngine(t, w, Options{
		Dashboard: &DashboardOptions{Deps: dashboard.Deps{Notice: true}},
		Notices:   []string{"observer: jev.yaml is broken"},
	})
	st, err := dial(t, w).Core(context.Background())
	if err != nil {
		t.Fatalf("Core: %v", err)
	}
	h, held, err := w.CoreRunning()
	if err != nil || !held {
		t.Fatalf("CoreRunning = %v, %v", held, err)
	}
	want := coreapi.CoreStatus{
		PID: h.PID, Owner: OwnerCore, Version: h.Version, Build: h.Build, Binary: h.Binary,
		URL: h.URL, LANURL: h.LANURL, TCPError: "", Jev: true,
		Notices: []string{"observer: jev.yaml is broken"},
	}
	if !st.Started.Equal(h.Started) || st.Started.IsZero() {
		t.Errorf("Started = %v, want core.lock's %v", st.Started, h.Started)
	}
	st.Started = time.Time{}
	if !reflect.DeepEqual(st, want) {
		t.Fatalf("Core = %+v\nwant  %+v", st, want)
	}
	if st.PID != os.Getpid() || st.Build == "" || st.Binary == "" || st.Version == "" || st.URL == "" {
		t.Fatalf("Core = %+v, want this process, its build and its URL", st)
	}
}

// With its TCP port taken the core runs on the socket alone, and says so:
// tcp_error, and the dashboard's error first among the notices, in the
// words the console's status line uses.
func TestClientCoreNoticesABusyPort(t *testing.T) {
	busy, err := net.Listen("tcp", freeAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	w := shortWorkspace(t)
	withTimelineDB(t, w)
	socketEngine(t, w, Options{Notices: []string{"jev: no key"}}, busy.Addr().String())
	st, err := dial(t, w).Core(context.Background())
	if err != nil {
		t.Fatalf("Core: %v", err)
	}
	if st.URL != "" || !strings.HasPrefix(st.TCPError, "dashboard not started: ") || st.Jev {
		t.Fatalf("Core = %+v, want no URL, the TCP error and no Jev", st)
	}
	if len(st.Notices) != 2 || st.Notices[0] != st.TCPError || st.Notices[1] != "jev: no key" {
		t.Fatalf("Notices = %q, want the dashboard's error then the caller's", st.Notices)
	}
}

// Client.Snapshot is the engine's Snapshot, every field of every type in
// it brought back by Go's default JSON, except Commands, which are a
// console's own keys.
func TestClientSnapshotIsTheEnginesInFull(t *testing.T) {
	w := shortWorkspace(t)
	withTimelineDB(t, w)
	var full query.Snapshot
	(&filler{t: t, on: map[reflect.Type]bool{}}).fill(reflect.ValueOf(&full).Elem(), "Snapshot")
	if len(full.Commands) == 0 {
		t.Fatal("the filled snapshot has no Commands to leave out")
	}
	e := New(Options{
		Workspace: w, Harnesses: testHarnesses, Owner: OwnerCore, Stderr: io.Discard,
		Act:       newHeldActs().act,
		Dashboard: &DashboardOptions{Addr: freeAddr, Socket: w.CoreSocket()},
	})
	e.snaps.compute = func(context.Context) (query.Snapshot, error) { return full, nil }
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(e.Stop)

	got, err := dial(t, w).Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	want := full
	want.Commands = nil
	if diff := sameValue(reflect.ValueOf(got), reflect.ValueOf(want), "Snapshot"); diff != "" {
		t.Fatal(diff)
	}
	if mine, _ := e.Snapshot(context.Background()); len(mine.Commands) == 0 {
		t.Fatal("the engine's own snapshot lost its Commands")
	}
}

// A snapshot the engine cannot read is the read's error at the client, in
// its words.
func TestClientSnapshotSaysWhyItCouldNotRead(t *testing.T) {
	w := shortWorkspace(t)
	withTimelineDB(t, w)
	e := New(Options{
		Workspace: w, Harnesses: testHarnesses, Owner: OwnerCore, Stderr: io.Discard,
		Act:       newHeldActs().act,
		Dashboard: &DashboardOptions{Addr: freeAddr, Socket: w.CoreSocket()},
	})
	e.snaps.compute = func(context.Context) (query.Snapshot, error) {
		return query.Snapshot{}, errors.New("workspace.yaml: yaml: line 3: mapping values are not allowed")
	}
	if err := e.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(e.Stop)
	_, err := dial(t, w).Snapshot(context.Background())
	var cerr *coreclient.Error
	if !errors.As(err, &cerr) || cerr.Status != http.StatusServiceUnavailable ||
		err.Error() != "workspace.yaml: yaml: line 3: mapping values are not allowed" {
		t.Fatalf("Snapshot error = %v (%T), want the read's error as a 503", err, err)
	}
}

// Client.Act runs every action coreapi names, the six of the chat on the
// Mate's host and the rest through Options.Act, with the request the client
// sent, field for field.
func TestClientActRunsEveryAction(t *testing.T) {
	w := hostWorkspace(t)
	withTimelineDB(t, w)
	calls := fakeHost(t, w.MateDir("shop"))
	h := newHeldActs()
	socketEngine(t, w, Options{Act: h.act})
	c := dial(t, w)

	actions := coreapiActions(t)
	if len(actions) < 24 {
		t.Fatalf("found %d actions in coreapi, want every one", len(actions))
	}
	// What each chat action asks the host: a say carries no news, an
	// interrupt and a fork_close no text, an undo the held item its Input
	// names.
	hostOp := map[coreapi.Action]hostCall{
		coreapi.ActionSay:       {Op: matehost.OpSubmit, Text: "text of say"},
		coreapi.ActionInterrupt: {Op: matehost.OpInterrupt, News: 7},
		coreapi.ActionAnswer:    {Op: matehost.OpAnswer, Text: "text of answer", News: 7},
		coreapi.ActionFork:      {Op: matehost.OpSessionAsk, Text: "text of fork", News: 7},
		coreapi.ActionForkClose: {Op: matehost.OpSessionClose, News: 7},
		coreapi.ActionUndo:      {Op: matehost.OpUndo, Held: 4},
	}
	ran := 0
	for _, a := range actions {
		req := coreapi.ActionRequest{
			Action: a, Target: "shop", TargetKind: "mate", Input: "text of " + string(a), News: 7,
			Repo: "../shop", Crew: "buy", Key: "k-" + string(a), Harness: "claude", HeadlessOnly: true,
		}
		if a == coreapi.ActionUndo {
			req.Input = "4"
		}
		line, err := c.Act(context.Background(), req)
		if err != nil {
			t.Errorf("%s: %v", a, err)
			continue
		}
		if call, ok := hostOp[a]; ok {
			if got := calls.last(); got != call {
				t.Errorf("%s reached the host as %+v, want %+v", a, got, call)
			}
			continue
		}
		ran++
		if line != "did "+string(a) {
			t.Errorf("%s = %q, want Options.Act's line", a, line)
		}
		h.mu.Lock()
		last := h.ran[len(h.ran)-1]
		h.mu.Unlock()
		if last != req {
			t.Errorf("%s reached Options.Act as %+v, want %+v", a, last, req)
		}
	}
	if ran != len(actions)-len(hostOp) || h.count() != ran {
		t.Fatalf("Options.Act ran %d times for %d actions outside the chat", h.count(), len(actions)-len(hostOp))
	}
	if got := len(calls.all()); got != len(hostOp) {
		t.Fatalf("the host was asked %d times, want once for each chat action", got)
	}
}

// coreapiActions are the values of every constant of type coreapi.Action,
// read from its source so a new one is covered here without a word.
func coreapiActions(t *testing.T) []coreapi.Action {
	t.Helper()
	path := filepath.Join("..", "coreapi", "coreapi.go")
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []coreapi.Action
	for _, d := range f.Decls {
		gen, ok := d.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			if id, ok := vs.Type.(*ast.Ident); !ok || id.Name != "Action" {
				continue
			}
			for _, v := range vs.Values {
				lit, ok := v.(*ast.BasicLit)
				if !ok {
					t.Fatalf("an Action constant in %s is not a literal", path)
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatal(err)
				}
				out = append(out, coreapi.Action(s))
			}
		}
	}
	return out
}

// The refusals come back as the engine's errors: a busy target as its
// *coreapi.BusyError, verbatim; a Mate with no REPL as ErrNoMateREPL
// whatever wrapped it; any other failure in its own words, with no code.
func TestClientActErrorsAreTheEngines(t *testing.T) {
	w := shortWorkspace(t)
	withTimelineDB(t, w)
	h := newHeldActs()
	act := func(ctx context.Context, req coreapi.ActionRequest) (string, error) {
		switch req.Action {
		case coreapi.ActionOpenWork:
			return "", fmt.Errorf("open work in shop: %w", coreapi.ErrNoMateREPL)
		case coreapi.ActionMerge:
			return "", errors.New("merge refused: the branch is not a fast-forward")
		}
		return h.act(ctx, req)
	}
	e := socketEngine(t, w, Options{Act: act})
	c := dial(t, w)
	ctx := context.Background()

	done := hold(t, e, h, stopMate)
	_, err := c.Act(ctx, stopMate)
	var busy *coreapi.BusyError
	if !errors.Is(err, coreapi.ErrBusy) || !errors.As(err, &busy) ||
		err.Error() != "Action refused: stop is still running on shop · nothing started" {
		t.Fatalf("a second stop = %v (%T), want the engine's BusyError", err, err)
	}
	close(h.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	_, err = c.Act(ctx, coreapi.ActionRequest{Action: coreapi.ActionOpenWork, Target: "shop"})
	var cerr *coreclient.Error
	if !errors.Is(err, coreapi.ErrNoMateREPL) || errors.Is(err, coreapi.ErrBusy) ||
		!errors.As(err, &cerr) || cerr.Status != http.StatusUnprocessableEntity ||
		err.Error() != "open work in shop: the Mate runs without the REPL" {
		t.Fatalf("open_work = %v, want ErrNoMateREPL as a 422 in the engine's words", err)
	}

	_, err = c.Act(ctx, mergeCrew)
	if !errors.As(err, &cerr) || cerr.Status != http.StatusUnprocessableEntity || cerr.Code != "" ||
		errors.Is(err, coreapi.ErrBusy) || errors.Is(err, coreapi.ErrNoMateREPL) ||
		err.Error() != "merge refused: the branch is not a fast-forward" {
		t.Fatalf("a failed merge = %v, want its own words as a 422 with no code", err)
	}
}

// A client that goes away in the middle of an action does not stop it: the
// action runs to its end on a context nobody cancels, and holds its target
// until then.
func TestClientGoneMidActionTheActionFinishes(t *testing.T) {
	w := shortWorkspace(t)
	withTimelineDB(t, w)
	started := make(chan struct{})
	release := make(chan struct{})
	outcome := make(chan string, 1)
	act := func(ctx context.Context, req coreapi.ActionRequest) (string, error) {
		if req.Action != coreapi.ActionMerge {
			return "did " + string(req.Action), nil
		}
		close(started)
		select {
		case <-release:
			outcome <- "finished"
			return "merged", nil
		case <-ctx.Done():
			outcome <- "cancelled: " + ctx.Err().Error()
			return "", ctx.Err()
		}
	}
	socketEngine(t, w, Options{Act: act})
	c := dial(t, w)

	ctx, cancel := context.WithCancel(context.Background())
	gone := make(chan error, 1)
	go func() {
		_, err := c.Act(ctx, mergeCrew)
		gone <- err
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the merge never started")
	}
	cancel()
	if err := <-gone; !errors.Is(err, context.Canceled) {
		t.Fatalf("the client's wait = %v, want it cancelled", err)
	}
	// Time for the server to see the connection gone and cancel the
	// request's context, which the action must not get.
	select {
	case o := <-outcome:
		t.Fatalf("the action ended when its client went away: %s", o)
	case <-time.After(500 * time.Millisecond):
	}
	if _, err := dial(t, w).Act(context.Background(), mergeCrew); !errors.Is(err, coreapi.ErrBusy) {
		t.Fatalf("a merge while the first still runs = %v, want ErrBusy", err)
	}
	close(release)
	select {
	case o := <-outcome:
		if o != "finished" {
			t.Fatalf("the action %s", o)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the action never finished")
	}
}

// A request on the socket that says it is the console makes ConsolePresent
// true for ten seconds; then, with no other, false again. A client that
// does not say so, and the header over TCP, count for nothing.
func TestAConsoleOnTheSocketIsPresentForTenSeconds(t *testing.T) {
	w := shortWorkspace(t)
	withTimelineDB(t, w)
	var (
		mu  sync.Mutex
		now = time.Now()
	)
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		mu.Lock()
		now = now.Add(d)
		mu.Unlock()
	}
	seen := &ConsoleSeen{now: clock}
	// As `mate core run` wires it: the engine records, Present answers.
	e := socketEngine(t, w, Options{Act: newHeldActs().act, ConsoleSeen: seen, ConsolePresent: seen.Present})
	if e.ConsolePresent() {
		t.Fatal("ConsolePresent before any client")
	}
	ctx := context.Background()
	if _, err := dial(t, w).Snapshot(ctx); err != nil {
		t.Fatal(err)
	}
	if code := tcpDo(t, http.MethodGet, e.Serving().URL+"api/workspace", coreapi.ClientHeader, coreapi.ClientConsole); code != http.StatusOK {
		t.Fatalf("GET over TCP = %d", code)
	}
	if e.ConsolePresent() {
		t.Fatal("ConsolePresent after a client that is no console, and a header over TCP")
	}
	if _, err := dial(t, w, coreclient.As(coreapi.ClientConsole)).Core(ctx); err != nil {
		t.Fatal(err)
	}
	if !e.ConsolePresent() {
		t.Fatal("ConsolePresent = false right after the console's request")
	}
	advance(ConsoleWindow - time.Second)
	if !e.ConsolePresent() {
		t.Fatal("ConsolePresent = false 9 seconds after the console's request")
	}
	advance(time.Second)
	if e.ConsolePresent() {
		t.Fatal("ConsolePresent = true 10 seconds after the console's last request")
	}
}

// filler sets every field of a value to something other than its zero,
// for a round trip to prove each one comes back. It fails on what Go's
// JSON cannot bring back: an interface, an unexported field, a field it
// is told to skip.
type filler struct {
	t *testing.T
	n int
	// on are the struct types being filled further up: a type that holds
	// itself gets one level of itself, not an endless one.
	on map[reflect.Type]bool
}

var fillTime = time.Date(2026, 11, 5, 9, 30, 15, 123456789, time.FixedZone("ICT", 7*3600))

func (f *filler) fill(v reflect.Value, path string) {
	f.n++
	switch v.Kind() {
	case reflect.String:
		v.SetString(fmt.Sprintf("%s#%d", path, f.n))
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(int64(f.n))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(uint64(f.n))
	case reflect.Float32, reflect.Float64:
		v.SetFloat(float64(f.n) + 0.5)
	case reflect.Pointer:
		if f.on[v.Type().Elem()] {
			return
		}
		p := reflect.New(v.Type().Elem())
		f.fill(p.Elem(), path)
		v.Set(p)
	case reflect.Slice:
		if f.on[v.Type().Elem()] {
			return
		}
		s := reflect.MakeSlice(v.Type(), 2, 2)
		for i := range 2 {
			f.fill(s.Index(i), fmt.Sprintf("%s[%d]", path, i))
		}
		v.Set(s)
	case reflect.Array:
		for i := range v.Len() {
			f.fill(v.Index(i), fmt.Sprintf("%s[%d]", path, i))
		}
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			f.t.Fatalf("%s is a map keyed by %s: JSON keys are strings", path, v.Type().Key())
		}
		m := reflect.MakeMap(v.Type())
		for i := range 2 {
			k := reflect.New(v.Type().Key()).Elem()
			k.SetString(fmt.Sprintf("%s.key%d", path, i))
			e := reflect.New(v.Type().Elem()).Elem()
			f.fill(e, path+"."+k.String())
			m.SetMapIndex(k, e)
		}
		v.Set(m)
	case reflect.Struct:
		if v.Type() == reflect.TypeFor[time.Time]() {
			v.Set(reflect.ValueOf(fillTime.Add(time.Duration(f.n) * time.Second)))
			return
		}
		f.on[v.Type()] = true
		defer delete(f.on, v.Type())
		for i := range v.NumField() {
			sf := v.Type().Field(i)
			name := path + "." + sf.Name
			if !sf.IsExported() {
				f.t.Fatalf("%s is unexported: JSON leaves it behind", name)
			}
			if tag, _, _ := strings.Cut(sf.Tag.Get("json"), ","); tag == "-" {
				f.t.Fatalf("%s is tagged json:\"-\": JSON leaves it behind", name)
			}
			f.fill(v.Field(i), name)
		}
	default:
		f.t.Fatalf("%s is a %s: JSON cannot bring it back", path, v.Kind())
	}
}

// sameValue compares two values field by field, times by Equal (JSON
// keeps the instant, not the monotonic reading or the *Location), and
// says where they first differ.
func sameValue(got, want reflect.Value, path string) string {
	if got.Type() != want.Type() {
		return fmt.Sprintf("%s: type %s, want %s", path, got.Type(), want.Type())
	}
	if got.Type() == reflect.TypeFor[time.Time]() {
		g, w := got.Interface().(time.Time), want.Interface().(time.Time)
		if !g.Equal(w) {
			return fmt.Sprintf("%s = %v, want %v", path, g, w)
		}
		return ""
	}
	switch got.Kind() {
	case reflect.Pointer:
		if got.IsNil() || want.IsNil() {
			if got.IsNil() != want.IsNil() {
				return fmt.Sprintf("%s: nil %v, want nil %v", path, got.IsNil(), want.IsNil())
			}
			return ""
		}
		return sameValue(got.Elem(), want.Elem(), path)
	case reflect.Slice, reflect.Array:
		if got.Len() != want.Len() {
			return fmt.Sprintf("%s: %d elements, want %d", path, got.Len(), want.Len())
		}
		for i := range got.Len() {
			if d := sameValue(got.Index(i), want.Index(i), fmt.Sprintf("%s[%d]", path, i)); d != "" {
				return d
			}
		}
		return ""
	case reflect.Map:
		if got.Len() != want.Len() {
			return fmt.Sprintf("%s: %d entries, want %d", path, got.Len(), want.Len())
		}
		for _, k := range want.MapKeys() {
			g := got.MapIndex(k)
			if !g.IsValid() {
				return fmt.Sprintf("%s: no key %v", path, k)
			}
			if d := sameValue(g, want.MapIndex(k), fmt.Sprintf("%s[%v]", path, k)); d != "" {
				return d
			}
		}
		return ""
	case reflect.Struct:
		for i := range got.NumField() {
			if d := sameValue(got.Field(i), want.Field(i), path+"."+got.Type().Field(i).Name); d != "" {
				return d
			}
		}
		return ""
	}
	if !reflect.DeepEqual(got.Interface(), want.Interface()) {
		return fmt.Sprintf("%s = %#v, want %#v", path, got.Interface(), want.Interface())
	}
	return ""
}
