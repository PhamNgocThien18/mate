package host

import (
	"os/exec"
	"path/filepath"

	"github.com/nguyenngocanh94/mate/internal/process"
)

// Options configure a host driver. Zero values pick production defaults.
type Options struct {
	Runner process.Runner
	Env    func(string) string
	// Pane is the host pane the console occupies. WezTerm reads WEZTERM_PANE
	// when this is empty.
	Pane      string
	WezTerm   string
	Osascript string
	// SelfCols reads the Console's own width now. Ghostty cannot split at a
	// width, so it evens the columns and then narrows the Console by
	// measuring; nil leaves the columns even.
	SelfCols func() int
}

func (o Options) runner() process.Runner {
	if o.Runner != nil {
		return o.Runner
	}
	return process.ExecRunner{}
}

func (o Options) getenv(key string) string {
	if o.Env == nil {
		return ""
	}
	return o.Env(key)
}

// Open returns a Host for kind, or nil when there is no driver (None, ITerm).
func Open(kind Kind, opt Options) Host {
	switch kind {
	case WezTerm:
		return newWezTerm(opt)
	case Ghostty:
		return newGhostty(opt)
	default:
		return nil
	}
}

// AttachArgv is the program a stage column runs to show an agent.
// --takeover: the stage is where the captain asked to see this agent, so it
// takes the agent's terminal from any client still holding it - one the
// captain attached elsewhere, or one left by an earlier console. Without
// it herdr refuses ("already has an attached client") and the attach exits
// at once.
func AttachArgv(herdr, session, agent string) []string {
	return []string{ResolveExec(orDefault(herdr, "herdr")), "--session", session, "agent", "attach", agent, "--takeover"}
}

// REPLArgv is the program a stage column runs to show a headless Mate
// (CON-224): its REPL, as a client of the Mate's host. A headless Mate has
// no TUI to attach to, and its host is not a pane the stage may lean on
// (CON-216). The REPL is the column's own process, so a stage that shows
// another row, or closes, ends it and leaves the host running. --in-pane:
// ^C never closes it, as it never closed the Mate's pane the stage
// attached before.
func REPLArgv(mate, workspace, project string) []string {
	return []string{ResolveExec(orDefault(mate, "mate")), "mate", "repl", project, "--workspace", workspace, "--in-pane"}
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// ResolveExec returns an absolute path for name so Ghostty's login wrapper
// (bash --noprofile --norc) can exec it. A name already absolute is kept.
// LookPath failure leaves the name unchanged.
func ResolveExec(name string) string {
	if filepath.IsAbs(name) {
		return name
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return name
	}
	if filepath.IsAbs(p) {
		return p
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}
