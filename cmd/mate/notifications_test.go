package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/notifications"
	"github.com/nguyenngocanh94/mate/internal/store"
)

func TestNotificationSnapshotUsesOwedNewsAcrossProjects(t *testing.T) {
	ws, _ := consoleFixture(t, "shop")
	at := time.Now().UTC()
	path := matehost.NewsPath(ws.MateDir("shop"))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	id, err := matehost.OpenNews(path, matehost.News{Title: "cần key", Question: "cấp key?"}, at)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := notificationSnapshot(context.Background(), ws, "http://127.0.0.1:9999/", at)
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Complete || len(snap.Projects) != 1 || len(snap.Projects[0].News) != 1 {
		t.Fatalf("snapshot: %+v", snap)
	}
	if snap.URL != "http://127.0.0.1:9999/office/#captain" {
		t.Fatalf("wrong captain link: %s", snap.URL)
	}
	snap, err = notificationSnapshot(context.Background(), ws, "http://127.0.0.1:9999/ http://192.168.1.5:9999/", at)
	if err != nil {
		t.Fatal(err)
	}
	if snap.URL != "http://127.0.0.1:9999/office/#captain" {
		t.Fatalf("captain link with a LAN URL: %s", snap.URL)
	}
	if !strings.Contains(snap.Projects[0].News[0].Title, "cần key") {
		t.Fatal("news title missing")
	}
	if err := matehost.CloseNews(path, id, "resolved", false, at.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	snap, err = notificationSnapshot(context.Background(), ws, "", at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Projects[0].News) != 0 {
		t.Fatal("closed news must not notify")
	}
}

func TestNotificationSnapshotNewsReadFailureIsNotNoNews(t *testing.T) {
	ws, _ := consoleFixture(t, "shop")
	path := matehost.NewsPath(ws.MateDir("shop"))
	// A directory in place of the log reliably fails reads even as root.
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	snap, err := notificationSnapshot(context.Background(), ws, "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if snap.Complete {
		t.Fatal("unreadable news was reported as no news")
	}
}

// senderFunc is a notifications.Sender for tests.
type senderFunc func(context.Context, notifications.Message) error

func (f senderFunc) Send(ctx context.Context, m notifications.Message) error { return f(ctx, m) }

// A console on a machine that cannot notify (notifications.Supported is
// false, as on Linux without notify-send) has no sender and runs no
// notification loop; with a sender, the loop sends the news waiting for the
// captain through it.
func TestConsoleNotificationLoopNeedsASender(t *testing.T) {
	// No sender: it returns before it opens anything, so even a root that
	// is no workspace is no error, and no loop runs.
	stop, status, err := startNotificationLoop(context.Background(), t.TempDir(), "", nil)
	if err != nil {
		t.Fatalf("no sender: %v", err)
	}
	stop()
	if line := status(); line != "" {
		t.Fatalf("no sender, status line %q", line)
	}

	ws, _ := consoleFixture(t, "shop")
	path := matehost.NewsPath(ws.MateDir("shop"))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := matehost.OpenNews(path, matehost.News{Title: "cần key", Question: "cấp key?"}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	sent := make(chan notifications.Message, 1)
	stop, _, err = startNotificationLoop(context.Background(), ws.Root(), "http://127.0.0.1:7777/", senderFunc(func(_ context.Context, m notifications.Message) error {
		select {
		case sent <- m:
		default:
		}
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	select {
	case m := <-sent:
		if !strings.Contains(m.Title, "shop") || !strings.Contains(m.Body, "cần key") || m.URL != "http://127.0.0.1:7777/office/#captain" {
			t.Fatalf("sent %+v", m)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the loop sent no notification for the news waiting")
	}
}

// The console starts its notification loop exactly where
// notifications.Supported says this machine can notify: always on macOS, on
// Linux when notify-send is on PATH. A loop that starts opens the workspace
// first, so on a root that is no workspace it is refused with
// ErrNotWorkspace; one that does not start is no error.
func TestConsoleNotificationsFollowSupported(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	starts := func() bool {
		t.Helper()
		stop, _, err := startConsoleNotifications(context.Background(), t.TempDir(), "")
		if err == nil {
			stop()
			return false
		}
		if !errors.Is(err, store.ErrNotWorkspace) {
			t.Fatalf("console notifications: %v", err)
		}
		return true
	}
	if got, want := starts(), notifications.Supported(); got != want {
		t.Fatalf("no notify-send on PATH: loop started %v, Supported() %v", got, want)
	}
	if err := os.WriteFile(filepath.Join(bin, "notify-send"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, want := starts(), notifications.Supported(); got != want {
		t.Fatalf("notify-send on PATH: loop started %v, Supported() %v", got, want)
	}
}
