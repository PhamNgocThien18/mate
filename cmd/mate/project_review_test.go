package main

import (
	"os"
	"strings"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/store"
)

// `mate project review` prints the setting, records on or off, and refuses
// anything else; wake's hand-back item carries the same word (CON-101).
func TestProjectReviewRecordsTheCaptainsChoice(t *testing.T) {
	t.Parallel()
	w := wakeFixture(t)
	code, out, _ := runMate(t, "project", "review", "shop", "--workspace", w.Root())
	if code != 0 || !strings.Contains(out, "review: undecided - ask the captain") {
		t.Fatalf("unset: exit %d, %q", code, out)
	}

	code, out, _ = runMate(t, "project", "review", "shop", "off", "--workspace", w.Root())
	if code != 0 || !strings.HasPrefix(out, "review: off") {
		t.Fatalf("off: exit %d, %q", code, out)
	}
	if cfg, err := w.LoadProject("shop"); err != nil || cfg.Review != store.ReviewOff {
		t.Fatalf("project.yaml review = %q, %v; want off", cfg.Review, err)
	}
	if code, _, _ = runMate(t, "project", "review", "shop", "maybe", "--workspace", w.Root()); code == 0 {
		t.Fatal("a value other than on or off was accepted")
	}
	if cfg, _ := w.LoadProject("shop"); cfg.Review != store.ReviewOff {
		t.Fatalf("a refused value changed the setting to %q", cfg.Review)
	}
}

func TestWakeSaysWhatTheProjectsReviewSettingAsks(t *testing.T) {
	t.Parallel()
	for review, want := range map[string]string{
		"":              "reviews: none yet; project review undecided: ask the captain",
		store.ReviewOn:  "reviews: none yet; project review on: start one",
		store.ReviewOff: "reviews: none yet; project review off: land once the hand-back checks out",
	} {
		w := wakeFixture(t)
		// Without its reviewer, k3's hand-back has no review yet.
		if err := os.Remove(w.CrewReviewMeta("shop", "rev-k3")); err != nil {
			t.Fatal(err)
		}
		cfg, err := w.LoadProject("shop")
		if err != nil {
			t.Fatal(err)
		}
		cfg.Review = review
		if err := w.SaveProject("shop", cfg); err != nil {
			t.Fatal(err)
		}
		if text := runWake(t, w); !strings.Contains(text, want) {
			t.Errorf("review=%q: wake lacks %q:\n%s", review, want, text)
		}
	}
}
