package spawn

import (
	"testing"

	"github.com/nguyenngocanh94/mate/internal/store"
)

// A start that names no mode takes project.yaml's, then the last run's,
// then Deps.DefaultMode (CON-220); a harness without headless turns always
// runs in the pane unless the start asked for headless, which is refused.
// StartRequest.HeadlessOnly overrides all but the request's mode, and says
// so when it overrides project.yaml's pane.
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
		note     bool
		err      bool
	}{
		{name: "default headless", fallback: ModeHeadless, can: true, want: true},
		{name: "default pane", fallback: "", can: true, want: false},
		{name: "last run pane", prior: ModePane, fallback: ModeHeadless, can: true, want: false},
		{name: "last run headless", prior: ModeHeadless, can: true, want: true},
		{name: "config pane", cfg: store.MateConfig{Mode: ModePane}, fallback: ModeHeadless, can: true, want: false},
		{name: "config headless", cfg: store.MateConfig{Mode: ModeHeadless}, can: true, want: true},
		{name: "old config headless", cfg: store.MateConfig{Headless: true}, can: true, want: true},
		{name: "config pane beats old config headless", cfg: store.MateConfig{Mode: ModePane, Headless: true}, fallback: ModeHeadless, can: true, want: false},
		{name: "config pane beats last run headless", prior: ModeHeadless, cfg: store.MateConfig{Mode: ModePane}, fallback: ModeHeadless, can: true, want: false},
		{name: "config headless beats last run pane", prior: ModePane, cfg: store.MateConfig{Mode: ModeHeadless}, can: true, want: true},
		{name: "old config headless beats last run pane", prior: ModePane, cfg: store.MateConfig{Headless: true}, can: true, want: true},
		{name: "empty config keeps last run pane", prior: ModePane, fallback: ModeHeadless, can: true, want: false},
		{name: "empty config keeps last run headless", prior: ModeHeadless, fallback: ModePane, can: true, want: true},
		{name: "request beats last run", req: ModePane, prior: ModeHeadless, can: true, want: false},
		{name: "request headless beats config pane", req: ModeHeadless, cfg: store.MateConfig{Mode: ModePane}, can: true, want: true},
		{name: "request pane beats config headless", req: ModePane, cfg: store.MateConfig{Mode: ModeHeadless}, fallback: ModeHeadless, can: true, want: false},
		{name: "no headless turns falls back", prior: ModeHeadless, fallback: ModeHeadless, can: false, want: false},
		{name: "no headless turns ignores config headless", cfg: store.MateConfig{Mode: ModeHeadless}, can: false, want: false},
		{name: "no headless turns refuses the request", req: ModeHeadless, can: false, err: true},
		{name: "bad request", req: "tui", can: true, err: true},
		{name: "headless only beats last run", only: true, prior: ModePane, can: true, want: true},
		{name: "headless only beats config pane and says so", only: true, prior: ModeHeadless, cfg: store.MateConfig{Mode: ModePane}, can: true, want: true, note: true},
		{name: "headless only with config headless says nothing", only: true, cfg: store.MateConfig{Mode: ModeHeadless}, can: true, want: true},
		{name: "headless only keeps the request", only: true, req: ModePane, cfg: store.MateConfig{Mode: ModePane}, can: true, want: false},
		{name: "headless only without headless turns", only: true, can: false, want: false},
		{name: "headless only without headless turns keeps config pane", only: true, cfg: store.MateConfig{Mode: ModePane}, can: false, want: false},
		{name: "bad config", cfg: store.MateConfig{Mode: "tui"}, can: true, err: true},
	}
	for _, c := range cases {
		got, note, err := headlessMode(StartRequest{Mode: c.req, HeadlessOnly: c.only}, map[string]string{MetaMode: c.prior}, c.cfg, c.fallback, c.can)
		if (err != nil) != c.err || got != c.want || (note != "") != c.note {
			t.Errorf("%s: headless = %v, note %q, err %v; want %v, note %v, err %v", c.name, got, note, err, c.want, c.note, c.err)
		}
	}
}
