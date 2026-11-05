package dashboard

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/db"
	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// TestChatShowsWhatTheHostHolds: GET /chat carries what the Mate's host
// holds for its undo (CON-222), whichever UI sent it, with how long until
// it goes as of the answer, so a page counts it down by its own clock.
func TestChatShowsWhatTheHostHolds(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "dash")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	ws, err := store.Init(root, store.Defaults{MateHarness: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.AddProject("shop", store.ProjectConfig{}); err != nil {
		t.Fatal(err)
	}
	if err := ws.WriteMateMeta("shop", map[string]string{"harness": "claude", "agent": "mate-shop", store.MetaMateMode: store.MateModeHeadless}); err != nil {
		t.Fatal(err)
	}
	dir := ws.MateDir("shop")
	at := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	// The host's timers never run: what it holds stays held.
	host := matehost.New(&matehost.Runner{}, matehost.Options{History: matehost.HistoryPath(dir), News: matehost.NewsPath(dir),
		Now:   func() time.Time { return at },
		After: func(time.Duration, func()) func() bool { return func() bool { return true } }})
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- host.Serve(ctx, matehost.SocketPath(dir), nil) }()
	t.Cleanup(func() { cancel(); <-served })
	c := matehost.Dial(dir)
	deadline := time.Now().Add(5 * time.Second)
	for c.Ping(ctx) != nil {
		if time.Now().After(deadline) {
			t.Fatal("the host never answered")
		}
		time.Sleep(10 * time.Millisecond)
	}
	held, err := c.Submit(ctx, "từ Office")
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{ws: ws, deps: Deps{Now: func() time.Time { return at.Add(1500 * time.Millisecond) }}}
	r := httptest.NewRequest("GET", "/api/projects/shop/chat", nil)
	r.SetPathValue("project", "shop")
	got, err := s.handleChat(ctx, r, envelope{})
	if err != nil {
		t.Fatal(err)
	}
	chat := got.(ChatResponse)
	want := ChatHeld{ID: held.ID, Kind: matehost.HeldSay, Text: "từ Office", At: db.FormatTime(at), Due: db.FormatTime(at.Add(matehost.UndoDelay)), LeftMS: 3500}
	if len(chat.Held) != 1 || chat.Held[0] != want {
		t.Fatalf("held = %+v, want %+v", chat.Held, want)
	}
}

// TestChatHistoryIsTheConversation: the City's and the Office's chat is
// what the REPL shows (CON-151): the captain's messages outside a news,
// the newest n of them, however many app turns came in between.
func TestChatHistoryIsTheConversation(t *testing.T) {
	at := time.Date(2026, 11, 1, 22, 0, 0, 0, time.UTC)
	var items []matehost.Item
	for i := range 5 {
		items = append(items, matehost.Item{At: at, Source: matehost.SourceCaptain, Text: "hỏi " + string(rune('a'+i)), Reply: "đáp", Done: true})
		for range 30 {
			items = append(items, matehost.Item{At: at, Source: matehost.SourceApp, Label: "digest", Text: "digest", Reply: "HU-165 is done.", Done: true})
		}
	}
	items = append(items,
		matehost.Item{At: at, Source: matehost.SourceApp, Label: "stow", Reply: "Everything is saved.", Done: true},
		matehost.Item{At: at, Source: matehost.SourceCaptain, Text: "trả lời topic", News: 7, Done: true})
	var b strings.Builder
	for _, it := range items {
		data, err := json.Marshal(it)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(append(data, '\n'))
	}
	path := filepath.Join(t.TempDir(), matehost.HistoryName)
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	got := chatHistory(path, 3)
	var texts []string
	for _, it := range got {
		texts = append(texts, it.Text)
	}
	if strings.Join(texts, ",") != "hỏi c,hỏi d,hỏi e" {
		t.Fatalf("chat = %q, want the last three captain messages", texts)
	}
}
