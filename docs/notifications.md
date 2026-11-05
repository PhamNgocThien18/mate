# Notifications

While the workspace console is running, mate sends native notifications:
Notification Center alerts under the name **Mate** on macOS, `notify-send` on
Linux. The terminal can be in the background or showing another pane. Quitting
the console stops monitoring, like the existing observer and auto daemon; this
is not a launch-at-login service.

- News waiting for the captain produces an alert within the next poll (normally
  five seconds). Existing outstanding news alerts once when first monitored.
  An update or another question on that news alerts again. Reading it does not
  count as answering it. Answered, closed and dropped news do not alert.
- While any news waits for the captain, there is no periodic report. With no
  outstanding news, Mate sends one workspace report every ten minutes, starting
  ten minutes after monitoring starts or the last poll with outstanding news.
  Reports summarize recorded Mate/Crew states and the latest recorded activity;
  headless Mate state is queried with a one-second bound. Silence alone never
  means an agent is stuck. No model is called to write a report.
- Open the Mate and press **Ctrl+T** to check and answer news. Backlog questions
  from pane Mates also alert, but are answered in their conversation.
- Receipts and the report clock are in `.mate/notifications.json`, serialized by
  `.mate/notifications.json.lock`. Restarting or opening another console does
  not resend accepted news. A failed submission is retried after a minute and
  shown on the console status line. Failed news reads suppress the "no news"
  report rather than treating unknown as empty.

The console checks once, when it starts, whether this machine can send
notifications. If it cannot, the console runs without them. Run
`mate notifications test` to send a test notification; it reports what is
missing when nothing can be sent. No shell interpolation is used for news text.

## macOS

macOS needs Xcode Command Line Tools (`xcrun swiftc`): on first use, mate builds
and ad-hoc signs a small Swift companion at
`~/Library/Application Support/mate/notifications/Mate.app`. Later sends reuse
the app. Source and build logic are bundled in the Go binary; there is no
third-party notifier dependency. `mate notifications test` also requests
notification permission for Mate.

The companion uses Apple's
[UserNotifications authorization API](https://developer.apple.com/documentation/usernotifications/unusernotificationcenter/requestauthorization(options:completionhandler:)).
A successful test means Notification Center accepted the request. Banner display
and sound still depend on **System Settings → Notifications → Mate** and Focus.
If permission is denied, enable it there and try again.

Notification clicks open `/office/#captain` on this console's dashboard when it
started successfully. The dashboard must still be running. Clicks only open
local HTTP dashboard URLs. A new revision replaces the same news notification;
periodic reports share one notification ID.

Validation on 2026-10-20: `mate notifications test` compiled and signed the
companion and received an acceptance receipt from the real macOS Notification
Center. This verifies submission, not that a banner was visually shown or a
notification click was delivered. Automated tests cover the ten-minute clock,
news updates and clearing, retries, unreadable sources, receipt persistence and
concurrent consoles; those tests use a recording sender.

## Linux

Linux needs `notify-send` on `PATH` (package `libnotify-bin` on Debian and
Ubuntu, `libnotify` on Fedora and Arch) and a running notification daemon to
show the alert; desktop environments ship one. The notification's title and
body are the two arguments mate gives `notify-send`.

Clicking a notification on Linux opens no link: open the dashboard from the
address on the console's status line. Each alert is a new notification; a new
revision of a news does not replace the earlier one.

Without a desktop session, over ssh for example, `notify-send` can be installed
yet have no D-Bus session to reach. The console still runs: its status line
shows the error, and the console tries again every minute. A successful
`mate notifications test` means `notify-send` accepted the notification; if no
banner appeared, check that a notification daemon is running.

On Linux, an automated test runs a stand-in `notify-send` that records its
arguments; `make check` on macOS does not run it. A banner on a real Linux
desktop has not been checked yet.

## Turning notifications off

Notifications are on by default. To disable them for a workspace, add
`MATE_NOTIFICATIONS=off` to `.mate/.env` and reopen its console. Other systems
have no native sender: the console runs without notifications, and
`mate notifications test` reports that notifications need macOS, or
`notify-send` on Linux.
