package spawn

import (
	"testing"

	"github.com/nguyenngocanh94/mate/internal/store"
)

// A start that names no mode takes the last run's, then project.yaml's,
// then Deps.DefaultMode; a harness without headless turns always runs in
// the pane unless the start asked for headless, which is refused.
// Deps.HeadlessOnly overrides all but the request.
func TestHeadlessModeOrder(t *testing.T) {
	cases := []struct {
		name     string
		req      string
		prior    string
		cfg      store.MateConfig
		fallback string
		only     bool
		can      bool
		want     bool
		err      bool
	}{
		{name: "default headless", fallback: ModeHeadless, can: true, want: true},
		{name: "default pane", fallback: "", can: true, want: false},
		{name: "last run pane", prior: ModePane, fallback: ModeHeadless, can: true, want: false},
		{name: "last run headless", prior: ModeHeadless, can: true, want: true},
		{name: "config pane", cfg: store.MateConfig{Mode: ModePane}, fallback: ModeHeadless, can: true, want: false},
		{name: "old config headless", cfg: store.MateConfig{Headless: true}, can: true, want: true},
		{name: "request beats last run", req: ModePane, prior: ModeHeadless, can: true, want: false},
		{name: "no headless turns falls back", prior: ModeHeadless, fallback: ModeHeadless, can: false, want: false},
		{name: "no headless turns refuses the request", req: ModeHeadless, can: false, err: true},
		{name: "bad request", req: "tui", can: true, err: true},
		{name: "headless only beats last run", only: true, prior: ModePane, cfg: store.MateConfig{Mode: ModePane}, can: true, want: true},
		{name: "headless only keeps the request", only: true, req: ModePane, can: true, want: false},
		{name: "headless only without headless turns", only: true, can: false, want: false},
		{name: "bad config", cfg: store.MateConfig{Mode: "tui"}, can: true, err: true},
	}
	for _, c := range cases {
		got, err := headlessMode(StartRequest{Mode: c.req}, map[string]string{MetaMode: c.prior}, c.cfg, c.fallback, c.only, c.can)
		if (err != nil) != c.err || got != c.want {
			t.Errorf("%s: headless = %v, err %v; want %v, err %v", c.name, got, err, c.want, c.err)
		}
	}
}
