package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/notifications"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/store"
)

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

// startConsoleNotifications asks notifications.Supported once, as the
// console starts: a machine that cannot notify (Linux without notify-send,
// a system mate has no notifier for) runs no notification loop at all.
func startConsoleNotifications(ctx context.Context, root, dashboardURL string) (func(), func() string, error) {
	var sender notifications.Sender
	if notifications.Supported() {
		sender = notifications.Native{}
	}
	return startNotificationLoop(ctx, root, dashboardURL, sender)
}

// startNotificationLoop sends through sender, and starts nothing when it is
// nil. Independent from the UI refresh and Mate's turn. It owns a store
// handle, and never waits on the Mate to deliver a notification. Like the
// observer, its lifetime is the console's, including while the terminal is
// backgrounded.
func startNotificationLoop(ctx context.Context, root, dashboardURL string, sender notifications.Sender) (func(), func() string, error) {
	if sender == nil {
		return func() {}, func() string { return "" }, nil
	}
	ws, err := store.Open(root)
	if err != nil {
		return nil, nil, err
	}
	env, err := ws.LoadEnv()
	if err != nil {
		return nil, nil, err
	}
	switch value := strings.ToLower(strings.TrimSpace(env["MATE_NOTIFICATIONS"])); value {
	case "off", "false", "0", "no":
		return func() {}, func() string { return "" }, nil
	case "", "on", "true", "1", "yes":
	default:
		return nil, nil, fmt.Errorf("MATE_NOTIFICATIONS must be on or off")
	}
	path, err := ws.Resolve(filepath.Join(ws.StateDir(), "notifications.json"))
	if err != nil {
		return nil, nil, err
	}
	if _, err := ws.Resolve(path + ".lock"); err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	var mu sync.Mutex
	status := ""
	setStatus := func(err error) {
		mu.Lock()
		defer mu.Unlock()
		status = ""
		if err != nil {
			status = "notifications: " + err.Error()
		}
	}
	go func() {
		defer close(done)
		timer := time.NewTimer(0)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			snap, err := notificationSnapshot(ctx, ws, dashboardURL, time.Now())
			if err == nil {
				err = notifications.Tick(ctx, path, snap, sender, time.Now())
			}
			setStatus(err)
			delay := 5 * time.Second
			if err != nil {
				delay = time.Minute
			}
			timer.Reset(delay)
		}
	}()
	return func() { cancel(); <-done }, func() string { mu.Lock(); defer mu.Unlock(); return status }, nil
}

func notificationSnapshot(ctx context.Context, ws *store.Workspace, dashboardURL string, now time.Time) (notifications.Snapshot, error) {
	snap, err := query.Load(ctx, ws, consoleHarnesses(), nil)
	if err != nil {
		return notifications.Snapshot{}, err
	}
	out := notifications.Snapshot{Complete: true}
	// The console's URL can be the loopback one followed by the LAN one
	// (startConsoleDashboard); the click opens on this Mac, so loopback.
	if fields := strings.Fields(dashboardURL); len(fields) > 0 {
		out.URL = strings.TrimRight(fields[0], "/") + "/office/#captain"
	}
	for _, p := range snap.Projects {
		np := notifications.Project{Name: p.Name}
		if p.Mate.News.State == query.Unknown {
			out.Complete = false
		}
		for _, n := range p.Mate.OwedNews() {
			np.News = append(np.News, notifications.News{Key: fmt.Sprintf("%s/news/%d", p.ProjectID, n.ID), Revision: n.At.UTC().Format(time.RFC3339Nano), Title: fmt.Sprintf("#%d · %s", n.ID, n.Title)})
		}
		np.Summary = notificationSummary(ctx, ws, p, now)
		out.Projects = append(out.Projects, np)
	}
	return out, nil
}

func notificationSummary(ctx context.Context, ws *store.Workspace, p query.ProjectNode, now time.Time) string {
	counts := map[query.CrewStatus]int{}
	for _, c := range p.Crews {
		counts[c.Status]++
	}
	state := "chưa có Mate"
	if p.Mate.Designated.IsKnown() {
		state = "Mate ghi nhận " + p.Mate.Designated.Value.Status.String()
	}
	if p.Mate.Designated.State == query.Unknown {
		state = "không đọc được trạng thái Mate"
	}
	activity := time.Time{}
	if p.Box.IsKnown() {
		activity = p.Box.Value.LastAt
	}
	if ws.MateHeadless(p.ProjectID) {
		client := matehost.Dial(ws.MateDir(p.ProjectID))
		client.Timeout = time.Second
		if s, err := client.State(ctx); err == nil {
			state = "Mate đang chờ lượt mới"
			if s.Busy {
				state = "Mate đang xử lý từ " + s.TurnStartedAt.Local().Format("15:04")
			}
			if s.Queued > 0 {
				state += fmt.Sprintf(", %d tin đang đợi", s.Queued)
			}
		} else {
			state = "không liên lạc được Mate; cần kiểm tra"
		}
		items := matehost.History(matehost.HistoryPath(ws.MateDir(p.ProjectID)), 1)
		if len(items) > 0 {
			it := items[0]
			at := it.At.Add(time.Duration(it.MS) * time.Millisecond)
			if at.After(activity) {
				activity = at
			}
			if it.Done && it.Error != "" {
				state += "; lượt gần nhất lỗi: " + it.Error
			}
		}
	}
	line := fmt.Sprintf("%s; %d crew làm, %d chờ quyết định, %d bàn giao, %d bị chặn", state,
		counts[query.CrewWorking]+counts[query.CrewSpawned], counts[query.CrewNeedsDecision], counts[query.CrewWaitMate], counts[query.CrewBlocked])
	if !activity.IsZero() {
		age := now.Sub(activity).Truncate(time.Minute)
		if age < 0 {
			age = 0
		}
		line += "; cập nhật ghi nhận " + age.String() + " trước"
	} else {
		line += "; chưa ghi nhận hoạt động"
	}
	return line
}
