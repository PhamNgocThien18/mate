package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/store"
)

func pluginsFile(w *store.Workspace) string { return filepath.Join(w.StateDir(), "plugins.yaml") }

func runMate(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	return runMateAs(t, testCLI(), args...)
}

// runMateAs is runMate through c.
func runMateAs(t *testing.T, c cli, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := c.mainRun(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestPluginHelpPrintsSyntaxAndSetup(t *testing.T) {
	code, out, _ := runMate(t, "plugin", "--help")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{
		"mate plugin add <package-path> --as <instance-id> [--set key=value ...] [--key <console-key>] [--workspace <dir>]",
		"mate plugin doctor [<instance-id>] [--project <project>] [--workspace <dir>]",
		"mate plugin list [--workspace <dir>]",
		"  1. mate plugin add", "  2. mate plugin doctor", "  3. restart the Mate(s) that should receive it",
		"  4. if the package has an MCP server, run /mcp",
		"most comments stay", "never logs in",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("help lacks %q:\n%s", want, out)
		}
	}
	if code, _, _ := runMate(t, "plugin"); code != 2 {
		t.Fatalf("bare mate plugin: exit %d, want 2", code)
	}
	if code, _, _ := runMate(t, "plugin", "remove"); code != 2 {
		t.Fatalf("unknown subcommand: exit %d, want 2", code)
	}
}

func TestPluginAddIsTheCaptains(t *testing.T) {
	w := pluginFixture(t, nil)
	pkg := filepath.Join(w.Root(), "pkg")
	for _, caller := range []string{"mate", "crew"} {
		as := testCLI().withEnv("MATE_CALLER", caller)
		code, _, stderr := runMateAs(t, as, "plugin", "add", pkg, "--as", "board", "--project", "shop", "--set", "id=1", "--workspace", w.Root())
		if code != 1 || !strings.Contains(stderr, "the captain runs mate plugin add; a "+caller+" cannot") {
			t.Fatalf("MATE_CALLER=%s: exit %d, stderr %q", caller, code, stderr)
		}
		if _, err := os.Stat(pluginsFile(w)); !os.IsNotExist(err) {
			t.Fatalf("plugins.yaml written for a %s", caller)
		}
		// doctor and list stay open to it.
		if code, _, stderr := runMateAs(t, as, "plugin", "list", "--workspace", w.Root()); code != 0 {
			t.Fatalf("list as %s: exit %d %s", caller, code, stderr)
		}
		if code, _, stderr := runMateAs(t, as, "plugin", "doctor", "--workspace", w.Root()); code != 0 {
			t.Fatalf("doctor as %s: exit %d %s", caller, code, stderr)
		}
	}
}

func TestPluginAddListDoctor(t *testing.T) {
	w := pluginFixture(t, nil)
	pkg := filepath.Join(w.Root(), "pkg")

	code, out, stderr := runMate(t, "plugin", "add", "--as", "board", pkg, "--project", "shop", "--set", "id=shop-1", "--key", "b", "--workspace", w.Root())
	if code != 0 {
		t.Fatalf("add: exit %d\n%s", code, stderr)
	}
	want := "wrote " + pluginsFile(w) + ": added binding of instance board (Board) to project shop\n" +
		"next:\n" +
		"  1. restart the Mate: mate mate stop shop, then mate mate start shop\n" +
		"  2. board has no MCP server: there is nothing to log in to\n" +
		"  3. ask the Mate to use board's skills, or press its console key, to see it took\n" +
		"check it offline first: mate plugin doctor board --project shop\n"
	if out != want {
		t.Fatalf("add printed\n%s\nwant\n%s", out, want)
	}
	code, out, stderr = runMate(t, "plugin", "add", pkg, "--as", "board", "--project", "blog", "--set=id=blog-1", "--workspace", w.Root())
	if code != 0 || !strings.Contains(out, ": added binding of instance board (Board) to project blog\n") {
		t.Fatalf("second add: exit %d\n%s%s", code, out, stderr)
	}

	code, out, _ = runMate(t, "plugin", "list", "--workspace", w.Root())
	if want := "board  " + pkg + "\n  blog  id=blog-1\n  shop  key b  id=shop-1\n"; code != 0 || out != want {
		t.Fatalf("list: exit %d\n%s\nwant\n%s", code, out, want)
	}

	code, out, stderr = runMate(t, "plugin", "doctor", "board", "--project", "shop", "--workspace", w.Root())
	want = "plugins.yaml: valid: ok (1 instances; a Mate start loads it)\n" +
		"board: package " + pkg + ": ok\n" +
		"board: manifest: ok\n" +
		"board/shop: open.url: ok (https://board.example/shop-1)\n" +
		"board/shop: key b: ok\n"
	if code != 0 || out != want {
		t.Fatalf("doctor: exit %d\n%s\nwant\n%s\n%s", code, out, want, stderr)
	}
}

func TestPluginAddRefusals(t *testing.T) {
	w := pluginFixture(t, map[string]map[string]string{"board": {"blog": "z"}})
	pkg := filepath.Join(w.Root(), "pkg")
	before, err := os.ReadFile(pluginsFile(w))
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(w.Root(), "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
		code int
		want string
	}{
		{"missing --set", []string{pkg, "--as", "board", "--project", "shop"}, 1, `needs --set id=<value>; nothing written`},
		{"different path", []string{other, "--as", "board", "--project", "shop", "--set", "id=1"}, 1, string(filepath.Separator) + "pkg, not "},
		{"reserved key", []string{pkg, "--as", "board", "--project", "shop", "--set", "id=1", "--key", "q"}, 1, `key "q" is reserved by the console`},
		{"unknown project", []string{pkg, "--as", "board", "--project", "docs", "--set", "id=1"}, 1, "no such project: docs"},
		{"no --as", []string{pkg, "--project", "shop"}, 2, "--as is required"},
		{"bad --set", []string{pkg, "--as", "board", "--project", "shop", "--set", "id"}, 2, `want key=value, got "id"`},
		{"two paths", []string{pkg, pkg, "--as", "board", "--project", "shop"}, 2, "want exactly 1 argument"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, _, stderr := runMate(t, append([]string{"plugin", "add", "--workspace", w.Root()}, tc.args...)...)
			if code != tc.code || !strings.Contains(stderr, tc.want) {
				t.Fatalf("exit %d, want %d; stderr %q, want %q", code, tc.code, stderr, tc.want)
			}
			after, err := os.ReadFile(pluginsFile(w))
			if err != nil || !bytes.Equal(after, before) {
				t.Fatalf("plugins.yaml changed:\n%s", after)
			}
		})
	}
}

func TestPluginDoctorFailsAndAsksTheHarness(t *testing.T) {
	// A harness that cannot give a Mate MCP servers, found through the
	// catalog's own capability rather than by name.
	var refusing harness.Kind
	for _, k := range harnesses.Kinds() {
		if p, err := harnesses.Lookup(k); err == nil && !p.Capabilities().MCPConfig.Verified() {
			refusing = k
			break
		}
	}
	if refusing == "" {
		t.Fatal("every registered harness takes MCP servers; point this test at the unknown-harness case instead")
	}
	root := t.TempDir()
	w, err := store.Init(root, store.Defaults{MateHarness: string(refusing)})
	if err != nil {
		t.Fatal(err)
	}
	pkg := filepath.Join(root, "pkg")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "apiVersion: mate.plugin/v1\nname: Server\nmcp:\n  type: stdio\n  command: [no-such-mate-plugin-command]\n"
	if err := os.WriteFile(filepath.Join(pkg, "mate-plugin.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pluginsFile(w), []byte("apiVersion: mate.plugins/v1\ninstances:\n  srv:\n    path: "+pkg+"\n    projects: {shop: {}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, stderr := runMate(t, "plugin", "doctor", "--workspace", root)
	if code != 1 || !strings.Contains(stderr, "mate plugin doctor: 3 of 6 checks failed") {
		t.Fatalf("exit %d, stderr %q\n%s", code, stderr, out)
	}
	for _, want := range []string{
		"srv: mcp command no-such-mate-plugin-command: fail: no-such-mate-plugin-command is not on PATH\n",
		"srv/shop: project: fail: not a project of this workspace\n",
		"srv/shop: harness " + string(refusing) + ": fail: the " + string(refusing) + " harness cannot give a Mate MCP servers: ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor lacks %q:\n%s", want, out)
		}
	}
}

func TestPluginMateHarnessPrefersARunningMate(t *testing.T) {
	w, err := store.Init(t.TempDir(), store.Defaults{MateHarness: "Alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.AddProject("shop", store.ProjectConfig{}); err != nil {
		t.Fatal(err)
	}
	h := pluginMateHarness(w)
	if k, src := h("shop"); k != "alpha" || src != "workspace default" {
		t.Fatalf("no Mate: %q %q", k, src)
	}
	if err := w.WriteMateMeta("shop", map[string]string{"harness": "beta", "stopped_at": "2026-10-12T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if k, src := h("shop"); k != "alpha" || src != "workspace default" {
		t.Fatalf("stopped Mate: %q %q", k, src)
	}
	if err := w.WriteMateMeta("shop", map[string]string{"harness": "beta", "pane": "p1"}); err != nil {
		t.Fatal(err)
	}
	if k, src := h("shop"); k != "beta" || src != "running Mate" {
		t.Fatalf("running Mate: %q %q", k, src)
	}
	w2, err := store.Init(t.TempDir(), store.Defaults{})
	if err != nil {
		t.Fatal(err)
	}
	if k, _ := pluginMateHarness(w2)("shop"); k != "" {
		t.Fatalf("no default: %q, want unknown", k)
	}
}

func TestPluginWorkspaceAddListDoctor(t *testing.T) {
	w := pluginFixture(t, nil)
	pkg := filepath.Join(w.Root(), "pkg")
	code, out, stderr := runMate(t, "plugin", "add", pkg, "--as", "board", "--set", "id=workspace", "--key", "b", "--workspace", w.Root())
	if code != 0 || !strings.Contains(out, "workspace instance") {
		t.Fatalf("add: %d %s%s", code, out, stderr)
	}
	data, err := os.ReadFile(pluginsFile(w))
	if err != nil || strings.Contains(string(data), "projects:") {
		t.Fatalf("workspace config: %s, %v", data, err)
	}
	code, out, stderr = runMate(t, "plugin", "list", "--workspace", w.Root())
	if code != 0 || !strings.Contains(out, "workspace  key b  id=workspace") {
		t.Fatalf("list: %d %s%s", code, out, stderr)
	}
	code, out, stderr = runMate(t, "plugin", "doctor", "board", "--workspace", w.Root())
	if code != 0 || !strings.Contains(out, "board/shop: open.url: ok") || !strings.Contains(out, "board/blog: open.url: ok") {
		t.Fatalf("doctor: %d %s%s", code, out, stderr)
	}
}
