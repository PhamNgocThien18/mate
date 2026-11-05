package store_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/brief/brieftest"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// A crew's kind decides first; a meta without one falls back to the brief,
// where only a scout brief carries `## Deliverable`; no kind and no brief
// is a ship.
func TestCrewIsScoutReadsKindThenBrief(t *testing.T) {
	w := newWorkspace(t)
	writeBrief := func(crew, text string) {
		t.Helper()
		path := w.CrewBrief("shop", crew)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeBrief("sc", brieftest.Scout("Find out why the cart empties.", "What empties it?"))
	writeBrief("sh", brieftest.Ship("Fix the cart."))

	cases := []struct {
		name, crew, kind string
		want             bool
	}{
		{"kind scout", "sh", "scout", true},
		{"kind ship over a scout brief", "sc", "ship", false},
		{"no kind, scout brief", "sc", "", true},
		{"no kind, ship brief", "sh", "", false},
		{"no kind, no brief", "gone", "", false},
		{"unknown kind, scout brief", "sc", "other", true},
	}
	for _, c := range cases {
		meta := map[string]string{}
		if c.kind != "" {
			meta[store.MetaKind] = c.kind
		}
		if got := w.CrewIsScout("shop", c.crew, meta); got != c.want {
			t.Errorf("%s: CrewIsScout = %v, want %v", c.name, got, c.want)
		}
	}
	if w.CrewIsScout("shop", "gone", nil) {
		t.Error("nil meta and no brief: CrewIsScout = true, want false")
	}
}
