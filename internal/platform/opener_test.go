package platform_test

import (
	"runtime"
	"slices"
	"testing"

	"github.com/nguyenngocanh94/mate/internal/platform"
)

func TestURLOpenerFor(t *testing.T) {
	for _, tc := range []struct {
		goos, name string
		args       []string
	}{
		{goos: "darwin", name: "open"},
		{goos: "linux", name: "xdg-open"},
		{goos: "freebsd", name: "xdg-open"},
		{goos: "windows", name: "rundll32", args: []string{"url.dll,FileProtocolHandler"}},
	} {
		name, args := platform.URLOpenerFor(tc.goos)
		if name != tc.name || !slices.Equal(args, tc.args) {
			t.Errorf("URLOpenerFor(%q) = %q %q, want %q %q", tc.goos, name, args, tc.name, tc.args)
		}
	}
}

// URLOpener is URLOpenerFor the system mate runs on.
func TestURLOpenerIsThisSystems(t *testing.T) {
	name, args := platform.URLOpener()
	wantName, wantArgs := platform.URLOpenerFor(runtime.GOOS)
	if name != wantName || !slices.Equal(args, wantArgs) {
		t.Fatalf("URLOpener() = %q %q, want %q %q", name, args, wantName, wantArgs)
	}
}
