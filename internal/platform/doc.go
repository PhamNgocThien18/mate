// Package platform is where mate calls the operating system for what only
// some systems have: advisory file locks, opening a file without following a
// symlink, signals, process groups and sessions. A port of mate starts here
// rather than in each package that locks a log or stops a process, and
// keeps the errors' meaning: callers read syscall.ESRCH and syscall.EPERM.
//
// Each group of calls has two builds. One is for the systems whose Go
// syscall package has flock: darwin, dragonfly, freebsd, illumos, linux,
// netbsd and openbsd, cmd/go's own list (CON-177). Not the "unix" tag,
// which takes in aix and solaris, where Go has no syscall.Flock. The other
// is for the rest, windows among them: each call does nothing and returns
// an error that names the system and matches errors.ErrUnsupported. mate
// does not run without its locks, and it never pretends to have signalled
// a process. The package does not build on plan9, whose syscall package
// has no Signal type.
//
// The package also names the program that opens a URL on each system
// (URLOpenerFor). That choice is a table, not a call, so it is one build
// for every system, and a test reads it for any GOOS.
//
// The functions are small and keep no state. They return the system's own
// error and leave its meaning to the caller.
// A caller that needs a seam for its tests keeps one of its own, a function
// field or variable whose default is the function here.
//
// ratchet_test.go keeps these calls in this package: outside it, only the
// files it lists may use them.
package platform
