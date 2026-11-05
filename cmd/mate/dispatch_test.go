package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// stubDispatcher is a dispatcher over a copy of the real command table
// whose handlers record the row they belong to and the arguments they got,
// and whose console records the directory it was asked to open. Nothing in
// it touches a workspace, a terminal or the process's environment.
type stubDispatcher struct {
	dispatcher
	gotRow     int
	gotArgs    []string
	consoleDir string
	consoles   int
}

func newStubDispatcher() *stubDispatcher {
	s := &stubDispatcher{gotRow: -1}
	cmds := commands()
	for i := range cmds {
		row := i
		cmds[i].run = func(args []string, _, _ io.Writer) error {
			s.gotRow, s.gotArgs = row, args
			return nil
		}
	}
	s.dispatcher = dispatcher{
		commands:      cmds,
		findWorkspace: func() (string, error) { return "", errors.New("no workspace") },
		isDir:         func(string) bool { return false },
		console: func(dir string, _, _ io.Writer) error {
			s.consoleDir = dir
			s.consoles++
			return nil
		},
	}
	return s
}

// gotName is the first name of the row whose handler ran, or "" if none did.
func (s *stubDispatcher) gotName() string {
	if s.gotRow < 0 {
		return ""
	}
	return s.commands[s.gotRow].names[0]
}

func TestCommandTableRowsAreComplete(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for i, c := range commands() {
		if len(c.names) == 0 || c.run == nil || c.summary == "" {
			t.Errorf("row %d (%v): want names, a summary and a handler", i, c.names)
		}
		if strings.Contains(c.summary, "\n") {
			t.Errorf("%v: summary must be one line, got %q", c.names, c.summary)
		}
		for _, name := range c.names {
			if name == "" || seen[name] {
				t.Errorf("row %d: name %q is empty or already taken", i, name)
			}
			seen[name] = true
		}
	}
}

func TestEveryCommandNameReachesItsRow(t *testing.T) {
	t.Parallel()
	for i, c := range commands() {
		for _, name := range c.names {
			s := newStubDispatcher()
			s.isDir = func(string) bool { return true } // a same-named directory must not matter
			var out, errw bytes.Buffer
			if err := s.run([]string{name, "a", "--b"}, &out, &errw); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if s.gotRow != i || !slices.Equal(s.gotArgs, []string{"a", "--b"}) {
				t.Fatalf("%s: handler of row %d got %q, want row %d with [a --b]", name, s.gotRow, s.gotArgs, i)
			}
			if s.consoles != 0 {
				t.Fatalf("%s: opened the console", name)
			}
		}
	}
}

// TestTableKeepsEveryCommandAndItsHandler pins the commands the switch
// dispatched before the table (CON-171) to their handlers, so a deleted row
// or a row wired to the wrong handler fails here rather than in the field.
// The handlers that read standard input are wrapped by withStdin, so for
// them only the name is checked. A new command adds itself here too.
func TestTableKeepsEveryCommandAndItsHandler(t *testing.T) {
	t.Parallel()
	want := map[string]any{
		"--version": cmdVersion, "-V": cmdVersion,
		"init": cmdInit, "project": cmdProject, "mate": cmdMate,
		"crew": nil, "brief": nil, "hook": nil,
		"remember": cmdRemember, "memory": cmdMemory, "checkpoint": cmdCheckpoint,
		"recall": cmdRecall, "learnings": cmdLearnings, "wake": cmdWake,
		"send": cmdSend, "notifications": cmdNotifications, "news": cmdNews,
		"work": cmdWork, "peek": cmdPeek, "state": cmdState, "review": cmdReview,
		"report": cmdReport, "diff": cmdDiff, "gc": cmdGC, "merge": cmdMerge,
		"land": cmdLand, "backlog": cmdBacklog, "pr": cmdPR, "events": cmdEvents,
		"reindex": cmdReindex, "usage": cmdUsage, "dashboard": cmdDashboard,
		"console": cmdConsoleLaunch, "pane": cmdPane, "migrate": cmdMigrate,
		"plugin": cmdPlugin,
	}
	var names []string
	for _, c := range commands() {
		names = append(names, c.names...)
	}
	if len(names) != len(want) {
		t.Errorf("table has %d names %v, want %d", len(names), names, len(want))
	}
	for name, fn := range want {
		c, ok := lookupCommand(commands(), name)
		if !ok {
			t.Errorf("%q is not in the table", name)
			continue
		}
		if fn != nil && reflect.ValueOf(c.run).Pointer() != reflect.ValueOf(fn).Pointer() {
			t.Errorf("%q runs the wrong handler", name)
		}
	}
}

func TestVersionIsOneCommandWithTwoNames(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"--version", "-V"} {
		c, ok := lookupCommand(commands(), name)
		if !ok || !slices.Equal(c.names, []string{"--version", "-V"}) {
			t.Fatalf("%s: row %v, ok=%v", name, c.names, ok)
		}
	}
}

func TestUsageListsEveryCommand(t *testing.T) {
	t.Parallel()
	const prefix, suffix = "usage: mate <workspace-dir> | mate <", "> ..."
	usage := usageText(commands())
	lines := strings.Split(usage, "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], prefix) || !strings.HasSuffix(lines[0], suffix) {
		t.Fatalf("usage = %q", usage)
	}
	if want := "internal, run by mate itself: mate <hook|pane> ..."; lines[1] != want {
		t.Fatalf("internal line = %q, want %q", lines[1], want)
	}
	listed := strings.Split(strings.TrimSuffix(strings.TrimPrefix(lines[0], prefix), suffix), "|")
	var internal []string
	for _, c := range commands() {
		for _, name := range c.names {
			if c.internal == slices.Contains(listed, name) {
				t.Errorf("%q: internal=%v but on the main line=%v", name, c.internal, !c.internal)
			}
		}
		if c.internal {
			internal = append(internal, c.names...)
		}
		if !strings.Contains(usage, c.summary) {
			t.Errorf("%v: summary %q missing from usage", c.names, c.summary)
		}
	}
	if !slices.Equal(internal, []string{"hook", "pane"}) {
		t.Fatalf("internal commands = %v, want [hook pane]", internal)
	}
}

func TestBareMateWithoutWorkspacePrintsUsage(t *testing.T) {
	t.Parallel()
	s := newStubDispatcher()
	var out, errw bytes.Buffer
	err := s.run(nil, &out, &errw)
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want *usageError", err)
	}
	if err.Error() != usageText(s.commands) {
		t.Fatalf("err = %q, want the usage text", err)
	}
	if s.consoles != 0 {
		t.Fatal("opened the console without a workspace")
	}
}

func TestBareMateOpensConsoleOnFoundWorkspace(t *testing.T) {
	t.Parallel()
	s := newStubDispatcher()
	s.findWorkspace = func() (string, error) { return "/found/ws", nil }
	var out, errw bytes.Buffer
	if err := s.run(nil, &out, &errw); err != nil {
		t.Fatal(err)
	}
	if s.consoleDir != "/found/ws" || s.gotName() != "" {
		t.Fatalf("console dir = %q, handler = %q; want the console on /found/ws", s.consoleDir, s.gotName())
	}
}

func TestDirectoryArgumentOpensConsole(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "init")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	s := newStubDispatcher()
	s.isDir = isDir
	var out, errw bytes.Buffer
	if err := s.run([]string{dir}, &out, &errw); err != nil {
		t.Fatal(err)
	}
	if s.consoleDir != dir || s.gotName() != "" {
		t.Fatalf("console dir = %q, handler = %q; want the console on %s", s.consoleDir, s.gotName(), dir)
	}

	// A directory followed by another argument is not a workspace to open.
	err := s.run([]string{dir, "extra"}, &out, &errw)
	var ue *usageError
	if !errors.As(err, &ue) || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("err = %v, want unknown command", err)
	}
	if s.consoles != 1 {
		t.Fatalf("consoles = %d, want 1", s.consoles)
	}
}

func TestCommandNameWinsOverSameNamedDirectory(t *testing.T) {
	t.Parallel()
	// The working directory holds a directory named init.
	s := newStubDispatcher()
	s.isDir = func(p string) bool { return p == "init" || p == "./init" }
	var out, errw bytes.Buffer
	if err := s.run([]string{"init"}, &out, &errw); err != nil {
		t.Fatal(err)
	}
	if s.gotName() != "init" || s.consoles != 0 {
		t.Fatalf("handler = %q, consoles = %d; want the init command over the ./init directory", s.gotName(), s.consoles)
	}
	s.gotRow = -1
	if err := s.run([]string{"./init"}, &out, &errw); err != nil {
		t.Fatal(err)
	}
	if s.consoleDir != "./init" || s.gotName() != "" {
		t.Fatalf("console dir = %q, handler = %q; want the console on ./init", s.consoleDir, s.gotName())
	}
}

func TestCmdVersionPrintsVersion(t *testing.T) {
	var out, errw bytes.Buffer
	if err := cmdVersion(nil, &out, &errw); err != nil {
		t.Fatalf("cmdVersion: %v", err)
	}
	if !strings.HasPrefix(out.String(), "mate ") {
		t.Fatalf("out = %q", out.String())
	}
}

func TestRunTopLevelVersionFlag(t *testing.T) {
	var out, errw bytes.Buffer
	if err := run([]string{"--version"}, &out, &errw); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.HasPrefix(out.String(), "mate ") {
		t.Fatalf("out = %q", out.String())
	}
}

func TestRunConsoleSubcommandNeedsATerminal(t *testing.T) {
	ws := t.TempDir()
	var out, errw bytes.Buffer
	if err := cmdInit([]string{ws}, &out, &errw); err != nil {
		t.Fatalf("init: %v", err)
	}
	out.Reset()
	errw.Reset()
	err := run([]string{"console", ws}, &out, &errw)
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want *usageError (console needs a TTY)", err)
	}
	if !strings.Contains(err.Error(), "real terminal") {
		t.Fatalf("err = %v, want the console TTY refusal", err)
	}
}

func TestRunConsoleSubcommandRejectsExtraArgs(t *testing.T) {
	var out, errw bytes.Buffer
	err := run([]string{"console", "a", "b"}, &out, &errw)
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want *usageError", err)
	}
}

func TestRunUnknownCommandIsUsageError(t *testing.T) {
	var out, errw bytes.Buffer
	err := run([]string{"bogus"}, &out, &errw)
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want *usageError", err)
	}
}

func TestRunUnknownProjectSubcommandIsUsageError(t *testing.T) {
	var out, errw bytes.Buffer
	err := run([]string{"project", "bogus"}, &out, &errw)
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want *usageError", err)
	}
}

func TestMainRunExitCodes(t *testing.T) {
	var out, errw bytes.Buffer
	if code := mainRun([]string{"--version"}, &out, &errw); code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}

	out.Reset()
	errw.Reset()
	if code := mainRun([]string{"bogus"}, &out, &errw); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.HasPrefix(errw.String(), "mate: ") {
		t.Fatalf("stderr = %q, want it to start with %q", errw.String(), "mate: ")
	}

	out.Reset()
	errw.Reset()
	ws := t.TempDir()
	missing := filepath.Join(ws, "nope")
	if code := mainRun([]string{"project", "list", "--workspace", missing}, &out, &errw); code != 1 {
		t.Fatalf("code = %d, want 1", code)
	}
}

func TestFindWorkspaceDirWalksUp(t *testing.T) {
	ws := t.TempDir()
	var out, errw bytes.Buffer
	if err := cmdInit([]string{ws}, &out, &errw); err != nil {
		t.Fatalf("init: %v", err)
	}
	nested := filepath.Join(ws, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldwd)
	if err := os.Chdir(nested); err != nil {
		t.Fatal(err)
	}

	dir, err := findWorkspaceDir("")
	if err != nil {
		t.Fatalf("findWorkspaceDir: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(ws)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want {
		t.Fatalf("dir = %s, want %s", dir, ws)
	}
}

func TestFindWorkspaceDirNoneFound(t *testing.T) {
	dir := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldwd)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	if _, err := findWorkspaceDir(""); err == nil {
		t.Fatal("want error when no workspace is found")
	}
}

func TestFindWorkspaceDirEnvVar(t *testing.T) {
	ws := t.TempDir()
	var out, errw bytes.Buffer
	if err := cmdInit([]string{ws}, &out, &errw); err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Setenv("MATE_WORKSPACE", ws)

	dir, err := findWorkspaceDir("")
	if err != nil {
		t.Fatalf("findWorkspaceDir: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(ws)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want {
		t.Fatalf("dir = %s, want %s", dir, ws)
	}
}

func TestFindWorkspaceDirFlagWinsOverEnv(t *testing.T) {
	ws := t.TempDir()
	var out, errw bytes.Buffer
	if err := cmdInit([]string{ws}, &out, &errw); err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Setenv("MATE_WORKSPACE", t.TempDir())

	dir, err := findWorkspaceDir(ws)
	if err != nil {
		t.Fatalf("findWorkspaceDir: %v", err)
	}
	if dir != ws {
		t.Fatalf("dir = %s, want %s", dir, ws)
	}
}
