package refresh

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/harness"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// logsAnswers reports whether the recorded harness declares verified
// turn-end evidence that mate's Stop hook logs each answer to sent.log.
func logsAnswers(reg harness.Registry, recorded string) bool {
	kind, err := reg.Parse(recorded)
	if err != nil {
		return false
	}
	profile, err := reg.Lookup(kind)
	if err != nil {
		return false
	}
	c := profile.Capabilities().TurnEnd
	return c.Verified() && c.Impl.LogsAnswers()
}

// shellWord quotes s as one word for a POSIX shell.
func shellWord(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }

// queueEmpty fails, naming the first one, while an outbox item waits to be
// typed into the Mate.
func queueEmpty(w *store.Workspace, project string) error {
	items, err := w.ReadOutbox(project)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.Queued() {
			return fmt.Errorf("refresh postponed: %s item %s is queued", item.Source, strconv.FormatInt(item.ID, 10))
		}
	}
	return nil
}

// mateQuiet reports whether the Mate's last answer to the captain came no
// earlier than the captain's last message to it, and store.QuietAfter ago
// or more.
func mateQuiet(w *store.Workspace, project string, now time.Time) (bool, error) {
	entries, _, err := w.ReadSent(project, 0)
	if err != nil {
		return false, err
	}
	var asked, answered time.Time
	for _, e := range entries {
		if e.Source == store.SourceUser && e.Target == store.TargetMate {
			asked = e.Time
		}
		if e.Source == store.SourceMate && e.Target == store.SourceUser {
			answered = e.Time
		}
	}
	return !answered.IsZero() && !answered.Before(asked) && now.Sub(answered) >= store.QuietAfter, nil
}

// mateComposer is the Mate's composer: read from its pane, or, for a
// headless Mate (CON-78), as its host states it.
func mateComposer(ctx context.Context, w *store.Workspace, deps spawn.Deps, project string) (send.Classification, error) {
	if host, ok := spawn.HostClient(w, project); ok {
		st, err := host.State(ctx)
		if err != nil {
			return send.Classification{}, err
		}
		switch {
		case st.Draft != "":
			return send.Classification{State: send.StatePending, Pending: st.Draft}, nil
		case st.Busy || st.Queued > 0:
			return send.Classification{State: send.StateBusy}, nil
		}
		return send.Classification{State: send.StateEmpty}, nil
	}
	h, kind, err := spawn.MateHandle(ctx, w, deps, project)
	if err != nil {
		return send.Classification{}, err
	}
	// Every read of a pane goes through its harness's screen source, and
	// every verdict on it through that harness's layout.
	profile, err := deps.Harnesses.Lookup(kind)
	if err != nil {
		return send.Classification{}, err
	}
	screens := profile.Screen()
	pane, err := deps.Runtime.ReadAgentStyled(ctx, h, screens.ReadSource(), send.DefaultLines)
	if err != nil {
		return send.Classification{}, err
	}
	return send.ClassifyComposer(screens, pane), nil
}
