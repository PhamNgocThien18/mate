package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nguyenngocanh94/mate/internal/consolecmd"
	"github.com/nguyenngocanh94/mate/internal/observability"
	"github.com/nguyenngocanh94/mate/internal/plugin"
	"github.com/nguyenngocanh94/mate/internal/spawn"
	"github.com/nguyenngocanh94/mate/internal/store"
)

const (
	pluginAddUsage    = "mate plugin add <package-path> --as <instance-id> [--set key=value ...] [--key <console-key>] [--workspace <dir>]"
	pluginDoctorUsage = "mate plugin doctor [<instance-id>] [--project <project>] [--workspace <dir>]"
	pluginListUsage   = "mate plugin list [--workspace <dir>]"
)

const pluginHelp = `usage:
  ` + pluginAddUsage + `
  ` + pluginDoctorUsage + `
  ` + pluginListUsage + `

A plugin is a local package with mate-plugin.yaml: MCP servers, skills and
an open link are optional. Install it once in <workspace>/.mate/plugins.yaml;
every project's Mate receives it on its next start, including future projects.
Crews receive no plugins. Configuration changes never restart running agents.

Setup:
  1. mate plugin add <package-path> --as <instance-id> [--set key=value ...]
  2. mate plugin doctor <instance-id>
  3. restart the Mate(s) that should receive it
  4. if the package has an MCP server, run /mcp in the Mate's pane and log in;
     verify one read through its tools.

add writes workspace config and an optional console key, replacing that
instance's config/key while keeping other instances. The package path of an
existing instance changes only by hand. Only the captain runs add.
The file keeps its order and most comments stay; blank lines and indentation
may change. Legacy --project installs and projects maps remain readable;
re-running add without --project explicitly promotes that instance to workspace.

list prints instances with their workspace config and any legacy project
bindings. doctor checks files, commands, links, keys and harness
compatibility offline; it never logs in, contacts a server or calls a tool. --project on doctor only selects the Mate harness to check,
not the plugin's installation scope. Exit 1 on any failed check.
`

// cmdPlugin dispatches `mate plugin <add|doctor|list>`.
func cmdPlugin(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprint(stderr, pluginHelp)
		return newUsageError("usage: mate plugin <add|doctor|list> ...")
	}
	switch args[0] {
	case "--help", "-h", "help":
		fmt.Fprint(stdout, pluginHelp)
		return nil
	case "add":
		return cmdPluginAdd(args[1:], stdout, stderr)
	case "doctor":
		return cmdPluginDoctor(args[1:], stdout, stderr)
	case "list":
		return cmdPluginList(args[1:], stdout, stderr)
	default:
		return newUsageErrorf("unknown plugin subcommand %q (want add, doctor or list; mate plugin --help)", args[0])
	}
}

// setFlags is the repeatable --set key=value.
type setFlags map[string]string

func (s setFlags) String() string { return "" }

func (s setFlags) Set(v string) error {
	k, val, ok := strings.Cut(v, "=")
	if !ok || k == "" || strings.TrimSpace(k) != k {
		return fmt.Errorf("want key=value, got %q", v)
	}
	if _, dup := s[k]; dup {
		return fmt.Errorf("key %s is set twice", k)
	}
	s[k] = val
	return nil
}

func cmdPluginAdd(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("plugin add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintln(stderr, "usage: "+pluginAddUsage) }
	as := fs.String("as", "", "instance id, which also names the MCP server")
	project := fs.String("project", "", "legacy project binding (omit for workspace installation)")
	key := fs.String("key", "", "optional console shortcut on the project row")
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	set := setFlags{}
	fs.Var(set, "set", "binding config key=value (repeat)")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return newUsageError("mate plugin add: want exactly 1 argument: <package-path>")
	}
	if *as == "" {
		fs.Usage()
		return newUsageError("mate plugin add: --as is required")
	}
	if caller := spawn.CallerFromEnv(); caller != spawn.CallerUser {
		return observability.NewError(observability.CodePermission,
			fmt.Sprintf("the captain runs mate plugin add; a %s cannot change .mate/plugins.yaml (mate plugin doctor and list are open to it)", caller))
	}
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	if _, ok := w.Project(*project); *project != "" && !ok {
		return fmt.Errorf("mate plugin add: %w: %s", store.ErrNoProject, *project)
	}
	pkg, err := filepath.Abs(fs.Arg(0))
	if err != nil {
		return err
	}
	if *key != "" {
		if owner := pluginKeyOwner(w)(*key); owner != "" {
			return fmt.Errorf("mate plugin add: key %q is %s; choose another --key; nothing written", *key, owner)
		}
	}
	res, err := plugin.AddBinding(w.Root(), plugin.AddRequest{
		Instance: *as, Path: pkg, Project: *project, Config: set, Key: *key,
	})
	var missing *plugin.MissingConfigError
	if errors.As(err, &missing) {
		var flags []string
		for _, k := range missing.Keys {
			flags = append(flags, "--set "+k+"=<value>")
		}
		return fmt.Errorf("mate plugin add: %s open.url %q needs %s; nothing written", missing.Manifest, missing.URL, strings.Join(flags, " "))
	}
	if err != nil {
		return fmt.Errorf("mate plugin add: %w; nothing written", err)
	}
	printAdded(stdout, res, *as, *project)
	return nil
}

func printAdded(stdout io.Writer, res plugin.AddResult, id, project string) {
	if project == "" {
		what := "added"
		if res.Replaced {
			what = "replaced"
		}
		fmt.Fprintf(stdout, "wrote %s: %s workspace instance %s (%s)\n", res.File, what, id, res.Manifest.Name)
		if len(res.Promoted) > 0 {
			fmt.Fprintf(stdout, "note: replaced the legacy bindings of %s with this workspace config\n", strings.Join(res.Promoted, ", "))
		}
		if res.Disabled {
			fmt.Fprintln(stdout, "note: instance is disabled; enable it in plugins.yaml to use it")
		}
		fmt.Fprintln(stdout, "restart each Mate that should receive the plugin; new Mate starts inherit it automatically")
		if res.Manifest.MCP != nil {
			fmt.Fprintf(stdout, "then run /mcp in the Mate's pane and log in to %s; verify one read through its tools\n", id)
		}
		fmt.Fprintf(stdout, "check offline: mate plugin doctor %s\n", id)
		return
	}
	what := "added"
	if res.Replaced {
		what = "replaced"
	}
	fmt.Fprintf(stdout, "wrote %s: %s binding of instance %s (%s) to project %s\n", res.File, what, id, res.Manifest.Name, project)
	if res.Disabled {
		fmt.Fprintf(stdout, "note: instance %s is enabled: false; the binding gives nothing until it is enabled\n", id)
	}
	fmt.Fprintln(stdout, "next:")
	fmt.Fprintf(stdout, "  1. restart the Mate: mate mate stop %s, then mate mate start %s\n", project, project)
	if res.Manifest.MCP != nil {
		fmt.Fprintf(stdout, "  2. in the Mate's pane, run /mcp and log in to %s\n", id)
		fmt.Fprintf(stdout, "  3. ask the Mate for one read through %s's tools to see them answer\n", id)
	} else {
		fmt.Fprintf(stdout, "  2. %s has no MCP server: there is nothing to log in to\n", id)
		fmt.Fprintf(stdout, "  3. ask the Mate to use %s's skills, or press its console key, to see it took\n", id)
	}
	fmt.Fprintf(stdout, "check it offline first: mate plugin doctor %s --project %s\n", id, project)
}

func cmdPluginDoctor(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("plugin doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintln(stderr, "usage: "+pluginDoctorUsage) }
	project := fs.String("project", "", "check as this project's Mate receives it (its harness; not the installation scope)")
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() > 1 {
		fs.Usage()
		return newUsageError("mate plugin doctor: want at most 1 argument: <instance-id>")
	}
	w, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return err
	}
	projects := []string{} // non-nil: a workspace without projects still checks
	for _, p := range w.Projects() {
		projects = append(projects, p.Name)
	}
	checks := plugin.Doctor(w.Root(), plugin.DoctorOptions{
		Instance:    fs.Arg(0),
		Project:     *project,
		Projects:    projects,
		Harness:     pluginMateHarness(w),
		MCPConfig:   pluginHarness,
		ValidateMCP: pluginServerConfig,
		KeyOwner:    pluginKeyOwner(w),
	})
	failed := 0
	for _, c := range checks {
		fmt.Fprintln(stdout, c.String())
		if !c.OK() {
			failed++
		}
	}
	if failed > 0 {
		return fmt.Errorf("mate plugin doctor: %d of %d checks failed", failed, len(checks))
	}
	return nil
}

func cmdPluginList(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("plugin list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintln(stderr, "usage: "+pluginListUsage) }
	workspaceFlag := fs.String("workspace", "", "workspace directory")
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		return &usageError{err}
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return newUsageError("mate plugin list: takes no arguments")
	}
	dir, err := findWorkspaceDir(*workspaceFlag)
	if err != nil {
		return err
	}
	cfg, err := plugin.ReadConfig(dir)
	if err != nil {
		return fmt.Errorf("%w (mate plugin doctor says more)", err)
	}
	if cfg.File == "" {
		fmt.Fprintf(stdout, "no plugins: %s does not exist\n", plugin.ConfigFile)
		return nil
	}
	if len(cfg.Instances) == 0 {
		fmt.Fprintf(stdout, "no instances in %s\n", cfg.File)
		return nil
	}
	for _, in := range cfg.Instances {
		line := in.ID + "  " + in.Path
		if !in.Enabled {
			line += "  (disabled)"
		}
		fmt.Fprintln(stdout, line)
		if in.Workspace != nil {
			fmt.Fprintln(stdout, pluginBindingLine("workspace", *in.Workspace))
		}
		names := make([]string, 0, len(in.Projects))
		for p := range in.Projects {
			names = append(names, p)
		}
		slices.Sort(names)
		for _, p := range names {
			b := in.Projects[p]
			line := "  " + p
			if b.Key != "" {
				line += "  key " + b.Key
			}
			keys := make([]string, 0, len(b.Config))
			for k := range b.Config {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			for _, k := range keys {
				line += "  " + k + "=" + b.Config[k]
			}
			fmt.Fprintln(stdout, line)
		}
	}
	return nil
}

// pluginKeyOwner is doctor's and add's KeyOwner: what already owns a
// console key on project rows, by the console start's own rule.
func pluginKeyOwner(w *store.Workspace) func(string) string {
	cfg, err := consolecmd.Load(filepath.Join(w.StateDir(), "console.yaml"))
	return func(key string) string {
		if owner := consoleKeyOwner(consolecmd.Config{}, key); owner != "" {
			return owner
		}
		if err != nil {
			return "not checkable: " + err.Error()
		}
		return consoleKeyOwner(cfg, key)
	}
}

// pluginMateHarness is doctor's Harness: the harness of the Mate recorded
// as running (mate.meta still names its pane or its headless host), else the workspace default,
// else unknown.
func pluginMateHarness(w *store.Workspace) func(string) (string, string) {
	return func(project string) (string, string) {
		if meta, err := w.ReadMateMeta(project); err == nil && store.MateRecorded(meta) && meta[spawn.MetaHarness] != "" {
			return strings.ToLower(meta[spawn.MetaHarness]), "running Mate"
		}
		if d := strings.ToLower(strings.TrimSpace(w.Defaults().MateHarness)); d != "" {
			return d, "workspace default"
		}
		return "", ""
	}
}

func pluginBindingLine(scope string, b plugin.Binding) string {
	line := "  " + scope
	if b.Key != "" {
		line += "  key " + b.Key
	}
	keys := make([]string, 0, len(b.Config))
	for k := range b.Config {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		line += "  " + k + "=" + b.Config[k]
	}
	return line
}
