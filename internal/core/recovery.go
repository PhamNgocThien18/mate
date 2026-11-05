package core

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/recovery"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// Recovery is where the engine's recovery stands.
type Recovery struct {
	// Reported is true once recovery has told any progress, and Progress
	// is the latest it told.
	Reported bool
	Progress recovery.Progress
	// Done is true once recovery has returned: Result is what it did, and
	// DoneAt when it returned.
	Done   bool
	Result recovery.Result
	DoneAt time.Time
}

// recoveryRun runs recovery once, in the background, as the engine's last
// loop: it starts once the others run.
type recoveryRun struct {
	run RecoverFunc
	ws  *store.Workspace
	now func() time.Time

	// cancel and done are set by Start and read by Stop: the engine calls
	// each once, Stop on a goroutine it starts after Start has returned.
	cancel context.CancelFunc
	done   chan struct{}

	mu    sync.Mutex
	state Recovery
}

// Start begins the run and returns at once; it cannot fail.
func (r *recoveryRun) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	r.cancel, r.done = cancel, done
	go func() {
		defer close(done)
		r.finish(r.run(ctx, r.ws, r.progress))
	}()
	return nil
}

// Stop cancels what is left of the run and waits for it to return; the
// engine bounds the wait (StopWait). A recovery still busy past it is left
// to the process exit, and the next recovery adopts or refuses what it left
// by name. A run that never started has nothing to stop.
func (r *recoveryRun) Stop() {
	if r.cancel == nil {
		return
	}
	r.cancel()
	<-r.done
}

func (r *recoveryRun) progress(p recovery.Progress) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state.Reported, r.state.Progress = true, p
}

func (r *recoveryRun) finish(res recovery.Result) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state.Done, r.state.Result, r.state.DoneAt = true, res, r.now()
}

func (r *recoveryRun) read() Recovery {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.state
}

// recoverySummaryFor is how long the one-line summary stays on the status
// line after recovery finishes. A row that failed keeps its own error.
const recoverySummaryFor = 30 * time.Second

// withRecovery puts where recovery stands into a snapshot query.Load built
// out of files alone: a status line while it works, a summary after, and the
// error of each agent that did not come back on that agent's row. A UI is
// usable throughout. Nothing is said before recovery first reports, and the
// summary leaves the status line recoverySummaryFor after recovery returned.
func withRecovery(snap query.Snapshot, st Recovery, now time.Time) query.Snapshot {
	var status query.RecoveryStatus
	var errs map[[2]string]string
	switch {
	case st.Done:
		if now.Sub(st.DoneAt) <= recoverySummaryFor {
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
			errs[[2]string{it.Project, it.Crew}] = "recovery failed: " + firstLine(it.Err.Error())
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

func firstLine(s string) string {
	first, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(first)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
