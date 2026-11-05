package spawn

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/crewstate"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// RemoveProjectOptions are what RemoveProject takes beyond the project.
type RemoveProjectOptions struct {
	// Stow asks a running Mate to record what exists only in its
	// conversation, just before the Mate is stopped. RemoveProjectStow
	// passes the same stow `mate mate stop` runs; nil stops the Mate at
	// once. It is called only for a Mate Herdr lists (or whose liveness
	// could not be asked), and an error from it fails the Mate's stop.
	Stow func(ctx context.Context, project string) error
	// Progress, when set, is told each step as it begins, in words fit for
	// a status line.
	Progress func(step string)
}

// RemoveProjectStow is how the CLI and the Console stop a project's Mate for
// a removal: with the stow `mate mate stop` runs (StowBeforeStop), announced
// on stderr.
func RemoveProjectStow(w *store.Workspace, deps Deps, caller string, stderr io.Writer, progress func(string)) RemoveProjectOptions {
	return RemoveProjectOptions{
		Progress: progress,
		Stow: func(ctx context.Context, project string) error {
			_, err := StowBeforeStop(ctx, w, deps, project, caller, false, stderr)
			return err
		},
	}
}

// StoppedAgent is one agent RemoveProject stopped.
type StoppedAgent struct {
	// Crew is the crew id, empty for the Mate.
	Crew string
	// Stop is what the stop established. It is zero for an agent Herdr did
	// not list, which needed no stop beyond clearing its run meta.
	Stop StopResult
	// Cleared is true for such an agent: only its run meta was cleared.
	Cleared bool
}

// RemoveProjectResult is what RemoveProject did.
type RemoveProjectResult struct {
	Project string
	// Stopped is every agent that was stopped or had its run meta cleared,
	// crews first, the Mate last.
	Stopped []StoppedAgent
	// Failed names every agent that could not be stopped, as `crew <id>` or
	// `mate`, with the reason. When it is not empty the project is still
	// registered.
	Failed []string
	// Removed is true once the project left workspace.yaml.
	Removed bool
}

// RemoveProject takes a project out of the workspace without leaving its
// agents running unmanaged (docs/mvp.md task 75). It stops every running
// crew the way `mate crew stop` does, stops the Mate the way `mate mate
// stop` does, and only then drops the project from workspace.yaml.
//
// Every stop is attempted, so one refusal does not hide another, but a
// single failure leaves the project registered and is named in the error:
// a project is never unregistered while an agent of it may still run.
// `projects/<p>/`, the repos, and whatever the stops kept stay on disk, so
// `mate project add` under the same name brings the history back.
//
// "Running" is Herdr's answer (query.ReadLiveness) where it can be had: an
// agent whose meta records a pane that Herdr does not list is already gone,
// and only its run meta is cleared. A headless Mate whose host runs as a
// process of its own (CON-225) is its socket's to answer for. Closed crews
// and agents with no pane are skipped. Herdr closes a workspace with its
// last tab, so once the agents' tabs are closed the project's workspace is
// gone with them.
func RemoveProject(ctx context.Context, w *store.Workspace, deps Deps, project string, opts RemoveProjectOptions) (RemoveProjectResult, error) {
	out := RemoveProjectResult{Project: project}
	if w == nil {
		return out, errUsage("spawn: a workspace is required")
	}
	if deps.Runtime == nil {
		return out, errUsage("spawn: a runtime adapter is required")
	}
	if err := w.RequireProject(project); err != nil {
		return out, err
	}
	progress := opts.Progress
	if progress == nil {
		progress = func(string) {}
	}
	spec, err := deps.sessionSpec(w)
	if err != nil {
		return out, err
	}
	live := query.ReadLiveness(ctx, deps.Runtime, spec)
	live.Hosts = HostLiveness(ctx, w)
	gone := func(agent string) bool { return live.Asked && !live.Alive(agent) }
	fail := func(who string, err error) { out.Failed = append(out.Failed, fmt.Sprintf("%s: %v", who, err)) }

	ids, err := w.CrewIDs(project)
	if err != nil {
		return out, err
	}
	for _, id := range ids {
		meta, err := w.ReadCrewMeta(project, id)
		if err != nil {
			fail("crew "+id, err)
			continue
		}
		if crewstate.State(meta[MetaState]).Closed() || meta[MetaPane] == "" {
			continue
		}
		progress("stopping crew " + id)
		if gone(meta[MetaAgent]) {
			if err := w.WriteCrewMeta(project, id, clearCrewRunMeta(meta)); err != nil {
				fail("crew "+id, err)
				continue
			}
			out.Stopped = append(out.Stopped, StoppedAgent{Crew: id, Cleared: true})
			continue
		}
		res, err := StopCrew(ctx, w, deps, project, id, false)
		if err != nil {
			fail("crew "+id, err)
			continue
		}
		out.Stopped = append(out.Stopped, StoppedAgent{Crew: id, Stop: res})
	}

	mate, err := w.ReadMateMeta(project)
	if err != nil {
		fail("mate", err)
	} else if store.MateRecorded(mate) {
		progress("stopping the Mate")
		stow := opts.Stow
		if live.MateGone(project, mate) {
			stow = nil
		}
		var err error
		if stow != nil {
			err = stow(ctx, project)
		}
		if err == nil {
			var res StopResult
			if res, err = StopMate(ctx, w, deps, project); err == nil {
				out.Stopped = append(out.Stopped, StoppedAgent{Stop: res})
			}
		}
		if err != nil {
			fail("mate", err)
		}
	}

	if len(out.Failed) > 0 {
		return out, fmt.Errorf("project remove %s: not removed, still registered; could not stop %s",
			project, strings.Join(out.Failed, "; "))
	}
	progress("unregistering " + project)
	if err := w.RemoveProject(project); err != nil {
		return out, fmt.Errorf("project remove %s: %w", project, err)
	}
	out.Removed = true
	return out, nil
}
