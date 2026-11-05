package config

import (
	"runtime/debug"
	"testing"
)

func TestFromBuildInfo(t *testing.T) {
	t.Parallel()
	const pseudo = "v0.0.0-20261105080000-0123456789ab"
	vcs := []debug.BuildSetting{
		{Key: "vcs", Value: "git"},
		{Key: "vcs.revision", Value: "0123456789abcdef0123456789abcdef01234567"},
		{Key: "vcs.time", Value: "2026-11-05T08:00:00Z"},
	}
	tests := []struct {
		name                string
		version, commit, at string
		info                *debug.BuildInfo
		ok                  bool
		want                [3]string
	}{
		{
			name:    "go install: module version, no vcs settings",
			version: "dev", commit: "unknown", at: "unknown",
			info: &debug.BuildInfo{Main: debug.Module{Version: pseudo}},
			ok:   true,
			want: [3]string{pseudo, "unknown", "unknown"},
		},
		{
			name:    "go build in a checkout: version, commit and time",
			version: "dev", commit: "unknown", at: "unknown",
			info: &debug.BuildInfo{Main: debug.Module{Version: pseudo + "+dirty"}, Settings: vcs},
			ok:   true,
			want: [3]string{pseudo + "+dirty", "0123456789abcdef0123456789abcdef01234567", "2026-11-05T08:00:00Z"},
		},
		{
			name:    "(devel) keeps dev but still takes the commit",
			version: "dev", commit: "unknown", at: "unknown",
			info: &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: vcs},
			ok:   true,
			want: [3]string{"dev", "0123456789abcdef0123456789abcdef01234567", "2026-11-05T08:00:00Z"},
		},
		{
			name:    "make build: ldflags win",
			version: "v1.2.3", commit: "abc1234", at: "2026-11-05T09:00:00Z",
			info: &debug.BuildInfo{Main: debug.Module{Version: pseudo}, Settings: vcs},
			ok:   true,
			want: [3]string{"v1.2.3", "abc1234", "2026-11-05T09:00:00Z"},
		},
		{
			name:    "no build info",
			version: "dev", commit: "unknown", at: "unknown",
			want: [3]string{"dev", "unknown", "unknown"},
		},
		{
			name:    "ok but nil build info",
			version: "dev", commit: "unknown", at: "unknown",
			ok:   true,
			want: [3]string{"dev", "unknown", "unknown"},
		},
		{
			name:    "empty module version",
			version: "dev", commit: "unknown", at: "unknown",
			info: &debug.BuildInfo{},
			ok:   true,
			want: [3]string{"dev", "unknown", "unknown"},
		},
	}
	for _, tc := range tests {
		v, c, d := fromBuildInfo(tc.version, tc.commit, tc.at, tc.info, tc.ok)
		if got := [3]string{v, c, d}; got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
