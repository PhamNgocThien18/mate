// ui_testdom.cjs - the browser-free harness the admin UI tests share: a
// small DOM with only the primitives app.js and task.js use, a crew fixture,
// and boot(), which loads the page into a node vm against a fake fetch.
// Layout and accessibility still need the real-browser acceptance check.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

class Node {
  constructor(tag, owner) {
    this.tagName = tag;
    this.owner = owner;
    this.children = [];
    this.attrs = {};
    this.listeners = {};
    this.parentNode = null;
    this._text = "";
    this._open = false;
  }
  set textContent(value) {
    this._text = String(value);
    for (const child of this.children) child.parentNode = null;
    this.children = [];
  }
  get textContent() { return this._text + this.children.map(child => child.textContent).join(""); }
  set className(value) { this.attrs.class = value; }
  get className() { return this.attrs.class || ""; }
  set open(value) {
    if (this._open === value) return;
    this._open = value;
    queueMicrotask(() => this.emit("toggle"));
  }
  get open() { return this._open; }
  get isConnected() { return this === this.owner.root || !!(this.parentNode && this.parentNode.isConnected); }
  setAttribute(key, value) { this.attrs[key] = String(value); if (key === "open") this.open = true; }
  getAttribute(key) { return this.attrs[key] == null ? null : this.attrs[key]; }
  appendChild(child) {
    if (child.parentNode) child.parentNode.children = child.parentNode.children.filter(node => node !== child);
    child.parentNode = this;
    this.children.push(child);
    return child;
  }
  addEventListener(name, fn) { (this.listeners[name] ||= []).push(fn); }
  emit(name) { for (const fn of this.listeners[name] || []) fn({ target: this }); }
  click() { this.emit("click"); }
  focus() { this.owner.activeElement = this; }
  scrollIntoView() { this.owner.scrolledTo = this; }
  matches(selector) {
    if (selector[0] === ".") return this.className.split(" ").includes(selector.slice(1));
    const attr = /^\[([^=\]]+)(?:="([^"]*)")?\]$/.exec(selector);
    if (attr) return attr[2] == null ? this.getAttribute(attr[1]) != null : this.getAttribute(attr[1]) === attr[2];
    return this.tagName === selector;
  }
  querySelectorAll(selector) {
    const matches = [];
    for (const child of this.children) {
      if (child.matches(selector)) matches.push(child);
      matches.push(...child.querySelectorAll(selector));
    }
    return matches;
  }
  querySelector(selector) { return this.querySelectorAll(selector)[0] || null; }
}

// Output and thinking default to small fixed values; a call that must rank
// first by output + thinking passes its own.
const tokens = (input, cache = 0, output = 10, thinking = 4) => ({ input, cache_read: cache, cache_write: 0, output, thinking, total: input + cache + output });
const at = seconds => new Date(Date.UTC(2026, 8, 28, 10, 0, seconds)).toISOString();
// One category per work kind, tokens additive because each call belongs to
// exactly one of them. `instructions` has an execution but no attributed
// call, so its tokens are null rather than 0.
const baseOverview = coverage => ({
  summary: "Run tests / build (1 call) · Wait / poll (1 call) · Research / inspect (1 call) · Read instructions (1 execution)",
  rule: "prompt-work-v1/observed-operations", coverage, sequence: [],
  categories: [
    { kind: "wait", label: "Wait / poll", model_calls: 1, execution_count: 1, tokens: tokens(50, 300, 40, 20), elapsed_ms: null,
      call_ids: ["call-3"], segment_ids: ["segment-poll"], execution_ids: ["exec-poll"], evidence: [] },
    { kind: "test", label: "Run tests / build", model_calls: 1, execution_count: 1, tokens: tokens(10, 400), elapsed_ms: 10000,
      call_ids: ["call-2"], segment_ids: ["segment-test"], execution_ids: ["exec-test"], evidence: [] },
    { kind: "research", label: "Research / inspect", model_calls: 1, execution_count: 0, tokens: tokens(100, 200), elapsed_ms: null,
      call_ids: ["call-1"], segment_ids: ["segment-read"], execution_ids: [], evidence: [] },
    { kind: "instructions", label: "Read instructions", model_calls: 0, execution_count: 1, tokens: null, elapsed_ms: 200,
      call_ids: [], segment_ids: [], execution_ids: [], evidence: [] }
  ]
});
// Segment kinds use the work vocabulary the projector shares with prompt
// overviews (docs/plans/2026-09-28-crew-observer-first-screen.md section 1):
// the old `read` and `poll` kinds are `research` and `wait` on the wire.
const fixture = () => ({
  project: "shop", crew: "test-build", generated_at: at(40), last_event_id: 1,
  ledger: { text: "Investigate build", state: "at_desk_working", turns: 3, tokens: tokens(160, 900), tool_count: 4, harness: "codex" },
  branch: { name: "mate/test-build", exists: true }, status_lines: [], questions: [],
  turns: [
    { id: "call-1", ordinal: 1, started_at: at(0), ended_at: at(10), duration_ms: 10000, tokens: tokens(100, 200), outcome: "tool", tool_count: 1, context_tokens_after: 300 },
    { id: "call-2", ordinal: 2, started_at: at(10), ended_at: at(20), duration_ms: 10000, tokens: tokens(10, 400), outcome: "tool", tool_count: 2, context_tokens_after: 410 },
    { id: "call-3", ordinal: 3, started_at: at(20), ended_at: at(30), duration_ms: 10000, tokens: tokens(50, 300, 40, 20), outcome: "tool", tool_count: 1, context_tokens_after: 390 }
  ],
  performance: {
    version: "v1", generated_at: at(40), tokens: tokens(160, 900), model_calls: 3,
    current_segment_id: "segment-poll", top_segment_ids: ["segment-test"], top_finding_ids: ["finding-poll"],
    top_output_segment_ids: ["segment-poll", "segment-read", "segment-test"], top_output_call_ids: ["call-3", "call-1", "call-2"],
    recent_window_ms: 300000, recent_tokens: tokens(160, 900),
    time: { prompt_elapsed_ms: 40000, tool_elapsed_ms: 20000, invocation_ms: 25000, unallocated_ms: 20000, decision_wait_ms: 0 },
    freshness: { last_observed_at: at(40), last_ingested_at: at(40), last_usage_at: at(30), stale: false,
      capabilities: ["native execution status"], missing: ["One child process has no response link"] },
    prompt_turns: [{ id: "prompt-1", prompt: "<img src=x onerror=alert(1)> Run the build", prompt_at: at(0), started_at: at(0),
      elapsed_ms: 40000, model_calls: 3, tokens: tokens(160, 900), segment_ids: ["segment-read", "segment-test", "segment-poll"], coverage: "native",
      overview: baseOverview("Types inferred from recorded operations.") }],
    segments: [
      { id: "segment-read", prompt_id: "prompt-1", label: "Read build config", kind: "research", started_at: at(0), ended_at: at(10), elapsed_ms: 10000,
        tokens: tokens(100, 200), model_calls: 1, call_ids: ["call-1"], execution_ids: [], repeat_count: 0, outcome: "completed" },
      { id: "segment-test", prompt_id: "prompt-1", label: "Run TripUITests", kind: "test", started_at: at(10), ended_at: at(20), elapsed_ms: 10000,
        tokens: tokens(10, 400), model_calls: 1, call_ids: ["call-2"], execution_ids: ["exec-wrapper", "exec-test"], repeat_count: 2, outcome: "failed" },
      { id: "segment-poll", prompt_id: "prompt-1", label: "Check TripUITests progress", kind: "wait", started_at: at(20), elapsed_ms: 20000,
        tokens: tokens(50, 300, 40, 20), model_calls: 1, call_ids: ["call-3"], execution_ids: ["exec-poll"], repeat_count: 3, outcome: "running" }
    ],
    // The crew-level overview is the prompt overviews summed by the projector
    // (one prompt here, so the same categories with a crew coverage line).
    overview: baseOverview("Types inferred from recorded operations across 1 prompts."),
    // One chain of three polls on process 77, two without new output; the
    // finding counters are zero because nothing repeated a read or an error.
    loops: { measured: true, chains: 1, polls: 3, unchanged_polls: 2, progress_polls: 1, unknown_polls: 0, processes_polled: 1,
      poll_calls: 1, poll_tokens: tokens(50, 300, 40, 20), repeated_reads: 0, repeated_errors: 0, repair_loops: 0, segment_repeats: 5,
      coverage: "Polling chains need native process identities; this harness reports them." },
    findings: [{ id: "finding-poll", kind: "process_polling", title: "TripUITests was polled 3 times", detail: "2 polls had no new output",
      severity: "info", confidence: "observed", ongoing: true, count: 3, tokens: null, segment_ids: ["segment-poll"], call_ids: ["call-3"], execution_ids: ["exec-poll"],
      evidence: [{ id: "source-1", kind: "execution", label: "Process 77", source_ref: { path: "rollout.jsonl", offset: 400 } }], review: "Review the wait interval" }],
    executions: [
      { id: "exec-wrapper", prompt_id: "prompt-1", tool: "exec", is_wrapper: true, status: "completed", output_bytes: null, new_output_bytes: null },
      { id: "exec-test", prompt_id: "prompt-1", tool: "CommandExecution", command: "xcodebuild -only-testing:TripUITests", process_id: "77",
        started_at: at(10), ended_at: at(20), duration_ms: 10000, status: "failed", exit_code: 65, output_bytes: 82, new_output_bytes: null,
        error_signature: "Simulator unavailable", output_excerpt: "<script>alert('unsafe')</script> simulator unavailable", source_ref: { path: "rollout.jsonl", offset: 200 } },
      { id: "exec-poll", prompt_id: "prompt-1", tool: "write_stdin", poll: true, process_id: "77", started_at: at(20), status: "running",
        output_bytes: 0, new_output_bytes: 0, duration_ms: null, source_ref: { path: "rollout.jsonl", offset: 400 } }
    ],
    processes: [{ id: "process-77", command: "xcodebuild -only-testing:TripUITests", execution_ids: ["exec-test"], poll_ids: ["exec-poll"],
      polls: 3, unchanged_polls: 2, progress_polls: 1, unknown_polls: 0, tokens: null }]
  }
});

// boot loads the page at `hash` with `body` as every API answer. With
// `opts.tab` it then opens that tab of the task page, for tests about what
// a tab shows rather than about which tab opens.
async function boot(body = fixture(), hash = "#/p/shop/t/test-build", opts = {}) {
  const doc = { activeElement: null };
  doc.root = new Node("body", doc);
  const ids = {};
  for (const id of ["view", "crumbs", "to-office", "live"]) { ids[id] = new Node("div", doc); doc.root.appendChild(ids[id]); }
  const liveText = new Node("span", doc); liveText.className = "text"; ids.live.appendChild(liveText);
  doc.getElementById = id => ids[id];
  doc.createElement = tag => new Node(tag, doc);
  doc.createElementNS = (_, tag) => new Node(tag, doc);
  doc.createTextNode = text => { const node = new Node("#text", doc); node.textContent = text; return node; };
  const requests = [], pendingEvents = [], intervals = [];
  let current = body, now = Date.parse(at(40));
  class Clock extends Date { static now() { return now; } }
  const window = { scrollY: 260, addEventListener() {}, scrollTo(_, y) { this.scrollY = y; } };
  const context = vm.createContext({ document: doc, window, location: { hash },
    Date: Clock, setTimeout, setInterval: fn => intervals.push(fn),
    fetch(url) {
      requests.push(url);
      if (url.startsWith("/api/events")) return new Promise(resolve => pendingEvents.push(resolve));
      return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(current) });
    }
  });
  for (const file of ["humanize.js", "task.js", "app.js"]) vm.runInContext(fs.readFileSync(path.join(__dirname, "ui", file), "utf8"), context);
  const flush = () => new Promise(resolve => setImmediate(resolve));
  await flush();
  if (opts.tab) {
    const button = ids.view.querySelectorAll("[data-tab]").find(node => node.getAttribute("data-tab") === opts.tab);
    if (button) { button.click(); await flush(); }
  }
  return { doc, view: ids.view, officeLink: ids["to-office"], window, requests, intervals, flush,
    async refresh(next = current, changed = false) {
      current = next;
      const reply = pendingEvents.shift();
      assert.ok(reply, "one long poll is outstanding");
      reply({ ok: true, status: 200, json: () => Promise.resolve({ last_event_id: 1, events: changed ? [{}] : [], now: [] }) });
      await flush();
    },
    advance(ms) { now += ms; intervals.forEach(fn => fn()); }
  };
}

const keyed = (view, attribute, value) => view.querySelectorAll(`[${attribute}]`).find(node => node.getAttribute(attribute) === value);

module.exports = { Node, tokens, at, baseOverview, fixture, boot, keyed };
