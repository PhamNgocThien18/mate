package spawn

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// A headless Mate (CON-78) runs `mate mate host <project>` in its pane
// instead of the harness's TUI. Everything up to the tab is a normal start:
// the manual, the settings and hooks, the launch argv. The host is handed
// that launch and runs it once per message (internal/matehost).

// HostStopWait is how long a stop lets a headless Mate finish its turn.
const HostStopWait = 2 * time.Minute

// headlessMode decides a start's mode: the request's, else, when the
// request is HeadlessOnly, headless if the harness can run it and the pane
// if not, else the mode the last run of this Mate recorded, else
// project.yaml's `mate.headless`.
func headlessMode(req StartRequest, prior map[string]string, cfg store.MateConfig, fallback string, canHeadless bool) (bool, error) {
	switch req.Mode {
	case ModeHeadless:
		if !canHeadless {
			return false, errNoHeadless
		}
		return true, nil
	case ModePane:
		return false, nil
	case "":
	default:
		return false, errUsage(fmt.Sprintf("unknown Mate mode %q (want %s or %s)", req.Mode, ModePane, ModeHeadless))
	}
	switch cfg.Mode {
	case ModePane, ModeHeadless, "":
	default:
		return false, errUsage(fmt.Sprintf("project.yaml mate.mode %q is not a Mate mode (want %s or %s)", cfg.Mode, ModePane, ModeHeadless))
	}
	if req.HeadlessOnly {
		return canHeadless, nil
	}
	// The mode of the last run, then project.yaml, then fallback
	// (Deps.DefaultMode: headless in the binary since 2026-10-15). A
	// harness without headless turns runs in the pane whatever the earlier
	// choice was, since a captain who switched harness asked for nothing
	// else.
	mode := prior[MetaMode]
	if mode == "" {
		mode = cfg.Mode
	}
	if mode == "" && cfg.Headless {
		mode = ModeHeadless
	}
	if mode == "" {
		mode = fallback
	}
	return mode == ModeHeadless && canHeadless, nil
}

// errNoHeadless refuses `--mode headless` on a harness that cannot run one.
var errNoHeadless = errUsage("this harness cannot run a headless Mate: it has no headless turns (harness.HeadlessTurns)")

// startHostInTab starts the host in the tab, waits until it answers on its
// socket and Herdr lists it under the Mate's name, and records mate.meta.
// Like startInTab, every failure is the caller's to compensate.
func startHostInTab(ctx context.Context, w *store.Workspace, deps Deps, project string, profile harness.Profile, mateDir string, decision resumeDecision, prep harness.Prepared, servers []harness.MCPServer, session runtime.SessionHandle, tab runtime.TabHandle) (StartResult, error) {
	runner, ok := deps.Runtime.(runtime.PaneRunner)
	if !ok {
		return StartResult{}, errUsage("spawn: this runtime cannot run a headless Mate's host in a pane")
	}
	reservation, err := runtime.AllocateAgentName(deps.Names, session.Name, AgentNamePrefix, project, runtime.FailOnCollision)
	if err != nil {
		return StartResult{}, err
	}
	hostLaunch, launch, err := buildHostLaunch(ctx, w, deps, project, profile, mateDir, decision, prep, servers, session, reservation.Name())
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
	launchedAt := deps.now()
	if err := runner.RunInPane(ctx, tab, []string{binary, "mate", "host", project, "--workspace", w.Root()}); err != nil {
		return StartResult{}, err
	}
	handle := runtime.AgentHandle{Session: session, Name: reservation.Name(), RawID: project, Kind: profile.Kind(), Tab: tab}
	observed, err := waitHost(ctx, deps, mateDir, handle)
	if err != nil {
		return StartResult{}, err
	}
	return recordHeadless(w, deps, project, profile.Kind(), mateDir, decision, session, tab, observed, hostLaunch.SessionID, launchedAt, launch)
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
// leaving its pane (context refresh, CON-133): the directory is rendered
// again and a fresh launch prepared, as a fresh StartMate would, and the
// host switches to it between turns. Herdr's pane, terminal and agent name
// stay as they were, so whoever is attached to the Mate stays attached;
// stopping and starting it closed the pane and ended every attach with
// "terminal ... not found".
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
	if meta[MetaMode] != ModeHeadless || meta[MetaAgent] == "" || meta[MetaPane] == "" {
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
	tab := runtime.TabHandle{Session: session, WorkspaceID: meta[MetaWorkspace], TabID: meta[MetaTab], PaneID: meta[MetaPane], Cwd: mateDir, Label: MateTabLabel}
	observed := runtime.ObservedAgent{Handle: runtime.AgentHandle{Session: session, Name: meta[MetaAgent], RawID: project, Kind: kind, Tab: tab}}
	return recordHeadless(w, deps, project, kind, mateDir, fresh, session, tab, observed, hostLaunch.SessionID, launchedAt, launch)
}

// waitHost waits, within the readiness timeout, for the host to answer on
// its socket and for Herdr to list it under the reserved name.
func waitHost(ctx context.Context, deps Deps, mateDir string, handle runtime.AgentHandle) (runtime.ObservedAgent, error) {
	ctx, cancel := context.WithTimeout(ctx, deps.readinessTimeout())
	defer cancel()
	client := matehost.Dial(mateDir)
	var last error
	for {
		if last = client.Ping(ctx); last == nil {
			obs, err := deps.Runtime.InspectAgent(ctx, handle)
			if err == nil {
				return obs, nil
			}
			last = err
		}
		if err := deps.sleep()(ctx, 100*time.Millisecond); err != nil {
			return runtime.ObservedAgent{}, observability.WrapError(observability.CodeUnknown,
				fmt.Sprintf("the headless Mate's host did not come up as agent %s within %s", handle.Name, deps.readinessTimeout()), last)
		}
	}
}

func recordHeadless(w *store.Workspace, deps Deps, project string, kind harness.Kind, mateDir string, decision resumeDecision, session runtime.SessionHandle, tab runtime.TabHandle, observed runtime.ObservedAgent, sessionID string, launchedAt time.Time, launch harness.LaunchSpec) (StartResult, error) {
	startedAt := deps.now()
	meta := map[string]string{
		MetaHarness:    string(kind),
		MetaAgent:      observed.Handle.Name,
		MetaPane:       tab.PaneID,
		MetaTab:        tab.TabID,
		MetaWorkspace:  tab.WorkspaceID,
		MetaSession:    session.Name,
		MetaSessionID:  sessionID,
		MetaStartedAt:  startedAt.Format(time.RFC3339),
		MetaLaunchedAt: launchedAt.Format(time.RFC3339),
		MetaMode:       ModeHeadless,
	}
	if launch.Model() != "" {
		meta[MetaModel] = launch.Model()
	}
	if launch.Effort() != "" {
		meta[MetaEffort] = string(launch.Effort())
	}
	res := StartResult{
		Project: project, Harness: kind, Agent: observed.Handle.Name, Session: session.Name,
		Workspace: tab.WorkspaceID, Tab: tab.TabID, Pane: tab.PaneID, SessionID: sessionID,
		StartedAt: startedAt, MateDir: mateDir, Status: observed.Status, ResumeNote: decision.Note,
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

// stopHost asks a headless Mate's host to finish its turn and exit. What it
// returns is why the stop was not the host's own clean exit, empty when it
// was; the caller then confirms the agent is gone or closes the pane.
func stopHost(ctx context.Context, deps Deps, mateDir string, handle runtime.AgentHandle) string {
	wait := deps.HostStopWait
	if wait <= 0 {
		wait = HostStopWait
	}
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

// hostExitPolls bounds stopHost's wait for the host to leave: 10 seconds.
const hostExitPolls = 100

// HostClient is the headless Mate's host, if project's Mate runs headless.
func HostClient(w *store.Workspace, project string) (matehost.Client, bool) {
	if !w.MateHeadless(project) {
		return matehost.Client{}, false
	}
	return matehost.Dial(w.MateDir(project)), true
}

// adoptInterruptedHost is adoptInterruptedStart for a headless Mate: a host
// left running in the Mate directory by a start that was interrupted, and
// still answering on its socket, is recorded instead of started again (its
// host lock would refuse a second one).
func adoptInterruptedHost(ctx context.Context, w *store.Workspace, deps Deps, project string, kind harness.Kind, mateDir string, session runtime.SessionHandle) (StartResult, bool, error) {
	name, err := runtime.SanitizeAgentName(AgentNamePrefix, project)
	if err != nil {
		return StartResult{}, false, err
	}
	obs, err := deps.Runtime.InspectAgent(ctx, runtime.AgentHandle{Session: session, Name: name, RawID: project})
	if err != nil {
		if runtime.IsAgentNotFound(err) {
			return StartResult{}, false, nil
		}
		return StartResult{}, false, err
	}
	if !sameDir(obs.Cwd, mateDir) || matehost.Dial(mateDir).Ping(ctx) != nil {
		return StartResult{}, false, observability.NewError(observability.CodeAlreadyExists, fmt.Sprintf(
			"Herdr already runs an agent named %s in pane %s of session %s that is not this project's headless Mate host; stop it in Herdr, then start again",
			name, obs.Handle.Tab.PaneID, session.Name))
	}
	l, err := matehost.ReadLaunch(mateDir)
	if err != nil {
		return StartResult{}, false, err
	}
	decision := resumeDecision{Note: fmt.Sprintf(
		"adopted the headless host %s, left running in pane %s by a start that was interrupted before it finished", name, obs.Handle.Tab.PaneID)}
	res, err := recordHeadless(w, deps, project, kind, mateDir, decision, session, obs.Handle.Tab, obs, l.SessionID, deps.now(), harness.LaunchSpec{})
	res.Adopted = err == nil
	return res, true, err
}
