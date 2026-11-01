// The Journey tab of the task page (docs/design/task-page.md): the header's
// seven numbers, the tab bar, the swimlane, the numbered hand-offs, the
// repo workflow and the glance cards, read from the task body and its
// `journey` field. Run with:
// node --test internal/dashboard/ui_journey_test.cjs
const { test } = require("node:test");
const assert = require("node:assert/strict");
const { tokens, at, keyed, boot } = require("./ui_testdom.cjs");

// A closed crew shaped like mihuv2/hu-246-editor-block-knob (31/10): two
// prompts, the second a task-notification from a background gate; a build
// the crew waited on; one failed short command; a question the captain
// answered; a PR opened, merged, and the crew closed. Its journey: the
// brief, the hand-back, the meta, three Mate turns (the spawn, a false
// no_turn incident, the merge), the incident, and a four-step workflow.
const stage = (kind, from, to, calls, extra = {}) => ({ kind, label: kind, started_at: at(from), ended_at: at(to), elapsed_ms: (to - from) * 1000,
  model_calls: calls, executions: 0, tokens: tokens(10, 100 * calls), call_ids: [], segment_ids: [], skills: [], parts: [kind], open: false, ...extra });
const mateTurn = (id, prompt, from, to, total, calls, activities) => ({ id, source: "crew", prompt, prompt_at: at(from), response: "",
  started_at: at(from), ended_at: at(to), duration_ms: (to - from) * 1000, model_calls: calls, tool_calls: calls, model: "claude-opus-5-5",
  tokens: tokens(100, total - 100), activities });
const journey = () => ({
  brief: { path: "/ws/.mate/projects/mihuv2/crews/hu-246/brief.md", bytes: 22223, captain_words: "Xoay mọi món bằng núm trên đầu.",
    task: "## Captain's words\n\nXoay mọi món bằng núm trên đầu.\n\n## Build\n\n- Wire the knob." },
  handback: { path: "/ws/.mate/projects/mihuv2/crews/hu-246/handback.md", bytes: 9000, markdown: "Closes HU-246\n\n| criterion | pass/fail |\n…",
    closes: ["Closes HU-246"], deviations: ["Added holdGesture()"], still_open: ["A real touch on the knob is not verified"], learning: "none (HU-246 4397c62)",
    criteria: [{ name: "Gate green", result: "pass", pass: true, command: "make pre-merge E=E2", output: "PASS on 4397c62" },
      { name: "Real touch on the knob", result: "fail", pass: false, command: "–", output: "no headless touch" }] },
  meta: { pr_url: "https://github.com/x/mihuv2/pull/215", pr_state: "merged", pr_sync: "fast-forwarded main", merge_commit: "f128f5668c7a",
    lane: "xcode", teardown: "clean", cleanup: "done (simulator 1.8 GB)", state: "finished" },
  reviews: [],
  mate_turns: [
    mateTurn("m1", "hu-245 reports: PR #214", -60, 0, 1000000, 5, [{ at: at(0), kind: "crew.spawned", crew: "hu-246", text: "Started: HU-246" }]),
    mateTurn("m2", "hu-246 no turn: no turn in the ledger 5m0s after launch", 300, 306, 400000, 2, []),
    mateTurn("m3", "hu-246 reports: https://github.com/x/mihuv2/pull/215", 741, 800, 1500000, 7, [
      { at: at(756), kind: "merge.done", crew: "hu-246", text: "Merged into main" },
      { at: at(780), kind: "crew.finished", crew: "hu-246", text: "Closed as finished" },
      { at: at(795), kind: "crew.spawned", crew: "hu-227", text: "Started: HU-227" }])
  ],
  incidents: [
    { at: at(299), kind: "no_turn", state: "open", text: "no turn in the ledger 5m0s after launch" },
    { at: at(781), kind: "no_turn", state: "resolved", text: "crew hu-246 is closed" }
  ],
  workflow: [
    { step: "Read docs/harness/mate.md", state: "done", evidence: "cat docs/harness/mate.md | head -40", at: at(1) },
    { step: "Screenshots with make shot", state: "missed" },
    { step: "ios-ui-verify after a UI change", state: "missed" },
    { step: "android verify", state: "n/a", evidence: "no changed file matches \\.kt$" }
  ],
  changed_files: ["ios/NoteEditorView.swift"], missing: []
});
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
  },
  journey: journey()
});

const kpi = (view, key) => keyed(view, "data-kpi", key);
const stepRows = view => view.querySelector(".journey-steps").querySelectorAll("tr").filter(tr => tr.getAttribute("data-step"));
const arrowOf = (svg, n) => svg.querySelectorAll(".journey-arrow").find(g => g.getAttribute("data-step") === String(n));

test("the header owns the seven numbers of the task and says what is unknown", async () => {
  const app = await boot(closedCrew(), "#/p/mihuv2/t/hu-246");
  assert.equal(app.view.querySelectorAll("[data-kpi]").length, 7);
  assert.match(kpi(app.view, "tokens").textContent, /3M/);
  assert.match(kpi(app.view, "mate").textContent, /2\.9M3 whole turns · 14 calls/, "Mate's turns about this crew, whole");
  assert.match(kpi(app.view, "calls").textContent, /9.*12 tool calls/);
  assert.match(kpi(app.view, "time").textContent, /spawn → close13m10:00 → 10:13/);
  assert.match(kpi(app.view, "machine").textContent, /6m54s.*66\.8% of the crew's time/);
  assert.match(kpi(app.view, "context").textContent, /110k/, "the peak is the largest context after a call, not the last");
  assert.match(kpi(app.view, "asked").textContent, /1 · 1.*waited 30s/);
  assert.match(app.view.querySelector(".crew-head").textContent, /done/, "the closed state is the pill's word");

  const body = closedCrew();
  delete body.journey;
  const plain = await boot(body, "#/p/mihuv2/t/hu-246");
  assert.match(kpi(plain.view, "mate").textContent, /\?Mate's turns could not be read/, "no journey is unknown, not zero");
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
    const cells = tr.children.filter(node => node.tagName === "td");
    return [cells[0].textContent, cells[2].textContent, cells[3].querySelector(".what").textContent, cells[4].textContent];
  });
  assert.deepEqual(summary, [
    ["1", "Mate → Crew", "Spawned the crew", "7m19s · 2.5M · 6 calls"],
    ["2", "Crew → Captain", "Asked a question", "waited 30s"],
    ["3", "Captain → Crew", "Answered by captain", "–"],
    ["4", "Observer → Mate", "Incident no turn", "–"],
    ["5", "Crew → Mate", "Mate read the crew's digest", "6s · 400k · 2 calls"],
    ["6", "Machine → Crew", "A background command finished", "3m4s · 500k · 3 calls"],
    ["7", "Crew → GitHub", "Opened PR #215", "–"],
    ["8", "Crew → Mate", "Mate merged into main, closed the crew, spawned hu-227", "59s · 1.5M · 7 calls"],
    ["9", "GitHub → Mate", "Merged PR #215", "–"],
    ["10", "Mate", "Closed the crew · finished", "–"]
  ]);
  assert.match(rows[0].textContent, /Read and follow the brief at brief\.md\. Start now\./, "prompt markup is stripped to its words");
  assert.match(rows[0].textContent, /Mate's turn: spawned hu-246 · 1m · 1M · 5 calls/, "the spawning turn is the spawn row's price on the Mate's side");
  assert.match(rows[1].textContent, /Keep RotateGesture\?/);
  assert.equal(rows[3].getAttribute("data-bad"), "true", "an open incident is drawn as trouble");
  assert.equal(rows[6].querySelector("a").getAttribute("href"), "https://github.com/x/mihuv2/pull/215");
  assert.match(rows[9].textContent, /branch mate\/hu-246 is gone/);
});

test("the spawn row opens the brief and the PR row opens the hand-back", async () => {
  const app = await boot(closedCrew(), "#/p/mihuv2/t/hu-246");
  const brief = keyed(app.view, "data-detail", "journey:brief");
  assert.match(brief.querySelector("summary").textContent, /The brief · 21\.7 KB · captain: Xoay mọi món bằng núm trên đầu\./);
  assert.equal(brief.open, false, "closed until asked");
  assert.match(brief.textContent, /## Build/);
  const handback = keyed(app.view, "data-detail", "journey:handback");
  assert.match(handback.querySelector("summary").textContent, /The hand-back · 1\/2 criteria pass · 1 deviation · 1 still open · learning: none \(HU-246 4397c62\)/);
  const criteria = handback.querySelector(".criteria").querySelectorAll("tr").filter(tr => tr.getAttribute("data-pass"));
  assert.deepEqual(criteria.map(tr => tr.getAttribute("data-pass")), ["true", "false"]);
  assert.match(criteria[1].textContent, /✗ fail/);
  assert.ok(keyed(app.view, "data-detail", "journey:handback-md"), "the whole markdown is one more click away");
});

test("the swimlane draws every lane: stages, Mate turns, incidents, long commands, failures, idle and one arrow per step", async () => {
  const app = await boot(closedCrew(), "#/p/mihuv2/t/hu-246");
  const svg = app.view.querySelector(".journey-svg");
  const lanes = svg.querySelectorAll(".journey-lane-label").map(t => t.textContent);
  assert.deepEqual(lanes, ["Captain", "Mate", "Observer", "Crew", "Machine", "GitHub"]);
  const crewBars = svg.querySelectorAll(".journey-bar").filter(g => g.getAttribute("data-lane") === "crew");
  assert.deepEqual(crewBars.map(g => g.getAttribute("data-kind")), ["research", "edit_code", "test", "test"]);
  assert.deepEqual(crewBars.map(g => g.getAttribute("data-prompt")), ["prompt-1", "prompt-1", "prompt-1", "prompt-2"]);
  const mateBars = svg.querySelectorAll(".journey-bar").filter(g => g.getAttribute("data-lane") === "mate");
  assert.deepEqual(mateBars.map(g => g.querySelector("title").textContent.split(" · ")[0]),
    ["Mate spawned hu-246", "Mate read the crew's digest", "Mate merged into main, closed the crew, spawned hu-227"]);
  assert.equal(mateBars[0].getAttribute("role"), null, "a Mate turn is not a crew stage: nothing to open in Trace");
  const observer = svg.querySelectorAll(".journey-dot").filter(g => g.getAttribute("data-lane") === "observer");
  assert.deepEqual(observer.map(g => g.getAttribute("data-tone")), ["critical", "muted"]);
  assert.deepEqual(observer.map(g => g.querySelector("text").textContent), ["no_turn", "resolved no_turn"]);
  const machine = svg.querySelectorAll(".journey-bar").filter(g => g.getAttribute("data-lane") === "machine");
  assert.deepEqual(machine.map(g => g.querySelector("text").textContent), ["build", "gate"], "wrappers, polls and short commands stay off the lane");
  assert.ok(machine[0].querySelector("title").textContent.startsWith("1m46s · xcodebuild"));
  const failed = svg.querySelectorAll(".journey-dot").filter(g => g.getAttribute("data-tone") === "critical" && g.getAttribute("data-lane") === "crew");
  assert.equal(failed.length, 1);
  assert.match(failed[0].querySelector("title").textContent, /failed · find \. \| xargs wc -l/);
  assert.equal(svg.querySelectorAll(".journey-idle").length, 1, "the gap between prompt 1's last call and prompt 2 is idle");
  assert.match(svg.querySelectorAll(".journey-idle")[0].parentNode.querySelector("title").textContent, /idle 1m56s, no tokens/, "the gap keeps its words even where the label does not fit");
  assert.deepEqual(svg.querySelectorAll(".journey-arrow").map(g => g.getAttribute("data-step")), ["1", "2", "3", "4", "5", "6", "7", "8", "9", "10"]);
  assert.equal(arrowOf(svg, 10).querySelectorAll("line").length, 0, "a step with no receiver is a number, not an arrow");
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

test("the repo workflow lists the declared steps with the crew's evidence, and counts only the steps that apply", async () => {
  const app = await boot(closedCrew(), "#/p/mihuv2/t/hu-246");
  const table = app.view.querySelector(".workflow");
  const rows = table.querySelectorAll("tr").filter(tr => tr.getAttribute("data-workflow"));
  assert.deepEqual(rows.map(tr => tr.getAttribute("data-workflow")), ["done", "missed", "missed", "n/a"]);
  assert.match(rows[0].textContent, /Read docs\/harness\/mate\.md✓ done10:00:01 cat docs\/harness\/mate\.md \| head -40/);
  assert.match(rows[3].textContent, /– n\/ano changed file matches/);
  assert.match(table.querySelector(".total").textContent, /taken1 \/ 3of the steps that apply/);

  const body = closedCrew();
  body.journey.workflow = null;
  const none = await boot(body, "#/p/mihuv2/t/hu-246");
  assert.equal(none.view.querySelector(".workflow"), null, "a project that declares no workflow has no checklist");
});

test("the glance cards read the round, the payer and the waiting from the same facts", async () => {
  const app = await boot(closedCrew(), "#/p/mihuv2/t/hu-246");
  const round = keyed(app.view, "data-glance", "round");
  assert.deepEqual(round.querySelectorAll("li").map(li => li.textContent), [
    "✓ spawned 10:00", "✓ pull request opened · #215", "✓ merged · fast-forwarded main", "✓ closed · finished",
    "asked the captain 1 time · handed back 1 time", "loaded no skill", "repo workflow: 1 of 3 steps that apply", "1 failed command",
    "teardown clean · cleanup done (simulator 1.8 GB)"
  ]);
  assert.match(keyed(app.view, "data-glance", "tokens").textContent, /3Mthe crew, over 9 model callsMate 2\.9M over 3 whole turns about this crew/);
  const machine = keyed(app.view, "data-glance", "machine");
  assert.match(machine.textContent, /6m54s \/ 10m20s/);
  assert.deepEqual(machine.querySelectorAll("li").map(li => li.textContent), ["gate · 1m59s at 10:07:17", "build · 1m46s at 10:02:20"]);
});

test("what the answer could not read is said once, at the foot of the tab", async () => {
  const body = closedCrew();
  body.journey.brief = null; body.journey.handback = null; body.journey.missing = ["brief", "hand-back"];
  const app = await boot(body, "#/p/mihuv2/t/hu-246");
  assert.match(app.view.querySelector(".journey-missing").textContent, /Not in this answer: brief · hand-back/);
  assert.equal(keyed(app.view, "data-detail", "journey:brief"), undefined);
});

test("a crew with nothing recorded says so instead of drawing an empty lane", async () => {
  const body = closedCrew();
  body.ledger = { text: "", state: "at_desk_working", turns: 0, tool_count: 0, tokens: tokens(0) };
  body.status_lines = []; body.questions = []; body.turns = []; body.performance = null; body.journey = { mate_turns: [], incidents: [], missing: ["brief"] };
  const app = await boot(body, "#/p/mihuv2/t/hu-246");
  assert.match(keyed(app.view, "data-panel", "journey").textContent, /Nothing has happened to this crew yet/);
  assert.match(kpi(app.view, "machine").textContent, /\?not measured/);
  assert.match(kpi(app.view, "mate").textContent, /–no Mate turn names this crew/);
});
