//go:build !darwin && !dragonfly && !freebsd && !illumos && !linux && !netbsd && !openbsd

package platform

import (
	"errors"
	"runtime"
)

// unsupported is the error of every call on a system outside the list in
// doc.go (the constraint above is that list, negated). Its text says what
// the system lacks and names the system; errors.Is matches it to
// errors.ErrUnsupported.
type unsupported string

func (u unsupported) Error() string { return "platform: " + string(u) + " on " + runtime.GOOS }

func (unsupported) Unwrap() error { return errors.ErrUnsupported }
