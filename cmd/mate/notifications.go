package main

import (
	"context"
	"fmt"
	"io"

	"github.com/nguyenngocanh94/mate/internal/notifications"
)

// cmdNotifications is `mate notifications test`. The loop that notifies
// while the console runs is internal/core's (CON-208).
func cmdNotifications(args []string, stdout, stderr io.Writer) error {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		_, err := io.WriteString(stdout, `Usage: mate notifications test

Send a native notification. On macOS it requests notification permission for
Mate: the companion app is built once with Xcode Command Line Tools; allow Mate
in System Settings > Notifications. On Linux it runs notify-send, which needs a
notification daemon. Acceptance does not prove a banner was shown; Focus and
notification settings control presentation.

While the console is running (including in the background), Mate notifies for
news waiting on you and reports recorded state every 10 minutes without news.
On macOS a click opens the workspace dashboard's captain desk when it is
available; check and answer news in Mate with Ctrl+T. Quit the console to stop
monitoring. Set MATE_NOTIFICATIONS=off in the workspace's .mate/.env to disable.
`)
		return err
	}
	if len(args) != 1 || args[0] != "test" {
		return newUsageError("usage: mate notifications test")
	}
	if err := (notifications.Native{}).Send(context.Background(), notifications.Message{
		ID: "mate-notification-test", Title: "Mate · thử notification", Body: "Mate sẽ báo khi có news cần anh xem; mỗi 10 phút sẽ báo cáo nếu không có news chờ.",
	}); err != nil {
		return err
	}
	_, err := fmt.Fprintln(stdout, notifications.AcceptedNote())
	return err
}
