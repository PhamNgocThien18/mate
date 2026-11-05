package store_test

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/store"
)

// One engine holds core.lock at a time; its record is JSON with the field
// names `mate core status` (CON-213) and the console read, rewritten once
// it knows where it serves; CoreRunning tells a held lock from a record
// left by an engine that has gone.
func TestOneEngineHoldsCoreLockAtATime(t *testing.T) {
	w := newWorkspace(t)
	if _, held, err := w.CoreRunning(); err != nil || held {
		t.Fatalf("CoreRunning before any engine = %v, %v; want not held", held, err)
	}
	started := time.Date(2026, 11, 5, 16, 0, 0, 0, time.UTC)
	holder := store.CoreHolder{PID: 4242, Owner: store.CoreOwnerCore, Started: started, Version: "v1-3-gabc", Binary: "/opt/homebrew/bin/mate", Build: "ab12"}
	lock, err := w.LockCore(holder)
	if err != nil {
		t.Fatalf("LockCore: %v", err)
	}
	if _, err := w.LockCore(store.CoreHolder{PID: 1, Owner: store.CoreOwnerConsole}); !errors.Is(err, store.ErrCoreRunning) {
		t.Fatalf("second LockCore = %v; want ErrCoreRunning", err)
	}
	holder.Socket, holder.URL, holder.TCPError = w.CoreSocket(), "http://127.0.0.1:7777/", ""
	if err := lock.Write(holder); err != nil {
		t.Fatalf("Write: %v", err)
	}
	h, held, err := w.CoreRunning()
	if err != nil || !held || h.PID != 4242 || h.Owner != store.CoreOwnerCore || !h.Started.Equal(started) || h.URL != holder.URL || h.Socket != w.CoreSocket() {
		t.Fatalf("CoreRunning = %+v, %v, %v; want the first engine's rewritten record", h, held, err)
	}
	data, err := os.ReadFile(w.CoreLockFile())
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("core.lock is not JSON: %v\n%s", err, data)
	}
	for _, key := range []string{"pid", "owner", "started", "version", "binary", "build", "socket", "url"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("core.lock has no %q: %s", key, data)
		}
	}

	lock.Unlock()
	if _, held, err := w.CoreRunning(); err != nil || held {
		t.Fatalf("CoreRunning after Unlock = %v, %v; want not held", held, err)
	}
	if h, err := w.CoreHolder(); err != nil || h.PID != 4242 {
		t.Fatalf("CoreHolder after Unlock = %+v, %v; the record stays", h, err)
	}
	again, err := w.LockCore(store.CoreHolder{PID: 7, Owner: store.CoreOwnerConsole})
	if err != nil {
		t.Fatalf("LockCore after Unlock: %v", err)
	}
	again.Unlock()
}

// ConsoleRunning tells a console holding its lock now, and whether it is
// one that takes core.lock (core_lock) or one built before CON-211.
func TestConsoleRunningSaysWhetherTheConsoleTakesCoreLock(t *testing.T) {
	w := newWorkspace(t)
	if _, held, err := w.ConsoleRunning(); err != nil || held {
		t.Fatalf("ConsoleRunning with no console = %v, %v", held, err)
	}
	unlock, err := w.LockConsole(store.ConsoleHolder{PID: 99, CoreLock: true})
	if err != nil {
		t.Fatal(err)
	}
	if h, held, err := w.ConsoleRunning(); err != nil || !held || h.PID != 99 || !h.CoreLock {
		t.Fatalf("ConsoleRunning = %+v, %v, %v; want console 99 taking core.lock", h, held, err)
	}
	unlock()
	if _, held, _ := w.ConsoleRunning(); held {
		t.Fatal("ConsoleRunning after unlock = held")
	}

	// What a console built before CON-211 writes: no core_lock.
	unlock, err = w.LockConsole(store.ConsoleHolder{PID: 98})
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	data, _ := os.ReadFile(w.ConsoleLockFile())
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["core_lock"]; ok {
		t.Fatalf("console.lock of an old console = %s, want no core_lock", data)
	}
	if h, held, _ := w.ConsoleRunning(); !held || h.CoreLock {
		t.Fatalf("ConsoleRunning = %+v, %v; want an old console", h, held)
	}
}
