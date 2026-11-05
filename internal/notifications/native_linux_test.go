package notifications

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Supported and Native follow the notify-send on PATH, and Native hands it
// the title and the body as they are.
func TestLinuxNotifiesThroughNotifySendOnPath(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	if Supported() {
		t.Fatal("Supported() with no notify-send on PATH")
	}
	if err := (Native{}).Send(context.Background(), Message{Title: "t", Body: "b"}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("Send with no notify-send on PATH: %v", err)
	}

	// A notify-send that writes its arguments down, NUL after each.
	record := filepath.Join(t.TempDir(), "args")
	script := "#!/bin/sh\nprintf '%s\\0' \"$@\" > '" + record + "'\n"
	if err := os.WriteFile(filepath.Join(bin, "notify-send"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if !Supported() {
		t.Fatal("Supported() is false with notify-send on PATH")
	}
	m := Message{ID: "news-1", Title: "Mate · shop · cần anh xem", Body: "#1 · cần key\nMở Mate, nhấn Ctrl+T để xem và trả lời news.", URL: "http://127.0.0.1:7777/office/#captain"}
	if err := (Native{}).Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00")
	if want := []string{"--", m.Title, m.Body}; !slices.Equal(got, want) {
		t.Fatalf("notify-send got %q, want %q", got, want)
	}
}

func TestLinuxTestNotificationSaysWhatNotifySendTook(t *testing.T) {
	want := "notify-send accepted Mate's test notification. If no banner appeared, check that a notification daemon is running."
	if got := AcceptedNote(); got != want {
		t.Fatalf("AcceptedNote() = %q, want %q", got, want)
	}
}
