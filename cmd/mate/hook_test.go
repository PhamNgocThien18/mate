package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/store"
)

// mateDirFor creates a workspace under t.TempDir() and returns the absolute,
// symlink-resolved cwd of one project's Mate: <root>/.mate/projects/<p>/mate.
// A hook process always runs with this as its cwd, so tests that do not set
// MATE_PROJECT/MATE_WORKSPACE run the hook from here (cli.inDir) to
// exercise the fallback.
func mateDirFor(t *testing.T, project string) (root, mateDir string) {
	t.Helper()
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.Init(resolved, workspaceDefaults())
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	mateDir = w.MateDir(project)
	if err := os.MkdirAll(mateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return w.Root(), mateDir
}

func TestCmdHookMatePromptWritesSentLogFromCwd(t *testing.T) {
	t.Parallel()
	root, mateDir := mateDirFor(t, "shop")
	c := testCLI().inDir(mateDir)

	var stdout, stderr bytes.Buffer
	stdin := strings.NewReader(`{"prompt":"ship the checkout fix","session_id":"s1"}`)
	if err := c.cmdHook([]string{"mate-prompt"}, stdin, &stdout, &stderr); err != nil {
		t.Fatalf("cmdHook mate-prompt: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want nothing (UserPromptSubmit stdout becomes model context)", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want nothing on success", stderr.String())
	}

	w, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	entries, _, err := w.ReadSent("shop", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Text != "ship the checkout fix" || entries[0].Source != store.SourceUser {
		t.Fatalf("sent.log = %+v", entries)
	}
}

func TestCmdHookMateStopUpdatesMetaFromCwd(t *testing.T) {
	t.Parallel()
	root, mateDir := mateDirFor(t, "shop")
	c := testCLI().inDir(mateDir)

	var stdout, stderr bytes.Buffer
	stdin := strings.NewReader(`{"session_id":"sess-1","transcript_path":"/tmp/t.jsonl","last_assistant_message":"PONG"}`)
	if err := c.cmdHook([]string{"mate-stop"}, stdin, &stdout, &stderr); err != nil {
		t.Fatalf("cmdHook mate-stop: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want nothing on success", stderr.String())
	}

	w, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := w.ReadMateMeta("shop")
	if err != nil {
		t.Fatal(err)
	}
	if meta["session_id"] != "sess-1" || meta["transcript"] != "/tmp/t.jsonl" {
		t.Fatalf("meta = %+v", meta)
	}
	entries, _, err := w.ReadSent("shop", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Text != "PONG" || entries[0].Source != store.SourceMate {
		t.Fatalf("sent.log = %+v", entries)
	}
}

func TestCmdHookEnvOverridesCwd(t *testing.T) {
	t.Parallel()
	root, mateDir := mateDirFor(t, "shop")
	// A cwd that resolves to no workspace at all: the env vars alone must be
	// enough to find the right one.
	elsewhere := t.TempDir()
	c := testCLI().withEnv("MATE_WORKSPACE", root, "MATE_PROJECT", "shop").inDir(elsewhere)

	var stdout, stderr bytes.Buffer
	stdin := strings.NewReader(`{"prompt":"hi"}`)
	if err := c.cmdHook([]string{"mate-prompt"}, stdin, &stdout, &stderr); err != nil {
		t.Fatalf("cmdHook mate-prompt: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want nothing (env named the workspace and project)", stderr.String())
	}
	_ = mateDir // only used to build the workspace; the cwd is elsewhere

	w, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	entries, _, err := w.ReadSent("shop", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("sent.log = %+v, want one entry written via the env override", entries)
	}
}

func TestCmdHookMatePromptMalformedJSONExitsZeroAndWritesNothing(t *testing.T) {
	t.Parallel()
	root, mateDir := mateDirFor(t, "shop")
	c := testCLI().inDir(mateDir)

	var stdout, stderr bytes.Buffer
	stdin := strings.NewReader(`{not json`)
	if err := c.cmdHook([]string{"mate-prompt"}, stdin, &stdout, &stderr); err != nil {
		t.Fatalf("cmdHook mate-prompt: %v, want nil (hooks always exit 0)", err)
	}
	if stderr.Len() == 0 {
		t.Fatal("stderr = empty, want the parse failure reported")
	}
	w, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	entries, _, err := w.ReadSent("shop", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("sent.log = %+v, want nothing appended for malformed JSON", entries)
	}
}

func TestCmdHookNoWorkspaceExitsZero(t *testing.T) {
	t.Parallel()
	c := testCLI().inDir(t.TempDir())

	var stdout, stderr bytes.Buffer
	stdin := strings.NewReader(`{"prompt":"hi"}`)
	if err := c.cmdHook([]string{"mate-prompt"}, stdin, &stdout, &stderr); err != nil {
		t.Fatalf("cmdHook mate-prompt: %v, want nil even with no workspace found", err)
	}
	if stderr.Len() == 0 {
		t.Fatal("stderr = empty, want the missing-workspace failure reported")
	}
}

func TestCmdHookUnknownSubcommandIsUsageError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := testCLI().cmdHook([]string{"bogus"}, strings.NewReader(""), &stdout, &stderr)
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want *usageError", err)
	}
}

// Not parallel: the hook reads its payload from os.Stdin, which the test
// swaps for a pipe.
func TestRunDispatchesHook(t *testing.T) {
	_, mateDir := mateDirFor(t, "shop")
	c := testCLI().inDir(mateDir)
	old := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old })
	if _, err := w.WriteString(`{"prompt":"hi"}`); err != nil {
		t.Fatal(err)
	}
	w.Close()

	var stdout, stderr bytes.Buffer
	if err := c.run([]string{"hook", "mate-prompt"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
}
