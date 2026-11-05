# External task management

Mate orchestrates agents. Choose any task manager and teach Mate its workflow
through `.mate/WORKSPACE.md`, `.mate/projects/<project>/PROJECT.md` or a
user-managed harness skill. No task manager, tracker directory, task-status
schema or task shortcut is installed by Mate. Without a chosen tracker, Mate
keeps requested work in its `backlog.md`.

To let Mate itself read or update the tracker, install it as a plugin whose
`plugins.yaml` entry has a `tracker:` block. `mate recall` then cross-checks
In flight backlog pointers through its `read` command, and `mate land` marks a
landed task through its `land` command. See
[A tracker plugin](plugins.md#a-tracker-plugin).

## Setup for any application

1. Install/configure the application independently. Choose the tracker location
   or account/project explicitly; Mate's cwd is its own state folder, not the
   tracker. For a shared local tracker, a directory under the project and outside
   `.mate/`, code repos and Crew worktrees avoids losing it during teardown.
2. Put the workflow in the captain's instructions: the CLI/API or skill to read,
   how to find/create/assign tasks, allowed states, who updates them and when
   delivery is accepted. Include task IDs in briefs. A Crew hand-back changes
   no external task; only a tracker plugin's `land` command does, from
   `mate land`.
3. If desired, add a console command binding using `mate console --help`.
   Opening the application's UI and teaching the agent its commands are
   separate settings. Mate neither initializes nor refreshes tracker data.
4. Test a read and a single authorized update with the application's own
   commands. Confirm the UI reflects it. Reopen the console after changing
   bindings; start/restart Mate to regenerate its built-in instructions.

## Local suggestion: Backlog.md

[Backlog.md](https://github.com/MrLesk/Backlog.md) stores Markdown tasks locally
and provides a local web Kanban, forms, CLI/JSON and MCP. This is a user choice,
not a Mate dependency. The recommendation comes from official documentation
and source; installation and live usage have not been verified here.

```sh
npm i -g backlog.md
cd /absolute/path/to/workspace/my-project
backlog init "My project" --no-git
backlog instructions overview
backlog browser
```

The `--no-git` setup keeps the tracker filesystem-only without remote Git
operations or automatic commits. Use one tracker for the project, shared by
its repos. Set Mate as the sole dispatcher unless you have verified the chosen
tool's claim/concurrency behavior.

Add a prompt to that project's `PROJECT.md`, substituting the actual path:

```text
Task manager: Backlog.md at /absolute/path/to/workspace/my-project.
Run backlog commands with cwd at that directory and read
`backlog instructions overview` before managing tasks.
You own task assignment and status updates; Crew reports results to you.
Record the task ID in each Crew brief and mark work In Progress before dispatch.
Set Done only after accepted review and the change has landed, or the scout
deliverable has been accepted. Read a task again after a write conflict.
```

Optional board binding in `.mate/console.yaml`:

```yaml
bindings:
  - key: t
    scope: project
    label: board
    command: [backlog, browser]
    cwd: "{project_dir}"
    mode: terminal
```

This runs the long-lived local browser server in a managed terminal tab, so
quitting the console stops that server. `external` is for short GUI launchers;
use it to open an already-running board URL, not to run its server. No
application binding is supplied by Mate.

[Research and primary sources](archive/research/local-task-management-2026-10-12.md)
cover the installation, UI, storage and concurrency limits in more detail.

## Existing Beads users

Mate has no Beads integration. The `mate tool beads ...`, `mate beads`,
`mate tasks` and `mate task-triage` commands, the generated task-management
skill, console `t` and automatic task recall do not exist. Configure your own
command and workflow instead. Existing `.beads/` data, exports, legacy
`tasks.yaml` and lock files are left where they are. Mate converts or deletes
no task records, and never uninstalls `bd` or `bv`. The old integration is
archived as [dated evidence](archive/evidence/beads-integration-2026-10-07.md).

Before using a new Mate session, inspect the previously generated
`<workspace>/.mate/projects/<project>/mate/.claude/skills/task-management/SKILL.md`
if present (Claude and Codex used that location). If it still calls
`mate tool beads`, remove that obsolete generated skill or replace it with
your selected workflow using the application's own commands. Check captain
instructions and other custom skills for old wrapper references too. Mate
preserves custom skill files, so restarting alone will not remove that file.

If continuing to use Beads, configure its tracker path explicitly and follow
the installed `bd`/`bv` documentation. Mate does no locking or export refresh
for it. If switching to another application, retain
the Beads store until you have verified an independent export/import and the
task counts, statuses and dependencies. No migration is performed here.
