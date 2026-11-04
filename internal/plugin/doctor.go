package plugin

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// DoctorOptions narrows what Doctor checks and hands it what only the
// caller knows. Every func may be nil, which skips that check.
type DoctorOptions struct {
	// Instance and Project, when set, limit the checks to that instance
	// and to bindings of that project.
	Instance string
	Project  string
	// Projects are the workspace's projects; a binding to any other fails.
	Projects []string
	// Harness names the harness a Mate of project runs on and where that
	// came from; an empty kind is unknown, which is not a failure.
	Harness func(project string) (kind, source string)
	// MCPConfig is nil when a Mate on kind can be given MCP servers, and
	// otherwise says why not.
	MCPConfig func(kind string) error
	// ValidateMCP checks the resolved server against the adapter offline.
	ValidateMCP func(kind, name string, server MCP) error
	// KeyOwner names what outside plugins.yaml already owns a console key
	// on project rows, or "" when the key is free.
	KeyOwner func(key string) string
	// LookPath finds a bare command name; nil is exec.LookPath.
	LookPath func(file string) (string, error)
}

// Check is one doctor result. Subject is "plugins.yaml", an instance id,
// "<instance>/<project>", or "<instance>/workspace" for the default harness
// future projects start with.
type Check struct {
	Subject string
	Name    string
	// Err is why the check failed; empty when it passed.
	Err string
	// Note is what a passing check found, when worth saying.
	Note string
}

// OK reports whether the check passed.
func (c Check) OK() bool { return c.Err == "" }

func (c Check) String() string {
	if !c.OK() {
		return fmt.Sprintf("%s: %s: fail: %s", c.Subject, c.Name, c.Err)
	}
	if c.Note != "" {
		return fmt.Sprintf("%s: %s: ok (%s)", c.Subject, c.Name, c.Note)
	}
	return fmt.Sprintf("%s: %s: ok", c.Subject, c.Name)
}

// Doctor checks <workspaceRoot>/.mate/plugins.yaml offline: that a Mate
// start would load it, and per enabled instance its package, manifest,
// skill files, MCP command, and per binding its open link, console key,
// project and harness. It reads files and looks commands up on PATH; it
// never logs in, contacts a server, or runs a command or tool.
func Doctor(workspaceRoot string, opts DoctorOptions) []Check {
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return []Check{{Subject: "plugins.yaml", Name: "valid", Err: err.Error()}}
	}
	lookPath := opts.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	var out []Check
	add := func(subject, name string, err error, note string) bool {
		c := Check{Subject: subject, Name: name, Note: note}
		if err != nil {
			c.Err, c.Note = err.Error(), ""
		}
		out = append(out, c)
		return err == nil
	}

	cfg, err := ReadConfig(root)
	if err != nil {
		add("plugins.yaml", "valid", err, "")
		return out
	}
	if cfg.File == "" {
		add("plugins.yaml", "valid", nil, "absent: no plugins configured")
		return out
	}
	_, loadErr := Load(root)
	add("plugins.yaml", "valid", loadErr, fmt.Sprintf("%d instances; a Mate start loads it", len(cfg.Instances)))

	found, bound := false, false
	for _, in := range cfg.Instances {
		if opts.Instance != "" && in.ID != opts.Instance {
			continue
		}
		found = true
		projects := sortedKeys(in.Projects)
		if in.Workspace != nil {
			// Check the default harness for future projects as well as every
			// current project's potentially different harness.
			projects = append([]string{""}, opts.Projects...)
		}
		if opts.Project != "" {
			if _, ok := in.BindingFor(opts.Project); !ok {
				if opts.Instance != "" {
					add(in.ID, "project "+opts.Project, errors.New("the instance has no binding for this project"), "")
				}
				continue
			}
			projects = []string{opts.Project}
		}
		bound = true
		if !in.Enabled {
			add(in.ID, "enabled", nil, "enabled: false; not read, gives nothing")
			continue
		}
		out = append(out, doctorInstance(in, projects, cfg, opts, lookPath)...)
	}
	if opts.Instance != "" && !found {
		add(opts.Instance, "instance", fmt.Errorf("no instance %s in %s", opts.Instance, cfg.File), "")
	} else if opts.Project != "" && opts.Instance == "" && !bound {
		add("plugins.yaml", "project "+opts.Project, nil, "no instance binds it")
	}
	return out
}

func doctorInstance(in Instance, projects []string, cfg Config, opts DoctorOptions, lookPath func(string) (string, error)) []Check {
	var out []Check
	add := func(subject, name string, err error, note string) bool {
		c := Check{Subject: subject, Name: name, Note: note}
		if err != nil {
			c.Err, c.Note = err.Error(), ""
		}
		out = append(out, c)
		return err == nil
	}
	if st, err := os.Stat(in.Path); err != nil || !st.IsDir() {
		add(in.ID, "package "+in.Path, errors.New("not a directory"), "")
		return out
	}
	add(in.ID, "package "+in.Path, nil, "")
	m, skills, err := parseManifest(in.Path, in.ID)
	if !add(in.ID, "manifest", err, "") {
		return out
	}
	for _, rel := range skills {
		_, err := loadSkill(in.Path, rel, in.ID)
		add(in.ID, "skill "+rel, err, "")
	}
	if m.MCP != nil {
		switch m.MCP.Type {
		case "http":
			add(in.ID, "mcp http "+m.MCP.URL, nil, "not contacted")
		case "stdio":
			path, err := findCommand(m.MCP.Command[0], m.MCP.Cwd, lookPath)
			add(in.ID, "mcp command "+m.MCP.Command[0], err, path)
		}
	}
	if in.Tracker != nil {
		if len(in.Tracker.Read) == 0 {
			add(in.ID, "tracker", nil, "no read command; recall cannot cross-check In flight")
		} else {
			path, err := findCommand(in.Tracker.Read[0], in.Path, lookPath)
			add(in.ID, "tracker read "+in.Tracker.Read[0], err, path)
		}
		if len(in.Tracker.Land) == 0 {
			add(in.ID, "tracker", nil, "no land command; mate land leaves the tracker to the Mate")
		} else {
			path, err := findCommand(in.Tracker.Land[0], in.Path, lookPath)
			add(in.ID, "tracker land "+in.Tracker.Land[0], err, path)
		}
	}

	for _, project := range projects {
		b, _ := in.BindingFor(project)
		subject := in.ID + "/" + project
		if project == "" {
			subject = in.ID + "/workspace"
		}
		if project != "" && opts.Projects != nil && !slices.Contains(opts.Projects, project) {
			add(subject, "project", errors.New("not a project of this workspace"), "")
		}
		if m.Open != nil {
			u, err := expandURL(m.Open.URL, b.Config)
			if missing := missingKeys(m.Open.URL, b.Config); len(missing) > 0 {
				err = fmt.Errorf("%s needs config %s, which the binding does not set", m.Open.URL, strings.Join(missing, ", "))
			}
			add(subject, "open.url", err, u)
		}
		if b.Key != "" {
			add(subject, "key "+b.Key, keyProblem(in, project, b.Key, m, cfg, opts.KeyOwner), "")
		}
		if m.MCP != nil {
			kind, source := "", ""
			if opts.Harness != nil {
				kind, source = opts.Harness(project)
			}
			switch {
			case kind == "":
				add(subject, "harness", nil, "unknown: no Mate recorded and no workspace default")
			case opts.MCPConfig == nil:
				add(subject, "harness "+kind, nil, source+"; support not checked")
			default:
				err := opts.MCPConfig(kind)
				if err == nil && opts.ValidateMCP != nil {
					err = opts.ValidateMCP(kind, in.ID, *m.MCP)
				}
				add(subject, "harness "+kind, err, source)
			}
		}
	}
	return out
}

// keyProblem is why key cannot be the console shortcut of instance in on
// project, or nil.
func keyProblem(in Instance, project, key string, m *Manifest, cfg Config, owner func(string) string) error {
	if m.Open == nil {
		return fmt.Errorf("a key needs an open link, but %s declares none", m.File)
	}
	for _, other := range cfg.Instances {
		if other.ID == in.ID || !other.Enabled {
			continue
		}
		if ob, ok := other.BindingFor(project); ok && ob.Key == key {
			if project == "" {
				return fmt.Errorf("instance %s binds the same key for the workspace", other.ID)
			}
			return fmt.Errorf("instance %s binds the same key on this project", other.ID)
		}
	}
	if owner != nil {
		if o := owner(key); o != "" {
			return errors.New(o)
		}
	}
	return nil
}

// findCommand resolves an MCP stdio command[0] the way a launch runs it: a
// name with a slash is a file relative to the server's cwd (absolute; the
// package root when the manifest sets none) and must be executable; a bare
// name is looked up on PATH.
func findCommand(name, cwd string, lookPath func(string) (string, error)) (string, error) {
	if !strings.Contains(name, "/") {
		p, err := lookPath(name)
		if err != nil {
			return "", fmt.Errorf("%s is not on PATH", name)
		}
		return p, nil
	}
	p := name
	if !filepath.IsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	st, err := os.Stat(p)
	switch {
	case err != nil:
		return "", fmt.Errorf("%s does not exist", p)
	case !st.Mode().IsRegular():
		return "", fmt.Errorf("%s is not a regular file", p)
	case st.Mode().Perm()&0o111 == 0:
		return "", fmt.Errorf("%s is not executable", p)
	}
	return p, nil
}
