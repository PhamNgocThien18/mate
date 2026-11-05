package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/core"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/recovery"
)

// recoverySummaryFor is how long the one-line summary stays on the status
// line after recovery finishes. A row that failed keeps its own error.
const recoverySummaryFor = 30 * time.Second

// consoleRecovery says where recovery stands (docs/mvp.md M17), which the
// engine runs in the background while the workspace is open (internal/core):
// a status line while it works, a summary after, and an error on the row of
// each agent that did not come back. The console is usable throughout; it
// reads this the way it reads the observer's, through apply on every load.
type consoleRecovery struct {
	now func() time.Time
	// state is the engine's Recovery.
	state func() core.Recovery
}

// recoveryProgress is the status line while recovery works.
func recoveryProgress(p recovery.Progress) query.RecoveryStatus {
	line := "recovering…"
	if p.Total > 0 {
		line = fmt.Sprintf("recovering %d of %d…", p.Done, p.Total)
	}
	return query.RecoveryStatus{Line: line, Active: true}
}

// recoveryRowErrors is the error of each agent that did not come back, by
// project and crew ("" for the Mate).
func recoveryRowErrors(res recovery.Result) map[[2]string]string {
	errs := map[[2]string]string{}
	for _, it := range res.Items {
		if it.Err != nil {
			errs[[2]string{it.Project, it.Crew}] = "recovery failed: " + oneLineText(it.Err.Error())
		}
	}
	return errs
}

// recoverySummary is the line recovery leaves behind: what came back, what
// was repaired, and the first thing that did not. A pass that found nothing
// says nothing.
func recoverySummary(res recovery.Result) query.RecoveryStatus {
	if res.Idle() {
		return query.RecoveryStatus{}
	}
	var parts []string
	if len(res.Items) > 0 || (res.Err == nil && len(res.Watchers) == 0) {
		parts = append(parts, fmt.Sprintf("recovered %d", res.Recovered()))
	}
	links := 0
	for _, f := range res.Fixes {
		if f.Err == nil {
			links++
		}
	}
	if links > 0 {
		parts = append(parts, fmt.Sprintf("repaired %d %s", links, plural(links, "link", "links")))
	}
	if n := len(res.Watchers) - failedWatchers(res); n > 0 {
		parts = append(parts, fmt.Sprintf("restarted %d pull request %s", n, plural(n, "watcher", "watchers")))
	}
	failures := res.Failures()
	if len(failures) > 0 {
		failed := fmt.Sprintf("%d failed: %s", len(failures), failures[0])
		parts = append(parts, failed)
	}
	return query.RecoveryStatus{Line: strings.Join(parts, "; "), Failed: len(failures) > 0, Failures: failures}
}

func failedWatchers(res recovery.Result) int {
	n := 0
	for _, w := range res.Watchers {
		if w.Err != nil {
			n++
		}
	}
	return n
}

func oneLineText(s string) string {
	first, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(first)
}

// apply puts recovery's state into a snapshot query.Load built out of files
// alone: the status line, and the error of each row that did not come back.
// Nothing is said before recovery first reports, and the summary leaves the
// status line recoverySummaryFor after recovery returned.
func (r *consoleRecovery) apply(snap query.Snapshot) query.Snapshot {
	if r == nil {
		return snap
	}
	st := r.state()
	var status query.RecoveryStatus
	var errs map[[2]string]string
	switch {
	case st.Done:
		if r.now().Sub(st.DoneAt) <= recoverySummaryFor {
			status = recoverySummary(st.Result)
		}
		errs = recoveryRowErrors(st.Result)
	case st.Reported:
		status = recoveryProgress(st.Progress)
	}
	snap.Recovery = status
	for p := range snap.Projects {
		project := &snap.Projects[p]
		if msg, ok := errs[[2]string{project.ProjectID, ""}]; ok {
			project.Mate.Error = query.KnownField(query.ErrorReason(msg))
		}
		for c := range project.Crews {
			crew := &project.Crews[c]
			if msg, ok := errs[[2]string{project.ProjectID, crew.CrewID}]; ok {
				crew.Error = query.KnownField(query.ErrorReason(msg))
			}
		}
	}
	return snap
}
