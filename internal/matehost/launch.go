// Package matehost runs a headless Mate (CON-78). Instead of a harness's
// own TUI, the Mate is `mate mate host <project>`: a background process of
// its own, outside Herdr (CON-225), that runs each message as one `claude
// -p` resumed on the same session; the turn lock makes sure only one runs
// at a time, whoever started it. The REPL (repl*.go) is one of its
// clients: it shows the captain their own messages and the Mate's replies
// to them, and keeps the app's event lines out of sight.
//
// The rest of mate reaches the host over a unix socket in the Mate's
// directory: the outbox delivers event lines through it, the snapshot asks
// it whether the Mate is alive, and stow, context refresh and the console
// ask it whether the Mate is busy or the captain has a draft.
package matehost

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Files of a headless Mate, all in its directory.
const (
	// LaunchName is what StartMate prepared for the host to run.
	LaunchName = "host.launch.json"
	// SocketName is the host's control socket.
	SocketName = "host.sock"
	// HistoryName is what the host showed, one item per line, replayed when
	// the host starts again.
	HistoryName = "host.jsonl"
	// LogName is where a host started outside Herdr writes its stdout and
	// stderr (CON-225); LogName+".1" is the run before the last rotation.
	LogName = "host.log"
	// TurnLock and HostLock name the store.TryMateLock locks.
	TurnLock = "turn"
	HostLock = "host"
)

// SocketPath is the control socket of the Mate in mateDir.
func SocketPath(mateDir string) string { return filepath.Join(mateDir, SocketName) }

// LogPath is the log of the host of the Mate in mateDir.
func LogPath(mateDir string) string { return filepath.Join(mateDir, LogName) }

// EnvHostTurn is set on every turn a host runs, to the turn's number in
// that host (1 for its first). Claude runs SessionStart on every resumed
// `-p` turn (docs/evidence/headless-mate-probe-2026-10-15.md); the hook
// prints the live-state digest only on a host's first turn, because later
// ones already have it in the conversation (QuietSessionStart).
const EnvHostTurn = "MATE_HOST_TURN"

// QuietSessionStart reports whether a SessionStart hook with this source,
// running under this EnvHostTurn value, should print nothing: a resumed
// turn after a host's first.
func QuietSessionStart(source, hostTurn string) bool {
	if source != "resume" {
		return false
	}
	n, err := strconv.Atoi(strings.TrimSpace(hostTurn))
	return err == nil && n > 1
}

// Launch is one headless Mate's harness launch, as StartMate prepared it:
// the same program, args and environment a pane launch would use. The host
// turns it into one run per message (claude.TurnArgs).
type Launch struct {
	Project string `json:"project"`
	// Agent is the Mate's agent name, `mate-<project>` (spawn's
	// AgentNamePrefix). A host outside Herdr (CON-225) is no Herdr agent;
	// the name stays the Mate's identity in mate.meta.
	Agent string `json:"agent"`
	// Harness is the harness kind whose HeadlessTurns runs each message.
	Harness   string   `json:"harness"`
	Program   string   `json:"program"`
	Args      []string `json:"args"`
	Dir       string   `json:"dir"`
	Env       []string `json:"env,omitempty"`
	UnsetEnv  []string `json:"unset_env,omitempty"`
	SessionID string   `json:"session_id"`
}

func (l Launch) valid() error {
	switch {
	case l.Project == "":
		return errors.New("matehost: the launch names no project")
	case l.Harness == "":
		return errors.New("matehost: the launch names no harness")
	case l.Program == "":
		return errors.New("matehost: the launch names no program")
	case l.Dir == "":
		return errors.New("matehost: the launch names no directory")
	case l.SessionID == "":
		return errors.New("matehost: the launch names no session id")
	}
	return nil
}

// WriteLaunch writes the launch into mateDir, replacing the last one.
func WriteLaunch(mateDir string, l Launch) error {
	if err := l.valid(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(mateDir, LaunchName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ReadLaunch reads the launch StartMate wrote into mateDir.
func ReadLaunch(mateDir string) (Launch, error) {
	data, err := os.ReadFile(filepath.Join(mateDir, LaunchName))
	if err != nil {
		return Launch{}, err
	}
	var l Launch
	if err := json.Unmarshal(data, &l); err != nil {
		return Launch{}, fmt.Errorf("matehost: %s: %w", LaunchName, err)
	}
	return l, l.valid()
}

// Marker opens every line the app sends the Mate (send.Marker).
const Marker = "⟦mate⟧ "

// Label is what an app line is about, for the small tag the REPL shows in
// its place: the word before its first colon (`digest`, `resolve`,
// `pr-merged`), or `mate` when there is none.
func Label(text string) string {
	text = strings.TrimSpace(strings.TrimPrefix(text, Marker))
	head, _, ok := strings.Cut(text, ":")
	if !ok || head == "" || strings.ContainsAny(head, " \t") {
		return "mate"
	}
	return head
}

// Silent reports a reply that says the Mate had nothing for the captain:
// exactly an em dash (the manual's headless rule) or a plain dash.
func Silent(reply string) bool {
	r := strings.TrimSpace(reply)
	return r == "—" || r == "-"
}

// Summary is an app line in one line for the captain: a digest's items
// without its count and its instructions (autopilot.Line), anything else
// without its marker and label.
func Summary(text string) string {
	text = strings.TrimSpace(strings.TrimPrefix(text, Marker))
	label := Label(text)
	if label != "mate" {
		text = strings.TrimSpace(strings.TrimPrefix(text, label+":"))
	}
	if label == "digest" {
		parts := strings.Split(text, " — ")
		if len(parts) >= 3 {
			text = strings.Join(parts[1:len(parts)-1], " — ")
		}
	}
	return strings.Join(strings.Fields(text), " ")
}
