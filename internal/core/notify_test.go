package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/notifications"
)

// The notification loop moved here from cmd/mate with its tests (CON-208).

func TestNotificationSnapshotUsesOwedNewsAcrossProjects(t *testing.T) {
	ws := shopWorkspace(t)
	at := time.Now().UTC()
	path := matehost.NewsPath(ws.MateDir("shop"))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	id, err := matehost.OpenNews(path, matehost.News{Title: "cần key", Question: "cấp key?"}, at)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := notificationSnapshot(context.Background(), ws, testHarnesses, "http://127.0.0.1:9999/", at)
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Complete || len(snap.Projects) != 1 || len(snap.Projects[0].News) != 1 {
		t.Fatalf("snapshot: %+v", snap)
	}
	if snap.URL != "http://127.0.0.1:9999/office/#captain" {
		t.Fatalf("wrong captain link: %s", snap.URL)
	}
	snap, err = notificationSnapshot(context.Background(), ws, testHarnesses, "http://127.0.0.1:9999/ http://192.168.1.5:9999/", at)
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
	snap, err = notificationSnapshot(context.Background(), ws, testHarnesses, "", at.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Projects[0].News) != 0 {
		t.Fatal("closed news must not notify")
	}
}

func TestNotificationSnapshotNewsReadFailureIsNotNoNews(t *testing.T) {
	ws := shopWorkspace(t)
	path := matehost.NewsPath(ws.MateDir("shop"))
	// A directory in place of the log reliably fails reads even as root.
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	snap, err := notificationSnapshot(context.Background(), ws, testHarnesses, "", time.Now())
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

// An engine with no notifier (NativeNotifier is nil on a machine that cannot
// notify, as on Linux without notify-send) runs no notification loop; with
// one, the loop sends the news waiting for the captain through it, linking
// to the captain's desk of the dashboard the engine serves.
func TestEngineNotifiesOnlyWithANotifier(t *testing.T) {
	ws := shopWorkspace(t)
	quiet := New(Options{Workspace: ws})
	if err := quiet.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if line, err := quiet.Notifications(); line != "" || err != nil {
		t.Fatalf("no notifier: %q, %v; want nothing", line, err)
	}
	quiet.Stop()

	path := matehost.NewsPath(ws.MateDir("shop"))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := matehost.OpenNews(path, matehost.News{Title: "cần key", Question: "cấp key?"}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	withTimelineDB(t, ws)
	sent := make(chan notifications.Message, 1)
	e := New(Options{
		Workspace: ws,
		Harnesses: testHarnesses,
		Dashboard: &DashboardOptions{Addr: freeAddr},
		Notifier: senderFunc(func(_ context.Context, m notifications.Message) error {
			select {
			case sent <- m:
			default:
			}
			return nil
		}),
	})
	if err := e.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer e.Stop()
	url, err := e.Dashboard()
	if err != nil {
		t.Fatalf("dashboard: %v", err)
	}
	select {
	case m := <-sent:
		want := strings.TrimRight(strings.Fields(url)[0], "/") + "/office/#captain"
		if !strings.Contains(m.Title, "shop") || !strings.Contains(m.Body, "cần key") || m.URL != want {
			t.Fatalf("sent %+v, want shop's news linking to %s", m, want)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the loop sent no notification for the news waiting")
	}
	if line, err := e.Notifications(); line != "" || err != nil {
		t.Fatalf("a loop that sends says %q, %v; want nothing", line, err)
	}
}

// `.mate/.env` turns the loop off without a word, and a value it does not
// know keeps the loop from starting with the reason - never the engine.
func TestEngineNotificationsFollowTheWorkspaceEnv(t *testing.T) {
	never := senderFunc(func(context.Context, notifications.Message) error {
		t.Error("a notification was sent")
		return nil
	})
	for _, tc := range []struct{ env, err string }{
		{"MATE_NOTIFICATIONS=off\n", ""},
		{"MATE_NOTIFICATIONS=sometimes\n", "MATE_NOTIFICATIONS must be on or off"},
	} {
		ws := shopWorkspace(t)
		if err := os.WriteFile(ws.EnvFile(), []byte(tc.env), 0o600); err != nil {
			t.Fatal(err)
		}
		e := New(Options{Workspace: ws, Notifier: never})
		if err := e.Start(context.Background()); err != nil {
			t.Fatalf("%q: Start = %v; the notifications never fail the engine", tc.env, err)
		}
		_, err := e.Notifications()
		got := ""
		if err != nil {
			got = err.Error()
		}
		if got != tc.err {
			t.Errorf("%q: error %q, want %q", tc.env, got, tc.err)
		}
		if e.notify.cancel != nil {
			t.Errorf("%q: the loop runs", tc.env)
		}
		e.Stop()
	}
}

// NativeNotifier is there exactly where notifications.Supported says this
// machine can notify: always on macOS, on Linux when notify-send is on PATH.
func TestNativeNotifierFollowsSupported(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	if got, want := NativeNotifier() != nil, notifications.Supported(); got != want {
		t.Fatalf("no notify-send on PATH: notifier %v, Supported() %v", got, want)
	}
	if err := os.WriteFile(filepath.Join(bin, "notify-send"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, want := NativeNotifier() != nil, notifications.Supported(); got != want {
		t.Fatalf("notify-send on PATH: notifier %v, Supported() %v", got, want)
	}
}
