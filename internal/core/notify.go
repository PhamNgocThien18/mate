package core

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nguyenngocanh94/mate/internal/matehost"
	"github.com/nguyenngocanh94/mate/internal/notifications"
	"github.com/nguyenngocanh94/mate/internal/query"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// NativeNotifier is this machine's notifier, asked once: nil on a machine
// that cannot notify (Linux without notify-send, a system mate has no
// notifier for), which then runs no notification loop at all.
func NativeNotifier() notifications.Sender {
	if notifications.Supported() {
		return notifications.Native{}
	}
	return nil
}

// notifyLoop sends the captain a native notification for news waiting on
// them, and reports recorded state every 10 minutes without news
// (internal/notifications), for as long as the engine runs, including while
// the console's terminal is backgrounded.
//
// It is independent of the snapshot and of the Mate's turn: it opens its
// own handle on the workspace, reads files only (query.Load, never Herdr),
// and never waits on the Mate to deliver a notification.
type notifyLoop struct {
	root      string
	harnesses query.Harnesses
	sender    notifications.Sender
	// url is the dashboard's, read as the loop starts: a click opens the
	// captain's desk there.
	url func() string

	// cancel and done are set by Start and read by Stop: the engine calls
	// each once, Stop on a goroutine it starts after Start has returned.
	cancel context.CancelFunc
	done   chan struct{}

	mu     sync.Mutex
	err    error
	status string
}

// Start begins the loop. It never fails the engine: a workspace whose
// `.mate/.env` says MATE_NOTIFICATIONS=off runs no loop and says nothing,
// and one that cannot run it keeps the reason for Engine.Notifications.
func (n *notifyLoop) Start(ctx context.Context) error {
	err := n.start(ctx)
	n.mu.Lock()
	defer n.mu.Unlock()
	n.err = err
	return nil
}

// notificationsOn is `.mate/.env`'s MATE_NOTIFICATIONS: on unless it says
// off.
func notificationsOn(ws *store.Workspace) (bool, error) {
	env, err := ws.LoadEnv()
	if err != nil {
		return false, err
	}
	switch value := strings.ToLower(strings.TrimSpace(env["MATE_NOTIFICATIONS"])); value {
	case "off", "false", "0", "no":
		return false, nil
	case "", "on", "true", "1", "yes":
		return true, nil
	default:
		return false, fmt.Errorf("MATE_NOTIFICATIONS must be on or off")
	}
}

func (n *notifyLoop) start(ctx context.Context) error {
	ws, err := store.Open(n.root)
	if err != nil {
		return err
	}
	if on, err := notificationsOn(ws); err != nil || !on {
		return err
	}
	path, err := ws.Resolve(filepath.Join(ws.StateDir(), "notifications.json"))
	if err != nil {
		return err
	}
	if _, err := ws.Resolve(path + ".lock"); err != nil {
		return err
	}
	dashboardURL := n.url()
	ctx, cancel := context.WithCancel(ctx)
	// The goroutine closes the channel made here, never n.done (the
	// CON-207 race).
	done := make(chan struct{})
	n.cancel, n.done = cancel, done
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
			snap, err := notificationSnapshot(ctx, ws, n.harnesses, dashboardURL, time.Now())
			if err == nil {
				err = notifications.Tick(ctx, path, snap, n.sender, time.Now())
			}
			n.setStatus(err)
			delay := 5 * time.Second
			if err != nil {
				delay = time.Minute
			}
			timer.Reset(delay)
		}
	}()
	return nil
}

// Stop ends the loop and waits for a tick in flight.
func (n *notifyLoop) Stop() {
	if n.cancel == nil {
		return
	}
	n.cancel()
	<-n.done
	n.cancel, n.done = nil, nil
}

func (n *notifyLoop) setStatus(err error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.status = ""
	if err != nil {
		n.status = "notifications: " + err.Error()
	}
}

func (n *notifyLoop) read() (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.status, n.err
}

func notificationSnapshot(ctx context.Context, ws *store.Workspace, harnesses query.Harnesses, dashboardURL string, now time.Time) (notifications.Snapshot, error) {
	snap, err := query.Load(ctx, ws, harnesses, nil)
	if err != nil {
		return notifications.Snapshot{}, err
	}
	out := notifications.Snapshot{Complete: true}
	// The dashboard's URL can be the loopback one followed by the LAN one
	// (dashboardPart); the click opens on this machine, so loopback.
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
