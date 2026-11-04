package store_test

import (
	"fmt"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/nguyenngocanh94/mate/internal/store"
)

// TestStoreReadersSeeAWholeConfigWhileItReloads: the console shares one
// *store.Workspace between its refresh, which re-reads workspace.yaml every
// two seconds, and the Office and City handlers, the actions and recovery,
// which read it (CON-204). However the reloads fall, each reading is of one
// whole configuration, the file before a save or the file after it, never
// parts of both. Under -race, a config swapped in unguarded is reported here
// as a DATA RACE.
func TestStoreReadersSeeAWholeConfigWhileItReloads(t *testing.T) {
	w := newWorkspace(t)
	a := store.WorkspaceConfig{
		Version: 1, Layout: 2, Session: "mate-aaaaaaaa", Root: "/ws/a",
		Defaults: store.Defaults{MateHarness: "harness-a", CrewHarness: "harness-a"},
		Crew:     store.CrewLaunch{Autocompact: 110000, ContextHigh: 120000},
		Lanes:    map[string]int{"xcode": 1},
		Projects: []store.ProjectRef{{Name: "a1"}, {Name: "a2"}},
	}
	b := store.WorkspaceConfig{
		Version: 1, Layout: 2, Session: "mate-bbbbbbbb", Root: "/ws/b",
		Defaults: store.Defaults{MateHarness: "harness-b", CrewHarness: "harness-b"},
		Crew:     store.CrewLaunch{Autocompact: 210000, ContextHigh: 220000},
		Lanes:    map[string]int{"xcode": 3},
		Projects: []store.ProjectRef{{Name: "b1"}},
	}
	if err := saveAsAnotherProcess(w, a); err != nil {
		t.Fatal(err)
	}
	if err := w.LoadConfig(); err != nil {
		t.Fatal(err)
	}

	var saved atomic.Bool
	var wg sync.WaitGroup
	// Another process saving workspace.yaml, one whole file after another.
	wg.Go(func() {
		defer saved.Store(true)
		for i := range 200 {
			cfg := a
			if i%2 == 0 {
				cfg = b
			}
			if err := saveAsAnotherProcess(w, cfg); err != nil {
				t.Error(err)
				return
			}
		}
	})
	for range 2 {
		wg.Go(func() {
			for !saved.Load() {
				if err := w.LoadConfig(); err != nil {
					t.Errorf("LoadConfig: %v", err)
					return
				}
			}
		})
	}
	for range 4 {
		wg.Go(func() {
			for !saved.Load() {
				if torn := tornReading(w, a, b); torn != "" {
					t.Error(torn)
					return
				}
			}
		})
	}
	wg.Wait()
}

// projectLookup is what Workspace.Project answers.
type projectLookup struct {
	Ref   store.ProjectRef
	Found bool
}

// tornReading is the first reading of w that is neither all of a nor all of
// b, or "" when every reading is one of the two.
func tornReading(w *store.Workspace, a, b store.WorkspaceConfig) string {
	ref, found := w.Project("a1")
	readings := []struct {
		name      string
		got, a, b any
	}{
		{"Config()", w.Config(), a, b},
		{"Session()", w.Session(), a.Session, b.Session},
		{"RecordedRoot()", w.RecordedRoot(), a.Root, b.Root},
		{"Defaults()", w.Defaults(), a.Defaults, b.Defaults},
		{"CrewAutocompact()", w.CrewAutocompact(), a.Crew.Autocompact, b.Crew.Autocompact},
		{"CrewContextHigh()", w.CrewContextHigh(), a.Crew.ContextHigh, b.Crew.ContextHigh},
		{`LaneCap("xcode")`, w.LaneCap("xcode"), a.Lanes["xcode"], b.Lanes["xcode"]},
		{"Projects()", w.Projects(), a.Projects, b.Projects},
		{`Project("a1")`, projectLookup{ref, found}, projectLookup{store.ProjectRef{Name: "a1"}, true}, projectLookup{}},
		{"Layout()", w.Layout(), a.Layout, b.Layout},
		{"LayoutOld()", w.LayoutOld(), false, false},
	}
	for _, r := range readings {
		if !reflect.DeepEqual(r.got, r.a) && !reflect.DeepEqual(r.got, r.b) {
			return fmt.Sprintf("%s = %+v, which is neither %+v nor %+v", r.name, r.got, r.a, r.b)
		}
	}
	return ""
}

// TestStoreChangesSurviveAReloadBesideThem: each change of workspace.yaml
// through a *store.Workspace reads the config, changes it and writes it back.
// A LoadConfig of the same handle on another goroutine, the console's
// refresh, must not land in between and undo the change: not in memory, and
// not in the file the next change writes (CON-204).
func TestStoreChangesSurviveAReloadBesideThem(t *testing.T) {
	fresh := newWorkspace(t)
	// Back on the old layout, so SetLayoutProjectDirs has a change to make.
	old := fresh.Config()
	old.Layout = 0
	if err := saveAsAnotherProcess(fresh, old); err != nil {
		t.Fatal(err)
	}
	w, err := store.OpenForMigrate(fresh.Root())
	if err != nil {
		t.Fatal(err)
	}
	stop := reloading(t, w)

	if err := w.SetLayoutProjectDirs(); err != nil {
		t.Fatalf("SetLayoutProjectDirs: %v", err)
	}
	for i := range 8 {
		name := fmt.Sprintf("p%d", i)
		if err := w.AddProject(name, store.ProjectConfig{}); err != nil {
			t.Fatalf("AddProject(%s): %v", name, err)
		}
	}
	var want []store.ProjectRef
	for i := range 8 {
		name := fmt.Sprintf("p%d", i)
		if i%2 == 0 {
			if err := w.RemoveProject(name); err != nil {
				t.Fatalf("RemoveProject(%s): %v", name, err)
			}
		} else {
			want = append(want, store.ProjectRef{Name: name})
		}
		if err := w.SetSession(fmt.Sprintf("mate-%08d", i)); err != nil {
			t.Fatalf("SetSession: %v", err)
		}
		if err := w.SetRoot(fmt.Sprintf("/moved/%d", i)); err != nil {
			t.Fatalf("SetRoot: %v", err)
		}
	}
	if err := w.SaveConfig(); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	stop()

	reopened, err := store.Open(w.Root())
	if err != nil {
		t.Fatalf("Open after the changes: %v", err)
	}
	for _, h := range []struct {
		where string
		w     *store.Workspace
	}{{"in memory", w}, {"on disk", reopened}} {
		if got := h.w.Projects(); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: projects = %+v, want %+v", h.where, got, want)
		}
		if got := h.w.Session(); got != "mate-00000007" {
			t.Errorf("%s: session = %q, want the last one set, mate-00000007", h.where, got)
		}
		if got := h.w.RecordedRoot(); got != "/moved/7" {
			t.Errorf("%s: root = %q, want the last one set, /moved/7", h.where, got)
		}
		if got := h.w.Layout(); got != 2 {
			t.Errorf("%s: layout = %d, want 2", h.where, got)
		}
	}
}

// TestStoreLoadConfigKeepsTheConfigOverABrokenFile: a workspace.yaml that no
// longer reads, the captain halfway through an edit or the file gone, makes
// LoadConfig fail and leaves the configuration it had loaded in place, so
// the console's refresh goes on with the projects it knew (CON-204).
func TestStoreLoadConfigKeepsTheConfigOverABrokenFile(t *testing.T) {
	for _, tc := range []struct {
		name   string
		damage func(path string) error
	}{
		{"a syntax error", func(path string) error {
			return os.WriteFile(path, []byte("version: 1\nprojects: [\n"), 0o644)
		}},
		{"a field of the wrong type", func(path string) error {
			return os.WriteFile(path, []byte("version: 1\nsession: mate-elsewhere\nprojects: shop\n"), 0o644)
		}},
		{"the file gone", os.Remove},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newProjectWorkspace(t)
			before := w.Config()
			if err := tc.damage(w.WorkspaceFile()); err != nil {
				t.Fatal(err)
			}
			if err := w.LoadConfig(); err == nil {
				t.Fatal("LoadConfig over a broken workspace.yaml succeeded, want an error")
			}
			if got := w.Config(); !reflect.DeepEqual(got, before) {
				t.Fatalf("config after the failed LoadConfig = %+v, want the one loaded before, %+v", got, before)
			}
		})
	}
}

// saveAsAnotherProcess writes cfg over w's workspace.yaml the way any process
// saving it does: whole, through a rename.
func saveAsAnotherProcess(w *store.Workspace, cfg store.WorkspaceConfig) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	tmp := w.WorkspaceFile() + ".another"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, w.WorkspaceFile())
}

// reloading runs LoadConfig on w in a loop, as the console's refresh does,
// until stop is called; stop waits for the loop to end, and runs at cleanup
// if the test never calls it.
func reloading(t *testing.T, w *store.Workspace) (stop func()) {
	t.Helper()
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			if err := w.LoadConfig(); err != nil {
				t.Errorf("LoadConfig: %v", err)
				return
			}
		}
	})
	var once sync.Once
	stop = func() { once.Do(func() { close(done); wg.Wait() }) }
	t.Cleanup(stop)
	return stop
}
