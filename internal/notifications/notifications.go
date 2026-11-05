// Package notifications delivers captain news and periodic recorded-state reports.
// It does not call a model or infer that an agent is stuck from silence.
package notifications

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/platform"
)

const ReportInterval = 10 * time.Minute

type Message struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url,omitempty"`
}

type Sender interface {
	Send(context.Context, Message) error
}

type News struct{ Key, Revision, Title string }
type Project struct {
	Name    string
	News    []News // Only news waiting for the captain.
	Summary string
}
type Snapshot struct {
	Projects []Project
	// Complete is false if any news source could not be read. In that case
	// known news can still notify, but a "no news" report would be a lie.
	Complete bool
	URL      string
}

type state struct {
	Delivered map[string]string `json:"delivered"`
	ReportAt  time.Time         `json:"report_at"`
}

// Tick serializes consoles sharing a workspace, and persists receipts only
// after the native notification API accepts a message. The caller retries
// failures; neither an unavailable notifier nor a restart loses a news.
func Tick(ctx context.Context, path string, snap Snapshot, sender Sender, now time.Time) error {
	lock, err := platform.OpenNoFollow(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	locked, err := platform.TryLock(lock)
	if err != nil {
		return err
	}
	if !locked {
		return nil
	}
	defer platform.Unlock(lock)
	s := state{Delivered: map[string]string{}, ReportAt: now}
	b, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(b, &s); err != nil {
			return fmt.Errorf("notification receipts: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if s.Delivered == nil {
		s.Delivered = map[string]string{}
	}
	save := func() error {
		b, err := json.Marshal(s)
		if err != nil {
			return err
		}
		f, err := os.CreateTemp(filepath.Dir(path), ".notifications-*")
		if err != nil {
			return err
		}
		defer os.Remove(f.Name())
		if _, err := f.Write(b); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		return os.Rename(f.Name(), path)
	}
	owed := false
	for _, p := range snap.Projects {
		for _, n := range p.News {
			owed = true
			if s.Delivered[n.Key] == n.Revision {
				continue
			}
			digest := sha256.Sum256([]byte(path + "\x00" + n.Key))
			msg := Message{ID: fmt.Sprintf("news-%x", digest[:12]), Title: "Mate · " + p.Name + " · cần anh xem", Body: n.Title + "\nMở Mate, nhấn Ctrl+T để xem và trả lời news.", URL: snap.URL}
			if err := sender.Send(ctx, msg); err != nil {
				return err
			}
			s.Delivered[n.Key] = n.Revision
			s.ReportAt = now
			if err := save(); err != nil {
				return err
			}
		}
	}
	// Begin a fresh quiet interval after the last poll with outstanding news.
	// Unreadable news also suppress a report and reset that interval.
	if owed || !snap.Complete {
		s.ReportAt = now
		return save()
	}
	if now.Sub(s.ReportAt) >= ReportInterval && len(snap.Projects) > 0 {
		lines := make([]string, 0, len(snap.Projects))
		for _, p := range snap.Projects {
			lines = append(lines, p.Name+": "+p.Summary)
		}
		digest := sha256.Sum256([]byte(path))
		msg := Message{ID: fmt.Sprintf("report-%x", digest[:12]), Title: "Mate · báo cáo 10 phút", Body: "Không có news chờ anh.\n" + strings.Join(lines, "\n"), URL: snap.URL}
		if err := sender.Send(ctx, msg); err != nil {
			return err
		}
		s.ReportAt = now
	}
	return save()
}
