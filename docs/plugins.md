# Plugins

A plugin is a local package directory with a `mate-plugin.yaml` manifest. It
may declare an MCP server, skills and an "open" link; every block is
optional. A workspace installs packages in `.mate/plugins.yaml`. Every current and
future project's Mate receives them when it starts; Crews never do.
Installation scope is independent of the external app's teams and projects. Mate has no
package manager: it downloads, locks, caches and updates nothing. Design and
rulings: [plans/plugins-and-office-2026-10-12.md](archive/plans/plugins-and-office-2026-10-12.md).

```yaml
# /path/to/tracker-plugin/mate-plugin.yaml
apiVersion: mate.plugin/v1
name: Tracker
mcp:
  type: http                 # or stdio, with command: [argv] and cwd (default: the package root)
  url: https://mcp.tracker.example/mcp
skills:
  - skills/workflow/SKILL.md # {instance_id} is replaced by the instance id
open:
  title: Open Tracker
  url: "{config.project_url}"
```

```yaml
# <workspace>/.mate/plugins.yaml
apiVersion: mate.plugins/v1
instances:
  tracker-work:              # instance id: also the MCP server's name
    path: /path/to/tracker-plugin
    config:
      project_url: https://tracker.example/workspace
    key: t                   # optional shortcut on every console project row
```

## A tracker plugin

An instance whose entry has a `tracker:` block is the workspace's task
tracker (CON-77). That is mate's one hook into a tracker, and it is the
captain's, not the package's: the binary cannot call the MCP server the Mate
logs in to, so what it reads goes through a local command.

```yaml
instances:
  linear-work:
    path: /path/to/linear-plugin
    tracker:
      read: [linear-axi, issue, view, "{id}"]   # optional; runs in the package root
      land: [./bin/land, "{id}"]               # optional; what `mate land` runs
```

With the block, `mate backlog add|move|done --tracker <tracker-id>` keeps only
the pointer line `- <crew-id>: <tracker-id>` in `backlog.md`, and `mate recall`
part 4 runs `read` once per In flight pointer (`{id}` replaced, or the id
appended; 3 s each, 10 s in all, 20 pointers at most) and prints the first
line of its stdout, or `unknown to the tracker` on a non-zero exit. Without
`read`, recall says the pointers were not cross-checked. `doctor` looks the
command up the way it looks up a stdio server's.

`land` is what `mate land <project> <crew>` runs once the crew's work is
merged and the crew closed (CON-193): the tracker's own side of a landing -
the task's state, a comment, the hand-back's screenshots - without the Mate
spending a model call on each. It gets `{id}` the way `read` does and the
landing's facts in its environment: `MATE_PROJECT`, `MATE_CREW`,
`MATE_TRACKER_ID`, `MATE_TASK` (the crew's `task=` line), `MATE_BRANCH`,
`MATE_MERGE_COMMIT` (empty for a local fast-forward), `MATE_PR_URL`,
`MATE_HANDBACK`, `MATE_BRIEF` and `MATE_LEARNING` (paths), and `MATE_SHOTS`
(the images the hand-back names that exist on disk, one per line). It runs
in the package root with 60 s to finish; its first stdout line is what
`land` prints. A non-zero exit, or no `land` command at all, makes `land`
say `tracker: <id> not updated ...; mark it done yourself`, and the Mate
does what it did before. The tracker id is whatever leads the crew's
`task=` line (`HU-12: ...`, as crews named by the issue carry it) or the
crew's backlog pointer. `examples/plugins/linear/bin/land` is one for
Linear, driven by `LINEAR_API_KEY`.

## Install a local package in four steps

```sh
mate plugin add /path/to/tracker-plugin --as tracker-work \
  --set project_url=https://tracker.example/workspace [--key t]
mate plugin doctor tracker-work
mate mate stop <project> && mate mate start <project>  # repeat for running Mates
```

Then, in the Mate's pane, run `/mcp` and log in to `tracker-work` (only when
the package has an MCP server), and ask the Mate for one read through its
tools. That read, not `doctor`, is the proof the tools work.
Measured so far: a stdio server through `claude -p`, and Linear's http server in an interactive Mate pane: one `/mcp` OAuth login serves every Mate and survives stop/start and resume ([evidence/linear-pilot-2026-10-12.md](evidence/linear-pilot-2026-10-12.md)). A Codex, pi or grok Mate is refused at start while an enabled plugin brings an MCP server.

- `add` is the captain's (a Mate or Crew is refused). It writes one explicit
  workspace installation. `--set` is its opaque config; every key the package's
  `open.url` substitutes must be set, or nothing is written. Re-running it
  for the same instance replaces its workspace config and keeps other
  instances. An instance's `path` changes only by hand. The file is validated as
  a Mate start reads it before it is written. It is rewritten through a YAML
  node tree: order and most comments survive, blank lines and hand-chosen
  indentation do not.
- `doctor [<instance-id>] [--project <p>]` is offline and prints one
  `ok`/`fail` line per check: the file loads, package, manifest, each skill
  file, the stdio command (a name with `/` is resolved from the server's
  `cwd`, the package root unless the manifest sets one; a bare name on
  `PATH`; an http server is not contacted), `open.url`, console key, project, and whether the Mate's
  harness (the running Mate's, else the workspace default) can take MCP
  servers. It never logs in or calls a tool. Exit 1 on any failure.
- `list` prints instances, packages and workspace config; no Mate needs to run.

Editing `plugins.yaml` by hand is fine. A change reaches a Mate on its next
start, and the console or Office on reopen. `enabled: false` keeps an
instance in the file but gives nothing.

Existing `projects:` bindings and `add --project` remain readable for backward
compatibility. New installations omit `--project`; adding an existing instance
without that flag replaces its legacy bindings with one workspace configuration.
`doctor --project` selects a harness to check; it does not set installation scope.
OAuth remains owned by each harness. Workspace configuration does not promise
a single shared login across agents or harnesses.

## Codex and pi

Codex 0.162.1 and pi 1.0.4 are the measured versions for the additional
adapters ([implementation evidence](evidence/codex-pi-mcp-2026-10-13.md)).
Both accept HTTP and stdio plugins on a Mate, fresh and resumed. pi now runs
Mate through a generated hook extension and explicitly loads Mate/plugin skills.
Crews receive no workspace plugins. Restart Mate after changing plugins.

Codex receives a whole `mcp_servers` table through `-c`; a Mate uses only the
workspace's resolved servers, including an empty table after removal. The
operator's config.toml is never edited. **Behavior change:** every Codex Mate,
even a workspace with no plugins, now excludes the operator's global MCP
servers. Move required servers into workspace plugins to expose them to Mate.
This matches Claude's strict MCP isolation. A Crew without plugin servers
keeps its existing launch behavior. Codex's native OAuth manager owns login.
For an HTTP instance, invoke login with the same URL/table and CODEX_HOME:

```sh
codex -c 'mcp_servers={"linear-work"={url="https://mcp.linear.app/mcp"}}' mcp login linear-work
```

Use your package's actual URL and instance name. The same override with
`mcp get <instance>` inspects configuration; it does not prove auth or a tool read.

pi receives servers through its native `registerMcpServer` API and initializes
`createMcpExtension` from the installed pi runtime with an empty file-config
loader. Only workspace servers are registered; global/project `mcp.json` is
ignored whether or not the workspace has plugins. Use `/mcp` in the Mate pane
to sign in to an HTTP server. pi owns its OAuth cache in the selected pi agent home. The adapter does
not edit global MCP configuration or switch the operator's auth home. A global
server sharing a workspace name cannot replace the registered instance.

Offline doctor/preflight validate resolved config and harness compatibility;
they do not contact the server. Live fixture proofs and external Linear OAuth
pilot results are reported separately in the evidence; do not infer a Linear
login from a successful fixture read.

pi recall enters model context at `before_agent_start`, before the first model
turn rather than when the composer opens. The launch records its UUID, but pi
intentionally creates no session file before a user or assistant message.
Stopping before the first prompt therefore starts fresh on the next launch,
with an explicit missing-session note. A recorded conversation resumes normally.
Transcript metadata is populated by the first recall hook. The recall hook
waits at most 30 seconds, like Codex's SessionStart hook; prompt and answer
receipts block the composer and wait at most five. Every hook surfaces stderr
diagnostics even when Mate intentionally returns exit status zero.
