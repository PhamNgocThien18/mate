package herdr

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/process"
	"github.com/nguyenngocanh94/mate/internal/runtime"
)

// Herdr is the live RuntimeAdapter. It talks to the herdr executable through
// a ProcessRunner and never imports application or spike/g1.
//
// EnsureSession binds a named session. Headless Herdr 0.8.2 starts with zero
// workspaces (live-captured); EnsureProjectWorkspace lists, matches label+cwd
// (labels are not unique), and CreateAgentTab renames the workspace-create
// root tab for Mate. There is no `herdr agent stop`.
type Herdr struct {
	Runner process.Runner
	// Binary is the herdr executable; empty means "herdr" on PATH.
	Binary string
	// Names is the live-name registry used to re-reserve at start. Required
	// for StartAgent. A reservation proves allocation, not current ownership.
	Names runtime.LiveNameRegistry
	Clock runtime.Clock
	// StartServer starts a named headless `herdr server --session`. Herdr
	// 0.8.2 does not daemonize (detached_server_daemon: false). Nil uses
	// the default starter, which is process-start + Wait in a goroutine.
	// Lab isolation owns provision; tests inject a stub.
	StartServer func(ctx context.Context, session string) error
	// Attach, when set, is the TTY handoff for AttachAgent (TTYHandoff.Attach
	// in production). Nil means this adapter resolves the attach argv and
	// then refuses, rather than pretending it attached.
	Attach func(ctx context.Context, argv []string) error
	// PaneShellBudget bounds the best-effort wait for a new pane to draw a
	// shell title. Zero means paneShellBudget. Tests shorten it; it is a
	// budget, not a gate - see waitPaneShell.
	PaneShellBudget time.Duration
	// AttachPreflight, when set, runs before AttachAgent probes Herdr. It is
	// where the caller checks preconditions no probe can establish - in
	// production, that its own stdio is a terminal - so a caller that cannot
	// use the answers does not ask the questions.
	AttachPreflight func(ctx context.Context) error
	// AdoptSleep is the pause between the ListAgents polls of a timed-out
	// start (adoptTimedOutStart). Nil sleeps for real; tests replace it so
	// the adoption window costs them no wall time.
	AdoptSleep func(ctx context.Context, d time.Duration) error
}

// paneShellBudget is how long a freshly created pane is given to draw a shell
// title before the caller proceeds anyway. paneBusyAttempts/paneBusyRetry are
// StartAgent's retry of Herdr's own agent_pane_busy, which is the
// authoritative "this pane cannot host an agent yet" (unchanged since G4-04).
const (
	paneShellBudget  = 5 * time.Second
	paneBusyAttempts = 25
	paneBusyRetry    = 100 * time.Millisecond
)

// startAdoptWindow bounds how long a timed-out `agent start` waits for Herdr
// to list the agent in its pane, polling every startAdoptPoll. A harness
// slower than the start timeout is still loading, not a failed start
// (docs/plans/pane-state-jev-2026-10-13.md section 5); the start timeout
// (spawn.DefaultStartTimeout, 10 s) plus this window keeps the 60 s an
// `agent start` was given before that timeout came down.
const (
	startAdoptWindow = 50 * time.Second
	startAdoptPoll   = time.Second
)

// errAgentNotListed is an adoption that found no agent in the pane yet:
// the one outcome worth polling again.
var errAgentNotListed = errors.New("Herdr lists no agent in the pane yet")

// NewHerdr returns a Herdr adapter over runner.
func NewHerdr(runner process.Runner) *Herdr {
	return &Herdr{Runner: runner}
}

var (
	_ runtime.Adapter     = (*Herdr)(nil)
	_ runtime.ReadAdapter = (*Herdr)(nil)
)

func (h *Herdr) binary() string {
	if h == nil || h.Binary == "" {
		return "herdr"
	}
	return h.Binary
}

func (h *Herdr) paneShellBudget() time.Duration {
	if h != nil && h.PaneShellBudget > 0 {
		return h.PaneShellBudget
	}
	return paneShellBudget
}

func (h *Herdr) now() time.Time {
	if h != nil && h.Clock != nil {
		return h.Clock.Now()
	}
	return time.Now().UTC()
}

// WithSession inserts `--session NAME` in the option region (before `--`).
// Extra harness args after `--` swallow a trailing --session; G1 observed
// that as agent_pane_not_found. If args already carry --session before `--`,
// they are returned unchanged so AgentStartCommand.Argv is not doubled.
func WithSession(session string, args []string) []string {
	if strings.TrimSpace(session) == "" {
		return append([]string(nil), args...)
	}
	if runtime.SessionBeforeTerminator(args) {
		return append([]string(nil), args...)
	}
	out := make([]string, 0, len(args)+2)
	inserted := false
	for _, a := range args {
		if a == "--" && !inserted {
			out = append(out, "--session", session)
			inserted = true
		}
		out = append(out, a)
	}
	if !inserted {
		out = append(out, "--session", session)
	}
	return out
}

func (h *Herdr) run(ctx context.Context, session string, args []string) (process.Result, error) {
	if h == nil || h.Runner == nil {
		return process.Result{}, observability.NewError(observability.CodeUnknown, "herdr: process runner is required")
	}
	if strings.TrimSpace(session) == "" {
		return process.Result{}, observability.NewError(observability.CodeUsage, "herdr: named session is required")
	}
	res, err := h.Runner.Run(ctx, process.Spec{Name: h.binary(), Args: WithSession(session, args)})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return process.Result{}, err
		}
		return process.Result{}, observability.WrapError(observability.CodeRuntimeUnavailable, "herdr executable failed to run", err)
	}
	if res.ExitCode != 0 {
		return res, mapProcessFailure(res.ExitCode, res.Stdout, res.Stderr)
	}
	return res, nil
}

// EnsureSession implements Adapter. The default session is never an app
// Workspace. A named session that is not running is started through
// StartServer; Herdr 0.8.2 does not daemonize, so that start is a long-lived
// process, not ProcessRunner.Run.
func (h *Herdr) EnsureSession(ctx context.Context, spec runtime.SessionSpec) (runtime.SessionHandle, error) {
	handle, err := runtime.ResolveSession(spec)
	if err != nil {
		return runtime.SessionHandle{}, err
	}
	if err := runtime.ClaimSessionOwner(handle.ConfigHome, handle.Name, spec.WorkspaceID.String()); err != nil {
		return runtime.SessionHandle{}, err
	}
	listed, err := h.lookupSession(ctx, handle.Name)
	if err != nil {
		return runtime.SessionHandle{}, err
	}
	if listed != nil {
		if listed.Default {
			return runtime.SessionHandle{}, fmt.Errorf("session: refusing to bind an app Workspace to the Herdr default session")
		}
		if listed.SocketPath != "" {
			handle.SocketPath = listed.SocketPath
		}
		if listed.Running {
			return handle, nil
		}
	}
	if err := h.startNamedServer(ctx, handle.Name); err != nil {
		return runtime.SessionHandle{}, err
	}
	if err := h.waitRunning(ctx, handle.Name); err != nil {
		return runtime.SessionHandle{}, err
	}
	return handle, nil
}

// LookupSession implements Adapter. It never starts a server and never
// writes the owner marker.
func (h *Herdr) LookupSession(ctx context.Context, spec runtime.SessionSpec) (runtime.SessionHandle, bool, error) {
	handle, err := runtime.SessionAddress(spec)
	if err != nil {
		return runtime.SessionHandle{}, false, err
	}
	listed, err := h.lookupSession(ctx, handle.Name)
	if err != nil {
		return runtime.SessionHandle{}, false, err
	}
	if listed == nil || !listed.Running {
		return handle, false, nil
	}
	if listed.Default {
		return runtime.SessionHandle{}, false, fmt.Errorf("session: refusing to bind an app Workspace to the Herdr default session")
	}
	if listed.SocketPath != "" {
		handle.SocketPath = listed.SocketPath
	}
	return handle, true, nil
}

func (h *Herdr) lookupSession(ctx context.Context, name string) (*sessionInfo, error) {
	res, err := h.run(ctx, name, []string{"session", "list", "--json"})
	if err != nil {
		return nil, err
	}
	list, err := parseSessionList(res.Stdout)
	if err != nil {
		return nil, observability.WrapError(observability.CodeUnknown, "herdr session list", err)
	}
	for i := range list {
		if list[i].Name == name {
			return &list[i], nil
		}
	}
	return nil, nil
}

func (h *Herdr) startNamedServer(ctx context.Context, session string) error {
	if h.StartServer != nil {
		return h.StartServer(ctx, session)
	}
	return startHerdrServer(h.binary(), session)
}

func startHerdrServer(binary, session string) error {
	if err := refuseUndeliverableHerdrSocket(session); err != nil {
		return err
	}
	cmd := herdrServerCommand(binary, session)
	if err := cmd.Start(); err != nil {
		return observability.WrapError(observability.CodeRuntimeUnavailable, "herdr server failed to start", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func refuseUndeliverableHerdrSocket(session string) error {
	home := herdrConfigHomeFromEnv()
	if home == "" {
		return nil
	}
	return runtime.CheckSessionSocket(home, session)
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

func herdrServerCommand(binary, session string) *exec.Cmd {
	cmd := exec.Command(binary, "server", "--session", session)
	cmd.Env = herdrServerEnv()
	applyHerdrServerProcAttr(cmd)
	return cmd
}

func applyHerdrServerProcAttr(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// herdrDefaultRootTabLabel is the label Herdr 0.8.2 assigns to the tab
// created with workspace create (live-captured; not the workspace label).
const herdrDefaultRootTabLabel = "1"

// herdrServerEnv is the environment for a herdr server mate itself
// cold-starts (EnsureSession's startNamedServer, when no session is already
// running). ADR 0007 (G4-04) originally allowlisted a small, fixed set of
// keys "so operator MATE_* / secrets are not inherited" and explicitly
// flagged "whether Herdr needs env keys beyond the server allowlist" as
// unproven.
//
// It is now proven, live, that Herdr does: a herdr server spawned with that
// allowlist forks the login shell for every new pane with the SAME
// restricted environment, and an operator's shell profile can depend on
// variables the allowlist has no way to anticipate (on the machine this was
// diagnosed on, Amazon Q's shell integration - Q_TERM/QTERM_SESSION_ID/
// Q_SET_PARENT_CHECK - whose absence made every freshly created pane's shell
// behave in a way Herdr's own `agent start` permanently refuses as
// agent_pane_busy: not a transient race - proven not to clear after 20s of
// extra warm-up, and not to clear on a second pane in the same session
// either, only on the presence of those specific variables). This defect
// hits the very first `mate start`/onboard against any brand-new workspace,
// because that is exactly when EnsureSession must cold-start a server rather
// than bind one already running (see internal/orchestration TestStartMate*
// and the live evidence in AGENTS.md).
//
// Enumerating every shell integration tool an operator might have installed
// is not viable, so the fix is directional rather than another fixed list:
// inherit the calling process's own environment (which is already just the
// operator's login shell environment - mate itself runs under it) and strip
// only mate's own MATE_* namespace, the one thing ADR 0007 named as
// deliberately not ambient here (it is injected explicitly, per pane, via
// `--env` at tab/workspace create - runtime.AllowlistedEnv - not via the
// server's own startup environment).
func herdrServerEnv() []string {
	env := os.Environ()
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		if refusedServerEnvKey(key) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// refusedServerEnvKey reports whether a caller environment variable must not
// reach the spawned server. Prefix-matching MATE_ rather than listing
// config.IdentityEnvKeys is deliberate: a key added later is refused by
// default, and the failure of a new key leaking is worse than the failure of
// an unrelated MATE_-prefixed one being dropped.
// harness.NestedSessionEnv is refused for the same reason: the server hands
// its environment to every pane, and an agent that starts the server must not
// make every Mate and Crew look like its own child session.
func refusedServerEnvKey(key string) bool {
	return strings.HasPrefix(key, "MATE_") || key == "HERDR_SESSION" || harness.IsNestedSessionEnv(key)
}

func (h *Herdr) waitRunning(ctx context.Context, session string) error {
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for {
		res, err := h.run(ctx, session, []string{"status", "--json"})
		if err == nil {
			st, parseErr := parseStatus(res.Stdout)
			if parseErr == nil && st.Running {
				return nil
			}
			if parseErr != nil {
				lastErr = parseErr
			} else {
				lastErr = observability.NewError(
					observability.CodeRuntimeUnavailable,
					fmt.Sprintf("named herdr session %q is listed but not running", session),
				)
			}
		} else if ctx.Err() == nil || lastErr == nil {
			lastErr = err
		}
		timedOut := time.Now().After(deadline) || ctx.Err() != nil
		if timedOut {
			return waitRunningTimeout(session, lastErr, ctx.Err())
		}
		select {
		case <-ctx.Done():
			return waitRunningTimeout(session, lastErr, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func waitRunningTimeout(session string, lastErr, ctxErr error) error {
	msg := fmt.Sprintf("named herdr session %q did not become running", session)
	if lastErr != nil {
		return observability.WrapError(observability.CodeRuntimeUnavailable, msg, lastErr)
	}
	if ctxErr != nil {
		return ctxErr
	}
	return observability.NewError(observability.CodeRuntimeUnavailable, msg)
}

// EnsureProjectWorkspace implements Adapter. Headless Herdr starts with zero
// workspaces; this never assumes a default layout. Labels are not unique
// (live: two --label Duplicate creates produced w1 and w2 at different cwds),
// so reuse requires label and the root-tab cwd (not the focused/active tab).
func (h *Herdr) EnsureProjectWorkspace(ctx context.Context, spec runtime.WorkspaceSpec) (runtime.WorkspaceHandle, error) {
	if err := runtime.CheckWorkspaceSpec(spec); err != nil {
		return runtime.WorkspaceHandle{}, err
	}
	env, err := runtime.AllowlistedEnv(spec.Env, spec.EnvKeys)
	if err != nil {
		return runtime.WorkspaceHandle{}, err
	}
	found, ok, err := h.LookupProjectWorkspace(ctx, spec)
	if err != nil {
		return runtime.WorkspaceHandle{}, err
	}
	if ok {
		if len(env) > 0 {
			return runtime.WorkspaceHandle{}, observability.NewError(
				observability.CodeUsage,
				"launch environment is injected only when the workspace pane is created (workspace create --env); this workspace already exists",
			)
		}
		h.collapseEmptyProjectWorkspaces(ctx, spec, found.WorkspaceID)
		return found, nil
	}
	args := []string{"workspace", "create", "--label", spec.Label, "--cwd", spec.Cwd, "--no-focus"}
	args = append(args, envFlags(env)...)
	res, err := h.run(ctx, spec.Session.Name, args)
	if err != nil {
		return runtime.WorkspaceHandle{}, err
	}
	created, err := parseWorkspaceCreated(res.Stdout)
	if err != nil {
		return runtime.WorkspaceHandle{}, observability.WrapError(observability.CodeUnknown, "herdr workspace create", err)
	}
	createdHandle := runtime.WorkspaceHandle{
		Session:     spec.Session,
		WorkspaceID: created.WorkspaceID,
		Label:       created.Label,
		Cwd:         created.Cwd,
		RootTab: runtime.TabHandle{
			Session: spec.Session, WorkspaceID: created.WorkspaceID,
			TabID: created.TabID, PaneID: created.PaneID, TerminalID: created.TerminalID,
			Cwd: created.Cwd, Label: created.TabLabel, Env: env,
		},
	}
	// Re-list: a concurrent ensure may have created another workspace with
	// the same label+cwd. Adopt one deterministically and close empty extras
	// so the Project is startable again without a human closing workspaces.
	// Nothing here closes on a pane heuristic: only an unoccupied duplicate
	// of this same label+cwd goes, and only once one has been adopted.
	found, ok, err = h.LookupProjectWorkspace(ctx, spec)
	if err != nil {
		return runtime.WorkspaceHandle{}, err
	}
	if !ok {
		return createdHandle, nil
	}
	h.collapseEmptyProjectWorkspaces(ctx, spec, found.WorkspaceID)
	return found, nil
}

// LookupProjectWorkspace implements Adapter. It never creates.
func (h *Herdr) LookupProjectWorkspace(ctx context.Context, spec runtime.WorkspaceSpec) (runtime.WorkspaceHandle, bool, error) {
	if err := runtime.CheckWorkspaceSpec(spec); err != nil {
		return runtime.WorkspaceHandle{}, false, err
	}
	listed, err := h.listWorkspaces(ctx, spec.Session.Name)
	if err != nil {
		return runtime.WorkspaceHandle{}, false, err
	}
	if len(listed) == 0 {
		return runtime.WorkspaceHandle{}, false, nil
	}
	panes, err := h.listPanes(ctx, spec.Session.Name)
	if err != nil {
		return runtime.WorkspaceHandle{}, false, err
	}
	tabs, err := h.listTabs(ctx, spec.Session.Name, "")
	if err != nil {
		return runtime.WorkspaceHandle{}, false, err
	}
	var matches []listedWorkspace
	for i := range listed {
		w := listed[i]
		if w.Label != spec.Label {
			continue
		}
		if cwd := workspaceRootCwd(w, tabs, panes); cwd != "" && runtime.SamePath(cwd, spec.Cwd) {
			matches = append(matches, w)
		}
	}
	if len(matches) == 0 {
		return runtime.WorkspaceHandle{}, false, nil
	}
	occupied, occErr := h.occupiedWorkspaces(ctx, spec.Session)
	if occErr != nil {
		occupied = nil
	}
	ids := make([]string, 0, len(matches))
	byID := map[string]listedWorkspace{}
	for _, m := range matches {
		ids = append(ids, m.WorkspaceID)
		byID[m.WorkspaceID] = m
	}
	chosen := pickWorkspaceID(ids, occupied)
	return h.workspaceHandle(spec.Session, byID[chosen], tabs, panes), true, nil
}

func (h *Herdr) occupiedWorkspaces(ctx context.Context, session runtime.SessionHandle) (map[string]bool, error) {
	out := map[string]bool{}
	agents, err := h.ListAgents(ctx, session)
	if err != nil {
		return nil, err
	}
	for _, ag := range agents {
		if id := ag.Handle.Tab.WorkspaceID; id != "" {
			out[id] = true
		}
	}
	return out, nil
}

func (h *Herdr) collapseEmptyProjectWorkspaces(ctx context.Context, spec runtime.WorkspaceSpec, keepID string) {
	listed, err := h.listWorkspaces(ctx, spec.Session.Name)
	if err != nil || len(listed) == 0 {
		return
	}
	panes, err := h.listPanes(ctx, spec.Session.Name)
	if err != nil {
		return
	}
	tabs, err := h.listTabs(ctx, spec.Session.Name, "")
	if err != nil {
		return
	}
	occupied, err := h.occupiedWorkspaces(ctx, spec.Session)
	if err != nil {
		return
	}
	for _, w := range listed {
		if w.WorkspaceID == keepID || w.Label != spec.Label {
			continue
		}
		if cwd := workspaceRootCwd(w, tabs, panes); cwd == "" || !runtime.SamePath(cwd, spec.Cwd) {
			continue
		}
		if occupied[w.WorkspaceID] {
			continue
		}
		_, _ = h.run(ctx, spec.Session.Name, []string{"workspace", "close", w.WorkspaceID})
	}
}

func (h *Herdr) listWorkspaces(ctx context.Context, session string) ([]listedWorkspace, error) {
	res, err := h.run(ctx, session, []string{"workspace", "list"})
	if err != nil {
		return nil, err
	}
	list, err := parseWorkspaceList(res.Stdout)
	if err != nil {
		return nil, observability.WrapError(observability.CodeUnknown, "herdr workspace list", err)
	}
	return list, nil
}

// CreateAgentTab implements Adapter. The first tab in a Project workspace is
// the unused root tab from workspace create (label "1" on 0.8.2); that is the
// Mate tab and is renamed. A later call issues `tab create` for Crew. Env on
// the rename path cannot be applied after the fact — set WorkspaceSpec.Env at
// create. TabSpec.Env is applied on the Crew `tab create` path.
func (h *Herdr) CreateAgentTab(ctx context.Context, spec runtime.TabSpec) (runtime.TabHandle, error) {
	if err := runtime.CheckTabSpec(spec); err != nil {
		return runtime.TabHandle{}, err
	}
	env, err := runtime.AllowlistedEnv(spec.Env, spec.EnvKeys)
	if err != nil {
		return runtime.TabHandle{}, err
	}
	session := spec.Workspace.Session.Name
	if existing, pane, ok, err := h.tabByLabel(ctx, spec.Workspace, spec.Label); err != nil {
		return runtime.TabHandle{}, err
	} else if ok {
		if err := h.waitPaneShell(ctx, session, pane.PaneID); err == nil {
			if len(env) > 0 {
				return runtime.TabHandle{}, observability.NewError(
					observability.CodeUsage,
					"launch environment is injected only when the pane is created (tab create --env); this tab already exists",
				)
			}
			if err := checkObservedTabCwd(spec.Cwd, pane.Cwd); err != nil {
				return runtime.TabHandle{}, err
			}
			return runtime.TabHandle{
				Session:     spec.Workspace.Session,
				WorkspaceID: spec.Workspace.WorkspaceID,
				TabID:       existing.TabID,
				PaneID:      pane.PaneID,
				TerminalID:  pane.TerminalID,
				Cwd:         pane.Cwd,
				Label:       spec.Label,
			}, nil
		} else if !paneMissing(err) {
			return runtime.TabHandle{}, err
		}
		// Pane is gone (typical after pane-close). Do not reuse the label.
	}
	if root, pane, ok, err := h.unusedRootTab(ctx, spec.Workspace); err != nil {
		return runtime.TabHandle{}, err
	} else if ok {
		if err := h.waitPaneShell(ctx, session, pane.PaneID); err == nil {
			crewTab := len(env) > 0 && spec.Cwd != "" && pane.Cwd != "" && !runtime.SamePath(pane.Cwd, spec.Cwd)
			if len(env) > 0 && !crewTab {
				return runtime.TabHandle{}, observability.NewError(
					observability.CodeUsage,
					"pane environment is injected by workspace create --env; tab rename cannot apply environment after the pane exists",
				)
			}
			if !crewTab {
				if err := checkObservedTabCwd(spec.Cwd, pane.Cwd); err != nil {
					return runtime.TabHandle{}, err
				}
				if _, err := h.run(ctx, session, []string{"tab", "rename", root.TabID, spec.Label}); err != nil {
					return runtime.TabHandle{}, err
				}
				return runtime.TabHandle{
					Session:     spec.Workspace.Session,
					WorkspaceID: spec.Workspace.WorkspaceID,
					TabID:       root.TabID,
					PaneID:      pane.PaneID,
					TerminalID:  pane.TerminalID,
					Cwd:         pane.Cwd,
					Label:       spec.Label,
					Env:         spec.Workspace.RootTab.Env,
				}, nil
			}
			// Crew: unused root pane stays the Mate slot; create a new tab
			// so --env is applied at pane create.
		} else if !paneMissing(err) {
			// Slow shell, not a missing pane: creating a second tab would
			// leave the root behind after stop (B5 live leftover).
			return runtime.TabHandle{}, err
		}
	}
	args := []string{
		"tab", "create",
		"--workspace", spec.Workspace.WorkspaceID,
		"--label", spec.Label,
		"--cwd", spec.Cwd,
		"--no-focus",
	}
	args = append(args, envFlags(env)...)
	res, err := h.run(ctx, session, args)
	if err != nil {
		return runtime.TabHandle{}, err
	}
	created, err := parseTabCreated(res.Stdout)
	if err != nil {
		return runtime.TabHandle{}, observability.WrapError(observability.CodeUnknown, "herdr tab create", err)
	}
	// The tab is real from this point on. A refusal below must still hand the
	// caller a handle naming it - RemoveTab needs TabID, not Cwd - so a saga
	// can compensate instead of leaking a tab it never learned exists.
	createdHandle := runtime.TabHandle{
		Session:     spec.Workspace.Session,
		WorkspaceID: created.WorkspaceID,
		TabID:       created.TabID,
		PaneID:      created.PaneID,
		TerminalID:  created.TerminalID,
		Cwd:         created.Cwd,
		Label:       spec.Label,
		Env:         env,
	}
	if err := checkObservedTabCwd(spec.Cwd, created.Cwd); err != nil {
		return createdHandle, err
	}
	if err := h.waitPaneShell(ctx, session, created.PaneID); err != nil {
		return createdHandle, err
	}
	return createdHandle, nil
}

// checkObservedTabCwd refuses a tab whose observed pane cwd cannot be trusted
// to satisfy the requested cwd. A missing observation and a mismatched one
// are different diagnoses even though both refuse: an empty cwd means Herdr
// did not tell mate where the pane runs, while a non-matching cwd means
// Herdr told mate and it disagrees with what was asked for. Both leave the
// launch guard in adapter.go unable to prove context delivery, which is what
// this refusal protects (the tab handle must carry what Herdr reported, not
// what was requested, or that guard compares the request against itself).
func checkObservedTabCwd(requested, observed string) error {
	if strings.TrimSpace(observed) == "" {
		return observability.NewError(
			observability.CodeStateConflict,
			fmt.Sprintf("herdr did not report a cwd for this pane; cannot verify the agent will discover its context at %s", requested),
		)
	}
	if !runtime.SamePath(observed, requested) {
		return observability.NewError(
			observability.CodeStateConflict,
			fmt.Sprintf("herdr reports the pane cwd as %s, not the requested %s; refusing to start an agent that would discover the wrong context", observed, requested),
		)
	}
	return nil
}

func (h *Herdr) tabByLabel(ctx context.Context, ws runtime.WorkspaceHandle, label string) (listedTab, listedPane, bool, error) {
	tabs, err := h.listTabs(ctx, ws.Session.Name, ws.WorkspaceID)
	if err != nil {
		return listedTab{}, listedPane{}, false, err
	}
	var found *listedTab
	for i := range tabs {
		if tabs[i].WorkspaceID == ws.WorkspaceID && tabs[i].Label == label {
			if found != nil {
				return listedTab{}, listedPane{}, false, observability.NewError(
					observability.CodeStateConflict,
					fmt.Sprintf("multiple tabs in workspace %s have label %q", ws.WorkspaceID, label),
				)
			}
			found = &tabs[i]
		}
	}
	if found == nil {
		return listedTab{}, listedPane{}, false, nil
	}
	pane, err := h.paneForTab(ctx, ws, found.TabID)
	if err != nil {
		return listedTab{}, listedPane{}, false, err
	}
	return *found, pane, true, nil
}

func (h *Herdr) unusedRootTab(ctx context.Context, ws runtime.WorkspaceHandle) (listedTab, listedPane, bool, error) {
	tabs, err := h.listTabs(ctx, ws.Session.Name, ws.WorkspaceID)
	if err != nil {
		return listedTab{}, listedPane{}, false, err
	}
	var root *listedTab
	for i := range tabs {
		if tabs[i].WorkspaceID == ws.WorkspaceID && tabs[i].Label == herdrDefaultRootTabLabel {
			if root != nil {
				return listedTab{}, listedPane{}, false, nil
			}
			root = &tabs[i]
		}
	}
	if root == nil {
		return listedTab{}, listedPane{}, false, nil
	}
	pane, err := h.paneForTab(ctx, ws, root.TabID)
	if err != nil {
		return listedTab{}, listedPane{}, false, err
	}
	return *root, pane, true, nil
}

func (h *Herdr) paneForTab(ctx context.Context, ws runtime.WorkspaceHandle, tabID string) (listedPane, error) {
	panes, err := h.listPanes(ctx, ws.Session.Name)
	if err != nil {
		return listedPane{}, err
	}
	for _, p := range panes {
		if p.TabID == tabID {
			return p, nil
		}
	}
	if ws.RootTab.PaneID != "" && ws.RootTab.TabID == tabID {
		return listedPane{
			PaneID:      ws.RootTab.PaneID,
			TabID:       ws.RootTab.TabID,
			WorkspaceID: ws.WorkspaceID,
			TerminalID:  ws.RootTab.TerminalID,
			Cwd:         ws.RootTab.Cwd,
		}, nil
	}
	return listedPane{}, observability.NewError(
		observability.CodeUnknown,
		fmt.Sprintf("workspace %s tab %s has no pane", ws.WorkspaceID, tabID),
	)
}

func (h *Herdr) listTabs(ctx context.Context, session, workspaceID string) ([]listedTab, error) {
	args := []string{"tab", "list"}
	if workspaceID != "" {
		args = append(args, "--workspace", workspaceID)
	}
	res, err := h.run(ctx, session, args)
	if err != nil {
		return nil, err
	}
	tabs, err := parseTabList(res.Stdout)
	if err != nil {
		return nil, observability.WrapError(observability.CodeUnknown, "herdr tab list", err)
	}
	return tabs, nil
}

func (h *Herdr) listPanes(ctx context.Context, session string) ([]listedPane, error) {
	res, err := h.run(ctx, session, []string{"pane", "list"})
	if err != nil {
		return nil, err
	}
	panes, err := parsePaneList(res.Stdout)
	if err != nil {
		return nil, observability.WrapError(observability.CodeUnknown, "herdr pane list", err)
	}
	return panes, nil
}

// workspaceRootTabID is the tab Herdr created with the workspace (tab
// number 1 on 0.8.2, or the unused default label "1"). Focus / active_tab_id
// is UI state and is not identity.
func workspaceRootTabID(w listedWorkspace, tabs []listedTab) string {
	var unlabeled string
	for _, t := range tabs {
		if t.WorkspaceID != w.WorkspaceID {
			continue
		}
		if t.Number == 1 {
			return t.TabID
		}
		if unlabeled == "" && t.Label == herdrDefaultRootTabLabel {
			unlabeled = t.TabID
		}
	}
	return unlabeled
}

func workspaceRootCwd(w listedWorkspace, tabs []listedTab, panes []listedPane) string {
	if p, ok := workspaceRootPane(w, tabs, panes); ok {
		return p.Cwd
	}
	return ""
}

func workspaceRootPane(w listedWorkspace, tabs []listedTab, panes []listedPane) (listedPane, bool) {
	root := workspaceRootTabID(w, tabs)
	if root == "" {
		return listedPane{}, false
	}
	for _, p := range panes {
		if p.TabID == root {
			return p, true
		}
	}
	return listedPane{}, false
}

func (h *Herdr) workspaceHandle(session runtime.SessionHandle, w listedWorkspace, tabs []listedTab, panes []listedPane) runtime.WorkspaceHandle {
	hnd := runtime.WorkspaceHandle{Session: session, WorkspaceID: w.WorkspaceID, Label: w.Label, Cwd: workspaceRootCwd(w, tabs, panes)}
	if p, ok := workspaceRootPane(w, tabs, panes); ok {
		hnd.RootTab = runtime.TabHandle{
			Session:     session,
			WorkspaceID: w.WorkspaceID,
			TabID:       p.TabID,
			PaneID:      p.PaneID,
			TerminalID:  p.TerminalID,
			Cwd:         p.Cwd,
		}
	}
	return hnd
}

func paneMissing(err error) bool {
	switch herdrCodeOf(err) {
	case runtime.HerdrPaneNotFound, runtime.HerdrAgentPaneNotFound:
		return true
	}
	return false
}

// StartAgent implements Adapter. agent_not_ready (blocked during startup) is
// a live agent: G4 lab capture left launch_pending=true / blocked, and
// `agent wait --until blocked` then exited 0. That is not a failed start.
func (h *Herdr) StartAgent(ctx context.Context, spec runtime.AgentStartSpec) (runtime.AgentHandle, error) {
	if spec == nil {
		return runtime.AgentHandle{}, observability.NewError(observability.CodeUsage, "start requires a spec from NewAgentStartSpec")
	}
	if err := spec.Validate(); err != nil {
		return runtime.AgentHandle{}, err
	}
	if h.Names == nil {
		return runtime.AgentHandle{}, observability.NewError(observability.CodeUsage, "start requires a live-name registry")
	}
	session := spec.Session()
	if err := h.Names.Reserve(session.Name, spec.Name(), spec.RawID()); err != nil {
		return runtime.AgentHandle{}, err
	}
	argv, err := runtime.AgentStartArgvTimeout(session.Name, spec.Name(), string(spec.Kind()), spec.Tab().PaneID, spec.Timeout(), spec.Launch().Args())
	if err != nil {
		return runtime.AgentHandle{}, err
	}
	// A long-running Herdr server can carry CLAUDE_CONFIG_DIR, and the
	// nested-session variables of whichever Claude Code session started it,
	// from the environment it was started with. The launch must remove those
	// inherited values in this pane (one `unset` for all of them), rather than
	// setting empty values or mutating the server environment shared by other
	// agents. See harness.NestedSessionEnv for the measured failure.
	if keys := spec.Launch().UnsetEnv(); len(keys) > 0 {
		argv := append([]string{"pane", "run", spec.Tab().PaneID, "unset"}, keys...)
		if _, err := h.run(ctx, session.Name, argv); err != nil {
			return runtime.AgentHandle{}, err
		}
	}
	// The launch's own environment (identity keys, the pinned CODEX_HOME, an
	// explicit CLAUDE_CONFIG_DIR) is exported into this pane's shell before
	// every start, the same way the unset above removes what it must not
	// carry. Pane creation is not enough: Herdr applies `--env` only when a
	// pane is made (docs/mvp.md section 7, task 22), and a Mate restarted
	// while a Crew holds its workspace gets a fresh `tab create` that
	// inherits nothing from the workspace (measured 2026-09-24, Herdr 0.8.2:
	// a tab created without --env in a workspace made with --env FOO=ws
	// echoes FOO empty).
	if env, err := runtime.PaneEnv(spec.Launch()); err != nil {
		return runtime.AgentHandle{}, err
	} else if len(env) > 0 {
		argv := append([]string{"pane", "run", spec.Tab().PaneID, "export"}, exportAssignments(env)...)
		if _, err := h.run(ctx, session.Name, argv); err != nil {
			return runtime.AgentHandle{}, err
		}
	}
	handle := runtime.AgentHandle{
		Session: session,
		Name:    spec.Name(),
		RawID:   spec.RawID(),
		Kind:    spec.Kind(),
		Tab:     spec.Tab(),
	}
	// agent_pane_busy is Herdr's own answer to "this pane cannot host an
	// agent yet", and it is the gate mate trusts: waitPaneShell is a
	// heuristic that proceeds when it cannot tell.
	var last error
	for attempt := 0; attempt < paneBusyAttempts; attempt++ {
		_, err = h.run(ctx, session.Name, argv)
		if err == nil {
			return handle, nil
		}
		if herdrCodeOf(err) == runtime.HerdrAgentNotReady {
			if _, inspectErr := h.InspectAgent(ctx, handle); inspectErr == nil {
				return handle, nil
			}
		}
		if herdrCodeOf(err) == runtime.HerdrTimeout {
			aerr := h.adoptTimedOutStart(ctx, handle)
			if aerr == nil {
				return handle, nil
			}
			return runtime.AgentHandle{}, withAdoptReason(err, aerr)
		}
		if herdrCodeOf(err) != runtime.HerdrAgentPaneBusy {
			return runtime.AgentHandle{}, err
		}
		last = err
		select {
		case <-ctx.Done():
			return runtime.AgentHandle{}, ctx.Err()
		case <-time.After(paneBusyRetry):
		}
	}
	if last == nil {
		last = runtime.NewHerdrError(runtime.HerdrAgentPaneBusy, "pane is not an available shell")
	}
	return runtime.AgentHandle{}, h.wrapPaneBusyExhausted(ctx, session.Name, last, handle.Name, spec.Tab().PaneID, paneBusyAttempts, paneBusyRetry)
}

// adoptTimedOutStart names the agent a timed-out `agent start` left in the
// pane. A harness held by a startup dialog never becomes interactive, so
// Herdr 0.9.3 times out with the agent detected but unnamed (measured
// 2026-10-13, codex-cli 0.162.1's hook review). Only one agent of the
// launched kind in this pane, unnamed or already ours, is adopted. A pane
// Herdr lists no agent in yet is polled again, every startAdoptPoll for
// startAdoptWindow, since a slow harness may not be detected by then;
// anything else leaves the start failed at once.
func (h *Herdr) adoptTimedOutStart(ctx context.Context, handle runtime.AgentHandle) error {
	polls := int(startAdoptWindow / startAdoptPoll)
	for attempt := 0; ; attempt++ {
		err := h.adoptOnce(ctx, handle)
		if !errors.Is(err, errAgentNotListed) {
			return err
		}
		if attempt >= polls {
			return fmt.Errorf("%w after %s", err, startAdoptWindow)
		}
		if err := h.adoptSleep(ctx, startAdoptPoll); err != nil {
			return err
		}
	}
}

// adoptOnce is one ListAgents read of adoptTimedOutStart.
func (h *Herdr) adoptOnce(ctx context.Context, handle runtime.AgentHandle) error {
	agents, err := h.ListAgents(ctx, handle.Session)
	if err != nil {
		return err
	}
	var found []runtime.ObservedAgent
	for _, a := range agents {
		if a.Handle.Tab.PaneID == handle.Tab.PaneID {
			found = append(found, a)
		}
	}
	if len(found) == 0 {
		return fmt.Errorf("pane %s: %w", handle.Tab.PaneID, errAgentNotListed)
	}
	if len(found) != 1 || found[0].Handle.Kind != handle.Kind {
		return fmt.Errorf("pane %s holds %d agents, none ours to adopt", handle.Tab.PaneID, len(found))
	}
	switch found[0].Handle.Name {
	case handle.Name:
		return nil
	case "":
		_, err := h.run(ctx, handle.Session.Name, []string{"agent", "rename", handle.Tab.PaneID, handle.Name})
		return err
	default:
		return fmt.Errorf("pane %s holds agent %s", handle.Tab.PaneID, found[0].Handle.Name)
	}
}

func (h *Herdr) adoptSleep(ctx context.Context, d time.Duration) error {
	if h.AdoptSleep != nil {
		return h.AdoptSleep(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// withAdoptReason is the start's own timeout with why the agent in the
// pane could not be adopted: the message says it and details carry it, and
// the error is still the Herdr timeout IsTimeout recognises.
func withAdoptReason(timeout, reason error) error {
	var coded *observability.Error
	if !errors.As(timeout, &coded) {
		return fmt.Errorf("%w; mate could not adopt an agent in the pane: %v", timeout, reason)
	}
	details := map[string]any{}
	for k, v := range coded.Details {
		details[k] = v
	}
	details["adopt_reason"] = reason.Error()
	out := coded.WithDetails(details)
	out.Message += "; mate could not adopt an agent in the pane: " + reason.Error()
	return out
}

// wrapPaneBusyExhausted turns Herdr's raw agent_pane_busy refusal (its own
// wording names only an opaque pane id, e.g. "agent target pane w3:p1 is not
// an available shell") into a message that says what mate was doing, what it
// found in that pane, and what happens next.
//
// It must NOT tell the operator to go inspect the named pane in Herdr: by the
// time this error reaches a caller, orchestration's own reconcileStartFailure
// (internal/orchestration/start.go) has already run InspectAgent (proving no
// agent started), RemoveTab (closing the pane - and, since it is the sole tab
// of a freshly created workspace, the workspace with it) and reverted the Mate
// record - every other outcome of that compensation produces a different
// wrapped error, so whenever this exact text is what a caller sees, the pane is
// already gone. Earlier wording that suggested checking or attaching to the
// pane was actively misleading for that reason.
//
// That is also why the one fact worth having is read *here*, while the pane
// still exists: `pane process-info` names the process Herdr refused to treat as
// a shell, which is the difference between "check the session's health" and
// "your shell startup re-executes the login shell inside a wrapper". One read,
// after the budget is spent - the retry loop is the hot path and must not pay
// for a diagnostic on every attempt - and a failed read degrades the message
// rather than replacing the refusal, because the pane-busy state_conflict is
// what happened and the inspection failure is a detail of it.
//
// The observation leads and the advice follows: the Console draws this on one
// line and cuts it at the terminal edge, so an 80-column reader has to get the
// pane and its occupant before anything else.
//
// It keeps the mapped Code (state_conflict) and Herdr's own message and pane
// id in Details, so nothing observable is lost.
func (h *Herdr) wrapPaneBusyExhausted(ctx context.Context, session string, last error, agentName, paneID string, attempts int, retry time.Duration) error {
	var coded *observability.Error
	if !errors.As(last, &coded) || coded.Code != observability.CodeStateConflict {
		return last
	}
	budget := time.Duration(attempts) * retry
	occupant, inspectErr := h.paneOccupant(ctx, session, paneID)
	extra := map[string]any{"pane_id": paneID, "herdr_message": coded.Message, "waited": budget.String()}
	var found string
	switch {
	case inspectErr != nil:
		extra["pane_process_error"] = inspectErr.Error()
		found = fmt.Sprintf("pane %s never reached an interactive shell prompt (mate could not inspect it: %v)", paneID, inspectErr)
	case occupant == "":
		found = fmt.Sprintf("pane %s never reached an interactive shell prompt (Herdr reported no process running in it)", paneID)
	default:
		extra["pane_process"] = occupant
		found = fmt.Sprintf("pane %s was running %q, not a shell prompt Herdr will start an agent in", paneID, occupant)
	}
	msg := fmt.Sprintf(
		"%s: mate spent %s retrying agent %s (Herdr's own answer: %q) and has already removed the pane and tab it created "+
			"for this attempt, so there is nothing left there to inspect. A shell integration in your shell startup files "+
			"that re-executes the login shell inside its own wrapper makes every new pane refuse this way; otherwise check "+
			"the Herdr session's health rather than this pane, since a retry creates an entirely new pane and hits the same "+
			"%s budget.",
		found, budget, agentName, coded.Message, budget,
	)
	wrapped := observability.WrapError(observability.CodeStateConflict, msg, last)
	details := make(map[string]any, len(coded.Details)+len(extra))
	for k, v := range coded.Details {
		details[k] = v
	}
	for k, v := range extra {
		details[k] = v
	}
	return wrapped.WithDetails(details)
}

// paneOccupant reports what Herdr sees running in a pane, for diagnostics
// only. An empty name with a nil error means Herdr reported no foreground
// process, which is a different fact from "mate could not ask".
func (h *Herdr) paneOccupant(ctx context.Context, session, pane string) (string, error) {
	res, err := h.run(ctx, session, []string{"pane", "process-info", "--pane", pane})
	if err != nil {
		return "", err
	}
	info, err := parsePaneProcessInfo(res.Stdout)
	if err != nil {
		return "", err
	}
	return strings.Join(info.Foreground, ", "), nil
}

// waitPaneShell gives a freshly created pane a moment to draw a shell prompt.
// Live capture: tab create returns a pane with no terminal_title (revision 0);
// about a second later the title is a shell prompt and agent start succeeds,
// while an immediate start can be agent_pane_busy ("not an available shell").
//
// The title is a *positive* signal only. Its absence does not prove the pane
// cannot host an agent: a live shell blocked on an interactive startup prompt
// never draws a title at all, and `agent start` on such a pane is accepted by
// Herdr rather than refused (lab evidence in ADR 0011: a pane whose zsh sat on
// "[oh-my-zsh] Would you like to update? [Y/n]" stayed titleless indefinitely
// at revision 0, and `agent start` engaged it). Treating a missing title as a
// failure cost a whole gate's worth of starts, because callers closed and
// recreated the workspace on it.
//
// So: a title returns nil early, a real `pane get` failure (a missing pane
// above all, which callers do act on) is returned, and an expired budget
// returns nil. `herdr agent start` is the authority on whether a pane can host
// an agent, and StartAgent retries its agent_pane_busy.
func (h *Herdr) waitPaneShell(ctx context.Context, session, paneID string) error {
	if strings.TrimSpace(paneID) == "" {
		return observability.NewError(observability.CodeUsage, "pane wait requires a pane id")
	}
	deadline := time.Now().Add(h.paneShellBudget())
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		res, err := h.run(ctx, session, []string{"pane", "get", paneID})
		if err != nil {
			return err
		}
		info, err := parsePaneInfo(res.Stdout)
		if err != nil {
			return observability.WrapError(observability.CodeUnknown, "herdr pane get", err)
		}
		if strings.TrimSpace(info.TerminalTitle) != "" {
			return nil
		}
		if time.Now().After(deadline) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// RunInPane implements PaneRunner with `herdr pane run`, which types the
// command into the pane's shell.
func (h *Herdr) RunInPane(ctx context.Context, tab runtime.TabHandle, argv []string) error {
	if strings.TrimSpace(tab.Session.Name) == "" || strings.TrimSpace(tab.PaneID) == "" {
		return observability.NewError(observability.CodeUsage, "pane run requires a named session and a pane")
	}
	if len(argv) == 0 {
		return observability.NewError(observability.CodeUsage, "pane run requires a program")
	}
	words := make([]string, 0, len(argv))
	for _, a := range argv {
		words = append(words, shellQuote(a))
	}
	_, err := h.run(ctx, tab.Session.Name, append([]string{"pane", "run", tab.PaneID}, words...))
	return err
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// InspectAgent implements Adapter.
func (h *Herdr) InspectAgent(ctx context.Context, handle runtime.AgentHandle) (runtime.ObservedAgent, error) {
	res, err := h.run(ctx, handle.Session.Name, []string{"agent", "get", handle.Name})
	if err != nil {
		return runtime.ObservedAgent{}, err
	}
	info, err := parseAgentInfo(res.Stdout)
	if err != nil {
		return runtime.ObservedAgent{}, observability.WrapError(observability.CodeUnknown, "herdr agent get", err)
	}
	return h.observed(handle, info), nil
}

// ReadAgent implements Adapter. Herdr owns the bounded snapshot; mate never
// reads a pane directly.
func (h *Herdr) ReadAgent(ctx context.Context, handle runtime.AgentHandle, source harness.ReadSource, lines int) (string, error) {
	if strings.TrimSpace(handle.Session.Name) == "" || strings.TrimSpace(handle.Name) == "" {
		return "", observability.NewError(observability.CodeUsage, "agent read requires a named session and agent")
	}
	if lines <= 0 {
		return "", observability.NewError(observability.CodeUsage, "agent read lines must be positive")
	}
	return h.readAgent(ctx, handle, source, lines, "text")
}

// ReadAgentStyled implements Adapter as the same read in `--format ansi`.
func (h *Herdr) ReadAgentStyled(ctx context.Context, handle runtime.AgentHandle, source harness.ReadSource, lines int) (string, error) {
	if strings.TrimSpace(handle.Session.Name) == "" || strings.TrimSpace(handle.Name) == "" {
		return "", observability.NewError(observability.CodeUsage, "agent read requires a named session and agent")
	}
	if lines <= 0 {
		return "", observability.NewError(observability.CodeUsage, "agent read lines must be positive")
	}
	return h.readAgent(ctx, handle, source, lines, "ansi")
}

// readAgent reads the agent's pane through the harness's source.
//
// A recent-unwrapped read falls back, while Herdr refuses it because a
// harness drawn on the alternate screen is working (HerdrAgentNotIdle, which
// is Codex for its whole turn), to the screen as it is drawn, which is the
// read that refusal itself names. The visible screen is the bottom of the
// same pane, rows wrapped at the pane width; it still carries the composer
// and the in-flight line a caller decides from.
func (h *Herdr) readAgent(ctx context.Context, handle runtime.AgentHandle, source harness.ReadSource, lines int, format string) (string, error) {
	read := func(source harness.ReadSource) (process.Result, error) {
		return h.run(ctx, handle.Session.Name, []string{
			"agent", "read", handle.Name, "--source", string(source),
			"--lines", fmt.Sprintf("%d", lines), "--format", format,
		})
	}
	var res process.Result
	var err error
	switch source {
	case harness.ReadRecentUnwrapped:
		res, err = read(source)
		if herdrCodeOf(err) == runtime.HerdrAgentNotIdle {
			res, err = read(harness.ReadVisible)
		}
	case harness.ReadVisible:
		res, err = read(source)
	default:
		return "", observability.NewError(observability.CodeUsage,
			fmt.Sprintf("agent read source %q is not one mate reads", source))
	}
	if err != nil {
		return "", err
	}
	return string(res.Stdout), nil
}

// ListAgents implements Adapter. The inventory is `agent list` of the named
// session. Occupancy is answered by name and recorded pane id, never by
// which tab is focused.
func (h *Herdr) ListAgents(ctx context.Context, session runtime.SessionHandle) ([]runtime.ObservedAgent, error) {
	if strings.TrimSpace(session.Name) == "" {
		return nil, observability.NewError(observability.CodeUsage, "agent list requires a named session")
	}
	res, err := h.run(ctx, session.Name, []string{"agent", "list"})
	if err != nil {
		return nil, err
	}
	infos, err := parseAgentList(res.Stdout)
	if err != nil {
		return nil, observability.WrapError(observability.CodeUnknown, "herdr agent list", err)
	}
	out := make([]runtime.ObservedAgent, 0, len(infos))
	for _, info := range infos {
		handle := runtime.AgentHandle{
			Session: session,
			Name:    info.Name,
			Kind:    harness.Kind(info.Kind),
			Tab: runtime.TabHandle{
				Session:     session,
				WorkspaceID: info.WorkspaceID,
				TabID:       info.TabID,
				PaneID:      info.PaneID,
				TerminalID:  info.TerminalID,
				Cwd:         info.Cwd,
			},
		}
		out = append(out, h.observed(handle, info))
	}
	return out, nil
}

// WaitAgent implements Adapter. A wait that matches blocked is success
// (live: `agent wait mate-g4-01 --until blocked --timeout 2000` exit 0).
func (h *Herdr) WaitAgent(ctx context.Context, handle runtime.AgentHandle, until runtime.WaitCondition) (runtime.ObservedAgent, error) {
	if err := runtime.CheckWaitTimeout(until.Timeout); err != nil {
		return runtime.ObservedAgent{}, err
	}
	args := []string{"agent", "wait", handle.Name}
	for _, s := range until.Until {
		args = append(args, "--until", string(s))
	}
	args = append(args, "--timeout", fmt.Sprintf("%d", until.Timeout.Milliseconds()))
	res, err := h.run(ctx, handle.Session.Name, args)
	if err != nil {
		return runtime.ObservedAgent{}, err
	}
	info, err := parseAgentInfo(res.Stdout)
	if err != nil {
		return runtime.ObservedAgent{}, observability.WrapError(observability.CodeUnknown, "herdr agent wait", err)
	}
	obs := h.observed(handle, info)
	if !until.Matches(obs.Status) {
		// Herdr already matched; if our condition disagrees, still return the
		// observation — do not invent a timeout the CLI did not report.
		return obs, nil
	}
	return obs, nil
}

// PromptAgent implements Adapter. agent_blocked here means the prompt was
// refused (requires interactive input). That is distinct from WaitAgent
// matching blocked, which is success.
func (h *Herdr) PromptAgent(ctx context.Context, handle runtime.AgentHandle, text string) error {
	_, err := h.run(ctx, handle.Session.Name, []string{"agent", "prompt", handle.Name, text})
	return err
}

// SendKeys implements Adapter as `herdr agent send-keys <name> <keys...>`:
// the press is addressed to the agent by name and Herdr resolves the name to
// its current pane inside that one request, so there is no window between
// looking a pane up and pressing into it during which the agent could stop
// or move and a new occupant receive the key (PR 96 counter-review B2). A
// name Herdr no longer knows - including one whose pane was just closed -
// answers agent_not_found and nothing is pressed (measured 2026-09-14). One
// call per press, so a caller can re-read the screen between a selection and
// its confirmation. The residual is Herdr's own name resolution: a different
// agent started under this exact name inside that request would receive the
// press, which mate's per-launch name allocation makes a collision, not a
// reuse.
func (h *Herdr) SendKeys(ctx context.Context, handle runtime.AgentHandle, keys []string) error {
	if strings.TrimSpace(handle.Session.Name) == "" || strings.TrimSpace(handle.Name) == "" {
		return observability.NewError(observability.CodeUsage, "send-keys requires a named session and agent")
	}
	if err := runtime.CheckSendKeys(keys); err != nil {
		return err
	}
	argv := append([]string{"agent", "send-keys", handle.Name}, keys...)
	_, err := h.run(ctx, handle.Session.Name, argv)
	return err
}

// SendText pastes one line through `herdr pane send-text`. Claude and Codex
// enable bracketed paste; explicit boundaries keep a delayed Codex consumer
// from treating the subsequent Enter as part of its heuristic paste burst.
// Validate the caller's text before adding our own terminal control bytes.
//
// Herdr addresses literal text by *pane*, not by agent name the way
// send-keys does, so the pane is resolved live from `agent get <name>`
// inside this call rather than taken from the caller's recorded handle: a
// persisted pane id can belong to somebody else by now (docs/mvp.md section
// 7, "do not trust an old handle"). A recorded pane that disagrees with the
// live one is refused instead of typed into. The residual race is the window
// between the resolve and the send, which is Herdr's own and no narrower
// than what `herdr pane send-text` offers.
func (h *Herdr) SendText(ctx context.Context, handle runtime.AgentHandle, text string) error {
	if strings.TrimSpace(handle.Session.Name) == "" || strings.TrimSpace(handle.Name) == "" {
		return observability.NewError(observability.CodeUsage, "send-text requires a named session and agent")
	}
	if err := runtime.ValidateSendText(text); err != nil {
		return err
	}
	obs, err := h.InspectAgent(ctx, handle)
	if err != nil {
		return err
	}
	pane := strings.TrimSpace(obs.Handle.Tab.PaneID)
	if pane == "" {
		return observability.NewError(observability.CodeNotFound,
			fmt.Sprintf("agent %s occupies no pane; nothing to type into", handle.Name))
	}
	if recorded := strings.TrimSpace(handle.Tab.PaneID); recorded != "" && recorded != pane {
		return observability.NewError(observability.CodeStateConflict,
			fmt.Sprintf("agent %s is in pane %s, not the recorded pane %s; refusing to type into a pane that moved", handle.Name, pane, recorded))
	}
	_, err = h.run(ctx, handle.Session.Name, []string{"pane", "send-text", pane, "\x1b[200~" + text + "\x1b[201~"})
	return err
}

// AttachAgent implements Adapter. Interactive attach is a TTY handoff, not a
// captured ProcessRunner.Run.
//
// Both live cases are real and the adapter detects which one it is instead of
// assuming: a pane running an agent attaches with `herdr agent attach <name>`,
// a pane with no agent with `herdr terminal attach <terminal_id>`. Detection
// asks Herdr; an explicit AttachTarget.Mode is the caller's decision.
//
// The returned AttachedTo describes what was actually attached, read live. A
// caller must report that rather than the handle it passed in: the recorded
// agent name may be dead and the recorded pane may belong to someone else.
//
// Detach is the shared config binding DetachKey and does not stop the agent
// (ADR 0003, re-verified live for G4-07). Ghostty-native TTY restore after the
// handoff remains unproven (AssumptionGhosttyTTYRestore); nothing here claims
// it.
func (h *Herdr) AttachAgent(ctx context.Context, handle runtime.AgentHandle, target runtime.AttachTarget) (runtime.AttachedTo, error) {
	// The terminal is the one precondition that cannot be satisfied by
	// anything Herdr says, so it is checked before any probe: a caller with
	// no terminal cannot use the answers.
	if h.AttachPreflight != nil {
		if err := h.AttachPreflight(ctx); err != nil {
			return runtime.AttachedTo{}, err
		}
	}
	attached, argv, err := h.attachArgv(ctx, handle, target)
	if err != nil {
		return runtime.AttachedTo{}, err
	}
	if h.Attach == nil {
		return runtime.AttachedTo{}, observability.NewError(
			observability.CodeUnknown,
			"attach is a TTY handoff; this adapter was built without one (see runtime.TTYHandoff)",
		).WithDetails(map[string]any{"argv": argv})
	}
	if err := h.Attach(ctx, argv); err != nil {
		return runtime.AttachedTo{}, err
	}
	return attached, nil
}

// attachArgv resolves the attach command for handle, and what that command
// will connect to. It runs before the terminal is handed over, so a target
// that cannot be attached is a JSON error rather than a half-entered alt
// screen.
func (h *Herdr) attachArgv(ctx context.Context, handle runtime.AgentHandle, target runtime.AttachTarget) (runtime.AttachedTo, []string, error) {
	session := handle.Session.Name
	switch target.Mode {
	case runtime.AttachTerminal:
		pane, err := h.attachTerminalPane(ctx, handle)
		if err != nil {
			return runtime.AttachedTo{}, nil, err
		}
		return runtime.AttachedTo{Mode: runtime.AttachTerminal, PaneID: pane.PaneID, TerminalID: pane.TerminalID},
			WithSession(session, []string{"terminal", "attach", pane.TerminalID}), nil
	case runtime.AttachAgent:
		if strings.TrimSpace(handle.Name) == "" {
			return runtime.AttachedTo{}, nil, observability.NewError(observability.CodeUsage, "agent attach requires an agent name")
		}
		// No PaneID: an explicit agent attach skips the probe by the caller's
		// choice, so nothing here observed which pane the agent is in. A
		// recorded one would be reported as if it had been.
		return runtime.AttachedTo{Mode: runtime.AttachAgent, AgentName: handle.Name},
			WithSession(session, []string{"agent", "attach", handle.Name}), nil
	case runtime.AttachDetect:
		return h.detectAttachArgv(ctx, handle)
	default:
		return runtime.AttachedTo{}, nil, observability.NewError(observability.CodeUsage,
			fmt.Sprintf("unknown attach mode %q", string(target.Mode)))
	}
}

// detectAttachArgv asks Herdr what the target actually is.
//
// The recorded agent name being live is the agent case, and the observation
// also supplies the live pane, so the outcome never repeats a stale handle.
//
// The recorded name *not* being live is not on its own the terminal case. It
// only says the name is gone; the recorded pane may have been taken by a
// different agent since, and terminal-attaching there would stream that
// agent's session under the dead name. So the pane is checked for an
// occupant, and only a genuinely agentless pane is attached as a terminal
// (PR #9 counter-review, B1).
func (h *Herdr) detectAttachArgv(ctx context.Context, handle runtime.AgentHandle) (runtime.AttachedTo, []string, error) {
	session := handle.Session.Name
	if strings.TrimSpace(handle.Name) != "" {
		obs, err := h.InspectAgent(ctx, handle)
		if err == nil {
			return runtime.AttachedTo{
					Mode:      runtime.AttachAgent,
					AgentName: obs.Handle.Name,
					PaneID:    obs.Handle.Tab.PaneID,
				},
				WithSession(session, []string{"agent", "attach", obs.Handle.Name}), nil
		}
		if herdrCodeOf(err) != runtime.HerdrAgentNotFound {
			return runtime.AttachedTo{}, nil, err
		}
		if strings.TrimSpace(handle.Tab.PaneID) == "" && strings.TrimSpace(handle.Tab.TerminalID) == "" {
			// No agent and no pane: there is nothing left to attach to, and
			// Herdr's own answer is the honest one to report.
			return runtime.AttachedTo{}, nil, err
		}
	}
	pane, err := h.attachTerminalPane(ctx, handle)
	if err != nil {
		return runtime.AttachedTo{}, nil, err
	}
	return runtime.AttachedTo{Mode: runtime.AttachTerminal, PaneID: pane.PaneID, TerminalID: pane.TerminalID},
		WithSession(session, []string{"terminal", "attach", pane.TerminalID}), nil
}

// attachTerminalPane reads the pane live and refuses one that hosts an agent.
//
// Two reasons the pane is read rather than trusted. A persisted terminal id
// may name someone else's terminal, because closed Herdr ids are
// unproven-not-reused (AssumptionClosedIDReuse). And a pane that has since
// been given to another agent must not be streamed as if it were a bare
// shell: `pane get` reports `agent` and a non-unknown `agent_status` exactly
// then, which is the signal this refuses on.
func (h *Herdr) attachTerminalPane(ctx context.Context, handle runtime.AgentHandle) (paneInfo, error) {
	pane := strings.TrimSpace(handle.Tab.PaneID)
	if pane == "" {
		if id := strings.TrimSpace(handle.Tab.TerminalID); id != "" {
			// No pane to verify against. The caller named a terminal
			// directly, which is only reachable through an explicit
			// AttachTerminal; detection never gets here without a pane.
			return paneInfo{TerminalID: id}, nil
		}
		return paneInfo{}, observability.NewError(observability.CodeUsage, "terminal attach requires a pane or terminal id")
	}
	res, err := h.run(ctx, handle.Session.Name, []string{"pane", "get", pane})
	if err != nil {
		return paneInfo{}, err
	}
	info, err := parsePaneInfo(res.Stdout)
	if err != nil {
		return paneInfo{}, observability.WrapError(observability.CodeUnknown, "herdr pane get", err)
	}
	if paneHasAgent(info) {
		return paneInfo{}, h.paneOccupiedError(ctx, handle, info)
	}
	if strings.TrimSpace(info.TerminalID) == "" {
		return paneInfo{}, observability.NewError(observability.CodeStateConflict,
			fmt.Sprintf("pane %s has no terminal to attach to", pane))
	}
	return info, nil
}

// paneHasAgent reports whether Herdr says this pane currently hosts an agent.
// Live 0.8.2: an agentless pane omits `agent` and reports
// `agent_status: unknown`; an occupied one emits `"agent":"claude"` and a
// real status.
func paneHasAgent(info paneInfo) bool {
	if strings.TrimSpace(info.Agent) != "" {
		return true
	}
	status := strings.TrimSpace(info.AgentStatus)
	return status != "" && runtime.AgentStatus(status) != runtime.AgentUnknown
}

// paneOccupiedError names the agent that actually holds the pane, so the
// refusal tells the operator what to attach to instead. The name is a best
// effort: if `agent list` cannot supply it the refusal still stands, because
// an unidentified occupant is still not the agent that was asked for.
func (h *Herdr) paneOccupiedError(ctx context.Context, handle runtime.AgentHandle, info paneInfo) error {
	occupant := h.agentNameInPane(ctx, handle.Session.Name, info.PaneID)
	details := map[string]any{
		"pane":            info.PaneID,
		"recorded_agent":  handle.Name,
		"pane_agent_kind": info.Agent,
	}
	if occupant != "" {
		details["pane_agent"] = occupant
		return observability.NewError(observability.CodeStateConflict,
			fmt.Sprintf("pane %s now hosts agent %q, not %q; attach that agent by name, or repair the binding", info.PaneID, occupant, handle.Name)).
			WithDetails(details)
	}
	return observability.NewError(observability.CodeStateConflict,
		fmt.Sprintf("pane %s hosts an agent that is not %q; refusing to attach to it", info.PaneID, handle.Name)).
		WithDetails(details)
}

// agentNameInPane returns the live agent occupying pane, or "" when Herdr
// cannot say. It is only used to make a refusal readable and never to decide
// one.
func (h *Herdr) agentNameInPane(ctx context.Context, session, pane string) string {
	res, err := h.run(ctx, session, []string{"agent", "list"})
	if err != nil {
		return ""
	}
	agents, err := parseAgentList(res.Stdout)
	if err != nil {
		return ""
	}
	for _, a := range agents {
		if a.PaneID == pane {
			return a.Name
		}
	}
	return ""
}

// StopAgent implements Adapter. There is no `herdr agent stop`. A graceful
// stop is `agent prompt <exit>` with the exit prompt the harness declared
// (Claude's `/exit`, G1), after `agent send-keys` of the keys it declared to
// clear its composer first (pi's ctrl+u). Force is pane close. When to stop
// (stop-before-switch) is G4-06.
func (h *Herdr) StopAgent(ctx context.Context, handle runtime.AgentHandle, stop runtime.Stop) error {
	switch stop.Mode() {
	case runtime.StopModeGraceful:
		clear, exit, err := stop.ExitPrompt()
		if err != nil {
			return err
		}
		if len(clear) > 0 {
			if err := h.SendKeys(ctx, handle, clear); err != nil && herdrCodeOf(err) != runtime.HerdrAgentNotFound {
				return err
			}
		}
		if err := h.PromptAgent(ctx, handle, exit); err != nil && herdrCodeOf(err) != runtime.HerdrAgentNotFound {
			return err
		}
		_, err = h.InspectAgent(ctx, handle)
		if err != nil && herdrCodeOf(err) == runtime.HerdrAgentNotFound {
			if h.Names != nil {
				h.Names.Release(handle.Session.Name, handle.Name)
			}
			return nil
		}
		if err != nil {
			return err
		}
		return observability.NewError(observability.CodeUnknown, fmt.Sprintf("graceful %s did not end the agent", exit))
	case runtime.StopModeForce:
		if strings.TrimSpace(handle.Tab.PaneID) == "" {
			return observability.NewError(observability.CodeUsage, "force stop requires a pane id")
		}
		if _, err := h.run(ctx, handle.Session.Name, []string{"pane", "close", handle.Tab.PaneID}); err != nil {
			return err
		}
		if h.Names != nil {
			h.Names.Release(handle.Session.Name, handle.Name)
		}
		return nil
	default:
		return observability.NewError(observability.CodeUsage, "stop is neither graceful nor force")
	}
}

// RemoveTab implements Adapter.
func (h *Herdr) RemoveTab(ctx context.Context, handle runtime.TabHandle) error {
	if strings.TrimSpace(handle.TabID) == "" {
		return observability.NewError(observability.CodeUsage, "tab close requires a tab id")
	}
	_, err := h.run(ctx, handle.Session.Name, []string{"tab", "close", handle.TabID})
	return err
}

func (h *Herdr) observed(handle runtime.AgentHandle, info agentInfo) runtime.ObservedAgent {
	live := true
	if handle.Tab.PaneID != "" && info.PaneID != "" && handle.Tab.PaneID != info.PaneID {
		live = false
	}
	out := handle
	if info.Name != "" {
		out.Name = info.Name
	}
	if info.PaneID != "" {
		out.Tab.PaneID = info.PaneID
	}
	if info.TabID != "" {
		out.Tab.TabID = info.TabID
	}
	if info.TerminalID != "" {
		out.Tab.TerminalID = info.TerminalID
	}
	if info.WorkspaceID != "" {
		out.Tab.WorkspaceID = info.WorkspaceID
	}
	if out.Kind == "" && info.Kind != "" {
		out.Kind = harness.Kind(info.Kind)
	}
	return runtime.ObservedAgent{
		Handle:        out,
		SessionRef:    info.SessionRef,
		Cwd:           info.Cwd,
		Status:        info.Status,
		LaunchPending: info.LaunchPending,
		ObservedAt:    h.now(),
		LiveHandleOK:  live,
	}
}

// exportAssignments renders vars as the words of one POSIX `export` typed
// into a pane's shell: KEY='value', with a single quote in the value closed,
// escaped and reopened, so a path with spaces or quotes arrives whole.
// AllowlistedEnv has already refused keys and values that could not be one
// assignment.
func exportAssignments(vars []runtime.EnvVar) []string {
	out := make([]string, 0, len(vars))
	for _, v := range vars {
		out = append(out, v.Key+"='"+strings.ReplaceAll(v.Value, "'", `'\''`)+"'")
	}
	return out
}

func envFlags(vars []runtime.EnvVar) []string {
	out := make([]string, 0, len(vars)*2)
	for _, v := range vars {
		out = append(out, "--env", v.Key+"="+v.Value)
	}
	return out
}
