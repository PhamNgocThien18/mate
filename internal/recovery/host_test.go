package recovery

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// A headless Mate whose host of its own died with the machine (CON-225) is
// brought back on its session even when Herdr cannot be asked, and no
// Herdr server is started for it.
func TestRunRestartsADeadHostWithoutHerdr(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(claude.ClaudeConfigDirEnv, "")
	// The host's socket lives in the Mate directory: a short root.
	root, err := os.MkdirTemp("/tmp", "rh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	w := crewWorkspace(t, filepath.Join(root, "ws"))
	if err := w.WriteMateMeta("shop", map[string]string{
		spawn.MetaHarness: "claude", spawn.MetaAgent: "mate-shop", spawn.MetaMode: spawn.ModeHeadless,
		spawn.MetaPID: "4242", spawn.MetaSocket: matehost.SocketPath(w.MateDir("shop")), spawn.MetaSessionID: sessionA,
	}); err != nil {
		t.Fatal(err)
	}
	fake := runtime.NewFake()
	fake.SessionLookupErr = context.DeadlineExceeded
	rt := &herdrDown{Fake: fake}
	d := deps(t, rt, fake.Names)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := make(chan struct{})
	d.Hosts = spawn.HostFuncs{Start: func(spec spawn.HostSpec) (int, <-chan struct{}, error) {
		l, err := matehost.ReadLaunch(spec.Dir)
		if err != nil {
			return 0, nil, err
		}
		host := matehost.New(&matehost.Runner{Launch: l, Turns: claude.Claude{}}, matehost.Options{PID: 4343})
		go func() {
			defer close(served)
			_ = host.Serve(ctx, matehost.SocketPath(spec.Dir), nil)
		}()
		return 4343, nil, nil
	}}
	t.Cleanup(func() { cancel(); <-served })

	res := Run(context.Background(), w, d, nil)
	if res.Err == nil {
		t.Fatal("want the Herdr question named as not asked")
	}
	if len(res.Items) != 1 || res.Items[0].Crew != "" || res.Items[0].Err != nil || !res.Items[0].Resumed {
		t.Fatalf("items = %+v, want the Mate back on its session", res.Items)
	}
	if len(rt.order) != 0 {
		t.Fatalf("calls = %v, want no Herdr server started for a host of its own", rt.order)
	}
	meta, _ := w.ReadMateMeta("shop")
	if !store.MateHostDetached(meta) || meta[spawn.MetaPID] != "4343" || meta[spawn.MetaSessionID] != sessionA {
		t.Fatalf("meta = %v", meta)
	}
}
