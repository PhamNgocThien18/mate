package dashboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nguyenngocanh94/mate/internal/matehost"
)

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
