// The Journey tab of the task page (docs/design/task-page.md): the header's
// seven numbers, the tab bar, the swimlane, the numbered hand-offs and the
// glance cards, all read from the task body the API returns today. Run with:
// node --test internal/dashboard/ui_journey_test.cjs
const { test } = require("node:test");
const assert = require("node:assert/strict");
const { tokens, at, keyed, boot } = require("./ui_testdom.cjs");

// A closed crew shaped like mihuv2/hu-246-editor-block-knob (31/10): two
// prompts, the second a task-notification from a background gate; a build
// the crew waited on; one failed short command; a question the captain
// answered; a PR opened, merged, and the crew closed.
const stage = (kind, from, to, calls, extra = {}) => ({ kind, label: kind, started_at: at(from), ended_at: at(to), elapsed_ms: (to - from) * 1000,
  model_calls: calls, executions: 0, tokens: tokens(10, 100 * calls), call_ids: [], segment_ids: [], skills: [], parts: [kind], open: false, ...extra });
const closedCrew = () => ({
  project: "mihuv2", crew: "hu-246", generated_at: at(900), last_event_id: 9,
  ledger: { text: "HU-246: the knob turns a text block", state: "gone", closed: true, close_state: "finished",
    spawned_at: at(0), closed_at: at(780), turns: 9, tool_count: 12, tokens: tokens(84, 3000000, 28000, 7000),
    context_tokens_last: 90000, context_pct: 9, question_count: 1, handback_count: 1, waited_ms: 30000, harness: "claude" },
  branch: { name: "mate/hu-246", exists: false, reason: "the crew was torn down" },
  status_lines: [
    { event_id: 1, at: at(1), verb: "working", text: "setup", line: "working: setup" },
    { event_id: 2, at: at(740), verb: "wait-mate", text: "https://github.com/x/mihuv2/pull/215", line: "pr-open: https://github.com/x/mihuv2/pull/215" },
    { event_id: 3, at: at(760), verb: "wait-mate", text: "https://github.com/x/mihuv2/pull/215", line: "pr-merged: https://github.com/x/mihuv2/pull/215" }
  ],
  questions: [{ text: "Keep RotateGesture?", asked_at: at(100), answered_at: at(130), answered_by: "captain", answer: "No, drop it.", waited_ms: 30000 }],
  turns: [
    { id: "call-1", ordinal: 1, started_at: at(1), ended_at: at(5), duration_ms: 4000, tokens: tokens(2, 6000), context_tokens_after: 28000 },
    { id: "call-2", ordinal: 2, started_at: at(5), ended_at: at(140), duration_ms: 135000, tokens: tokens(2, 28000), context_tokens_after: 110000 },
    { id: "call-3", ordinal: 3, started_at: at(600), ended_at: at(700), duration_ms: 100000, tokens: tokens(2, 90000), context_tokens_after: 90000 }
  ],
  performance: {
    version: "v1", generated_at: at(900), tokens: tokens(84, 3000000, 28000, 7000), model_calls: 9,
    time: { prompt_elapsed_ms: 620000, tool_elapsed_ms: 414000, invocation_ms: 414000, decision_wait_ms: 30000, unallocated_ms: 176000 },
    freshness: { last_observed_at: at(780), last_ingested_at: at(900), last_usage_at: at(700), stale: false },
    prompt_turns: [
      { id: "prompt-1", prompt: "Read and follow the brief at <b>brief.md</b>. Start now.", prompt_at: at(0), started_at: at(1), ended_at: at(440),
        elapsed_ms: 439000, model_calls: 6, tokens: tokens(72, 2500000), segment_ids: [],
        overview: { summary: "", rule: "prompt-work-v1", coverage: "", sequence: [], categories: [], steps: [
          stage("research", 1, 36, 2), stage("edit_code", 36, 140, 3), stage("test", 140, 440, 1)
        ] } },
      { id: "prompt-2", prompt: "<task-notification>gate finished exit 0</task-notification>", prompt_at: at(556), started_at: at(556), ended_at: at(740),
        elapsed_ms: 184000, model_calls: 3, tokens: tokens(12, 500000), segment_ids: [],
        overview: { summary: "", rule: "prompt-work-v1", coverage: "", sequence: [], categories: [], steps: [stage("test", 556, 740, 3)] } }
    ],
    segments: [], findings: [], processes: [], skills: [], loops: null,
    executions: [
      { id: "exec-fail", prompt_id: "prompt-1", tool: "Bash", command: "find . | xargs wc -l", started_at: at(4), ended_at: at(4), duration_ms: 200, status: "failed", exit_code: 123 },
      { id: "exec-build", prompt_id: "prompt-1", tool: "Bash", command: "xcodebuild build-for-testing -scheme Mihu", started_at: at(140), ended_at: at(246), duration_ms: 106000, status: "completed", exit_code: 0 },
      { id: "exec-gate", prompt_id: "prompt-1", tool: "Bash", command: "make pre-merge E=E2", started_at: at(437), ended_at: at(556), duration_ms: 119000, status: "completed", exit_code: 0 },
      { id: "exec-wrapper", prompt_id: "prompt-1", tool: "exec", is_wrapper: true, started_at: at(437), ended_at: at(556), duration_ms: 119000, status: "completed" },
      { id: "exec-poll", prompt_id: "prompt-2", tool: "Bash", poll: true, command: "sleep 30", started_at: at(600), ended_at: at(630), duration_ms: 30000, status: "completed", exit_code: 0 }
    ]
  }
});

const kpi = (view, key) => keyed(view, "data-kpi", key);
const stepRows = view => view.querySelector(".journey-steps").querySelectorAll("tr").filter(tr => tr.getAttribute("data-step"));
const arrowOf = (svg, n) => svg.querySelectorAll(".journey-arrow").find(g => g.getAttribute("data-step") === String(n));

test("the header owns the seven numbers of the task and says what is unknown", async () => {
  const app = await boot(closedCrew(), "#/p/mihuv2/t/hu-246");
  assert.equal(app.view.querySelectorAll("[data-kpi]").length, 7);
  assert.match(kpi(app.view, "tokens").textContent, /3M/);
  assert.match(kpi(app.view, "mate").textContent, /\?.*not in the API yet/, "Mate's turns are unknown, not zero");
  assert.match(kpi(app.view, "calls").textContent, /9.*12 tool calls/);
  assert.match(kpi(app.view, "time").textContent, /spawn → close13m10:00 → 10:13/);
  assert.match(kpi(app.view, "machine").textContent, /6m54s.*66\.8% of the crew's time/);
  assert.match(kpi(app.view, "context").textContent, /110k/, "the peak is the largest context after a call, not the last");
  assert.match(kpi(app.view, "asked").textContent, /1 · 1.*waited 30s/);
  assert.match(app.view.querySelector(".crew-head").textContent, /done/, "the closed state is the pill's word");
});

test("Journey is the default tab, Trace keeps the diagnostics, and the choice survives a poll", async () => {
  const app = await boot(closedCrew(), "#/p/mihuv2/t/hu-246");
  const tabs = app.view.querySelectorAll("[role=\"tab\"]");
  assert.deepEqual(tabs.map(tab => tab.getAttribute("aria-selected")), ["true", "false"]);
  assert.ok(keyed(app.view, "data-panel", "journey"));
  assert.equal(keyed(app.view, "data-panel", "trace"), undefined);
  assert.equal(app.view.querySelector(".steps-card"), null, "the stage card belongs to Trace");

  tabs[1].click();
  await app.flush();
  assert.ok(keyed(app.view, "data-panel", "trace"));
  assert.ok(app.view.querySelector(".steps-card"));
  assert.equal(keyed(app.view, "data-panel", "journey"), undefined);
  await app.refresh(closedCrew(), true);
  assert.ok(keyed(app.view, "data-panel", "trace"), "a poll does not flip the tab back");
});

test("the hand-offs are numbered in time order, each with its lanes, its words and its price", async () => {
  const app = await boot(closedCrew(), "#/p/mihuv2/t/hu-246");
  const rows = stepRows(app.view);
  const summary = rows.map(tr => {
    const cells = tr.querySelectorAll("td");
    return [cells[0].textContent, cells[2].textContent, cells[3].querySelector(".what").textContent, cells[4].textContent];
  });
  assert.deepEqual(summary, [
    ["1", "Mate → Crew", "Spawned the crew", "7m19s · 2.5M · 6 calls"],
    ["2", "Crew → Captain", "Asked a question", "waited 30s"],
    ["3", "Captain → Crew", "Answered by captain", "–"],
    ["4", "Machine → Crew", "A background command finished", "3m4s · 500k · 3 calls"],
    ["5", "Crew → GitHub", "Opened PR #215", "–"],
    ["6", "GitHub → Mate", "Merged PR #215", "–"],
    ["7", "Mate", "Closed the crew · finished", "–"]
  ]);
  assert.match(rows[0].textContent, /Read and follow the brief at brief\.md\. Start now\./, "prompt markup is stripped to its words");
  assert.match(rows[1].textContent, /Keep RotateGesture\?/);
  assert.equal(rows[4].querySelector("a").getAttribute("href"), "https://github.com/x/mihuv2/pull/215");
  assert.match(rows[6].textContent, /branch mate\/hu-246 is gone/);
});

test("the swimlane draws stages by kind, long commands on the machine lane, failures, idle and one arrow per step", async () => {
  const app = await boot(closedCrew(), "#/p/mihuv2/t/hu-246");
  const svg = app.view.querySelector(".journey-svg");
  const crewBars = svg.querySelectorAll(".journey-bar").filter(g => g.getAttribute("data-lane") === "crew");
  assert.deepEqual(crewBars.map(g => g.getAttribute("data-kind")), ["research", "edit_code", "test", "test"]);
  assert.deepEqual(crewBars.map(g => g.getAttribute("data-prompt")), ["prompt-1", "prompt-1", "prompt-1", "prompt-2"]);
  const machine = svg.querySelectorAll(".journey-bar").filter(g => g.getAttribute("data-lane") === "machine");
  assert.deepEqual(machine.map(g => g.querySelector("text").textContent), ["build", "gate"], "wrappers, polls and short commands stay off the lane");
  assert.ok(machine[0].querySelector("title").textContent.startsWith("1m46s · xcodebuild"));
  const failed = svg.querySelectorAll(".journey-dot").filter(g => g.getAttribute("data-tone") === "critical");
  assert.equal(failed.length, 1);
  assert.match(failed[0].querySelector("title").textContent, /failed · find \. \| xargs wc -l/);
  assert.equal(svg.querySelectorAll(".journey-idle").length, 1, "the gap between prompt 1's last call and prompt 2 is idle");
  assert.match(svg.querySelectorAll(".journey-idle-label")[0].textContent, /idle 1m56s, no tokens/);
  assert.deepEqual(svg.querySelectorAll(".journey-arrow").map(g => g.getAttribute("data-step")), ["1", "2", "3", "4", "5", "6", "7"]);
  assert.equal(arrowOf(svg, 7).querySelectorAll("line").length, 0, "a step with no receiver is a number, not an arrow");
  assert.ok(svg.querySelectorAll(".journey-dot").some(g => g.querySelector("text") && g.querySelector("text").textContent === "merged"));
});

test("a crew stage opens Trace on its prompt", async () => {
  const app = await boot(closedCrew(), "#/p/mihuv2/t/hu-246");
  const stage = app.view.querySelector(".journey-svg").querySelectorAll(".journey-bar").find(g => g.getAttribute("data-prompt") === "prompt-2");
  stage.click();
  await app.flush();
  assert.ok(keyed(app.view, "data-panel", "trace"));
  assert.equal(app.doc.scrolledTo, keyed(app.view, "data-detail", "prompt:prompt-2"));
});

test("the glance cards read the round, the payer and the waiting from the same facts", async () => {
  const app = await boot(closedCrew(), "#/p/mihuv2/t/hu-246");
  const round = keyed(app.view, "data-glance", "round");
  assert.deepEqual(round.querySelectorAll("li").map(li => li.textContent), [
    "✓ spawned 10:00", "✓ pull request opened", "✓ merged", "✓ closed · finished",
    "asked the captain 1 time · handed back 1 time", "loaded no skill", "1 failed command"
  ]);
  assert.match(keyed(app.view, "data-glance", "tokens").textContent, /3Mthe crew, over 9 model calls.*not in the API yet/);
  const machine = keyed(app.view, "data-glance", "machine");
  assert.match(machine.textContent, /6m54s \/ 10m20s/);
  assert.deepEqual(machine.querySelectorAll("li").map(li => li.textContent), ["gate · 1m59s at 10:07:17", "build · 1m46s at 10:02:20"]);
});

test("a crew with nothing recorded says so instead of drawing an empty lane", async () => {
  const body = closedCrew();
  body.ledger = { text: "", state: "at_desk_working", turns: 0, tool_count: 0, tokens: tokens(0) };
  body.status_lines = []; body.questions = []; body.turns = []; body.performance = null;
  const app = await boot(body, "#/p/mihuv2/t/hu-246");
  assert.match(keyed(app.view, "data-panel", "journey").textContent, /Nothing has happened to this crew yet/);
  assert.match(kpi(app.view, "machine").textContent, /\?not measured/);
});
