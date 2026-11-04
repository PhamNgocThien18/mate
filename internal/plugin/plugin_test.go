package plugin

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func packagesDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("testdata", "packages"))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// workspace writes plugins.yaml into a fresh workspace, replacing $PKG with
// the absolute testdata/packages directory.
func workspace(t *testing.T, pluginsYAML string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".mate"), 0o755); err != nil {
		t.Fatal(err)
	}
	data := strings.ReplaceAll(pluginsYAML, "$PKG", packagesDir(t))
	if err := os.WriteFile(filepath.Join(root, ".mate", "plugins.yaml"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

const fullSkill = "# Workflow\nCall mcp__tracker-work__list_issues with {\"limit\": 5}.\n"

func TestResolve(t *testing.T) {
	pkgs := packagesDir(t)
	full := `apiVersion: mate.plugins/v1
instances:
  tracker-work:
    path: $PKG/full
    projects:
      shop:
        config:
          project_url: https://tracker.example/project/shop/overview
        key: t
`
	for _, tc := range []struct {
		name    string
		yaml    string
		project string
		want    Resolved
	}{{
		name: "http with open and skill", yaml: full, project: "shop",
		want: Resolved{
			Servers: []MCPServer{{Name: "tracker-work", Type: "http", URL: "https://mcp.tracker.example/mcp"}},
			Skills:  []Skill{{Instance: "tracker-work", Name: "workflow", Content: []byte(fullSkill)}},
			Links:   []Link{{Instance: "tracker-work", Title: "Open Tracker", URL: "https://tracker.example/project/shop/overview", Key: "t"}},
		},
	}, {
		name: "project without binding gets nothing", yaml: full, project: "blog",
	}, {
		name: "disabled instance gives nothing", project: "shop",
		yaml: strings.Replace(full, "    path: $PKG/full\n", "    path: $PKG/full\n    enabled: false\n", 1),
	}, {
		name: "disabled instance is not read", project: "shop",
		yaml: "apiVersion: mate.plugins/v1\ninstances:\n  gone:\n    path: $PKG/no-such-package\n    enabled: false\n    projects:\n      shop: {}\n",
	}, {
		name: "stdio", project: "shop",
		yaml: "apiVersion: mate.plugins/v1\ninstances:\n  local:\n    path: $PKG/stdio\n    enabled: true\n    projects:\n      shop:\n",
		want: Resolved{Servers: []MCPServer{{Name: "local", Type: "stdio",
			Command: []string{"node", "server.js", "--read-only"}, Cwd: filepath.Join(pkgs, "stdio", "bin")}}},
	}, {
		name: "open only", project: "shop",
		yaml: "apiVersion: mate.plugins/v1\ninstances:\n  board:\n    path: $PKG/openonly\n    projects:\n      shop:\n        config: {team: core, project_id: \"42\", unused: x}\n",
		want: Resolved{Links: []Link{{Instance: "board", Title: "Open board", URL: "https://board.example/core/projects/42"}}},
	}, {
		name: "skills only, manifest order, instance_id substituted", project: "shop",
		yaml: "apiVersion: mate.plugins/v1\ninstances:\n  notes-a:\n    path: $PKG/skillsonly\n    projects:\n      shop: {}\n",
		want: Resolved{Skills: []Skill{
			{Instance: "notes-a", Name: "beta", Content: []byte("beta for notes-a\n")},
			{Instance: "notes-a", Name: "alpha", Content: []byte("alpha\n")},
		}},
	}, {
		name: "instance order follows the file, not the alphabet", project: "shop",
		yaml: `apiVersion: mate.plugins/v1
instances:
  zeta:
    path: $PKG/openonly
    projects:
      shop: {key: z, config: {team: a, project_id: "1"}}
  alpha:
    path: $PKG/full
    projects:
      shop: {key: a, config: {project_url: "https://tracker.example/a"}}
      blog: {key: z, config: {project_url: "https://tracker.example/b"}}
`,
		want: Resolved{
			Servers: []MCPServer{{Name: "alpha", Type: "http", URL: "https://mcp.tracker.example/mcp"}},
			Skills:  []Skill{{Instance: "alpha", Name: "workflow", Content: []byte(strings.ReplaceAll(fullSkill, "tracker-work", "alpha"))}},
			Links: []Link{
				{Instance: "zeta", Title: "Open board", URL: "https://board.example/a/projects/1", Key: "z"},
				{Instance: "alpha", Title: "Open Tracker", URL: "https://tracker.example/a", Key: "a"},
			},
		},
	}, {
		name: "duplicate key with a disabled instance is allowed", project: "shop",
		yaml: full + "  old:\n    path: $PKG/full\n    enabled: false\n    projects:\n      shop: {key: t}\n",
		want: Resolved{
			Servers: []MCPServer{{Name: "tracker-work", Type: "http", URL: "https://mcp.tracker.example/mcp"}},
			Skills:  []Skill{{Instance: "tracker-work", Name: "workflow", Content: []byte(fullSkill)}},
			Links:   []Link{{Instance: "tracker-work", Title: "Open Tracker", URL: "https://tracker.example/project/shop/overview", Key: "t"}},
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Resolve(workspace(t, tc.yaml), tc.project)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestResolveRelativePackagePath(t *testing.T) {
	// testdata/workspace/.mate/plugins.yaml names ../packages/full,
	// resolved from the workspace root.
	got, err := Resolve(filepath.Join("testdata", "workspace"), "shop")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Servers) != 1 || got.Servers[0].Name != "tracker-work" || len(got.Skills) != 1 || string(got.Skills[0].Content) != fullSkill {
		t.Fatalf("relative package not resolved: %+v", got)
	}
	cfg, err := Load(filepath.Join("testdata", "workspace"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(packagesDir(t), "full"); cfg.Instances[0].Path != want {
		t.Fatalf("package path %s, want %s", cfg.Instances[0].Path, want)
	}
}

func TestResolveWithoutPluginsYAML(t *testing.T) {
	for _, root := range []string{t.TempDir(), filepath.Join(t.TempDir(), "missing")} {
		got, err := Resolve(root, "shop")
		if err != nil || !reflect.DeepEqual(got, Resolved{}) {
			t.Fatalf("absent plugins.yaml: %+v, %v", got, err)
		}
	}
}

func TestResolveRefusesInvalidConfig(t *testing.T) {
	const plugins = "plugins.yaml" // error file is <workspace>/.mate/plugins.yaml
	bind := func(id, pkg, project, body string) string {
		return "  " + id + ":\n    path: $PKG/" + pkg + "\n    projects:\n      " + project + ":\n" + body
	}
	head := "apiVersion: mate.plugins/v1\ninstances:\n"
	fullShop := bind("work", "full", "shop", "        config: {project_url: \"https://tracker.example/shop\"}\n        key: t\n")
	for _, tc := range []struct {
		name     string
		yaml     string
		file     string // plugins.yaml, or a package name for its manifest
		instance string
		project  string
		msg      string
	}{
		{name: "unknown field in manifest", yaml: head + bind("srv", "unknownfield", "shop", ""),
			file: "unknownfield", instance: "srv", msg: "field env not found"},
		{name: "unknown field in instance", yaml: head + fullShop + "    enable: true\n",
			file: plugins, instance: "work", msg: "field enable not found"},
		{name: "unknown field in binding", yaml: head + fullShop + "        keys: [u]\n",
			file: plugins, instance: "work", project: "shop", msg: "field keys not found"},
		{name: "unknown top-level field", yaml: head + fullShop + "version: 2\n",
			file: plugins, msg: "field version not found"},
		{name: "duplicate instance id", yaml: head + fullShop + fullShop,
			file: plugins, instance: "work", msg: "duplicate instance id"},
		{name: "duplicate key in one project", yaml: head + fullShop + bind("board", "openonly", "shop", "        config: {team: a, project_id: \"1\"}\n        key: t\n"),
			file: plugins, instance: "board", project: "shop", msg: `duplicate key "t", already used by instance work`},
		{name: "skill path leaves the package", yaml: head + bind("esc", "escape", "shop", ""),
			file: "escape", instance: "esc", msg: "leaves the package"},
		{name: "missing skill file", yaml: head + bind("miss", "missingskill", "shop", ""),
			file: "missingskill", instance: "miss", msg: "does not exist"},
		{name: "mcp url with file scheme", yaml: head + bind("f", "mcpfile", "shop", ""),
			file: "mcpfile", instance: "f", msg: "must use http or https"},
		{name: "open url with javascript scheme", yaml: head + bind("js", "openjs", "shop", ""),
			file: "openjs", instance: "js", msg: "must use http or https"},
		{name: "config value substitutes a javascript URL", yaml: head + bind("work", "full", "shop", "        config: {project_url: \"javascript:alert(1)\"}\n"),
			file: plugins, instance: "work", project: "shop", msg: "must use http or https"},
		{name: "config value substitutes a file URL", yaml: head + bind("work", "full", "shop", "        config: {project_url: \"file:///etc/passwd\"}\n"),
			file: plugins, instance: "work", project: "shop", msg: "must use http or https"},
		{name: "mcp url with credentials", yaml: head + bind("mc", "mcpcreds", "shop", ""),
			file: "mcpcreds", instance: "mc", msg: "must not contain credentials"},
		{name: "static open url with credentials", yaml: head + bind("oc", "opencreds", "shop", ""),
			file: "opencreds", instance: "oc", msg: "must not contain credentials"},
		{name: "config value substitutes a URL with userinfo", yaml: head + bind("work", "full", "shop", "        config: {project_url: \"https://board.example@evil.example/x\"}\n"),
			file: plugins, instance: "work", project: "shop", msg: "must not contain credentials"},
		{name: "missing config.project_url", yaml: head + bind("work", "full", "shop", "        config: {projecturl: \"https://tracker.example\"}\n"),
			file: plugins, instance: "work", project: "shop", msg: "needs {config.project_url}"},
		{name: "unknown skill variable", yaml: head + bind("bv", "badvar", "shop", ""),
			file: "badvar", instance: "bv", msg: "unknown variable {project_url}"},
		{name: "missing package path", yaml: head + bind("gone", "no-such-package", "shop", ""),
			file: plugins, instance: "gone", msg: "is not a directory"},
		{name: "package without manifest", yaml: head + bind("bare", "stdio/bin", "shop", ""),
			file: "stdio/bin", instance: "bare", msg: "package has no mate-plugin.yaml"},
		{name: "instance id with uppercase", yaml: head + bind("Work", "full", "shop", "        config: {project_url: \"https://tracker.example\"}\n"),
			file: plugins, instance: "Work", msg: "instance id must match"},
		{name: "key without open link", yaml: head + bind("srv", "stdio", "shop", "        key: s\n"),
			file: plugins, instance: "srv", project: "shop", msg: "needs an open link"},
		{name: "wrong apiVersion", yaml: strings.Replace(head, "mate.plugins/v1", "mate.plugin/v1", 1) + fullShop,
			file: plugins, msg: "apiVersion must be mate.plugins/v1"},
		{name: "missing path", yaml: head + "  nopath:\n    projects:\n      shop: {}\n",
			file: plugins, instance: "nopath", msg: "path is required"},
		{name: "multiple documents", yaml: head + fullShop + "---\napiVersion: mate.plugins/v1\n",
			file: plugins, msg: "expected one YAML document"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := workspace(t, tc.yaml)
			_, err := Resolve(root, "shop")
			var ce *ConfigError
			if !errors.As(err, &ce) {
				t.Fatalf("want *ConfigError, got %v", err)
			}
			wantFile := filepath.Join(packagesDir(t), filepath.FromSlash(tc.file), ManifestFile)
			if tc.file == plugins {
				wantFile = filepath.Join(root, ".mate", "plugins.yaml")
			}
			if ce.File != wantFile || ce.Instance != tc.instance || ce.Project != tc.project {
				t.Fatalf("located at file=%s instance=%q project=%q, want %s %q %q (%v)", ce.File, ce.Instance, ce.Project, wantFile, tc.instance, tc.project, err)
			}
			for _, want := range []string{wantFile, tc.instance, tc.project, tc.msg} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q lacks %q", err, want)
				}
			}
			if _, err := Load(root); err == nil || err.Error() != ce.Error() {
				t.Fatalf("Load and Resolve disagree: %v", err)
			}
		})
	}
}

func TestResolveStdioCwdDefaultsToPackageRoot(t *testing.T) {
	pkg := pkgDir(t, "apiVersion: mate.plugin/v1\nname: Serve\nmcp:\n  type: stdio\n  command: [./bin/serve]\n",
		map[string]string{"bin/serve": "#!/bin/sh\n"})
	root := workspace(t, "apiVersion: mate.plugins/v1\ninstances:\n  serve:\n    path: "+pkg+"\n    projects: {shop: {}}\n")
	got, err := Resolve(root, "shop")
	if err != nil {
		t.Fatal(err)
	}
	want := []MCPServer{{Name: "serve", Type: "stdio", Command: []string{"./bin/serve"}, Cwd: pkg}}
	if !reflect.DeepEqual(got.Servers, want) {
		t.Fatalf("servers %+v, want %+v", got.Servers, want)
	}
	if !filepath.IsAbs(got.Servers[0].Cwd) {
		t.Fatalf("cwd %q is not absolute", got.Servers[0].Cwd)
	}
}

// One project's broken binding refuses that project and Load, and leaves
// every other project's plugins as they are.
func TestResolveIgnoresAnotherProjectsBinding(t *testing.T) {
	head := "apiVersion: mate.plugins/v1\ninstances:\n"
	shopWork := "        config: {project_url: \"https://tracker.example/shop\"}\n        key: t\n"
	work := func(blog string) string {
		return "  work:\n    path: $PKG/full\n    projects:\n      shop:\n" + shopWork + "      blog:\n" + blog
	}
	okBlog := work("        config: {project_url: \"https://tracker.example/blog\"}\n")
	want := Resolved{
		Servers: []MCPServer{{Name: "work", Type: "http", URL: "https://mcp.tracker.example/mcp"}},
		Skills:  []Skill{{Instance: "work", Name: "workflow", Content: []byte(strings.ReplaceAll(fullSkill, "tracker-work", "work"))}},
		Links:   []Link{{Instance: "work", Title: "Open Tracker", URL: "https://tracker.example/shop", Key: "t"}},
	}
	for _, tc := range []struct {
		name, yaml, msg string
	}{
		{name: "blog config substitutes a javascript URL",
			yaml: head + work("        config: {project_url: \"javascript:alert(1)\"}\n"), msg: "must use http or https"},
		{name: "blog binding misses its config",
			yaml: head + work("        config: {}\n"), msg: "needs {config.project_url}"},
		{name: "unknown field in the blog binding",
			yaml: head + work("        config: {project_url: \"https://tracker.example/blog\"}\n        keys: [u]\n"), msg: "field keys not found"},
		{name: "blog binding value of the wrong type",
			yaml: head + work("        config: [x]\n"), msg: "cannot unmarshal"},
		{name: "blog key without open link",
			yaml: head + okBlog +
				"  srv:\n    path: $PKG/stdio\n    projects:\n      blog: {key: s}\n", msg: "needs an open link"},
		{name: "duplicate key in blog",
			yaml: head + work("        config: {project_url: \"https://tracker.example/blog\"}\n        key: b\n") +
				"  board:\n    path: $PKG/openonly\n    projects:\n      blog: {config: {team: a, project_id: \"1\"}, key: b}\n", msg: `duplicate key "b"`},
		{name: "instance bound only to blog has no package",
			yaml: head + okBlog + "  gone:\n    path: $PKG/no-such-package\n    projects:\n      blog: {}\n", msg: "is not a directory"},
		{name: "instance bound only to blog has a broken manifest",
			yaml: head + okBlog + "  srv:\n    path: $PKG/unknownfield\n    projects:\n      blog: {}\n", msg: "field env not found"},
		{name: "unknown field in an instance bound only to blog",
			yaml: head + okBlog + "  board:\n    path: $PKG/openonly\n    enable: true\n    projects:\n      blog: {config: {team: a, project_id: \"1\"}}\n", msg: "field enable not found"},
		{name: "invalid id of an instance bound only to blog",
			yaml: head + okBlog + "  Board:\n    path: $PKG/openonly\n    projects:\n      blog: {config: {team: a, project_id: \"1\"}}\n", msg: "instance id must match"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := workspace(t, tc.yaml)
			got, err := Resolve(root, "shop")
			if err != nil {
				t.Fatalf("shop refused for blog's problem: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("shop got  %+v\nwant %+v", got, want)
			}
			if _, err := Resolve(root, "blog"); err == nil || !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("blog: %v, want %q", err, tc.msg)
			}
			if _, err := Load(root); err == nil || !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("Load: %v, want %q", err, tc.msg)
			}
		})
	}
}

func TestSkillSymlinkCannotLeavePackage(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "secret.md")
	if err := os.WriteFile(outside, []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, link := range map[string]func(pkg string) error{
		"file symlink": func(pkg string) error {
			if err := os.MkdirAll(filepath.Join(pkg, "skills", "evil"), 0o755); err != nil {
				return err
			}
			return os.Symlink(outside, filepath.Join(pkg, "skills", "evil", "SKILL.md"))
		},
		"directory symlink": func(pkg string) error {
			if err := os.MkdirAll(filepath.Join(pkg, "skills"), 0o755); err != nil {
				return err
			}
			return os.Symlink(filepath.Dir(outside), filepath.Join(pkg, "skills", "evil"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			pkg := t.TempDir()
			manifest := "apiVersion: mate.plugin/v1\nname: Evil\nskills:\n  - skills/evil/" + map[bool]string{true: "SKILL.md", false: "secret.md"}[name == "file symlink"] + "\n"
			if err := os.WriteFile(filepath.Join(pkg, ManifestFile), []byte(manifest), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := link(pkg); err != nil {
				t.Fatal(err)
			}
			root := workspace(t, "apiVersion: mate.plugins/v1\ninstances:\n  evil:\n    path: "+pkg+"\n    projects:\n      shop: {}\n")
			_, err := Resolve(root, "shop")
			if err == nil || !strings.Contains(err.Error(), "resolves outside the package") ||
				!strings.Contains(err.Error(), filepath.Join(pkg, ManifestFile)) || !strings.Contains(err.Error(), "instance evil") {
				t.Fatalf("symlink escape accepted or unlocated: %v", err)
			}
		})
	}
}

func TestSymlinkInsidePackageIsAllowed(t *testing.T) {
	pkg := t.TempDir()
	if err := os.MkdirAll(filepath.Join(pkg, "shared"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, "shared", "SKILL.md"), []byte("shared\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(pkg, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(pkg, "shared"), filepath.Join(pkg, "skills", "ops")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkg, ManifestFile), []byte("apiVersion: mate.plugin/v1\nname: Ops\nskills: [skills/ops/SKILL.md]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve(workspace(t, "apiVersion: mate.plugins/v1\ninstances:\n  ops:\n    path: "+pkg+"\n    projects:\n      shop: {}\n"), "shop")
	if err != nil || len(got.Skills) != 1 || got.Skills[0].Name != "ops" || string(got.Skills[0].Content) != "shared\n" {
		t.Fatalf("in-package symlink: %+v, %v", got, err)
	}
}

func TestTemplates(t *testing.T) {
	values := map[string]string{"project_url": "https://x.example/a?b=c", "team": "{config.team}"}
	for tmpl, want := range map[string]string{
		"{config.project_url}":            "https://x.example/a?b=c",
		"https://x.example/{config.team}": "https://x.example/{config.team}", // inserted literally, never expanded again
		"https://x.example/{team}":        "",                                // only config.<key>
		"https://x.example/{config.team":  "",                                // unclosed
		"https://x.example/}":             "",                                // stray brace
		"https://x.example/{config.a+b}":  "",                                // no expressions
		"https://x.example/ a":            "",                                // whitespace
		"https:///path":                   "",                                // no host
		"HTTPS://X.example/":              "HTTPS://X.example/",
		"ftp://x.example/":                "",
	} {
		got, err := expandURL(tmpl, values)
		if want == "" {
			if err == nil {
				t.Errorf("%q accepted as %q", tmpl, got)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("%q = %q, %v; want %q", tmpl, got, err, want)
		}
	}
	got, err := expandSkill([]byte("{instance_id} {\"k\": {}} ${HOME} {instance_id}"), "work")
	if err != nil || string(got) != "work {\"k\": {}} ${HOME} work" {
		t.Fatalf("skill substitution: %q, %v", got, err)
	}
	if _, err := expandSkill([]byte("{instance}"), "work"); err == nil {
		t.Fatal("unknown skill variable accepted")
	}
}

// TestExamplePackages keeps the shipped example packages valid for the resolver.
func TestExamplePackages(t *testing.T) {
	pkg, err := filepath.Abs(filepath.Join("..", "..", "examples", "plugins", "linear"))
	if err != nil {
		t.Fatal(err)
	}
	root := workspace(t, `apiVersion: mate.plugins/v1
instances:
  linear-work:
    path: `+pkg+`
    projects:
      shop:
        config:
          project_url: https://linear.app/acme/project/shop-1234/overview
        key: t
`)
	got, err := Resolve(root, "shop")
	if err != nil {
		t.Fatal(err)
	}
	wantServers := []MCPServer{{Name: "linear-work", Type: "http", URL: "https://mcp.linear.app/mcp"}}
	if !reflect.DeepEqual(got.Servers, wantServers) {
		t.Errorf("servers = %+v, want %+v", got.Servers, wantServers)
	}
	if len(got.Links) != 1 || got.Links[0].URL != "https://linear.app/acme/project/shop-1234/overview" || got.Links[0].Key != "t" {
		t.Errorf("links = %+v", got.Links)
	}
	if len(got.Skills) != 1 || got.Skills[0].Name != "workflow" {
		t.Fatalf("skills = %+v", got.Skills)
	}
	content := string(got.Skills[0].Content)
	if !strings.HasPrefix(content, "---\nname: linear-work-workflow\n") {
		t.Errorf("skill frontmatter = %q", content[:min(len(content), 60)])
	}
	if strings.Contains(content, "{instance_id}") {
		t.Error("skill still has {instance_id}")
	}
}

// An instance may carry a `tracker:` block (CON-77): the one hook mate has
// into a task tracker. Its optional `read` is an argv; an empty command
// name is refused. TrackerFor is the first enabled instance bound to the
// project that has the block.
func TestTrackerBlock(t *testing.T) {
	root := workspace(t, `apiVersion: mate.plugins/v1
instances:
  notes:
    path: $PKG/skillsonly
  tracker-work:
    path: $PKG/full
    config: {project_url: "https://tracker.example/workspace"}
    tracker:
      read: [linear-axi, issue, view, "{id}"]
  legacy:
    path: $PKG/full
    projects:
      other:
        config: {project_url: "https://tracker.example/other"}
    tracker: {}
`)
	for _, read := range []func(string) (Config, error){ReadConfig, Load} {
		cfg, err := read(root)
		if err != nil {
			t.Fatal(err)
		}
		in, ok := cfg.TrackerFor("shop")
		if !ok || in.ID != "tracker-work" || strings.Join(in.Tracker.Read, " ") != "linear-axi issue view {id}" {
			t.Fatalf("TrackerFor(shop) = %+v %v", in, ok)
		}
		if in, ok := cfg.TrackerFor("other"); !ok || in.ID != "tracker-work" {
			t.Fatalf("TrackerFor(other) = %s %v, want the first instance with the block", in.ID, ok)
		}
		if cfg.Instances[0].Tracker != nil {
			t.Fatal("an instance without the block has a tracker")
		}
		if legacy := cfg.Instances[2]; legacy.Tracker == nil || legacy.Tracker.Read != nil {
			t.Fatalf("an empty tracker block = %+v, want a tracker with no read command", legacy.Tracker)
		}
	}

	disabled := workspace(t, "apiVersion: mate.plugins/v1\ninstances:\n  t:\n    path: $PKG/full\n    enabled: false\n    tracker: {}\n")
	if cfg, err := ReadConfig(disabled); err != nil {
		t.Fatal(err)
	} else if _, ok := cfg.TrackerFor("shop"); ok {
		t.Fatal("a disabled instance is a tracker")
	}

	_, err := ReadConfig(workspace(t, "apiVersion: mate.plugins/v1\ninstances:\n  t:\n    path: $PKG/full\n    tracker:\n      read: [\"\"]\n"))
	if err == nil || !strings.Contains(err.Error(), "tracker.read") {
		t.Fatalf("an empty read command was accepted: %v", err)
	}
	_, err = ReadConfig(workspace(t, "apiVersion: mate.plugins/v1\ninstances:\n  t:\n    path: $PKG/full\n    tracker:\n      land: [\"\"]\n"))
	if err == nil || !strings.Contains(err.Error(), "tracker.land") {
		t.Fatalf("an empty land command was accepted: %v", err)
	}
	_, err = ReadConfig(workspace(t, "apiVersion: mate.plugins/v1\ninstances:\n  t:\n    path: $PKG/full\n    tracker:\n      raed: [x]\n"))
	if err == nil || !strings.Contains(err.Error(), "raed") {
		t.Fatalf("a misspelt tracker field was accepted: %v", err)
	}
}

// Doctor finds the tracker's read command the way it finds a stdio
// server's, and says when there is none to find.
func TestDoctorChecksTheTrackerReadCommand(t *testing.T) {
	root := workspace(t, `apiVersion: mate.plugins/v1
instances:
  present:
    path: $PKG/skillsonly
    tracker:
      read: [sh, -c, "echo {id}"]
      land: [sh, -c, "echo landed {id}"]
  absent:
    path: $PKG/skillsonly
    tracker:
      read: [./no-such-reader]
      land: [./no-such-lander]
  silent:
    path: $PKG/skillsonly
    tracker: {}
`)
	var got []string
	for _, c := range Doctor(root, DoctorOptions{}) {
		if strings.Contains(c.Name, "tracker") {
			got = append(got, c.String())
		}
	}
	want := []string{
		"present: tracker read sh: ok (",
		"present: tracker land sh: ok (",
		"absent: tracker read ./no-such-reader: fail: ",
		"absent: tracker land ./no-such-lander: fail: ",
		"silent: tracker: ok (no read command; recall cannot cross-check In flight)",
		"silent: tracker: ok (no land command; mate land leaves the tracker to the Mate)",
	}
	if len(got) != len(want) {
		t.Fatalf("tracker checks = %q, want %d", got, len(want))
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Errorf("check %d = %q, want prefix %q", i, got[i], want[i])
		}
	}
}
