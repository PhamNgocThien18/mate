package main

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/crewstate"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// A lane (store.RepoConfig.Lane, CON-118) is a machine resource the crews
// of a repo contend for, named by the captain; mate knows nothing of what
// the name stands for (CON-146). The app only reports how full each lane
// is; the Mate decides what to queue (skill new-task). A crew holds its
// repo's lane while it is spawned or working - the states in which it
// builds. One waiting on the Mate, the captain or a blocker holds nothing.

// laneRow is one lane's occupancy.
type laneRow struct {
	Name string
	Cap  int
	// Holders are "<project>/<crew> (<state>)", in project then crew order.
	Holders []string
}

// Full reports whether no crew may take the lane now.
func (r laneRow) Full() bool { return len(r.Holders) >= r.Cap }

// laneHeld is whether a crew in the state holds its repo's lane.
func laneHeld(state string) bool {
	return state == string(crewstate.StateSpawned) || state == string(crewstate.StateWorking)
}

// crewLane is the lane a crew holds: what its spawn recorded (`lane=`,
// empty for none), or its repo's for a crew from before lanes.
func crewLane(cfg store.ProjectConfig, meta map[string]string) string {
	if lane, ok := meta[spawn.MetaLane]; ok {
		return strings.TrimSpace(lane)
	}
	repo, err := cfg.CrewRepo(meta)
	if err != nil {
		return ""
	}
	return repo.Lane
}

// laneRows reads every project's repos and crews and returns the lanes
// some repo declares, in name order; none when no repo declares one.
func laneRows(w *store.Workspace) ([]laneRow, error) {
	caps := map[string]int{}
	holders := map[string][]string{}
	for _, ref := range w.Projects() {
		cfg, err := w.LoadProject(ref.Name)
		if err != nil {
			return nil, err
		}
		declared := false
		for _, r := range cfg.Repos {
			if r.Lane != "" {
				caps[r.Lane] = w.LaneCap(r.Lane)
				declared = true
			}
		}
		if !declared {
			continue
		}
		crews, err := spawn.ListCrews(w, ref.Name)
		if err != nil {
			return nil, err
		}
		for _, c := range crews {
			if c.Closed || !laneHeld(c.State) {
				continue
			}
			meta, err := w.ReadCrewMeta(ref.Name, c.Crew)
			if err != nil {
				return nil, err
			}
			lane := crewLane(cfg, meta)
			if lane == "" {
				continue
			}
			holders[lane] = append(holders[lane], fmt.Sprintf("%s/%s (%s)", ref.Name, c.Crew, c.State))
		}
	}
	names := make([]string, 0, len(caps))
	for name := range caps {
		names = append(names, name)
	}
	sort.Strings(names)
	rows := make([]laneRow, 0, len(names))
	for _, name := range names {
		rows = append(rows, laneRow{Name: name, Cap: caps[name], Holders: holders[name]})
	}
	return rows, nil
}

// writeLanes prints the lanes block of `crew dispatch`: nothing when no
// repo declares a lane, else one line per lane with its holders and what
// to do when it is full.
func writeLanes(out io.Writer, rows []laneRow) {
	if len(rows) == 0 {
		return
	}
	fmt.Fprintln(out, "\nlanes (a crew holds its repo's lane while spawned or working; wait-mate, needs-decision and blocked hold nothing):")
	for _, r := range rows {
		line := fmt.Sprintf("  %s: %d/%d busy", r.Name, len(r.Holders), r.Cap)
		if len(r.Holders) > 0 {
			line += " - " + strings.Join(r.Holders, ", ")
		}
		if r.Full() {
			line += fmt.Sprintf("; full: queue the next %s task with `blocked-by: lane %s` and dispatch work outside the lane", r.Name, r.Name)
		} else {
			line += fmt.Sprintf("; %d free", r.Cap-len(r.Holders))
		}
		fmt.Fprintln(out, line)
	}
}

// laneGuard refuses a spawn into a full lane, exit 2 like any other bad
// argument: the Mate queues the task with `blocked-by: lane <name>` and
// dispatches work outside the lane (skill new-task). It reports and the
// Mate still decides, but twice in one evening a Mate dispatched into a
// lane that `crew dispatch` had just called full, so the report alone was
// not enough. `--lane none` passes, for a crew that will never use the
// lane's resource.
func laneGuard(w *store.Workspace, req spawn.SpawnCrewRequest) error {
	cfg, err := w.LoadProject(req.Project)
	if err != nil {
		return err
	}
	lane, err := spawn.LaneFor(req, cfg)
	if err != nil || lane == "" {
		// A repo or lane the spawn cannot resolve is SpawnCrew's refusal
		// to make, with its own exit code; the guard only knows lanes.
		return nil
	}
	rows, err := laneRows(w)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.Name != lane || !r.Full() {
			continue
		}
		return newUsageErrorf("mate crew spawn: lane %s is full (%d/%d: %s); queue the task with `blocked-by: lane %s` and dispatch work outside the lane, or pass --lane none only when this crew will never use the lane's resource",
			lane, len(r.Holders), r.Cap, strings.Join(r.Holders, ", "), lane)
	}
	return nil
}
