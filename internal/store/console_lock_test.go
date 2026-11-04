package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/store"
)

func TestOneConsoleHoldsTheWorkspaceAtATime(t *testing.T) {
	w := newWorkspace(t)
	started := time.Date(2026, 11, 4, 15, 36, 8, 0, time.UTC)

	unlock, err := w.LockConsole(store.ConsoleHolder{PID: 43674, Started: started})
	if err != nil {
		t.Fatalf("first LockConsole: %v", err)
	}
	if _, err := w.LockConsole(store.ConsoleHolder{PID: 21958}); !errors.Is(err, store.ErrConsoleRunning) {
		t.Fatalf("second LockConsole while held = %v; want ErrConsoleRunning", err)
	}
	h, err := w.ConsoleHolder()
	if err != nil {
		t.Fatalf("ConsoleHolder: %v", err)
	}
	if h.PID != 43674 || !h.Started.Equal(started) {
		t.Fatalf("holder = %+v; the refused console must not overwrite the first one's record", h)
	}

	unlock()
	unlock2, err := w.LockConsole(store.ConsoleHolder{PID: 21958})
	if err != nil {
		t.Fatalf("LockConsole after the first released it: %v", err)
	}
	defer unlock2()
	if h, err := w.ConsoleHolder(); err != nil || h.PID != 21958 {
		t.Fatalf("holder after takeover = %+v, %v; want only the new console's record", h, err)
	}
}
