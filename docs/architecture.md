# Architecture

What mate is made of once the fourth wave of the core split
(`refactor/con-203-wave-4`, 42c6571, 2026-11-05) is on `main`. Domain terms
(Mate, Crew, outbox, hand-back...) are defined in
[GLOSSARY.md](../GLOSSARY.md). `docs/mvp.md` is the history of how this came
to be; this file is the current map. Installing and building:
[install.md](install.md).

mate is splitting its core into a process of its own (CON-203). Done so far:
`internal/coreapi` (CON-206); the engine in `internal/core` (CON-207) with the
shared snapshot, Office/City and notifications (CON-208), the one entry
for every UI's actions (CON-209), a stop within one fixed bound
(CON-210), `mate core run` with `core.lock` (CON-211), and its clients'
API on `.mate/core.sock` with `internal/coreclient` (CON-212); the Mate's
and the Project's actions (CON-231); `mate core start|stop|restart|status`
and plain `mate` running the core (CON-213); `internal/refresh`
(CON-170); `internal/platform` (CON-184, CON-185); `CrewIsScout` in `store`
(CON-165); the CLI command table (CON-171); `internal/wake` (CON-169);
`internal/usage` (CON-168); `internal/recall` (CON-167). Every
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
| 4 integration (tích hợp) | Adapters to something outside mate: harness CLIs, Herdr, the terminal host, git, `gh`, Jev, OS notifications | `harness/catalog`, `harness/claude`, `harness/codex`, `harness/grok`, `harness/pi`, `screen`, `screen/fixture`, `screen/jev`, `screen/chain`, `send`, `notice`, `host`, `panerun`, `gitx`, `github`, `facts`, `quota`, `teardown`, `notifications`, `matehost`, `coreclient` |
| 5 service (dịch vụ) | Application logic that combines the layers below, and the core that runs it | `spawn`, `outbox`, `autopilot`, `watch`, `timeline`, `recovery`, `refresh`, `prwatch`, `migrate`, `mateassets`, `hook`, `wake`, `recall`, `dashboard`, `usage`, `core` |
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
  code) and the owner of `news.jsonl` and `work.jsonl`. The REPL is a
  client of the host's socket (CON-223): it reaches the host only through
  `matehost.Conn` (`REPLConn` over `host.sock`), never a `*Host`. The host
  runs outside Herdr and tells Herdr nothing (CON-225): `matehost` has no
  Herdr code.

`dashboard` is in service, not UI: it is the Office/City HTTP server the
engine runs (`internal/core/dashboard.go`), so `core` imports it, and it
reads `store`, `db`, `timeline` and `matehost` itself; its SQL is in
`timeline/read` (CON-166). The UI is the web
pages it serves. Since CON-165 it no longer imports `spawn`
(`store.Workspace.CrewIsScout`).

`usage` (CON-168) builds the pages `mate usage` prints; `cmd/mate/usage.go`
only reads the flags. It also writes the `## Usage (app)` section of a
crew's learning record when the crew closes (`usage.RecordAtClose`,
CON-230), in the words of `--why`'s head. It is in service because `--why`
reads through `dashboard.Server`, so that page and the Task page cannot
disagree, and the ledger reads the crews through `spawn`. Its reads of
`mate.db` are in `timeline/read`; it holds no SQL. Because `usage` imports
`dashboard`, `dashboard` cannot import `usage`: the Office's usage page
reusing the ledger needs `--why` split out of `usage` first.

## 2. Processes and how they talk

| Process | Started by | Code | Runs |
| -- | -- | -- | -- |
| Console | `mate console` (plain `mate` and `mate <dir>` run the core since 2026-11-06) | `runConsole`, `cmd/mate/console.go` | One per workspace (flock on `.mate/console.lock`, CON-162). Runs the engine, which serves Office/City and sends notifications; refuses to open while `mate core` runs (CON-211) |
| Core | `mate`, `mate <dir>`, `mate core run` (foreground); `mate core start` (background, CON-213) | `cmdCoreHere`, `cmdCoreRun`, `runCore`, `cmd/mate/core.go`; start/stop/status in `cmd/mate/core_control.go` | The engine with no console: the same parts as the console's, plus `.mate/core.sock` and the clients' routes on it (CON-212). Stops within the engine's bound on SIGINT, SIGTERM or SIGHUP. `start` runs `mate core run` in a session of its own with its output in `.mate/core.log`; `stop` sends SIGTERM, then SIGKILL after 60 s, only to the pid `core.lock`'s holder wrote ([core.md](core.md)) |
| Pane runner | the console's host columns | `mate pane serve`, `internal/panerun` | One per sibling pane: the stage, and command tabs (a file review is a command tab); ends when the console's pid is gone. The stage runs `herdr agent attach --takeover` for a crew or a pane Mate, and the Mate REPL for a headless Mate (CON-224) |
| Herdr server | `runtime.Herdr`, when the session it needs is not running | `herdr server --session <s>` (external) | Every agent pane: a pane Mate and every Crew. Outlives the console |
| Mate, pane mode | `spawn` | harness TUI in a Herdr pane | Claude Code calls `mate hook mate-prompt` / `mate-stop` (`internal/hook`). Codex and pi Mates, and `mate.mode: pane` |
| Mate, headless | `spawn` (`startHost`, `internal/spawn/headless.go`; `ExecHosts`, `hostproc.go`) | `mate mate host <project> --no-repl`, `internal/matehost` (`Host.Live`) | One per headless Mate, a background process of its own outside Herdr (CON-225): its own session (`platform.StartNewSession`), no terminal, no tab or pane, stdout and stderr in `host.log`. Each turn is one `claude -p`. `mate.meta` records its `pid` and `socket`; whether it is alive is what `host.sock` answers (`spawn.HostLiveness`; a host that does not answer but still holds its host lock is alive and wedged). Runs until the `stop` op, SIGTERM or SIGHUP; a stop the op does not end sends SIGTERM, then SIGKILL, to the recorded pid, only while some process holds the host lock and no host answers under another pid. The escalation runs to its end even when the stop was cut short. Nothing starts it again when it dies (**Target**: CON-226, mate core watches it); recovery at the next engine start does. Run by hand in a terminal it runs the REPL as a child process, `mate mate repl <project> --in-pane`. A host an older binary started in its Herdr pane keeps that pane, and Herdr answers for it, until its next restart |
| Mate REPL | the console's stage; the captain, in any terminal | `mate mate repl <project>`, `matehost.RunREPL` | A client of `host.sock` (CON-223): `subscribe` for events, `history` on each connection, one op per action; connects again on its own when the host restarts. Any number per Mate; they share the chat and the draft |
| Crew | `mate crew spawn` (`internal/spawn`) | harness TUI in a Herdr pane, in `.worktrees/<project>-<crew>` | Appends status lines to its `.status` file; stops in `wait-mate` to hand back |
| PR watcher | `mate pr watch` (`cmd/mate/pr.go`); `prwatch.Sweep` from recovery and the auto daemon | `internal/prwatch`, detached by `prwatch.ExecStarter` | One per crew pull request; on merge or close, queues a line in the outbox and delivers it itself |
| Read-only dashboard | `mate dashboard` | `cmdDashboard`, `cmd/mate/dashboard.go` | Office/City over `db.OpenRead`; the port is busy while a console or a core serves it |
| CLI commands | the Mate, a Crew or the captain | `commands()` table, `cmd/mate/dispatch.go` | Short-lived: `mate send`, `mate wake`, `mate news`, `mate merge`... Read and write `.mate/` directly, not through the console |

In the console or in `mate core run`, `core.Engine`
(`internal/core/engine.go`, built by `engineOptions` in
`cmd/mate/console_auto.go`) first takes `.mate/core.lock` (CON-211): one
engine per workspace, wherever it runs; a second one gets
`core.RunningError` naming the holder's pid and owner (`core` or
`console`) and starts nothing. The console and `mate core run` take it
(`core.Lock`) before `engineOptions` builds the parts, whose observer opens
`mate.db`, and hand it in as `Options.Claim`. A core then binds `.mate/core.sock` (0600;
a socket left by a dead core is removed, a path too long or a filesystem
with no unix sockets stops it). It then starts, in order: the re-read
of `workspace.yaml` every 2 s (`internal/core/reload.go`); the observer
(`internal/watch`, a poll every 5 s) which records the timeline at the end of
each poll (`internal/timeline`); the outbox sender (`internal/outbox`, every
2 s); the auto daemon (`internal/autopilot`, every 90 s); recovery
(`internal/recovery`); the Office/City server (`internal/core/dashboard.go`);
the notification loop (`internal/core/notify.go`). `Stop` returns within
`core.StopWait` (45 s) plus the closing of `mate.db` (CON-210): it refuses
every new action (`coreapi.ErrStopping`, "mate core is stopping · nothing
started"; a page gets it as a 409), cancels every part, the snapshot being
computed and every action in flight (the pages' too) at once, then waits for
them all together under that one bound; the server's own shutdown takes at
most 5 s of it. It closes `mate.db` only once everything has stopped; past
the bound it names what still runs on stderr and leaves `mate.db` to the
process exit. `core.lock` is released last, or, past the bound, also left to
the process exit. A console quit in the middle of an action waits for it inside
that same bound (`quitAbandoned`, `cmd/mate/console.go`).

`Options.ConsolePresent` says whether a console is open: always true for
the console's engine; for `mate core run`, true while a request saying
`X-Mate-Client: console` (`coreapi.ClientHeader`) reached its socket in the
last 10 s (`core.ConsoleSeen`, CON-212). Only the outbox loop reads it
(`outbox.Deps.ConsolePresent`, wired by `engineOptions`): with no console it types nothing into a Manual
project's Mate (a digest at the head is still withdrawn), while an Auto
project gets its lines, digests and refreshes as before; `Attempt` called
by itself (`assign`, `mate pr watch`) is not gated. The auto daemon does
not read it: `Sweep`, the budgets and M21's rearm run either way (CON-203,
decided 2026-11-05).

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
host. It also runs the Mate's and the Project's actions itself
(`ownAction`, `internal/core/act_mate.go` and `act_project.go`, CON-231):
`start`, `resume`, `onboard`, `stop` of a Mate, `repair`, `mode`,
`restart_mate`, `clear_composer`, `remove_project`, `open_news`,
`open_work` and `okay_work`, over `Options.Workspace` and `Options.Spawn`.
A restart held for the captain's second press (`restartHolds`) is the
engine's, in memory only: any UI confirms it, and only a new engine
forgets it. Every other action, a crew's, the box's and Jev's, runs
through `Options.Act`, cmd/mate's `engineActions` (**Target**: CON-232
moves them in). It holds one
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
  it (`OpDeliver`), spawn, Office chat and City ask it for state,
  history and answers, and every Mate REPL is a client of it (CON-223).
  Its `ping` is the headless Mate's liveness (CON-225): the snapshot
  (`spawn.HostLiveness` into `query.Liveness.Hosts`), recovery, `mate
  project remove`, `mate migrate` and `mate mate status` ask it, never
  Herdr.
  What the captain sends through it from any UI (`submit`, `answer`,
  `work-okay`) the host holds `matehost.UndoDelay` before it runs, and
  `undo` takes it back (`internal/matehost/hold.go`, CON-222); the state
  and every event list what it holds. The console drives
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
  default `127.0.0.1:7777`, LAN only with `allow_remote`). `mate core run`
  serves the same routes on `.mate/core.sock` too (`dashboard.ListenUnix`);
  a busy TCP port is said (and recorded as `tcp_error` in `core.lock`) and
  the core runs on the socket alone. The socket alone also serves the
  clients' routes (CON-212, `internal/dashboard/client.go`; over TCP each
  is a 404): `GET /api/core` (`coreapi.CoreStatus`: `core.lock`'s record,
  Jev as the pages' `Deps.Notice` says it, and the notices a console's
  status line shows), `GET /api/snapshot`
  (`Engine.Snapshot` in Go's default JSON, without `Commands`) and `POST
  /api/act` (a `coreapi.ActionRequest` run by `Engine.Act` under
  `context.WithoutCancel`, so it finishes when the client goes away; no
  re-check against a fresh snapshot, as the console's keys have none). A
  refusal is `{"error", "code"}` (`coreapi.ErrorBody`: 409 `busy`, 409
  `stopping`, 422 `no_mate_repl`, 422 with no code for an action that
  failed, 503 for a snapshot that could not be read). `internal/coreclient`
  is the client: `Dial`, then `Core`, `Snapshot` (a `coreapi.LoadFunc`) and
  `Act` (a `coreapi.ActionFunc`), whose errors are coreapi's again
  (`errors.Is` finds `ErrBusy`, `ErrNoMateREPL`, `ErrStopping`). The
  socket is 0600 and `/api/act` takes the client's own text (a resolve's
  `Input`), which `POST /api/actions` never takes from a page; that is why
  it is not on TCP.

Names (2026-11-05, CON-216): **mate core** is the process that runs the
engine; **headless** is only how a Mate runs, one `claude -p` per message
in its host. Neither word stands for the other.

**Target** (CON-203): the `mate core` process runs the engine whenever the
workspace is in use (`mate`, or `mate core start|stop|restart|status`,
CON-213). UIs on this
machine reach it over `.mate/core.sock` through `internal/coreclient`
(built in CON-212); the console becomes such a client in CON-214. CLI
commands keep writing files directly. Until then, a console runs its own engine and closing it stops the engine,
and with it Office/City and notifications; `mate core run` is the engine
without a console, and the two refuse to run side by side.

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
| `console.lock` | `store` | the console (flock; `core_lock: true` since CON-211) | a second console, which asks to replace it; `mate core run`, which refuses beside a console that has no `core_lock` |
| `core.lock` | `store` | the engine, in `mate core` or a console (flock; JSON `pid`, `owner`, `started`, `version`, `binary`, `build`, `socket`, `url`, `lan_url`, `tcp_error`) | a second engine, which is refused; `mate reindex`, to name who holds `mate.db` |
| `core.sock` | `store` path, `core` serves | `mate core run` (unix socket, 0600) | `internal/coreclient` (`/api/core`, `/api/snapshot`, `/api/act`, CON-212); the Office/City routes over HTTP |
| `core.log`, `core.log.1` | `store` path | the output of a core `mate core start` ran; `start` moves a log over 10 MB to `core.log.1` | the captain |
| `mate.db`, `mate.db.lock` | `db` | the timeline ingest in the engine; `mate reindex` | the engine's snapshot (tokens), dashboard, `mate events`, `mate usage`, `mate state`, `mate recall`, refresh; every query outside `db` and `timeline` is in `timeline/read` |
| `notifications.json` | `notifications` | the engine's notification loop (`internal/core/notify.go`) | the same loop (flock on `notifications.json.lock`) |
| `projects/<p>/project.yaml`, `PROJECT.md` | `store` | `mate project …`; the Mate (`PROJECT.md`) | spawn, query, `mate recall` |
| `projects/<p>/sent.log` | `store` | outbox, `mate send`, hook, refresh, autopilot, the console | box, timeline |
| `projects/<p>/incidents.log` | `store` | watch, outbox, timeline, `mate crew budget` | box |
| `projects/<p>/crews/<c>.meta` | `store` | spawn, `mate crew …`, prwatch, autopilot | query, watch, timeline, recovery |
| `projects/<p>/crews/<c>.status` | `store` | the Crew (`echo >> $MATE_STATUS`), prwatch, merge | box, watch, timeline, `mate wake` |
| `projects/<p>/crews/<c>/brief.md`, `report.md`, `handback.md` | `store` | spawn (brief); the Crew | the Mate, spawn, the console stage, dashboard |
| `projects/<p>/crews/<c>.prwatch` | `prwatch` | `prwatch.Ensure` (the watcher's pid, in whoever starts it); the watcher removes it | `prwatch.Ensure` and `prwatch.Sweep` |
| `projects/<p>/mate/mate.meta` | `store` | spawn, hook (session id) | query, watch, timeline, refresh. A headless Mate's records its host's `pid` and `socket` and no pane (`store.MateHostDetached`, CON-225) |
| `projects/<p>/mate/.manual`, `.auto` | `store` | the console's `m` key (`store.Workspace.SetMode`); autopilot releases `.manual` | autopilot, timeline; Auto means no `.manual` |
| `projects/<p>/mate/.outbox`, `.outbox.lock` | `store` | `assign` from the console, autopilot (digests), stow, prwatch | outbox sender, query |
| `projects/<p>/mate/.auto-cursor`, `.wake-cursor` | `store` | outbox (on a digest), `mate wake` (`internal/wake`) | autopilot, `mate wake` |
| `projects/<p>/mate/memory.md`, `backlog.md` | `memory` format, `store` I/O | the Mate (`mate remember`, `mate backlog`) | `mate recall` |
| `projects/<p>/mate/news.jsonl` | `matehost` | `mate news`; the friction check of `mate wake`, `land` and `merge`; the host (answers) | query, dashboard, REPL, notifications |
| `projects/<p>/mate/work.jsonl` | `matehost` | `mate work`, `mate land`, the console (the captain's tick) | query, dashboard, REPL |
| `projects/<p>/mate/host.sock`, `host.jsonl`, `host.launch.json`, `topics/` | `matehost` | the headless host; spawn (`host.launch.json`) | outbox, spawn, `mate mate …`, Office chat, City, notifications, the snapshot's liveness |
| `projects/<p>/mate/host.log`, `host.log.1` | `matehost` (path), `spawn` (rotation) | the headless host's stdout and stderr (CON-225); a start moves a log of `spawn.HostLogMax` (1 MiB) or more to `host.log.1` | the captain; a failed start quotes its last lines |
| `projects/<p>/mate/learnings/<c>.md` | `learnings` | the Mate; the app (`## Usage (app)` through `usage`, predictions) | `mate wake`, `mate learnings` |

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
