package usage

import (
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/timeline"
)

// A crew that has made no model call yet gets each crew page's header and
// a line saying nothing was recorded, never an error or an empty page
// (CON-168, Trường hợp đặc biệt).
func TestUsageOfACrewWithNoTurnsSaysSo(t *testing.T) {
	w := usageWorkspace(t)
	if err := w.WriteCrewMeta("shop", "idle", map[string]string{"harness": "codex", "task": "wait", "state": "spawned"}); err != nil {
		t.Fatal(err)
	}
	// The ingest has recorded the crew and its task, but no turn.
	handle, err := db.Open(w)
	if err != nil {
		t.Fatal(err)
	}
	actor := timeline.CrewActorID("shop", "idle")
	for _, stmt := range []string{
		`INSERT INTO actor(id, project, kind, name) VALUES ('` + actor + `', 'shop', 'crew', 'idle')`,
		`INSERT INTO task(crew_actor_id, project, spawned_at) VALUES ('` + actor + `', 'shop', '` + db.FormatTime(time.Now()) + `')`,
	} {
		if _, err := handle.SQL().Exec(stmt); err != nil {
			handle.Close()
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	handle.Close()
	for name, tc := range map[string]struct {
		req  Request
		want string
	}{
		"turns": {Request{Project: "shop", Crew: "idle"},
			"TURN  STARTED  MODEL  EFFORT  IN  CACHED  OUT  TOTAL  CTX  DURATION  OUTCOME\nno turns recorded for idle\n"},
		"sessions": {Request{Project: "shop", Crew: "idle", Sessions: true},
			"SESSION  TURNS  IN  CACHED  OUT  TOTAL  CTX-MAX  STARTED  ENDED\nno turns recorded for idle\n"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := writePage(t, w, tc.req); got != tc.want {
				t.Fatalf("got:\n%q\nwant:\n%q", got, tc.want)
			}
		})
	}
	why := writePage(t, w, Request{Project: "shop", Crew: "idle", Why: true})
	if !strings.HasPrefix(why, "usage why shop idle\n") || !strings.HasSuffix(why, "\nno model calls recorded for idle; there is nothing to explain yet\n") {
		t.Fatalf("--why of a crew with no calls:\n%s", why)
	}
}
