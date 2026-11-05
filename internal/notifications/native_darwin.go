package notifications

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/nguyenngocanh94/mate/internal/platform"
)

//go:embed macos/MateNotifications.swift
var nativeSource []byte

// supported is always true: Notification Center is part of macOS. The
// companion app is built on the first send, and a failed build is a failed
// send.
func supported() bool { return true }

const acceptedNote = "Notification Center accepted Mate's test notification. If no banner appeared, check System Settings > Notifications > Mate and Focus."

const nativePlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleIdentifier</key><string>com.mate.notifications</string>
<key>CFBundleName</key><string>Mate</string>
<key>CFBundleDisplayName</key><string>Mate</string>
<key>CFBundleExecutable</key><string>MateNotifications</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleVersion</key><string>1</string>
<key>LSUIElement</key><true/>
<key>NSPrincipalClass</key><string>NSApplication</string>
</dict></plist>`

// InstallNative builds a stable app bundle so macOS can remember permission
// for Mate, rather than attributing its alerts to Script Editor or Terminal.
func InstallNative(ctx context.Context) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, "Library", "Application Support", "mate", "notifications")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	lock, err := platform.OpenNoFollow(filepath.Join(dir, "install.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return "", err
	}
	defer lock.Close()
	// Installation normally takes seconds; bound lock acquisition by context.
	for {
		locked, err := platform.TryLock(lock)
		if err != nil {
			return "", err
		}
		if locked {
			break
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	defer platform.Unlock(lock)
	app := filepath.Join(dir, "Mate.app")
	version := fmt.Sprintf("%x", sha256.Sum256(append(append([]byte{}, nativeSource...), []byte(nativePlist)...)))
	if b, err := os.ReadFile(filepath.Join(app, "Contents", "Resources", "source.sha256")); err == nil && string(b) == version {
		if _, err := os.Stat(filepath.Join(app, "Contents", "MacOS", "MateNotifications")); err == nil {
			return app, nil
		}
	}
	stage, err := os.MkdirTemp(dir, "build-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	bundle := filepath.Join(stage, "Mate.app")
	contents := filepath.Join(bundle, "Contents")
	if err := os.MkdirAll(filepath.Join(contents, "MacOS"), 0o700); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(contents, "Resources"), 0o700); err != nil {
		return "", err
	}
	source := filepath.Join(stage, "MateNotifications.swift")
	for path, data := range map[string][]byte{source: nativeSource, filepath.Join(contents, "Info.plist"): []byte(nativePlist), filepath.Join(contents, "Resources", "source.sha256"): []byte(version)} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return "", err
		}
	}
	cmd := exec.CommandContext(ctx, "/usr/bin/xcrun", "swiftc", "-O", "-o", filepath.Join(contents, "MacOS", "MateNotifications"), source)
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build Mate notification app (requires Xcode Command Line Tools): %w: %s", err, strings.TrimSpace(string(b)))
	}
	cmd = exec.CommandContext(ctx, "/usr/bin/codesign", "--force", "--sign", "-", bundle)
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("sign Mate notification app: %w: %s", err, strings.TrimSpace(string(b)))
	}
	// Only replace our generated bundle. Restore it if the final rename fails.
	old := app + ".previous"
	if err := os.RemoveAll(old); err != nil {
		return "", err
	}
	if _, err := os.Lstat(app); err == nil {
		if err := os.Rename(app, old); err != nil {
			return "", err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.Rename(bundle, app); err != nil {
		_ = os.Rename(old, app)
		return "", err
	}
	_ = os.RemoveAll(old)
	return app, nil
}

func sendNative(ctx context.Context, m Message) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	app, err := InstallNative(ctx)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "mate-notification-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	payload, result := filepath.Join(dir, "message.json"), filepath.Join(dir, "result.json")
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if err := os.WriteFile(payload, b, 0o600); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "/usr/bin/open", "-n", "-W", "-g", app, "--args", "--send", payload, result)
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("open Mate notification app: %w: %s", err, strings.TrimSpace(string(b)))
	}
	b, err = os.ReadFile(result)
	if err != nil {
		return fmt.Errorf("Mate notification app returned no receipt: %w", err)
	}
	var receipt struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(b, &receipt); err != nil {
		return err
	}
	if receipt.Error != "" {
		return errors.New(receipt.Error)
	}
	return nil
}
