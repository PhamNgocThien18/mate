//go:build !darwin && !linux

package notifications

import "context"

func supported() bool { return false }

func sendNative(context.Context, Message) error { return ErrUnsupported }

// acceptedNote is never printed: Send never succeeds here.
const acceptedNote = ""
