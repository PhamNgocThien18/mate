package spawn_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness/catalog"
	"github.com/nguyenngocanh94/mate/internal/harness/claude"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// TestLiveHeadlessMate is CON-78's proof, moved out of Herdr by CON-225: a
// headless Claude Mate whose host is a process of its own. Herdr lists no
// agent for it, two app lines are two `claude -p` turns on one session
// (the second resumed), the Mate's own hooks record both answers in
// sent.log, and the stop is the host's own clean exit.
func TestLiveHeadlessMate(t *testing.T) {
	requireLive(t)
	session, configHome := liveLabSession(t)

	// The host's socket lives in the Mate directory, so the workspace sits
	// under /private/tmp: short enough for a unix socket path, and with no
	// symlink for Herdr's resolved pane cwd to disagree with.
	root, err := os.MkdirTemp("/private/tmp", "mh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	w, err := store.Init(root, store.Defaults{})
	if err != nil {
		t.Fatalf("store.Init: %v", err)
	}
	useLabSession(t, w, session)
	if w, err = store.Open(root); err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(w.ProjectHome("shop"), "shop")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	liveGit(t, repo, "init", "-b", "main")
	liveGit(t, repo, "config", "user.email", "mate-test@example.com")
	liveGit(t, repo, "config", "user.name", "mate test")
	liveGit(t, repo, "commit", "--allow-empty", "-m", "init")
	if err := w.AddProject("shop", store.ProjectConfig{
		Mate:  store.MateConfig{Model: "haiku", Effort: "low"},
		Repos: []store.RepoConfig{{Path: repo, DefaultBranch: "main"}},
	}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}

	names := runtime.NewMemoryNameRegistry()
	rt := runtime.NewHerdr(process.ExecRunner{})
	rt.Names = names
	rt.StartServer = func(context.Context, string) error {
		t.Fatal("the lab session is provisioned by the runner; this test must not start a Herdr server")
		return nil
	}
	deps := spawn.Deps{
		Harnesses:        catalog.Default(),
		Runtime:          rt,
		Names:            names,
		ConfigHome:       configHome,
		Binary:           binaryPath(t),
		ReadinessTimeout: 90 * time.Second,
		StartTimeout:     60 * time.Second,
	}
	marker := filepath.Join(configHome, "mate", "session-owners", session)
	_ = os.Remove(marker)
	t.Cleanup(func() { _ = os.Remove(marker) })
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	started, err := spawn.StartMate(ctx, w, deps, spawn.StartRequest{Project: "shop", Harness: claude.KindClaude, Mode: spawn.ModeHeadless})
	if err != nil {
		t.Fatalf("StartMate: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer stopCancel()
		_, _ = spawn.StopMate(stopCtx, w, deps, "shop")
	})
	if !started.Headless || started.Agent != "mate-shop" || started.PID <= 0 || started.Pane != "" {
		t.Fatalf("started = %+v, want a host of its own", started)
	}
	// CON-225: the host is a process of its own, and Herdr has nothing of it.
	handle := runtime.AgentHandle{Session: runtime.SessionHandle{Name: session, ConfigHome: configHome}, Name: started.Agent, RawID: "shop"}
	if _, err := rt.InspectAgent(ctx, handle); !runtime.IsAgentNotFound(err) {
		t.Fatalf("Herdr answers for %s (%v), want no agent: the host runs outside Herdr", started.Agent, err)
	}
	if alive := spawn.HostLiveness(ctx, w); !alive["shop"] {
		t.Fatalf("host liveness = %v, want shop answering", alive)
	}

	host := matehost.Dial(w.MateDir("shop"))
	for i, word := range []string{"pong", "pang"} {
		line := matehost.Marker + "ping: run no commands and reply with exactly the word " + word
		if err := host.Deliver(ctx, line); err != nil {
			t.Fatalf("deliver %d: %v", i, err)
		}
		for {
			st, err := host.State(ctx)
			if err != nil {
				t.Fatalf("state: %v", err)
			}
			if !st.Busy {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("the turn never ended")
			case <-time.After(time.Second):
			}
		}
		items, err := host.History(ctx, 1)
		if err != nil || len(items) != 1 {
			t.Fatalf("history = %+v, %v", items, err)
		}
		if it := items[0]; it.Error != "" || !strings.Contains(strings.ToLower(it.Reply), word) {
			t.Fatalf("turn %d = %+v, want a reply with %q", i, it, word)
		}
	}
	entries, _, err := w.ReadSent("shop", 0)
	if err != nil {
		t.Fatal(err)
	}
	answers := 0
	for _, e := range entries {
		if e.Source == store.SourceMate && e.Target == store.SourceUser {
			answers++
		}
	}
	if answers < 2 {
		t.Fatalf("sent.log has %d answers from the Mate, want both turns' Stop hooks: %+v", answers, entries)
	}
	meta := readMeta(t, w, "shop")
	if meta[spawn.MetaSessionID] != started.SessionID || meta[spawn.MetaTranscript] == "" {
		t.Fatalf("meta = %v, want the one session and its transcript", meta)
	}

	stopped, err := spawn.StopMate(ctx, w, deps, "shop")
	if err != nil {
		t.Fatalf("StopMate: %v", err)
	}
	if !stopped.Confirmed || stopped.Forced != "" {
		t.Fatalf("stop = %+v, want the host's clean exit", stopped)
	}
	if alive := spawn.HostLiveness(ctx, w); len(alive) != 0 {
		t.Fatalf("host liveness after the stop = %v, want no host recorded", alive)
	}
	if err := matehost.Dial(w.MateDir("shop")).Ping(ctx); err == nil {
		t.Fatal("the host still answers after its stop")
	}
}
