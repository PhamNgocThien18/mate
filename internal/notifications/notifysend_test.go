package notifications

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"testing"
)

// notifySend runs on Linux only, but it is plain Go: these tests run
// wherever make check does.

func TestNotifySendSendsTitleAndBodyAsArguments(t *testing.T) {
	var got []string
	n := notifySend{
		lookPath: func(file string) (string, error) {
			if file != "notify-send" {
				t.Errorf("looked up %q, want notify-send", file)
			}
			return "/usr/bin/notify-send", nil
		},
		run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			if _, bounded := ctx.Deadline(); !bounded {
				t.Error("notify-send ran without a time bound")
			}
			got = append([]string{name}, args...)
			return nil, nil
		},
	}
	if !n.available() {
		t.Fatal("notify-send on PATH is not available")
	}
	// A body that starts like an option stays the body: "--" ends notify-send's options.
	m := Message{ID: "news-1", Title: "Mate · shop · cần anh xem", Body: "-#1 · cần key\nMở Mate, nhấn Ctrl+T để xem và trả lời news.", URL: "http://127.0.0.1:7777/office/#captain"}
	if err := n.send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if want := []string{"/usr/bin/notify-send", "--", m.Title, m.Body}; !slices.Equal(got, want) {
		t.Fatalf("ran %q, want %q", got, want)
	}
}

func TestNotifySendMissingIsUnsupported(t *testing.T) {
	n := notifySend{
		lookPath: func(string) (string, error) { return "", exec.ErrNotFound },
		run: func(context.Context, string, ...string) ([]byte, error) {
			t.Fatal("ran a notifier that is not on PATH")
			return nil, nil
		},
	}
	if n.available() {
		t.Fatal("available without notify-send on PATH")
	}
	err := n.send(context.Background(), Message{Title: "Mate · thử notification", Body: "x"})
	if !errors.Is(err, ErrUnsupported) || err.Error() != "native Mate notifications need macOS, or notify-send on Linux" {
		t.Fatalf("send without notify-send: %v", err)
	}
}

// Over ssh, notify-send can be installed with no D-Bus session to reach: the
// console's status line shows why, and the loop tries again a minute later.
func TestNotifySendFailureSaysWhy(t *testing.T) {
	n := notifySend{
		lookPath: func(string) (string, error) { return "/usr/bin/notify-send", nil },
		run: func(context.Context, string, ...string) ([]byte, error) {
			return []byte("Cannot autolaunch D-Bus without X11 $DISPLAY\n"), errors.New("exit status 1")
		},
	}
	err := n.send(context.Background(), Message{Title: "t", Body: "b"})
	if err == nil || err.Error() != "notify-send: exit status 1: Cannot autolaunch D-Bus without X11 $DISPLAY" {
		t.Fatalf("failed send: %v", err)
	}
}
