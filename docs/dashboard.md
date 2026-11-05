# Dashboard

This is the contract of `mate dashboard` and of `internal/dashboard` (docs/mvp.md M6).

The dashboard reads, and the one served by `mate console` can also act.
It opens `.mate/mate.db` with `db.OpenRead`, which takes no lock, so it runs beside an open console rather than instead of it.
It writes nothing itself. Its one write, `POST /api/actions` (section 7b, CON-141), hands a button press to the console's own action function, so a page and the console's action sheet run one code path; `mate dashboard` on its own has no console behind it and stays read-only.

```
mate dashboard [<workspace>] [--addr 127.0.0.1:7777] [--open] [--allow-remote]
```

`--addr` must be a loopback address unless `--allow-remote` is passed.
A workspace's timeline quotes every line the captain typed and names every path a crew touched, so putting it on a routable address has to be a decision rather than a default.
`--open` runs the platform opener (`open`, `xdg-open`, `rundll32`) on the URL; a machine with none of them says so and keeps serving.
The server prints its URL and runs until Ctrl+C.

`mate console` serves the same dashboard for as long as it is open, on `127.0.0.1:7777` unless `.mate/console.yaml` says otherwise.
Its `dashboard.addr` is the bind address and `dashboard.allow_remote: true` is what `--allow-remote` is for `mate dashboard`; with a non-loopback `addr` and no `allow_remote` the console says `dashboard not started` and serves nothing.
When the bind is reachable from the LAN the status line shows the LAN URL beside the loopback one (docs/console-commands.md, "Dashboard on the LAN").
The setting is read at console start.

## 1. Conventions

Every response carries two fields before anything else.

```json
{"generated_at": "2026-09-19T10:47:00.000000000Z", "last_event_id": 412}
```

`last_event_id` is `MAX(event.id)` over the whole workspace: the cursor for newly recorded events.
It is the number to hand back to `/api/events?since=`, so a page always polls from the exact id the data it is showing was computed at.

`generated_at` is when the snapshot behind the bytes was computed, not when they were served.
Workspace, project, history, turn, diff and `/api/now` responses are cached per `last_event_id`: while the id stands still they return the bytes already built, and when it moves every cached answer is dropped at once.
Crew task and Mate conversation responses are rebuilt on each request: running execution clocks and recording-heartbeat age can change without a new event. Usage is never extrapolated between harness records.
The events endpoint is uncached, and so are plugins, board, news, work, actions, chat and forks: each reads files, the console or a window that moves at midnight, none of which is an event.

A timestamp is the database's own string: RFC3339 with nanoseconds, in UTC (`db.TimeFormat`).
It is passed through and never reformatted, so a value on the page and a row in the database compare byte for byte.

A nullable number is `null`, never `0`.
A cost with no `pricing` row and a context percentage with no known context window are unknown, and docs/timeline.md's "a missing price is not a price of zero" is the same rule `mate usage` prints as `?`.

Token buckets are always this object.

```json
{"input": 32, "cache_read": 57690, "cache_write": 739, "output": 911, "thinking": 73, "total": 59372}
```

`total` is `input + cache_read + cache_write + output` and excludes `thinking`, which is exactly what `mate usage`'s TOTAL column and `v_now.tokens_today` sum.

A `ref` is a transcript locator - the file a fact was read out of and the byte offset inside it - and it is what makes every number on the page traceable back to the harness's own record.

```json
{"path": "/Users/x/.codex/sessions/2026/09/19/rollout-01a0b944.jsonl", "offset": 18422}
```

Errors are JSON with the same envelope, an `error` sentence and, when there is more to say, a `reason`.
An unknown project or crew is `404` and the error names what was missing and what there is instead.

```json
{"generated_at": "…", "last_event_id": 412, "error": "no project \"nosuch\" in this workspace; it has shop, site"}
```

There is no `Access-Control-Allow-Origin` header of any kind.
The only page that reads this API is the one this server serves.

## 2. `GET /api/workspace`

Tier 1: one card per registered project.

`mate.harness` and `mate.running` come from the `actor` row (`harness`, `first_seen`, `gone_at`), which `v_now` has no column for; running means started and not stopped, where stopped is the current `mate.meta`'s `stopped_at`: a relaunch writes a meta without one and clears the `gone_at` of the life before it.
`mate.state`, `mate.since` and `mate.tokens_today` are `v_now`.
`mate.context_tokens` is the actor's latest turn's `context_tokens_after` and `mate.context_pct` that against the model's `pricing.context_window`; for a Mate whose `mate.meta` names a `session_id`, both are the latest of that session, and `null` until it has one.
`crews_by_state` counts this project's crew actors by their `v_now` scene state, and a crew no projection has placed counts under `"unknown"` rather than being dropped.
`inbox_waiting` is the length of `box.Inbox` through `query.LoadBox`, the same loader and the same number the console's rail header shows.
`mode` is `Workspace.Auto`: `"auto"`, or `"manual"` while the project's `mate/.manual` exists.
`repos` names the project's repos in `project.yaml` order (`RepoNames`), and is empty when it has none or its `project.yaml` cannot be read; Agent City draws one tent per repo (section 12, CON-125).

```json
{
  "generated_at": "2026-09-19T10:47:00.000000000Z",
  "last_event_id": 412,
  "root": "/Users/x/work",
  "projects": [
    {
      "name": "shop",
      "mode": "manual",
      "mate": {
        "harness": "claude",
        "running": true,
        "state": "idle",
        "since": "2026-09-19T10:46:40.000000000Z",
        "tokens_today": 57683,
        "context_pct": 29.2,
        "context_tokens": 58400
      },
      "crews_by_state": {"waiting_review": 1},
      "inbox_waiting": 1,
      "repos": ["shop"]
    }
  ]
}
```

A project whose box could not be read still gets a card, with `"error"` saying why: one broken project must not blank the workspace.

## 3. `GET /api/projects/{project}`

Tier 2: the Mate above, the task table below, the inbox beside it.

`mate` is the card's block plus what the Mate has spent and what it last did.
There is no `task` row for a Mate - its turns belong to the project and not to any one task (docs/timeline.md) - so `turns`, `tokens` and `cost` are computed from `turn` directly, with the same all-zeroes-is-not-a-price guard `v_task_ledger` uses.
This is the same SQL `cmd/mate/usage.go`'s `mateLedgerRow` runs, and the unit tests compare the two number for number.
`last_turn` is `null` for a Mate that has taken no model call yet, and otherwise the full model-call object of section 4.
The project page links to `GET /api/projects/{project}/mate`. Its `exchanges`
are prompt-level interactions: one captain message, crew digest, or app prompt
and the Mate's response. Calls are grouped by `(session_id,
harness_turn_ref)`, so a prompt spanning several model calls appears once.
An inbound message with no call still appears with `model_calls: 0` and an
empty response. The prompt and response come from `sent.log` through the
`message` table; a hook echo is used only when no other message carries the prompt. Selected
events and messages to Crew appear as human-readable activities. `turns` in
the Mate summary remains the count of model calls used for token accounting.
The UI shows newest exchanges first and reveals older ones in batches of 20.

`tasks` is every crew the project has ever recorded, open and closed, oldest spawn first: one row of `v_task_ledger` each, with the crew's current `v_now` state, target and detail hung on it, plus two things no view carries.
`tool_count` is `SUM(turn.tool_count)` for the crew, which the tier-3 ledger asks for and `v_task_ledger` has no column for.
`age_ms` is `closed_at - spawned_at` for a closed task and `now - spawned_at` for an open one.
`harness` is the crew's `actor.harness`, the same source the Mate card reads, so a page can name the harness of a recording whose telemetry carries no version.
`closed` is true when the task has a `close_state` or a `closed_at`.
`review` is set only for a reviewer crew, one `mate review` started and so left `review.meta` beside its report: the first line of the report's `## Verdict` section (`pass`, `changes-requested`, `incomplete`), or `pending` while there is none. A reviewer that delivered its Verdict is closed `reviewed`, otherwise `finished` or `failed` like any crew; `review` is what it concluded.
`failed_reason` is crew meta's `failed_reason=`, written when a spawn never came up; a crew closed with `crew stop --discard` is `failed` with none.
`reviews` names the reviewers `mate review` started for the task (crew meta `reviews=`, or a reviewer's `review.meta` naming the crew); `review_of` is the task a reviewer row reviewed; `tokens_with_reviews` is `tokens.total` plus every reviewer's total, so a task's cost with its reviews is one number (CON-75). The Office's cabinet peek reads it.

`repo` is the repo the crew worked in: crew meta `repo=`, or the project's sole repo, resolved through `project.yaml` as `mate crew spawn` does (`CrewRepo`); absent when that does not resolve, for a crew whose repo has left `project.yaml` or a project whose `project.yaml` cannot be read (CON-125).

`role` is what the task was for: `review` for a reviewer crew (it has `review.meta`), `scout` for a scout (crew meta `kind=scout`, or a brief with `## Deliverable` for a crew spawned before `kind` was recorded), else `ship`.

`model` and `effort` are the launch settings crew meta recorded (`model=`, `effort=`), which is what was asked for; `last_model` is the model the recording observed. Each is absent when unknown.

`agents` is the kinds of agent the crew delegated to, first use first and each once: the `agent` of its `tool.called` events (docs/timeline.md), which is Claude's `subagent_type` - a project's `.claude/agents/<name>.md` such as `backend-reviewer`, or a built-in such as `Explore`, as written. Finding them reads every `tool.called` payload, so the server keeps them in memory, reads only the events past the highest id it has seen, and starts over when that id goes backwards (a `mate reindex`) (CON-126). Absent for a crew that delegated to none, or only to agents of no named kind. Events ingested before the field existed carry no `agent`; `mate reindex` rebuilds them from the transcripts (CON-124).

`inbox` is `box.Inbox` flattened through `query.LoadBox`, oldest first, the same rows in the same order the console's rail draws.
`assign_key` is set on an entry the Mate can be handed: it is what section 7b's `assign` names the entry by.
A failed box read leaves `inbox` empty and puts the reason in `inbox_error` rather than failing the page.

```json
{
  "generated_at": "2026-09-19T10:47:00.000000000Z",
  "last_event_id": 412,
  "project": "shop",
  "mode": "manual",
  "mate": {
    "harness": "claude",
    "running": true,
    "state": "idle",
    "since": "2026-09-19T10:46:40.000000000Z",
    "target": "",
    "detail": "",
    "tokens_today": 57683,
    "context_pct": 29.2,
    "context_tokens": 58400,
    "turns": 6,
    "tokens": {"input": 212, "cache_read": 55112, "cache_write": 1840, "output": 519, "thinking": 84, "total": 57683},
    "cost": null,
    "last_turn": {"…": "a turn object, section 4"}
  },
  "tasks": [
    {
      "crew": "buybtn",
      "text": "Add a Buy button to README.md linking to the checkout page",
      "branch": "mate/buybtn",
      "state": "waiting_review",
      "since": "2026-09-19T10:46:12.000000000Z",
      "target": "mate",
      "detail": "",
      "close_state": "",
      "closed": false,
      "spawned_at": "2026-09-19T10:44:09.000000000Z",
      "closed_at": "",
      "merged_at": "",
      "age_ms": 171000,
      "turns": 9,
      "tokens": {"input": 23400, "cache_read": 32100, "cache_write": 1120, "output": 1154, "thinking": 351, "total": 57774},
      "cost": null,
      "last_model": "gpt-5-codex",
      "context_tokens_last": 41220,
      "context_pct": null,
      "question_count": 1,
      "handback_count": 1,
      "waited_ms": 86721,
      "tool_count": 14,
      "harness": "codex"
    }
  ],
  "inbox": [
    {
      "seq": 3,
      "at": "2026-09-19T10:45:12.000000000Z",
      "kind": "status",
      "source": "crew",
      "target": "",
      "crew": "buybtn",
      "verb": "needs-decision",
      "text": "what is the checkout page URL for the Buy button?",
      "attention": true
    }
  ]
}
```

## 3b. `GET /api/plugins?project=<p>`

The apps a project's Mate receives from `.mate/plugins.yaml` (docs/archive/plans/plugins-and-office-2026-10-12.md), as validated local metadata, for Mate Office's Apps block (section 11).
It reads `plugins.yaml` and the workspace packages' manifests through `internal/plugin` on every request and is not cached: an edit to the file is not an event.
It writes nothing, calls no MCP server or tracker, and runs no command; opening a link is the browser's job, never the server's.
`project` is required; a missing or unknown one is a 404 that names it.

```json
{
  "generated_at": "2026-09-19T10:47:00.000000000Z",
  "last_event_id": 412,
  "project": "shop",
  "apps": [
    {"instance": "linear-work", "name": "Linear", "status": "configured", "harness": "claude",
     "open": {"title": "Open Linear", "url": "https://linear.app/acme/team/SHOP"},
     "mcp": {"name": "linear-work", "type": "http"}, "skills": ["workflow"], "error": null}
  ]
}
```

One row per enabled workspace instance (plus legacy bindings for this project),
in `plugins.yaml` order. The project parameter selects the Mate/harness status;
it does not limit a workspace installation to that project.
`name` is the manifest's; `open.url` is the package's link with this project's config substituted, and it is the only URL in the answer - the MCP endpoint, a stdio command and binding config stay out.
`mcp` is `null` for a package without a server, `open` for one without a link, and `skills` lists skill names, `[]` when none.

`status` is one of three words, and each is about configuration only:

| `status` | Meaning |
| --- | --- |
| `configured` | the configuration is valid and the Mate's harness can take it |
| `configuration error` | `error` says what is wrong, with the file, instance and project it is in |
| `harness unsupported` | valid, but the app has an MCP server and the Mate's harness cannot be given one; `error` says why |

Whether the Mate is logged in to an app, or its tools answer, is unknown here: there is no auth, connected or ready field, and a valid file is not evidence of either.
The place to check is `/mcp` in the Mate's Claude pane.

`harness` is the running Mate's (`actor.harness`, section 9); for a Mate that is not running it is `workspace.yaml`'s `defaults.mate_harness`, which a new start uses, and `unknown` when that is empty.
Whether a harness can be given MCP servers is the harness profile's answer, not the dashboard's: the server asks `Deps.PluginHarness(kind)`, which `mate dashboard` and the console's dashboard wire to the profile's `MCPConfig` capability (`harness.Capabilities`), so `internal/dashboard` names no harness (the harness ratchet, `internal/harness/ratchet_test.go`).
A harness whose `MCPConfig` is not verified answers with the capability's `Reason`, which is the app's `error`; a kind the binary does not have answers that it is unknown.
The same capability decides the launch: a Mate launch carrying MCP servers on a harness without it is refused with `ErrMCPUnsupported`, so the dashboard and `mate mate start` cannot disagree.
An `unknown` harness, or a server started without that seam, calls no app `harness unsupported`.
Only an app with an MCP server can be `harness unsupported`; a skills-only or open-only app stays `configured`.
While any app is unsupported the Mate will not start, so a `configured` app beside it carries an `error` saying so.

A broken `plugins.yaml` is an answer, not a 500.
A problem in the file as a whole (YAML that does not parse, a wrong `apiVersion`) is `200` with `"apps": []` and the problem in a top-level `error`.
A problem located at one instance is also in the top-level `error`, and every row of the project says `configuration error`: the one at fault carries the problem, the others that the Mate will not start with this file, because a Mate start refuses the whole file (`internal/spawn`).
Those rows are read from the file without validating it, so their `name` is the instance id and they carry no `open`, `mcp` or skills.
A valid file has no top-level `error`.

## 3c. `GET /api/board?days=<n>`

The Office's daily board (CON-84): one column per calendar day, oldest first, for the workspace and for each project.
`days` defaults to 14 and is capped at 90; the board offers 7, 14 and 30.
A day is the server clock's zone, named in `zone`, not UTC: `v_now.tokens_today` and the top bar's `Today` are a UTC day, so the two disagree in the hours the zones do.
The response is not cached, because the window moves at midnight whether or not an event does.

Each day carries:

- `tokens`: the token buckets of every `turn` that started that day (`started_at`, else `ended_at`), Mate and crews alike; `mate_tokens` and `crew_tokens` split `tokens.total` by the actor's kind. The board counts in tokens only and has no cost.
- `spawned`: tasks spawned that day by `role` (`ship`, `scout`, `review`, as in section 3).
- `closed`: ships and scouts closed that day by final state: `finished`, `discarded` (a `failed` task with no `failed_reason`), `didnt_start` (one with), or any other `close_state` as it is. There is no `merged`: the timeline sees only the merges mate made itself, and a PR merged on GitHub is not one of them, so a merged count would read as work not landing.
- `reviews`: reviewers closed that day by verdict: `pass`, `changes-requested`, `incomplete`, or `no_verdict` for one closed without a Verdict. The Office calls `changes-requested` rework.
- `incidents`: incidents opened that day by `kind` (`stale`, `runtime_lost`, `wedged`, `budget`, `no_turn`, `context_high`). `budget` is a crew or the project crossing its token budget, or a crew reaching half of it without a hand-back. A harness running out of quota is not recorded on the timeline yet (CON-85), so the board cannot count it.

`total` is every project summed. A project whose tasks could not be read carries `error`, and its tokens and incidents are still counted.

```json
{
  "generated_at": "…", "last_event_id": 412, "days": 14, "zone": "+07:00",
  "dates": ["2026-10-03", "…", "2026-10-16"],
  "total": [{"date": "2026-10-16", "tokens": {"input": 0, "cache_read": 0, "cache_write": 0, "output": 0, "thinking": 0, "total": 0},
             "mate_tokens": 0, "crew_tokens": 0, "spawned": {"ship": 5, "review": 7}, "closed": {"finished": 6, "discarded": 1},
             "reviews": {"pass": 5, "changes-requested": 2}, "incidents": {"budget": 7, "runtime_lost": 2}}],
  "projects": [{"name": "shop", "days": ["…one per date…"]}]
}
```

`GET /api/board/day?date=<YYYY-MM-DD>` opens one of those days (CON-88), in the same zone, for every project; the page filters by project.
`date` is required, and one that is not a day is a 400. It is not cached either.

- `actors`: each Mate and crew that took turns that day, with `project`, `kind` (`mate` or `crew`), `name` (empty for a Mate), `turns` and `tokens`, biggest first.
- `tasks`: every task opened, closed or taking turns that day: a section 3 task object plus `project`, `opened`, `closed_today`, `day_turns` and `day_tokens`; its own `tokens` stay the task's whole life. Most tokens that day first.
- `incidents`: incidents opened that day, oldest first: `project`, `kind`, `actor_kind`, `name` (empty for the Mate), `opened_at`, and `resolved_at` once resolved.
- `errors`: by project, why its tasks could not be read; absent when every project was read.

## 3d. `GET /api/news?project=<p>`

Every news a headless Mate opened for the captain (`mate news open`, `mate/news.jsonl`, CON-78), closed ones included, for Mate Office's captain drawer (section 11, CON-97) and the City's Topics (section 12).
Without `project` it answers for every project in workspace order; an unknown `project` is a 404 that names it.
It folds the log through `matehost.LoadNews` on every request and is not cached: the REPL marking a news seen or the captain answering it writes the log, not an event.
It writes nothing: reading a news here does not mark it seen.

```json
{
  "generated_at": "…", "last_event_id": 412,
  "projects": [
    {"project": "shop", "logged": true, "news": [
      {"id": 3, "crew": "buybtn", "kind": "decision", "title": "Checkout URL", "context": ["buybtn asked twice"],
       "question": "Which page?", "options": [{"key": "a", "text": "express", "recommended": true}],
       "status": "working", "opened_at": "…", "at": "…", "seen_at": "…", "answer": "[a] express", "answered_at": "…",
       "notes": [{"at": "…", "text": "CI is green"}]}
    ]},
    {"project": "infra", "logged": false, "news": []}
  ]
}
```

`news` is newest move first (`at`: opened, updated, answered, closed; being seen is not a move).
`status` is the log's: `new` (the captain has not opened it), `waiting` (opened, not answered), `working` (answered, the Mate is on it), `done`, `dropped`.
`seen_at` is when a `new` news was last opened and `answered_at` when the last answer was given; both are absent when that never happened, or happened before the log recorded it.
`archived` is true once the news was taken out of the list; a new question on an open news clears it. The City hides an archived news unless it waits on the captain.
`logged` is false when the Mate has no news log, which is not the same answer as a log with nothing in it; a log that cannot be read is `logged: true` with `error` and no news.
Answering a news from a page is `POST /api/actions` `answer` (section 7b).

## 3e. `GET /api/work?project=<p>`

The Mate's work board (`mate work`, `mate/work.jsonl`, CON-158): what the captain asked it for and what it proposed, where each row stands, okayed rows included, for the Work tab of Mate Office's Mate drawer and of the City's Mate panel.
It answers "how is the project doing?" and is never a question: a row that waits on the captain names the news (`topic`) it waits on, and nothing on it counts into Needs you.
Without `project` it answers for every project in workspace order; an unknown `project` is a 404 that names it.
It folds the log through `matehost.LoadWork` on every request and is not cached: the Mate writing the board is not an event.

```json
{
  "generated_at": "…", "last_event_id": 412,
  "projects": [
    {"project": "shop", "logged": true, "work": [
      {"id": 3, "kind": "assigned", "title": "Gộp make shot vào pre-push hook", "status": "done", "last": "PR #232 merged",
       "added_at": "…", "at": "…", "done_at": "…",
       "notes": [{"at": "…", "op": "add", "text": "anh giao trong chat"}, {"at": "…", "op": "done", "text": "PR #232 merged"}]},
      {"id": 4, "kind": "proposed", "title": "Dọn DerivedData nguội", "status": "waiting", "topic": 15, "last": "hỏi anh có làm không", "added_at": "…", "at": "…", "notes": […]}
    ]}
  ]
}
```

`work` is latest move first. `status` is the log's: `working`, `waiting` (on news `topic`), `done` (the captain may tick it), `okayed` (ticked, off the board; `okayed_at`). `kind` is `assigned` (the captain asked) or `proposed` (the Mate suggested).
The captain's tick is the one write: `POST /api/actions` with `{"action": "okay", "project": "shop", "work": 3}`, offered in `mate_more` wherever the dashboard can act; it refuses a row that is not `done`. The Mate never okays its own rows, and does not wait for the tick.

## 4. `GET /api/projects/{project}/tasks/{crew}`

Tier 3 leads with the current activity, up to three evidence-linked findings,
prompt-level timelines and the segments using the most tokens. Model calls,
status lines, questions and the branch remain available as supporting evidence.

`ledger` is the same task object as tier 2's row.

`?view=summary` answers with `ledger`, `status_lines` and `questions` only - no `turns`, `branch`, `performance` or `journey`, which are most of the full answer's time and bytes (CON-126).

`turns` is one object per model call, oldest first.
`trigger_kind` is the `kind` of the event named by `turn.trigger_event_id`, joined on because a page showing a bare id would be showing the reader a number they cannot read.
`duration_ms` is `ended_at - started_at`, and `0` for a turn that has not ended.
`context_pct` is `100 * context_tokens_after / pricing.context_window` for that turn's model, `null` when the model has no priced window.
`ref` is the turn's own `ref_path`/`ref_offset`.

`status_lines` is the crew's `status.appended` events, with `verb`, `text` and `line` read out of the payload docs/timeline.md section 4 documents.

`questions` is the `question` table for this crew, decisions only (`kind` `decision`, a `needs-decision` line); a hand-back wait the Mate or the captain answered is a `handback` row, which feeds the decision-wait findings but is not listed here.
The answer's text is not on that table - it records which event answered, not what the answer said - so it is read from the answering event: `message.text` when a `message` row exists for it, otherwise that event's own payload, whose `text` is the answer verbatim.
`answered_by` is the answering actor's name.
`waited_ms` is `null` while a question is still waiting, which is not the same as having waited zero.

`branch` is the task's recorded branch and whether git still has it.
A branch that is gone is not an error: the crew was torn down and its work landed or was discarded, and `reason` says so.

```json
{
  "generated_at": "2026-09-19T10:47:00.000000000Z",
  "last_event_id": 412,
  "project": "shop",
  "crew": "buybtn",
  "ledger": {"…": "a task object, section 3"},
  "turns": [
    {
      "id": "crew:shop:buybtn#t7",
      "ordinal": 6,
      "started_at": "2026-09-19T10:45:58.000000000Z",
      "ended_at": "2026-09-19T10:46:04.000000000Z",
      "duration_ms": 6000,
      "trigger_event_id": 288,
      "trigger_kind": "message.sent",
      "outcome": "tool_use",
      "model": "gpt-5-codex",
      "tokens": {"input": 2140, "cache_read": 9120, "cache_write": 0, "output": 142, "thinking": 33, "total": 11402},
      "context_tokens_after": 41220,
      "context_pct": null,
      "tool_count": 3,
      "ref": {"path": "/Users/x/.codex/sessions/…/rollout-01a0b944.jsonl", "offset": 18422}
    }
  ],
  "status_lines": [
    {
      "event_id": 231,
      "at": "2026-09-19T10:44:31.000000000Z",
      "turn_id": "crew:shop:buybtn#t2",
      "verb": "working",
      "text": "verifying isolated worktree and task brief",
      "line": "working: verifying isolated worktree and task brief",
      "ref": {"path": "/Users/x/.codex/sessions/…/rollout-01a0b944.jsonl", "offset": 9004}
    }
  ],
  "questions": [
    {
      "id": "crew:shop:buybtn#q1",
      "asked_event_id": 252,
      "asked_at": "2026-09-19T10:45:12.000000000Z",
      "text": "what is the checkout page URL for the Buy button?",
      "answered_event_id": 288,
      "answered_at": "2026-09-19T10:46:00.000000000Z",
      "answered_by": "mate",
      "answer": "Use pages/checkout-express.html for the Buy button.",
      "waited_ms": 48000
    }
  ],
  "branch": {"name": "mate/buybtn", "exists": true}
}
```

### The journey field

`journey` (journey.go, docs/design/task-page.md) is what the task page's Journey tab reads beyond the fields above. Each part is one file or one query, and `missing` names in words what could not be read, so the page says "not recorded" rather than drawing zero.

| Field | Meaning |
| --- | --- |
| `brief` | `crews/<crew>/brief.md`, with its `path` and `bytes`: `captain_words` (the `## Captain's words` section) and `task` (the `# Task` body up to the next level-one heading); the role and rules boilerplate is not returned. `null` when there is no brief. |
| `handback` | `crews/<crew>/handback.md` verbatim in `markdown`, with its `path` and `bytes` (`kind` `handback`), or for a scout without one its `report.md` (`kind` `report`), which is what a scout hands back. Plus `closes` (the tracker lines at the top), `criteria[]{name, result, pass, command, output}` from the table whose header starts with `criterion`, `deviations[]`, `still_open[]`, and `learning` (the text after `Learning:`). `null` when the crew has not handed back. |
| `meta` | The crew's `.meta`, the keys the page reads: `pr_url`, `pr_state`, `pr_sync`, `merge_commit`, `lane`, `size`, `teardown`, `cleanup`, `launched_at`, `started_at`, `stopped_at`, `state`, `worktree`, `repo`. |
| `reviews[]` | `{crew, verdict}` for each reviewer `mate review` started for this task; the verdict is the reviewer's report's, `pending` while it has none. |
| `mate_turns[]` | The Mate's exchanges (section 3's shape) that touched this crew: one whose activities name it (spawned, answered, merged, closed) or whose prompt opens with the crew's name (its digests). Each is the whole exchange: what else the Mate did in it is not separated out, and `activities` says what it did. They are computed once per `last_event_id` and shared across task pages. |
| `incidents[]` | This crew's lines of `incidents.log`: `at`, `kind`, `state` (`open` or `resolved`), `text`. |
| `workflow[]` | project.yaml's `workflow` steps checked against the recording: `{step, state, evidence, at}` with `state` `done`, `missed`, `n/a` (the step is asked of another role, its `files` matched nothing the crew changed, or its `since` is after the spawn) or `unknown` (a bad pattern, or `files` could not be checked). A step's `roles` (`ship`, `scout`, `review`) say which crews it is asked of; without `roles` it is asked of ship crews only, so a scout is never marked missed for a commit or a gate. `null` when the project declares none. A step is done when its `command` matches a native, non-poll command the crew ran, its `read` matches a path it read (a file tool's target or a command naming the path), its `skill` matches a skill it loaded, or its `handback` matches a line of the hand-back. |
| `changed_files[]` | The files the crew's work touched: its branch against its base while the branch exists, else its merge commit's. `null` when neither could be read. |
| `missing[]` | What this answer could not read: `brief`, `hand-back`, `meta`, `incidents`, `project.yaml`, `changed files`, `Mate's turns: <why>`. |

### Crew performance projection

The task response also carries `performance` (`version: crew-observability-v1`).
The observer records `telemetry.*` facts in the derived event store; this read-only
projection uses their native prompt/execution identities and the existing ledger
for billed token buckets. It never adds native response usage to ledger usage.

| Field | Meaning |
| --- | --- |
| `tokens`, `model_calls` | Exactly the Crew ledger's bucket totals and model-call count. |
| `prompt_turns` | Native prompt ID scoped to its session, actual prompt and source locator, prompt timing, bucket totals, call count and `segment_ids`. A missing prompt ID leaves that call isolated. |
| `segments` | Contiguous activity in call order, with `kind` from the work vocabulary (`research`, `instructions`, `write_code`, `edit_code`, `review`, `test`, `coordination`, `wait`, `response`, `mixed`, `unknown`), concrete `label`/`target`, `call_ids`, `execution_ids`, buckets, nullable elapsed/tool-union/invocation time, repeat count and versioned classification rule. Each ledger call is assigned once. A running execution before usage arrives can have zero attributed calls. |
| `current_segment_id`, `top_segment_ids` | Current observed work when identifiable, and up to five segments ranked by total tokens. A completed run has no current segment. |
| `top_output_segment_ids`, `top_output_call_ids` | Up to five segments and up to five ledger calls ranked by output + thinking, ties by id. Total tokens mostly rank the longest context; these rank what the model itself wrote. |
| `findings`, `top_finding_ids` | Versioned polling, repeated-read, repeated-error/repair, failed-command, long-execution, output/context and decision-wait observations. The first screen shows at most three: warnings when there are any, else info, and never a large output the next call's context shows the model did not take in (it grew by under a tenth of the output's size as text, as with an image). Findings include explanatory text, confidence, count, source evidence, related calls/segments/executions and a review suggestion. Their call sets can overlap; do not add their tokens together. |
| `executions` | Native commands and observed tools, with exact command/cwd, native process/wrapper links when known, start/end, nullable duration/exit/output measurements, output hash/preview, polling and wrapper flags, and source locator. Wrapper success never overrides a nested native failure. |
| `processes` | One process per session identity, actual job command when known, execution/poll IDs, unchanged/progress/unknown-output poll counts and tokens of the associated polling calls. Polls are not additional builds/tests. |
| `time` | Prompt elapsed, union of tool spans, sum of invocation durations, union of decision waits, and time not explained by measured tool/wait spans. Lanes overlap. None is called API latency or total thinking time. Missing measurements are `null`. Legacy synthesized `thinking` actions are excluded. |
| `recent_tokens`, `recent_window_ms` | Actual usage reported in the last five minutes, never an interpolated burn rate. |
| `freshness` | Separate last observed/ingested/usage stamps, age, stale flag, adapter capabilities and explicit missing-data reasons. "Child-agent usage is unavailable" is a reason only when the crew started a child agent (Task, Agent, spawn_agent). `telemetry_cursor.observed_at` supplies the observer heartbeat even when no transcript bytes changed. |
| `profile` | Recorded launch snapshot, if available: repo revision/dirty state, requested model/effort, harness and configured document hashes/sizes. Historical runs without a snapshot remain unknown. |
| `runtime`, `observed_inputs`, `progress` | Actually reported model/effort/harness version, actually observed instruction-input hashes/bytes, and file-change evidence. Configured files and observed inputs are distinct; bytes are not exact billed token attribution. |
| `overview` | The Crew-level work overview: the same categories as `prompt_turns[].overview` summed over every prompt by the same classifier. Category `tokens` add up to `tokens` and `model_calls` to `model_calls`; `sequence` is empty (the per-prompt sequences carry order) and `coverage` ends with "across N prompts". |
| `loops` | How often the Crew repeated itself: `chains` (polling findings), `polls`/`unchanged_polls`/`progress_polls`/`unknown_polls`/`processes_polled` from `processes`, `poll_calls`/`poll_tokens` from `wait` segments (`null` when there is none), `repeated_reads`/`repeated_errors`/`repair_loops` finding counts and `segment_repeats` (Σ `repeat_count`). `measured` is true only with native execution status; otherwise every process number is 0 and `coverage` says polling chains are not measurable while repeated reads and retries still are. |

Finding/process `tokens` is `null` if no model-call link is confirmed. A native
execution with no confirmed wrapper parent remains visible in its prompt's
execution lane; nearby timestamps do not establish token attribution. Repeated
reads require identical query/range and output hash, and reset after a recorded
file change or changed output. Retries with intervening file changes are shown
as repair sequences; missing file state is never treated as unchanged code.
Detector thresholds live in `diagnostics.Options` and are exercised by numeric
and false-positive fixtures.

### Work overview for every prompt

Both `performance.prompt_turns[].overview` for Crew and
`exchanges[].overview` for Mate use the same projection. The collapsed prompt
shows a short summary and work-type labels; opening it shows the observed
sequence, usage by type, execution time and source evidence.

The types include research/inspection, writing files, editing files, review,
tests/builds, coordination, waiting/polling and reading instructions.
Classification uses actual tool operations, literal commands and native file
changes. A request to run tests does not establish that tests ran. An edit is
not automatically called a bug fix. Unclassified work remains explicit. One
command line that builds or tests (xcodebuild, `go test`, a script named
`build.sh` or `run-tests.sh`, a wrapper such as `sim-run.sh … xcodebuild`) is
a test/build even when it wrote, patched or read files first: the build is
its minutes. A build beside a review, a git write or a poll stays mixed, and
so do the separate tools one wrapper invokes.

`overview.categories` contain `kind`, `label`, `model_calls`,
`execution_count`, nullable `tokens`/`elapsed_ms`, related call/segment/execution
IDs and self-contained command/source `evidence`. Each model call belongs to
exactly one category. A call covering several kinds belongs to `mixed`; its
usage is not divided among tools. The other categories can still show those
tools as evidence with unattributed usage. Category token buckets add up to
the prompt ledger. Execution intervals can overlap across categories.

`overview.steps` are runs of consecutive calls of one kind; besides tokens
and calls each carries `tool_elapsed_ms`, the union of its commands' spans,
so the steps of a kind say where the header's "waiting on the machine" went.

`overview.sequence` retains successive observed types, including returns such
as test → edit → test. One operation containing several types becomes one
mixed step; it does not invent an order among parallel tools. `rule` identifies
the classifier and `coverage` explains the available evidence.

Mate grouping uses confirmed native response aliases when a legacy call has
no prompt ID. Otherwise that call stays isolated. Native prompts and their
work remain visible before the first usage record; message-only prompts show
an empty overview until activity arrives. The overview adds no model calls.

## 4b. `GET /api/projects/{project}/history?since=<RFC3339>`

Every crew's `status_lines` and decision `questions`, as section 4 carries them, from `since` on, by crew name: `{ "project", "since", "crews": { "<crew>": { "status_lines": [...], "questions": [...] } } }`. A crew with neither since then is absent. Two queries for the whole project, where section 4 takes two a crew, so Agent City's tent reads a week of a busy project in one request (section 12, CON-127), and Mate Office's inbox reads its last 24 hours (section 11).

`since` is required; a missing or unparsable one is a 400. The answer's `since` is the database's own string for it. The answer is cached per `last_event_id` like section 2's, keyed by the query, so a reader that keeps `since` fixed for the day is answered from the cache.

## 5. `GET /api/projects/{project}/tasks/{crew}/turns/{turn}`

One turn opened: the turn itself, its tool calls, and the story rows recorded inside it.

`actions` is the `action` rows of that turn, oldest first.
`action` carries no locator of its own - the schema puts `ref_path`/`ref_offset` on `event` - so each action's `ref` comes from the `tool.called` event it was written beside, through `action.event_id`.
`duration_ms` and `ok` are `null` for a call whose result the transcript did not carry.
`actions` can be longer than the turn's `tool_count`: the count is the calls the harness made, while `action` also holds the synthesised `thinking` rows the ingest writes for a busy stretch no call explains (docs/timeline.md section 4).
A UI that draws them should say which is which by the tool name.

`events` is `v_story` filtered to `turn_id`, in exactly the shape and field order `mate events` prints as JSON lines: `id`, `at`, `project`, `kind`, `actor`, `actor_kind`, `subject`, `task`, `turn`, `cause`, `cause_kind`, `payload`, `ref`, `ref_offset`.
It is produced by `timeline.Story`, the same function the CLI calls, so the two cannot drift.

A turn id belonging to another crew is a `404`: a link built from one page must not render under another page's heading.

Turn ids contain `#` (`crew:shop:buybtn#<session>#turn#20`), so the path segment must be percent-encoded - `encodeURIComponent` in the UI.
An unescaped `#` makes the browser send only the part before it and treat the rest as a fragment, which arrives here as a turn id that matches nothing.

```json
{
  "generated_at": "2026-09-19T10:47:00.000000000Z",
  "last_event_id": 412,
  "project": "shop",
  "crew": "buybtn",
  "turn": {"…": "a turn object, section 4"},
  "actions": [
    {
      "id": "crew:shop:buybtn#a21",
      "at": "2026-09-19T10:46:01.000000000Z",
      "ended_at": "2026-09-19T10:46:01.131000000Z",
      "tool": "exec",
      "target": "git add README.md && git commit -m \"docs: add Buy link\"",
      "summary": "",
      "duration_ms": 131,
      "ok": true,
      "event_id": 301,
      "ref": {"path": "/Users/x/.codex/sessions/…/rollout-01a0b944.jsonl", "offset": 19110}
    }
  ],
  "events": [
    {
      "id": 301,
      "at": "2026-09-19T10:46:01.000000000Z",
      "project": "shop",
      "kind": "tool.called",
      "actor": "buybtn",
      "actor_kind": "crew",
      "task": "buybtn",
      "turn": "crew:shop:buybtn#t7",
      "payload": {"class": "shell", "target": "git add README.md && …", "tool": "exec"},
      "ref": "/Users/x/.codex/sessions/…/rollout-01a0b944.jsonl",
      "ref_offset": 19110
    }
  ]
}
```

## 6. `GET /api/projects/{project}/tasks/{crew}/diff`

The bottom of the task page: `mate diff <project> <crew>`, verbatim.

The CLI's own `crewDiffText` produces it, so the page and the terminal can never disagree about what a branch contains.
A branch git no longer has returns `200` with an empty `text` and a `reason`, not an error, for the same reason the CLI says it plainly: a crew whose branch was deleted is a crew whose work landed or was discarded.
A diff that fails for any other reason answers the same way - the rest of the task page is still true, and a `500` here would take it down with the branch.

```json
{
  "generated_at": "2026-09-19T10:47:00.000000000Z",
  "last_event_id": 412,
  "project": "shop",
  "crew": "buybtn",
  "branch": "mate/buybtn",
  "exists": true,
  "text": "0d2d20d docs: add Buy link\n\ndiff --git a/README.md b/README.md\n…"
}
```

## 7. `GET /api/events?since=<id>&wait=<seconds>&project=<p>`

The long poll that keeps a page current without a refresh button.

It returns as soon as an event with `id > since` exists.
Otherwise it waits up to `wait` seconds, polling the database once a second, and then returns an empty list - an empty answer is the honest "still nothing", and the client polls again with the same cursor.
`wait` is clamped to 25 seconds, which is under every default proxy and browser idle timeout a local page can meet.
`since` defaults to 0, which returns the whole story.
`project` is optional; without it the story of every project is returned, merged and sorted by `(at, id)`.

`events` are `v_story` rows through `timeline.Story`, the same shape and field order `mate events` prints.

`now` are the `v_now` rows of the actors that moved, so one call refreshes both the story and the scene.
`v_now` has no "changed since" of its own, so which actors moved is read off `transition` - the table the view's scene columns already come from - as the distinct actors with a transition whose `event_id > since`.

`timeline_stale`, present only when there is something to say, lists the projects whose ingest has failed every pass since `since` (wall clock), with the latest `error` on one line (CON-139). Everything served for such a project is behind its files from then on. Only the console's dashboard knows: it is the timeline's writer. `mate dashboard` on its own never sends the field.

This endpoint is not cached: it answers "what happened after the id you hold", which depends on the caller's cursor. Section 1 names the endpoints that are.

```json
{
  "generated_at": "2026-09-19T10:47:00.000000000Z",
  "last_event_id": 412,
  "project": "shop",
  "since": 400,
  "events": [{"id": 401, "at": "…", "project": "shop", "kind": "turn.ended", "…": "…"}],
  "now": [
    {
      "actor_id": "crew:shop:buybtn",
      "actor": "buybtn",
      "actor_kind": "crew",
      "project": "shop",
      "state": "waiting_review",
      "since": "2026-09-19T10:46:12.000000000Z",
      "target": "mate",
      "detail": "",
      "tokens_today": 57774,
      "context_pct": null,
      "context_tokens": 41220
    }
  ]
}
```

## 7b. Actions (CON-141)

`GET /api/projects/{project}/actions` is the console's action sheet for the project's Mate and for each open crew, in the sheet's order and words.

```json
{"generated_at": "…", "last_event_id": 412, "project": "shop",
 "mate": [{"action": "start", "label": "Start mate", "available": false, "reason": "Mate is recorded running"},
          {"action": "stop", "label": "Stop mate…", "available": true, "confirm": "Stop the Mate of shop? …"}],
 "crews": {"buybtn": [{"action": "merge", "label": "Merge…", "available": true, "confirm": "Merge mate/buybtn into main and finish crew buybtn? …"}]}}
```

Availability is `internal/query`'s (`query.ActionAvailability`), read from the console's own snapshot - the one answer the console's sheet shows too - and `reason` is why a button cannot run.
`confirm` is the question a page asks before a destructive action; a button without one runs at once.
The Mate has start (or `resume`, or `onboard` to create it), `stop`, `restart_mate`, `mode` and `clear_composer`; a crew has `stop`, `restart_crew`, `merge` (only once it reported `wait-mate`) and `diff`, which a page reads from section 6 rather than posting.
The console's `Repair binding…` is not offered: repairing a crew is not wired behind it.
A dashboard with no console behind it answers `read_only` with the reason and no buttons.

`POST /api/actions` with `{"action": "stop", "project": "shop", "crew": "buybtn"}` (`crew` only for a crew's action) presses one.
The press is checked again against a fresh snapshot, then run by the console's `consoleAction`; the answer is `{"line": "…"}`, the console's own one line.

| Status | When |
| --- | --- |
| `200` | it ran; `line` says what happened |
| `400` | an action not offered for that row (`repair`, an `assign` whose `key` is no open question, an `okay` with no `work`, …) or a body that is not one |
| `403` | no console behind this dashboard, or an `Origin` that is not this server's |
| `404` | an unknown project, or a crew that is not open |
| `409` | the action cannot run now (its `reason`), or another action is still running on the same Mate or crew |
| `415` | a body that is not `application/json` |
| `422` | the service ran and refused or failed; `error` is its message verbatim |

There is no authentication (CON-141 leaves that to the LAN): anyone who can open the page can press a button.
The JSON body and the `Origin` check only keep another site open in the same browser from pressing one, since a cross-site form cannot send JSON without a preflight this server never answers.
An action runs to its end even when the page that pressed it goes away.

#### The City's actions (CON-142)

Agent City (section 12) and Mate Office (section 11) press more than the console's sheet, still through the same `POST /api/actions` and the same checks.
`GET /api/projects/{project}/actions` carries them beside the sheet, and `GET /api/actions` answers every project at once from one read of the console's snapshot, `{"read_only"?, "projects": {"<p>": <the per-project answer>}}`, which is what the City polls:

| Field | Buttons |
| --- | --- |
| `mate_more` | `say` (the Mate's chat; it stands for `answer`, `fork`, `fork_close` and `interrupt` too, refused while the Mate is not running or is not headless), `remove_project`, with the console's own question, and `okay` (section 3e) |
| `crew_more` | `peek` and `explain_notice` (a live binding; Jev configured), then one `assign` per open question of the crew, with its `key` (`query.BoxEntry.AssignKey`), refused once the Mate has it or while it waits in the outbox |
| `harnesses` | the harnesses a new Mate can run on, the workspace's default first: `onboard` takes one as `harness` |
| `daemon` | the auto daemon's `sends`, `last_sent_at`, and the `notice` of why the digest queued has not gone, when there is any |

A press may carry `text` (`say`, `answer`, `fork`), `news` (`answer`, `fork`, `fork_close`, and `interrupt` of a session rather than the Mate's turn), `work` (`okay`), `harness` (`onboard`), `repo` (`add_project`) and `key` (`assign`).
`assign` hands the question's resolve line to the Mate, the console's `[assign]`; the line is the server's from its snapshot, never the page's.
`add_project` names a project that is not there yet in `project` and is refused (`409`) for a name `[a-z][a-z0-9-]{0,31}` does not take or one already in the workspace; with an empty `repo` the project starts with none.
The chat's actions go to the headless Mate's own host over its socket (`matehost`'s `submit`, `interrupt`, `answer`, `session-ask`, `session-close`): a message sent from a page is the same captain turn as one typed in the Mate's pane, queued behind the turn in flight, and an answer lands in its news.
Talking, peeking and `okay` take no lock on the Mate or crew, so they neither wait for nor hold up a stop or a merge; the body may be 64 KiB.

`GET /api/projects/{project}/chat` is the headless Mate's conversation with the captain as its REPL shows it (`mate/history.jsonl`, the last 80 items, CON-151): `{"headless", "items": [{"at", "source", "label", "text", "reply", "silent", "interrupted", "error", "done", "tools": [names], "ms", "tokens", "news"}], "state": {"busy", "queued", "turn_source", …} | null, "state_error"?}`. `state` is the host's own answer; `null` with `state_error` when it did not give one, and `null` without one for a Mate that is not headless.
`GET /api/projects/{project}/forks/{news}` is what was said in a news's private session (`mate/topics/<news>.jsonl`), with `open` (`null` when the host did not answer) and the question it is answering now as `running`.

### `GET /api/now?project=<p>`

The whole scene in one call: every `v_now` row, in the shape of `/api/events`' `now`, sorted by `actor_id`.
`/api/events` only carries the rows of actors that moved since a cursor, so a page that draws everyone at once - Mate Office, section 11 - would otherwise have to poll from 0 and replay the story.
`project` is optional; an unknown project is a 404 that names it.
An actor with no scene row yet is simply absent, and a page reads its tokens today as unknown rather than 0.

```json
{
  "generated_at": "2026-09-19T10:47:02.114Z",
  "last_event_id": 1840,
  "now": [
    {"actor_id": "crew:shop:buybtn", "actor": "buybtn", "actor_kind": "crew", "project": "shop", "state": "waiting_review", "since": "2026-09-19T10:46:24.375000000Z", "target": "mate", "detail": "handback", "tokens_today": 0, "context_pct": null, "context_tokens": null}
  ]
}
```

## 8. `GET /` - the UI

`/` serves an embedded `embed.FS` rooted at `internal/dashboard/ui/`: plain HTML and JS, no build step and no CDN, so the dashboard is a single binary that works with no network at all.
Each file carries a content `ETag` with `Cache-Control: no-cache`, so a browser picks up a changed asset after a binary upgrade without a forced reload.
The admin UI is at `/` (section 10), Mate Office at `/office/` (section 11) and Agent City at `/city/` (section 12).

## 9. What is read directly rather than through a view

The views are the contract, and these are the places they do not reach.
Each is named here because a later change to a view should absorb them rather than leave two ways to ask the same question.

1. `actor.harness`, `actor.first_seen` and `actor.gone_at`, for a Mate card's harness and whether it is running. `v_now` carries neither.
2. `SUM(turn.tool_count)` per actor, for the tier-3 ledger's tool-call count. `v_task_ledger` has no such column.
3. The Mate's own turn count, token buckets and cost, from `turn`. `v_task_ledger` joins `task`, and a Mate has no task row.
4. `turn`, `action`, `question` and `message` themselves, for the turn timeline: the views summarise a task, they do not enumerate what happened inside it.
5. `transition`, for which actors moved since an event id, because `v_now` is a snapshot with no history in it.
6. Each actor's latest `turn` joined to `pricing`, for every scene row's `context_tokens` and `context_pct`, and for a Mate the latest turn of the session its `mate.meta` names.
7. The `agent` of `tool.called` event payloads, for a task's `agents`.
8. `turn` joined to `actor`, and the `incident` table, for the daily board's tokens and incidents (section 3c).

Scene rows are read from `v_now` directly rather than through `scene.Now`, which selects only the eight columns `mate events --scene` prints.

## 10. The UI

The admin page, under `internal/dashboard/ui/`: `index.html`, `app.css`, `app.js`, `task.js` (the task tier, docs/design/task-page.md), `trace.js` (its Trace tab) and `humanize.js`. `chat.js` beside them is the chat's markdown and meta line, shared by Mate Office and Agent City.
Plain HTML, CSS and JavaScript loaded as classic scripts - no build step, no framework, no CDN and no off-origin request of any kind.
`internal/dashboard/ui_assets_test.go` walks every embedded file and fails the build on a `<script src=`, `<link href=`, `<img src=`, `fetch(`, `url(`, `@import` or `new WebSocket/EventSource/Worker` pointing anywhere but this origin, and on any bare `http://` or `https://` outside a comment.

### Routes

The three tiers are one document, routed on the hash, so a link to a task survives a copy-paste and the back button works.

| Hash | Tier |
| --- | --- |
| `#/` | workspace: one card per project - mode, the Mate's harness, running/stopped, scene state, since, tokens today, context %, crews counted by state, `N waiting` |
| `#/p/<project>` | project: the Mate panel, the task table (filter chips, default `open`), the inbox beside it |
| `#/p/<project>/mate` | Mate: prompt-level questions, replies, decisions and outcomes; filter by captain, crew or system |
| `#/p/<project>/t/<crew>` | task: one header (state, crew tokens, Mate tokens around the task, model calls, spawn→close, time waiting on the machine, context peak, questions · hand-backs) over two tabs. **Journey** (default): a swimlane of Captain, Mate, Observer, Crew, Machine and GitHub from spawn to close, the hand-offs between them as numbered steps with their price, the project's workflow checked (`Repo workflow`), and three cards reading the task at a glance. **Trace**: the per-step diagnostics - a strip of the stages in prompt order (one block per stage, as wide as its tokens, coloured by kind, skill pins and failed commands marked; a block opens its segments and evidence below the strip), findings with the most expensive step and the repeats among them, one effort-by-kind table that filters the strip; the tool mix, recording details, ledger totals, status lines, questions and raw model calls are folded, the branch diff stays a section |

`#/p/<project>/t/<crew>/usage` is the task tier opened on its Trace tab, then scrolled once to the shared header (`.task-kpis`) when it has rendered; the crumbs and links keep producing the plain task hash, and a URL without the suffix opens Journey and does not scroll.
The chosen tab is kept across polls and reset when the route moves to another crew.

The Journey tab is built from what section 4 already returns: the first prompt is the spawn, a later prompt is a message from Mate or, when it is a `<task-notification>`, a background command reporting back; a question is a hand-off to the captain and its answer one back; a status line whose own verb (the word before the colon in `line`) is `pr-open` or `pr-merged` is a hand-off to or from GitHub, `wait-mate`/`handback` one to Mate, `needs-decision`/`blocked` one to the captain; the crew lane draws the prompt overviews' stages by kind; the machine lane draws native, non-poll executions of 5 s or more; a gap of 5 s or more between prompts is idle.
The brief, the hand-back, the Mate's turns about the crew, the observer's incidents, the PR from `.meta` and the workflow come from section 4's `journey` field; what it names in `missing` is said to be missing on the page rather than drawn as zero.
Mate Office links to it.

A project or crew name is `encodeURIComponent`-ed into the hash, and a turn id into the path of `/turns/{turn}` (section 5).

### Live update

One loop, one request at a time: `GET /api/events?since=<the last_event_id the page's data was built at>&wait=<n>`, plus `&project=<p>` below the workspace tier. `wait` is 5 on the Mate and task tiers and 20 otherwise.
On any `events` or `now` the page re-fetches only the endpoint the current tier reads and re-renders, keeping scroll position, the expanded turns and keyboard focus. The Mate and task tiers re-fetch after a quiet poll too, because execution clocks and observer health move before a new event.
Because the API is cached per `last_event_id` and the page always polls from the id its own bytes carry, a re-fetch after an event that changed nothing it shows costs the server a cache hit.

The header links to Mate Office (section 11), opened on the crew drawer for a task page, the Mate drawer for a Mate page, and the whole floor otherwise, and beside it to Agent City (section 12).
The header carries the state as words: `live · HH:MM:SS` after every answered poll, `stale · <reason>` when one fails, where the reason is the API's own `reason` or, for a transport failure, "the dashboard is not answering".
A failed poll retries every 3 seconds rather than spinning.

One thing the `since` contract does not cover: `mate reindex` deletes and re-inserts every event, so `MAX(event.id)` can go *backwards*.
A page holding a cursor from before a rebuild would poll a dead id for ever while reporting `live`, so the page treats a `last_event_id` lower than its own cursor as a rebuild, takes the server's number and re-fetches.

### Numbers

`humanize.js` re-implements `internal/query`'s `HumanizeTokens` and `HumanizeCost` - `523`, `96.3k`, `1.2M`, `$0.12`, `$1.2k` - including Go's round-half-to-even, which JavaScript's own `toFixed` does not do (`$0.125` is `$0.12` in Go and `$0.13` in `toFixed`).
`TestUIHumanizeMatchesGo` builds a table of ~900 cases from the Go functions, sweeping every boundary plus a deterministic spread, and replays it through the page's own file in node when the machine has one.
Durations (`840ms`, `6.2s`, `1m7s`, `2h04m`, `3d 4h`) are the page's own: the console's `age` answers a different question and rounds a minute and seven seconds down to `1m`.

A `null` cost or context percentage renders `?`, never `0` - the same answer `mate usage` prints.
A timestamp is shown as a local clock time with the database's own RFC3339 string on hover, so a value on the page and a row in the database stay comparable.
Every turn, action, status line and event carries its `ref` as a `path:offset` tooltip.

### Vocabulary and colour

The state words are `internal/timeline/scene`'s own (`at_desk_working`, `waiting_review`, `walking_to_ceo`, …) and the inbox's phrases (`needs an answer`, `stuck, quiet too long`, `agent gone`, `over budget`, `send wedged`) name the same needs the console's box does, so the TUI and the browser describe the same workspace.
Every state word is a glyph plus the word: colour is never the only difference between a crew working and a crew blocked.

Colour is defined once as tokens on `:root` and redefined under `prefers-color-scheme: dark`; the dark series steps are chosen for the dark surface rather than flipped.
The four token buckets are a stacked meter in a fixed slot order - input, cache read, cache write, output - assigned to the bucket and not to its size, with a 2px surface gap between segments and a legend that always shows the numbers.
Context % is a single-hue meter, because it is one magnitude against one window.
Below 720px the tables collapse to one card per row and the page does not scroll sideways.

## 11. Mate Office

A second face on the same API, served at `/office/` from `internal/dashboard/ui/office/`: `index.html`, `office.css`, `office.js`, plus `../humanize.js` shared with the admin UI and `../chat.js` shared with the City.
It draws the workspace as an office from the owner's "Mate Office" design: a hallway with the captain's desk and the crews standing in it, and one room per project with the Mate at the big desk, a desk per open crew and a filing cabinet of closed tasks.
A room's door light is the project's, not only its Mate's: `stopped` when the Mate is not running, `running` while the Mate or any open crew is at work (a Mate in any state but `idle`, `asleep`, `blocked` or `gone`; a crew `arriving`, `at_desk_working`, `walking_to_ceo` or `leaving`), and otherwise `idle 3h`, the age of the latest scene `since` among the Mate and its open crews.
The nameplate of each room shows how many tokens that project has consumed: the Mate's own turns plus every task, open and closed, which is the total line of `mate usage`.
A room whose project page has not been read, or whose sum is missing a part, shows `?`.
It holds to every rule of section 10 - same origin only, text set with `textContent`, no build step, `null` shown as `?` - and its only request that is not a GET is an action button's `POST /api/actions` (section 7b).
Its view switch is `Office | City | Admin`.

| Hash | Opens |
| --- | --- |
| `#mate/<project>` | the Mate drawer, in three tabs. **Talk**: the chat (section 7b's `/chat`), a composer that sends with ⌘↵, and `Interrupt`. **Work**: the Mate's work board (section 3e). **Details**: usage, current activity, facing, last turn, mode, the inbox daemon, the crews in the room and the Apps block |
| `#crew/<project>/<crew>` | the crew drawer: state and why, current activity, step, target, usage with a `Usage details` button, the task, its branch, agents and skills, a link to the admin timeline |
| `#cabinet/<project>` | the filing cabinet: closed tasks by age, filterable by final state, each with its final state and a `Usage details` button under it |
| `#inbox/<project>` | the room's message box: what waits in the inbox (section 3), each with `Assign to Mate` or `Open crew`, or the `Full log · 24h` of every crew's lines (section 4b) |
| `#add` | the add-project form: a name and an optional repo path (section 7b's `add_project`) |
| `#board`, `#board/<date>` | the daily board: tokens, work done and incidents by day (section 3c), or one day opened up |
| `#captain` | the captain drawer: the news every Mate sent the captain, with where each stands (section 3d) |

The Mate drawer and the crew drawer each have an `Actions` block under their head: section 7b's buttons, in the console sheet's order, plus `Remove project…` for the Mate and `Peek` for a crew. A button that cannot run is disabled with its reason under it, and so are the console's buttons no page can run yet (`Repair binding…`, `Open in Zed`, Jev's explain on the Mate). A destructive one opens a question with `Cancel`, where the focus lands, and the action's own button; only that one runs it. While an action runs every button waits, and the one running says `Running …`. The answer is the console's one line, or the service's error in its own words, and the page re-reads the floor and the buttons at once. `Diff` shows the crew's branch (section 6) under the buttons and hides it on a second press, and `Peek` its screen. A dashboard with no console says it is read-only instead of drawing buttons.

Each room has an `inbox` mailbox that opens `#inbox/<project>`, and the floor ends with a `New room` door that opens `#add`.
The Mate drawer's head has `Full screen`, which widens it to the page. The top bar has `Mates only`, which draws each room as its Mate alone (the console's `v`), and a light/dark theme switch. All three are remembered in the browser.

`Usage details` is a link to `../#/p/<project>/t/<crew>/usage` (names `encodeURIComponent`-ed) that opens the admin page in a new tab (`target="_blank"`, `rel="noopener"`); the Mate has none.

The Details tab ends with an Apps block from `/api/plugins?project=<p>` (section 3b), read only while that drawer is open: one line per app with its name, its status chip and, only for a `configured` app with an `http(s)` `open.url`, an `Open` link to that URL in a new tab (`target="_blank"`, `rel="noopener"`).
A `configuration error` shows its message as text; when any app has an MCP server one line says "Login and tool status: check /mcp in the Mate's Claude pane", because the page does not know.
A project with no apps has no block, and a failed read shows `Apps: ?` while the rest of the drawer still draws.
It is a view, not a place to manage apps: no form, button or request beyond that GET.

The hallway holds a whiteboard, `Daily board`, that opens `#board`: section 3c's numbers in a wide drawer, the whole workspace or one project, over 7, 14 or 30 days (the choice is remembered in the browser).
`Today so far` sets today against the average of the days before it in the window - tokens, tasks opened, done, dropped (discarded and didn't start), approved reviews, rework (reviews that requested changes), incidents and over budget - with the change coloured by which way is better; today is still running, so a lower number before evening is not news.
`By day` is one row per day, newest first, with a token bar split into Mate and crews and each count's breakdown as its tooltip.
A day opens `#board/<date>` (`/api/board/day`): that day against the average of the others, then `Tasks`, `Tokens by who` and `Incidents`, with `‹` and `›` to step a day.
The board reads `/api/board` only while it is open, and again when an event lands.

The captain's desk in the hallway (on a phone, the captain's card) is a button that opens `#captain`; it says how much news is unread or waiting for an answer. A news that arrives while the page is open shows a toast with `Open news`.
The drawer lists every Mate's news, newest first: `Waiting on you` (unread and read-not-answered), `The Mate is on it` (answered), and `Earlier` (closed and dropped, the latest ten with `Show all`).
Each row names its project, crew and `#id` with a status chip; opening it shows the question, context, options, when it was sent, read and answered, the captain's answer and the Mate's updates.
A news waiting on the captain opens with an answer form: pick an option or write one, then `Send to the Mate` (section 7b's `answer`, CON-161). Talking it over privately is the City's. A read-only dashboard says to answer in the Mate's REPL instead.
Nothing here marks a news seen. A project whose log cannot be read shows `<project>: news ?` with the reason as its tooltip.

Reads: `/api/workspace`, `/api/now`, `/api/projects/{p}` per project, `/api/news` and `/api/work` (again on every quiet poll, because both move without an event), one project's `/actions` to learn whether the page can act, and for an open drawer `/api/projects/{p}/actions` plus `/api/projects/{p}/mate`, `/chat` and `/api/plugins?project=<p>`, or `/api/projects/{p}/tasks/{crew}`, or `/history` for the inbox log, or `/api/board?days=<n>` and `/api/board/day`.
It long-polls `/api/events` from the lowest `last_event_id` it holds (`wait=5` with a drawer other than the cabinet or the board open, 20 otherwise), re-reads the floor when anyone moved, and re-reads the open drawer after a quiet poll.
A `timeline_stale` in the answer turns the live pill `stale` with `timeline <p> since <time>: <error>` until an answer without it.

The typefaces are the design's - Geist, Geist Mono and Bricolage Grotesque - as latin WOFF2 subsets under `ui/office/fonts/`.
All three are under the SIL Open Font License 1.1; the licences are embedded from `internal/dashboard/fontlicenses/` and served at `/office/fonts/licenses/<Family>-OFL.txt`, kept outside `ui/` because they name web addresses.

`internal/dashboard/ui_office_test.go` proves the page, its assets, the fonts and their licences are served from the binary and tests `/api/now`; it runs `ui_office_test.cjs`, which drives `office.js` in node against `testdata/office/busy.json` (built by `gen.cjs` beside it) for the drawers, the Apps block, the stuck crew, unknowns and the phone sheet.

A cabinet row's final state is in the reader's words, not the crew state machine's, which `crew list` and the console keep.
A `failed` task with a `failed_reason` is `didn't start`, the one red final state, with the reason as its tooltip.
Otherwise a reviewer shows its `review` however it was closed: `approved` (pass), `changes requested`, `review incomplete`, or `no verdict`.
Then a task the timeline saw merged is `merged`, a `failed` task is `discarded`, because closing with `--discard` is a decision rather than a fault, and any other `close_state` is shown as it is, `finished` for most.
A row shows its final state as a glyph only, one per state, with the word and why as its tooltip and accessible name, because the column is too narrow for `changes requested`; the summary above the table names each glyph with its count.

Each cabinet row, and each task in the cabinet's peek, starts with a glyph for its `role`: code brackets for a ship (coding), a compass for a scout, a checked clipboard for a review; its tooltip names the role, and a review names the task it reviewed. A row without `role` shows the unknown glyph rather than passing for a ship.

A crew wears a badge for each kind of agent it delegated to (`agents`, CON-124): under its state chip at its desk and in the hallway, after its name in the cabinet's peek, the cabinet rows and the daily board's task rows, on its phone card, and as an `Agents` line in the crew drawer (`none delegated to` when there are none). The badge is the kind as written - `backend-reviewer`, `ios-reviewer`, `Explore` - with "delegated to the … agent" as its tooltip and accessible name; the page does not know which agents a project defines, so it does not sort or colour them by kind. A crew that delegated to none wears none.

The Office filing cabinet includes a `Harness / model / effort` column. Harness comes from the recorded actor, model prefers `last_model` (observed) and falls back to `model` (requested launch setting), and `effort` is the requested launch setting retained in crew meta. These optional fields are omitted when unavailable; the cabinet displays `?` rather than guessing defaults. Requested effort is not proof of runtime effort. The cabinet is wider on desktop and its table scrolls horizontally on narrow screens.

## 12. Agent City

A third face on the same API, served at `/city/` from `internal/dashboard/ui/city/` (CON-125): the workspace as a 3D city, the same scene as the Agent City promo, built from the live API instead of a snapshot.
It is linked from the admin header (`Agent City`, beside `Mate Office`) and from the Office's view switch.

| File | Holds |
| --- | --- |
| `index.html`, `city.css` | the page: title, view switch, Needs you, Go to, System, live pill, the rail, pins, the panel, the scrub bar; it loads `../chat.js` before the module |
| `app.js` | reads the API, keeps the city live, the panels and their buttons, pins, hover, click and keys |
| `city.js` | the city: island, streets, City Hall, one block per project, one tent per repo, the setters `setCrews`, `setMate`, `setMerged`, `setBoard`, `setHall`, `setMailbox` |
| `room.js` | one tent up close, playing back its history |
| `history.js` | what a tent's agents did, read from the history endpoint |
| `chatter.js` | the small talk mascots say now and then, by pose |
| `agent-mascot.js`, `bubble.js`, `harnesses.js` | the AgentMascot component, as in the promo |
| `vendor/three.js` | three.js r186 and the addons the page uses, prebuilt into one ES module; its header carries the MIT licence and the command that built it |

The pages are ES modules rather than classic scripts, the one way this UI departs from section 10's letter; there is still no build step at serve time, no framework fetched and no CDN.
`TestUIMakesNoOffOriginRequest` walks the vendored file too: the only URLs in it are the XHTML and XLink namespaces `createElementNS` and `getAttributeNS` take as identifiers, exempt beside the SVG one.
`internal/dashboard/ui_city_test.go` proves the page is the embedded file, follows every relative `import` from `app.js` and checks each module is served as JavaScript, and checks both other UIs link here.

The city is a grid of blocks, 3×3 or 5×5 past eight projects, with City Hall at the centre; projects take the blocks in workspace order, the ones beside the hall first, and the blocks left over are parks. A workspace with more than 24 projects draws the first 24 and says how many it left out.

| Thing | From |
| --- | --- |
| a tent per repo | `repos` (section 2); a project with none gets one tent named after it |
| a tent's sign: `N merged` | ship tasks with a `merged_at` whose `repo` is the tent's (a task whose repo is unknown or no longer in `project.yaml` counts in the project's first tent); a rise pops the sign |
| the crews in a tent | the project's tasks whose scene `state` is not `gone`, posed by state: `at_desk_working` works, `walking_to_ceo`, `waiting_at_ceo` and `blocked` think, `asleep` sleeps, the rest stand idle; a new crew says its name, a changed state says a word |
| the Mate on the plaza | the card's `mate`: asleep when not running, else posed by `state` |
| the project's signboard | `/api/board?days=7` for the project: tokens, `closed.finished`, `reviews.pass`, tokens per task |
| City Hall's screen | each project: Mate running, crews not `gone`, tokens today (the board's last day, else the Mate's `tokens_today`) |
| the mailbox | news with `status` `new` or `waiting` across `/api/news`; the flag is up while there is any |

An unknown harness (one AgentMascot has no preset for) is a plain grey mascot with a ring for a logo, named after the harness.

Every mascot, in the city and in a tent, now and then says a line going round Vietnamese social media that fits its pose - working, thinking, idle, done or asleep - from `chatter.js`, every 12 to 34 seconds at random, and only when it is not already talking (CON-128). Small talk is for fun and says nothing about the work: it comes in a yellow bubble, never the paper one real lines use, and never in a tent's feed. In a tent it falls only where the mascot has said nothing real for a moment, one line at a time in the room, seeded by the crew's name so a rebuilt tent says the same lines at the same times; past the history it goes on while the tent is watched.

Since CON-142 the city also does the console's work (design: `docs/design/agent-city-console/`).
Every thing is a place, and one panel holds what it can do: docked on the right of a wide screen, a bottom sheet with a grab handle on a phone, the camera flown (and its view offset) so the thing stays in sight beside it.

| Place | Panel |
| --- | --- |
| a Mate on its plaza | state, mode, tokens, context; the Mate's buttons (section 7b); tabs Chat (the REPL's conversation, send with a 5-second undo, Interrupt), Work (the work board, section 3e, a done row ticked with a 5-second undo), Inbox (what waits, with `Assign to the Mate`), Apps (`/api/plugins`), Project (`Remove project…`) |
| a crew | state, status line, harness · model · effort, branch, tokens; its buttons, `Explain notice`; tabs Status, Peek and Diff; links to the admin timeline, usage and the Office drawer |
| City Hall | Topics (every news, waiting first: options, an own answer with a 5-second undo, `Discuss privately` in a read-only fork of the Mate) and Projects (each one's Mate and crews, `Remove…`, `+ Add project…`) |
| an empty park lot | the add-project form; the new project takes the next lot |
| a block | its Mate, tents and crews |
| a tent | its playback (below) and the crews in it, as tabs Playback and Crews here |
| System (the top bar's chip) | whether the page and each project's timeline are current, whether actions are served, and what each auto daemon sent (section 7b's `daemon`) |

On the web only a topic needs the captain (CON-154): a news its Mate opened that the captain has not answered. An incident, a crew ready to merge or a crew asking its Mate belongs to its tent, where `Crews here` says it.
Pins over the scene are buttons, reached by Tab: City Hall's `?` with the count of topics waiting, and `+` on a project with no Mate. One pin per thing, the most urgent; a running action's `⟳` and a result's `✓`/`✕` win for a moment. Far off a pin keeps its glyph and drops its words. The `Needs you` rail lists the topics waiting; `n` opens the next, `/` or `g` opens Go to (every place by name; on a phone it is how a crew is reached), `h` City Hall, `Escape` closes or goes back.
Every button keeps its place and a line under it for the reason it cannot run: the API's, or the page's own (read-only, stale, replaying, another action of the same Mate or crew running). A destructive one asks first in the console's words, `Cancel` focused. A press says `Running …` until the one line comes back: `✓` and the line, `⊘ Refused, nothing changed:` for a 409, `✕ Ran and failed:` for a 422, each in the server's words. A merge carries a crate from the crew to its tent's sign.
A selected mascot does not make small talk. A standalone dashboard shows the panels without buttons and says it is read-only.

Clicking a tent opens `#tent=<project>/<repo>`: the tent up close, playing back what its agents did (CON-127).
Its tasks are the project's rows on the tent spawned since midnight UTC seven days ago, or still open; a tent with no ship task among them takes its last three merged ship tasks and their reviews instead, and `since` moves back to the earliest spawn. Their lines are one read of section 4b with that `since`.
The playback is every task's spawn (or, for a reviewer, the Mate asking it to review), all its status lines, its questions and the answers, its hand-backs, each reviewer's verdict (`review` of the reviewer row; the reviewer's last hand-back is where it lands), and the merge, or the close of a task that was not merged, in the order they happened, one every 2.5 seconds whatever the real gap; the panel lists them with their real local times.
A crew sits at the round table from its spawn until just before the first event after its task closed; seats are reused, and past seven a second arc forms behind the first. The task board shows each ship task as it stood at that moment: `Doing` from its spawn, `Review` while a reviewer of it is at the table, `Done` once merged, with its latest status line or the PR.
The scrub bar under the tent runs from the first event to now, with the days and the merges marked: drag it, or click a line in the panel, to go there. Play runs at 1×, 4×, 16× or 64× until it reaches now; `Live` jumps to now and follows. A tent opens live, three events before now. Live and caught up, the open crews and the Mate end posed by their scene states, and a new event on the project reads the history again and plays it; scrubbed back, the view stays on the same event. `Space` plays and pauses.
A tent's mascots keep their whole timeline (AgentMascot's `keep`), so a pose or a line scrubbed back to is the one played there; by default a mascot drops what is more than ten seconds older than its newest.
The history is kept per tent while the project's rows are the same, and read ahead while the pointer rests on a tent, so a click opens it at once. A repo with no task shows a sleeping Mate.
Acting is for now only: while a tent is scrubbed back every button waits with `replaying; go Live to act`, and the panel offers `Go Live` (`L`).
`Escape` leaves the tent.

Reads: `/api/workspace`, `/api/projects/{p}` per project, `/api/actions`, `/api/board?days=7`, `/api/news`, `/api/work`, `/api/projects/{p}/history` for a tent, and for an open panel `/chat`, `/forks/{news}`, `…/diff` and `/api/plugins`; its one write is `POST /api/actions`.
The city is drawn as soon as `/api/workspace` answers; each project's crews and signs fill in as its page arrives, and the board and the news are read beside them and never hold the city up (CON-126).
It long-polls `/api/events?since=<last_event_id>&wait=20`; an answer with events re-reads the workspace and the pages of the projects they name, an id that went backwards (a `mate reindex`) re-reads every project, and every answer re-reads `/api/actions`, `/api/news`, `/api/work`, the open chat or fork and, once it is five minutes old, `/api/board`. The pill says `live` or `stale` with the reason, and a failed poll retries every 3 seconds.
The page marks `city:drawn` and `city:tent <project>/<repo>` on the browser's performance timeline at the first frame of each.
The scene is one light palette whatever the system theme.
