package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/consolecmd"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/ui/console"
)

func TestConsoleSetupHelpNeedsNoWorkspaceOrTerminal(t *testing.T) {
	t.Parallel()
	for _, flag := range []string{"--help", "-h"} {
		var out, stderr bytes.Buffer
		if err := testCLI().run([]string{"console", flag}, &out, &stderr); err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{".mate/console.yaml", "scope: crew", "command: [code", "mode: external", "{crew_dir}", "Reopen the console"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("setup help missing %q", want)
			}
		}
		if stderr.Len() != 0 {
			t.Fatalf("setup help wrote stderr: %s", &stderr)
		}
	}
}

func TestConsoleLauncherPreservesArgumentsAndReleasesBackgroundPipes(t *testing.T) {
	t.Parallel()
	bin := filepath.Join(t.TempDir(), "editor launcher")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\"\nsleep 2 &\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	args := []string{"--reuse-window", "/tmp/a b/$(echo nope).md"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := runConsoleLauncher(ctx, consolecmd.Invocation{Name: bin, Args: args, Dir: filepath.Dir(bin)}, nil, &out, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != strings.Join(args, "\n")+"\n" {
		t.Fatalf("launcher changed arguments: %q", out.String())
	}
}

// Existing host recovery tests use a configured terminal command too.
func configuredReviewView(w *store.Workspace, rec *recordingColumns) console.CommandViewFunc {
	cfg := consolecmd.Config{Bindings: []consolecmd.Binding{{
		Key: "e", Scope: "crew", Label: "files", Command: []string{"editor", "{crew_dir}"}, Mode: "terminal",
	}}}
	rec.tabs[consoleCommandRole(0)] = rec.tabs[roleReview]
	return consoleCommandView(w, rec.consoleColumns, cfg, runConsoleLauncher)
}

func TestConsoleConfiguredReportCommand(t *testing.T) {
	t.Parallel()
	w, deps := consoleFixture(t, "shop")
	spawnFakeCrew(t, w, deps, "shop", "k3")
	rec := newRecordingColumns(t)
	rec.tabs[consoleCommandRole(0)] = rec.tabs[roleReview]
	cfg := consolecmd.Config{Bindings: []consolecmd.Binding{{
		Key: "v", Scope: "crew", Label: "report", Command: []string{"editor", "--", "{report_path}"}, Mode: "terminal",
	}}}
	view := consoleCommandView(w, rec.consoleColumns, cfg, runConsoleLauncher)
	target := console.StageTarget{Kind: console.StageCrew, ID: "k3", ProjectID: "shop"}
	if err := view(context.Background(), "v", target); err == nil || len(rec.of(roleReview)) != 0 {
		t.Fatalf("missing report launched editor: %v", err)
	}
	report := w.CrewReport("shop", "k3")
	if err := os.WriteFile(report, []byte("# report"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := view(context.Background(), "v", target); err != nil {
		t.Fatal(err)
	}
	shown := rec.of(roleReview)
	if len(shown) != 1 || !slices.Equal(shown[0].Argv, []string{"/opt/editor", "--", report}) || shown[0].Dir != w.CrewDir("shop", "k3") || len(rec.of(roleStage)) != 0 {
		t.Fatalf("wrong report launch: %+v", shown)
	}
	rec.findTool = func(string) string { return "" }
	if err := view(context.Background(), "v", target); err == nil || !strings.Contains(err.Error(), "not found") || len(rec.of(roleReview)) != 1 {
		t.Fatalf("missing executable launched: %v", err)
	}
}

func TestExternalEditorWithoutHost(t *testing.T) {
	t.Parallel()
	w, deps := consoleFixture(t, "shop")
	spawnFakeCrew(t, w, deps, "shop", "k3")
	bin := filepath.Join(t.TempDir(), "gui launcher")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, app := range []string{"zed", "code"} {
		t.Run(app, func(t *testing.T) {
			cfg := consolecmd.Config{Bindings: []consolecmd.Binding{{Key: "e", Scope: "crew", Label: app, Mode: "external", Command: []string{bin, "--reuse-window", "{crew_dir}"}}}}
			calls := 0
			var failure error
			runner := func(ctx context.Context, inv consolecmd.Invocation, in io.Reader, out, stderr io.Writer) error {
				calls++
				if _, ok := ctx.Deadline(); !ok || inv.Name != bin || in != nil || out == nil || stderr == nil || inv.Dir != w.CrewDir("shop", "k3") || !slices.Equal(inv.Args, []string{"--reuse-window", inv.Dir}) {
					t.Fatalf("invalid launcher invocation: %+v", inv)
				}
				_, _ = io.WriteString(stderr, "launcher diagnostic")
				return failure
			}
			view := consoleCommandView(w, nil, cfg, runner)
			target := console.StageTarget{Kind: console.StageCrew, ID: "k3", ProjectID: "shop"}
			if err := view(context.Background(), "e", target); err != nil || calls != 1 {
				t.Fatalf("GUI needs no host: %v, calls %d", err, calls)
			}
			failure = errors.New("launcher failed")
			if err := view(context.Background(), "e", target); err == nil || !strings.Contains(err.Error(), "launcher diagnostic") {
				t.Fatalf("launcher failure hidden: %v", err)
			}
		})
	}
}

func TestConsoleCommandConfigurationAndTabs(t *testing.T) {
	t.Parallel()
	w, _ := consoleFixture(t, "shop")
	if cfg, err := loadConsoleCommands(w); err != nil || len(cfg.Bindings) != 0 {
		t.Fatalf("no config should bind no editor: %+v %v", cfg, err)
	}
	path := filepath.Join(w.StateDir(), "console.yaml")
	for _, data := range []string{
		"bindings: [{key: enter, scope: crew, label: editor, command: [editor]}]",
		"bindings: [{key: e, scope: crew, label: a, command: [editor]}, {key: e, scope: crew, label: b, command: [editor]}]",
	} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadConsoleCommands(w); err == nil {
			t.Fatalf("key conflict accepted: %s", data)
		}
	}
	data := `bindings:
  - {key: e, scope: crew, label: report, command: [editor, "{report_path}"]}
  - {key: t, scope: project, label: files, command: [code, "{project_dir}"], mode: external}
`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConsoleCommands(w)
	if err != nil {
		t.Fatal(err)
	}
	c, err := newConsoleColumns(reviewHost{}, os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.close)
	if err := c.addCommandTabs(cfg); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.tabs[consoleCommandRole(0)]; !ok {
		t.Fatal("terminal tab not planned")
	}
	if _, ok := c.tabs[consoleCommandRole(1)]; ok {
		t.Fatal("GUI command planned a terminal tab")
	}
	argv := c.tabs[consoleCommandRole(0)].col.Argv
	if !slices.Contains(argv, "--idle") || !slices.Contains(argv, consoleCommandRole(0)) {
		t.Fatalf("pane cannot serve configured role: %q", argv)
	}
	bindings := consoleCommandBindings(cfg)
	if len(bindings) != 2 || bindings[0].Key != "e" || bindings[1].Label != "files" {
		t.Fatalf("configured keys missing from read model: %+v", bindings)
	}
}

// {worktree_dir} opens the code a crew is changing, and only while that
// worktree is on disk.
func TestWorktreeDirOpensTheCrewsWorktree(t *testing.T) {
	t.Parallel()
	w, deps := consoleFixture(t, "shop")
	spawnFakeCrew(t, w, deps, "shop", "k3")
	worktree := w.WorktreeDir("shop", "k3")
	if st, err := os.Stat(worktree); err != nil || !st.IsDir() {
		t.Fatalf("setup: the fake crew has no worktree at %s: %v", worktree, err)
	}
	cfg := consolecmd.Config{Bindings: []consolecmd.Binding{
		{Key: "e", Scope: "crew", Label: "code", Mode: "external", Command: []string{"/bin/echo", "{worktree_dir}"}},
		{Key: "e", Scope: "project", Label: "code", Mode: "external", Command: []string{"/bin/echo", "{project_dir}"}},
	}}
	var got []consolecmd.Invocation
	runner := func(_ context.Context, inv consolecmd.Invocation, _ io.Reader, _, _ io.Writer) error {
		got = append(got, inv)
		return nil
	}
	view := consoleCommandView(w, nil, cfg, runner)
	if err := view(context.Background(), "e", console.StageTarget{Kind: console.StageCrew, ID: "k3", ProjectID: "shop"}); err != nil {
		t.Fatal(err)
	}
	if err := view(context.Background(), "e", console.StageTarget{ProjectID: "shop"}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !slices.Equal(got[0].Args, []string{worktree}) || !slices.Equal(got[1].Args, []string{w.ProjectHome("shop")}) {
		t.Fatalf("launches = %+v, want the worktree then the project folder", got)
	}
	if err := os.RemoveAll(worktree); err != nil {
		t.Fatal(err)
	}
	err := view(context.Background(), "e", console.StageTarget{Kind: console.StageCrew, ID: "k3", ProjectID: "shop"})
	if err == nil || !strings.Contains(err.Error(), "no worktree") || len(got) != 2 {
		t.Fatalf("a removed worktree launched or was misreported: %v, %d launches", err, len(got))
	}
}
