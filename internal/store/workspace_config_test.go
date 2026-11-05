package store_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

// TestStoreLoadConfigKeepsTheConfigOverABrokenFile: a workspace.yaml that
// does not parse, or is gone, makes LoadConfig fail and leaves the
// configuration it had loaded in place, so the console's refresh goes on with
// the projects it knew (CON-204).
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

// emptyWorkspaceFiles are empty workspace.yaml files: no key at their top.
// Each parses without an error, as a workspace with no projects on layout 1
// (CON-218).
var emptyWorkspaceFiles = []struct{ name, body string }{
	{"an empty file", ""},
	{"only whitespace", "  \n\n    \n"},
	{"only comments", "# workspace.yaml\n# nothing here yet\n"},
	{"an empty mapping", "{}\n"},
	{"a null document", "---\n"},
	{"a null value", "~\n"},
}

// keptSession is a session name no LoadConfig derives: over an empty file,
// the old LoadConfig filled in SessionName(root), which is also what Init
// stores, so only a session set by hand shows that it was kept.
const keptSession = "mate-kept0001"

// TestStoreLoadConfigRefusesAnEmptyFile: an editor that empties
// workspace.yaml before writing it, or a shell redirect, can leave the file
// empty for a moment. LoadConfig fails with ErrEmptyWorkspaceFile, naming the
// file, and leaves the configuration it had loaded in place; once the file is
// written again, the next LoadConfig reads it (CON-218). Before, the console's
// refresh swapped in a configuration with no projects.
func TestStoreLoadConfigRefusesAnEmptyFile(t *testing.T) {
	for _, tc := range emptyWorkspaceFiles {
		t.Run(tc.name, func(t *testing.T) {
			w := newProjectWorkspace(t)
			if err := w.SetSession(keptSession); err != nil {
				t.Fatal(err)
			}
			projects, layout, session := w.Projects(), w.Layout(), w.Session()
			before := w.Config()
			if err := os.WriteFile(w.WorkspaceFile(), []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			err := w.LoadConfig()
			if !errors.Is(err, store.ErrEmptyWorkspaceFile) {
				t.Errorf("LoadConfig over %s: err = %v, want ErrEmptyWorkspaceFile", tc.name, err)
			} else if !strings.Contains(err.Error(), w.WorkspaceFile()) {
				t.Errorf("error %q does not name the file, %s", err, w.WorkspaceFile())
			}
			if got := w.Projects(); !reflect.DeepEqual(got, projects) {
				t.Errorf("projects after the failed LoadConfig = %+v, want the ones loaded before, %+v", got, projects)
			}
			if got := w.Layout(); got != layout {
				t.Errorf("layout after the failed LoadConfig = %d, want the one loaded before, %d", got, layout)
			}
			if got := w.Session(); got != session {
				t.Errorf("session after the failed LoadConfig = %q, want the one loaded before, %q", got, session)
			}
			if got := w.Config(); !reflect.DeepEqual(got, before) {
				t.Errorf("config after the failed LoadConfig = %+v, want the one loaded before, %+v", got, before)
			}

			again := before
			again.Projects = append(append([]store.ProjectRef(nil), before.Projects...), store.ProjectRef{Name: "garden"})
			if err := saveAsAnotherProcess(w, again); err != nil {
				t.Fatal(err)
			}
			if err := w.LoadConfig(); err != nil {
				t.Fatalf("LoadConfig once the file is written again: %v", err)
			}
			if got := w.Projects(); !reflect.DeepEqual(got, again.Projects) {
				t.Errorf("projects once the file is written again = %+v, want %+v", got, again.Projects)
			}
		})
	}
}

// TestStoreASaveAfterAnEmptyFileKeepsTheProjects: the next change through
// the handle that refused an empty workspace.yaml writes the file from the
// configuration it kept, every project plus the change (CON-218). Before, the
// change was made to the empty configuration, and the save wrote the file
// without the projects or the layout.
func TestStoreASaveAfterAnEmptyFileKeepsTheProjects(t *testing.T) {
	w := newProjectWorkspace(t)
	if err := w.AddProject("garden", store.ProjectConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := w.SetSession(keptSession); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.WorkspaceFile(), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := w.LoadConfig(); !errors.Is(err, store.ErrEmptyWorkspaceFile) {
		t.Errorf("LoadConfig over an empty file: err = %v, want ErrEmptyWorkspaceFile", err)
	}
	if err := w.AddProject("orchard", store.ProjectConfig{}); err != nil {
		t.Fatalf("AddProject after the empty file: %v", err)
	}

	reopened, err := store.Open(w.Root())
	if err != nil {
		t.Fatalf("Open after the save: %v", err)
	}
	want := []store.ProjectRef{{Name: "shop"}, {Name: "garden"}, {Name: "orchard"}}
	if got := reopened.Projects(); !reflect.DeepEqual(got, want) {
		t.Errorf("projects on disk = %+v, want %+v", got, want)
	}
	if got := reopened.Session(); got != keptSession {
		t.Errorf("session on disk = %q, want %q", got, keptSession)
	}
}

// TestStoreOpenRefusesAnEmptyFile: a command that opens a workspace whose
// workspace.yaml is empty is told so, and the file is not read as layout 1.
// Before, Open refused it with ErrLayoutOld, which sent the captain to `mate
// migrate`, and OpenForMigrate let that migrate run over no projects
// (CON-218).
func TestStoreOpenRefusesAnEmptyFile(t *testing.T) {
	for _, tc := range emptyWorkspaceFiles {
		t.Run(tc.name, func(t *testing.T) {
			w := newProjectWorkspace(t)
			if err := os.WriteFile(w.WorkspaceFile(), []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, open := range []struct {
				name string
				fn   func(string) (*store.Workspace, error)
			}{{"Open", store.Open}, {"OpenForMigrate", store.OpenForMigrate}} {
				if _, err := open.fn(w.Root()); !errors.Is(err, store.ErrEmptyWorkspaceFile) {
					t.Errorf("%s over %s: err = %v, want ErrEmptyWorkspaceFile", open.name, tc.name, err)
				}
			}
		})
	}
}

// TestStoreAFileWithoutVersionStillLoads: an empty workspace.yaml is one with
// no key at its top, not one without `version:` (CON-218). A file that names
// neither a version nor a layout, as an old workspace's may, still opens as
// layout 1 with its projects.
func TestStoreAFileWithoutVersionStillLoads(t *testing.T) {
	w := newWorkspace(t)
	if err := os.WriteFile(w.WorkspaceFile(), []byte("session: mate-old\nprojects:\n    - name: shop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old, err := store.OpenForMigrate(w.Root())
	if err != nil {
		t.Fatalf("OpenForMigrate: %v", err)
	}
	if got, want := old.Projects(), []store.ProjectRef{{Name: "shop"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("projects = %+v, want %+v", got, want)
	}
	if got := old.Session(); got != "mate-old" {
		t.Errorf("session = %q, want mate-old", got)
	}
	if got := old.Layout(); got != 1 {
		t.Errorf("layout = %d, want 1", got)
	}
	if _, err := store.Open(w.Root()); !errors.Is(err, store.ErrLayoutOld) {
		t.Errorf("Open: err = %v, want ErrLayoutOld", err)
	}
}

// TestStoreAFailedSaveLeavesTheConfigAsItWas: a change whose workspace.yaml
// cannot be written returns the error and leaves the configuration as it
// was, because the new one is swapped in only once it is saved (CON-204).
// Before, the change stayed in memory, and the next save of anything wrote
// it to the file.
func TestStoreAFailedSaveLeavesTheConfigAsItWas(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*store.Workspace) error
	}{
		{"SetRoot", func(w *store.Workspace) error { return w.SetRoot("/elsewhere") }},
		{"SetSession", func(w *store.Workspace) error { return w.SetSession("mate-elsewhere") }},
		{"AddProject", func(w *store.Workspace) error { return w.AddProject("other", store.ProjectConfig{}) }},
		{"RemoveProject", func(w *store.Workspace) error { return w.RemoveProject("shop") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newProjectWorkspace(t)
			before := w.Config()
			// workspace.yaml becomes a link out of the workspace, which every
			// write of it refuses; the rest of `.mate/` still takes writes.
			outside := filepath.Join(t.TempDir(), "workspace.yaml")
			if err := os.WriteFile(outside, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(w.WorkspaceFile()); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, w.WorkspaceFile()); err != nil {
				t.Fatalf("symlink: %v", err)
			}
			var boundary *store.BoundaryError
			if err := tc.change(w); !errors.As(err, &boundary) {
				t.Fatalf("%s with workspace.yaml unwritable: err = %v, want *store.BoundaryError", tc.name, err)
			}
			if got := w.Config(); !reflect.DeepEqual(got, before) {
				t.Fatalf("config after the failed save = %+v, want it as it was, %+v", got, before)
			}
		})
	}
}

// TestStoreConfigSharesNothingWithTheWorkspace: Config is the caller's copy.
// Every other reader of the workspace shares one configuration, so writing
// into the slices and maps Config returned must change nothing they see
// (CON-204).
func TestStoreConfigSharesNothingWithTheWorkspace(t *testing.T) {
	w := newWorkspace(t)
	cfg := w.Config()
	cfg.Lanes = map[string]int{"xcode": 1}
	cfg.Projects = []store.ProjectRef{{Name: "a1"}}
	if err := saveAsAnotherProcess(w, cfg); err != nil {
		t.Fatal(err)
	}
	if err := w.LoadConfig(); err != nil {
		t.Fatal(err)
	}

	got := w.Config()
	got.Projects[0].Name = "changed"
	got.Lanes["xcode"] = 9
	if p := w.Projects(); len(p) != 1 || p[0].Name != "a1" {
		t.Errorf("projects after writing into Config's copy = %+v, want [{Name:a1}]", p)
	}
	if n := w.LaneCap("xcode"); n != 1 {
		t.Errorf("lane cap after writing into Config's copy = %d, want 1", n)
	}

	// Config copies exactly these; a field that holds a reference, added to
	// WorkspaceConfig later, has to be copied too (clone, workspace.go).
	if refs := referenceFields(reflect.TypeOf(store.WorkspaceConfig{}), ""); !reflect.DeepEqual(refs, []string{"Lanes", "Projects"}) {
		t.Errorf("WorkspaceConfig holds references in %v, but Config copies only [Lanes Projects]", refs)
	}
}

// referenceFields names the fields of the struct typ, at any depth, whose
// value a plain copy shares rather than duplicates.
func referenceFields(typ reflect.Type, prefix string) []string {
	var out []string
	for i := range typ.NumField() {
		f := typ.Field(i)
		name := prefix + f.Name
		switch f.Type.Kind() {
		case reflect.Struct:
			out = append(out, referenceFields(f.Type, name+".")...)
		case reflect.Slice, reflect.Map, reflect.Pointer:
			out = append(out, name)
			if f.Type.Elem().Kind() == reflect.Struct {
				out = append(out, referenceFields(f.Type.Elem(), name+"[].")...)
			}
		case reflect.Interface, reflect.Chan, reflect.Func:
			out = append(out, name)
		}
	}
	return out
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
