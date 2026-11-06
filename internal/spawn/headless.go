package spawn

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// A headless Mate (CON-78) runs `mate mate host <project>` instead of the
// harness's TUI, as a background process of its own outside Herdr
// (CON-225, hostproc.go): no tab, no pane, no Herdr agent. Everything up
// to the launch is a normal start: the manual, the settings and hooks, the
// launch argv. The host is handed that launch and runs it once per message
// (internal/matehost).
//
// A host an older binary started in the Mate's Herdr pane still runs
// there, and its meta still records the pane: Herdr answers for it, and
// its stop closes the pane, until the restart that brings it back as a
// process of its own.

// HostStopWait is how long a stop lets a headless Mate finish its turn.
const HostStopWait = 2 * time.Minute

// headlessMode decides a start's mode, first match wins:
//
//  1. the request's Mode (`mate mate start --mode`);
//  2. project.yaml's `mate.mode`, or the older `mate.headless: true`;
//  3. the mode the last run of this Mate recorded in mate.meta (a stop
//     keeps it);
//  4. fallback, Deps.DefaultMode: headless in the binary since 2026-10-15.
//
// A harness without headless turns runs in the pane whatever step 2 to 4
// chose, since a captain who switched harness asked for nothing else; only
// step 1 asking for headless is refused. A HeadlessOnly request with no
// Mode runs headless when the harness can, whatever step 2 to 4 chose;
// when that overrides `mate.mode: pane`, note says so for the start's
// result (CON-220).
func headlessMode(req StartRequest, prior map[string]string, cfg store.MateConfig, fallback string, canHeadless bool) (headless bool, note string, err error) {
	switch req.Mode {
	case ModeHeadless:
		if !canHeadless {
			return false, "", errNoHeadless
		}
		return true, "", nil
	case ModePane:
		return false, "", nil
	case "":
	default:
		return false, "", errUsage(fmt.Sprintf("unknown Mate mode %q (want %s or %s)", req.Mode, ModePane, ModeHeadless))
	}
	switch cfg.Mode {
	case ModePane, ModeHeadless, "":
	default:
		return false, "", errUsage(fmt.Sprintf("project.yaml mate.mode %q is not a Mate mode (want %s or %s)", cfg.Mode, ModePane, ModeHeadless))
	}
	if req.HeadlessOnly {
		if canHeadless && cfg.Mode == ModePane {
			note = headlessOnlyNote
		}
		return canHeadless, note, nil
	}
	mode := cfg.Mode
	if mode == "" && cfg.Headless {
		mode = ModeHeadless
	}
	if mode == "" {
		mode = prior[MetaMode]
	}
	if mode == "" {
		mode = fallback
	}
	return mode == ModeHeadless && canHeadless, "", nil
}

// headlessOnlyNote is StartResult.ModeNote when a HeadlessOnly start runs
// headless over project.yaml's `mate.mode: pane`.
// Short, and printed first, because the console's status line is one row.
const headlessOnlyNote = "project.yaml's mate.mode is pane, but nothing here can show a pane: running headless"

// errNoHeadless refuses `--mode headless` on a harness that cannot run one.
var errNoHeadless = errUsage("this harness cannot run a headless Mate: it has no headless turns (harness.HeadlessTurns)")

// startHost starts the host as a process of its own, waits until it
// answers on its socket, and records mate.meta: the host's pid and socket,
// no pane or tab (CON-225). A failure after the process started ends it
// and leaves no meta, as a pane start's compensate does.
func startHost(ctx context.Context, w *store.Workspace, deps Deps, project string, profile harness.Profile, mateDir string, decision resumeDecision, prep harness.Prepared, servers []harness.MCPServer, session runtime.SessionHandle) (StartResult, error) {
	agent, err := runtime.SanitizeAgentName(AgentNamePrefix, project)
	if err != nil {
		return StartResult{}, err
	}
	hostLaunch, launch, err := buildHostLaunch(ctx, w, deps, project, profile, mateDir, decision, prep, servers, session, agent)
	if err != nil {
		return StartResult{}, err
	}
	if err := matehost.WriteLaunch(mateDir, hostLaunch); err != nil {
		return StartResult{}, err
	}
	binary, err := deps.binary()
	if err != nil {
		return StartResult{}, err
	}
	env, err := mateEnv(deps.Harnesses, project, session)
	if err != nil {
		return StartResult{}, err
	}
	launchedAt := deps.now()
	pid, exited, err := deps.hosts().StartHost(HostSpec{
		Argv: HostArgv(binary, w.Root(), project),
		Dir:  mateDir,
		Env:  hostEnv(os.Environ(), env),
		Log:  matehost.LogPath(mateDir),
	})
	if err != nil {
		return StartResult{}, err
	}
	res, err := waitAndRecordHost(ctx, w, deps, project, profile.Kind(), mateDir, decision, session, agent, pid, exited, hostLaunch.SessionID, launchedAt, launch)
	if err != nil {
		compensateHost(ctx, w, deps, project, pid)
		return StartResult{}, err
	}
	return res, nil
}

// HostArgv is the command a headless Mate's host runs as.
func HostArgv(binary, root, project string) []string {
	return []string{binary, "mate", "host", project, "--workspace", root, "--no-repl"}
}

func waitAndRecordHost(ctx context.Context, w *store.Workspace, deps Deps, project string, kind harness.Kind, mateDir string, decision resumeDecision, session runtime.SessionHandle, agent string, pid int, exited <-chan struct{}, sessionID string, launchedAt time.Time, launch harness.LaunchSpec) (StartResult, error) {
	if err := waitHostUp(ctx, deps, mateDir, pid, exited); err != nil {
		return StartResult{}, err
	}
	return recordHeadless(w, deps, project, kind, mateDir, decision, session, agent, hostPlace{pid: pid}, sessionID, launchedAt, launch)
}

// compensateHost undoes a host start that failed after the process was
// started: the host is ended and mate.meta removed. Its own failures are
// swallowed: the caller must see why the start was refused.
func compensateHost(ctx context.Context, w *store.Workspace, deps Deps, project string, pid int) {
	ctx, cancel := cleanupContext(ctx)
	defer cancel()
	_, _ = endHost(ctx, w, deps, project, pid, pingHost(ctx, w.MateDir(project)) == nil)
	_ = os.Remove(w.MateMeta(project))
}

// buildHostLaunch is the launch the host of agent runs each message with:
// the argv a pane launch of the harness would use, and its session id.
func buildHostLaunch(ctx context.Context, w *store.Workspace, deps Deps, project string, profile harness.Profile, mateDir string, decision resumeDecision, prep harness.Prepared, servers []harness.MCPServer, session runtime.SessionHandle, agent string) (matehost.Launch, harness.LaunchSpec, error) {
	env, err := mateEnv(deps.Harnesses, project, session)
	if err != nil {
		return matehost.Launch{}, harness.LaunchSpec{}, err
	}
	cfg, err := w.LoadProject(project)
	if err != nil {
		return matehost.Launch{}, harness.LaunchSpec{}, err
	}
	launch, err := buildLaunchSpec(ctx, profile, project, mateDir, decision, prep, servers, env, deps.Harnesses.EnvKeys(), cfg.Mate)
	if err != nil {
		return matehost.Launch{}, harness.LaunchSpec{}, err
	}
	turns, ok := profile.(harness.HeadlessTurns)
	if !ok {
		return matehost.Launch{}, harness.LaunchSpec{}, errUsage(fmt.Sprintf("the %s harness cannot run a headless Mate", profile.Kind()))
	}
	sessionID := turns.LaunchSessionID(launch.Args())
	if sessionID == "" {
		sessionID = prep.SessionID
	}
	hostLaunch := matehost.Launch{
		Project:   project,
		Agent:     agent,
		Harness:   string(profile.Kind()),
		Program:   launch.Kind(),
		Args:      launch.Args(),
		Dir:       mateDir,
		UnsetEnv:  launch.UnsetEnv(),
		SessionID: sessionID,
	}
	for _, v := range launch.Env() {
		hostLaunch.Env = append(hostLaunch.Env, v.Key+"="+v.Value)
	}
	return hostLaunch, launch, nil
}

// ErrNoReload is a Mate ReloadMate cannot give a fresh session in place: it
// runs in its pane rather than headless, or its host does not answer, or
// is too old to know the op. The caller stops and starts it instead.
var ErrNoReload = errors.New("the Mate cannot change session in place")

// ReloadMate gives a live headless Mate a fresh harness session without
// stopping its host (context refresh, CON-133): the directory is rendered
// again and a fresh launch prepared, as a fresh StartMate would, and the
// host switches to it between turns. The host, its pid and every REPL
// connected to it stay as they were; a host an older binary runs in its
// Herdr pane keeps its pane, so whoever is attached stays attached.
//
// A *matehost.BusyError means a turn runs or waits: nothing changed.
// ErrNoReload means the caller must stop and start the Mate instead.
func ReloadMate(ctx context.Context, w *store.Workspace, deps Deps, project string) (StartResult, error) {
	if w == nil {
		return StartResult{}, errUsage("spawn: a workspace is required")
	}
	if err := store.ValidateProjectName(project); err != nil {
		return StartResult{}, err
	}
	meta, err := w.ReadMateMeta(project)
	if err != nil {
		return StartResult{}, err
	}
	if meta[MetaMode] != ModeHeadless || meta[MetaAgent] == "" {
		return StartResult{}, ErrNoReload
	}
	mateDir := w.MateDir(project)
	client := matehost.Dial(mateDir)
	if err := client.Ping(ctx); err != nil {
		return StartResult{}, fmt.Errorf("%w: the host does not answer (%v)", ErrNoReload, err)
	}
	kind, err := deps.Harnesses.Parse(meta[MetaHarness])
	if err != nil {
		return StartResult{}, err
	}
	profile, err := deps.Harnesses.Lookup(kind)
	if err != nil {
		return StartResult{}, err
	}
	if _, ok := profile.(harness.HeadlessTurns); !ok {
		return StartResult{}, ErrNoReload
	}
	cfg, err := w.LoadProject(project)
	if err != nil {
		return StartResult{}, err
	}
	plugins, err := resolveMatePlugins(w, project)
	if err != nil {
		return StartResult{}, err
	}
	if err := prepareMateDir(w, deps, project, cfg, profile, mateDir, plugins.skills, true); err != nil {
		return StartResult{}, err
	}
	fresh := resumeDecision{}
	prep, err := prepareMateLaunch(ctx, w, deps, profile, mateDir, fresh, plugins.servers)
	if err != nil {
		return StartResult{}, err
	}
	session := runtime.SessionHandle{Name: meta[MetaSession]}
	if session.Name == "" {
		session.Name = w.Session()
	}
	hostLaunch, launch, err := buildHostLaunch(ctx, w, deps, project, profile, mateDir, fresh, prep, plugins.servers, session, meta[MetaAgent])
	if err != nil {
		return StartResult{}, err
	}
	launchedAt := deps.now()
	if err := client.Reload(ctx, hostLaunch); err != nil {
		var busy *matehost.BusyError
		if errors.As(err, &busy) {
			return StartResult{}, err
		}
		return StartResult{}, fmt.Errorf("%w: %v", ErrNoReload, err)
	}
	// After the host switched: a host started again later reads its
	// session from here (adoptInterruptedHost too).
	if err := matehost.WriteLaunch(mateDir, hostLaunch); err != nil {
		return StartResult{}, err
	}
	place := hostPlace{pid: hostPID(meta)}
	if meta[MetaPane] != "" {
		place = hostPlace{tab: runtime.TabHandle{Session: session, WorkspaceID: meta[MetaWorkspace], TabID: meta[MetaTab], PaneID: meta[MetaPane], Cwd: mateDir, Label: MateTabLabel}}
	}
	return recordHeadless(w, deps, project, kind, mateDir, fresh, session, meta[MetaAgent], place, hostLaunch.SessionID, launchedAt, launch)
}

// hostPlace is where a headless Mate's host runs: a process of its own,
// by pid (CON-225), or the Herdr pane an older binary started it in, by
// tab.
type hostPlace struct {
	pid int
	tab runtime.TabHandle
}

func recordHeadless(w *store.Workspace, deps Deps, project string, kind harness.Kind, mateDir string, decision resumeDecision, session runtime.SessionHandle, agent string, place hostPlace, sessionID string, launchedAt time.Time, launch harness.LaunchSpec) (StartResult, error) {
	startedAt := deps.now()
	meta := map[string]string{
		MetaHarness:    string(kind),
		MetaAgent:      agent,
		MetaSession:    session.Name,
		MetaSessionID:  sessionID,
		MetaStartedAt:  startedAt.Format(time.RFC3339),
		MetaLaunchedAt: launchedAt.Format(time.RFC3339),
		MetaMode:       ModeHeadless,
	}
	tab := place.tab
	if tab.PaneID != "" {
		meta[MetaPane], meta[MetaTab], meta[MetaWorkspace] = tab.PaneID, tab.TabID, tab.WorkspaceID
	} else {
		meta[MetaSocket] = matehost.SocketPath(mateDir)
		if place.pid > 0 {
			meta[MetaPID] = strconv.Itoa(place.pid)
		}
	}
	if launch.Model() != "" {
		meta[MetaModel] = launch.Model()
	}
	if launch.Effort() != "" {
		meta[MetaEffort] = string(launch.Effort())
	}
	res := StartResult{
		Project: project, Harness: kind, Agent: agent, Session: session.Name,
		Workspace: tab.WorkspaceID, Tab: tab.TabID, Pane: tab.PaneID, PID: place.pid, SessionID: sessionID,
		StartedAt: startedAt, MateDir: mateDir, ResumeNote: decision.Note,
		Headless: true,
	}
	if decision.Resume {
		meta[MetaResumed] = "true"
		meta[MetaResumedFrom] = decision.SessionID
		res.Resumed, res.ResumedFrom = true, decision.SessionID
	}
	if err := w.WriteMateMeta(project, meta); err != nil {
		return StartResult{}, err
	}
	return res, nil
}

// stopHost asks a headless Mate's host in its Herdr pane, which an older
// binary started, to finish its turn and exit. What it returns is why the
// stop was not the host's own clean exit, empty when it was; the caller
// then confirms the agent is gone or closes the pane. A host of its own is
// ended by endHost.
func stopHost(ctx context.Context, deps Deps, mateDir string, handle runtime.AgentHandle) string {
	wait := deps.hostStopWait()
	killed, err := matehost.Dial(mateDir).Stop(ctx, wait, false)
	switch {
	case err != nil:
		return fmt.Sprintf("the headless host did not answer its stop (%s)", oneLineErr(err))
	case killed:
		return fmt.Sprintf("the headless Mate's turn was still running after %s and was killed", wait)
	}
	// The host exits after answering, and Herdr drops the agent with it.
	for range hostExitPolls {
		if _, err := deps.Runtime.InspectAgent(ctx, handle); runtime.IsAgentNotFound(err) {
			return ""
		}
		if deps.sleep()(ctx, 100*time.Millisecond) != nil {
			break
		}
	}
	return "the headless host was still listed after it was asked to stop"
}

// HostClient is the headless Mate's host, if project's Mate runs headless.
func HostClient(w *store.Workspace, project string) (matehost.Client, bool) {
	if !w.MateHeadless(project) {
		return matehost.Client{}, false
	}
	return matehost.Dial(w.MateDir(project)), true
}

// adoptInterruptedHost is adoptInterruptedStart for a headless Mate: a host
// left running in the Mate directory by a start that was interrupted
// before it wrote mate.meta, and still answering on its socket, is
// recorded instead of started again (its host lock would refuse a second
// one). ok is false when no host answers.
func adoptInterruptedHost(ctx context.Context, w *store.Workspace, deps Deps, project string, kind harness.Kind, mateDir string, session runtime.SessionHandle) (StartResult, bool, error) {
	client := matehost.Dial(mateDir)
	client.Timeout = hostPingTimeout
	st, err := client.State(ctx)
	if err != nil {
		return StartResult{}, false, nil
	}
	l, err := matehost.ReadLaunch(mateDir)
	if err != nil {
		return StartResult{}, false, err
	}
	// The host runs the harness its launch names, whatever this start
	// asked for.
	if launched, err := deps.Harnesses.Parse(l.Harness); err == nil {
		kind = launched
	}
	agent := l.Agent
	if agent == "" {
		if agent, err = runtime.SanitizeAgentName(AgentNamePrefix, project); err != nil {
			return StartResult{}, false, err
		}
	}
	// A host of an older binary gives no pid: it is adopted all the same,
	// and its stop op is then the only way to end it.
	decision := resumeDecision{Note: fmt.Sprintf(
		"adopted the headless host (pid %d), left running by a start that was interrupted before it finished", st.PID)}
	res, err := recordHeadless(w, deps, project, kind, mateDir, decision, session, agent, hostPlace{pid: st.PID}, l.SessionID, deps.now(), harness.LaunchSpec{})
	res.Adopted = err == nil
	return res, true, err
}
