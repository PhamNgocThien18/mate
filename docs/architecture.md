# Architecture

What mate is made of once the fourth wave of the core split
(`refactor/con-203-wave-4`, 42c6571, 2026-11-05) is on `main`. Domain terms
(Mate, Crew, outbox, hand-back...) are defined in
[GLOSSARY.md](../GLOSSARY.md). `docs/mvp.md` is the history of how this came
to be; this file is the current map. Installing and building:
[install.md](install.md).

mate is splitting its core into a process of its own (CON-203). Done so far:
`internal/coreapi` (CON-206); the engine in `internal/core` (CON-207) with the
shared snapshot, Office/City and notifications (CON-208), and the one entry
for every UI's actions (CON-209); `internal/refresh`
(CON-170); `internal/platform` (CON-184, CON-185); `CrewIsScout` in `store`
(CON-165); the CLI command table (CON-171); `internal/wake` (CON-169);
`internal/recall` (CON-167). Every
part marked **Target** below is planned, not built; the issue named beside it
owns the change. A PR that moves a package or a process updates the
paragraph it changes.

## 1. Packages by layer

Seven layers, lowest first. Every package with a non-test `.go` file under
`cmd/`, `internal/` and `assets/` belongs to exactly one, except the
test-only packages of rule 3. The Packages column decides: a package not
listed has no layer yet, and a subpackage does not inherit its parent's
layer (`harness` is read, `harness/claude` integration; `timeline/scene` and
`timeline/read` are read, `timeline` service). The "What belongs" column
describes the layer; it does not decide it. The same table is data in code,
`layers` in `internal/archtest/layers_test.go`, and the test keeps both
equal: a new package goes into both in one change, and the test's message
names the layers its imports and importers allow.

| Layer | What belongs | Packages |
| -- | -- | -- |
| 1 leaf (lá) | Types, rules, formats and OS shims that import no other layer | `assets`, `brief`, `capability`, `config`, `consolecmd`, `crewstate`, `memory`, `names`, `observability`, `platform`, `process`, `telemetry` |
| 2 storage (lưu trữ) | The workspace store and the database and config files beside it | `store`, `db`, `plugin`, `learnings` |
| 3 read (đọc) | Read models over stored state, and the ports they read through | `query`, `coreapi`, `box`, `timeline/scene`, `timeline/read`, `diagnostics`, `dispatch`, `harness`, `runtime` |
| 4 integration (tích hợp) | Adapters to something outside mate: harness CLIs, Herdr, the terminal host, git, `gh`, Jev, OS notifications | `harness/catalog`, `harness/claude`, `harness/codex`, `harness/grok`, `harness/pi`, `screen`, `screen/fixture`, `screen/jev`, `screen/chain`, `send`, `notice`, `host`, `panerun`, `gitx`, `github`, `facts`, `quota`, `teardown`, `notifications`, `matehost` |
| 5 service (dịch vụ) | Application logic that combines the layers below, and the core that runs it | `spawn`, `outbox`, `autopilot`, `watch`, `timeline`, `recovery`, `refresh`, `prwatch`, `migrate`, `mateassets`, `hook`, `wake`, `recall`, `dashboard`, `core` |
| 6 UI (giao diện) | What the captain looks at; Office and City are web pages in `internal/dashboard/ui/`, served by `dashboard` (service) | `ui/console` |
| 7 binary | Wiring and CLI | `cmd/mate` |

Package paths are under `internal/` except `assets` and `cmd/mate`. Some
packages below storage still read or write a file under `.mate/` (section
3, Owner column); the layer follows the imports, not the file.

### Import rules

1. A package imports only packages of its own layer or a lower one. The rule
   reads non-test files, all build constraints included.
2. Placing a package (a guide for the table, not checked by itself): a port
   (an interface several layers use) sits at or below the lowest layer that
   imports it; its concrete adapters sit in integration. Today: `process` in
   leaf (it imports nothing of mate's); `runtime` (the `Adapter`/`ReadAdapter` port) in read, because
   `query` reads liveness through `runtime.ReadAdapter`; `harness` (the
   contract and Registry) in read, because `runtime` and `dispatch` import
   it; `screen` (the `Observer` port) in integration, because its lowest
   user is `send`.
3. Outside the rules: `_test.go` files, the test-only packages
   `brief/brieftest`, `harness/harnesstest` and `harness/codex/codexlab`
   (no non-test file imports them), and `scripts/`. `scripts/ptysmoke` is a
   module of its own (CON-183): `go list ./...` at the root does not reach
   it, and `make vet` vets it separately.
4. The boundary tests of section 4 add stricter local rules on top.

`internal/archtest/layers_test.go` checks rule 1 in `make check`: it fails
on a package with no layer, on an import that breaks the rule, and on a
line of `internal/archtest/violations.txt` that no longer happens. That
file is the list of today's violations, one import pair per line, each
either an exception (it stays; the design reason is on the line) or debt
(it must go, and `debtCeiling` in the test only goes down).

Packages that mix layers, until their issue splits them:

- `runtime` is the port and also the Herdr adapter (`herdr*.go`). **Target**:
  the adapter moves to `internal/runtime/herdr`, integration (CON-190).
- `matehost` is the headless Mate host, the Mate's REPL (`repl*.go`, UI
  code) and the owner of `news.jsonl` and `work.jsonl`. **Target**: the REPL
  becomes a client of the host (CON-216, Backlog).

`dashboard` is in service, not UI: it is the Office/City HTTP server the
engine runs (`internal/core/dashboard.go`), so `core` imports it, and it
reads `store`, `db`, `timeline` and `matehost` itself; its SQL is in
`timeline/read` (CON-166). The UI is the web
pages it serves. Since CON-165 it no longer imports `spawn`
(`store.Workspace.CrewIsScout`).

## 2. Processes and how they talk

| Process | Started by | Code | Runs |
| -- | -- | -- | -- |
| Console | `mate`, `mate <dir>`, `mate console` | `runConsole`, `cmd/mate/console.go` | One per workspace (flock on `.mate/console.lock`, CON-162). Runs the engine, which serves Office/City and sends notifications |
| Pane runner | the console's host columns | `mate pane serve`, `internal/panerun` | One per sibling pane: the stage, and command tabs (a file review is a command tab); ends when the console's pid is gone |
| Herdr server | `runtime.Herdr`, when the session it needs is not running | `herdr server --session <s>` (external) | Every agent pane, Mate and Crew. Outlives the console |
| Mate, pane mode | `spawn` | harness TUI in a Herdr pane | Claude Code calls `mate hook mate-prompt` / `mate-stop` (`internal/hook`) |
| Mate, headless | `spawn` (`internal/spawn/headless.go`) | `mate mate host <project>`, `internal/matehost` | One per headless Mate, in its Herdr pane: a REPL; each turn is one `claude -p` |
| Crew | `mate crew spawn` (`internal/spawn`) | harness TUI in a Herdr pane, in `.worktrees/<project>-<crew>` | Appends status lines to its `.status` file; stops in `wait-mate` to hand back |
| PR watcher | `mate pr watch` (`cmd/mate/pr.go`); `prwatch.Sweep` from recovery and the auto daemon | `internal/prwatch`, detached by `prwatch.ExecStarter` | One per crew pull request; on merge or close, queues a line in the outbox and delivers it itself |
| Read-only dashboard | `mate dashboard` | `cmdDashboard`, `cmd/mate/dashboard.go` | Office/City over `db.OpenRead`; the port is busy while a console serves it |
| CLI commands | the Mate, a Crew or the captain | `commands()` table, `cmd/mate/dispatch.go` | Short-lived: `mate send`, `mate wake`, `mate news`, `mate merge`... Read and write `.mate/` directly, not through the console |

Inside the console, `core.Engine` (`internal/core/engine.go`, built by
`engineOptions` in `cmd/mate/console_auto.go`) starts, in order: the re-read
of `workspace.yaml` every 2 s (`internal/core/reload.go`); the observer
(`internal/watch`, a poll every 5 s) which records the timeline at the end of
each poll (`internal/timeline`); the outbox sender (`internal/outbox`, every
2 s); the auto daemon (`internal/autopilot`, every 90 s); recovery
(`internal/recovery`); the Office/City server (`internal/core/dashboard.go`);
the notification loop (`internal/core/notify.go`). `Stop` stops them in
reverse.

`Engine.Snapshot` (`internal/core/snapshot.go`) is the one `query.Snapshot`
every UI reads: `query.LoadLive` plus what the loops know, kept for up to
2 s, computed only when someone reads. `Engine.FreshSnapshot` skips the
cache (the button check of `POST /api/actions`); `Engine.Invalidate` drops
it after an action. The console TUI (`internal/ui/console`) reads it every
2 s through `coreapi.LoadFunc` (`consoleLoad` adds the console's own keys)
and acts through `coreapi.ActionFunc`: `Engine.Act` (`internal/core/act.go`).

`Engine.Act` is the one entry for an action from any UI: the console's
keys (`tuiAction`, `cmd/mate/console_actions.go`) and every
`POST /api/actions` of Office and City. It runs the chat (`say`,
`interrupt`, `answer`, `fork`, `fork_close`) itself on the headless Mate's
host, and every other action through `Options.Act`, cmd/mate's
`engineActions` (**Target**: CON-215 plans moving them in). It holds one
lock per target while an action runs: the Project, `project/crew` when the
request names a crew, the workspace for adding a Project; the chat,
`peek`, `explain_notice`, `okay_work`, `open_news` and `open_work` take
`<target>#<action>` instead, so they run beside a stop or a merge. A
second action on a held target, from any UI, is refused with
`coreapi.ErrBusy` ("Action refused: … is still running on … · nothing
started"; a page gets it as a 409). Each console still runs one action at
a time of its own. After every action `Act` calls `Invalidate`.

How processes exchange state:

- **Files under `.mate/`.** The main channel. mate's own log appends
  (`.status`, `sent.log`, `incidents.log`; a Crew's `echo` takes no lock)
  and the read-modify-writes of the outbox,
  news, work, backlog and notifications logs take an advisory `flock`
  through `internal/platform` (`platform.Lock`, `platform.TryLock`); every
  such lock fails where flock is missing (CON-177). `.meta` files and
  `workspace.yaml` are replaced whole (temp file, then rename) with no
  cross-process lock: `store.Workspace.UpdateCrewMeta` re-reads and
  rewrites, so two processes updating one meta can lose an update.
  Section 3 lists the files.
- **SQLite, one writer.** `.mate/mate.db` is derived from the files and can
  be rebuilt (`mate reindex`). `db.Open` takes `.mate/mate.db.lock`; a second
  writer gets `db.ErrLocked` (a console then observes without recording).
  Readers use `db.OpenRead`.
- **Unix sockets.** `.mate/projects/<p>/mate/host.sock`: one JSON request
  and one JSON response per connection (`matehost.Client`), except
  `subscribe`, which keeps the connection and streams every host event as
  one JSON line (CON-221); the outbox delivers to a headless Mate through
  it (`OpDeliver`), and spawn, Office chat and City ask it for state,
  history and answers. The console drives
  each pane runner over its own socket in a temp directory
  (`panerun.SocketDir`: `stage.sock`, `pane.sock`), not under `.mate/`.
- **Herdr.** `runtime.Herdr` runs the `herdr` executable (`process.Runner`)
  against the server's socket (`<config>/herdr/sessions/<s>/herdr.sock`):
  start, read, send keys, attach. `send.Send` types a line into a pane only
  after the fixture observer reads an empty composer; a send never asks Jev
  (`internal/screen/chain/chain.go`). The observer and the startup settle
  ask Jev only about a screen the fixture cannot read, when it is
  configured.
- **HTTP.** Office and City call the engine's server (`internal/dashboard`,
  default `127.0.0.1:7777`, LAN only with `allow_remote`).

**Target** (CON-203): one `mate core` process per workspace runs the engine
with no UI open (CON-210 bounded stop;
CON-211 `mate core run` and `core.lock`; CON-213 `start|stop|restart|status`).
UIs reach it over `.mate/core.sock` (CON-212) through `internal/coreclient`
(CON-214). The console becomes a client. CLI commands keep writing files
directly. Until then, closing the console stops the engine, and with it
Office/City and notifications.

## 3. State files in `.mate/`

Paths are relative to `.mate/`; `<p>` is a project, `<c>` a crew. Path
helpers live in `internal/store/layout.go`. `internal/store` says it is the
only package that touches `.mate/`; the Owner column shows where that is not
true yet.

| File | Owner | Written by | Read by |
| -- | -- | -- | -- |
| `workspace.yaml` | `store` | `mate init`, `mate project …` | every process (`store.Open`); the engine re-reads it every 2 s |
| `.env`, `WORKSPACE.md`, `CREW.md` | `store` | the captain | `store.LoadEnv`; brief and manual rendering |
| `console.yaml` | `consolecmd` | the captain | the console (`loadConsoleCommands`) |
| `plugins.yaml` | `plugin` | `mate plugin` | `spawn` at Mate start, console, dashboard |
| `crew-dispatch.json` | `dispatch` | the captain | `mate crew dispatch` |
| `console.lock` | `store` | the console (flock) | a second console, which asks to replace it |
| `mate.db`, `mate.db.lock` | `db` | the timeline ingest in the engine; `mate reindex` | the engine's snapshot (tokens), dashboard, `mate events`, `mate usage`, `mate state`, `mate recall`, refresh; every query outside `db` and `timeline` is in `timeline/read` |
| `notifications.json` | `notifications` | the engine's notification loop (`internal/core/notify.go`) | the same loop (flock on `notifications.json.lock`) |
| `projects/<p>/project.yaml`, `PROJECT.md` | `store` | `mate project …`; the Mate (`PROJECT.md`) | spawn, query, `mate recall` |
| `projects/<p>/sent.log` | `store` | outbox, `mate send`, hook, refresh, autopilot, the console | box, timeline |
| `projects/<p>/incidents.log` | `store` | watch, outbox, timeline, `mate crew budget` | box |
| `projects/<p>/crews/<c>.meta` | `store` | spawn, `mate crew …`, prwatch, autopilot | query, watch, timeline, recovery |
| `projects/<p>/crews/<c>.status` | `store` | the Crew (`echo >> $MATE_STATUS`), prwatch, merge | box, watch, timeline, `mate wake` |
| `projects/<p>/crews/<c>/brief.md`, `report.md`, `handback.md` | `store` | spawn (brief); the Crew | the Mate, spawn, the console stage, dashboard |
| `projects/<p>/crews/<c>.prwatch` | `prwatch` | `prwatch.Ensure` (the watcher's pid, in whoever starts it); the watcher removes it | `prwatch.Ensure` and `prwatch.Sweep` |
| `projects/<p>/mate/mate.meta` | `store` | spawn, hook (session id) | query, watch, timeline, refresh |
| `projects/<p>/mate/.manual`, `.auto` | `store` | the console's `m` key (`store.Workspace.SetMode`); autopilot releases `.manual` | autopilot, timeline; Auto means no `.manual` |
| `projects/<p>/mate/.outbox`, `.outbox.lock` | `store` | `assign` from the console, autopilot (digests), stow, prwatch | outbox sender, query |
| `projects/<p>/mate/.auto-cursor`, `.wake-cursor` | `store` | outbox (on a digest), `mate wake` (`internal/wake`) | autopilot, `mate wake` |
| `projects/<p>/mate/memory.md`, `backlog.md` | `memory` format, `store` I/O | the Mate (`mate remember`, `mate backlog`) | `mate recall` |
| `projects/<p>/mate/news.jsonl` | `matehost` | `mate news`; the friction check of `mate wake`, `land` and `merge`; the host (answers) | query, dashboard, REPL, notifications |
| `projects/<p>/mate/work.jsonl` | `matehost` | `mate work`, `mate land`, the console (the captain's tick) | query, dashboard, REPL |
| `projects/<p>/mate/host.sock`, `host.jsonl`, `host.launch.json`, `topics/` | `matehost` | the headless host; spawn (`host.launch.json`) | outbox, spawn, `mate mate …`, Office chat, City, notifications |
| `projects/<p>/mate/learnings/<c>.md` | `learnings` | the Mate; the app (`## Usage (app)`, predictions) | `mate wake`, `mate learnings` |

Crews work in `.worktrees/<project>-<crew>`, git worktrees that `spawn`
creates and removes; `teardown` first stops what the worktree started.

## 4. Boundary tests

Each test fails `make check` when its rule breaks. The first four are the
ones CON-179 names; the last three came with CON-184, CON-206 and CON-207.
They add local rules to the repo-wide layer rule, which
`internal/archtest/layers_test.go` checks (section 1).

| Test | Rule |
| -- | -- |
| `internal/harness/ratchet_test.go` | Outside `internal/harness/...`, no non-test file names a harness: no import of a harness package, no harness-named identifier of package `harness`, no string literal that carries, as a whole token, a harness name or a name its profile declares (config directory, instruction file, env keys: `.claude`, `CLAUDE.md`, `CODEX_HOME`). Ceiling 0; `ratchetAllow` lists three exceptions, each with a reason, and an entry that matches nothing fails |
| `internal/screen/ratchet_test.go` | Every `Observe` call in `send`, `spawn` and `outbox` is listed in `observeSites` with the deterministic check (string compare or highlight confirmation) that must follow it in the same function, or, for a helper, in each caller the entry names; a Jev reading alone never triggers an irreversible key |
| `internal/ui/console/boundary_test.go` | `internal/ui/console` imports none of `store`, `runtime`, `harness`, `process`, `box`, `send`, `spawn`, `watch`, `autopilot`, `outbox`, `host`, `core`, `coreclient`, `dashboard` (or their subpackages), nor any path containing `herdr`; test files included. It sees `query` types, the `coreapi` contract and closures from `cmd/mate` |
| `internal/ui/console/tool_keys_test.go` | Three lists are equal: `ownedKeys`, the keys the tree's handlers (`onKey`, `onListKey`, `onDetailKey`, `onBoxKey`) compare against in their source, and `consolecmd.ReservedKeys`; a key the console does not own reaches a configured tool from every pane |
| `internal/platform/ratchet_test.go` | Outside `internal/platform`, no `syscall.Flock`/`LOCK_*`/`O_NOFOLLOW`/`Kill`/`Getpgid`, `Setsid`/`Setpgid`, `runtime.GOOS`, import of `x/sys/unix` or `creack/pty`, or `"osascript"`, except the files in `platformAdapters` |
| `internal/coreapi/boundary_test.go` | `internal/coreapi` imports the standard library and `internal/query` only |
| `internal/core/boundary_test.go` | Nothing `internal/core` links, transitively, is under `cmd/` or `internal/ui/` |

## 5. Where to make a change

**A harness.** Add `internal/harness/<name>` implementing `harness.Profile`
(`internal/harness/contract.go`: `Launcher`, `ScreenProfile`, and the optional
interfaces such as `MCPConfigurer` and `GracefulStopper`). Register it in
`catalog.Default` (`internal/harness/catalog/catalog.go`), the one list of
harnesses; in `cmd/` and `internal/`, only `cmd/mate` imports `catalog`. The contract suite in
`internal/harness/catalog/contract_test.go` then runs over it, and the
harness ratchet keeps its name out of every other package. Live tests use
`internal/harness/harnesstest`. Design: `docs/plans/harness-registry-2026-09-30.md`.

**A plugin.** No Go change: a directory with `mate-plugin.yaml`, installed in
`.mate/plugins.yaml` (`docs/plugins.md`). To change what a plugin can declare:
`internal/plugin` (`manifest.go`, `config.go`); it reaches the Mate at start
through `spawn` (`internal/spawn/plugins.go`), the console through
`cmd/mate/console_plugins.go`, Office through `internal/dashboard/plugins.go`,
and the CLI through `cmd/mate/plugin.go`.

**A terminal host.** Implement `host.Host` (`internal/host/target.go`) in a
new file beside `wezterm.go` and `ghostty.go`; add its `Kind` and detection
to `DetectFor` (`internal/host/detect.go`) and its case to `Open`
(`internal/host/open.go`). Code bound to one OS or one tool goes in
`platformAdapters` of `internal/platform/ratchet_test.go`, with a reason.
Panes it creates run `mate pane serve` (`cmd/mate/pane.go`).

**A CLI command.** Add one row to `commands()` in `cmd/mate/dispatch.go`
(names, one-line summary, handler, and `internal` for a command only mate
itself runs); dispatch and the usage text both come from that table, and
`cmd/mate/dispatch_test.go` checks every row is reachable and listed. Put the
handler `cmdX` in `cmd/mate/<x>.go`, with only flag parsing and output there,
and the logic in an `internal/` package, as `internal/wake` does for
`mate wake` and `internal/recall` for `mate recall` (CON-164). A command the
Mate or a Crew uses must also be taught in its manual or skill under
`assets/mate/` or the brief template `assets/crew/brief.md.tmpl`. A console
key that runs a tool is not a CLI command: `.mate/console.yaml`,
`internal/consolecmd`, `docs/console-commands.md`.
