package query

import (
	"context"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/runtime"
)

// liveWorkspace records a Mate and two crews, all with a pane, the way a
// workspace looks after a machine restart: the meta files are intact.
func liveWorkspace(t *testing.T) (*runtime.Fake, runtime.SessionSpec, runtime.SessionHandle, func(*testing.T) ProjectNode) {
	t.Helper()
	ws := newWorkspace(t, "shop")
	if err := ws.WriteMateMeta("shop", map[string]string{
		"harness": "claude", "session": "fm-x", "pane": "w1:p1", "agent": "mate-shop"}); err != nil {
		t.Fatal(err)
	}
	for id, agent := range map[string]string{"k1": "crew-k1", "k2": "crew-k2"} {
		if err := ws.WriteCrewMeta("shop", id, map[string]string{
			"task": "ship", "agent": agent, "pane": "w1:p-" + id, "state": "spawned"}); err != nil {
			t.Fatal(err)
		}
	}
	rt := runtime.NewFake()
	spec := runtime.SessionSpec{WorkspaceID: "ws-live", Name: "fm-x", ConfigHome: t.TempDir(), Names: rt.Names}
	session, err := rt.EnsureSession(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	return rt, spec, session, func(t *testing.T) ProjectNode {
		t.Helper()
		live := ReadLiveness(context.Background(), rt, spec)
		snap, err := LoadLive(context.Background(), ws, testHarnesses, nil, live)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		return snap.Projects[0]
	}
}

func putAgent(rt *runtime.Fake, session runtime.SessionHandle, name, pane string) {
	rt.PutAgent(runtime.AgentHandle{Session: session, Name: name,
		Tab: runtime.TabHandle{Session: session, PaneID: pane}}, runtime.AgentIdle)
}

func crewByID(p ProjectNode, id string) CrewNode {
	for _, c := range p.Crews {
		if c.CrewID == id {
			return c
		}
	}
	return CrewNode{}
}

func TestLoadLiveHerdrDownReadsRecordedPanesAsStopped(t *testing.T) {
	rt, _, _, load := liveWorkspace(t)
	rt.SessionNotRunning = true
	p := load(t)
	if got := p.Mate.Designated.Value.Status; got != MateStopped {
		t.Fatalf("mate status = %q, want stopped with Herdr down", got)
	}
	if p.Mate.Binding.State != Absent {
		t.Fatalf("mate binding = %+v, want Absent", p.Mate.Binding)
	}
	if got := findAction(t, p.Mate.Actions, "resume"); !got.Available {
		t.Fatalf("resume = %+v, want available so s works after a restart", got)
	}
	if got := findAction(t, p.Mate.Actions, "start"); got.Available {
		t.Fatalf("start = %+v, want refused for a stopped Mate", got)
	}
	for _, id := range []string{"k1", "k2"} {
		if c := crewByID(p, id); c.Binding.State != Absent {
			t.Fatalf("crew %s binding = %+v, want Absent with Herdr down", id, c.Binding)
		}
	}
}

func TestLoadLiveAgentGoneIsStoppedAgentAliveIsRunning(t *testing.T) {
	rt, _, session, load := liveWorkspace(t)
	putAgent(rt, session, "mate-shop", "w1:p1")
	putAgent(rt, session, "crew-k1", "w1:p-k1")
	// crew-k2 is not listed: its pane died with a server that came back.
	p := load(t)
	if got := p.Mate.Designated.Value.Status; got != MateRunning {
		t.Fatalf("mate status = %q, want running for a listed agent", got)
	}
	if got := findAction(t, p.Mate.Actions, "stop"); !got.Available {
		t.Fatalf("stop = %+v, want available for a live Mate", got)
	}
	if c := crewByID(p, "k1"); c.Binding.State != Known || c.Binding.Value.Status != BindingActive {
		t.Fatalf("k1 binding = %+v, want active", c.Binding)
	}
	if c := crewByID(p, "k2"); c.Binding.State != Absent {
		t.Fatalf("k2 binding = %+v, want Absent for an agent Herdr does not list", c.Binding)
	}
}

func TestLoadLiveDeliberateStopStaysCreated(t *testing.T) {
	ws := newWorkspace(t, "shop")
	// mate stop removes `pane`: nothing recorded, nothing to reconcile.
	if err := ws.WriteMateMeta("shop", map[string]string{"harness": "claude", "agent": "mate-shop"}); err != nil {
		t.Fatal(err)
	}
	snap, err := LoadLive(context.Background(), ws, testHarnesses, nil, Liveness{Asked: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := snap.Projects[0].Mate.Designated.Value.Status; got != MateCreated {
		t.Fatalf("mate status = %q, want created: a deliberate stop is not a lost pane", got)
	}
}

func TestLoadLiveUnaskedKeepsRecordedState(t *testing.T) {
	rt, spec, _, load := liveWorkspace(t)
	rt.SessionLookupErr = context.DeadlineExceeded
	if got := ReadLiveness(context.Background(), rt, spec); got.Asked {
		t.Fatalf("liveness = %+v, want unasked after a transport fault", got)
	}
	p := load(t)
	if got := p.Mate.Designated.Value.Status; got != MateRunning {
		t.Fatalf("mate status = %q, want the recorded running when Herdr could not be asked", got)
	}
}

// A headless Mate whose host runs as a process of its own (CON-225) has no
// pane: its host's answer on its socket, not Herdr's, says whether it runs.
func TestLoadLiveDetachedHostAnswersForItsMate(t *testing.T) {
	ws := newWorkspace(t, "shop")
	if err := ws.WriteMateMeta("shop", map[string]string{
		"harness": "claude", "agent": "mate-shop", "mode": "headless", "pid": "4242", "socket": "/x/host.sock", "session": "fm-x"}); err != nil {
		t.Fatal(err)
	}
	load := func(live Liveness) MateNode {
		t.Helper()
		snap, err := LoadLive(context.Background(), ws, testHarnesses, nil, live)
		if err != nil {
			t.Fatal(err)
		}
		return snap.Projects[0].Mate
	}
	for name, tc := range map[string]struct {
		live   Liveness
		status MateStatus
		bound  bool
	}{
		"host answers": {Liveness{Asked: true, Hosts: map[string]bool{"shop": true}}, MateRunning, true},
		"host gone":    {Liveness{Asked: true, Hosts: map[string]bool{"shop": false}}, MateStopped, false},
		// Herdr lists nothing: that says nothing about a host outside it.
		"host not asked":     {Liveness{Asked: true}, MateRunning, true},
		"nothing asked":      {Liveness{}, MateRunning, true},
		"herdr down, answer": {Liveness{Hosts: map[string]bool{"shop": true}}, MateRunning, true},
	} {
		m := load(tc.live)
		if got := m.Designated.Value.Status; got != tc.status {
			t.Errorf("%s: status = %q, want %q", name, got, tc.status)
		}
		if bound := m.Binding.State == Known && m.Binding.Value.Status == BindingActive; bound != tc.bound {
			t.Errorf("%s: binding = %+v, want active %v", name, m.Binding, tc.bound)
		}
		if tc.bound && (m.Binding.Value.Runtime != "host" || m.Binding.Value.Pane != "") {
			t.Errorf("%s: binding = %+v, want the host's, with no pane", name, m.Binding.Value)
		}
		if got := findAction(t, m.Actions, "stop").Available; got != tc.bound {
			t.Errorf("%s: stop available = %v, want %v", name, got, tc.bound)
		}
		if got := findAction(t, m.Actions, "resume").Available; got != !tc.bound {
			t.Errorf("%s: resume available = %v, want %v", name, got, !tc.bound)
		}
	}
}

func TestMateGone(t *testing.T) {
	pane := map[string]string{"agent": "mate-shop", "pane": "w1:p1", "mode": "headless"}
	host := map[string]string{"agent": "mate-shop", "mode": "headless", "pid": "1"}
	stopped := map[string]string{"mode": "headless"}
	for name, tc := range map[string]struct {
		live Liveness
		meta map[string]string
		want bool
	}{
		"pane listed":       {Liveness{Asked: true, Agents: map[string]bool{"mate-shop": true}}, pane, false},
		"pane not listed":   {Liveness{Asked: true}, pane, true},
		"pane unasked":      {Liveness{}, pane, false},
		"host answers":      {Liveness{Hosts: map[string]bool{"shop": true}}, host, false},
		"host silent":       {Liveness{Hosts: map[string]bool{"shop": false}}, host, true},
		"host unasked":      {Liveness{Asked: true}, host, false},
		"stopped, recorded": {Liveness{Asked: true, Hosts: map[string]bool{"shop": false}}, stopped, false},
	} {
		if got := tc.live.MateGone("shop", tc.meta); got != tc.want {
			t.Errorf("%s: gone = %v, want %v", name, got, tc.want)
		}
	}
}
