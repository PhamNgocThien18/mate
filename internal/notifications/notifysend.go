package notifications

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// notifySendWait bounds one notify-send, which returns once the
// notification server took the notification or refused it: a server that
// never answers does not hold the notification loop.
const notifySendWait = 30 * time.Second

// notifySend is the Linux sender: notify-send, the freedesktop.org
// notification client libnotify installs, run with no shell, the title and
// the body as its two arguments after "--" so neither is read as an option.
// It has no click action and no notification ID: a Linux notification
// opens no link, and a new revision of a news is a new notification
// (docs/notifications.md).
//
// It is plain Go, built on every system so its tests run wherever make
// check does; only the linux build sends with it (native_linux.go).
type notifySend struct {
	lookPath func(file string) (string, error)
	// run runs the program and returns what it wrote to stdout and stderr.
	run func(ctx context.Context, name string, args ...string) ([]byte, error)
}

// available reports whether notify-send is on PATH.
func (n notifySend) available() bool {
	_, err := n.lookPath("notify-send")
	return err == nil
}

func (n notifySend) send(ctx context.Context, m Message) error {
	path, err := n.lookPath("notify-send")
	if err != nil {
		return ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(ctx, notifySendWait)
	defer cancel()
	if out, err := n.run(ctx, path, "--", m.Title, m.Body); err != nil {
		return fmt.Errorf("notify-send: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
