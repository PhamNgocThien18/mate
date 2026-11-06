package main

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/quota"
)

// TestMain: started with fakeCoreEnv set, the test binary is not a test run
// but the fake core `mate core start` runs in core_control_test.go.
func TestMain(m *testing.M) {
	if os.Getenv(fakeCoreEnv) != "" {
		os.Exit(fakeCoreMain())
	}
	os.Exit(m.Run())
}

// testCLI is the CLI a test runs: an empty environment where the CLI reads
// one (cli.getenv: MATE_CALLER, and a hook's MATE_WORKSPACE and
// MATE_PROJECT), no current directory for a hook, and no quota-axi, so no
// test depends on this machine's vendor accounts. A test that wants more
// sets the field, or calls withEnv or inDir. Commands that find their
// workspace through findWorkspaceDir still read the process's
// MATE_WORKSPACE and cwd, so a test passes --workspace.
func testCLI() cli {
	c := newCLI()
	c.getenv = func(string) string { return "" }
	c.getwd = func() (string, error) { return "", errors.New("a test CLI has no current directory; use inDir") }
	c.readQuota = func(context.Context) (quota.Snapshot, error) { return quota.Snapshot{}, quota.ErrNotInstalled }
	return c
}

// withEnv is c over an environment of exactly these key, value pairs.
func (c cli) withEnv(kv ...string) cli {
	env := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		env[kv[i]] = kv[i+1]
	}
	c.getenv = func(k string) string { return env[k] }
	return c
}

// inDir is c run from dir.
func (c cli) inDir(dir string) cli {
	c.getwd = func() (string, error) { return dir, nil }
	return c
}
