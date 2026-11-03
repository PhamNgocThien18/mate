package matehost

import (
	"path/filepath"
	"testing"
	"time"
)

// A row goes working → waiting → working → done → okayed; the Mate's ops
// refuse what the row is not in, and the captain's okay only a done row
// (CON-158).
func TestDutyLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), DutyName)
	now := time.Date(2026, 11, 3, 15, 0, 0, 0, time.Local)
	id, err := AddDuty(path, DutyAssigned, "Gộp make shot vào pre-push hook", "anh giao trong chat", now)
	if err != nil || id != 1 {
		t.Fatalf("add = %d, %v", id, err)
	}
	if id, _ := AddDuty(path, DutyProposed, "Dọn DerivedData", "", now); id != 2 {
		t.Fatalf("second id = %d", id)
	}
	if _, err := AddDuty(path, DutyAssigned, "", "", now); err == nil {
		t.Fatal("a row without a title was added")
	}
	if _, err := AddDuty(path, "wish", "x", "", now); err == nil {
		t.Fatal("a row of an unknown kind was added")
	}
	row := func(id int) Duty {
		list, err := LoadDuty(path)
		if err != nil {
			t.Fatal(err)
		}
		w, _ := findDuty(list, id)
		return w
	}
	if w := row(1); w.Status != DutyWorking || w.Kind != DutyAssigned || w.Last != "anh giao trong chat" {
		t.Fatalf("after add: %+v", w)
	}
	if w := row(2); w.Kind != DutyProposed {
		t.Fatalf("second kind = %s", w.Kind)
	}

	if err := OkayDuty(path, 1, now); err == nil {
		t.Fatal("a working row was okayed")
	}
	if err := ReopenDuty(path, 1, "", now); err == nil {
		t.Fatal("a working row was reopened")
	}
	if err := ProgressDuty(path, 1, "", now); err == nil {
		t.Fatal("progress without a line")
	}
	if err := WaitDuty(path, 1, 0, "", now); err == nil {
		t.Fatal("wait without a topic")
	}
	if err := WaitDuty(path, 1, 14, "cần anh chọn 4 hay 6 lane", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if w := row(1); w.Status != DutyWaiting || w.Topic != 14 || w.Last != "cần anh chọn 4 hay 6 lane" {
		t.Fatalf("after wait: %+v", w)
	}
	if err := ProgressDuty(path, 1, "anh chọn 6, crew hu-258 mở PR", now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if w := row(1); w.Status != DutyWorking || w.Topic != 0 {
		t.Fatalf("after progress: %+v", w)
	}
	if err := DoneDuty(path, 1, "PR #232 merged", now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if w := row(1); w.Status != DutyDone || w.DoneAt.IsZero() || len(w.Notes) != 4 {
		t.Fatalf("after done: %+v", w)
	}
	if err := OkayDuty(path, 1, now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if w := row(1); w.Status != DutyOkayed || w.OkayedAt.IsZero() || w.Open() {
		t.Fatalf("after okay: %+v", w)
	}
	for _, err := range []error{
		ProgressDuty(path, 1, "x", now), WaitDuty(path, 1, 2, "", now), DoneDuty(path, 1, "", now), OkayDuty(path, 1, now),
	} {
		if err == nil {
			t.Fatal("an okayed row took a Mate op or a second okay")
		}
	}
	if err := ReopenDuty(path, 1, "hook không chạy trên CI", now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if w := row(1); w.Status != DutyWorking || !w.OkayedAt.IsZero() || !w.DoneAt.IsZero() || w.Last != "hook không chạy trên CI" {
		t.Fatalf("after reopen: %+v", w)
	}
	if err := ProgressDuty(path, 9, "x", now); err == nil {
		t.Fatal("an unknown id took progress")
	}
}

// A missing log is an empty board, not an error.
func TestLoadDutyMissing(t *testing.T) {
	list, err := LoadDuty(filepath.Join(t.TempDir(), DutyName))
	if err != nil || len(list) != 0 {
		t.Fatalf("missing log: %v, %v", list, err)
	}
}
