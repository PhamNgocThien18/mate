package outbox_test

import (
	"context"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/outbox"
	"github.com/nguyenngocanh94/mate/internal/send"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// The loop with no console open (CON-211, CON-203 decided 2026-11-05): an
// Auto project's Mate still gets its lines, a Manual project's waits for a
// console, and a line tried by itself (Attempt: an `assign`, `mate pr
// watch`) goes out as it always has.

// senderWithConsole is f's sender whose loop asks present whether a console
// is open.
func (f *fixture) senderWithConsole(present bool) *outbox.Sender {
	f.t.Helper()
	ws, err := store.Open(f.ws.Root())
	if err != nil {
		f.t.Fatalf("store.Open: %v", err)
	}
	deps := f.deps()
	deps.ConsolePresent = func() bool { return present }
	return outbox.New(ws, deps)
}

func (f *fixture) setAuto(on bool) {
	f.t.Helper()
	if err := f.ws.SetMode(project, on); err != nil {
		f.t.Fatal(err)
	}
}

func TestWithNoConsoleTheLoopStillDeliversToAnAutoProject(t *testing.T) {
	f := newFixture(t)
	s := f.senderWithConsole(false)
	f.assign(s, "crews/k3.status@0", resolveLine)
	f.drain(s)
	if typed := f.typed(); len(typed) != 1 || typed[0] != send.Marker+resolveLine {
		t.Fatalf("typed %#v, want the line delivered to the Auto project's Mate", typed)
	}
}

func TestWithNoConsoleTheLoopLeavesAManualProjectsLineQueued(t *testing.T) {
	f := newFixture(t)
	f.setAuto(false)
	s := f.senderWithConsole(false)
	f.assign(s, "crews/k3.status@0", resolveLine)
	f.drain(s)
	f.drain(s)
	if typed := f.typed(); len(typed) != 0 {
		t.Fatalf("typed %#v into a Manual project's Mate with no console open", typed)
	}
	if items := f.items(); len(items) != 1 || !items[0].Queued() || items[0].Attempts != 0 {
		t.Fatalf("items = %+v, want the line still queued and untried", items)
	}

	// Tried by itself - the assign's own immediate attempt - it goes out.
	got, err := s.Attempt(context.Background(), project)
	if err != nil || !got.Delivered {
		t.Fatalf("Attempt = %+v, %v; want it delivered", got, err)
	}
	if typed := f.typed(); len(typed) != 1 {
		t.Fatalf("typed %#v, want the line once", typed)
	}
}

// A digest queued before the captain took the composer is withdrawn as it
// is with a console, not left to hold every line behind it.
func TestWithNoConsoleAManualProjectsDigestIsStillWithdrawn(t *testing.T) {
	f := newFixture(t)
	s := f.senderWithConsole(false)
	if _, err := s.Enqueue(project, outbox.Request{Source: store.OutboxSourceDigest, Key: "digest@1", Text: "digest: 1 item(s)"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	f.setAuto(false)
	f.drain(s)
	if typed := f.typed(); len(typed) != 0 {
		t.Fatalf("typed %#v, want nothing", typed)
	}
	if items := f.items(); len(items) != 1 || items[0].State != store.OutboxDropped {
		t.Fatalf("items = %+v, want the digest dropped", items)
	}
}

func TestWithAConsoleTheLoopDeliversToBothModes(t *testing.T) {
	for _, auto := range []bool{true, false} {
		f := newFixture(t)
		f.setAuto(auto)
		s := f.senderWithConsole(true)
		f.assign(s, "crews/k3.status@0", resolveLine)
		f.drain(s)
		if typed := f.typed(); len(typed) != 1 {
			t.Fatalf("auto=%v: typed %#v, want the line delivered with a console open", auto, typed)
		}
	}
}
