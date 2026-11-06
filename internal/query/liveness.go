package query

import (
	"context"

	"github.com/nguyenngocanh94/mate/internal/runtime"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// Liveness is what Herdr says about the workspace's agents at one moment. A
// recorded pane is not evidence of a live agent: a machine restart takes
// Herdr's server and every pane with it and leaves the meta files as they
// were, so the loader asks instead of trusting the file.
//
// The zero value means nobody asked, and the loader keeps the recorded
// state with its caveat - the answer for a one-shot read that never calls
// Herdr. An asked Liveness with no agents is the positive answer "nothing
// is alive".
//
// A headless Mate whose host runs outside Herdr (store.MateHostDetached,
// CON-225) is not Herdr's to answer for: Hosts holds what each such host
// said on its socket, by project, and a project missing from it was not
// asked.
type Liveness struct {
	Asked  bool
	Agents map[string]bool
	Hosts  map[string]bool
}

// Alive reports whether Herdr listed the named agent. It is only meaningful
// when Asked.
func (l Liveness) Alive(agent string) bool { return l.Agents[agent] }

// Host reports whether the host of project's headless Mate answered on its
// socket, and whether it was asked at all.
func (l Liveness) Host(project string) (alive, asked bool) {
	alive, asked = l.Hosts[project]
	return alive, asked
}

// MateGone reports whether the Mate meta records for project is known to
// be gone: a recorded pane Herdr does not list, or a host of its own that
// did not answer. A question nobody asked is never "gone".
func (l Liveness) MateGone(project string, meta map[string]string) bool {
	if store.MateHostDetached(meta) {
		alive, asked := l.Host(project)
		return asked && !alive
	}
	return meta["pane"] != "" && l.Asked && !l.Alive(meta["agent"])
}

// ReadLiveness asks Herdr which agents of the session are up. It never
// starts a server: a session that is not running, or a server that is not
// there, is the answer that no agent is alive. Any other failure leaves the
// question unasked, so a transport fault cannot make every row read as
// stopped.
func ReadLiveness(ctx context.Context, rt runtime.ReadAdapter, spec runtime.SessionSpec) Liveness {
	if rt == nil {
		return Liveness{}
	}
	session, running, err := rt.LookupSession(ctx, spec)
	if err != nil {
		if runtime.IsServerNotRunning(err) {
			return Liveness{Asked: true}
		}
		return Liveness{}
	}
	if !running {
		return Liveness{Asked: true}
	}
	agents, err := rt.ListAgents(ctx, session)
	if err != nil {
		if runtime.IsServerNotRunning(err) {
			return Liveness{Asked: true}
		}
		return Liveness{}
	}
	out := Liveness{Asked: true, Agents: make(map[string]bool, len(agents))}
	for _, a := range agents {
		if a.LiveHandleOK {
			out.Agents[a.Handle.Name] = true
		}
	}
	return out
}
