package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/notice"
	"github.com/nguyenngocanh94/mate/internal/store"
)

// Jev's configuration: what `.mate/.env` turns on, read by `mate core`
// (explain_notice, through core.Options.Notice) and by every command that
// reads a pane (configuredObserver). The action itself is the core's
// (internal/core's explainNotice, CON-232).

// The workspace's `.mate/.env` opts this core into remote classification:
// `MATE_JEV_API_KEY_FILE` names a key file, `~/` or relative to the
// workspace root, and with it the action is on unless `MATE_JEV=off` (the
// values are jevSettings'). The key is read here and stays in this client;
// no key is added to spawned agents' environments. The process environment
// is not consulted, so one workspace's switch never reaches another's
// core.
func consoleNoticeClient(ws *store.Workspace) (*notice.Client, error) {
	env, mode, err := jevSettings(ws)
	if err != nil || mode == jevOff {
		return nil, err
	}
	return jevClient(ws, env)
}

// jevMode is what `MATE_JEV` turns on.
type jevMode int

const (
	jevOff jevMode = iota
	// jevNotice is the console's Explain notice action alone, the panes
	// read by the fixture observer.
	jevNotice
	// jevObserver is that action and the observer chain.
	jevObserver
)

// jevSettings reads `.mate/.env` and its MATE_JEV switch:
//
//	off (false, 0, no)               nothing goes to Jev
//	fixture                          the notice action; panes read by the fixture observer
//	on, observer (true, 1, yes)      the notice action and the observer chain
//	unset, with MATE_JEV_API_KEY_FILE  the same as fixture
//	unset, no key file named         off, silently: a workspace that never set Jev up
//
// A key alone keeps the panes on the fixture observer until the day-long
// live run's evidence is committed (docs/plans/jev-observer-2026-10-08.md
// section 6, PR 3); the chain is asked for by name.
//
// Any other value is a mistake, said in the error, and Jev stays off.
func jevSettings(ws *store.Workspace) (map[string]string, jevMode, error) {
	env, err := ws.LoadEnv()
	if err != nil {
		return nil, jevOff, fmt.Errorf("Jev disabled: %w", err)
	}
	switch value := strings.ToLower(strings.TrimSpace(env["MATE_JEV"])); value {
	case "":
		if env["MATE_JEV_API_KEY_FILE"] == "" {
			return env, jevOff, nil
		}
		return env, jevNotice, nil
	case "off", "false", "0", "no":
		return env, jevOff, nil
	case "fixture":
		return env, jevNotice, nil
	case "on", "observer", "true", "1", "yes":
		return env, jevObserver, nil
	}
	return nil, jevOff, errors.New("Jev disabled: MATE_JEV must be on, observer, fixture or off")
}

// jevClient is the Jev client over the key file `.mate/.env` names.
func jevClient(ws *store.Workspace, env map[string]string) (*notice.Client, error) {
	path := env["MATE_JEV_API_KEY_FILE"]
	if path == "" {
		return nil, errors.New("Jev disabled: MATE_JEV_API_KEY_FILE is not set in .mate/.env")
	}
	f, err := os.Open(keyFilePath(ws.Root(), path))
	if err != nil {
		return nil, errors.New("Jev disabled: cannot read MATE_JEV_API_KEY_FILE")
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	key := strings.TrimSpace(string(b))
	if err != nil || len(b) > 4096 || key == "" || strings.ContainsAny(key, "\r\n\t ") {
		return nil, errors.New("Jev disabled: invalid API key file")
	}
	return notice.New(key), nil
}

// keyFilePath expands a leading `~/` and anchors a relative path at the
// workspace root, the directory `.mate/.env` describes.
func keyFilePath(root, path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, path)
}
