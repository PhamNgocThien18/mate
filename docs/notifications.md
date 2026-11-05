# macOS notifications

Mate sends native Notification Center alerts under the name **Mate** while the
workspace console is running. The terminal can be in the background or showing
another pane. Quitting the console stops monitoring, like the existing observer
and auto daemon; this is not a launch-at-login service.

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
- Notification clicks open `/office/#captain` on this console's dashboard when it
  started successfully. The dashboard must still be running. Answer the news
  there, or in a headless Mate's REPL, where **Ctrl+T** opens the Topic page.
  A read-only dashboard only shows news.
- Receipts and the report clock are in `.mate/notifications.json`, serialized by
  `.mate/notifications.json.lock`. Restarting the console does not resend
  accepted news. A new revision replaces the same news notification;
  periodic reports share one notification ID. A failed submission is retried
  after a minute and shown on the console status line. Failed news reads suppress
  the "no news" report rather than treating unknown as empty.

Run `mate notifications test` to send a test and request macOS notification
permission. On first use, Mate builds and ad-hoc signs a small Swift companion
at `~/Library/Application Support/mate/notifications/Mate.app`; this needs Xcode
Command Line Tools (`xcrun swiftc`). Later sends reuse the app. Source and build
logic are bundled in the Go binary; there is no third-party notifier dependency.

The companion uses Apple's
[UserNotifications authorization API](https://developer.apple.com/documentation/usernotifications/unusernotificationcenter/requestauthorization(options:completionhandler:)).
A successful test means Notification Center accepted the request. Banner display
and sound still depend on **System Settings → Notifications → Mate** and Focus.
If permission is denied, enable it there and try again. No shell interpolation is
used for news text. Notification clicks only open local HTTP dashboard URLs.

Notifications are on by default on macOS. To disable for a workspace, add
`MATE_NOTIFICATIONS=off` to `.mate/.env` and reopen its console. Other platforms
keep running without a native sender; the explicit test command reports that
macOS is required.

`mate notifications test` has compiled and signed the companion and received
an acceptance receipt from the real Notification Center. That proves
submission, not that a banner was shown or a click was delivered. Automated
tests cover the ten-minute clock, news updates and clearing, retries,
unreadable sources, receipt persistence and concurrent consoles, with a
recording sender.
