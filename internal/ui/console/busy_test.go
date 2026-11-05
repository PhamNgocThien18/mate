package console

import (
	"testing"

	"github.com/nguyenngocanh94/mate/internal/coreapi"
)

// busyOn is the core's refusal of an action on a target another action,
// from this console or another UI, is still running on (CON-209).
func busyOn(running coreapi.Action, target string) error {
	return &coreapi.BusyError{Running: running, Target: target}
}

// An assign the core refused because the crew is busy shows the core's
// line verbatim: not "Assign refused: …", which would read as a composer
// that refused the line, and nothing around it.
func TestABusyAssignShowsTheCoresLineVerbatim(t *testing.T) {
	m, act, _ := bxPayments(t, 40, 36)
	act.err = busyOn(coreapi.ActionMerge, "payments-api/k3")
	m = bxFocusBox(t, m)
	m, cmd := send(t, m, key("a"))
	if cmd == nil {
		t.Fatal("a on the box ran nothing")
	}
	m, _ = send(t, m, cmd())
	if len(act.reqs) != 1 || act.reqs[0].Action != coreapi.ActionResolve {
		t.Fatalf("requests = %+v, want one resolve", act.reqs)
	}
	want := "Action refused: merge is still running on payments-api/k3 · nothing started"
	if m.msg.tone != toneError || m.msg.text != want {
		t.Fatalf("message = %+v, want the error %q", m.msg, want)
	}
}

// Any other action the core refused as busy shows the same line, not
// "Action failed: …": nothing ran, so nothing failed.
func TestABusyActionShowsTheCoresLineVerbatim(t *testing.T) {
	m, act, _ := bxPayments(t, 40, 36)
	act.err = busyOn(coreapi.ActionRestartMate, "payments-api")
	m, cmd := send(t, m, key("m"))
	if cmd == nil {
		t.Fatal("m ran nothing")
	}
	m, _ = send(t, m, cmd())
	if len(act.reqs) != 1 || act.reqs[0].Action != coreapi.ActionMode {
		t.Fatalf("requests = %+v, want one mode", act.reqs)
	}
	want := "Action refused: restart_mate is still running on payments-api · nothing started"
	if m.msg.tone != toneError || m.msg.text != want {
		t.Fatalf("message = %+v, want the error %q", m.msg, want)
	}
}
