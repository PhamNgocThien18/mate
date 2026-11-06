package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/matehost"
)

// `mate work` refuses the shapes it does not take before touching any
// log (CON-158).
func TestWorkArgs(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{},
		{"okay", "p", "1"},
		{"add", "p"},
		{"add", "p", " "},
		{"progress", "p", "1"},
		{"progress", "p", "x", "line"},
		{"wait", "p", "1", "line"},
		{"wait", "p", "1", "--topic", "0"},
		{"done", "p", "1", "a", "b"},
		{"list"},
	} {
		if err := cmdWork(args, io.Discard, io.Discard); err == nil {
			t.Errorf("%q: ran", args)
		}
	}
}

// The list shows open rows with the Mate's last line, the topic a waiting
// row waits on, and marks what the Mate proposed.
func TestWorkListLine(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 3, 15, 0, 0, 0, time.Local)
	got := workListLine(matehost.Work{ID: 3, Kind: matehost.WorkProposed, Title: "Dọn DerivedData", Status: matehost.WorkWaiting, Topic: 15, Last: "hỏi anh có làm không", At: now})
	want := "#3 waiting on news #15 proposed: Dọn DerivedData\n    hỏi anh có làm không\n"
	if got != want {
		t.Fatalf("line = %q, want %q", got, want)
	}
	var out bytes.Buffer
	if err := cmdWorkList([]string{"p", "--workspace", t.TempDir()}, &out, io.Discard); err == nil || !strings.Contains(err.Error(), "p") {
		t.Fatalf("list of an unknown project: %v", err)
	}
}
