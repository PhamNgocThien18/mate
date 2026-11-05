package spawn

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/github"
	"github.com/nguyenngocanh94/mate/internal/gitx"
	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/screen"
	"github.com/nguyenngocanh94/mate/internal/screen/fixture"
	"github.com/nguyenngocanh94/mate/internal/store"
	"github.com/nguyenngocanh94/mate/internal/teardown"
)

// Meta keys written to `mate/mate.meta`. They are spelled once, here, so the
// CLI, the tests and later tasks cannot drift apart.
const (
	MetaHarness   = "harness"
	MetaAgent     = "agent"
	MetaPane      = "pane"
	MetaTab       = "tab"
	MetaWorkspace = "workspace"
	MetaSession   = "session"
	MetaSessionID = "session_id"
	MetaStartedAt = "started_at"
	// MetaLaunchedAt is when the harness process was started, taken just
	// before `agent start`. started_at is written only once the agent is
	// ready and has its first prompt, which for Codex is after the rollout
	// already exists, so it cannot anchor the rollout adoption rule
	// (harness.AdoptCodexRollout rejects a rollout older than its anchor;
	// measured 2026-09-24, task 34: a rollout opened 0.2s before started_at).
	MetaLaunchedAt = "launched_at"
	MetaStoppedAt  = "stopped_at"
	// MetaTranscript is the Claude transcript path the Stop hook reports
	// (docs/mvp.md decision 9: ".meta ghi transcript= và session_id= từ
	// ngày đầu"). Task 08's mate-stop hook writes it; StartMate does not.
	MetaTranscript = "transcript"
	// MetaResumed and MetaResumedFrom record task 10's resume decision.
	// Both are written only when this start resumed a previous harness
	// session; a fresh start's meta rewrite (StartMate always writes a
	// brand new map) leaves them absent, not "false"/"".
	MetaResumed     = "resumed"
	MetaResumedFrom = "resumed_from"
	// MetaModel and MetaEffort are the launch profile the agent was started
	// with (harness/profile.go): what was asked for, written only when
	// something was. An effort the harness does not take is still recorded;
	// it was requested, and left out of the launch.
	MetaModel  = "model"
	MetaEffort = "effort"
	// MetaBudget is the crew's token budget (CON-76): the dispatch rule's
	// `budget` the Mate passed as `--budget`, else the project's
	// `budget.crew_tokens`, written only when one of them is set. The
	// observer's budget check reads it before the project's block.
	MetaBudget = store.MetaBudget
	// MetaMode is ModeHeadless for a Mate that runs as a headless host
	// (CON-78). Absent means the harness's TUI runs in the pane. A stop
	// keeps it, so a restart comes back in the same mode.
	MetaMode = store.MetaMateMode
)

// Mate modes (StartRequest.Mode, MetaMode).
const (
	ModePane     = "pane"
	ModeHeadless = store.MateModeHeadless
)

// AgentNamePrefix is the first half of a Mate's live Herdr agent name; the
// project name is the second, so a Mate is `mate-<project>`.
const AgentNamePrefix = "mate"

// cleanupTimeout bounds a compensation that runs after its caller's context
// was cancelled.
const cleanupTimeout = 30 * time.Second

// cleanupContext is the context a compensation runs under: the caller's
// values without its cancellation, bounded by cleanupTimeout. A start is
// most often compensated *because* it was cancelled (the Console quit
// mid-start), and undoing it with the cancelled context would fail every
// Herdr call at once and leave the agent running unrecorded.
func cleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
}

// MateTabLabel is the Herdr tab label of the Mate pane.
const MateTabLabel = "mate"

// Defaults for the timeouts a start depends on. They are generous because
// the cost of being wrong is asymmetric: a slow harness that is still
// drawing its banner must not be torn down, while a wedged one is reported
// with the screen in the error either way.
const (
	// DefaultReadinessTimeout is how long Herdr may take to report the agent
	// idle, done or blocked.
	DefaultReadinessTimeout = 90 * time.Second
	// DefaultStartTimeout is `agent start --timeout`. A harness held by a
	// startup dialog never becomes interactive; the Herdr adapter adopts it
	// after this timeout and the settle answers the dialog
	// (docs/plans/pane-state-jev-2026-10-13.md section 5). An agent Herdr
	// has not listed yet by then is polled for up to 50 s more
	// (runtime.Herdr's adoption window), so a slow harness keeps the 60 s
	// a start had before. Herdr accepts > 3000 and <= 300000 ms.
	DefaultStartTimeout = 10 * time.Second
)

// Deps are the collaborators a start or stop needs. The zero value is not
// usable: Runtime and Harnesses are required. Everything else has a default.
type Deps struct {
	// Runtime is the Herdr adapter. Tests pass runtime.NewFake().
	Runtime runtime.Adapter
	// DefaultMode is the Mate's mode when neither the start, the last run
	// nor project.yaml names one. LiveDeps sets ModeHeadless (2026-10-15);
	// empty is ModePane.
	DefaultMode string
	// HostStopWait bounds how long a stop lets a headless Mate finish its
	// turn; zero means HostStopWait.
	HostStopWait time.Duration
	// Harnesses are the harnesses a start may launch and a stop may meet.
	// The zero Registry holds none, so every start is refused; cmd/mate
	// passes catalog.Default().
	Harnesses harness.Registry
	// Names is the live agent-name registry. Nil means a fresh in-process
	// one, which is what a one-shot CLI invocation wants.
	Names runtime.LiveNameRegistry
	// ConfigHome is the Herdr config home used to resolve socket paths.
	// Empty means the environment's (HERDR_CONFIG_PATH, XDG_CONFIG_HOME,
	// then $HOME/.config).
	ConfigHome string
	// Binary is the absolute path of the mate binary the Mate invokes.
	// Empty means os.Executable.
	Binary string
	// ReadinessTimeout bounds the wait for Herdr to report the agent ready.
	ReadinessTimeout time.Duration
	// StartupPromptTimeout bounds the startup-screen settle. Zero means the
	// readiness timeout (ADR 0028).
	StartupPromptTimeout time.Duration
	// StartTimeout is passed to `agent start --timeout`.
	StartTimeout time.Duration
	// BriefDeliveryTimeout bounds the wait for a crew pane to leave idle
	// after the brief prompt. Zero means DefaultBriefDeliveryTimeout.
	BriefDeliveryTimeout time.Duration
	// GitHub is the `gh` pull request delivery merges and checks with
	// (docs/mvp.md M18). The zero value is the real gh.
	GitHub github.Client
	// Git runs the crew worktree commands. The zero value is the real git.
	Git gitx.Git
	// Worktrees acquires and releases crew working copies. Nil means
	// GitWorktrees over Git: linked git worktrees under `.worktrees/`.
	Worktrees Worktrees
	// Cleaner tears down what a crew's worktree started before a stop
	// gives the worktree back (CON-111). Nil runs only the repo's
	// teardown command; LiveDeps adds the process sweep. What a toolchain
	// starts is the teardown command's job (CON-146).
	Cleaner Cleaner
	// Sleep is the pause between pane polls; tests shorten it.
	Sleep func(ctx context.Context, d time.Duration) error
	// Now is the clock the meta's timestamps come from.
	Now func() time.Time
	// NewSessionID mints the Claude session uuid; tests make it
	// deterministic. Nil means uuid.NewString.
	NewSessionID func() string
	// Observer reads the pane during the startup settle (settle.go). Nil
	// means the fixture observer (internal/screen/fixture).
	Observer screen.Observer
}

// LiveOption changes what LiveDeps builds.
type LiveOption func(*liveOptions)

type liveOptions struct {
	herdrBinary string
}

// WithHerdrBinary makes the adapter run the herdr executable at path instead
// of the "herdr" on PATH. The console passes the one its stage column runs,
// which can be where the console's PATH does not reach (CON-189).
func WithHerdrBinary(path string) LiveOption {
	return func(o *liveOptions) { o.herdrBinary = path }
}

// LiveDeps is what the CLI uses: the real Herdr adapter over os/exec, and
// the harnesses the binary was built with. The name registry is shared with
// the adapter, which re-reserves the name at start; without one StartAgent
// refuses outright. Given no option, the adapter runs the "herdr" on PATH.
func LiveDeps(harnesses harness.Registry, opts ...LiveOption) Deps {
	var o liveOptions
	for _, opt := range opts {
		opt(&o)
	}
	names := runtime.NewMemoryNameRegistry()
	rt := runtime.NewHerdr(process.ExecRunner{})
	rt.Binary = o.herdrBinary
	rt.Names = names
	return Deps{Runtime: rt, Harnesses: harnesses, Names: names, DefaultMode: ModeHeadless, Cleaner: teardown.Live()}
}

// Cleaner is what a crew stop asks to tear a worktree's resources down.
// teardown.Cleaner is the real one.
type Cleaner interface {
	Clean(ctx context.Context, t teardown.Target) teardown.Report
}

func (d Deps) cleaner() Cleaner {
	if d.Cleaner != nil {
		return d.Cleaner
	}
	return teardown.Cleaner{}
}

// defaultHarness is the harness a role gets when the request named none:
// the one the workspace recorded, else the registry's default for the role.
func (d Deps) defaultHarness(recorded string, role harness.AgentRole) (harness.Kind, error) {
	if strings.TrimSpace(recorded) == "" {
		return d.Harnesses.Default(role)
	}
	return d.Harnesses.Parse(recorded)
}

func (d Deps) names() runtime.LiveNameRegistry {
	if d.Names != nil {
		return d.Names
	}
	return runtime.NewMemoryNameRegistry()
}

func (d Deps) readinessTimeout() time.Duration {
	if d.ReadinessTimeout > 0 {
		return d.ReadinessTimeout
	}
	return DefaultReadinessTimeout
}

func (d Deps) startupPromptTimeout() time.Duration {
	if d.StartupPromptTimeout > 0 {
		return d.StartupPromptTimeout
	}
	return d.readinessTimeout()
}

func (d Deps) briefDeliveryTimeout() time.Duration {
	if d.BriefDeliveryTimeout > 0 {
		return d.BriefDeliveryTimeout
	}
	return DefaultBriefDeliveryTimeout
}

// git is the git command surface the crew saga uses. A Deps with no Git
// runs the real binary, which is what the CLI wants and what the package's
// own tests want too: they operate on real temporary repositories.
func (d Deps) git() gitx.Git {
	if d.Git.Runner != nil {
		return d.Git
	}
	return gitx.New()
}

// worktrees is the working-copy backend a spawn acquires from and a stop
// releases to.
func (d Deps) worktrees() Worktrees {
	if d.Worktrees != nil {
		return d.Worktrees
	}
	return GitWorktrees{Git: d.git()}
}

func (d Deps) startTimeout() time.Duration {
	if d.StartTimeout > 0 {
		return d.StartTimeout
	}
	return DefaultStartTimeout
}

func (d Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now().UTC()
}

func (d Deps) observer() screen.Observer {
	if d.Observer != nil {
		return d.Observer
	}
	return fixture.New()
}

func (d Deps) sleep() sleeper {
	if d.Sleep != nil {
		return d.Sleep
	}
	return sleepCtx
}

// binary is the absolute path the rendered manual tells the Mate to run.
func (d Deps) binary() (string, error) {
	if strings.TrimSpace(d.Binary) != "" {
		return filepath.Abs(d.Binary)
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return exe, nil
}

// configHome resolves the Herdr config home the session socket lives under.
// It mirrors what the Herdr adapter does for its own default, because
// SessionSpec requires the caller to name it.
func (d Deps) configHome() (string, error) {
	home := strings.TrimSpace(d.ConfigHome)
	if home == "" {
		home = herdrConfigHomeFromEnv()
	}
	if home == "" {
		return "", errUsage("cannot resolve the Herdr config home: set HERDR_CONFIG_PATH, XDG_CONFIG_HOME or HOME")
	}
	return home, nil
}

func herdrConfigHomeFromEnv() string {
	if v := strings.TrimSpace(os.Getenv("HERDR_CONFIG_PATH")); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); v != "" {
		return v
	}
	if home := strings.TrimSpace(os.Getenv("HOME")); home != "" {
		return filepath.Join(home, ".config")
	}
	return ""
}

// sessionSpec names the workspace's Herdr session. The name is the one
// stored in workspace.yaml at Init, so moving or re-resolving the workspace
// never renames a live session; the owning id is derived from the resolved
// root path.
func (d Deps) sessionSpec(w *store.Workspace) (runtime.SessionSpec, error) {
	home, err := d.configHome()
	if err != nil {
		return runtime.SessionSpec{}, err
	}
	id, err := runtime.ParseWorkspaceID(store.SessionName(w.Root()))
	if err != nil {
		return runtime.SessionSpec{}, err
	}
	return runtime.SessionSpec{
		WorkspaceID: id,
		Name:        w.Session(),
		ConfigHome:  home,
		Names:       d.names(),
	}, nil
}
