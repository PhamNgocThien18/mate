package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/matehost"
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
