// Behaviour checks for Agent City's panel (ui/city/app.js), written from
// the rules rather than from the code. Today they cover the Mate's work
// board (CON-158): the Duty tab and its badge, the groups and row states,
// the tick with its undo, the link from a waiting row to its topic, and
// that nothing on the board counts as a need (CON-154).
//
// Run with: node --test internal/dashboard/ui_city_test.cjs
// TestCityBehaviour in ui_city_test.go runs it as part of `go test`
// whenever node is on PATH.
//
// app.js is an ES module that draws with three.js. Here its imports are
// cut and every drawing object is a stub that accepts anything, so only
// the panel - plain DOM built with h() - is exercised. The DOM below
// implements only the primitives app.js uses; layout, colour and the city
// itself are checked in a real browser (docs/dashboard.md section 12).
const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

// ------------------------------------------------------------ a small DOM

class Node {
  constructor(tag, doc) {
    this.tagName = tag;
    this.doc = doc;
    this.children = [];
    this.attrs = {};
    this.listeners = {};
    this.parentNode = null;
    this._text = "";
    this.style = {};
    this.dataset = {};
    this.scrollTop = 0;
    this.hidden = false;
    this.disabled = false;
    this.checked = false;
    this.value = "";
    const self = this;
    this.classList = {
      add(...cs) { self.attrs.class = [...new Set([...self.classes, ...cs])].join(" "); },
      remove(...cs) { self.attrs.class = self.classes.filter(c => !cs.includes(c)).join(" "); },
      toggle(c, on) { (on == null ? !self.classes.includes(c) : on) ? this.add(c) : this.remove(c); },
      contains(c) { return self.classes.includes(c); }
    };
  }
  set textContent(v) { this._text = String(v); this.children.forEach(c => { c.parentNode = null; }); this.children = []; }
  get textContent() { return this._text + this.children.map(c => c.textContent).join(""); }
  get id() { return this.attrs.id || ""; }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return Object.prototype.hasOwnProperty.call(this.attrs, k) ? this.attrs[k] : null; }
  removeAttribute(k) { delete this.attrs[k]; }
  hasAttribute(k) { return Object.prototype.hasOwnProperty.call(this.attrs, k); }
  set className(v) { this.attrs.class = v; }
  get className() { return this.attrs.class || ""; }
  get classes() { return this.className.split(/\s+/).filter(Boolean); }
  get isConnected() { return this === this.doc.body || !!(this.parentNode && this.parentNode.isConnected); }
  appendChild(c) { if (c.parentNode) c.parentNode.removeChild(c); c.parentNode = this; this.children.push(c); return c; }
  append(...kids) { for (const k of kids) this.appendChild(k instanceof Node ? k : this.doc.createTextNode(String(k))); }
  replaceChildren(...kids) { this.textContent = ""; this.append(...kids); }
  removeChild(c) { this.children = this.children.filter(x => x !== c); c.parentNode = null; return c; }
  remove() { if (this.parentNode) this.parentNode.removeChild(this); }
  replaceWith(n) { const p = this.parentNode; if (!p) return; const i = p.children.indexOf(this); if (n.parentNode) n.parentNode.removeChild(n); n.parentNode = p; p.children[i] = n; this.parentNode = null; }
  addEventListener(name, fn) { (this.listeners[name] ||= []).push(fn); }
  dispatch(name, extra) {
    const ev = Object.assign({ type: name, target: this, stopped: false, stopPropagation() { this.stopped = true; }, preventDefault() {} }, extra);
    for (let n = this; n && !ev.stopped; n = n.parentNode) for (const fn of n.listeners[name] || []) fn(ev);
    return ev;
  }
  click() { return this.dispatch("click"); }
  focus() { this.doc.activeElement = this; }
  closest(sel) { for (let n = this; n; n = n.parentNode) if (n.tagName !== "#text" && n.matches(sel)) return n; return null; }
  matchesCompound(sel) {
    if (sel === ":focus") return this.doc.activeElement === this;
    const re = /([#.]?[\w-]+|\[[^\]]+\])/g;
    let m;
    while ((m = re.exec(sel))) {
      const t = m[1];
      if (t[0] === "#") { if (this.attrs.id !== t.slice(1)) return false; }
      else if (t[0] === ".") { if (!this.classes.includes(t.slice(1))) return false; }
      else if (t[0] === "[") {
        const a = /^\[([^=\]]+)(?:="?([^"\]]*)"?)?\]$/.exec(t);
        if (a[2] == null ? !this.hasAttribute(a[1]) : this.getAttribute(a[1]) !== a[2]) return false;
      } else if (this.tagName !== t) return false;
    }
    return true;
  }
  matches(sel) {
    return sel.split(",").some(one => {
      const parts = one.trim().split(/\s+(?![^\[]*\])/);
      if (!this.matchesCompound(parts[parts.length - 1])) return false;
      let n = this.parentNode;
      for (let i = parts.length - 2; i >= 0; i--) {
        while (n && !n.matchesCompound(parts[i])) n = n.parentNode;
        if (!n) return false;
        n = n.parentNode;
      }
      return true;
    });
  }
  querySelectorAll(sel) {
    const out = [];
    const walk = n => { for (const c of n.children) { if (c.tagName !== "#text" && c.matches(sel)) out.push(c); walk(c); } };
    walk(this);
    return out;
  }
  querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
  get text() { return this.textContent.replace(/\s+/g, " ").trim(); }
}

// anything is a value that takes any use: a three.js class, an instance,
// a method, a vector. Drawing is not under test here.
function anything() {
  return new Proxy(function () {}, {
    get(_, k) {
      if (k === Symbol.toPrimitive) return () => 0;
      if (k === Symbol.iterator) return function* () {};
      if (k === "then") return undefined;
      return anything();
    },
    set() { return true; },
    apply() { return anything(); },
    construct() { return anything(); },
  });
}

// ------------------------------------------------------------ the fixture

// The workspace, pages and news are Mate Office's busy fixture; the duty
// board is this file's own, in the shape GET /api/duty answers.
const OFFICE = path.join(__dirname, "testdata", "office", "busy.json");
const office = () => JSON.parse(fs.readFileSync(OFFICE, "utf8"));
const at = (m) => `2026-10-03T14:${String(m).padStart(2, "0")}:07.000000000Z`;
const dutyBoard = () => ({
  generated_at: at(32), last_event_id: 900,
  projects: [
    { project: "shop", logged: true, duty: [
      { id: 1, kind: "assigned", title: "Fold make shot into the pre-push hook", status: "done", last: "PR #232 merged", added_at: at(0), at: at(30), done_at: at(30),
        notes: [{ at: at(0), op: "add", text: "asked in chat" }, { at: at(30), op: "done", text: "PR #232 merged" }] },
      { id: 2, kind: "assigned", title: "Raise the xcode lane to 6", status: "waiting", topic: 14, last: "4 or 6 lanes?", added_at: at(1), at: at(25),
        notes: [{ at: at(1), op: "add" }, { at: at(25), op: "wait", topic: 14, text: "4 or 6 lanes?" }] },
      { id: 3, kind: "proposed", title: "Sweep cold DerivedData", status: "working", last: "launchd job in hu-259", added_at: at(2), at: at(20),
        notes: [{ at: at(2), op: "add" }, { at: at(20), op: "progress", text: "launchd job in hu-259" }] },
      { id: 4, kind: "assigned", title: "Stop crews pulling main", status: "okayed", added_at: at(0), at: at(6), done_at: at(5), okayed_at: at(6),
        notes: [{ at: at(0), op: "add" }, { at: at(5), op: "done" }, { at: at(6), op: "okay" }] }
    ] },
    { project: "docs-site", logged: true, duty: [] },
    { project: "infra", logged: false, duty: [] }
  ]
});
const buttons = (project, readOnly) => readOnly
  ? { project, read_only: readOnly, mate: [], crews: {}, mate_more: [], crew_more: {} }
  : { project, mate: [], crews: {}, mate_more: [{ action: "say", label: "Message…", available: true }, { action: "okay", label: "OK", available: true }], crew_more: {} };

// boot loads chat.js and app.js (its imports cut, their modules stubbed)
// into a fresh context against a fake fetch that serves the fixture.
async function boot(opts = {}) {
  const fx = office();
  // News 14 is the topic duty row 2 waits on.
  fx.news.projects[0].news.unshift({ id: 14, title: "Lane count", question: "Four lanes or six?", options: [], context: [], status: "waiting", opened_at: at(25), at: at(25), notes: [] });
  const duty = opts.duty || dutyBoard();
  const doc = { activeElement: null };
  doc.body = new Node("body", doc);
  doc.documentElement = new Node("html", doc);
  const el = (tag, id, cls, ...kids) => { const n = new Node(tag, doc); if (id) n.setAttribute("id", id); if (cls) n.className = cls; kids.forEach(k => n.appendChild(k)); return n; };
  const g = () => el("span", "", "g");
  const dock = el("aside", "dock", "", el("button", "grab"), el("div", "", "dhead", el("div", "crumbs", "crumbs"), el("h2", "dock-title"), el("div", "dock-sub", "sub"), el("button", "close")), el("div", "dbody", "dbody"));
  for (const n of [el("canvas", "c"), el("button", "needs", "chip", g(), el("span", "needs-n")), el("button", "goto", "chip"), el("button", "sys", "chip", g(), el("span", "sys-t")),
    el("span", "live", "chip live", g(), el("span", "", "t")), el("p", "boot"), el("p", "hidden"), el("nav", "rail"), el("div", "pins"), dock,
    el("div", "scrub", "", el("button", "play"), el("button", "speed"), el("div", "ticks"), el("input", "seek"), el("span", "when"), el("button", "golive")), el("div", "scrim"), el("div", "tip"), el("p", "hint")]) doc.body.appendChild(n);
  doc.querySelector = sel => doc.body.querySelector(sel);
  doc.getElementById = id => doc.body.querySelector("#" + id);
  doc.createElement = tag => new Node(tag, doc);
  doc.createTextNode = text => { const n = new Node("#text", doc); n._text = String(text); return n; };

  const requests = [], pendingEvents = [], intervals = new Map(), winListeners = {};
  let timer = 0;
  const context = vm.createContext({
    document: doc, Node, console, innerWidth: 1280, devicePixelRatio: 1,
    location: { hash: "", pathname: "/city/", search: "" }, history: { replaceState() {} },
    performance: { now: () => 0, mark() {} },
    addEventListener(name, fn) { (winListeners[name] ||= []).push(fn); },
    ResizeObserver: class { observe() {} },
    // A second passes only when a test says so (tick); timeouts never fire.
    setTimeout() { return 0; }, clearTimeout() {},
    setInterval(fn) { intervals.set(++timer, fn); return timer; }, clearInterval(id) { intervals.delete(id); },
    fetch(url, init) {
      requests.push({ url, method: (init && init.method) || "GET", body: init && init.body ? JSON.parse(init.body) : null });
      const answer = (status, body) => Promise.resolve({ ok: status < 300, status, json: () => Promise.resolve(JSON.parse(JSON.stringify(body))) });
      if (init && init.method === "POST") {
        const res = opts.press ? opts.press(JSON.parse(init.body)) : { status: 404, body: { error: "not found " + url } };
        return answer(res.status || 200, res.body);
      }
      if (url.startsWith("/api/events")) return new Promise(resolve => pendingEvents.push(resolve));
      const u = new URL(url, "http://city.test");
      const p = decodeURIComponent(u.pathname);
      let m, body = null;
      if (p === "/api/workspace") body = fx.workspace;
      else if (p === "/api/news") body = fx.news;
      else if (p === "/api/duty") body = duty;
      else if (p === "/api/board") body = { days: 7, projects: [] };
      else if (p === "/api/plugins") body = { apps: [] };
      else if (p === "/api/actions") body = { read_only: opts.readOnly || undefined, projects: Object.fromEntries(Object.keys(fx.projects).map(n => [n, buttons(n, opts.readOnly)])) };
      else if ((m = /^\/api\/projects\/([^/]+)\/chat$/.exec(p))) body = { project: m[1], headless: true, items: [], state: null };
      else if ((m = /^\/api\/projects\/([^/]+)$/.exec(p))) body = fx.projects[m[1]];
      if (!body) return answer(404, { error: "not found " + p });
      return answer(200, body);
    }
  });
  vm.runInContext("Date.now = function () { return " + fx.now_ms + "; };", context);
  const stubs = `
    const THREE = anything(), OrbitControls = anything(), PALETTE = anything(), HARNESSES = {};
    const lights = () => {};
    const harnessName = (x) => (x ? x[0].toUpperCase() + x.slice(1) : 'Agent');
    const formatTokens = (x) => String(x);
    const createTentRoom = () => anything();
    const readHistory = async () => ({ since: '', tasks: [], events: [] });
    const createCity = () => ({ group: anything(), size: 3, hidden: 0, mascots: new Map(), tents: new Map(), blocks: new Map(), hall: [anything()], seats: new Map(),
      setMate() {}, setCrews() {}, setMerged() {}, setHall() {}, setBoard() {}, setMailbox() {}, update() {}, dispose() {} });
  `;
  context.anything = anything;
  vm.runInContext(fs.readFileSync(path.join(__dirname, "ui", "chat.js"), "utf8"), context, { filename: "chat.js" });
  const app = fs.readFileSync(path.join(__dirname, "ui", "city", "app.js"), "utf8").replace(/^import [^\n]*\n/gm, "");
  vm.runInContext(stubs + app, context, { filename: "app.js" });
  const flush = async () => { for (let i = 0; i < 8; i++) await new Promise(r => setImmediate(r)); };
  await flush();
  const dbody = doc.getElementById("dbody");
  const api = {
    doc, dbody, requests, flush,
    async click(node) { assert.ok(node, "the thing to click exists"); node.click(); await flush(); },
    // tick: one second of every countdown.
    async tick(n = 1) { for (let i = 0; i < n; i++) for (const fn of [...intervals.values()]) fn(); await flush(); },
    async answerEvents() { pendingEvents.shift()({ ok: true, status: 200, json: () => Promise.resolve({ last_event_id: 900, events: [], now: [] }) }); await flush(); },
    // openMate: the Mate of `project` through Go to, then its `tab`.
    async openMate(project, tab) {
      await api.click(doc.getElementById("goto"));
      await api.click(dbody.querySelectorAll("li").find(li => li.querySelector("b") && li.querySelector("b").text === `Mate · ${project}`));
      if (tab) await api.click(dbody.querySelector(`#tab-${tab}`));
    },
    row: (id) => dbody.querySelectorAll("li").find(li => li.dataset.duty === String(id)),
    groups: () => dbody.querySelectorAll(".group-h").map(n => n.text),
    title: () => doc.getElementById("dock-title").text,
    sub: () => doc.getElementById("dock-sub").text,
    needs: () => doc.getElementById("needs-n").text,
    rail: () => doc.getElementById("rail").querySelectorAll("button").map(b => b.text)
  };
  return api;
}

// ------------------------------------------------------------ the checks

test("the Mate's panel has a Duty tab after Chat, its badge the rows waiting for an OK", async () => {
  const c = await boot();
  await c.openMate("shop");
  const tabs = c.dbody.querySelectorAll("[role=tab]").map(b => b.text);
  assert.deepEqual(tabs.slice(0, 3), ["Chat", "Duty1", "Inbox1"], "Chat · Duty · Inbox, the Duty badge counting the one done row");
  assert.equal(c.sub(), "Claude · plaza of shop · duty 3 · 1 for you", "the dock's sub line carries the board's count: open rows, and how many wait for you");
  await c.openMate("docs-site");
  assert.equal(c.dbody.querySelector("#tab-duty").text, "Duty", "no badge when nothing waits for an OK");
  assert.equal(c.sub(), "Codex · plaza of docs-site", "and nothing on the sub line");
});

test("the board groups rows by where they stand, each row in its state", async () => {
  const c = await boot();
  await c.openMate("shop", "duty");
  assert.deepEqual(c.groups(), ["For you to OK · 1", "In progress · 1", "Waiting on a topic · 1", "Okayed · 1"]);
  const done = c.row(1), working = c.row(3), waiting = c.row(2), okayed = c.row(4);
  const box = done.querySelector("input[type=checkbox]");
  assert.ok(box, "a done row carries a real checkbox");
  assert.equal(box.getAttribute("aria-label"), "OK: Fold make shot into the pre-push hook");
  assert.equal(box.disabled, false, "the checkbox is live");
  assert.ok(done.text.includes("assigned · PR #232 merged"), "a row's second line says who put it there and the Mate's last word");
  assert.ok(working.classes.includes("k-run") && working.querySelector(".g").text === "▶", "a working row runs");
  assert.ok(!working.querySelector("input"), "a working row has no checkbox");
  assert.equal(working.querySelector(".tag").text, "proposed", "what the Mate proposed is tagged");
  assert.ok(waiting.classes.includes("k-topic") && waiting.querySelector(".g").text === "?", "a waiting row asks");
  assert.equal(waiting.querySelector(".side button").text, "topic #14 ›", "and links to its topic");
  assert.ok(okayed.classes.includes("okayed") && okayed.classes.includes("quiet") && okayed.querySelector(".g").text === "✓", "an okayed row is struck and quiet");
  assert.ok(!c.row(1).querySelector(".notes"), "a row's history is folded");
  await c.click(c.row(1).querySelector("b"));
  assert.deepEqual(c.row(1).querySelectorAll(".notes li").map(n => n.text.replace(/^.*? · /, "")), ["add · asked in chat", "done · PR #232 merged"], "a click on the row opens its history");
});

test("the board says when there is nothing, when the Mate keeps none, and when it cannot be read", async () => {
  const empty = await boot();
  await empty.openMate("docs-site", "duty");
  assert.equal(empty.dbody.querySelector(".note").text, "Nothing on the duty board yet.");
  await empty.openMate("infra", "duty");
  assert.equal(empty.dbody.querySelector(".note").text, "This Mate has put nothing on its duty board.");
  const broken = await boot({ duty: { projects: [{ project: "shop", logged: true, error: "duty.jsonl: permission denied", duty: [] }] } });
  await broken.openMate("shop", "duty");
  assert.equal(broken.dbody.querySelector(".note").text, "The board could not be read: duty.jsonl: permission denied");
});

test("ticking a done row waits five seconds for an undo, then okays it", async () => {
  const presses = [];
  const c = await boot({ press: body => { presses.push(body); return { body: { line: "okayed #1" } }; } });
  await c.openMate("shop", "duty");
  const box = c.row(1).querySelector("input[type=checkbox]");
  box.checked = true;
  box.dispatch("change");
  await c.flush();
  let row = c.row(1);
  assert.ok(row.text.includes("Okaying in 5s"), "the row counts down");
  assert.ok(row.querySelector("input[type=checkbox]").disabled, "the checkbox is held while it counts");
  await c.tick(2);
  row = c.row(1);
  assert.ok(row.text.includes("Okaying in 3s"), "a second at a time");
  assert.equal(presses.length, 0, "nothing is sent while the undo is open");
  await c.click(row.querySelectorAll("button").find(b => b.text === "Undo"));
  row = c.row(1);
  assert.ok(!row.text.includes("Okaying"), "undo puts the row back");
  assert.equal(row.querySelector("input[type=checkbox]").disabled, false);
  assert.equal(presses.length, 0, "and nothing was sent");

  const again = c.row(1).querySelector("input[type=checkbox]");
  again.checked = true;
  again.dispatch("change");
  await c.flush();
  await c.tick(5);
  assert.deepEqual(presses, [{ action: "okay", project: "shop", duty: 1 }], "after the countdown the tick is one okay on the row");
  assert.ok(c.requests.filter(r => r.url === "/api/duty").length >= 2, "and the board is read again");
  assert.ok(c.dbody.querySelector(".result") && c.dbody.querySelector(".result").text.includes("okayed #1"), "the server's line shows on the board");
});

test("a read-only dashboard shows the rows but holds the tick", async () => {
  const c = await boot({ readOnly: "this dashboard is read-only: actions run in the dashboard `mate console` serves" });
  await c.openMate("shop", "duty");
  assert.ok(c.row(1).querySelector("input[type=checkbox]").disabled, "the checkbox cannot be ticked");
  assert.ok(c.dbody.querySelector(".band.ro"), "and the band says why");
});

test("a waiting row's topic button opens that topic in City Hall", async () => {
  const c = await boot();
  await c.openMate("shop", "duty");
  await c.click(c.row(2).querySelector(".side button"));
  assert.equal(c.title(), "Lane count", "the topic the row waits on");
  assert.ok(c.sub().startsWith("shop · #14"), "by its number");
  assert.equal(c.requests.filter(r => r.method === "POST").length, 0, "opening a topic sends nothing");
});

test("nothing on the board is a need: the rail counts the topics only", async () => {
  const c = await boot();
  assert.equal(c.needs(), "Needs you · 3", "shop's three waiting news (4, 3 and 14), nothing from the board");
  for (const b of c.rail()) assert.ok(!/make shot|DerivedData|xcode lane|pulling main/.test(b), `the rail names no duty row: ${b}`);
  await c.answerEvents();
  assert.equal(c.needs(), "Needs you · 3", "and a quiet poll, which reads the board again, changes nothing");
  assert.ok(c.requests.filter(r => r.url === "/api/duty").length >= 2, "the board is read on every poll answer");
});

test("okayed rows fold after ten, Show all opens the rest", async () => {
  const board = dutyBoard();
  const shop = board.projects[0];
  shop.duty = shop.duty.filter(r => r.status !== "okayed");
  for (let i = 1; i <= 12; i++) shop.duty.push({ id: 100 + i, kind: "assigned", title: `old ${i}`, status: "okayed", added_at: at(0), at: at(0), okayed_at: at(0), notes: [] });
  const c = await boot({ duty: board });
  await c.openMate("shop", "duty");
  assert.equal(c.dbody.querySelectorAll("li.okayed").length, 10);
  const more = c.dbody.querySelectorAll("button").find(b => b.text === "Show all 12");
  await c.click(more);
  assert.equal(c.dbody.querySelectorAll("li.okayed").length, 12);
});
