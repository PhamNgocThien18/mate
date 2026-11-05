// Package config holds build-time metadata and process-wide defaults.
package config

import "runtime/debug"

// Set by the Makefile through -ldflags. A binary built without them
// (`go install`, plain `go build`) takes what the Go toolchain recorded in
// its build info instead; "dev"/"unknown" remain only when that is missing.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

func init() {
	info, ok := debug.ReadBuildInfo()
	Version, Commit, BuildDate = fromBuildInfo(Version, Commit, BuildDate, info, ok)
}

// fromBuildInfo fills a version still at "dev" from the build info: the main
// module's version (a pseudo-version for `go install ...@<commit>`, or one
// the toolchain stamps from a git checkout), and the commit and its time
// from the vcs.* settings. Those settings exist only for a build inside a
// checkout, so a `go install` binary keeps "unknown" for both. A version set
// through -ldflags is left alone, together with its commit and date.
func fromBuildInfo(version, commit, date string, info *debug.BuildInfo, ok bool) (string, string, string) {
	if version != "dev" || !ok || info == nil {
		return version, commit, date
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		version = v
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if commit == "unknown" && s.Value != "" {
				commit = s.Value
			}
		case "vcs.time":
			if date == "unknown" && s.Value != "" {
				date = s.Value
			}
		}
	}
	return version, commit, date
}
