package notifications

import (
	"context"
	"errors"
)

// Native delivers through the system's own notifications: Notification
// Center under Mate's own identity on macOS (native_darwin.go), notify-send
// on Linux (native_linux.go). Elsewhere Send returns ErrUnsupported.
type Native struct{}

func (Native) Send(ctx context.Context, m Message) error { return sendNative(ctx, m) }

// Supported reports whether Native can deliver on this machine: always on
// macOS, on Linux when notify-send is on PATH, never elsewhere. The console
// asks once, when it starts, and runs no notification loop without it.
func Supported() bool { return supported() }

// AcceptedNote is the line `mate notifications test` prints once Native's
// Send returned nil: what took the notification on this system, and where
// to look when no banner appeared.
func AcceptedNote() string { return acceptedNote }

var ErrUnsupported = errors.New("native Mate notifications need macOS, or notify-send on Linux")
