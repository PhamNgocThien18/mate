// Behaviour checks for Mate Office (ui/office/), written from the design
// boards and the dashboard's rules rather than from the code: the three
// drawers open from what they belong to, a stuck crew reads as stuck, an
// unknown value reads as unknown, workspace text stays text, and the page
// asks for nothing but GETs on its own origin.
//
// Run with: node --test internal/dashboard/ui_office_test.cjs
// TestOfficeBehaviour in ui_office_test.go runs it as part of `go test`
// whenever node is on PATH.
//
// The DOM below implements only the primitives office.js uses. Layout,
// colour and the drawings are checked in a real browser (docs/dashboard.md
// section 11), not here.
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
    this.scrollTop = 0;
  }
  set textContent(v) { this._text = String(v); this.children.forEach(c => { c.parentNode = null; }); this.children = []; }
  get textContent() { return this._text + this.children.map(c => c.textContent).join(""); }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return Object.prototype.hasOwnProperty.call(this.attrs, k) ? this.attrs[k] : null; }
  removeAttribute(k) { delete this.attrs[k]; }
  hasAttribute(k) { return Object.prototype.hasOwnProperty.call(this.attrs, k); }
  get className() { return this.attrs.class || ""; }
  get classes() { return this.className.split(/\s+/).filter(Boolean); }
  appendChild(c) { if (c.parentNode) c.parentNode.removeChild(c); c.parentNode = this; this.children.push(c); return c; }
  insertBefore(c, ref) {
    if (c.parentNode) c.parentNode.removeChild(c);
    const i = ref ? this.children.indexOf(ref) : -1;
    c.parentNode = this;
    if (i < 0) this.children.push(c); else this.children.splice(i, 0, c);
    return c;
  }
  removeChild(c) { this.children = this.children.filter(x => x !== c); c.parentNode = null; return c; }
  replaceChild(n, old) { const i = this.children.indexOf(old); if (n.parentNode) n.parentNode.removeChild(n); n.parentNode = this; this.children[i] = n; old.parentNode = null; return old; }
  get firstChild() { return this.children[0] || null; }
  addEventListener(name, fn) { (this.listeners[name] ||= []).push(fn); }
  dispatch(name, extra) {
    const ev = Object.assign({ type: name, target: this, stopped: false, stopPropagation() { this.stopped = true; } }, extra);
    for (let n = this; n && !ev.stopped; n = n.parentNode) for (const fn of n.listeners[name] || []) fn(ev);
    return ev;
  }
  click() { return this.dispatch("click"); }
  focus() { this.doc.activeElement = this; }
  matchesCompound(sel) {
    const re = /([#.]?[\w-]+|\[[^\]]+\])/g;
    let m;
    while ((m = re.exec(sel))) {
      const t = m[1];
      if (t[0] === "#") { if (this.attrs.id !== t.slice(1)) return false; }
      else if (t[0] === ".") { if (!this.classes.includes(t.slice(1))) return false; }
      else if (t[0] === "[") {
        const a = /^\[([^=\]]+)(?:="([^"]*)")?\]$/.exec(t);
        if (a[2] == null ? !this.hasAttribute(a[1]) : this.getAttribute(a[1]) !== a[2]) return false;
      } else if (this.tagName !== t) return false;
    }
    return true;
  }
  matches(sel) {
    const parts = sel.trim().split(/\s+(?![^\[]*\])/);
    if (!this.matchesCompound(parts[parts.length - 1])) return false;
    let n = this.parentNode;
    for (let i = parts.length - 2; i >= 0; i--) {
      while (n && !n.matchesCompound(parts[i])) n = n.parentNode;
      if (!n) return false;
      n = n.parentNode;
    }
    return true;
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

// ------------------------------------------------------------ the fixture

const FIXTURE = path.join(__dirname, "testdata", "office", "busy.json");
const fixture = () => JSON.parse(fs.readFileSync(FIXTURE, "utf8"));

// boot loads humanize.js and office.js into a fresh context the way the page
// does, against a fake fetch that serves the fixture.
async function boot(opts = {}) {
  const fx = opts.fixture || fixture();
  const doc = { activeElement: null };
  doc.body = new Node("body", doc);
  doc.documentElement = new Node("html", doc);
  const root = new Node("div", doc);
  root.setAttribute("id", "office");
  doc.body.appendChild(root);
  doc.getElementById = id => (id === "office" ? root : null);
  doc.createElement = tag => new Node(tag, doc);
  doc.createElementNS = (_, tag) => new Node(tag, doc);
  doc.createTextNode = text => { const n = new Node("#text", doc); n._text = String(text); return n; };

  const requests = [];
  const pendingEvents = [];
  const winListeners = {};
  let hash = opts.hash || "";
  const window = {
    scrollY: 0,
    scrollTo() {},
    addEventListener(name, fn) { (winListeners[name] ||= []).push(fn); },
    matchMedia: q => ({ matches: q.includes("max-width") ? !!opts.phone : false, addEventListener() {} }),
    localStorage: { getItem() { return null; }, setItem() {} },
    onhashchange: null
  };
  window.location = {
    get hash() { return hash; },
    set hash(v) {
      const next = v === "#" ? "" : v;
      if (next === hash) return;
      hash = next;
      setImmediate(() => (winListeners.hashchange || []).forEach(fn => fn({})));
    }
  };
  const fail = opts.fail || {};
  const context = vm.createContext({
    // Timers never fire: a retry after a failed poll must not keep node up.
    document: doc, window, setTimeout() { return 0; }, setInterval() { return 0; }, console,
    fetch(url, init) {
      requests.push({ url, method: init && init.method, headers: init && init.headers, body: init && init.body });
      if (init && init.method === "POST") {
        const res = url === "/api/actions" && opts.press ? opts.press(JSON.parse(init.body)) : { status: 404, body: { error: "not found " + url } };
        const answer = r => { const status = r.status || 200; return { ok: status < 300, status, json: () => Promise.resolve(r.body) }; };
        // A press may answer later: a promise stands for an action still running.
        return res && typeof res.then === "function" ? res.then(answer) : Promise.resolve(answer(res));
      }
      if (url.startsWith("/api/events")) {
        if (opts.eventsFail) return Promise.reject(new TypeError("Failed to fetch"));
        return new Promise(resolve => pendingEvents.push(resolve));
      }
      const u = new URL(url, "http://office.test");
      const p = decodeURIComponent(u.pathname);
      let body = null;
      if (fail[p]) return Promise.resolve({ ok: false, status: 500, json: () => Promise.resolve({ error: "the timeline could not be read", reason: fail[p] }) });
      let m;
      if (p === "/api/workspace") body = fx.workspace;
      else if (p === "/api/plugins") {
        const project = u.searchParams.get("project");
        body = (fx.plugins || {})[project] || { project, apps: [] };
      }
      else if (p === "/api/now") body = fx.now;
      else if (p === "/api/news") body = fx.news || { projects: [] };
      else if (p === "/api/board") body = boardBody(Number(u.searchParams.get("days")));
      else if (p === "/api/board/day") body = boardDayBody(u.searchParams.get("date"));
      else if ((m = /^\/api\/projects\/([^/]+)\/actions$/.exec(p))) body = (opts.actions || {})[m[1]] ||
        { project: m[1], read_only: "this dashboard is read-only: actions run in the dashboard `mate console` serves", mate: [], crews: {} };
      else if ((m = /^\/api\/projects\/([^/]+)\/tasks\/([^/]+)\/diff$/.exec(p))) body = { project: m[1], crew: m[2], branch: "mate/" + m[2], exists: true, text: "diff --git a/x b/x\n+one line\n" };
      else if ((m = /^\/api\/projects\/([^/]+)\/tasks\/([^/]+)$/.exec(p))) body = fx.tasks[m[1] + "/" + m[2]] || taskBody(fx, m[1], m[2]);
      else if ((m = /^\/api\/projects\/([^/]+)\/mate$/.exec(p))) body = fx.mates[m[1]] || { project: m[1], exchanges: [] };
      else if ((m = /^\/api\/projects\/([^/]+)$/.exec(p))) body = fx.projects[m[1]];
      if (!body) return Promise.resolve({ ok: false, status: 404, json: () => Promise.resolve({ error: "not found " + p }) });
      return Promise.resolve({ ok: true, status: 200, json: () => Promise.resolve(JSON.parse(JSON.stringify(body))) });
    }
  });
  // The fixture's own clock: ages read "23m" because it is 14:32:07 there.
  vm.runInContext("Date.now = function () { return " + fx.now_ms + "; };", context);
  for (const file of ["humanize.js", path.join("office", "office.js")]) {
    vm.runInContext(fs.readFileSync(path.join(__dirname, "ui", file), "utf8"), context, { filename: file });
  }
  const flush = async () => { for (let i = 0; i < 8; i++) await new Promise(r => setImmediate(r)); };
  await flush();
  return {
    doc, root, requests, flush, fx,
    // answerEvents ends the open /api/events long poll with body.
    async answerEvents(body) {
      pendingEvents.shift()({ ok: true, status: 200, json: () => Promise.resolve(body) });
      await flush();
    },
    drawer: () => root.querySelector("#drawer"),
    key(name) { (winListeners.keydown || []).forEach(fn => fn({ key: name })); },
    hash: () => hash,
    async click(node) { assert.ok(node, "the thing to click exists"); node.click(); await flush(); }
  };
}

function taskBody(fx, project, crew) {
  const t = fx.projects[project].tasks.find(x => x.crew === crew);
  return { project, crew, ledger: t, turns: [], status_lines: [], questions: [], branch: { name: t.branch, exists: true },
    performance: { skills: [], segments: [], prompt_turns: [] } };
}

// boardBody is /api/board for the fixture's day (2026-10-03, +07:00): shop
// works every day, docs-site only today, and the total is their sum.
function boardBody(days) {
  const dates = [];
  for (let i = days - 1; i >= 0; i--) dates.push(new Date(Date.UTC(2026, 9, 3 - i)).toISOString().slice(0, 10));
  const day = (date, o) => Object.assign({ date, tokens: { input: 0, cache_read: 0, cache_write: 0, output: 0, thinking: 0, total: 0 },
    mate_tokens: 0, crew_tokens: 0, spawned: {}, closed: {}, reviews: {}, incidents: {} }, o);
  const toks = n => ({ input: 0, cache_read: n, cache_write: 0, output: 0, thinking: 0, total: n });
  const shop = dates.map((date, i) => i === days - 1
    ? day(date, { tokens: toks(3000000), mate_tokens: 1000000, crew_tokens: 2000000, spawned: { ship: 2, review: 1 },
        closed: { finished: 3, discarded: 1 }, reviews: { pass: 1, "changes-requested": 2 }, incidents: { stale: 1, budget: 2 } })
    : day(date, { tokens: toks(2000000), mate_tokens: 500000, crew_tokens: 1500000, spawned: { ship: 1 },
        closed: { finished: 2 }, reviews: { "changes-requested": 1 }, incidents: { stale: 1 } }));
  const docs = dates.map((date, i) => i === days - 1
    ? day(date, { tokens: toks(500000), crew_tokens: 500000, spawned: { scout: 1 }, closed: { didnt_start: 1 } })
    : day(date));
  const total = dates.map((date, i) => {
    const a = shop[i], b = docs[i], sum = (x, y) => { const o = Object.assign({}, x); for (const k in y) o[k] = (o[k] || 0) + y[k]; return o; };
    return day(date, { tokens: toks(a.tokens.total + b.tokens.total), mate_tokens: a.mate_tokens + b.mate_tokens, crew_tokens: a.crew_tokens + b.crew_tokens,
      spawned: sum(a.spawned, b.spawned), closed: sum(a.closed, b.closed), reviews: sum(a.reviews, b.reviews), incidents: sum(a.incidents, b.incidents) });
  });
  return { generated_at: "2026-10-03T14:32:07.000000000Z", last_event_id: 1, days, zone: "+07:00", dates, total,
    projects: [{ name: "shop", days: shop }, { name: "docs-site", days: docs }, { name: "infra", days: dates.map(d => day(d)) }] };
}

// boardDayBody is /api/board/day: today has a shop ship opened and
// finished, a docs-site scout that never started, the shop Mate and an
// open budget incident; every other day only the shop Mate.
function boardDayBody(date) {
  const toks = (cr, out) => ({ input: 1000, cache_read: cr, cache_write: 0, output: out, thinking: 0, total: 1000 + cr + out });
  const base = { generated_at: "2026-10-03T14:32:07.000000000Z", last_event_id: 1, date, zone: "+07:00" };
  if (date !== "2026-10-03") {
    return Object.assign(base, { actors: [{ project: "shop", kind: "mate", name: "", turns: 3, tokens: toks(499000, 0) }], tasks: [], incidents: [] });
  }
  const task = (o) => Object.assign({ state: "", closed: true, turns: 4, harness: "claude", model: "opus", effort: "high",
    tokens: toks(1900000, 9000), opened: true, closed_today: true, day_turns: 4, day_tokens: toks(1900000, 9000) }, o);
  return Object.assign(base, {
    actors: [
      { project: "shop", kind: "crew", name: "login-flow", turns: 4, tokens: toks(1900000, 9000) },
      { project: "shop", kind: "mate", name: "", turns: 9, tokens: toks(990000, 9000) },
      { project: "docs-site", kind: "crew", name: "seo", turns: 1, tokens: toks(499000, 0) }
    ],
    tasks: [
      task({ project: "shop", crew: "login-flow", text: "Fix the checkout total", role: "ship", close_state: "finished",
        spawned_at: "2026-10-03T02:00:00.000000000Z", closed_at: "2026-10-03T05:30:00.000000000Z" }),
      task({ project: "docs-site", crew: "seo", text: "Why is the build slow", role: "scout", close_state: "failed", failed_reason: "herdr refused",
        spawned_at: "2026-10-03T06:00:00.000000000Z", closed_at: "2026-10-03T06:01:00.000000000Z", turns: 1,
        tokens: toks(499000, 0), day_turns: 1, day_tokens: toks(499000, 0) })
    ],
    incidents: [
      { project: "shop", kind: "stale", actor_kind: "crew", name: "login-flow", opened_at: "2026-10-03T03:00:00.000000000Z", resolved_at: "2026-10-03T03:12:00.000000000Z" },
      { project: "shop", kind: "budget", actor_kind: "mate", name: "", opened_at: "2026-10-03T07:00:00.000000000Z" }
    ]
  });
}

const byKey = (root, key) => root.querySelector(`[data-key="${key}"]`);
const deskOf = (root, crew) => root.querySelectorAll(".desk").find(n => n.querySelector(".id").text === crew);
const roomOf = (root, name) => root.querySelector(`section.room[data-room="${name}"]`);

// ------------------------------------------------------------ the office

test("every project is a room: its Mate at the big desk, open crews at desks, finished ones in the cabinet", async () => {
  const app = await boot();
  const rooms = app.root.querySelectorAll("section.room");
  assert.deepEqual(rooms.map(r => r.getAttribute("data-room")), ["shop", "docs-site", "infra"]);
  const shop = roomOf(app.root, "shop");
  assert.deepEqual(shop.querySelectorAll(".desk").map(d => d.querySelector(".id").text), ["rd1", "api-fix", "ui-polish", "db-migrate"]);
  assert.match(shop.querySelector(".mate").text, /Mate · shop.*deciding.*Claude.*1\.8M today/);
  assert.equal(shop.querySelector(".cab .cnt").text, "4", "four finished tasks are filed, not seated");
  assert.match(shop.querySelector(".plate").text, /shop.*running.*2 waiting/);
  assert.equal(roomOf(app.root, "docs-site").querySelector(".light").text, "running",
    "an idle Mate with crews at work is a project at work");
  assert.equal(shop.querySelector(".plate .ptok").text, "3.7M tokens", "the Mate plus every task, open and closed");
  assert.equal(roomOf(app.root, "docs-site").querySelector(".plate .ptok").text, "719k tokens");
  assert.equal(roomOf(app.root, "infra").querySelector(".plate .ptok").text, "725.3k tokens");
  const infra = roomOf(app.root, "infra");
  assert.ok(infra.classes.includes("r-stopped"), "a room whose Mate is not running has its lights off");
  assert.match(infra.text, /stopped/);
  assert.match(infra.text, /Lights off · project stopped/);
  assert.match(app.root.querySelector(".top").text, /Today 2\.6M tokens/);
});

test("a room where nobody is working says how long since anyone moved", async () => {
  const fx = fixture();
  // docs-site's Mate is idle; stop its busy crews. The last to move is
  // search, 20s ago, so that is how long the room has been quiet.
  const states = { nav: "waiting_review", i18n: "waiting_at_ceo", search: "idle" };
  fx.projects["docs-site"].tasks.forEach(t => { if (states[t.crew]) t.state = states[t.crew]; });
  const app = await boot({ fixture: fx });
  const light = roomOf(app.root, "docs-site").querySelector(".light");
  assert.equal(light.text, "idle 20s");
  assert.ok(light.classes.includes("idle"));
  assert.equal(roomOf(app.root, "shop").querySelector(".light").text, "running");
});

test("a crew with a question stands in the hallway, and its empty desk shows it went", async () => {
  const app = await boot();
  const figs = app.root.querySelectorAll(".hall .fig");
  assert.deepEqual(figs.map(f => f.querySelector(".id").text), ["ui-polish", "i18n"]);
  assert.match(figs[0].querySelector(".bubble").text, /needs an answer/);
  assert.ok(figs[1].classes.includes("walk"));
  assert.match(figs[1].text, /to the Mate/);
  const desk = deskOf(app.root, "ui-polish");
  assert.ok(desk.classes.includes("away"));
  assert.equal(desk.querySelector(".a-head"), null, "nobody sits at the desk of a crew at the door");
  assert.match(app.root.querySelector(".cap").text, /1 question waiting/);
});

test("a stuck crew looks stuck: alert, outlined desk, the word and why", async () => {
  const app = await boot();
  const desk = deskOf(app.root, "db-migrate");
  assert.ok(desk.classes.includes("s-blocked"));
  assert.ok(desk.querySelector(".a-alert"), "the red alert badge");
  const chip = desk.querySelector(".chip");
  assert.ok(chip.classes.includes("t-bad"));
  assert.equal(chip.text, "stuck");
  assert.equal(desk.querySelector(".quiet").text, "quiet too long · 23m");
  assert.match(desk.getAttribute("aria-label"), /stuck, quiet too long/);
});

test("clicking the Mate opens the Mate drawer for that room", async () => {
  const app = await boot();
  assert.ok(app.drawer().hasAttribute("hidden"), "no drawer before anything is picked");
  await app.click(roomOf(app.root, "shop").querySelector(".mate"));
  assert.equal(app.hash(), "#mate/shop");
  const d = app.drawer();
  assert.ok(!d.hasAttribute("hidden"));
  assert.equal(d.querySelector("h2").text, "shop");
  assert.match(d.querySelector(".role").text, /Mate · team lead.*Claude/);
  assert.match(d.querySelector(".dstate").text, /deciding.*since 14m/);
  assert.equal(d.querySelector(".big .n").text, "2.4M");
  assert.equal(d.querySelector(".big .c b").text, "$6.31");
  assert.deepEqual(d.querySelectorAll(".bl b").map(b => b.text), ["283.2k", "1.9M", "194.6k", "42.2k"]);
  assert.match(d.querySelector(".meter").text, /context 62% · 124k of 200k/);
  assert.match(d.querySelector(".tv").text, /1\.8M/);
  assert.match(d.text, /Thinking it over, facing api-fix/);
  assert.deepEqual(d.querySelectorAll(".cr .id").map(n => n.text), ["rd1", "api-fix", "ui-polish", "db-migrate"]);
  assert.ok(app.requests.some(r => r.url === "/api/projects/shop/mate"), "the drawer reads the Mate's own endpoint");
  assert.ok(roomOf(app.root, "shop").querySelector(".mate").classes.includes("sel"), "the picked Mate is ringed");
});

test("clicking a crew opens its drawer: state, usage, current activity and the task", async () => {
  const app = await boot();
  await app.click(deskOf(app.root, "db-migrate"));
  assert.equal(app.hash(), "#crew/shop/db-migrate");
  const d = app.drawer();
  assert.equal(d.querySelector("h2").text, "db-migrate");
  assert.match(d.querySelector(".role").text, /Crew · shop/);
  const chip = d.querySelector(".dstate .chip");
  assert.ok(chip.classes.includes("t-bad"));
  assert.equal(chip.text, "stuck, quiet too long");
  assert.equal(d.querySelector(".big .n").text, "310.4k");
  assert.match(d.querySelector(".meter").text, /context 88% · 176k of 200k/);
  assert.match(d.querySelector(".tv").text, /196k/);
  assert.equal(d.querySelectorAll(".spark i").length, 12, "twelve half hours");
  const activity = d.querySelector("[aria-label=\"Current activity\"]");
  assert.match(activity.text, /Running 0042_add_order_index/);
  assert.match(activity.text, /Run tests \/ build · running/);
  assert.match(activity.text, /db\/migrations\/0042_add_order_index\.sql/);
  const task = d.querySelector("[aria-label=\"Task\"]");
  assert.match(task.text, /Order indexes and backfill.*mate\/db-migrate.*57.*db-migrations ×2/);
  assert.equal(task.querySelector("a.link").getAttribute("href"), "../#/p/shop/t/db-migrate");
  assert.ok(app.requests.some(r => r.url === "/api/projects/shop/tasks/db-migrate"));
  const admin = app.root.querySelectorAll(".seg a").find(a => a.text === "Admin");
  assert.equal(admin.getAttribute("href"), "../#/p/shop/t/db-migrate", "Admin goes to the same crew's page");
});

test("picking a crew from the Mate drawer moves the drawer to that crew", async () => {
  const app = await boot({ hash: "#mate/shop" });
  const row = app.drawer().querySelectorAll(".cr").find(r => r.querySelector(".id").text === "rd1");
  await app.click(row);
  assert.equal(app.hash(), "#crew/shop/rd1");
  assert.equal(app.drawer().querySelector("h2").text, "rd1");
});

test("the filing cabinet lists finished tasks by drawer, with their final state", async () => {
  const app = await boot();
  await app.click(roomOf(app.root, "shop").querySelector(".cab"));
  assert.equal(app.hash(), "#cabinet/shop");
  const d = app.drawer();
  assert.equal(d.querySelector("h2").text, "Filing cabinet");
  assert.match(d.querySelector(".role").text, /shop · finished tasks/);
  assert.match(d.querySelector(".dstate").text, /4 finished.*oldest Sep 10/);
  assert.match(d.querySelector(".csum").text, /3.*1.*708\.9k tokens/);
  assert.deepEqual(d.querySelectorAll(".csum .cf").map(b => b.getAttribute("aria-label").split(":")[0]), ["3 merged", "1 discarded"]);
  const groups = d.querySelectorAll(".cg").map(g => [g.querySelector(".cgh b").text, g.querySelectorAll(".tn").map(n => n.text)]);
  assert.deepEqual(groups, [
    ["Top drawer", ["login-flow", "search-facets"]],
    ["Middle drawer", ["order-email"]],
    ["Bottom drawer", ["healthcheck"]]
  ]);
  const discarded = d.querySelectorAll(".tr").find(r => r.querySelector(".tn").text === "search-facets");
  assert.ok(!discarded.querySelector(".chip").classes.includes("t-bad"), "work thrown away on purpose is not an error");
  assert.match(discarded.querySelector(".chip").getAttribute("aria-label"), /^discarded/);
  assert.match(discarded.text, /2h 0m.*4d ago.*Sep 29.*133k/);
  assert.ok(roomOf(app.root, "shop").querySelector(".cab").classes.includes("open"), "its top drawer is pulled out");
});

test("the cabinet says what a review concluded and tells a start that never came up from discarded work", async () => {
  const fx = JSON.parse(fs.readFileSync(path.join(__dirname, "testdata/office/busy.json"), "utf8"));
  const closed = fx.projects.shop.tasks.filter(t => t.closed);
  // A reviewer is closed like any crew, `failed` after --discard included;
  // what it concluded is what the row says.
  Object.assign(closed[0], { review: "pass", merged_at: "" });
  Object.assign(closed[1], { review: "changes-requested" });
  Object.assign(closed[2], { review: "pending", merged_at: "", close_state: "finished" });
  Object.assign(closed[3], { review: "pending", close_state: "failed", merged_at: "", failed_reason: "herdr refused the launch" });
  const app = await boot({ fixture: fx, hash: "#cabinet/shop" });
  const rows = Object.fromEntries(app.drawer().querySelectorAll(".tr").map(r => [r.querySelector(".tn").text, r]));
  // The column is too narrow for "changes requested", so a row shows the
  // glyph and carries the word as its name and tooltip.
  const chipOf = crew => rows[crew].querySelector(".chip");
  const word = crew => chipOf(crew).getAttribute("aria-label").split(":")[0];
  const tone = crew => chipOf(crew).classes.find(c => c.startsWith("t-"));
  assert.deepEqual([word("login-flow"), tone("login-flow")], ["approved", "t-ok"]);
  assert.deepEqual([word("search-facets"), tone("search-facets")], ["changes requested", "t-wait"]);
  assert.deepEqual([word("order-email"), tone("order-email")], ["no verdict", "t-gone"]);
  assert.deepEqual([word("healthcheck"), tone("healthcheck")], ["didn't start", "t-bad"]);
  assert.equal(chipOf("healthcheck").getAttribute("data-tip"), "didn't start: herdr refused the launch");
  for (const crew of Object.keys(rows)) {
    assert.ok(chipOf(crew).classes.includes("ico"), crew + "'s final state is a glyph");
    assert.equal(chipOf(crew).text, "", crew + "'s final state has no text to overflow the column");
  }
  const glyphs = Object.keys(rows).map(crew => chipOf(crew).querySelector("path").getAttribute("d"));
  assert.equal(new Set(glyphs).size, glyphs.length, "each final state has its own glyph");
  const summary = app.drawer().querySelectorAll(".csum .cf");
  assert.deepEqual(summary.map(b => b.getAttribute("data-tip").split(":")[0]), ["1 approved", "1 changes requested", "1 no verdict", "1 didn't start"], "the summary names them on hover");
  assert.deepEqual(summary.map(b => b.text), ["1", "1", "1", "1"], "the summary is glyph and count only");
  assert.ok(!app.drawer().text.includes("failed"), "no row reads failed");
});

test("a crew wears a badge for each kind of agent it delegated to", async () => {
  const app = await boot({ hash: "#crew/shop/rd1" });
  const desk = deskOf(app.root, "rd1");
  assert.deepEqual(desk.querySelectorAll(".ag").map(b => b.text), ["fe-reviewer", "Explore"], "one badge per kind, first use first, as written");
  assert.equal(desk.querySelector(".ag").getAttribute("aria-label"), "delegated to the fe-reviewer agent");
  assert.equal(deskOf(app.root, "api-fix").querySelectorAll(".ag").length, 0, "a crew that delegated to none wears none");
  const kv = app.drawer().querySelector("[aria-label=\"Task\"] .kv");
  const dt = kv.children.findIndex(n => n.tagName === "dt" && n.text === "Agents");
  assert.ok(dt >= 0, "the crew drawer has an Agents line");
  assert.deepEqual(kv.children[dt + 1].querySelectorAll(".ag").map(b => b.text), ["fe-reviewer", "Explore"]);

  const other = await boot({ hash: "#crew/shop/api-fix" });
  const kv2 = other.drawer().querySelector("[aria-label=\"Task\"] .kv");
  const dt2 = kv2.children.findIndex(n => n.tagName === "dt" && n.text === "Agents");
  assert.equal(kv2.children[dt2 + 1].text, "none delegated to");

  const cab = await boot({ hash: "#cabinet/shop" });
  const row = cab.drawer().querySelectorAll(".tr").find(r => r.querySelector(".tn").text === "login-flow");
  assert.deepEqual(row.querySelectorAll(".ag").map(b => b.text), ["backend-reviewer"], "a cabinet row wears the badge too");
  const peek = roomOf(cab.root, "shop").querySelector(".cab .peek");
  assert.deepEqual(peek.querySelectorAll(".pk .ag").map(b => b.text), ["backend-reviewer"], "and so does the cabinet's peek");
});

test("every cabinet row and the cabinet's peek show what the task was for", async () => {
  const fx = JSON.parse(fs.readFileSync(path.join(__dirname, "testdata/office/busy.json"), "utf8"));
  const closed = fx.projects.shop.tasks.filter(t => t.closed);
  Object.assign(closed[0], { role: "ship" });
  Object.assign(closed[1], { role: "scout" });
  Object.assign(closed[2], { role: "review", review_of: "login-flow", review: "pass" });
  delete closed[3].role;
  const app = await boot({ fixture: fx, hash: "#cabinet/shop" });
  const rows = Object.fromEntries(app.drawer().querySelectorAll(".tr").map(r => [r.querySelector(".tn").text, r]));
  const label = crew => rows[crew].querySelector(".rl").getAttribute("aria-label");
  assert.match(label("login-flow"), /^coding/);
  assert.match(label("search-facets"), /^scout/);
  assert.equal(label("order-email"), "review of login-flow");
  assert.equal(label("healthcheck"), "role unknown", "a row without a role is not taken for a ship");
  const glyphs = Object.keys(rows).map(crew => rows[crew].querySelector(".rl path").getAttribute("d"));
  assert.equal(new Set(glyphs).size, glyphs.length, "each role has its own glyph");
  const peek = roomOf(app.root, "shop").querySelector(".cab .peek");
  assert.deepEqual(peek.querySelectorAll(".pk .rl").map(r => r.getAttribute("aria-label").split(/[: ]/)[0]), ["coding", "scout", "review"]);
});

test("pressing a summary state shows only its tasks, pressing it again shows them all", async () => {
  const app = await boot({ hash: "#cabinet/shop" });
  const crews = () => app.drawer().querySelectorAll(".tn").map(n => n.text);
  const filter = word => app.drawer().querySelectorAll(".csum .cf").find(b => b.getAttribute("aria-label").includes(" " + word));
  await app.click(filter("discarded"));
  assert.deepEqual(crews(), ["search-facets"]);
  assert.equal(filter("discarded").getAttribute("aria-pressed"), "true");
  assert.ok(filter("merged").classes.includes("dim"), "the other states are dimmed");
  assert.match(app.drawer().querySelector(".csum .tt").text, /133k tokens/, "the total is of the tasks shown");
  await app.click(filter("merged"));
  assert.deepEqual(crews(), ["login-flow", "order-email", "healthcheck"]);
  await app.click(filter("merged"));
  assert.deepEqual(crews(), ["login-flow", "search-facets", "order-email", "healthcheck"]);
  assert.equal(filter("merged").getAttribute("aria-pressed"), "false");
});

test("a task that ran under a minute, a day, or for an unknown time says so", async () => {
  const fx = JSON.parse(fs.readFileSync(path.join(__dirname, "testdata/office/busy.json"), "utf8"));
  const closed = fx.projects.shop.tasks.filter(t => t.closed);
  closed[0].age_ms = 42000;
  closed[1].age_ms = 26 * 3600000 + 5 * 60000;
  closed[2].age_ms = 0;
  const app = await boot({ fixture: fx, hash: "#cabinet/shop" });
  assert.deepEqual(app.drawer().querySelectorAll(".trun").map(n => n.text), ["42s", "1d 2h", "?", "2h 0m"]);
});

test("the cabinet names the harness, observed model and requested effort, retaining unknowns", async () => {
  const fx = JSON.parse(fs.readFileSync(path.join(__dirname, "testdata/office/busy.json"), "utf8"));
  const closed = fx.projects.shop.tasks.filter(t => t.closed);
  Object.assign(closed[0], { harness: "codex", model: "requested-model", last_model: "observed-model", effort: "high" });
  Object.assign(closed[1], { harness: "pi", model: "configured-model", last_model: "", effort: "low" });
  Object.assign(closed[2], { harness: "", model: "", last_model: "", effort: "" });
  const app = await boot({ fixture: fx, hash: "#cabinet/shop" });
  assert.deepEqual(app.drawer().querySelector(".colh").children.map(n => n.text),
    ["Task", "Harness / model / effort", "Final state", "Ran", "Closed", "Tokens"]);
  assert.ok(app.drawer().classes.includes("drawer-cabinet"));
  const agents = app.drawer().querySelectorAll(".ta");
  assert.equal(agents[0].text, "Codexobserved-modeleffort: high");
  assert.equal(agents[0].querySelector(".mono").getAttribute("title"), "Last observed model");
  assert.equal(agents[1].text, "Piconfigured-modeleffort: low");
  assert.equal(agents[1].querySelector(".mono").getAttribute("title"), "Requested model");
  assert.equal(agents[2].text, "harness ?model ?effort ?");
});

test("the Mate drawer lists the project's apps: status, an Open link only when configured, and where login is checked", async () => {
  const app = await boot({ hash: "#mate/shop" });
  assert.ok(app.requests.some(r => r.url === "/api/plugins?project=shop" && (r.method || "GET") === "GET"),
    "the drawer reads the project's apps");
  const block = app.drawer().querySelector("[aria-label=\"Apps\"]");
  assert.ok(block, "an Apps block in the Mate drawer");
  assert.equal(block.querySelector("h3").text, "Apps");
  assert.deepEqual(block.querySelectorAll("dt").map(n => n.text), ["Linear", "notes"]);
  const [linear, notes] = block.querySelectorAll("dd");

  assert.equal(linear.querySelector(".chip").text, "configured");
  const open = linear.querySelectorAll("a");
  assert.equal(open.length, 1);
  assert.equal(open[0].text, "Open");
  assert.equal(open[0].getAttribute("href"), "https://linear.app/acme/team/SHOP");
  assert.equal(open[0].getAttribute("target"), "_blank");
  assert.equal(open[0].getAttribute("rel"), "noopener");

  assert.equal(notes.querySelector(".chip").text, "configuration error");
  assert.ok(notes.querySelector(".chip").classes.includes("t-bad"));
  assert.equal(notes.querySelectorAll("a").length, 0, "no Open link for an app whose configuration is wrong");
  assert.equal(notes.querySelector(".err-note").text,
    "/ws/.mate/plugins.yaml: instance notes: package path /ws/<img src=x onerror=alert(1)> is not a directory");
  assert.equal(app.root.querySelectorAll("img").length, 0, "the message is text");

  assert.match(block.text, /Login and tool status: check \/mcp in the Mate's Claude pane/);
  assert.doesNotMatch(block.text, /connected|logged in|ready/i, "Office does not know whether an app is connected");
  assert.equal(app.drawer().querySelectorAll("form, button[type=\"submit\"]").length, 0);
});

test("on a harness that cannot take MCP servers only the app with one is unsupported", async () => {
  const fx = fixture();
  const refusal = "the codex harness cannot give a Mate MCP servers: only config parsing is proven";
  fx.plugins.shop.apps = [
    Object.assign({}, fx.plugins.shop.apps[0], { status: "harness unsupported", harness: "codex", error: refusal }),
    { instance: "board", name: "Board", status: "configured", harness: "codex",
      open: { title: "Open board", url: "https://board.example/shop" }, mcp: null, skills: [],
      error: "the Mate will not start while linear-work needs MCP servers: " + refusal }
  ];
  const app = await boot({ fixture: fx, hash: "#mate/shop" });
  const [linear, board] = app.drawer().querySelector("[aria-label=\"Apps\"]").querySelectorAll("dd");
  assert.equal(linear.querySelector(".chip").text, "harness unsupported");
  assert.equal(linear.querySelectorAll("a").length, 0, "no Open link for an unsupported app");
  assert.equal(linear.querySelector(".err-note").text, refusal);
  assert.equal(board.querySelector(".chip").text, "configured");
  assert.equal(board.querySelector("a").getAttribute("href"), "https://board.example/shop");
  assert.match(board.querySelector(".err-note").text, /the Mate will not start while linear-work needs MCP servers/);
});

test("the apps are read only while a Mate drawer is open, and a project without any shows no block", async () => {
  const app = await boot({ hash: "#crew/shop/db-migrate" });
  assert.ok(!app.requests.some(r => r.url.startsWith("/api/plugins")), "no app read for a crew drawer");
  const docs = await boot({ hash: "#mate/docs-site" });
  assert.ok(docs.requests.some(r => r.url === "/api/plugins?project=docs-site"));
  assert.equal(docs.drawer().querySelector("[aria-label=\"Apps\"]"), null);
});

test("when the apps cannot be read the block says Apps: ? and the drawer still draws", async () => {
  const app = await boot({ hash: "#mate/shop", fail: { "/api/plugins": "plugins.yaml is unreadable" } });
  const d = app.drawer();
  assert.equal(d.querySelector("[aria-label=\"Apps\"]").text, "Apps: ?");
  assert.equal(d.querySelector("h2").text, "shop");
  assert.deepEqual(d.querySelectorAll(".cr .id").map(n => n.text), ["rd1", "api-fix", "ui-polish", "db-migrate"]);
  assert.match(d.text, /Thinking it over, facing api-fix/);
});

// The admin page's token consumption for a task, in a new tab; the Mate has
// no task page, so its drawer has no such button.
const usageHref = (project, crew) => "../#/p/" + encodeURIComponent(project) + "/t/" + encodeURIComponent(crew) + "/usage";
const usageLinks = d => d.querySelectorAll("a").filter(a => a.text === "Usage details");

test("the crew drawer and every cabinet row link to the task's usage in a new tab", async () => {
  const app = await boot({ hash: "#crew/shop/db-migrate" });
  let links = usageLinks(app.drawer());
  assert.equal(links.length, 1);
  assert.equal(links[0].getAttribute("href"), usageHref("shop", "db-migrate"));
  assert.equal(links[0].getAttribute("target"), "_blank");
  assert.equal(links[0].getAttribute("rel"), "noopener");
  assert.ok(app.drawer().querySelectorAll("a").some(a => a.getAttribute("href") === "../#/p/shop/t/db-migrate"), "Open timeline is unchanged");
  assert.ok(links[0].querySelector("svg"), "an icon beside the words");

  for (const project of ["shop", "infra"]) {
    const cab = await boot({ hash: "#cabinet/" + project });
    const rows = cab.drawer().querySelectorAll(".tr");
    assert.ok(rows.length > 0, project + " has finished tasks");
    for (const row of rows) {
      const a = usageLinks(row);
      assert.equal(a.length, 1);
      assert.equal(a[0].getAttribute("href"), usageHref(project, row.querySelector(".tn").text));
      assert.equal(a[0].getAttribute("target"), "_blank");
      assert.equal(a[0].getAttribute("rel"), "noopener");
    }
  }

  const mate = await boot({ hash: "#mate/shop" });
  assert.equal(usageLinks(mate.drawer()).length, 0, "no button for the Mate");
});

test("Escape, the close button and a click on the floor close the drawer", async () => {
  const app = await boot({ hash: "#mate/shop" });
  assert.ok(!app.drawer().hasAttribute("hidden"));
  app.key("Escape");
  await app.flush();
  assert.equal(app.hash(), "");
  assert.ok(app.drawer().hasAttribute("hidden"));

  await app.click(deskOf(app.root, "rd1"));
  await app.click(app.drawer().querySelector("[aria-label=\"Close details\"]"));
  assert.ok(app.drawer().hasAttribute("hidden"));

  await app.click(deskOf(app.root, "rd1"));
  assert.ok(!app.drawer().hasAttribute("hidden"));
  await app.click(app.root.querySelector("#floor"));
  assert.ok(app.drawer().hasAttribute("hidden"), "clicking the floor closes it");
});

// ------------------------------------------------------------ the captain's news

const newsRows = (sec) => sec.querySelectorAll("button.nw");
const newsSec = (d, label) => d.querySelector(`section[aria-label="${label}"]`);
const kvOf = (n) => { const o = {}; const dt = n.querySelectorAll("dt"), dd = n.querySelectorAll("dd"); dt.forEach((t, i) => { o[t.text] = dd[i].text; }); return o; };
const newsRow = (d, title) => d.querySelectorAll("button.nw").find(b => b.querySelector(".nwt b").text === title);

test("the captain's desk says how much news is unread and opens the news from every Mate", async () => {
  const app = await boot();
  const desk = app.root.querySelector(".cap");
  assert.match(desk.text, /1 question waiting.*1 news unread/);
  await app.click(desk);
  assert.equal(app.hash(), "#captain");
  const d = app.drawer();
  assert.ok(!d.hasAttribute("hidden"));
  assert.equal(d.querySelector("h2").text, "News from the Mates");
  assert.match(d.querySelector(".dstate").text, /1 unread, 1 to answer/);

  // What the captain owes comes first, newest first, each with where it
  // stands; what the Mate is on next; closed news below, the latest ten.
  const waiting = newsSec(d, "Waiting on you");
  assert.deepEqual(newsRows(waiting).map(b => b.querySelector(".nwt b").text), ["Primary button colour", "Staging DB password"]);
  assert.match(newsRows(waiting)[0].text, /shop · ui-polish · #4 · unread/);
  assert.ok(newsRows(waiting)[0].classes.includes("unread"));
  assert.match(newsRows(waiting)[1].text, /#3 · read, not answered/);
  assert.match(newsRows(newsSec(d, "The Mate is on it"))[0].text, /PR #41 ready.*answered, Mate on it/);
  const earlier = newsSec(d, "Earlier");
  const old = newsRows(earlier);
  assert.equal(old.length, 10, "the latest ten closed news");
  assert.match(old[0].text, /Login tests flaky.*shop · login-flow · #1 · closed/);
  assert.ok(old.some(b => /Old note 8.*dropped/.test(b.text)));
  await app.click(earlier.querySelector(".news-more"));
  assert.equal(newsRows(newsSec(app.drawer(), "Earlier")).length, 12, "Show all lists every closed news");
  assert.match(app.drawer().text, /Sending an answer from the browser is not wired yet/);
});

test("a news opens into its question, options, when it was read and answered, and the Mate's updates", async () => {
  const app = await boot({ hash: "#captain" });
  await app.click(newsRow(app.drawer(), "Primary button colour"));
  let body = app.drawer().querySelector(".nli.open .nwb");
  assert.match(body.text, /Brand blue or the darker one\?/);
  assert.deepEqual(body.querySelectorAll(".nopt li").map(li => li.text), ["a brand blue recommended", "b darker blue"]);
  let kv = kvOf(body);
  assert.equal(kv.Status, "unread");
  assert.equal(kv.Read, "not yet");
  assert.equal(kv.Answered, "not yet");
  assert.equal(app.root.querySelectorAll("img").length, 0, "the Mate's context stays text");
  assert.match(body.text, /<img src=x onerror=alert\(1\)>/);

  await app.click(newsRow(app.drawer(), "PR #41 ready"));
  body = app.drawer().querySelectorAll(".nli.open .nwb")[1];
  kv = kvOf(body);
  assert.equal(kv.Status, "answered, Mate on it");
  assert.match(kv.Read, /^Oct 3 .* · 1h ago$/);
  assert.match(kv.Answered, /^Oct 3 .* · 40m ago$/);
  assert.equal(kv["Your answer"], "[a] merge it");
  assert.match(body.querySelector(".nnotes").text, /CI is green/);

  // Opening news is a view: nothing but GETs, and no seen is written.
  for (const r of app.requests) assert.equal(r.method || "GET", "GET", r.url);
  assert.ok(app.requests.some(r => r.url === "/api/news"));
});

test("a Mate whose news cannot be read says so and the others still show", async () => {
  const fx = fixture();
  fx.news.projects[1] = { project: "docs-site", logged: true, news: [], error: "permission denied" };
  const app = await boot({ fixture: fx, hash: "#captain" });
  const d = app.drawer();
  assert.equal(d.querySelector(".news-err").text, "docs-site: news ?");
  assert.equal(d.querySelector(".news-err").getAttribute("title"), "permission denied");
  assert.equal(newsRows(newsSec(d, "Earlier")).length, 1);
});

test("when the news cannot be read the desk says news ? and the drawer says why", async () => {
  const app = await boot({ fail: { "/api/news": "news.jsonl is locked" } });
  assert.match(app.root.querySelector(".cap").text, /news \?/);
  await app.click(app.root.querySelector(".cap"));
  assert.match(app.drawer().text, /The news could not be read: news.jsonl is locked/);
});

test("with no news log anywhere the drawer says where news comes from", async () => {
  const fx = fixture();
  fx.news.projects = fx.news.projects.map(p => ({ project: p.project, logged: false, news: [] }));
  const app = await boot({ fixture: fx, hash: "#captain" });
  assert.match(app.root.querySelector(".cap").text, /no news waiting/);
  assert.match(app.drawer().text, /No Mate has opened a news log/);
});

test("on a phone the captain's card opens the news too", async () => {
  const app = await boot({ phone: true });
  const card = app.root.querySelector(".mcap");
  assert.match(card.text, /1 news unread/);
  await app.click(card);
  assert.equal(app.drawer().querySelector("h2").text, "News from the Mates");
});

// ------------------------------------------------------------ unknowns

test("an unknown value reads as unknown, never as zero", async () => {
  // search has no scene row yet and no recorded turn; docs-site prices
  // nothing.
  const app = await boot({ hash: "#crew/docs-site/search" });
  const d = app.drawer();
  assert.equal(d.querySelector(".tv b").text, "?", "tokens today without a scene row");
  assert.match(d.querySelector(".meter").text, /context \? · window unknown/);
  assert.ok(d.querySelector(".meter .bar").classes.includes("unk"), "the hatched track, not an empty one");
  assert.equal(d.querySelector(".big .c b").text, "?");
  assert.match(d.querySelector(".big .c").text, /cost not reported/);

  const mate = await boot({ hash: "#mate/docs-site" });
  assert.equal(mate.drawer().querySelector(".big .c b").text, "?", "a Mate on an unpriced model has no cost");
});

test("when the scene cannot be read, tokens today is ? rather than 0", async () => {
  const app = await boot({ fail: { "/api/now": "no such table: v_now" } });
  assert.match(app.root.querySelector(".top .kpi").text, /Today \? tokens/);
  assert.equal(roomOf(app.root, "shop").querySelectorAll(".desk").length, 4, "the rest of the office still draws");
});

test("a room whose project cannot be read says so and the others still draw", async () => {
  const app = await boot({ fail: { "/api/projects/docs-site": "sent.log is unreadable" } });
  assert.match(roomOf(app.root, "docs-site").text, /could not be read: sent\.log is unreadable/);
  assert.equal(roomOf(app.root, "docs-site").querySelector(".plate .ptok b").text, "?", "an unread room is not 0 tokens");
  assert.equal(roomOf(app.root, "shop").querySelector(".plate .ptok b").text, "3.7M");
  assert.equal(roomOf(app.root, "shop").querySelectorAll(".desk").length, 4);
});

test("a project token label is ? when any part of the sum is missing", async () => {
  const fx = fixture();
  delete fx.projects.shop.mate.tokens.total;
  const app = await boot({ fixture: fx });
  assert.equal(roomOf(app.root, "shop").querySelector(".plate .ptok b").text, "?", "a partial sum is not the project's total");
  assert.equal(roomOf(app.root, "docs-site").querySelector(".plate .ptok").text, "719k tokens");
});

test("a failed poll turns the live pill stale with the reason", async () => {
  const app = await boot({ eventsFail: true });
  const pill = app.root.querySelector("#live");
  assert.ok(pill.classes.includes("stale"));
  assert.match(pill.text, /stale · the dashboard is not answering/);
});

test("a timeline that stopped moving turns the live pill stale, and clears with it", async () => {
  const app = await boot();
  const quiet = { last_event_id: 1, events: [], now: [] };
  await app.answerEvents(Object.assign({}, quiet, { timeline_stale: [
    { project: "shop", since: "2026-10-03T07:30:43.000000000Z", error: "refused" }
  ] }));
  let pill = app.root.querySelector("#live");
  assert.ok(pill.classes.includes("stale"), "a page drawn from a frozen timeline is not live");
  assert.match(pill.text, /stale · timeline shop since \d\d:30:43: refused/);
  await app.answerEvents(quiet);
  pill = app.root.querySelector("#live");
  assert.ok(!pill.classes.includes("stale"), "the pill is live again once the timeline moves");
});

// ------------------------------------------------------------ safety

test("workspace text stays text", async () => {
  const fx = fixture();
  const evil = "<img src=x onerror=alert(1)>";
  fx.projects.shop.tasks[0].crew = evil;
  fx.projects.shop.tasks[0].text = "<script>alert('task')</script>";
  fx.projects.shop.inbox[0].text = "<b>bold</b>";
  const app = await boot({ fixture: fx, hash: "#crew/shop/" + encodeURIComponent(evil) });
  assert.equal(app.root.querySelectorAll("img").length, 0);
  assert.equal(app.root.querySelectorAll("script").length, 0);
  assert.equal(app.drawer().querySelector("h2").text, evil);
  assert.match(app.drawer().text, /<script>alert\('task'\)<\/script>/);
  const src = fs.readFileSync(path.join(__dirname, "ui", "office", "office.js"), "utf8");
  for (const sink of ["innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval("]) {
    assert.ok(!src.includes(sink), "office.js uses " + sink);
  }
});

// ------------------------------------------------------------ actions (CON-141)

const SHOP_ACTIONS = {
  shop: {
    project: "shop",
    mate: [
      { action: "start", label: "Start mate", available: false, reason: "Mate is recorded running" },
      { action: "stop", label: "Stop mate…", available: true, confirm: "Stop the Mate of shop? This Mate only; its crews keep running." },
      { action: "mode", label: "Mode → auto", available: true }
    ],
    crews: {
      "api-fix": [
        { action: "stop", label: "Stop crew…", available: true, confirm: "Close shop/api-fix?" },
        { action: "merge", label: "Merge…", available: false, reason: "not waiting on merge; a crew can be merged once it reports wait-mate" },
        { action: "diff", label: "Diff", available: true }
      ]
    }
  }
};

const actionsOf = app => app.drawer().querySelector('section[aria-label="Actions"]');
const actionButton = (app, action) => actionsOf(app).querySelector(`[data-focus="act:${action}"]`);
const posts = app => app.requests.filter(r => r.method === "POST");

test("the Mate drawer offers the console's actions, and one that cannot run says why", async () => {
  const app = await boot({ hash: "#mate/shop", actions: SHOP_ACTIONS });
  const start = actionButton(app, "start");
  assert.ok(start, "the start button is drawn");
  assert.ok("disabled" in start.attrs, "start is disabled on a running Mate");
  assert.equal(start.parentNode.querySelector(".why").textContent, "Mate is recorded running", "the reason sits under the button");
  assert.ok(!("disabled" in actionButton(app, "mode").attrs));
  await app.click(start);
  assert.equal(posts(app).length, 0, "a disabled button sends nothing");
});

test("a destructive action asks first: Cancel sends nothing, the action's own button runs it", async () => {
  const pressed = [];
  const app = await boot({ hash: "#mate/shop", actions: SHOP_ACTIONS, press: b => { pressed.push(b); return { body: { line: "Mate mate-shop stop confirmed in session s1" } }; } });
  const dialog = () => app.root.querySelector('.dlg[role="alertdialog"]');
  await app.click(actionButton(app, "stop"));
  assert.match(dialog().textContent, /Stop the Mate of shop\?/);
  assert.match(dialog().querySelector(".dlg-obj").textContent, /shop/, "the dialog names what it acts on");
  assert.equal(app.doc.activeElement && app.doc.activeElement.attrs["data-focus"], "act-cancel", "the focus lands on Cancel");
  await app.click(dialog().querySelector('[data-focus="act-cancel"]'));
  assert.equal(dialog(), null);
  assert.equal(posts(app).length, 0);

  await app.click(actionButton(app, "stop"));
  app.key("Escape");
  await app.flush();
  assert.equal(dialog(), null, "Escape answers no");
  assert.ok(!app.drawer().hasAttribute("hidden"), "and leaves the drawer open");
  assert.equal(posts(app).length, 0);

  await app.click(actionButton(app, "stop"));
  assert.equal(dialog().querySelector('[data-focus="act-confirm"]').textContent, "Stop mate", "only the action's own name runs it");
  await app.click(dialog().querySelector('[data-focus="act-confirm"]'));
  assert.deepEqual(pressed, [{ action: "stop", project: "shop" }]);
  const post = posts(app)[0];
  assert.equal(post.url, "/api/actions");
  assert.equal(post.headers["content-type"], "application/json");
  assert.equal(actionsOf(app).querySelector(".act-out").textContent, "Mate mate-shop stop confirmed in session s1");
});

test("an action that runs at once names the crew, and a failure is the service's own words", async () => {
  const app = await boot({ hash: "#mate/shop", actions: SHOP_ACTIONS,
    press: () => ({ status: 422, body: { error: "send: the composer is not empty" } }) });
  await app.click(actionButton(app, "mode"));
  const out = actionsOf(app).querySelector('.res.t-bad[role="status"]');
  assert.ok(out, "the failure is shown");
  assert.match(out.textContent, /^Failed after running ·/);
  assert.equal(out.querySelector(".act-err").textContent, "send: the composer is not empty");
});

test("a crew drawer has the crew's actions, and Diff shows the branch without writing anything", async () => {
  const app = await boot({ hash: "#crew/shop/api-fix", actions: SHOP_ACTIONS });
  assert.match(actionButton(app, "merge").parentNode.textContent, /not waiting on merge/);
  await app.click(actionButton(app, "diff"));
  assert.match(actionsOf(app).querySelector(".act-diff").textContent, /\+one line/);
  assert.equal(posts(app).length, 0);
  await app.click(actionButton(app, "diff"));
  assert.equal(actionsOf(app).querySelector(".act-diff"), null, "a second press hides it");
});

test("a dashboard that cannot act says so instead of drawing buttons", async () => {
  const app = await boot({ hash: "#mate/shop" });
  assert.match(actionsOf(app).textContent, /read-only/);
  assert.equal(actionsOf(app).querySelector("button"), null);
});

test("reading the page only ever GETs its own origin", async () => {
  const app = await boot({ hash: "#crew/shop/db-migrate" });
  await app.click(roomOf(app.root, "shop").querySelector(".mate"));
  await app.click(roomOf(app.root, "shop").querySelector(".cab"));
  assert.ok(app.requests.length >= 8);
  assert.ok(app.requests.some(r => r.url === "/api/plugins?project=shop"), "the Mate drawer's app read is among them");
  for (const r of app.requests) {
    assert.ok(r.url.startsWith("/api/"), r.url + " is not this origin's API");
    assert.equal(r.method || "GET", "GET", r.url + " is not a GET");
  }
});


test("a refusal says nothing changed, and is told apart from a failure", async () => {
  const app = await boot({ hash: "#mate/shop", actions: SHOP_ACTIONS,
    press: () => ({ status: 409, body: { error: "Action refused: Mate is recorded stopped · nothing started" } }) });
  await app.click(actionButton(app, "mode"));
  const out = actionsOf(app).querySelector('.res[role="status"]');
  assert.ok(out.classes.includes("t-wait"), "a refusal is not drawn as a failure");
  assert.match(out.textContent, /^Refused · nothing changed · Mate is recorded stopped$/);
});

test("every slot keeps its place: what the web cannot do yet is dimmed with why", async () => {
  const app = await boot({ hash: "#mate/shop", actions: SHOP_ACTIONS });
  const labels = actionsOf(app).querySelectorAll(".act .l").map(n => n.textContent);
  assert.deepEqual(labels, ["Start mate", "Stop mate…", "Restart mate…", "Mode → auto", "Clear composer", "Explain notice (Jev)", "Copy agent id", "Remove project…"]);
  const remove = actionButton(app, "remove_project");
  assert.ok("disabled" in remove.attrs);
  assert.equal(remove.parentNode.querySelector(".why").textContent, "not wired yet");
  assert.equal(actionButton(app, "restart_mate").parentNode.querySelector(".why").textContent, "?", "a button the service did not send is unknown, not hidden");
  assert.match(actionButton(app, "stop").textContent, /x$/, "the console's key is on the button");
});

test("while an action runs, that drawer's other buttons wait and the running one says so", async () => {
  let finish;
  const app = await boot({ hash: "#mate/shop", actions: SHOP_ACTIONS,
    press: () => new Promise(r => { finish = () => r({ body: { line: "Mode is manual" } }); }) });
  await app.click(actionButton(app, "mode"));
  const mode = actionButton(app, "mode");
  assert.equal(mode.querySelector(".l").textContent, "Running Mode → auto…");
  assert.equal(mode.attrs["aria-busy"], "true");
  assert.ok("disabled" in actionButton(app, "stop").attrs, "the drawer's other buttons wait");
  assert.equal(actionButton(app, "stop").parentNode.querySelector(".why").textContent, "waiting on Mode → auto");
  await app.click(actionButton(app, "stop"));
  assert.equal(app.root.querySelector(".dlg"), null, "a waiting button asks nothing");
  finish();
  await app.flush();
  assert.equal(actionsOf(app).querySelector(".act-out").textContent, "Mode is manual");
  assert.ok(!("disabled" in actionButton(app, "stop").attrs), "the drawer's buttons come back");
  assert.equal(posts(app).length, 1);
});

test("a crew drawer can copy the crew's id even when the clipboard is out of reach", async () => {
  const app = await boot({ hash: "#crew/shop/api-fix", actions: SHOP_ACTIONS });
  await app.click(actionButton(app, "copy"));
  assert.match(actionsOf(app).querySelector(".res").textContent, /api-fix · select it to copy/);
  assert.equal(posts(app).length, 0);
});

test("a read-only dashboard says so in a band under the top bar", async () => {
  const app = await boot();
  const band = app.root.querySelector(".bn.ro");
  assert.ok(band, "the band is drawn");
  assert.match(band.textContent, /read-only/);
  const acting = await boot({ actions: SHOP_ACTIONS });
  assert.equal(acting.root.querySelector(".bn.ro"), null, "a dashboard that can act draws no band");
});

test("the Mate drawer's Talk tab shows the recorded turns, the last one open, and a composer that says why it cannot send", async () => {
  const fx = fixture();
  fx.mates.shop.exchanges = [
    { id: "e1", source: "crew", prompt: "api-fix is ready", prompt_at: "2026-10-03T13:42:07.000000000Z", response: "Reviewing it now.", ended_at: "2026-10-03T13:43:07.000000000Z", tool_calls: 2, model_calls: 3, tokens: { total: 1200 } },
    { id: "e2", source: "captain", prompt: "Merge api-fix when green", prompt_at: "2026-10-03T14:10:07.000000000Z", response: "<b>Merged</b> → main.", ended_at: "2026-10-03T14:11:07.000000000Z", tool_calls: 1, model_calls: 2, tokens: { total: 900 } }
  ];
  const app = await boot({ hash: "#mate/shop", fixture: fx, actions: SHOP_ACTIONS });
  await app.click(app.drawer().querySelector('[data-focus="tab:talk"]'));
  const talk = app.drawer().querySelector('section[aria-label="Talk"]');
  assert.equal(talk.querySelectorAll(".tc").length, 1, "the older turn folds to one line");
  assert.match(talk.querySelector(".tc").textContent, /crew.*api-fix is ready/);
  assert.equal(talk.querySelector(".msg.reply").textContent, "<b>Merged</b> → main.", "a reply stays text");
  await app.click(talk.querySelector(".tc"));
  assert.equal(app.drawer().querySelectorAll(".tx").length, 2, "a folded turn opens");
  const send = app.drawer().querySelector(".dfoot .btn.primary");
  assert.ok("disabled" in send.attrs);
  assert.match(app.drawer().querySelector(".dfoot").textContent, /not wired yet/);
  assert.equal(posts(app).length, 0);
});

test("the Mate drawer goes full screen and back; other drawers have no such button", async () => {
  const app = await boot({ hash: "#mate/shop", actions: SHOP_ACTIONS });
  const full = () => app.drawer().querySelector('[data-focus="drawer-full"]');
  assert.ok(!app.drawer().classes.includes("drawer-full"));
  assert.equal(full().getAttribute("aria-label"), "Full screen");
  assert.equal(full().parentNode.parentNode, app.drawer(), "the buttons sit on the drawer itself, so they stay put as it scrolls");
  await app.click(full());
  assert.ok(app.drawer().classes.includes("drawer-full"));
  assert.equal(full().getAttribute("aria-pressed"), "true");
  await app.click(app.drawer().querySelector('[data-focus="tab:talk"]'));
  assert.ok(app.drawer().classes.includes("drawer-full"), "the Talk tab stays full screen");
  await app.click(full());
  assert.ok(!app.drawer().classes.includes("drawer-full"));
  await app.click(deskOf(app.root, "rd1"));
  assert.equal(app.drawer().querySelector("h2").text, "rd1");
  assert.equal(full(), null, "a crew drawer stays at the side");
});

test("each room has an inbox on its wall; it lists what waits and the full log", async () => {
  const app = await boot();
  const box = roomOf(app.root, "shop").querySelector(".mbox");
  assert.equal(box.querySelector(".cnt").textContent, "1");
  await app.click(box);
  assert.equal(app.hash(), "#inbox/shop");
  const d = app.drawer();
  assert.equal(d.querySelector("h2").text, "Message box");
  const item = d.querySelector(".iitem");
  assert.match(item.textContent, /needs-decision.*ui-polish.*keep the 6px button radius/);
  const assign = item.querySelector('[aria-label="Assign to Mate"]');
  assert.ok("disabled" in assign.attrs);
  assert.match(item.querySelector(".why").textContent, /read-only dashboard|not wired yet/);
  assert.match(d.querySelector(".autol").textContent, /manual/);
  await app.click(d.querySelector('[data-focus="inbox-mode:log"]'));
  assert.ok(app.requests.some(r => r.url.startsWith("/api/projects/shop/history?since=")), "the log reads the room's history");
  assert.equal(posts(app).length, 0);
});

test("Mates only shrinks every room to its plate and its Mate", async () => {
  const app = await boot();
  await app.click(app.root.querySelector('[data-focus="mates-only"]'));
  const shop = roomOf(app.root, "shop");
  assert.ok(shop.classes.includes("compact"));
  assert.equal(shop.querySelectorAll(".desk").length, 0);
  assert.ok(shop.querySelector(".mate"));
  assert.match(shop.querySelector(".mates-line").textContent, /4 open crews · 1 in the inbox/);
  await app.click(app.root.querySelector('[data-focus="mates-only"]'));
  assert.ok(roomOf(app.root, "shop").querySelectorAll(".desk").length > 0);
});

test("the new-room door opens a form that checks the name and gives the command to run", async () => {
  const app = await boot();
  await app.click(app.root.querySelector(".newroom"));
  assert.equal(app.hash(), "#add");
  const name = app.root.querySelector("#add-name");
  const type = async v => { name.value = v; name.dispatch("input", { target: name }); await app.flush(); };
  assert.equal(app.root.querySelector("#add-go-why").textContent, "name is required");
  await type("Shop App");
  assert.match(app.root.querySelector("#add-name-why").textContent, /lowercase/);
  await type("shop");
  assert.match(app.root.querySelector("#add-name-why").textContent, /already exists/);
  await type("kiosk");
  assert.match(app.root.querySelector("#add-go-why").textContent, /read-only dashboard|not wired yet/);
  assert.equal(app.root.querySelector("#add-cmd-t").textContent, "mate project add kiosk");
  assert.ok("disabled" in app.root.querySelector("#add-go").attrs);
  assert.equal(posts(app).length, 0);
});

test("an owed news opens into an answer form whose Send says why it cannot send yet", async () => {
  const app = await boot({ hash: "#captain" });
  const d = app.drawer();
  await app.click(newsRow(d, "Primary button colour"));
  const form = app.drawer().querySelector(".nans-form");
  assert.ok(form, "the answer form is drawn");
  const radios = form.querySelectorAll('input[type="radio"]');
  assert.equal(radios.length, 2);
  radios[1].dispatch("change");
  await app.flush();
  assert.ok(app.drawer().querySelectorAll(".opt")[1].classes.includes("on"), "the picked option is marked");
  assert.match(app.drawer().querySelector(".nans-form .pick").textContent, /Mate's pick/);
  assert.match(app.drawer().querySelector(".nans-form").textContent, /not wired yet/);
  await app.click(newsRow(app.drawer(), "PR #41 ready"));
  assert.equal(app.drawer().querySelectorAll(".nans-form").length, 1, "news the Mate is on gets no form");
});

test("news that arrives while the page is open shows a toast that opens it", async () => {
  const fx = fixture();
  const app = await boot({ fixture: fx });
  assert.equal(app.root.querySelector(".toast"), null, "what was there at load is not news");
  fx.news.projects[0].news.unshift({ id: 9, title: "Ship on Friday?", context: [], options: [], status: "new", opened_at: "2026-10-03T14:31:07.000000000Z", at: "2026-10-03T14:31:07.000000000Z", notes: [], kind: "decision", question: "Friday?" });
  await app.answerEvents({ last_event_id: 900, events: [], now: [] });
  const t = app.root.querySelector(".toast");
  assert.ok(t, "the toast is drawn");
  assert.match(t.textContent, /News from the Mate of shop.*Ship on Friday\?/);
  await app.click(t.querySelector('[data-focus="toast-open"]'));
  assert.equal(app.hash(), "#captain");
  assert.equal(app.root.querySelector(".toast"), null);
});

test("a room whose timeline stopped moving says since when", async () => {
  const app = await boot();
  await app.answerEvents({ last_event_id: 900, events: [], now: [], timeline_stale: [{ project: "shop", since: "2026-10-03T13:41:00.000000000Z", error: "git timed out" }] });
  assert.match(roomOf(app.root, "shop").querySelector(".rnote").textContent, /timeline stuck since/);
  assert.equal(roomOf(app.root, "infra").querySelector(".rnote"), null);
});

// ------------------------------------------------------------ the daily board

const kpi = (d, label) => d.querySelectorAll(".bk").find(n => n.querySelector(".bkl").text === label);

test("the whiteboard in the hallway opens the daily board for the whole workspace", async () => {
  const app = await boot();
  await app.click(app.root.querySelector(".hall .dbd"));
  assert.equal(app.hash(), "#board");
  const d = app.drawer();
  assert.equal(d.querySelector("h2").text, "Daily board");
  assert.ok(d.classes.includes("drawer-cabinet"), "the board is wide");
  assert.match(d.querySelector(".role").text, /workspace · by day/);
  assert.match(d.querySelector(".dstate").text, /last 14 days.*days in \+07:00/);
  assert.ok(app.requests.some(r => r.url === "/api/board?days=14"));
  assert.equal(d.querySelectorAll("tbody tr").length, 14);
  assert.equal(d.querySelector("tbody tr th").text, "Today", "newest first");
});

test("today is set against the average of the days before it, in tokens and counts", async () => {
  const app = await boot({ hash: "#board" });
  const d = app.drawer();
  assert.match(kpi(d, "Tokens").text, /3\.5M.*avg 2M · \+75%/);
  assert.ok(kpi(d, "Tokens").querySelector(".bkd").classes.includes("down"), "more tokens is not good news");
  assert.match(kpi(d, "Done").text, /3.*avg 2\.0 · \+50%/);
  assert.ok(kpi(d, "Done").querySelector(".bkd").classes.includes("up"));
  assert.deepEqual([kpi(d, "Dropped").querySelector("b").text, kpi(d, "Dropped").querySelector(".bkd").text], ["2", "avg 0.0"]);
  assert.match(kpi(d, "Rework").text, /2.*avg 1\.0 · \+100%/);
  assert.deepEqual([kpi(d, "Over budget").querySelector("b").text, kpi(d, "Over budget").querySelector(".bkd").text], ["2", "avg 0.0"]);
  assert.match(kpi(d, "Incidents").getAttribute("title"), /2 over budget, 1 stale/);
  assert.match(kpi(d, "Opened").getAttribute("title"), /1 review, 1 scout, 2 ship/);
  assert.doesNotMatch(d.text, /\$|USD|cost/i, "the board counts tokens, not money");
  assert.doesNotMatch(d.querySelector("thead").text, /merged/i, "the timeline does not see every merge");
});

test("the board's range and project buttons re-read and narrow it", async () => {
  const app = await boot({ hash: "#board" });
  const rangeBtn = n => app.drawer().querySelectorAll(".bseg button").find(b => b.text === n);
  await app.click(rangeBtn("7 days"));
  assert.ok(app.requests.some(r => r.url === "/api/board?days=7"));
  assert.match(app.drawer().querySelector(".dstate").text, /last 7 days/);
  assert.equal(app.drawer().querySelectorAll("tbody tr").length, 7);
  await app.click(rangeBtn("docs-site"));
  const d = app.drawer();
  assert.match(d.querySelector(".role").text, /docs-site · by day/);
  assert.match(kpi(d, "Tokens").text, /500k.*avg 0/);
  assert.match(kpi(d, "Dropped").getAttribute("title"), /1 didn't start/);
  const today = d.querySelector("tbody tr");
  assert.deepEqual(today.children.map(c => c.text), ["Today", "500k", "1", "0", "1", "0", "0", "0", "0"]);
});

const section = (d, label) => d.querySelector(`section[aria-label="${label}"]`);
const rowsOf = sec => sec.querySelector("tbody").querySelectorAll("tr");

test("a day on the board opens into its tasks, who spent the tokens and its incidents", async () => {
  const app = await boot({ hash: "#board" });
  const todayRow = app.drawer().querySelector("tbody tr");
  await app.click(todayRow.querySelector(".bday"));
  assert.equal(app.hash(), "#board/2026-10-03");
  assert.ok(app.requests.some(r => r.url === "/api/board/day?date=2026-10-03"));
  const d = app.drawer();
  assert.ok(d.classes.includes("drawer-day"), "an open day widens the drawer");
  assert.equal(d.querySelector(".bnavd h3").text, "Today");
  assert.match(kpi(d, "Tokens").text, /3\.5M/);

  const tasks = section(d, "Tasks that day");
  assert.match(tasks.querySelector("h3").text, /2 tasks · 2\.4M tokens that day/);
  const [ship, scout] = rowsOf(tasks);
  assert.match(ship.text, /login-flow.*Fix the checkout total.*shop.*Claude.*opus.*effort: high.*opened .*finished .*4.*1\.9M.*1\.9M/);
  assert.match(scout.text, /seo.*docs-site.*didn't start/);

  const who = section(d, "Tokens by who");
  assert.deepEqual(rowsOf(who).map(r => r.children[0].text), ["login-flow", "Mate", "seo"], "biggest first");
  assert.match(who.querySelector("tfoot").text, /All.*14.*3k.*3\.4M.*0.*18k.*3\.4M/);

  const inc = section(d, "Incidents that day");
  assert.equal(rowsOf(inc).length, 2);
  assert.match(rowsOf(inc)[0].text, /stale.*login-flow.*shop.*after 12m/);
  assert.match(rowsOf(inc)[1].text, /over budget.*Mate.*shop.*still open/);
  assert.doesNotMatch(d.text, /\$|USD|cost/i, "the day counts tokens, not money");

  await app.click(ship);
  assert.equal(app.hash(), "#crew/shop/login-flow", "a task row opens its crew");
});

test("an open day steps to its neighbours, narrows to a project and closes back to all days", async () => {
  const app = await boot({ hash: "#board/2026-10-03" });
  let d = app.drawer();
  assert.ok(d.querySelectorAll(".bstep").find(b => b.getAttribute("aria-label") === "Day after").hasAttribute("disabled"), "nothing after today");
  await app.click(d.querySelectorAll(".bstep").find(b => /Day before/.test(b.getAttribute("aria-label"))));
  assert.equal(app.hash(), "#board/2026-10-02");
  d = app.drawer();
  assert.match(section(d, "Tasks that day").text, /No task opened, closed or worked that day/);
  assert.match(section(d, "Incidents that day").text, /No incidents that day/);
  assert.ok(d.querySelectorAll("tbody tr").find(r => r.classes.includes("bon")), "the open day's row is marked");

  await app.click(d.querySelectorAll(".bstep").find(b => /Day after/.test(b.getAttribute("aria-label"))));
  await app.click(app.drawer().querySelectorAll(".bseg button").find(b => b.text === "docs-site"));
  d = app.drawer();
  assert.deepEqual(rowsOf(section(d, "Tasks that day")).map(r => r.querySelector(".bday").text), ["seo"]);
  assert.deepEqual(rowsOf(section(d, "Tokens by who")).map(r => r.children[0].text), ["seo"]);
  assert.match(section(d, "Incidents that day").text, /No incidents that day/);

  await app.click(d.querySelector(".bback"));
  assert.equal(app.hash(), "#board");
  assert.ok(!app.drawer().querySelector(".bnav"), "all days again");
});

test("on a phone the hallway has the board too", async () => {
  const app = await boot({ phone: true });
  await app.click(app.root.querySelector(".mhall .dbm"));
  assert.equal(app.drawer().querySelector("h2").text, "Daily board");
});

// ------------------------------------------------------------ phone

test("on a phone the rooms are cards and the drawer is a bottom sheet over a scrim", async () => {
  const app = await boot({ phone: true });
  assert.equal(app.root.querySelectorAll("section.room").length, 0);
  assert.equal(app.root.querySelectorAll("section.mroom").length, 3);
  const shop = app.root.querySelector('section.mroom[aria-label="shop room"]');
  assert.equal(shop.querySelector(".ptok").text, "3.7M tokens");
  const scrim = app.root.querySelector(".scrim");
  assert.ok(scrim.hasAttribute("hidden"));
  const rd1 = app.root.querySelectorAll(".mc").find(n => n.querySelector(".id").text === "rd1");
  await app.click(rd1);
  assert.equal(app.drawer().querySelector("h2").text, "rd1");
  assert.ok(app.drawer().querySelector(".grab"), "the sheet's handle");
  assert.ok(!app.root.querySelector(".scrim").hasAttribute("hidden"));
  await app.click(app.root.querySelector(".scrim"));
  assert.ok(app.drawer().hasAttribute("hidden"), "tapping the scrim closes the sheet");
  const stuck = app.root.querySelectorAll(".mc").find(n => n.querySelector(".id").text === "db-migrate");
  assert.ok(stuck.classes.includes("p-blocked"));
  assert.equal(stuck.querySelector(".mw").text, "stuck");
});
