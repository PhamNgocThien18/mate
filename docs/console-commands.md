# Console commands and editors

Mate does not choose an editor. Add bindings to `<workspace>/.mate/console.yaml`
and reopen the console. Without this file, no editor key or editor tab is added.
Deleting a binding removes its key on the next console start. Existing workspaces
that used the built-in Fresh `e` binding need to add a binding explicitly.

## Setup

1. Install the editor and make its CLI available. Check `code --version`,
   `zed --version`, or your chosen editor's equivalent. On macOS, VS Code's
   Command Palette has **Shell Command: Install 'code' command in PATH**.
2. Find the workspace root: the directory containing `.mate/workspace.yaml`.
   Create or edit `.mate/console.yaml` there, preserving existing bindings.
   Pick a configuration below; use an absolute executable path if needed.
3. Reopen Mate with `mate console /path/to/workspace`, select a Crew row and
   press the configured key. `?` lists the configured keys.
4. Confirm the editor opens the report or folder. If the report has not been
   written yet, use `{crew_dir}` to open the folder. A missing executable means
   the CLI needs installing on PATH or selecting by absolute path.

`mate console --help` prints setup steps and examples without opening the TUI,
including when run outside a workspace. The agent Mate's `mate-commands` skill
also teaches this setup flow and the workspace config location.

## Dashboard on the LAN

The console serves the read-only dashboard on `127.0.0.1:7777`, reachable
only from the same machine. To open it from a phone or another computer on
the same network, bind every interface and say that you mean it:

```yaml
dashboard:
  addr: 0.0.0.0:7777
  allow_remote: true
```

`allow_remote` is required for any address that is not loopback: the
dashboard has no login and quotes everything typed in the console. Without
it `mate core` starts, the console says `dashboard not started` on its status
line, and nothing is served over TCP. With it the status line shows both URLs, the loopback one
and the LAN one (the machine's private IPv4, Wi-Fi on a laptop); open the
LAN one on the phone. The port stays whatever `addr` says. `mate core` reads
the setting when it starts (CON-214), so run `mate core restart` after
changing it.

## Zed

```yaml
bindings:
  - key: e
    scope: crew
    label: report
    command: [zed, "{report_path}"]
    mode: external
```

Install the editor's CLI on PATH, or use its absolute executable path. GUI
launchers use `external`: Mate captures their output and reports launch errors
in the console, without opening a terminal tab. They work even without a
supported terminal host. The launcher must return within 10 seconds; do not
add `--wait`. The GUI application keeps its own lifetime after launch.

To open the code instead - a crew's worktree on a crew row, the project's
folder on the Mate or project row - bind the same key on both scopes, the
crew binding first. A key runs the first of its bindings that fits the row,
and a project binding fits a crew row too:

```yaml
bindings:
  - key: e
    scope: crew
    label: code
    command: [zed, "{worktree_dir}"]
    mode: external
  - key: e
    scope: project
    label: code
    command: [zed, "{project_dir}"]
    mode: external
```

## VS Code

Use the same binding with:

```yaml
    command: [code, --reuse-window, "{report_path}"]
    mode: external
```

## A terminal editor

```yaml
bindings:
  - key: e
    scope: crew
    label: report
    command: [nvim, "{report_path}"]
    cwd: "{crew_dir}"
    mode: terminal
```

`terminal` is the default mode. It needs a supported host (WezTerm or Ghostty),
opens/reuses a tab for this binding and leaves the agent pane intact. Mate owns
the tab and its processes; quitting the console closes it. To use Fresh, change
the command to `[fresh, "{report_path}"]`. There is no special Fresh integration.

## Fields and paths

| Field | Meaning |
| --- | --- |
| `key` | A console key, for example `e`, `v` or `ctrl+e`; case matters |
| `scope` | `crew` or `project`, the tree row where the key acts |
| `label` | Text shown on the key line and in help |
| `command` | Nonempty YAML list: executable followed by its arguments |
| `cwd` | Working directory; defaults to the crew/project directory for the scope |
| `mode` | `terminal` (default) or `external` |

Paths available in arguments and `cwd`:

| Variable | Meaning |
| --- | --- |
| `{workspace_dir}` | Workspace root |
| `{project_dir}` | Project's directory under the workspace root |
| `{crew_dir}` | Crew's persistent folder under `.mate/projects/<p>/crews/<id>`; crew scope only |
| `{report_path}` | Existing `report.md` in that folder; crew scope only |
| `{worktree_dir}` | Crew's git worktree, as its meta records it, while it is on disk; crew scope only |

If a report is not written yet, a binding using `{report_path}` reports that
it is unavailable and does not launch. Use `{crew_dir}` instead to open the
folder, including its brief and handback. Stopped crews keep their folder and
report. A finished crew whose worktree was removed has no `{worktree_dir}`;
the key says so and launches nothing. Relative `cwd` values are relative to the workspace root.

Arguments are passed directly, without a shell: spaces and shell characters in
paths stay intact. `~`, `$EDITOR`, shell quoting and command substitution are
not expanded. Specify an absolute path rather than `~/...`. If a shell is
needed, name it explicitly in `command`, such as `[sh, -c, "..."]`; brace
expressions in strings are reserved for the path variables above.

Multiple bindings are supported. Reserved console keys (such as Enter, `r`,
`s` and `q`) and duplicate keys within one scope are refused. There are no built-in application keys; `t` can be assigned to any project
command. The same key can
be bound once on crew rows and once on project rows. Unknown fields, path
variables, scopes and modes are configuration errors reported before console
startup. A missing executable is reported when its key is pressed.

## Plugin links

A plugin instance bound to a project in `.mate/plugins.yaml` can give that
project an "open" link with a console `key`. The key appears on project rows
like any configured command and opens the URL with the machine's opener
(`open` on macOS, `xdg-open` on Linux); no terminal tab is created. Pressing
the key on a project without that binding reports an error. A key that
`console.yaml` binds on project rows, or that the console reserves, is refused
before startup, naming `plugins.yaml`, the instance and the key. Without
`plugins.yaml` nothing changes. See `docs/plans/plugins-and-office-2026-10-12.md`.
