package matehost

import (
	"path/filepath"
	"testing"
	"time"
)

// A row goes working → waiting → working → done → okayed; the Mate's ops
// refuse what the row is not in, and the captain's okay only a done row
// (CON-158).
func TestWorkLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), WorkName)
	now := time.Date(2026, 11, 3, 15, 0, 0, 0, time.Local)
	id, err := AddWork(path, WorkAssigned, "Gộp make shot vào pre-push hook", "anh giao trong chat", now)
	if err != nil || id != 1 {
		t.Fatalf("add = %d, %v", id, err)
	}
	if id, _ := AddWork(path, WorkProposed, "Dọn DerivedData", "", now); id != 2 {
		t.Fatalf("second id = %d", id)
	}
	if _, err := AddWork(path, WorkAssigned, "", "", now); err == nil {
		t.Fatal("a row without a title was added")
	}
	if _, err := AddWork(path, "wish", "x", "", now); err == nil {
		t.Fatal("a row of an unknown kind was added")
	}
	row := func(id int) Work {
		list, err := LoadWork(path)
		if err != nil {
			t.Fatal(err)
		}
		w, _ := findWork(list, id)
		return w
	}
	if w := row(1); w.Status != WorkWorking || w.Kind != WorkAssigned || w.Last != "anh giao trong chat" {
		t.Fatalf("after add: %+v", w)
	}
	if w := row(2); w.Kind != WorkProposed {
		t.Fatalf("second kind = %s", w.Kind)
	}

	if err := OkayWork(path, 1, now); err == nil {
		t.Fatal("a working row was okayed")
	}
	if err := ReopenWork(path, 1, "", now); err == nil {
		t.Fatal("a working row was reopened")
	}
	if err := ProgressWork(path, 1, "", now); err == nil {
		t.Fatal("progress without a line")
	}
	if err := WaitWork(path, 1, 0, "", now); err == nil {
		t.Fatal("wait without a topic")
	}
	if err := WaitWork(path, 1, 14, "cần anh chọn 4 hay 6 lane", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if w := row(1); w.Status != WorkWaiting || w.Topic != 14 || w.Last != "cần anh chọn 4 hay 6 lane" {
		t.Fatalf("after wait: %+v", w)
	}
	if err := ProgressWork(path, 1, "anh chọn 6, crew hu-258 mở PR", now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if w := row(1); w.Status != WorkWorking || w.Topic != 0 {
		t.Fatalf("after progress: %+v", w)
	}
	if err := DoneWork(path, 1, "PR #232 merged", now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if w := row(1); w.Status != WorkDone || w.DoneAt.IsZero() || len(w.Notes) != 4 {
		t.Fatalf("after done: %+v", w)
	}
	if err := OkayWork(path, 1, now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if w := row(1); w.Status != WorkOkayed || w.OkayedAt.IsZero() || w.Open() {
		t.Fatalf("after okay: %+v", w)
	}
	for _, err := range []error{
		ProgressWork(path, 1, "x", now), WaitWork(path, 1, 2, "", now), DoneWork(path, 1, "", now), OkayWork(path, 1, now),
	} {
		if err == nil {
			t.Fatal("an okayed row took a Mate op or a second okay")
		}
	}
	if err := ReopenWork(path, 1, "hook không chạy trên CI", now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if w := row(1); w.Status != WorkWorking || !w.OkayedAt.IsZero() || !w.DoneAt.IsZero() || w.Last != "hook không chạy trên CI" {
		t.Fatalf("after reopen: %+v", w)
	}
	if err := ProgressWork(path, 9, "x", now); err == nil {
		t.Fatal("an unknown id took progress")
	}
}

// A missing log is an empty board, not an error.
func TestLoadWorkMissing(t *testing.T) {
	list, err := LoadWork(filepath.Join(t.TempDir(), WorkName))
	if err != nil || len(list) != 0 {
		t.Fatalf("missing log: %v, %v", list, err)
	}
}
