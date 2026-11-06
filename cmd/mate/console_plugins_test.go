package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/consolecmd"
	"github.com/nguyenngocanh94/mate/internal/platform"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

// pluginFixture is a workspace with projects shop and blog and one
// package with an open link. bindings maps project to the key it binds.
func pluginFixture(t *testing.T, instances map[string]map[string]string) *store.Workspace {
	t.Helper()
	root := t.TempDir()
	w, err := store.Init(root, workspaceDefaults())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"shop", "blog"} {
		repo := filepath.Join(w.ProjectHome(p), p)
		if err := os.MkdirAll(repo, 0o755); err != nil {
			t.Fatal(err)
		}
		initGitRepo(t, repo)
		if err := w.AddProject(p, store.ProjectConfig{Repos: []store.RepoConfig{{Path: repo, DefaultBranch: "main"}}}); err != nil {
			t.Fatal(err)
		}
	}
	pkg := filepath.Join(root, "pkg")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "apiVersion: mate.plugin/v1\nname: Board\nopen:\n  title: Open board\n  url: \"https://board.example/{config.id}\"\n"
	if err := os.WriteFile(filepath.Join(pkg, "mate-plugin.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if instances != nil {
		var b strings.Builder
		b.WriteString("apiVersion: mate.plugins/v1\ninstances:\n")
		for inst, projects := range instances {
			b.WriteString("  " + inst + ":\n    path: " + pkg + "\n    projects:\n")
			for p, key := range projects {
				b.WriteString("      " + p + ":\n        config: {id: " + p + "-1}\n        key: " + key + "\n")
			}
		}
		if err := os.WriteFile(filepath.Join(w.StateDir(), "plugins.yaml"), []byte(b.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return w
}

func TestPluginLinksBindAndOpen(t *testing.T) {
	t.Parallel()
	w := pluginFixture(t, map[string]map[string]string{"board": {"shop": "b"}})
	links, _, err := loadPluginLinks(w, consolecmd.Config{})
	if err != nil {
		t.Fatal(err)
	}
	b := links.bindings()
	if len(b) != 1 || b[0].Key != "b" || b[0].Label != "Open board" || b[0].Scope != "project" || b[0].Role != "plugin-board" {
		t.Fatalf("bindings %+v", b)
	}
	var got consolecmd.Invocation
	runner := func(_ context.Context, inv consolecmd.Invocation, _ io.Reader, _, _ io.Writer) error {
		got = inv
		return nil
	}
	opener := filepath.Join(t.TempDir(), "opener")
	view := consolePluginView(links, urlOpener{name: opener}, runner, nil)
	if err := view(context.Background(), "b", console.StageTarget{ProjectID: "shop"}); err != nil {
		t.Fatal(err)
	}
	if got.Name != opener || len(got.Args) != 1 || got.Args[0] != "https://board.example/shop-1" {
		t.Fatalf("invocation %+v", got)
	}

	// An opener's own arguments, Windows' url.dll,FileProtocolHandler, go
	// before the URL, the same on every open.
	prefixed := consolePluginView(links, urlOpener{name: opener, args: []string{"url.dll,FileProtocolHandler"}}, runner, nil)
	for range 2 {
		got = consolecmd.Invocation{}
		if err := prefixed(context.Background(), "b", console.StageTarget{ProjectID: "shop"}); err != nil {
			t.Fatal(err)
		}
		if got.Name != opener || !slices.Equal(got.Args, []string{"url.dll,FileProtocolHandler", "https://board.example/shop-1"}) {
			t.Fatalf("invocation with the opener's arguments %+v", got)
		}
	}

	got = consolecmd.Invocation{}
	err = view(context.Background(), "b", console.StageTarget{ProjectID: "blog"})
	if err == nil || err.Error() != "no app bound to b for project blog" || got.Name != "" {
		t.Fatalf("unbound project: %v, %+v", err, got)
	}

	failing := func(context.Context, consolecmd.Invocation, io.Reader, io.Writer, io.Writer) error {
		return errors.New("boom")
	}
	if err := consolePluginView(links, urlOpener{name: opener}, failing, nil)(context.Background(), "b", console.StageTarget{ProjectID: "shop"}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("runner error lost: %v", err)
	}
	if err := consolePluginView(links, urlOpener{name: "no-such-opener-xyz"}, runner, nil)(context.Background(), "b", console.StageTarget{ProjectID: "shop"}); err == nil || err.Error() != `Open board: URL opener "no-such-opener-xyz" not found` {
		t.Fatalf("missing opener not named: %v", err)
	}
}

func TestPluginLinksSharedKeyOneLinePerInstance(t *testing.T) {
	t.Parallel()
	w := pluginFixture(t, map[string]map[string]string{"board": {"shop": "b", "blog": "b"}})
	links, _, err := loadPluginLinks(w, consolecmd.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if b := links.bindings(); len(b) != 1 {
		t.Fatalf("same key, same instance, two projects: %+v", b)
	}
	w = pluginFixture(t, map[string]map[string]string{"board": {"shop": "b"}, "wiki": {"blog": "b"}})
	links, _, err = loadPluginLinks(w, consolecmd.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if b := links.bindings(); len(b) != 2 {
		t.Fatalf("same key, two instances: %+v", b)
	}
}

func TestPluginLinkCollisions(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, key string
		cfg       consolecmd.Config
		want      string
	}{
		{"console.yaml", "t", consolecmd.Config{Bindings: []consolecmd.Binding{{Key: "t", Scope: "project", Label: "term", Command: []string{"x"}, Mode: "external"}}}, "already bound"},
		{"reserved", "q", consolecmd.Config{}, "reserved"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := pluginFixture(t, map[string]map[string]string{"board": {"shop": tc.key}})
			_, _, err := loadPluginLinks(w, tc.cfg)
			if err == nil {
				t.Fatal("collision accepted")
			}
			for _, s := range []string{"plugins.yaml", `"board"`, `"` + tc.key + `"`, tc.want} {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("error %q lacks %q", err, s)
				}
			}
		})
	}
	// A crew-scope console.yaml binding of the same key does not collide.
	w := pluginFixture(t, map[string]map[string]string{"board": {"shop": "t"}})
	cfg := consolecmd.Config{Bindings: []consolecmd.Binding{{Key: "t", Scope: "crew", Label: "c", Command: []string{"x"}, Mode: "external"}}}
	if _, _, err := loadPluginLinks(w, cfg); err != nil {
		t.Fatal(err)
	}
}

func TestNoPluginsYAMLChangesNothing(t *testing.T) {
	t.Parallel()
	w := pluginFixture(t, nil)
	links, _, err := loadPluginLinks(w, consolecmd.Config{})
	if err != nil || len(links) != 0 || len(links.bindings()) != 0 {
		t.Fatalf("links %v, %v", links, err)
	}
	if consolePluginView(links, urlOpener{name: "open"}, nil, nil) != nil {
		t.Fatal("view installed without plugins")
	}
}

// A plugin configuration error does not keep the console from starting: it
// runs without any plugin link, and says why in one status-line notice that
// names the file.
func TestPluginLinkConfigErrorDegradesToNotice(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, yaml, want string }{
		{"unparsable file", "bogus: [\n", "yaml"},
		// An instance bound only to blog has no package; shop's board
		// link resolves, and is dropped with it.
		{"one project's binding", "", "is not a directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := pluginFixture(t, map[string]map[string]string{"board": {"shop": "b"}})
			file := filepath.Join(w.StateDir(), "plugins.yaml")
			text := tc.yaml
			if text == "" {
				data, err := os.ReadFile(file)
				if err != nil {
					t.Fatal(err)
				}
				text = string(data) + "  gone:\n    path: " + filepath.Join(w.Root(), "no-such-package") + "\n    projects:\n      blog: {}\n"
			}
			if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
			links, notice, err := loadPluginLinks(w, consolecmd.Config{})
			if err != nil {
				t.Fatalf("console refused to start: %v", err)
			}
			if len(links) != 0 || len(links.bindings()) != 0 {
				t.Fatalf("plugin links kept: %v", links)
			}
			if !strings.HasPrefix(notice, "plugin links off: ") || !strings.Contains(notice, file) || !strings.Contains(notice, tc.want) || strings.Contains(notice, "\n") {
				t.Fatalf("notice %q: want one line naming %s and %q", notice, file, tc.want)
			}
		})
	}
}

// The console's plugin links and `mate dashboard --open` open a URL with
// the program internal/platform names for this system (platform.URLOpenerFor
// has the table), and each says when it is missing.
// Not parallel: it sets PATH in the process environment.
func TestURLOpenersAreThePlatforms(t *testing.T) {
	name, args := platform.URLOpenerFor(runtime.GOOS)
	if got := hostOpener(); got.name != name || !slices.Equal(got.args, args) {
		t.Fatalf("console opener %+v, want %q %q", got, name, args)
	}
	t.Setenv("PATH", t.TempDir())
	if err := openInBrowser("http://127.0.0.1:7777/"); err == nil || err.Error() != "no "+name+" on PATH" {
		t.Fatalf("dashboard opener without %s on PATH: %v", name, err)
	}
}
