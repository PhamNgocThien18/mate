// Package platform is where mate calls the operating system for what only
// some systems have: advisory file locks, opening a file without following a
// symlink, signals, process groups and sessions. Porting mate to another
// system means changing this package, not the packages that lock a log or
// stop a process.
//
// Each group of calls has two builds. One is for the systems whose Go
// syscall package has flock: darwin, dragonfly, freebsd, illumos, linux,
// netbsd and openbsd, cmd/go's own list (CON-177). Not the "unix" tag,
// which takes in aix and solaris, where Go has no syscall.Flock. On every
// other system each call does nothing and returns an error that names the
// system and matches errors.ErrUnsupported: mate does not run without its
// locks, and it never pretends to have signalled a process.
//
// The functions are small and keep no state. They return the system's own
// error (syscall.ESRCH, syscall.EPERM) and leave its meaning to the caller.
// A caller that needs a seam for its tests keeps one of its own, a function
// field or variable whose default is the function here.
//
// ratchet_test.go keeps these calls in this package: outside it, only the
// files it lists may use them.
package platform
