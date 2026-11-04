// Focused interaction checks for the dependency-free dashboard. Run with:
// node --test internal/dashboard/ui_observability_test.cjs
// The DOM is ui_testdom.cjs's; layout and accessibility still need the
// real-browser acceptance check.
const { test } = require("node:test");
const assert = require("node:assert/strict");
const dom = require("./ui_testdom.cjs");
const { tokens, at, fixture, keyed } = dom;
// Everything here is about the Trace tab of the task page (docs/design/
// task-page.md, trace.js); the Journey tab has ui_journey_test.cjs. The
// base fixture is a recording older than stages, so its strip is drawn
// from the three work segments; stepsFixture below has stages.
const boot = (body, hash) => dom.boot(body, hash, { tab: "trace" });
const blocks = view => view.querySelectorAll(".tl-stage");
const openSegments = view => view.querySelectorAll("[data-segment]").filter(node => node.open).map(node => node.getAttribute("data-segment"));

test("finding opens exact evidence and its segment without inventing call usage", async () => {
  const app = await boot();
  assert.match(app.view.textContent, /Check TripUITests progress/);
  assert.match(app.view.textContent, /usage not linked to model calls/);
  keyed(app.view, "data-focus", "finding:finding-poll").click();
  const segment = keyed(app.view, "data-segment", "segment-poll");
  assert.equal(segment.open, true);
  assert.equal(segment.getAttribute("data-selected"), "true");
  assert.equal(keyed(app.view, "data-segment", "segment-test"), undefined, "only the finding's block is open below the strip");
  assert.match(app.view.querySelector("[data-evidence-panel]").textContent, /3 polls · 1 with new output · 2 unchanged/);
  assert.ok(app.doc.scrolledTo === app.view.querySelector("[data-evidence-panel]"), "scrolled to it");
  assert.equal(app.view.querySelectorAll("img").length, 0, "prompt markup remains text");
});

test("a block opens its segment, where a native child exit overrides wrapper success and unknown output stays unknown", async () => {
  const app = await boot();
  keyed(app.view, "data-focus", "stage:segment-test").click();
  await app.flush();
  const segment = keyed(app.view, "data-segment", "segment-test");
  assert.equal(segment.open, true);
  assert.match(segment.textContent, /exit 65 · failed/);
  assert.match(segment.textContent, /xcodebuild -only-testing:TripUITests/);
  assert.match(segment.textContent, /wrapper result · child exit unknown/);
  assert.match(segment.textContent, /output 82 B · new output unknown/);
  assert.equal(segment.querySelectorAll("script").length, 0, "recorded output is text");
  assert.equal(keyed(app.view, "data-focus", "stage:segment-test").getAttribute("aria-pressed"), "true");
  assert.ok(app.doc.scrolledTo === app.view.querySelector(".tl-drill"), "scrolled to the open block");
  keyed(app.view, "data-focus", "close-stage").click();
  assert.equal(app.view.querySelectorAll(".tl-drill").length, 0);
});

test("a top finding opens an unlinked native error without a segment", async () => {
  const body = fixture();
  const p = body.performance;
  const failed = p.executions.find(e => e.id === "exec-test");
  failed.call_id = "";
  p.segments[1].execution_ids = ["exec-wrapper"];
  p.findings = [{ id: "native-error", title: "TripUITests failed twice with exit 65", detail: "Simulator unavailable", count: 2,
    confidence: "native exit", tokens: null, segment_ids: [], call_ids: [], execution_ids: [failed.id], evidence: [] }];
  p.top_finding_ids = ["native-error"];
  const app = await boot(body);
  keyed(app.view, "data-focus", "finding:native-error").click();
  const panel = app.view.querySelector("[data-evidence-panel]");
  assert.ok(panel);
  assert.match(panel.textContent, /xcodebuild -only-testing:TripUITests/);
  assert.match(panel.textContent, /exit 65 · failed/);
  assert.ok(app.doc.scrolledTo === panel, "scrolled to it");
  assert.deepEqual(openSegments(app.view), []);
});

test("primary work labels keep wrapper JavaScript in evidence instead of the first screen", async () => {
  const body = fixture();
  const p = body.performance;
  p.segments[2].label = 'Build / test · const r = await tools.exec_command({cmd:"make check"});';
  p.segments[2].kind = "test";
  p.segments[2].execution_ids = ["exec-wrapper"];
  const app = await boot(body);
  const label = keyed(app.view, "data-focus", "current-work");
  // The fallback label comes from the shared KINDS table, so the `test` kind
  // reads "Run tests / build" (the projector's workLabels).
  assert.equal(label.textContent, "Run tests / build · native command not linked");
  assert.doesNotMatch(label.textContent, /const r/);
});

test("running native execution before usage is labelled pending, and synthetic gaps stay out of primary labels", async () => {
  const body = fixture();
  const current = body.performance.segments[2];
  current.model_calls = 0;
  current.call_ids = [];
  current.tokens = { input: 0, cache_read: 0, cache_write: 0, output: 0, total: 0 };
  current.label = "Activity not classified · turn.gap";
  const app = await boot(body);
  assert.equal(keyed(app.view, "data-focus", "current-work").textContent, "Activity not recorded");
  assert.match(app.view.querySelector(".work-current").textContent, /Usage not reported for this activity yet/);
  assert.doesNotMatch(app.view.querySelector(".work-current").textContent, /0 model calls · 0 tokens/);
  assert.match(keyed(app.view, "data-focus", "stage:" + current.id).getAttribute("title"), /usage pending/, "the block says so too");
  keyed(app.view, "data-focus", "current-work").click();
  assert.match(keyed(app.view, "data-segment", current.id).textContent, /Total\?/);
  assert.match(keyed(app.view, "data-drill", current.id).textContent, /Tokens\?/);
});

test("live refresh retains the open block, its evidence, focus and scroll", async () => {
  const app = await boot();
  const button = keyed(app.view, "data-focus", "stage:segment-test");
  button.click();
  button.focus();
  await app.flush();
  await app.refresh();
  assert.equal(keyed(app.view, "data-segment", "segment-test").open, true);
  assert.match(keyed(app.view, "data-segment", "segment-test").textContent, /exit 65/);
  assert.equal(app.doc.activeElement.getAttribute("data-focus"), "stage:segment-test");
  assert.equal(app.window.scrollY, 260);
  assert.ok(app.requests.some(url => url.includes("wait=5")));
});

test("elapsed and recording age advance locally without interpolating usage or fetching", async () => {
  const app = await boot();
  const requests = app.requests.length;
  const elapsed = app.view.querySelector("[data-elapsed-start]");
  const before = elapsed.textContent;
  const header = app.view.querySelector(".task-kpis").textContent;
  app.advance(35000);
  assert.notEqual(elapsed.textContent, before);
  assert.equal(app.view.querySelector(".task-kpis").textContent, header);
  assert.equal(app.requests.length, requests);
  assert.equal(app.view.querySelector("[data-observed-at]").textContent, "Recording is stale");
});

test("a closed task keeps its recording state but no observer ages", async () => {
  const body = fixture();
  body.ledger.closed = true;
  body.ledger.close_state = "finished";
  const app = await boot(body);
  assert.match(app.view.querySelector(".work-current").textContent, /Historical recording/);
  assert.equal(app.view.querySelectorAll(".freshness-line").length, 0);
  assert.equal(app.view.querySelectorAll("[data-age-at]").length, 0);
});

test("missing diagnostics gives an explicit coverage message and leaves totals reachable", async () => {
  const body = fixture();
  delete body.performance;
  const app = await boot(body);
  assert.match(app.view.textContent, /Work diagnostics are not available yet/);
  assert.ok(keyed(app.view, "data-detail", "ledger"));
  assert.ok(keyed(app.view, "data-detail", "raw-calls"));
  assert.equal(app.view.querySelectorAll(".stages-card").length, 0);
});

test("launch configuration and observed runtime remain distinct with missing document sizes unknown", async () => {
  const body = fixture();
  body.performance.runtime = { model: "actual-model", effort: "high", harness_version: "0.157" };
  body.performance.observed_inputs = [{ id: "instructions-1", kind: "instruction", path: "AGENTS.md", bytes: 42, sha256: "observed-hash", at: at(2), source_ref: { path: "rollout.jsonl", offset: 80 } }];
  body.performance.profile = { repo: "shop", harness: "codex", requested_model: "requested-model", requested_effort: "xhigh", repo_commit: "abc123",
    captured_at: at(0), repo_dirty: false, documents: [{ path: "CLAUDE.md", state: "missing", bytes: 0 }] };
  const app = await boot(body);
  const recording = keyed(app.view, "data-detail", "recording-details");
  assert.equal(recording.open, false, "the fold is closed until opened");
  assert.match(recording.textContent, /Observed runtime.*actual-model/);
  assert.match(recording.textContent, /Requested model requested-model/);
  assert.match(recording.textContent, /AGENTS.md · 42 B/);
  assert.match(recording.textContent, /CLAUDE.md · missing · size unknown/);
  assert.match(recording.textContent, /not proof that the harness loaded it/);
  assert.match(recording.textContent, /Unclassified segments: 0 of 3/);
});

function workOverviewFixture() {
  return {
    summary: "Researched the setup, edited code, and ran tests twice.", rule: "recorded-work-types-v1", coverage: "Mixed calls are charged once; one execution has no call link.",
    categories: [
      { kind: "research", label: "Research", model_calls: 1, execution_count: 1, tokens: tokens(100, 200), elapsed_ms: 4000,
        call_ids: ["call-1"], segment_ids: ["segment-read"], execution_ids: [], evidence: [{ id: "research-1", kind: "command", label: "Read build settings", at: at(2), command: "cat Makefile", status: "completed", exit_code: 0, source_ref: { path: "rollout.jsonl", offset: 10 } }] },
      { kind: "edit_code", label: "Edit code", model_calls: 0, execution_count: 0, tokens: null, elapsed_ms: null,
        call_ids: [], segment_ids: [], execution_ids: [], evidence: [{ id: "edit-1", kind: "file_change", label: "Changed Trip.swift", at: at(5), source_ref: { path: "rollout.jsonl", offset: 20 } }] },
      { kind: "test", label: "Run tests", model_calls: 0, execution_count: 2, tokens: null, elapsed_ms: 10000,
        call_ids: [], segment_ids: ["segment-test"], execution_ids: ["exec-test"], evidence: [{ id: "test-1", kind: "command", label: "TripUITests failed", at: at(10), command: "xcodebuild -only-testing:TripUITests", status: "failed", exit_code: 65,
          output_excerpt: "<script>unsafe()</script> simulator unavailable", source_ref: { path: "rollout.jsonl", offset: 30 } }] },
      { kind: "mixed", label: "Mixed activity", model_calls: 1, execution_count: 0, tokens: tokens(10, 400), elapsed_ms: null,
        call_ids: ["call-2"], segment_ids: ["segment-test"], execution_ids: [], evidence: [] },
      { kind: "unknown", label: "Unknown", model_calls: 1, execution_count: 0, tokens: tokens(50, 300), elapsed_ms: null,
        call_ids: ["call-3"], segment_ids: ["segment-poll"], execution_ids: [], evidence: [] }
    ],
    sequence: [
      { kind: "research", label: "Research", started_at: at(0), segment_ids: ["segment-read"] },
      { kind: "edit_code", label: "Edit code", started_at: at(5), segment_ids: [] },
      { kind: "test", label: "Run tests", started_at: at(10), segment_ids: ["segment-test"] },
      { kind: "edit_code", label: "Edit code", started_at: at(15), segment_ids: [] },
      { kind: "test", label: "Run tests", started_at: at(20), segment_ids: ["segment-poll"] }
    ]
  };
}

// The overview (app.js) is the Mate page's reading of an exchange; the
// crew page no longer draws it, its stages are the strip.
const mateBody = overview => ({ project: "shop", last_event_id: 1, mate: { harness: "claude", turns: 3, tokens: tokens(160, 900) },
  exchanges: [{ id: "exchange-1", source: "captain", prompt: "Fix the build", response: "Investigated and handed back", model_calls: 3, tool_calls: 4,
    started_at: at(0), prompt_at: at(0), duration_ms: 30000, tokens: tokens(160, 900), activities: [], overview }] });

test("Mate overview keeps mixed call tokens intact and unknown attribution out of zero-cost categories", async () => {
  const app = await boot(mateBody(workOverviewFixture()), "#/p/shop/mate");
  const exchange = keyed(app.view, "data-detail", "exchange:exchange-1");
  exchange.open = true;
  await app.flush();
  const mixed = keyed(app.view, "data-detail", "mate-overview:exchange-1:category:mixed");
  const tests = keyed(app.view, "data-detail", "mate-overview:exchange-1:category:test");
  const edits = keyed(app.view, "data-detail", "mate-overview:exchange-1:category:edit_code");
  assert.match(mixed.querySelector("summary").textContent, /420 tokens/);
  assert.match(tests.querySelector("summary").textContent, /usage not attributed/);
  assert.match(tests.querySelector("summary").textContent, /0 attributed calls · 2 recorded executions/);
  assert.doesNotMatch(tests.querySelector("summary").textContent, /0 tokens/);
  assert.match(edits.querySelector("summary").textContent, /time unknown/);
  assert.doesNotMatch(edits.querySelector("summary").textContent, /0 executions|0 recorded executions/);
  assert.equal(tests.querySelectorAll(".meter").length, 0, "unattributed categories do not draw a zero-token meter");
  assert.equal(mixed.querySelectorAll(".meter").length, 1, "mixed usage is displayed once in its category");
  const panel = keyed(app.view, "data-overview", "mate-overview:exchange-1");
  assert.deepEqual(panel.querySelectorAll(".overview-stage").map(node => node.textContent), ["1. Research", "2. Edit code", "3. Run tests", "4. Edit code", "5. Run tests"]);
});

test("Mate overview works without a Crew performance blob and retains expanded category on refresh", async () => {
  const body = mateBody(workOverviewFixture());
  const app = await boot(body, "#/p/shop/mate");
  const exchange = keyed(app.view, "data-detail", "exchange:exchange-1");
  assert.equal(exchange.open, false);
  assert.match(exchange.querySelector("summary").textContent, /Research.*Edit code.*Run tests/);
  assert.equal(exchange.querySelector("summary").querySelectorAll("button").length, 0);
  exchange.open = true;
  const category = keyed(app.view, "data-detail", "mate-overview:exchange-1:category:test");
  category.open = true;
  category.querySelector("summary").focus();
  await app.flush();
  assert.match(category.textContent, /xcodebuild -only-testing:TripUITests/);
  assert.match(category.textContent, /exit 65 · failed/);
  assert.match(category.textContent, /rollout.jsonl:30/);
  assert.equal(category.querySelectorAll("script").length, 0);
  await app.refresh(body, true);
  assert.equal(keyed(app.view, "data-detail", "exchange:exchange-1").open, true);
  assert.equal(keyed(app.view, "data-detail", "mate-overview:exchange-1:category:test").open, true);
  assert.equal(app.doc.activeElement.getAttribute("data-focus"), "detail:mate-overview:exchange-1:category:test");
});

test("header names the harness from the ledger and adds the observed version only when recorded", async () => {
  const app = await boot();
  const chips = app.view.querySelector(".head-chips");
  assert.ok(chips, "harness chip rendered from ledger.harness");
  assert.match(chips.textContent, /codex/);
  assert.doesNotMatch(chips.textContent, /undefined|null/);
  const body = fixture();
  body.performance.runtime = { model: "m", effort: "high", harness_version: "0.157.1" };
  const versioned = await boot(body);
  assert.match(versioned.view.querySelector(".head-chips").textContent, /codex 0\.157\.1/);
  assert.match(versioned.view.querySelector(".head-chips").textContent, /m · high/);
});

test("the strip draws one block per segment of an older recording, in order, with the failed command and the open one marked", async () => {
  const app = await boot();
  const card = app.view.querySelector(".stages-card");
  assert.match(card.querySelector(".sc-aside").textContent, /^3 segments · stages not recorded · #3 in progress$/);
  const items = blocks(app.view);
  assert.deepEqual(items.map(b => b.getAttribute("data-kind")), ["research", "test", "wait"]);
  assert.deepEqual(items.map(b => b.getAttribute("data-stage")), ["1", "2", "3"]);
  assert.deepEqual(items.map(b => b.getAttribute("data-failed")), [null, "true", null], "the segment whose command exited 65");
  assert.deepEqual(items.map(b => b.getAttribute("data-open")), [null, null, "true"]);
  assert.match(items[1].getAttribute("title"), /^#2 Run tests \/ build · 420 tokens · 1 call · 10s · P1 \d\d:\d\d:\d\d · a native command failed$/);
  assert.match(items[2].getAttribute("title"), /in progress$/);
  assert.equal(items[1].getAttribute("style"), "flex-grow:420", "a block is as wide as its tokens");
  const band = keyed(app.view, "data-prompt", "prompt-1");
  assert.match(band.querySelector(".tl-prompt-tag").textContent, /^P1 \d\d:\d\d · 3 calls · 1.1k · 40s$/);
  assert.match(band.querySelector(".tl-prompt-text").textContent, /^<img src=x onerror=alert\(1\)> Run the build$/, "the prompt is quoted as text");
  assert.equal(band.querySelectorAll("img").length, 0);
  assert.deepEqual(card.querySelector(".tl-legend").querySelectorAll(".item").map(node => node.textContent),
    ["Research / inspect", "Run tests / build", "Wait / poll", "failed command", "in progress"]);
  assert.equal(app.view.querySelectorAll(".tl-drill").length, 0, "nothing is open below the strip until a block is clicked");
  assert.match(keyed(app.view, "data-focus", "finding:finding-poll").querySelector(".finding-kind").textContent, /repeat · info/);
});

test("the derived findings read rankings and loops without inventing numbers, and open their segments", async () => {
  const app = await boot();
  const list = app.view.querySelector(".finding-list");
  assert.deepEqual(list.querySelectorAll(".finding-card").map(card => card.getAttribute("data-finding")), ["expensive", "finding-poll", "repeats"],
    "the most expensive step, then the projector's top finding, then the repeats; three open");
  assert.equal(app.view.querySelectorAll(".more-findings").length, 0);
  const expensive = keyed(app.view, "data-finding", "expensive");
  assert.match(expensive.textContent, /Most expensive step: call #3 · 40 output \+ 20 thinking/);
  assert.match(expensive.textContent, /by output \+ thinking: call #3 · 10s · context after 390 · by context re-sent: Run TripUITests · 420/);
  assert.match(expensive.querySelector(".finding-kind").textContent, /cost · info/);
  const repeats = keyed(app.view, "data-finding", "repeats");
  assert.match(repeats.textContent, /Repeats: 1 chain · 3 polls/);
  assert.match(repeats.textContent, /2 polls saw no new output \(66\.7%\) · 1 process was polled/);
  expensive.click();
  await app.flush();
  const panel = app.view.querySelector("[data-evidence-panel]");
  assert.match(panel.textContent, /By output \+ thinking: Check TripUITests progress · call #3 · 40 output \+ 20 thinking · 10s · context after 390 · P1 \d\d:\d\d/);
  assert.match(panel.textContent, /Segment with most output: Check TripUITests progress · 60 output \+ thinking · 1 call · 20s/);
  assert.match(panel.textContent, /By context re-sent: Run TripUITests · 420 tokens · 1 call · 2 repeats · 10s/);
  assert.ok(app.doc.scrolledTo === panel, "scrolled to it");
  keyed(app.view, "data-focus", "answer-top-call").click();
  await app.flush();
  assert.deepEqual(openSegments(app.view), ["segment-poll"], "the top output call opens its own segment");
  keyed(app.view, "data-focus", "answer-top-context-segment").click();
  await app.flush();
  assert.deepEqual(openSegments(app.view), ["segment-test"]);
  repeats.click();
  await app.flush();
  const repeatPanel = app.view.querySelector("[data-evidence-panel]");
  assert.match(repeatPanel.textContent, /Poll calls: 1 · 390 tokens · 36\.4% of the Crew/);
  assert.match(repeatPanel.textContent, /Longest: TripUITests was polled 3 times · 3 polls · 2 unchanged/);
  assert.match(repeatPanel.textContent, /Same content re-read: 0 · same-error retries: 0 · repair loops: 0 · repeats inside segments: 5/);
  keyed(app.view, "data-focus", "answer-longest-chain").click();
  await app.flush();
  assert.equal(keyed(app.view, "data-finding", "finding-poll").getAttribute("aria-pressed"), "true", "the longest chain is the projector's finding");
});

test("unmeasured polling says so", async () => {
  const body = fixture();
  body.performance.loops = { measured: false, chains: 0, polls: 0, unchanged_polls: 0, progress_polls: 0, unknown_polls: 0, processes_polled: 0,
    poll_calls: 0, poll_tokens: null, repeated_reads: 2, repeated_errors: 1, repair_loops: 0, segment_repeats: 5,
    coverage: "Polling chains are not measurable for this harness (no native process identities); repeated reads and retries are still detected." };
  const app = await boot(body);
  const repeats = keyed(app.view, "data-finding", "repeats");
  assert.match(repeats.textContent, /Repeats: polling not measurable/);
  assert.doesNotMatch(repeats.textContent, /\d+ polls?\b/, "an unmeasured harness prints no poll count, not even 0");
  assert.match(repeats.textContent, /Same content re-read: 2 · same-error retries: 1 · repair loops: 0/);
  repeats.click();
  assert.match(app.view.querySelector("[data-evidence-panel]").textContent, /native process identities/);
  delete body.performance.loops;
  delete body.performance.top_output_call_ids;
  delete body.performance.top_output_segment_ids;
  delete body.performance.top_segment_ids;
  const older = await boot(body);
  assert.match(keyed(older.view, "data-finding", "repeats").textContent, /Repeats not recorded yet/);
  assert.match(keyed(older.view, "data-finding", "expensive").textContent, /not ranked yet/);
});

test("findings beyond the first three are one click away", async () => {
  const body = fixture();
  const p = body.performance;
  for (let i = 0; i < 4; i++) p.findings.push({ id: "slow-" + i, kind: "long_execution", title: "command " + i + " took 2 min", severity: "info", confidence: "observed", segment_ids: ["segment-test"], execution_ids: ["exec-test"] });
  p.top_finding_ids = ["finding-poll", "slow-0", "slow-1", "slow-2"];
  const app = await boot(body);
  assert.deepEqual(app.view.querySelector(".finding-list").querySelectorAll(".finding-card").map(card => card.getAttribute("data-finding")), ["expensive", "finding-poll", "slow-0"]);
  const more = keyed(app.view, "data-detail", "more-findings");
  assert.match(more.querySelector("summary").textContent, /Show 4 more findings/);
  assert.deepEqual(more.querySelectorAll(".finding-card").map(card => card.getAttribute("data-finding")), ["slow-1", "slow-2", "repeats", "slow-3"]);
  assert.match(app.view.querySelector(".findings").querySelector(".sc-aside").textContent, /5 findings recorded/);
});

test("an effort row filters the strip to its kind, and the segment drill-down keeps its counts", async () => {
  const app = await boot();
  const row = keyed(app.view, "data-focus", "effort:test");
  assert.equal(row.getAttribute("aria-pressed"), "false");
  row.click();
  assert.deepEqual(blocks(app.view).map(b => b.getAttribute("data-dim")), ["true", null, "true"]);
  assert.equal(keyed(app.view, "data-focus", "effort:test").getAttribute("aria-pressed"), "true");
  assert.match(app.view.querySelector(".effort").querySelector(".sc-aside").textContent, /strip filtered to Run tests \/ build/);
  keyed(app.view, "data-focus", "effort:test").click();
  assert.deepEqual(blocks(app.view).map(b => b.getAttribute("data-dim")), [null, null, null]);
  keyed(app.view, "data-focus", "stage:segment-test").click();
  const drill = keyed(app.view, "data-drill", "segment-test");
  assert.match(drill.textContent, /#2Run tests \/ build/);
  assert.match(drill.textContent, /P1 \d\d:\d\d:\d\d/);
  assert.match(drill.textContent, /Calls1Tool calls1Tokens420Cache read400Work20Output10Thinking4/);
  const segment = keyed(app.view, "data-segment", "segment-test");
  assert.match(segment.textContent, /Tool calls2/);
  assert.match(segment.textContent, /Thinking4/);
});

test("tool mix rows add up to the crew row, under the fold", async () => {
  const app = await boot();
  const fold = keyed(app.view, "data-detail", "tool-mix");
  assert.equal(fold.open, false);
  const table = fold.querySelector(".tool-mix");
  assert.ok(table, "the tool mix table is rendered");
  assert.ok(table.parentNode.className.includes("table-wrap"));
  // The instructions category has executions but no attributed call, so it
  // gets no column: it would be dashes in every row.
  assert.deepEqual(table.querySelectorAll("th").map(node => node.textContent),
    ["Prompt", "Calls", "Tokens", "Run tests / build", "Wait / poll", "Research / inspect", "Repeats", "Failed cmds"]);
  const rows = table.querySelectorAll("tbody")[0].querySelectorAll("tr");
  assert.equal(rows.length, 2, "one prompt row and the crew row");
  assert.match(rows[0].textContent, /P1 · <img src=x onerror=alert…/, "the prompt is quoted as text, cut at 24 characters");
  assert.match(rows[0].textContent, /1 · 390/);
  assert.equal(rows[0].querySelectorAll("img").length, 0, "prompt markup remains text");
  const crew = rows[rows.length - 1];
  assert.deepEqual(crew.querySelectorAll("td").map(node => node.textContent), ["Crew", "3", "1.1k", "1 · 420", "1 · 390", "1 · 310", "5", "1"],
    "the crew row is the ledger: 3 calls, 1.1k tokens, 5 repeats, 1 failed native command");
  assert.match(fold.textContent, /Each model call belongs to exactly one work type/);
});

test("status lines and questions are folded, the diff stays a section", async () => {
  const body = fixture();
  body.status_lines = [{ at: at(5), text: "building" }];
  body.questions = [{ id: "q1", text: "Ship it?", asked_at: at(6) }];
  const app = await boot(body);
  assert.match(keyed(app.view, "data-detail", "status-lines").querySelector("summary").textContent, /Status lines \(1\)/);
  assert.match(keyed(app.view, "data-detail", "questions").querySelector("summary").textContent, /Questions \(1\)/);
  assert.ok(app.view.querySelectorAll("h2").some(node => node.textContent === "diff"));
});

test("Mate refreshes observed execution time after a quiet five-second poll without inventing usage", async () => {
  const body = { project: "shop", last_event_id: 1, mate: { harness: "claude", turns: 3, tokens: tokens(160, 900) },
    exchanges: [{ id: "active-exchange", source: "captain", prompt: "Run tests", model_calls: 3,
      started_at: at(0), prompt_at: at(0), tokens: tokens(160, 900), activities: [], overview: workOverviewFixture() }] };
  const app = await boot(body, "#/p/shop/mate");
  const key = "mate-overview:active-exchange:category:test";
  assert.match(keyed(app.view, "data-detail", key).querySelector("summary").textContent, /10s observed execution time/);
  const pageRequests = app.requests.filter(url => url === "/api/projects/shop/mate").length;
  const next = structuredClone(body);
  next.exchanges[0].overview.categories.find(category => category.kind === "test").elapsed_ms = 15000;
  await app.refresh(next);
  assert.equal(app.requests.filter(url => url === "/api/projects/shop/mate").length, pageRequests + 1);
  assert.ok(app.requests.filter(url => url.startsWith("/api/events")).every(url => url.includes("wait=5")));
  assert.match(keyed(app.view, "data-detail", key).querySelector("summary").textContent, /15s observed execution time/);
  assert.match(keyed(app.view, "data-detail", key).querySelector("summary").textContent, /usage not attributed/);
  assert.match(keyed(app.view, "data-detail", "mate-overview:active-exchange:category:mixed").querySelector("summary").textContent, /420 tokens/);
});

// The stages the projector folds a prompt's calls into, with the skill two
// loads of which the strip places. Stage tokens are the calls' own, so they
// add up to the prompt and to the task, which the header says.
const step = (kind, label, calls, executions, usage, extra = {}) => ({ kind, label, started_at: at(0), ended_at: at(10), elapsed_ms: 10000,
  model_calls: calls.length, executions, tokens: usage, call_ids: calls, segment_ids: [], skills: [], parts: [kind], open: false, ...extra });
const stepsFixture = () => {
  const body = fixture();
  const prompt = body.performance.prompt_turns[0];
  prompt.overview.steps = [
    step("instructions", "Read instructions", ["call-1"], 1, tokens(100, 200), { skills: ["token-review"], segment_ids: ["segment-read"], tool_elapsed_ms: 1500 }),
    step("mixed", "Mixed activity", ["call-2"], 2, tokens(10, 400), { parts: ["research", "test"], segment_ids: ["segment-test"], tool_elapsed_ms: 165000 }),
    step("response", "Respond", ["call-3"], 0, tokens(50, 300, 40, 20), { elapsed_ms: null, open: true })
  ];
  // The three calls' own usage: 310 + 420 + 390.
  prompt.tokens = body.performance.tokens = tokens(160, 900, 60, 24);
  body.ledger.tokens = prompt.tokens;
  body.performance.skills = [
    { name: "token-review", count: 2, loads: [
      { at: at(1), prompt_id: "prompt-1", call_id: "call-1", execution_id: "exec-skill", via: "tool" },
      { at: at(2), prompt_id: "prompt-1", call_id: "", execution_id: "exec-unlinked", via: "read", path: ".claude/skills/token-review/SKILL.md" }] }
  ];
  return body;
};
const cells = row => row.querySelectorAll("[role]").filter(node => node.getAttribute("role") === "cell").map(node => node.textContent);
const pinsOf = node => node.querySelectorAll(".sc-pin").map(pin => pin.textContent);

test("the strip lists each stage in order with its type, a mixed stage names what it mixed, and an open stage says so", async () => {
  const app = await boot(stepsFixture());
  const card = app.view.querySelector(".stages-card");
  assert.ok(card, "the stages card is rendered");
  assert.match(card.querySelector(".sc-aside").textContent, /^3 stages · #3 in progress$/);
  const items = blocks(app.view);
  assert.deepEqual(items.map(b => b.getAttribute("data-kind")), ["instructions", "mixed", "response"]);
  assert.deepEqual(items.map(b => b.getAttribute("data-block")), ["stage-1", "stage-2", "stage-3"]);
  assert.match(items[0].getAttribute("title"), /^#1 Read instructions · 310 tokens · 1 call · 10s · P1 /);
  assert.match(items[1].getAttribute("title"), /^#2 Mixed activity \(Research \/ inspect \+ Run tests \/ build\) · 420 tokens/, "a mixed stage names what it mixed");
  assert.match(items[2].getAttribute("title"), /^#3 Respond · 390 tokens · 1 call · \? · P1 [^·]+ · in progress$/, "unknown time is ? rather than 0");
  assert.equal(items[2].getAttribute("data-open"), "true");
  assert.deepEqual(items.map(pinsOf), [["1"], [], []], "the skill load rides on the stage of its call");
  assert.equal(app.view.querySelector(".tl").getAttribute("data-pins"), "true");
  assert.doesNotMatch(card.textContent, /xcodebuild|rollout\.jsonl/, "the strip names work types, never commands or files");
  keyed(app.view, "data-focus", "stage:stage-2").click();
  const drill = keyed(app.view, "data-drill", "stage-2");
  assert.match(drill.textContent, /#2Mixed activityResearch \/ inspect \+ Run tests \/ build/);
  assert.match(drill.textContent, /Calls1Tool calls2Tokens420Cache read400Work20/);
  assert.match(drill.textContent, /1 work segment/);
  assert.deepEqual(openSegments(app.view), ["segment-test"], "a stage opens the work segments behind it");
  assert.match(keyed(app.view, "data-segment", "segment-test").textContent, /exit 65/);
  keyed(app.view, "data-focus", "stage:stage-3").click();
  assert.match(keyed(app.view, "data-drill", "stage-3").textContent, /Time\?/);
  assert.match(keyed(app.view, "data-drill", "stage-3").textContent, /Model calls in this block \(1\)/, "a stage without a segment lists its calls");
});

test("the skills band places each skill load on its stage, says how it was loaded, and opens the stage", async () => {
  const app = await boot(stepsFixture());
  const skills = keyed(app.view, "data-skills", "true");
  assert.equal(skills.querySelector(".sc-aside").textContent, "1 skill · 2 loads");
  const row = keyed(skills, "data-skill", "token-review");
  assert.deepEqual(pinsOf(row), ["1"]);
  assert.match(row.textContent, /token-review×2/);
  assert.match(row.querySelector(".sc-skill-where").textContent, /^skill tool and read SKILL\.md · loaded in/);
  assert.deepEqual(row.querySelectorAll(".sc-load").map(chip => chip.textContent), ["#1Read instructions", "stage not linked"],
    "a load without a call link is listed and not given a stage");
  assert.match(skills.textContent, /does not show that the skill was followed/);
  keyed(app.view, "data-focus", "skill-load:token-review:1").click();
  assert.ok(keyed(app.view, "data-drill", "stage-1"));
});

test("the strip says what is not recorded instead of drawing zeros", async () => {
  const older = await boot();
  const card = older.view.querySelector(".stages-card");
  assert.match(card.textContent, /Skills not recorded yet/);
  assert.match(card.querySelector(".sc-aside").textContent, /stages not recorded/);
  assert.deepEqual(older.view.querySelector(".effort").querySelectorAll(".sc-effort").map(row => row.getAttribute("data-effort")), ["research", "test", "wait", "total"],
    "an older recording sums its segments");

  const body = stepsFixture();
  body.performance.skills = [];
  const none = await boot(body);
  assert.match(keyed(none.view, "data-skills", "true").textContent, /No skill was loaded in this recording/);

  const empty = fixture();
  empty.performance.segments = [];
  empty.performance.prompt_turns = [];
  const nothing = await boot(empty);
  assert.match(nothing.view.querySelector(".stages-card").textContent, /No prompt has been recorded yet/);
  assert.equal(nothing.view.querySelectorAll(".tl-stage").length, 0);
  assert.equal(nothing.view.querySelectorAll(".sc-effort").length, 0, "no effort table of zeros");
});

test("a long task keeps every stage on the strip", async () => {
  const body = stepsFixture();
  body.performance.prompt_turns[0].overview.steps = Array.from({ length: 30 }, (_, i) =>
    step(i % 2 ? "test" : "edit_code", i % 2 ? "Run tests / build" : "Edit code / files", ["call-" + i], 1, tokens(10, 20)));
  const app = await boot(body);
  assert.equal(blocks(app.view).length, 30);
  assert.equal(blocks(app.view)[29].getAttribute("data-stage"), "30");
});

test("the effort table is summed from the stages and its total matches the header", async () => {
  const app = await boot(stepsFixture());
  const header = keyed(app.view, "data-kpi", "tokens").querySelector(".kpi-value").textContent;
  const total = cells(keyed(app.view, "data-effort", "total"));
  // 1120 tokens, 900 of them the context re-read (cache), 220 added (work).
  assert.deepEqual(total, ["Total", "3", "3", "2m46s", "1.1k", "900", "220", "", "100%"]);
  assert.equal(total[4], header, "tokens agree with the header");
  // Time is how long the kind's commands ran: a kind that ran none is a dash.
  assert.deepEqual(cells(keyed(app.view, "data-effort", "instructions")), ["Read instructions", "1", "1", "1.5s", "310", "200", "110", "", "27.7%"]);
  assert.deepEqual(cells(keyed(app.view, "data-effort", "response")), ["Respond", "1", "–", "–", "390", "300", "90", "", "34.8%"]);
  const track = keyed(app.view, "data-effort", "response").querySelector(".sc-track");
  assert.equal(track.children.length, 2, "work solid and cache read faded");
  assert.equal(track.children[1].getAttribute("class"), "sc-cache");
  assert.equal(app.view.querySelectorAll(".coverage-note").filter(note => /not in a recorded stage/.test(note.textContent)).length, 0);
});

test("only the kinds with a call are listed, in a fixed order, mixed as one row naming what it mixed", async () => {
  const body = stepsFixture();
  body.performance.prompt_turns[0].overview.steps.push(
    step("mixed", "Mixed activity", ["call-4"], 1, tokens(5, 100), { parts: ["edit_code", "test"] }),
    step("mixed", "Mixed activity", ["call-5"], 1, tokens(5, 100), { parts: ["research", "test"] }));
  const app = await boot(body);
  const rows = app.view.querySelector(".effort").querySelectorAll(".sc-effort").filter(row => row.getAttribute("data-effort") !== "total");
  assert.deepEqual(rows.map(row => row.getAttribute("data-effort")), ["instructions", "response", "mixed"]);
  const mixed = rows[2];
  assert.equal(mixed.getAttribute("data-parts"), "edit_code+test · research+test");
  assert.deepEqual(cells(mixed).slice(0, 7), ["Mixed activityEdit code / files + Run tests / build · Research / inspect + Run tests / build", "3", "4", "2m45s", "650", "600", "50"]);
});

test("stages are numbered continuously across prompts, each prompt a band, the gap between them idle", async () => {
  const body = stepsFixture();
  body.performance.prompt_turns[0].ended_at = at(40);
  body.performance.prompt_turns.push({ id: "prompt-2", prompt: "Fix the build and rerun it", prompt_at: at(50), started_at: at(50), ended_at: at(55),
    elapsed_ms: 5000, model_calls: 2, tokens: tokens(20, 40), segment_ids: [], overview: { categories: [], sequence: [], steps: [
      step("edit_code", "Edit code / files", ["call-4"], 1, tokens(10, 20)),
      step("test", "Run tests / build", ["call-5"], 1, tokens(10, 20))] } });
  const app = await boot(body);
  const strip = app.view.querySelector(".tl");
  assert.deepEqual(strip.querySelectorAll(".tl-prompt").map(band => band.getAttribute("data-prompt")), ["prompt-1", "prompt-2"]);
  assert.deepEqual(blocks(app.view).map(b => b.getAttribute("data-stage")), ["1", "2", "3", "4", "5"]);
  const second = keyed(app.view, "data-prompt", "prompt-2");
  assert.match(second.querySelector(".tl-prompt-tag").textContent, /^P2 \d\d:\d\d · 2 calls · 70 · 5s$/);
  assert.equal(second.querySelector(".tl-prompt-text").textContent, "Fix the build and rerun it");
  assert.equal(second.querySelectorAll(".tl-stage").length, 2);
  assert.deepEqual(strip.querySelectorAll(".tl-idle").map(node => node.textContent), ["idle 10s"]);
  assert.match(app.view.querySelector(".stages-card").querySelector(".sc-aside").textContent, /5 stages/);
  keyed(app.view, "data-focus", "prompt:prompt-2").click();
  assert.equal(keyed(app.view, "data-prompt", "prompt-2").getAttribute("data-selected"), "true");
  assert.equal(keyed(app.view, "data-prompt", "prompt-1").getAttribute("data-selected"), "false");
});

test("a skill pin carries the skill's place in the skills list, on every stage that loaded it", async () => {
  const body = stepsFixture();
  body.performance.skills = [
    { name: "alpha", count: 2, loads: [{ call_id: "call-1", via: "tool" }, { call_id: "call-3", via: "tool" }] },
    { name: "beta", count: 1, loads: [{ call_id: "call-2", via: "read" }] }
  ];
  const app = await boot(body);
  assert.deepEqual(blocks(app.view).map(pinsOf), [["1"], ["2"], ["1"]]);
  assert.deepEqual(keyed(app.view, "data-skills", "true").querySelectorAll("li").map(pinsOf), [["1"], ["2"]]);
  assert.deepEqual(keyed(app.view, "data-skill", "alpha").querySelectorAll(".sc-load").map(chip => chip.textContent), ["#1Read instructions", "#3Respond"]);
  assert.match(keyed(app.view, "data-skill", "beta").textContent, /read SKILL\.md · loaded in#2Mixed activity/);
  assert.ok(app.view.querySelector(".tl-legend").querySelectorAll(".item").some(item => /skill load/.test(item.textContent)));
});

test("the header links to Mate Office, opened on the crew this page shows", async () => {
  const app = await boot();
  assert.equal(app.officeLink.getAttribute("href"), "office/#crew/shop/test-build");
});

test("a /usage suffix opens the task tier on Trace and scrolls to the header once, keeping the plain crumb", async () => {
  const app = await boot(fixture(), "#/p/shop/t/test-build/usage");
  assert.match(app.view.textContent, /Check TripUITests progress/, "the task tier renders on its Trace tab");
  assert.ok(app.doc.scrolledTo === app.view.querySelector(".task-kpis"), "scrolled to it");
  const crumb = app.doc.root.children.find(node => node.children.length && node.querySelectorAll("a").some(a => a.textContent === "test-build"));
  assert.equal(crumb.querySelectorAll("a").pop().getAttribute("href"), "#/p/shop/t/test-build", "breadcrumbs stay plain");
  app.doc.scrolledTo = null;
  await app.refresh(fixture(), true);
  assert.ok(app.doc.scrolledTo === null, "later polls do not scroll again", "scrolled to it");

  const plain = await boot();
  assert.equal(plain.doc.scrolledTo, undefined, "old URLs do not scroll");
});
