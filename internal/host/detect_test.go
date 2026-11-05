package host

import (
	"runtime"
	"strings"
	"testing"
)

func TestDetectFor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		goos string
		env  map[string]string
		want Kind
	}{
		{name: "empty", goos: "darwin", want: None},
		{
			name: "wezterm pane",
			goos: "darwin",
			env:  map[string]string{"WEZTERM_PANE": "12"},
			want: WezTerm,
		},
		{
			name: "wezterm pane on linux",
			goos: "linux",
			env:  map[string]string{"WEZTERM_PANE": "12"},
			want: WezTerm,
		},
		{
			name: "wezterm wins over ghostty",
			goos: "darwin",
			env:  map[string]string{"WEZTERM_PANE": "1", "TERM_PROGRAM": "ghostty"},
			want: WezTerm,
		},
		{
			name: "ghostty on macOS",
			goos: "darwin",
			env:  map[string]string{"TERM_PROGRAM": "ghostty"},
			want: Ghostty,
		},
		// The Ghostty driver is AppleScript: elsewhere the console runs its
		// Mates headless, as in any terminal it has no driver for.
		{
			name: "ghostty on linux",
			goos: "linux",
			env:  map[string]string{"TERM_PROGRAM": "ghostty"},
			want: None,
		},
		{
			name: "ghostty on freebsd",
			goos: "freebsd",
			env:  map[string]string{"TERM_PROGRAM": "ghostty"},
			want: None,
		},
		{
			name: "iterm",
			goos: "darwin",
			env:  map[string]string{"TERM_PROGRAM": "iTerm.app"},
			want: ITerm,
		},
		{
			name: "herdr desktop app says ghostty",
			goos: "darwin",
			env:  map[string]string{"TERM_PROGRAM": "ghostty", "HC_TERMINAL_ID": "7"},
			want: None,
		},
		{
			name: "blank wezterm pane is ignored",
			goos: "darwin",
			env:  map[string]string{"WEZTERM_PANE": "  ", "TERM_PROGRAM": "ghostty"},
			want: Ghostty,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := DetectFor(tc.goos, func(k string) string { return tc.env[k] })
			if got != tc.want {
				t.Fatalf("DetectFor(%q) = %q, want %q", tc.goos, got, tc.want)
			}
		})
	}
}

// Detect is DetectFor on the system the console runs on.
func TestDetectIsDetectForThisSystem(t *testing.T) {
	t.Parallel()
	getenv := func(k string) string { return map[string]string{"TERM_PROGRAM": "ghostty"}[k] }
	if got, want := Detect(getenv), DetectFor(runtime.GOOS, getenv); got != want {
		t.Fatalf("Detect = %q, DetectFor(%q) = %q", got, runtime.GOOS, want)
	}
	if Detect(nil) != None || DetectFor("darwin", nil) != None {
		t.Fatal("no environment must detect no host")
	}
}

func TestOpenNilForNoneAndITerm(t *testing.T) {
	t.Parallel()
	if Open(None, Options{}) != nil {
		t.Fatal("Open(None) must be nil")
	}
	if Open(ITerm, Options{}) != nil {
		t.Fatal("Open(ITerm) is reserved and must be nil")
	}
}

func TestNoHostHint(t *testing.T) {
	t.Parallel()
	local := NoHostHint(func(string) string { return "" })
	if !strings.Contains(local, "inside WezTerm or Ghostty") {
		t.Fatalf("local hint = %q", local)
	}
	for _, key := range []string{"SSH_CONNECTION", "SSH_TTY"} {
		got := NoHostHint(func(k string) string {
			if k == key {
				return "x"
			}
			return ""
		})
		if !strings.Contains(got, "SSHMUX:") {
			t.Fatalf("%s set: hint = %q, want the wezterm ssh multiplexing advice", key, got)
		}
	}
	if NoHostHint(nil) != local {
		t.Fatal("nil getenv must give the local hint")
	}
}
