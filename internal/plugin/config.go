package plugin

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

var instanceIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Config is a validated plugins.yaml with the manifest of every enabled
// instance loaded.
type Config struct {
	// File is the absolute path of plugins.yaml; empty when it is absent.
	File      string
	Instances []Instance
}

// Instance is one entry under instances, in file order.
type Instance struct {
	ID string
	// Path is the absolute package root.
	Path    string
	Enabled bool
	// Manifest is nil for a disabled instance: its package is not read.
	Manifest *Manifest
	// Workspace applies to every current and future project.
	Workspace *Binding
	// Projects is the legacy project-scoped configuration.
	Projects map[string]Binding
	// Tracker is set when the instance is the workspace's task tracker
	// (CON-77): `mate backlog --tracker` then keeps only a pointer to the
	// tracker's own id, and `mate recall` cross-checks those pointers
	// through Tracker.Read when there is one.
	Tracker *Tracker
}

// Tracker is an instance's `tracker:` block in plugins.yaml, the one hook
// mate itself has into a task tracker. It is the captain's, not the
// package's, because reading a tracker from the binary needs a local
// command (a CLI, a script) rather than the MCP server the Mate logs in
// to. Read is that command's argv, or nil when the captain gave none: the
// block alone still says "this project's tasks live in the tracker".
type Tracker struct {
	// Read prints what the tracker knows about one id: its argv, with
	// `{id}` replaced by the id in any argument, or the id appended when no
	// argument carries `{id}`. Exit 0 and a first line of stdout is the
	// answer; a non-zero exit is "unknown to the tracker". It runs in the
	// package root.
	Read []string
	// Land records a landed task in the tracker (CON-193): its argv, with
	// `{id}` replaced the way Read's is, run by `mate land` once the crew's
	// work is merged and the crew closed. It gets the task's facts in its
	// environment (MATE_TRACKER_ID, MATE_HANDBACK, MATE_MERGE_COMMIT, ...;
	// docs/plugins.md) and does what the tracker wants done at landing:
	// the state, a comment, the hand-back's screenshots. Nil when the
	// captain gave none, and `mate land` then says the tracker was not
	// updated.
	Land []string
}

// TrackerFor is the tracker instance project receives: the first enabled
// instance bound to it whose plugins.yaml entry has a `tracker:` block.
func (c Config) TrackerFor(project string) (Instance, bool) {
	for _, in := range c.Instances {
		if _, ok := in.BindingFor(project); !ok || !in.Enabled || in.Tracker == nil {
			continue
		}
		return in, true
	}
	return Instance{}, false
}

// Binding holds workspace configuration or a legacy project override.
type Binding struct {
	Config map[string]string
	Key    string
	// OpenURL is the package's open.url with this binding's config
	// substituted; empty when the package declares no open link or the
	// instance is disabled.
	OpenURL string
}

type rawConfig struct {
	APIVersion string                 `yaml:"apiVersion"`
	Instances  map[string]rawInstance `yaml:"instances"`
}

type rawInstance struct {
	Path     string                 `yaml:"path"`
	Enabled  *bool                  `yaml:"enabled"`
	Config   map[string]string      `yaml:"config"`
	Key      string                 `yaml:"key"`
	Projects map[string]*rawBinding `yaml:"projects"`
	Tracker  *rawTracker            `yaml:"tracker"`
}

type rawTracker struct {
	Read []string `yaml:"read"`
	Land []string `yaml:"land"`
}

type rawBinding struct {
	Config map[string]string `yaml:"config"`
	Key    string            `yaml:"key"`
}

// Load reads and validates <workspaceRoot>/.mate/plugins.yaml and every
// enabled instance's manifest and skills. A missing plugins.yaml is an empty
// Config. Unknown fields, extra YAML documents and invalid values are
// *ConfigError naming the file and, where known, the instance and project.
func Load(workspaceRoot string) (Config, error) {
	return readConfig(workspaceRoot, true, "")
}

// ReadConfig reads <workspaceRoot>/.mate/plugins.yaml and checks its own
// shape without reading any package: every Manifest is nil and every
// OpenURL empty. It lists what the file says even when a package is
// missing or broken; Load checks everything a Mate start reads, and
// Resolve is what a Mate start runs.
func ReadConfig(workspaceRoot string) (Config, error) {
	return readConfig(workspaceRoot, false, "")
}

// readConfig reads plugins.yaml; see parseConfig for packages and only.
func readConfig(workspaceRoot string, packages bool, only string) (Config, error) {
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return Config{}, err
	}
	file := filepath.Join(root, filepath.FromSlash(ConfigFile))
	data, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, &ConfigError{File: file, Msg: err.Error()}
	}
	return parseConfig(root, file, data, packages, only)
}

// parseConfig validates data as the plugins.yaml at file in the workspace
// root. With packages, every enabled instance's manifest, skills, open link
// and console key are checked as well.
//
// A non-empty only narrows both the check and the result to what project
// only receives: the file itself (YAML, apiVersion, fields outside any
// instance, duplicate instance ids), every workspace instance, and the
// legacy instances bound to only, with that one binding. A problem that lies
// wholly in another project's binding, or in a legacy instance only is not
// bound to, is not this project's to refuse.
func parseConfig(root, file string, data []byte, packages bool, only string) (Config, error) {
	fail := func(instance, project, format string, args ...any) error {
		return &ConfigError{File: file, Instance: instance, Project: project, Msg: fmt.Sprintf(format, args...)}
	}

	lay, err := readLayout(file, data)
	if err != nil {
		return Config{}, err
	}
	var raw rawConfig
	if err := decodeStrict(data, &raw); err != nil && (only == "" || lay.concerns(err, only)) {
		instance, project := lay.locate(err)
		return Config{}, fail(instance, project, "%v", err)
	}
	if raw.APIVersion != configAPIVersion {
		return Config{}, fail("", "", "apiVersion must be %s, got %q", configAPIVersion, raw.APIVersion)
	}

	cfg := Config{File: file}
	for _, id := range lay.ids {
		ri := raw.Instances[id]
		global := lay.workspace[id]
		if _, bound := ri.Projects[only]; only != "" && !bound && !global {
			continue
		}
		if !instanceIDRe.MatchString(id) {
			return Config{}, fail(id, "", "instance id must match [a-z0-9][a-z0-9-]*")
		}
		if strings.TrimSpace(ri.Path) == "" {
			return Config{}, fail(id, "", "path is required")
		}
		in := Instance{ID: id, Path: ri.Path, Enabled: ri.Enabled == nil || *ri.Enabled, Projects: map[string]Binding{}}
		if ri.Tracker != nil {
			if len(ri.Tracker.Read) > 0 && strings.TrimSpace(ri.Tracker.Read[0]) == "" {
				return Config{}, fail(id, "", "tracker.read: the command name is empty")
			}
			if len(ri.Tracker.Land) > 0 && strings.TrimSpace(ri.Tracker.Land[0]) == "" {
				return Config{}, fail(id, "", "tracker.land: the command name is empty")
			}
			in.Tracker = &Tracker{Read: append([]string(nil), ri.Tracker.Read...), Land: append([]string(nil), ri.Tracker.Land...)}
		}
		if !filepath.IsAbs(in.Path) {
			in.Path = filepath.Join(root, in.Path)
		}
		in.Path = filepath.Clean(in.Path)
		if in.Enabled && packages {
			if st, err := os.Stat(in.Path); err != nil || !st.IsDir() {
				return Config{}, fail(id, "", "package path %s is not a directory", in.Path)
			}
			if in.Manifest, err = loadManifest(in.Path, id); err != nil {
				return Config{}, err
			}
		}
		bindings := ri.Projects
		if _, exists := bindings[""]; exists {
			return Config{}, fail(id, "", "project name must be non-empty without surrounding spaces")
		}
		if global {
			bindings = make(map[string]*rawBinding, len(ri.Projects)+1)
			for name, binding := range ri.Projects {
				bindings[name] = binding
			}
			bindings[""] = &rawBinding{Config: ri.Config, Key: ri.Key}
		}
		for _, project := range sortedKeys(bindings) {
			if only != "" && project != only && project != "" {
				continue
			}
			rb := bindings[project]
			if rb == nil {
				rb = &rawBinding{}
			}
			if (project == "" && !global) || strings.TrimSpace(project) != project {
				return Config{}, fail(id, project, "project name must be non-empty without surrounding spaces")
			}
			b := Binding{Config: rb.Config, Key: rb.Key}
			if b.Config == nil {
				b.Config = map[string]string{}
			}
			if rb.Key != "" && strings.TrimSpace(rb.Key) != rb.Key {
				return Config{}, fail(id, project, "key %q has surrounding spaces", rb.Key)
			}
			if in.Enabled && packages {
				if b.Key != "" && in.Manifest.Open == nil {
					return Config{}, fail(id, project, "key %q needs an open link, but %s declares none", b.Key, filepath.Join(in.Path, ManifestFile))
				}
				if in.Manifest.Open != nil {
					if b.OpenURL, err = expandURL(in.Manifest.Open.URL, b.Config); err != nil {
						return Config{}, fail(id, project, "open.url: %v", err)
					}
				}
			}
			if project == "" {
				in.Workspace = &b
			} else {
				in.Projects[project] = b
			}
		}
		cfg.Instances = append(cfg.Instances, in)
	}
	if packages {
		contexts := map[string]bool{"": true}
		for _, in := range cfg.Instances {
			for project := range in.Projects {
				contexts[project] = true
			}
		}
		for _, project := range sortedKeys(contexts) {
			owners := map[string]string{}
			for _, in := range cfg.Instances {
				b, ok := in.BindingFor(project)
				if !ok || !in.Enabled || b.Key == "" {
					continue
				}
				if owner, dup := owners[b.Key]; dup {
					return Config{}, fail(in.ID, project, "duplicate key %q, already used by instance %s", b.Key, owner)
				}
				owners[b.Key] = in.ID
			}
		}
	}
	return cfg, nil
}

// decodeStrict decodes exactly one YAML document, refusing unknown fields
// and duplicate mapping keys. A *yaml.TypeError (an unknown field or a
// value of the wrong type) still leaves out holding every other value.
func decodeStrict(data []byte, out any) error {
	d := yaml.NewDecoder(bytes.NewReader(data))
	d.KnownFields(true)
	err := d.Decode(out)
	if err == io.EOF {
		err = nil
	}
	var typeErr *yaml.TypeError
	if err != nil && !errors.As(err, &typeErr) {
		return err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("expected one YAML document")
	}
	return err
}

// layout records the order of instance ids in plugins.yaml and the lines
// each instance and project binding spans, so decode errors can name them.
type layout struct {
	ids       []string
	spans     []span
	workspace map[string]bool
}

type span struct {
	instance, project string
	start, end        int
}

func readLayout(file string, data []byte) (layout, error) {
	var doc yaml.Node
	l := layout{workspace: map[string]bool{}}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return l, &ConfigError{File: file, Msg: err.Error()}
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return l, nil // decodeStrict reports the shape error
	}
	top := doc.Content[0]
	for i := 0; i+1 < len(top.Content); i += 2 {
		if top.Content[i].Value != "instances" || top.Content[i+1].Kind != yaml.MappingNode {
			continue
		}
		m := top.Content[i+1]
		seen := map[string]int{}
		for j := 0; j+1 < len(m.Content); j += 2 {
			k, v := m.Content[j], m.Content[j+1]
			if first, dup := seen[k.Value]; dup {
				return l, &ConfigError{File: file, Instance: k.Value,
					Msg: fmt.Sprintf("duplicate instance id (lines %d and %d)", first, k.Line)}
			}
			seen[k.Value] = k.Line
			l.ids = append(l.ids, k.Value)
			l.spans = append(l.spans, span{instance: k.Value, start: k.Line, end: lastLine(v)})
			l.workspace[k.Value] = true
			if v.Kind != yaml.MappingNode {
				continue
			}
			for f := 0; f+1 < len(v.Content); f += 2 {
				if v.Content[f].Value == "projects" {
					l.workspace[k.Value] = false
				}
			}
			for f := 0; f+1 < len(v.Content); f += 2 {
				if v.Content[f].Value == "config" || v.Content[f].Value == "key" {
					l.workspace[k.Value] = true
				}
			}
			for f := 0; f+1 < len(v.Content); f += 2 {
				if v.Content[f].Value != "projects" || v.Content[f+1].Kind != yaml.MappingNode {
					continue
				}
				pm := v.Content[f+1]
				for p := 0; p+1 < len(pm.Content); p += 2 {
					l.spans = append(l.spans, span{instance: k.Value, project: pm.Content[p].Value,
						start: pm.Content[p].Line, end: lastLine(pm.Content[p+1])})
				}
			}
		}
	}
	return l, nil
}

// concerns reports whether a decode error touches what project receives:
// any part of it outside every instance, in an instance bound to project
// but outside its bindings, or in project's own binding. Only a
// *yaml.TypeError can be narrowed; any other error concerns every project.
func (l layout) concerns(err error, project string) bool {
	var typeErr *yaml.TypeError
	if !errors.As(err, &typeErr) {
		return true
	}
	for _, msg := range typeErr.Errors {
		instance, p := l.locate(errors.New(msg))
		switch {
		case instance == "", p == project:
			return true
		case p == "" && l.binds(instance, project):
			return true
		}
	}
	return false
}

// binds reports whether instance has a binding for project.
func (l layout) binds(instance, project string) bool {
	if l.workspace[instance] {
		return true
	}
	for _, s := range l.spans {
		if s.instance == instance && s.project == project {
			return true
		}
	}
	return false
}

func lastLine(n *yaml.Node) int {
	line := n.Line
	for _, c := range n.Content {
		line = max(line, lastLine(c))
	}
	return line
}

var errLineRe = regexp.MustCompile(`line (\d+):`)

// locate names the instance and project whose entry contains the first line
// reported by a yaml error; both are empty outside any instance.
func (l layout) locate(err error) (instance, project string) {
	m := errLineRe.FindStringSubmatch(err.Error())
	if m == nil {
		return "", ""
	}
	line, _ := strconv.Atoi(m[1])
	for _, s := range l.spans {
		if line >= s.start && line <= s.end {
			instance = s.instance
			if s.project != "" {
				project = s.project
			}
		}
	}
	return instance, project
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
