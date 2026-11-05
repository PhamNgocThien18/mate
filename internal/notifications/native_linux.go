package notifications

import (
	"context"
	"os/exec"
)

// linuxNotifier is the notify-send on this machine's PATH.
var linuxNotifier = notifySend{
	lookPath: exec.LookPath,
	run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, name, args...).CombinedOutput()
	},
}

func supported() bool { return linuxNotifier.available() }

func sendNative(ctx context.Context, m Message) error { return linuxNotifier.send(ctx, m) }

const acceptedNote = "notify-send accepted Mate's test notification. If no banner appeared, check that a notification daemon is running."
