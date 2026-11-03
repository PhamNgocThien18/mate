import * as THREE from './vendor/three.js';
import { OrbitControls } from './vendor/three.js';
import { createCity, lights, PALETTE, harnessName, formatTokens } from './city.js';
import { createTentRoom } from './room.js';
import { readHistory } from './history.js';
import { HARNESSES } from './harnesses.js';

// Agent City's page: the workspace drawn as a city (city.js), kept live from
// the dashboard's API the way Mate Office is - one long poll on /api/events,
// and a reload of what it reads when an event lands. Since CON-142 it does
// the console's work too (design: docs/design/agent-city-console):
//
// - places: City Hall (topics the Mates ask the captain, the projects, adding
//   one), a block's plaza (its Mate: chat, inbox, actions), a crew (actions,
//   peek, diff), a tent (playback of what its agents did, the crews in it);
// - one panel at a time, docked right on a wide screen and a bottom sheet on
//   a phone, the camera flown so the thing stays in view beside it;
// - pins: DOM buttons over the scene for only what needs the captain (a topic
//   waiting, a crew ready to merge, an incident) and what is running, so they
//   read at any distance and Tab reaches them;
// - every action is the console's (POST /api/actions): fixed buttons, a reason
//   under the ones that cannot run, a question with Cancel focused for the
//   destructive ones, Running …, one line back. A 409 is "refused, nothing
//   changed", a 422 "ran and failed", each in the server's own words.
//
// Acting is for now only: a tent scrubbed back shows the past, and its
// buttons wait until it is Live again. Text from the API is set with
// textContent (h() below), never parsed as HTML.

const BOARD_DAYS = 7;
const $ = (s) => document.querySelector(s);
const enc = encodeURIComponent;
const phone = () => innerWidth <= 760;
const clock = (iso) => (iso ? new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) : '?');
// endOf is when a turn ended: it began at `at` and took `ms`.
const endOf = (x) => (x.at ? new Date(Date.parse(x.at) + (x.ms ?? 0)).toISOString() : null);
const ago = (iso) => {
  if (!iso) return '?';
  const m = Math.round((Date.now() - Date.parse(iso)) / 60e3);
  return m < 1 ? 'now' : m < 60 ? `${m}m` : m < 1440 ? `${Math.round(m / 60)}h` : `${Math.round(m / 1440)}d`;
};
const unk = (v, f = (x) => x) => (v == null ? '?' : f(v));
const nowISO = () => new Date().toISOString();

// h('div', { class: 'x', onclick }, 'text', child…): text is always a text node.
function h(tag, attrs = {}, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs ?? {})) {
    if (v == null || v === false) continue;
    if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else if (k === 'class') el.className = v;
    else if (k === 'dataset') Object.assign(el.dataset, v);
    else if (k in el && typeof v !== 'string') el[k] = v;
    else el.setAttribute(k, v === true ? '' : v);
  }
  for (const k of kids.flat()) if (k != null && k !== false) el.append(k instanceof Node ? k : document.createTextNode(String(k)));
  return el;
}

// ---- The API -------------------------------------------------------------------

function request(path, init) {
  return fetch(path, init)
    .catch(() => {
      throw Object.assign(new Error('the dashboard is not answering'), { status: 0 });
    })
    .then((r) =>
      r.json().then(
        (body) => {
          if (!r.ok) throw Object.assign(new Error(body.error || body.reason || `the dashboard answered ${r.status}`), { status: r.status });
          return body;
        },
        () => {
          throw Object.assign(new Error(`the dashboard answered ${r.status} with something that is not JSON`), { status: r.status });
        },
      ),
    );
}
const api = (path) => request(path, { method: 'GET', headers: { accept: 'application/json' } });
// POST /api/actions: a JSON body to this origin is what the server asks of a write.
const press = (body) => request('/api/actions', { method: 'POST', headers: { accept: 'application/json', 'content-type': 'application/json' }, body: JSON.stringify(body) });

// ---- Renderer, camera ---------------------------------------------------------

const canvas = $('#c');
const renderer = new THREE.WebGLRenderer({ canvas, antialias: true });
renderer.setPixelRatio(Math.min(devicePixelRatio, 2));
renderer.outputColorSpace = THREE.SRGBColorSpace;
renderer.toneMapping = THREE.NeutralToneMapping;
renderer.shadowMap.enabled = true;
renderer.shadowMap.type = THREE.PCFShadowMap;
const camera = new THREE.PerspectiveCamera(30, 1, 0.5, 600);
const controls = new OrbitControls(camera, canvas);
controls.enableDamping = true;
controls.maxPolarAngle = 1.35;
// Mascots face +z; keep the view in front of them.
const CITY_LIMITS = { minDistance: 8, minAzimuthAngle: -1.1, maxAzimuthAngle: 1.1 };
const ROOM_LIMITS = { minDistance: 6, maxDistance: 34, minAzimuthAngle: -0.9, maxAzimuthAngle: 0.9 };

// ---- State -------------------------------------------------------------------

const S = {
  ws: null, // GET /api/workspace
  tasks: new Map(), // project → task rows
  inbox: new Map(), // project → inbox rows (what still waits)
  acts: new Map(), // project → its buttons (GET /api/actions)
  readOnly: '', // why this dashboard cannot act, when it cannot
  news: new Map(), // project → its news (GET /api/news)
  duty: new Map(), // project → { logged, error, rows } (GET /api/duty, the Mate's duty board)
  dutyAll: new Set(), // projects whose okayed rows are all shown
  dutyOpen: new Set(), // duty row keys whose notes are open
  chats: new Map(), // project → GET /chat
  forks: new Map(), // topic key → GET /forks/{news}
  peeks: new Map(), // project/crew → { text, at, err }
  diffs: new Map(), // project/crew → GET …/diff, or a promise of it
  plugins: new Map(), // project → its apps
  timelineStale: [], // GET /api/events' timeline_stale
  stale: '',
  staleSince: null,
  sel: null, // { kind: 'mate'|'crew'|'hall'|'tent'|'system'|'goto'|'add'|'lot'|'block'|'create', project, crew, tab, topic, back }
  busy: new Map(), // row key → label running
  results: new Map(), // row key → { kind, text, at }
  drafts: new Map(), // input id → text
  pending: new Map(), // send id → { text, left, timer } waiting out its undo
  folded: new Map(), // project → show earlier chat turns
  flashes: [], // { anchor, kind, glyph, text, until }
};
const rowKey = (project, crew) => (crew ? `${project}/${crew}` : project);
const projectOf = (name) => S.ws?.projects.find((p) => p.name === name);
const taskOf = (project, crew) => S.tasks.get(project)?.find((t) => t.crew === crew);
const openCrews = (project) => (S.tasks.get(project) ?? []).filter((t) => !t.closed && t.state !== 'gone');
const hasMate = (p) => Boolean(p?.mate?.harness);
const topicKey = (project, id) => `${project}#${id}`;

let scene = null;
let city = null;
let shape = ''; // the projects and repos the city was built for
let home = null;
let lastEventID = 0;
const start = performance.now();
const now = () => (performance.now() - start) / 1000; // the city's clock, in seconds

// The project's repos as the city draws them: none means one tent named after it.
const tentRepos = (p) => (p.repos?.length ? p.repos : [p.name]);
// Whether a task row worked in the tent: its repo's, or the project's first
// tent when its repo is unknown or no longer in project.yaml.
function onTent(p, repo) {
  const repos = tentRepos(p);
  return (t) => t.repo === repo || (repo === repos[0] && !repos.includes(t.repo));
}

function buildCity(projects) {
  if (city) {
    scene.remove(city.group);
    city.dispose();
    scene.environment?.dispose();
  }
  city = createCity(projects.map((p) => ({ name: p.name, repos: p.repos ?? [], mate: { harness: p.mate?.harness } })));
  scene = new THREE.Scene();
  scene.background = new THREE.Color(PALETTE.paper);
  const k = city.size / 3;
  scene.fog = new THREE.Fog(PALETTE.paper, 170 * k, 320 * k);
  lights(scene, renderer, 27 * k);
  scene.add(city.group, ring, crate);
  // A phone is tall and narrow: stand further back so the whole island fits across.
  home = { target: new THREE.Vector3(0, 1, 1), position: new THREE.Vector3(-20, 62, 70).multiplyScalar(k * (phone() ? 1.9 : 1)) };
  controls.maxDistance = 170 * k;
  if (!room && !S.sel) {
    controls.target.copy(home.target);
    camera.position.copy(home.position);
  }
  $('#hidden').textContent = city.hidden ? `${city.hidden} more project${city.hidden === 1 ? '' : 's'} than the city has blocks · Go to (/) reaches them` : '';
}

// The selection ring on the ground, and the crate a merge carries.
const ring = new THREE.Mesh(new THREE.TorusGeometry(0.75, 0.06, 8, 48), new THREE.MeshBasicMaterial({ color: PALETTE.coral, toneMapped: false }));
ring.rotation.x = -Math.PI / 2;
ring.visible = false;
const crate = new THREE.Mesh(new THREE.BoxGeometry(0.45, 0.35, 0.45), new THREE.MeshStandardMaterial({ color: '#C99A62', roughness: 0.8 }));
crate.castShadow = true;
crate.visible = false;
let crateFlight = null;

// ---- Reading ---------------------------------------------------------------------
//
// The city is drawn as soon as /api/workspace answers; each project fills in
// as its own page arrives. The buttons (one snapshot for every project), the
// news and the board are read beside them and never hold the city up.

let board = null;
let boardAt = -Infinity;
let boardBusy = false;
const BOARD_EVERY = 5 * 60e3; // the board counts days; minutes-old numbers are fine
const loaded = new Set(); // projects whose crews are placed; a new crew there says its name

function applyProject(p, t) {
  const rows = S.tasks.get(p.name);
  if (!rows || !city) return;
  city.setCrews(p.name, rows.filter((r) => r.state !== 'gone'), t, { fresh: loaded.has(p.name) });
  for (const repo of tentRepos(p)) city.setMerged(p.name, repo, rows.filter((r) => r.role === 'ship' && r.merged_at && onTent(p, repo)(r)).length, t);
  loaded.add(p.name);
}

function applyHall() {
  if (!city) return;
  city.setHall((S.ws?.projects ?? []).map((p) => {
    const bp = board?.projects?.find((x) => x.name === p.name);
    return { name: p.name, running: Boolean(p.mate?.running), working: openCrews(p.name).length, tokens: bp?.days?.at(-1)?.tokens?.total ?? p.mate?.tokens_today ?? 0 };
  }));
}

function applyBoard(t) {
  for (const p of S.ws?.projects ?? []) {
    const bp = board?.projects?.find((x) => x.name === p.name);
    city.setBoard(p.name, bp ? boardStats(bp, board.days) : null, t);
  }
  applyHall();
}

function boardStats(bp, days) {
  const s = { days, tokens: 0, finished: 0, passed: 0 };
  for (const d of bp.days ?? []) {
    s.tokens += d.tokens?.total ?? 0;
    s.finished += d.closed?.finished ?? 0;
    s.passed += d.reviews?.pass ?? 0;
  }
  return s;
}

/**
 * Read the workspace, redraw the city if its projects or repos changed, then
 * read the pages of `only` (a set of project names; every project when null),
 * applying each as it arrives, and the buttons of every project.
 */
async function loadAll(only = null) {
  const body = await api('/api/workspace');
  S.ws = body;
  // Polling from the id the workspace was built at can only read an event twice, never miss one.
  lastEventID = body.last_event_id ?? 0;
  const projects = body.projects ?? [];
  const next = JSON.stringify(projects.map((p) => [p.name, p.repos ?? [], p.mate?.harness ?? '']));
  const t = now();
  if (next !== shape) {
    shape = next;
    buildCity(projects);
    loaded.clear();
    for (const p of projects) applyProject(p, t);
    if (board) applyBoard(t);
    only = null;
  }
  for (const name of [...S.tasks.keys()]) if (!projectOf(name)) S.tasks.delete(name);
  for (const p of projects) city.setMate(p.name, p.mate ?? {}, t);
  applyHall();
  if (!S.stale) setLive('live', 'live');
  $('#boot').hidden = true;
  render();
  await Promise.all([
    ...projects.filter((p) => !only || only.has(p.name)).map((p) =>
      api(`/api/projects/${enc(p.name)}`).then((page) => {
        if (S.ws !== body) return;
        S.tasks.set(p.name, page.tasks ?? []);
        S.inbox.set(p.name, page.inbox ?? []);
        applyProject(p, now());
      }, () => {}),
    ),
    loadActions(),
  ]);
  if (S.ws === body) {
    applyHall();
    if (room?.project && (!only || only.has(room.project))) refreshRoom();
    render();
  }
}

async function loadActions() {
  try {
    const body = await api('/api/actions');
    S.readOnly = body.read_only ?? '';
    S.acts = new Map(Object.entries(body.projects ?? {}));
  } catch {
    // The buttons stay as they were; the stale band says why.
  }
}

function refreshBoard() {
  if (boardBusy || performance.now() - boardAt < BOARD_EVERY) return;
  boardBusy = true;
  boardAt = performance.now();
  api(`/api/board?days=${BOARD_DAYS}`)
    .then((body) => {
      board = body;
      if (city) applyBoard(now());
    }, () => (boardAt = -Infinity))
    .finally(() => (boardBusy = false));
}

// The news is a file the Mate's REPL writes, not an event: it is read again
// on every answer of the long poll.
let newsBusy = false;
function refreshNews() {
  if (newsBusy) return;
  newsBusy = true;
  api('/api/news')
    .then((body) => {
      S.news = new Map((body.projects ?? []).map((p) => [p.project, p.news ?? []]));
      city?.setMailbox(topics().filter((x) => x.group === 'waiting').length, now());
      render();
    }, () => {})
    .finally(() => (newsBusy = false));
}

// The Mate's duty board (CON-158): what the captain asked it for and what
// it proposed, with where each row stands. Like the news it is a file the
// Mate writes, read again on every answer of the long poll. It is a report,
// never a question: nothing on it is a topic, so nothing on it reaches the
// Needs-you rail, the pins or City Hall (CON-154).
let dutyBusy = false;
function refreshDuty() {
  if (dutyBusy) return;
  dutyBusy = true;
  api('/api/duty')
    .then((body) => {
      S.duty = new Map((body.projects ?? []).map((p) => [p.project, { logged: Boolean(p.logged), error: p.error ?? '', rows: p.duty ?? [] }]));
      render();
    }, () => {})
    .finally(() => (dutyBusy = false));
}
const dutyRows = (project) => S.duty.get(project)?.rows ?? [];
// open: rows still on the board (not okayed); done: rows waiting for the captain's tick.
const dutyCounts = (project) => ({ open: dutyRows(project).filter((x) => x.status !== 'okayed').length, done: dutyRows(project).filter((x) => x.status === 'done').length });

// Every news as a topic: waiting on the captain (`new`, `waiting`), the Mate
// on it (`working`), or earlier (`done`, `dropped`). Archiving hides an
// answered news; one still waiting on the captain stays, as in the REPL.
const GROUP = { new: 'waiting', waiting: 'waiting', working: 'on_it', done: 'earlier', dropped: 'earlier' };
function topics() {
  const out = [];
  for (const [project, list] of S.news) {
    if (!projectOf(project)) continue;
    for (const n of list) {
      const group = GROUP[n.status] ?? 'earlier';
      if (!n.archived || group === 'waiting') out.push({ ...n, project, key: topicKey(project, n.id), group });
    }
  }
  return out.sort((a, b) => (b.at ?? '').localeCompare(a.at ?? ''));
}
const topicOf = (key) => topics().find((x) => x.key === key);

function setLive(state, text) {
  const pill = $('#live');
  pill.dataset.state = state;
  pill.querySelector('.g').textContent = state === 'stale' ? '▲' : '●';
  pill.querySelector('.t').textContent = text;
}

// One long poll on /api/events, as the admin UI and Office keep themselves
// live. An event reloads the workspace and the pages of the projects it
// names; every answer, quiet or not, reads the news, the open chat and the
// buttons again, and the board once it is BOARD_EVERY old.
async function poll() {
  for (;;) {
    try {
      const body = await api(`/api/events?since=${lastEventID}&wait=20`);
      if (S.stale) {
        S.stale = '';
        S.staleSince = null;
      }
      S.timelineStale = body.timeline_stale ?? [];
      const id = body.last_event_id ?? 0;
      const moved = [...(body.events ?? []), ...(body.now ?? [])];
      // A smaller id is a `mate reindex` under us: the old cursor will never come round again.
      if (id < lastEventID) await loadAll();
      else if (moved.length) {
        const names = new Set(moved.map((x) => x.project));
        await loadAll(names.has('') || names.has(undefined) ? null : names);
      } else {
        lastEventID = id;
        await loadActions();
        setLive('live', 'live');
        render();
      }
      refreshNews();
      refreshDuty();
      refreshBoard();
      refreshOpen();
    } catch (e) {
      if (!S.stale) S.staleSince = nowISO();
      S.stale = e.message;
      setLive('stale', `stale · ${e.message}`);
      render();
      await new Promise((r) => setTimeout(r, 3000));
    }
  }
}

// What the open panel reads besides the poll: the Mate's chat while it is
// open, and a fork while it answers.
function refreshOpen() {
  const sel = S.sel;
  if (sel?.kind === 'mate' && (sel.tab ?? 'chat') === 'chat') loadChat(sel.project);
  if (sel?.kind === 'hall' && sel.topic && S.forks.has(sel.topic)) loadFork(sel.topic);
}

// ---- What needs the captain ---------------------------------------------------------
//
// On the web only a topic needs the captain (CON-154): a news the Mate
// opened and the captain has not answered, as Office's captain desk counts
// it. An incident, a crew ready to merge or a crew asking its Mate belongs
// to its tent: Crews here says it, and the Mate in the tent opens its chat
// and inbox.

// What the page and the buttons say about one open crew.
function crewFacts(project, t) {
  const mine = (S.inbox.get(project) ?? []).filter((x) => x.crew === t.crew);
  const acts = S.acts.get(project);
  const merge = acts?.crews?.[t.crew]?.find((b) => b.action === 'merge');
  const assign = (acts?.crew_more?.[t.crew] ?? []).filter((b) => b.action === 'assign');
  return {
    incident: mine.find((x) => x.kind === 'incident'),
    status: [...mine].reverse().find((x) => x.kind === 'status'),
    ready: merge ? merge.available : t.state === 'waiting_review',
    assign,
    open: assign.find((b) => b.available),
  };
}

function attention() {
  return topics().filter((x) => x.group === 'waiting').map((tp) => ({
    title: tp.question || tp.title,
    sub: `${tp.project} · the Mate asks you · ${ago(tp.opened_at)}`,
    open: () => select({ kind: 'hall', tab: 'topics', topic: tp.key }),
  }));
}

function renderRail(items) {
  $('#needs-n').textContent = items.length ? `Needs you · ${items.length}` : 'Nothing needs you';
  $('#needs').dataset.tone = items.length ? 'need' : 'ok';
  $('#rail').replaceChildren(h('h2', {}, items.length ? `Needs you · ${items.length}` : 'Needs you'),
    ...(items.length ? items.map((x) => h('button', { type: 'button', class: 'k-topic', onclick: x.open },
      h('span', { class: 'g', 'aria-hidden': 'true' }, '?'), h('b', {}, x.title), h('small', {}, x.sub))) : [h('p', { class: 'empty' }, 'Nothing waits on you.')]));
}

// ---- Pins ---------------------------------------------------------------------------

const pinsEl = $('#pins');
const pinEls = new Map();
function anchorPos(anchor, out) {
  if (!city) return null;
  if (anchor === 'hall') {
    city.hall[0].getWorldPosition(out);
    return out.setY(out.y + 2.1);
  }
  const i = anchor.indexOf(':');
  const kind = anchor.slice(0, i);
  const rest = anchor.slice(i + 1);
  if (kind === 'crew') {
    const m = city.mascots.get(rest);
    return m?.root.visible ? out.copy(m.root.position).setY(m.root.position.y + 1.45) : null;
  }
  if (kind === 'mate') {
    const m = city.mascots.get(`${rest}:mate`);
    if (m?.root.visible) return out.copy(m.root.position).setY(m.root.position.y + 1.85);
    const b = city.blocks.get(rest);
    return b ? out.set(b.centre.x, 2.4, b.centre.z) : null;
  }
  if (kind === 'tent') {
    const b = city.tents.get(rest);
    return b ? out.set(b.x - b.w / 2 + 0.7, 1.6, b.z + b.d / 2 + 0.45) : null;
  }
  if (kind === 'block') {
    const b = city.blocks.get(rest);
    return b ? out.set(b.centre.x, 3.2, b.centre.z) : null;
  }
  return null;
}

function wantedPins() {
  const pins = new Map(); // anchor → pin
  const put = (anchor, pin) => {
    const have = pins.get(anchor);
    if (!have || pin.rank < have.rank) pins.set(anchor, pin);
  };
  const waiting = attention().length;
  if (waiting) put('hall', { kind: 'topic', rank: 1, glyph: '?', long: `${waiting} waiting on you`, short: String(waiting), label: `City Hall: ${waiting} topic${waiting === 1 ? '' : 's'} waiting on you`, open: () => select({ kind: 'hall', tab: 'topics' }) });
  for (const p of S.ws?.projects ?? []) {
    if (!hasMate(p)) put(`mate:${p.name}`, { kind: 'empty', rank: 5, glyph: '+', long: 'No Mate yet', short: 'no Mate', label: `${p.name} has no Mate yet`, open: () => select({ kind: 'mate', project: p.name }) });
  }
  // Running and just-finished actions sit over their thing and win.
  for (const [key, label] of S.busy) {
    const [project, crew] = key.split('/');
    const anchor = crew ? `crew:${key}` : projectOf(project) ? `mate:${project}` : 'hall';
    put(anchor, { kind: 'run', rank: -2, glyph: '⟳', long: `Running ${label.replace(/…$/, '').toLowerCase()}…`, short: '…', label: `Running ${label}`, open: () => select(crew ? { kind: 'crew', project, crew } : projectOf(project) ? { kind: 'mate', project } : { kind: 'hall', tab: 'projects' }) });
  }
  const t = performance.now();
  S.flashes = S.flashes.filter((f) => f.until > t);
  for (const f of S.flashes) put(f.anchor, { kind: f.kind, rank: -1, glyph: f.glyph, long: f.text, short: f.glyph, label: f.text, shake: f.kind === 'bad' || f.kind === 'refused', open: () => {} });
  return pins;
}

let pinsKey = '';
function renderPins() {
  const want = room ? new Map() : wantedPins();
  const key = JSON.stringify([...want].map(([a, p]) => [a, p.kind, p.long]));
  if (key === pinsKey) return;
  pinsKey = key;
  for (const [a, el] of pinEls) {
    if (!want.has(a)) {
      el.remove();
      pinEls.delete(a);
    }
  }
  for (const [a, p] of want) {
    const el = h('button', { type: 'button', class: `pin k-${p.kind}${p.shake ? ' shake' : ''}`, 'aria-label': p.label, onclick: p.open },
      h('span', { class: 'g', 'aria-hidden': 'true' }, p.glyph), h('span', { class: 'w' }, p.long));
    el.dataset.short = p.short ?? '';
    if (pinEls.has(a)) pinEls.get(a).replaceWith(el);
    else pinsEl.append(el);
    pinEls.set(a, el);
  }
}

const v = new THREE.Vector3();
function placePins() {
  const w = canvas.clientWidth;
  const hh = canvas.clientHeight;
  for (const [a, el] of pinEls) {
    if (!anchorPos(a, v)) {
      el.style.display = 'none';
      continue;
    }
    const dist = v.distanceTo(camera.position);
    v.project(camera);
    el.style.display = v.z > 1 ? 'none' : '';
    el.style.transform = `translate(${((v.x + 1) / 2) * w}px, ${((1 - v.y) / 2) * hh}px) translate(-50%, calc(-100% - 6px))`;
    // Far away a pin keeps its glyph and drops its words; the rail and the label still say them.
    el.classList.toggle('far', dist > 70 && !el.classList.contains('k-run'));
    el.classList.toggle('dim', Boolean(S.sel) && !el.matches(':focus') && !isSelected(a));
  }
}
const isSelected = (a) => S.sel && ((S.sel.kind === 'crew' && a === `crew:${S.sel.project}/${S.sel.crew}`) || (S.sel.kind === 'mate' && a === `mate:${S.sel.project}`) || (S.sel.kind === 'hall' && a === 'hall'));

function flash(anchor, kind, glyph, text, ms = 3500) {
  S.flashes.push({ anchor, kind, glyph, text, until: performance.now() + ms });
  setTimeout(renderPins, ms + 50);
}

// ---- Camera ------------------------------------------------------------------------

let flight = null;
function fly(target, position) {
  flight = { from: [controls.target.clone(), camera.position.clone()], to: [target, position], t0: performance.now() };
}
function focusOn(sel) {
  if (!city || room) return;
  const p = new THREE.Vector3();
  let dist = [0, 24, 32];
  if (sel.kind === 'crew' && anchorPos(`crew:${sel.project}/${sel.crew}`, p)) p.y -= 1.2;
  else if ((sel.kind === 'mate' || sel.kind === 'create') && anchorPos(`mate:${sel.project}`, p)) p.y -= 1.6;
  else if (sel.kind === 'hall' || sel.kind === 'system') {
    anchorPos('hall', p);
    p.y = 1.5;
    dist = [0, 16, 24];
  } else if (sel.kind === 'lot' && sel.at) {
    p.copy(sel.at);
    dist = [0, 16, 22];
  } else if (sel.kind === 'block' && anchorPos(`block:${sel.project}`, p)) {
    p.y = 0.5;
    dist = [0, 16, 22];
  } else return;
  fly(p.clone(), p.clone().add(new THREE.Vector3(...dist)));
  ring.visible = sel.kind === 'crew' || sel.kind === 'mate';
  if (ring.visible) {
    ring.position.set(p.x, 0.2, p.z);
    ring.scale.setScalar(sel.kind === 'mate' ? 1.15 : 0.85);
  }
}

// ---- Selection, the dock --------------------------------------------------------------

const dock = $('#dock');
const dbody = $('#dbody');
let lastFocus = null;

function select(sel, { fly: doFly = true } = {}) {
  if (room && sel.kind !== 'crew' && sel.kind !== 'tent' && sel.back?.kind !== 'tent') leaveRoom();
  if (!S.sel) lastFocus = document.activeElement;
  // Chatter never talks over the thing you are working with.
  for (const m of city?.mascots.values() ?? []) if (m.chatAt === Infinity) m.chatAt = undefined;
  const id = sel.kind === 'crew' ? `${sel.project}/${sel.crew}` : sel.kind === 'mate' ? `${sel.project}:mate` : null;
  if (id && city?.mascots.get(id)) city.mascots.get(id).chatAt = Infinity;
  S.sel = sel;
  document.body.classList.add('docked');
  tip.style.display = 'none';
  if (phone() && !document.body.classList.contains('sheet-full')) document.body.classList.remove('sheet-peek');
  resize();
  if (doFly) focusOn(sel);
  render();
  $('#dock-title').focus({ preventScroll: true });
  // What the panel reads for itself.
  if (sel.kind === 'mate' && (sel.tab ?? 'chat') === 'chat') loadChat(sel.project);
  if (sel.kind === 'mate' && sel.tab === 'apps') loadPlugins(sel.project);
  if (sel.kind === 'mate' && sel.tab === 'duty') refreshDuty();
  if (sel.kind === 'crew' && sel.tab === 'peek') loadPeek(sel.project, sel.crew);
  if (sel.kind === 'crew' && sel.tab === 'diff') loadDiff(sel.project, sel.crew);
  if (sel.kind === 'hall' && sel.topic && topicOf(sel.topic)) loadFork(sel.topic);
}

function closeDock() {
  if (room && S.sel?.back?.kind === 'tent') return select(S.sel.back);
  if (room) return leaveRoom();
  if (S.sel?.back) return select(S.sel.back);
  for (const m of city?.mascots.values() ?? []) if (m.chatAt === Infinity) m.chatAt = undefined;
  S.sel = null;
  ring.visible = false;
  document.body.classList.remove('docked', 'sheet-peek', 'sheet-full');
  resize();
  render();
  lastFocus?.focus?.();
}
$('#close').addEventListener('click', closeDock);
$('#grab').addEventListener('click', () => {
  const b = document.body.classList;
  if (b.contains('sheet-peek')) b.remove('sheet-peek');
  else if (b.contains('sheet-full')) {
    b.remove('sheet-full');
    b.add('sheet-peek');
  } else b.add('sheet-full');
  resize();
});

function render() {
  renderRail(attention());
  renderPins();
  renderSystemChip();
  if (!S.sel) return;
  // Keep what the captain is typing, and where they are, across a redraw.
  const focusId = document.activeElement?.id;
  const caret = document.activeElement?.selectionStart;
  const scroll = dbody.scrollTop;
  const view = VIEWS[S.sel.kind]?.() ?? { title: '?', body: [] };
  $('#crumbs').replaceChildren(...(view.crumbs ?? []).flatMap((c, i) => [...(i ? [h('span', { 'aria-hidden': 'true' }, '›')] : []), c.go ? h('button', { type: 'button', onclick: c.go }, c.text) : h('span', {}, c.text)]));
  $('#dock-title').textContent = view.title;
  $('#dock-sub').textContent = view.sub ?? '';
  dbody.replaceChildren(...view.body.filter(Boolean));
  dbody.scrollTop = scroll;
  if (focusId && focusId !== 'dock-title') {
    const el = document.getElementById(focusId);
    if (el) {
      el.focus({ preventScroll: true });
      if (caret != null && el.setSelectionRange) el.setSelectionRange(caret, caret);
    }
  }
}

// ---- Shared pieces of a panel ----------------------------------------------------------

const STATE = {
  at_desk_working: ['▶', 'working', 'ok'],
  waiting_at_ceo: ['◆', 'asking its Mate', 'need'],
  walking_to_ceo: ['◆', 'asking its Mate', 'need'],
  waiting_review: ['⇢', 'in review', 'need'],
  blocked: ['■', 'blocked', 'warn'],
  asleep: ['◌', 'asleep', 'off'],
  idle: ['○', 'idle', 'off'],
  arriving: ['›', 'arriving', 'ok'],
  leaving: ['‹', 'leaving', 'off'],
  gone: ['✕', 'gone', 'off'],
};
const stateWord = (state) => STATE[state]?.[1] ?? (state ? state.replace(/_/g, ' ') : '?');
function stateEl(state, override) {
  const [g, word, tone] = override ?? STATE[state] ?? ['?', state ? state.replace(/_/g, ' ') : '?', 'off'];
  return h('span', { class: 'state', dataset: { tone } }, h('span', { class: 'g', 'aria-hidden': 'true' }, g), word);
}
function facts(rows) {
  return h('dl', { class: 'facts' }, rows.map(([k, val]) => h('div', {}, h('dt', {}, k), h('dd', { class: val === '?' ? 'unk' : '' }, val))));
}
function bands(extra = []) {
  const out = [];
  if (S.stale) out.push(h('div', { class: 'band stale', role: 'status' }, h('span', { class: 'g' }, '▲'), h('span', {}, `Stale since ${clock(S.staleSince)}: ${S.stale}. What you see may be old; actions wait until the city is live again.`)));
  return [...out, ...extra];
}
function resultEl(key) {
  const r = S.results.get(key);
  if (!r) return null;
  const glyph = { ok: '✓', refused: '⊘', failed: '✕', info: 'i' }[r.kind];
  const lead = { ok: '', refused: 'Refused, nothing changed: ', failed: 'Ran and failed: ', info: '' }[r.kind];
  return h('div', { class: 'result', dataset: { kind: r.kind }, role: 'status', 'aria-live': 'polite' }, h('span', { class: 'g', 'aria-hidden': 'true' }, glyph),
    h('span', {}, h('b', {}, lead), r.text, h('span', { style: 'opacity:.7' }, ` · ${clock(r.at)}`)));
}
// A refusal (409, or a press the page never sent) changed nothing; anything
// else from the server ran and failed.
const outcome = (e) => (e.status === 422 ? 'failed' : 'refused');
const readOnlyBand = () => h('div', { class: 'band ro' }, h('span', { class: 'g' }, '⊘'), h('span', {}, `Read-only: ${S.readOnly.replace(/^this dashboard is read-only: /, '')}.`));

// Why a button cannot run right now, beyond what the API says: the page's own state.
function blockedBy(key) {
  if (S.readOnly) return 'read-only dashboard';
  if (S.stale) return `stale since ${clock(S.staleSince)}; wait for live`;
  if (room && mode !== 'live') return 'replaying; go Live to act';
  const running = S.busy.get(key);
  if (running) return `waiting: ${running.replace(/…$/, '')} is running`;
  return '';
}

/**
 * The Actions block: the API's buttons in its order, each with its reason
 * line kept even when empty, so nothing moves when a button changes state.
 */
function actionsBlock({ project, crew, buttons, extra = [], links = [] }) {
  const key = rowKey(project, crew);
  if (S.readOnly) return [h('h3', { class: 'sec' }, 'Actions'), readOnlyBand(), links.length ? h('div', { class: 'links' }, links) : null];
  const running = S.busy.get(key);
  const page = blockedBy(key);
  const els = [...(buttons ?? []), ...extra].map((b) => {
    const isRunning = running && running === b.label;
    const why = isRunning ? 'running…' : b.toggle ? (b.available ? '' : b.reason) : page || (b.available ? '' : b.reason);
    const disabled = isRunning || (b.toggle ? !b.available : Boolean(page) || !b.available);
    const danger = Boolean(b.confirm) && b.action !== 'merge';
    return h('div', { class: 'act' },
      h('button', { type: 'button', id: `act-${key}-${b.action}`, class: `${isRunning ? 'running' : ''} ${danger ? 'danger' : ''} ${b.action === 'merge' ? 'primary' : ''}`, disabled, 'aria-pressed': b.toggle ? String(Boolean(b.pressed)) : null, 'aria-describedby': `why-${key}-${b.action}`, onclick: () => (b.run ? b.run() : pressButton(project, crew, b)) },
        isRunning ? h('span', { class: 'g', 'aria-hidden': 'true' }, '⟳') : null, isRunning ? `Running ${b.label.replace(/…$/, '').toLowerCase()}…` : b.label),
      h('small', { id: `why-${key}-${b.action}` }, why));
  });
  return [h('h3', { class: 'sec' }, 'Actions', crew ? null : h('span', { class: 'r' }, projectOf(project)?.mode === 'auto' ? '● auto' : '○ manual')), h('div', { class: 'actions' }, els), resultEl(key), links.length ? h('div', { class: 'links' }, links) : null];
}

// A destructive press asks first, Cancel focused; then runs and reports one line.
const TITLES = {
  'crew:stop': (p, t) => [`Stop crew ${t.crew}?`, `${p}/${t.crew} · worktree and branch ${t.branch ?? '?'}`, 'Stop crew'],
  'crew:restart_crew': (p, t) => [`Restart crew ${t.crew}?`, `${p}/${t.crew} · same worktree, same branch`, 'Restart crew'],
  'crew:merge': (p, t) => [`Merge ${t.branch ?? t.crew}?`, `${p}/${t.crew} · fast-forward only · the crew finishes`, 'Merge'],
  'mate:stop': (p) => [`Stop the Mate of ${p}?`, `${p} · the Mate only · ${openCrews(p).length} crews keep running`, 'Stop mate'],
  'mate:restart_mate': (p) => [`Restart the Mate of ${p}?`, `${p} · the Mate only · ${openCrews(p).length} crews keep running`, 'Restart mate'],
};
function pressButton(project, crew, b) {
  if (b.action === 'onboard') return select({ kind: 'create', project, back: { kind: 'mate', project } });
  const t = crew ? taskOf(project, crew) : null;
  const title = TITLES[`${crew ? 'crew' : 'mate'}:${b.action}`]?.(project, t);
  const go = () => run(rowKey(project, crew), b.label, { action: b.action, project, crew }, crew ? `crew:${project}/${crew}` : `mate:${project}`);
  if (!b.confirm) return go();
  const head = title?.[0] ?? `${b.label.replace(/…$/, '')}?`;
  // The console's question often opens with the title's own words: say them once.
  const body = b.confirm.startsWith(head) ? b.confirm.slice(head.length).trim() : b.confirm;
  confirmBox({ title: head, scope: title?.[1], body, yes: title?.[2] ?? b.label.replace(/…$/, ''), onYes: go });
}

async function run(key, label, req, anchor, after) {
  S.busy.set(key, label);
  S.results.delete(key);
  render();
  if (req.action === 'merge') startCrate(req.project, req.crew);
  try {
    const { line } = await press(req);
    S.results.set(key, { kind: 'ok', text: line, at: nowISO() });
    const t = req.crew && taskOf(req.project, req.crew);
    if (req.action === 'merge' && t) flash(`tent:${req.project}/${t.repo || tentRepos(projectOf(req.project))[0]}`, 'ok', '✓', `merged ${req.crew}`, 5000);
    else flash(anchor, 'ok', '✓', 'done');
    after?.(true, line);
  } catch (e) {
    const kind = outcome(e);
    S.results.set(key, { kind, text: e.message, at: nowISO() });
    flash(anchor, kind === 'failed' ? 'bad' : 'refused', '✕', kind === 'failed' ? 'failed' : 'refused');
    if (req.action === 'merge') crateBack();
    after?.(false, e.message);
  } finally {
    S.busy.delete(key);
    await loadAll().catch(() => render());
  }
}

const scrim = $('#scrim');
function confirmBox({ title, scope, body, list, yes, onYes }) {
  const back = document.activeElement;
  const close = () => {
    scrim.classList.remove('on');
    scrim.replaceChildren();
    back?.focus?.();
  };
  const cancel = h('button', { type: 'button', class: 'btn', onclick: close }, 'Cancel');
  const box = h('div', { class: 'confirm', role: 'alertdialog', 'aria-modal': 'true', 'aria-labelledby': 'cf-t', 'aria-describedby': 'cf-b' },
    h('h2', { id: 'cf-t' }, title),
    scope ? h('div', { class: 'scope' }, scope) : null,
    h('p', { id: 'cf-b' }, body),
    list ? h('ul', {}, list.map((x) => h('li', {}, x))) : null,
    h('div', { class: 'row' }, cancel, h('button', { type: 'button', class: 'btn coral', onclick: () => (close(), onYes()) }, yes)));
  box.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') {
      e.stopPropagation();
      close();
    }
    if (e.key === 'Tab') {
      const f = [...box.querySelectorAll('button')];
      const i = f.indexOf(document.activeElement);
      e.preventDefault();
      f[(i + (e.shiftKey ? -1 : 1) + f.length) % f.length].focus();
    }
  });
  scrim.replaceChildren(box);
  scrim.classList.add('on');
  cancel.focus();
}

// A send with a short undo: the text waits `left` seconds, then goes.
function sendLater(id, text, go) {
  const p = { text, left: 5 };
  p.timer = setInterval(() => {
    p.left--;
    if (p.left <= 0) {
      clearInterval(p.timer);
      S.pending.delete(id);
      go(text);
    }
    render();
  }, 1000);
  S.pending.set(id, p);
  render();
}
function undo(id, draftId) {
  const p = S.pending.get(id);
  if (!p) return;
  clearInterval(p.timer);
  S.pending.delete(id);
  // A chat or an answer goes back to its draft; a tick has none to go back to.
  if (draftId) S.drafts.set(draftId, p.text);
  render();
  if (draftId) document.getElementById(draftId)?.focus();
}
function textarea(id, attrs = {}) {
  const el = h('textarea', { id, ...attrs, oninput: attrs.oninput ?? ((e) => S.drafts.set(id, e.target.value)) });
  el.value = S.drafts.get(id) ?? '';
  return el;
}

// ---- Views ----------------------------------------------------------------------------

const VIEWS = {};

function tabs(list, current, set) {
  return h('div', { class: 'tabs', role: 'tablist' }, list.map(([id, label, n]) => h('button', { type: 'button', role: 'tab', id: `tab-${id}`, 'aria-selected': String(current === id), onclick: () => set(id) }, label, n ? h('span', { class: 'n' }, String(n)) : null)));
}
const setTab = (tab) => select({ ...S.sel, tab }, { fly: false });

const officeMate = (p) => `../office/#mate/${enc(p)}`;
const officeCrew = (p, c) => `../office/#crew/${enc(p)}/${enc(c)}`;
const adminTask = (p, c) => `../#/p/${enc(p)}/t/${enc(c)}`;
const ext = (href, text, title) => h('a', { href, target: '_blank', rel: 'noopener', title }, text);

// The Mate, at its plaza.
VIEWS.mate = () => {
  const p = projectOf(S.sel.project);
  if (!p) return { title: 'Gone', body: [h('p', {}, `${S.sel.project} is no longer in the workspace.`), resultEl('workspace')] };
  const m = p.mate ?? {};
  const acts = S.acts.get(p.name);
  const inbox = S.inbox.get(p.name) ?? [];
  const tab = S.sel.tab ?? 'chat';
  const duty = dutyCounts(p.name);
  const state = !hasMate(p) ? stateEl('', ['+', 'no Mate yet', 'off']) : m.running ? stateEl(m.state) : stateEl('', ['◌', 'stopped', 'off']);
  const tent = S.sel.back?.kind === 'tent' ? S.sel.back : null;
  return {
    crumbs: [{ text: 'City' }, { text: p.name, go: () => select({ kind: 'block', project: p.name }) }, ...(tent ? [{ text: `tent ${tent.repo}`, go: () => select(tent) }] : [])],
    title: `Mate · ${p.name}`,
    // The board's count rides on the sub line; it is not a need (CON-154), so it is nowhere else.
    sub: hasMate(p) ? `${harnessName(m.harness)} · ${tent ? `in tent ${tent.repo}` : `plaza of ${p.name}`}${duty.open ? ` · duty ${duty.open}${duty.done ? ` · ${duty.done} for you` : ''}` : ''}` : 'This block has a plaza but no Mate',
    body: [
      ...bands(),
      facts([
        ['State', state],
        ['Mode', p.mode === 'auto' ? '● auto' : p.mode === 'manual' ? '○ manual' : unk(p.mode)],
        ['Tokens today', hasMate(p) ? unk(m.tokens_today, formatTokens) : '?'],
        ['Context', m.context_pct == null ? '?' : `${Math.round(m.context_pct)}% · ${formatTokens(m.context_tokens ?? 0)}`],
      ]),
      ...actionsBlock({ project: p.name, buttons: acts?.mate ?? [], links: [ext(officeMate(p.name), 'Desk in Office ↗', 'the Mate in Mate Office, in a new tab')] }),
      tabs([['chat', 'Chat'], ['duty', 'Duty', duty.done], ['inbox', 'Inbox', inbox.length], ['apps', 'Apps'], ['project', 'Project']], tab, setTab),
      ...{ chat: chatView, duty: dutyView, inbox: inboxView, apps: appsView, project: projectView }[tab](p),
    ],
  };
};

// The headless Mate's chat (GET /chat), as its REPL shows it: the
// captain's messages and the Mate's replies, nothing the crews or the app
// said and none of the Mate's tools (CON-151). A message waits 5 s for an
// undo, then goes to the Mate as the captain's.
const chatBusy = new Set();
async function loadChat(project) {
  if (chatBusy.has(project)) return;
  chatBusy.add(project);
  try {
    S.chats.set(project, await api(`/api/projects/${enc(project)}/chat`));
  } catch (e) {
    S.chats.set(project, { ...(S.chats.get(project) ?? { items: [] }), error: e.message });
  } finally {
    chatBusy.delete(project);
  }
  render();
  // While the Mate answers, read again until it is done.
  const c = S.chats.get(project);
  if (c?.state?.busy && S.sel?.kind === 'mate' && S.sel.project === project) setTimeout(() => S.sel?.project === project && loadChat(project), 2500);
}

function chatView(p) {
  const c = S.chats.get(p.name);
  const items = c?.items ?? [];
  const pend = S.pending.get(`chat:${p.name}`);
  const say = S.acts.get(p.name)?.mate_more?.find((b) => b.action === 'say');
  const blocked = blockedBy(`${p.name}#chat`) || (!hasMate(p) ? 'no Mate yet · Create mate first' : say && !say.available ? say.reason : !say ? '?' : '');
  const busy = Boolean(c?.state?.busy);
  // Older turns fold to one line; the last two exchanges stay open.
  const from = S.folded.get(p.name) ? 0 : Math.max(0, items.length - 2);
  const list = [
    from ? h('button', { type: 'button', class: 'fold', onclick: () => (S.folded.set(p.name, true), render()) }, `Show ${from} earlier turn${from === 1 ? '' : 's'}`) : null,
    ...items.slice(from).flatMap((x) => [
      h('div', { class: 'msg captain' }, x.text, h('div', { class: 'meta' }, h('span', {}, clock(x.at)))),
      x.error ? h('div', { class: 'msg mate' }, h('div', { class: 'meta' }, h('span', {}, `✕ This turn failed · ${x.error.split('\n')[0]}`)))
        : x.reply ? h('div', { class: 'msg mate' }, chatMarkdown(x.reply), h('div', { class: 'meta' },
          x.interrupted ? h('span', {}, '⊘ interrupted · the reply above is not complete') : h('span', {}, chatMeta(x)),
          h('span', { class: 'at' }, clock(endOf(x)))))
          : x.interrupted ? h('div', { class: 'msg mate' }, h('div', { class: 'meta' }, h('span', {}, '⊘ interrupted'))) : null,
    ]),
    !items.length ? h('p', { class: 'note' }, c ? (c.headless ? 'No messages yet.' : 'This Mate is not headless: its chat is in its own pane.') : 'loading…') : null,
    pend ? h('div', { class: 'msg pending' }, pend.text, h('div', { class: 'meta' }, `Sending in ${pend.left}s`, h('button', { type: 'button', id: 'undo-chat', onclick: () => undo(`chat:${p.name}`, `chat-${p.name}`) }, 'Undo'))) : null,
    busy ? h('div', { class: 'turn', role: 'status' }, h('span', { class: 'g', 'aria-hidden': 'true' }, '◐'), `${c.state.turn_source === 'app' ? 'working' : 'thinking'}…${c.state.queued ? ` · ${c.state.queued} waiting` : ''}`) : null,
    c?.state_error ? h('p', { class: 'note' }, `The Mate's host did not answer: ${c.state_error}`) : null,
  ];
  const ta = textarea(`chat-${p.name}`, { placeholder: blocked ? '' : `Message the Mate of ${p.name}`, disabled: Boolean(blocked), 'aria-label': 'Message to the Mate', onkeydown: (e) => {
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      sendChat(p.name);
    }
  } });
  return [
    h('div', { class: 'chat', 'aria-live': 'polite' }, list),
    h('div', { class: 'composer' }, ta,
      h('div', { class: 'row' }, h('small', {}, blocked || '⌘↵ send · 5 s to undo · Enter for a new line'),
        h('button', { type: 'button', class: 'btn', disabled: !busy || Boolean(blocked), title: busy ? '' : 'nothing to interrupt', onclick: () => interrupt(p.name) }, 'Interrupt'),
        h('button', { type: 'button', class: 'btn solid', disabled: Boolean(blocked) || Boolean(pend), onclick: () => sendChat(p.name) }, 'Send'))),
    resultEl(`${p.name}#chat`),
  ];
}
function sendChat(project) {
  const id = `chat-${project}`;
  const text = (S.drafts.get(id) ?? '').trim();
  if (!text) return;
  S.drafts.set(id, '');
  sendLater(`chat:${project}`, text, (txt) => talk(project, { action: 'say', project, text: txt }, id));
}
function interrupt(project) {
  talk(project, { action: 'interrupt', project });
}
// A chat action: no Running … (it answers at once), the outcome on the result line.
async function talk(project, req, draftId) {
  const key = `${project}#chat`;
  try {
    const { line } = await press(req);
    S.results.set(key, { kind: 'ok', text: line, at: nowISO() });
  } catch (e) {
    S.results.set(key, { kind: outcome(e), text: e.message, at: nowISO() });
    if (draftId && req.text && !S.drafts.get(draftId)) S.drafts.set(draftId, req.text);
  }
  await loadChat(project);
}

// ---- The Mate's duty board (CON-158) ----------------------------------------------
//
// What the captain asked the Mate for and what it proposed, grouped by where
// it stands. A done row carries a checkbox: the tick (`okay`) waits 5 s for
// an undo, then takes the row off the board. A row waiting on a topic links
// to that topic in City Hall; the row itself never asks anything.
const DUTY_GROUPS = [['done', 'For you to OK'], ['working', 'In progress'], ['waiting', 'Waiting on a topic'], ['okayed', 'Okayed']];
const DUTY_SHOWN = 10; // okayed rows shown before "Show all"
function dutyView(p) {
  const got = S.duty.get(p.name);
  if (!got) return [h('p', { class: 'note' }, 'loading…')];
  if (got.error) return [h('p', { class: 'note' }, `The board could not be read: ${got.error}`)];
  if (!got.logged) return [h('p', { class: 'note' }, 'This Mate has put nothing on its duty board.')];
  if (!got.rows.length) return [h('p', { class: 'note' }, 'Nothing on the duty board yet.')];
  const groups = DUTY_GROUPS.flatMap(([st, label]) => {
    const all = got.rows.filter((x) => x.status === st);
    if (!all.length) return [];
    const fold = st === 'okayed' && !S.dutyAll.has(p.name) && all.length > DUTY_SHOWN;
    return [
      h('div', { class: 'group-h' }, `${label} · ${all.length}`),
      h('ul', { class: 'list duty' }, (fold ? all.slice(0, DUTY_SHOWN) : all).map((x) => dutyRow(p.name, x))),
      fold ? h('button', { type: 'button', class: 'fold', onclick: () => (S.dutyAll.add(p.name), render()) }, `Show all ${all.length}`) : null,
    ];
  });
  return [S.readOnly && got.rows.some((x) => x.status === 'done') ? readOnlyBand() : null, ...groups, resultEl(`${p.name}#duty`)];
}
function dutyRow(project, x) {
  const key = `${project}#${x.id}`;
  const pend = S.pending.get(`duty:${key}`);
  const done = x.status === 'done';
  const glyph = done
    ? h('span', { class: 'g tick' }, h('input', { type: 'checkbox', 'aria-label': `OK: ${x.title}`, disabled: Boolean(S.readOnly) || Boolean(pend), checked: Boolean(pend), onchange: (e) => e.target.checked && okayDuty(project, x) }))
    : h('span', { class: 'g', 'aria-hidden': 'true' }, x.status === 'working' ? '▶' : x.status === 'waiting' ? '?' : '✓');
  const side = x.status === 'waiting' && x.topic
    ? h('button', { type: 'button', class: 'link', onclick: () => select({ kind: 'hall', tab: 'topics', topic: topicKey(project, x.topic) }, { fly: false }) }, `topic #${x.topic} ›`)
    : h('small', {}, ago(x.at));
  const k = { working: 'k-run', waiting: 'k-topic', okayed: 'okayed quiet', done: 'done' }[x.status] ?? '';
  const toggle = (e) => {
    if (e.target.closest?.('button, input')) return;
    if (S.dutyOpen.has(key)) S.dutyOpen.delete(key);
    else S.dutyOpen.add(key);
    render();
  };
  return h('li', { class: `click ${k}`, dataset: { duty: String(x.id), status: x.status }, onclick: toggle },
    glyph,
    h('b', {}, x.title),
    h('div', { class: 'side' }, side),
    pend
      ? h('small', { class: 'okaying', role: 'status' }, `Okaying in ${pend.left}s `, h('button', { type: 'button', onclick: () => undo(`duty:${key}`) }, 'Undo'))
      : h('small', {}, x.kind === 'proposed' ? h('span', { class: 'tag' }, 'proposed') : 'assigned', x.last ? ` · ${x.last}` : ''),
    S.dutyOpen.has(key) ? h('ol', { class: 'notes' }, (x.notes ?? []).map((n) => h('li', {}, `${clock(n.at)} · ${n.op}${n.topic ? ` #${n.topic}` : ''}${n.text ? ` · ${n.text}` : ''}`))) : null);
}
// The tick: 5 s to undo, then `okay` on the row; the outcome on the board's result line.
function okayDuty(project, x) {
  const key = `${project}#${x.id}`;
  S.results.delete(`${project}#duty`);
  sendLater(`duty:${key}`, String(x.id), async () => {
    try {
      const { line } = await press({ action: 'okay', project, duty: x.id });
      S.results.set(`${project}#duty`, { kind: 'ok', text: line, at: nowISO() });
    } catch (e) {
      S.results.set(`${project}#duty`, { kind: outcome(e), text: e.message, at: nowISO() });
    }
    render();
    refreshDuty();
  });
}

// The project's inbox: what waits, as the console's box rail shows it.
const VERB = { 'needs-decision': ['◆', 'decision'], blocked: ['■', 'incident'], 'wait-mate': ['⇢', 'merge'] };
function inboxView(p) {
  const rows = S.inbox.get(p.name) ?? [];
  return [
    rows.length ? h('ul', { class: 'list' }, rows.map((x) => {
      const [g, k] = x.kind === 'incident' ? ['!', 'incident'] : VERB[x.verb] ?? ['·', ''];
      const t = x.crew ? taskOf(p.name, x.crew) : null;
      const assign = t ? crewFacts(p.name, t).assign[0] : null;
      return h('li', { class: `click k-${k}`, onclick: (e) => x.crew && !e.target.closest('button') && select({ kind: 'crew', project: p.name, crew: x.crew, tab: 'status', back: { kind: 'mate', project: p.name, tab: 'inbox' } }) },
        h('span', { class: 'g', 'aria-hidden': 'true' }, g),
        h('b', {}, `${x.crew || 'Mate'} · ${x.kind === 'incident' ? `incident: ${x.verb}` : x.verb}`),
        assign ? h('div', { class: 'side' }, assignButton(p, x.crew, assign)) : h('div', { class: 'side' }, h('small', {}, ago(x.at))),
        h('small', {}, x.text));
    })) : h('p', { class: 'note' }, 'Nothing in the inbox.'),
    resultEl(`${p.name}#assign`),
  ];
}
function assignButton(p, crew, b) {
  const why = blockedBy(`${p.name}#assign`) || (b.available ? b.reason ?? '' : b.reason);
  const busy = S.busy.get(`${p.name}#assign`) === crew;
  return h('div', { class: 'act' }, h('button', { type: 'button', class: 'btn', disabled: busy || !b.available || Boolean(blockedBy(`${p.name}#assign`)), onclick: () => assign(p.name, crew, b.key) }, busy ? 'Running assign…' : b.available ? 'Assign to the Mate' : 'Assigned'), h('small', {}, why));
}
async function assign(project, crew, key) {
  S.busy.set(`${project}#assign`, crew);
  render();
  try {
    const { line } = await press({ action: 'assign', project, crew, key });
    S.results.set(`${project}#assign`, { kind: 'ok', text: line, at: nowISO() });
  } catch (e) {
    S.results.set(`${project}#assign`, { kind: outcome(e), text: e.message, at: nowISO() });
  } finally {
    S.busy.delete(`${project}#assign`);
    await loadAll().catch(() => render());
  }
}

async function loadPlugins(project) {
  try {
    const body = await api(`/api/plugins?project=${enc(project)}`);
    S.plugins.set(project, { apps: body.apps ?? [], error: body.error });
  } catch (e) {
    S.plugins.set(project, { apps: [], error: e.message });
  }
  render();
}
function appsView(p) {
  const got = S.plugins.get(p.name);
  if (!got) return [h('p', { class: 'note' }, 'loading…')];
  return [
    got.error ? h('p', { class: 'note' }, got.error) : null,
    got.apps.length ? h('ul', { class: 'list' }, got.apps.map((a) => h('li', {},
      h('span', { class: 'g' }, a.error ? '▲' : '●'), h('b', {}, a.name || a.instance),
      h('div', { class: 'side' }, a.open?.url ? ext(a.open.url, `${a.open.title || 'Open'} ↗`) : h('small', {}, 'no page to open')),
      h('small', {}, a.error ?? a.status ?? '?')))) : h('p', { class: 'note' }, 'No apps or plugins in this project.'),
  ];
}

function projectView(p) {
  const n = openCrews(p.name).length;
  const rm = S.acts.get(p.name)?.mate_more?.find((b) => b.action === 'remove_project');
  const busy = S.busy.get('workspace');
  const why = blockedBy('workspace') || (rm ? (rm.available ? '' : rm.reason) : '?');
  return [
    facts([['Repos', (p.repos ?? []).join(', ') || 'none yet'], ['Open crews', String(n)], ['Mode', unk(p.mode)]]),
    h('h3', { class: 'sec' }, 'Project'),
    S.readOnly ? readOnlyBand() : h('div', { class: 'actions' }, h('div', { class: 'act' },
      h('button', { type: 'button', class: busy === `Remove ${p.name}…` ? 'running' : 'danger', id: `del-${p.name}`, disabled: Boolean(why) || Boolean(busy), onclick: () => removeProject(p.name) }, busy === `Remove ${p.name}…` ? 'Running remove…' : 'Remove project…'),
      h('small', {}, busy === `Remove ${p.name}…` ? 'running…' : why))),
    resultEl('workspace'),
  ];
}

function removeProject(name) {
  const rm = S.acts.get(name)?.mate_more?.find((b) => b.action === 'remove_project');
  if (!rm?.available) return;
  confirmBox({
    title: `Remove project ${name}?`,
    scope: `${name} · its Mate and ${openCrews(name).length} crews stop`,
    body: rm.confirm.replace(`Remove project ${name}? `, ''),
    yes: 'Remove project',
    onYes: () => {
      const at = city.blocks.get(name)?.centre.clone();
      run('workspace', `Remove ${name}…`, { action: 'remove_project', project: name }, `mate:${name}`, (ok) => {
        if (!ok) return;
        select({ kind: 'hall', tab: 'projects' }, { fly: false });
        if (at) fly(at.clone().setY(0.5), at.clone().add(new THREE.Vector3(0, 16, 22)));
      });
    },
  });
}

// Create a Mate: pick the harness, then the console's onboard.
VIEWS.create = () => {
  const p = projectOf(S.sel.project);
  if (!p) return { title: 'Gone', body: [] };
  const acts = S.acts.get(p.name);
  const choices = acts?.harnesses ?? [];
  const pick = S.drafts.get(`harness-${p.name}`) ?? choices[0]?.kind;
  const onboard = acts?.mate?.find((b) => b.action === 'onboard');
  const key = p.name;
  const running = S.busy.get(key);
  const why = blockedBy(key) || (!onboard ? '?' : !onboard.available ? onboard.reason : !pick ? 'no harness can run a Mate' : '');
  return {
    crumbs: [{ text: p.name, go: () => select({ kind: 'mate', project: p.name }) }, { text: 'Create mate' }],
    title: `Create the Mate of ${p.name}`,
    sub: 'It moves onto the plaza of this block and starts by reading the repo.',
    body: [
      ...bands(),
      S.readOnly ? readOnlyBand() : h('div', { class: 'form' },
        h('fieldset', { class: 'opts', style: 'border:0;padding:0;margin:0' }, h('legend', { class: 'sec', style: 'margin-bottom:6px;font-size:11px;letter-spacing:.08em;color:var(--muted)' }, 'HARNESS'),
          h('div', { class: 'harness' }, choices.map((x) => h('label', {},
            h('input', { type: 'radio', name: 'harness', value: x.kind, checked: pick === x.kind, onchange: () => (S.drafts.set(`harness-${p.name}`, x.kind), render()) }),
            logo(x.kind), h('b', {}, harnessName(x.kind)), h('small', { class: 'hint' }, x.default ? 'the workspace default' : ''))))),
        h('div', { class: 'act' }, h('button', { type: 'button', class: running ? 'running' : 'primary', id: 'create-go', disabled: Boolean(running) || Boolean(why), onclick: () => run(key, 'Create mate…', { action: 'onboard', project: p.name, harness: pick }, `mate:${p.name}`, (ok) => ok && select({ kind: 'mate', project: p.name })) },
          running ? h('span', { class: 'g' }, '⟳') : null, running ? 'Running create…' : `Create mate on ${harnessName(pick)}`), h('small', {}, running ? 'reading the repo can take a minute' : why))),
      resultEl(key),
    ],
  };
};
function logo(harness) {
  const hn = HARNESSES[harness];
  if (!hn?.logo) return h('span', { class: 'g' }, '◎');
  const fill = Array.isArray(hn.logoColor) ? hn.logoColor[1] : hn.logoColor ?? '#8E857A';
  return h('img', { src: `data:image/svg+xml,${enc(hn.logo.replace('<svg ', `<svg fill="${fill}" `))}`, alt: '' });
}

// A crew, where it stands.
VIEWS.crew = () => {
  const { project, crew } = S.sel;
  const t = taskOf(project, crew);
  const p = projectOf(project);
  if (!t || !p) return { title: crew, body: [h('p', {}, 'This crew is no longer in the workspace.'), resultEl(rowKey(project, crew))] };
  if (t.closed || t.state === 'gone') {
    return {
      crumbs: [{ text: project, go: () => select({ kind: 'mate', project }) }, { text: 'crew' }],
      title: `${crew} · ${t.role}`,
      sub: t.text,
      body: [
        facts([['State', stateEl('', t.merged_at ? ['✓', `merged ${clock(t.merged_at)}`, 'ok'] : ['◌', (t.close_state || 'closed').replace(/_/g, ' '), 'off'])], ['Branch', t.branch ?? '?'], ['Tokens', t.tokens?.total ? formatTokens(t.tokens.total) : '?'], ['Closed', clock(t.closed_at)]]),
        resultEl(rowKey(project, crew)),
        h('p', { class: 'note' }, 'This crew is closed, so it has no actions. Its story stays in the tent\'s playback and in the Office cabinet.'),
        h('div', { class: 'links' }, h('a', { href: '#', onclick: (e) => (e.preventDefault(), enterRoom(`${project}/${t.repo || tentRepos(p)[0]}`)) }, 'Step into its tent'), ext(adminTask(project, crew), 'Timeline ↗')),
      ],
    };
  }
  const f = crewFacts(project, t);
  const acts = S.acts.get(project);
  const sheet = acts?.crews?.[crew] ?? [];
  const more = acts?.crew_more?.[crew] ?? [];
  const tab = S.sel.tab ?? (f.incident ? 'peek' : 'status');
  const kit = [harnessName(t.harness), t.model, t.effort].filter(Boolean).join(' · ');
  const toggle = (b, id) => b && { ...b, toggle: true, pressed: tab === id, run: () => setTab(tab === id ? 'status' : id) };
  const extra = [
    toggle(sheet.find((b) => b.action === 'diff'), 'diff'),
    toggle(more.find((b) => b.action === 'peek'), 'peek'),
    more.find((b) => b.action === 'explain_notice'),
  ].filter(Boolean);
  const replay = room && mode !== 'live';
  return {
    crumbs: [{ text: project, go: () => select({ kind: 'mate', project, back: S.sel.back?.kind === 'tent' ? S.sel.back : null }) }, ...(S.sel.back?.kind === 'tent' ? [{ text: `tent ${S.sel.back.repo}`, go: () => select(S.sel.back) }] : []), { text: 'crew' }],
    title: `${crew} · ${t.role}`,
    sub: t.text,
    body: [
      ...bands(replay ? [h('div', { class: 'band replay' }, h('span', { class: 'g' }, '⏸'), h('span', {}, `The tent shows ${$('#when').textContent}; this panel shows now. Actions wait for Live. `), h('button', { type: 'button', class: 'btn', style: 'min-height:32px', onclick: goLive }, 'Go Live'))] : []),
      facts([
        ['State', f.incident ? stateEl('', ['!', `${f.incident.verb || 'incident'} ${ago(f.incident.at)}`, 'warn']) : stateEl(t.state)],
        ['Status line', f.status ? `${f.status.verb}${f.assign.length && !f.open ? ' · handed to the Mate' : ''}` : t.detail || '?'],
        ['Harness', kit || '?'],
        ['Branch', t.branch ?? '?'],
        ['Tokens', t.tokens?.total ? formatTokens(t.tokens.total) : '?'],
        ['Since', t.since ? `${clock(t.since)} · ${ago(t.since)}` : '?'],
      ]),
      ...actionsBlock({ project, crew, buttons: sheet.filter((b) => b.action !== 'diff'), extra, links: [ext(adminTask(project, crew), 'Timeline ↗', 'the admin timeline, in a new tab'), ext(`${adminTask(project, crew)}/usage`, 'Usage ↗', 'the admin usage page, in a new tab'), ext(officeCrew(project, crew), 'Office ↗', 'this crew in Mate Office, in a new tab')] }),
      tabs([['status', 'Status'], ['peek', 'Peek'], ['diff', 'Diff']], tab, setTab),
      ...{ status: crewStatus, peek: crewPeek, diff: crewDiff }[tab](p, t, f),
    ],
  };
};
function crewStatus(p, t, f) {
  const q = f.status?.verb === 'needs-decision' && f.assign.length;
  return [
    f.status ? h('div', { class: 'result', dataset: { kind: 'info' } }, h('span', { class: 'g' }, VERB[f.status.verb]?.[0] ?? '·'), h('span', {}, h('b', {}, `${f.status.verb}: `), f.status.text)) : h('p', { class: 'note' }, t.detail || 'No status line waiting.'),
    f.incident ? h('div', { class: 'result', dataset: { kind: 'failed' } }, h('span', { class: 'g' }, '!'), h('span', {}, h('b', {}, `${f.incident.verb || 'incident'}: `), f.incident.text)) : null,
    q ? h('div', { class: 'form' },
      h('p', { class: 'note' }, p.mode === 'auto' ? 'Auto: the daemon hands this to the Mate.' : `${p.name} is manual: the Mate does not see this until someone hands it over.`),
      assignButton(p, t.crew, f.assign[0]),
      resultEl(`${p.name}#assign`)) : null,
  ];
}
// Peek is the console's: the crew's screen read through Herdr, read-only.
async function loadPeek(project, crew, force = false) {
  const key = rowKey(project, crew);
  const have = S.peeks.get(key);
  if (have?.loading || (!force && have && Date.now() - have.at < 15e3)) return;
  S.peeks.set(key, { ...(have ?? {}), loading: true, at: have?.at ?? 0 });
  render();
  try {
    const { line } = await press({ action: 'peek', project, crew });
    S.peeks.set(key, { text: line, at: Date.now() });
  } catch (e) {
    S.peeks.set(key, { err: e.message, kind: outcome(e), at: Date.now() });
  }
  render();
}
function crewPeek(p, t) {
  const key = rowKey(p.name, t.crew);
  const pk = S.peeks.get(key);
  const b = S.acts.get(p.name)?.crew_more?.[t.crew]?.find((x) => x.action === 'peek');
  const why = S.readOnly ? 'read-only dashboard' : b && !b.available ? b.reason : '';
  return [
    h('div', { class: 'peekmeta' }, h('span', {}, pk?.loading ? '◐ reading the screen…' : pk?.at ? `◉ read-only · captured ${ago(new Date(pk.at).toISOString())} ago` : why ? `⊘ ${why}` : '◉ read-only'),
      h('button', { type: 'button', class: 'btn', style: 'min-height:32px', disabled: Boolean(why) || pk?.loading, onclick: () => loadPeek(p.name, t.crew, true) }, 'Refresh')),
    pk?.err ? h('div', { class: 'result', dataset: { kind: pk.kind } }, h('span', { class: 'g' }, '✕'), h('span', {}, pk.err)) : null,
    pk?.text ? h('pre', { class: 'peek', 'aria-label': `Screen of ${t.crew}` }, pk.text) : null,
    h('p', { class: 'note' }, 'No terminal in the browser. To type into this agent, use mate console or ask the Mate.'),
  ];
}
async function loadDiff(project, crew) {
  const key = rowKey(project, crew);
  if (S.diffs.has(key)) return;
  S.diffs.set(key, { loading: true });
  try {
    S.diffs.set(key, await api(`/api/projects/${enc(project)}/tasks/${enc(crew)}/diff`));
  } catch (e) {
    S.diffs.set(key, { reason: e.message });
  }
  render();
}
// The files a `mate diff` touches and its lines in and out.
function diffFiles(text) {
  const files = [];
  let f = null;
  for (const line of text.split('\n')) {
    const m = line.match(/^diff --git a\/(.+) b\/(.+)$/);
    if (m) files.push((f = { path: m[2], add: 0, del: 0 }));
    else if (f && line.startsWith('+') && !line.startsWith('+++')) f.add++;
    else if (f && line.startsWith('-') && !line.startsWith('---')) f.del++;
  }
  return files;
}
const DIFF_LINES = 240;
function crewDiff(p, t) {
  const d = S.diffs.get(rowKey(p.name, t.crew));
  if (!d || d.loading) return [h('p', { class: 'note' }, 'loading…')];
  if (!d.text) return [h('p', { class: 'note' }, d.reason || 'Nothing on this branch beyond the default branch.')];
  const files = diffFiles(d.text);
  const add = files.reduce((n, x) => n + x.add, 0);
  const del = files.reduce((n, x) => n + x.del, 0);
  const lines = d.text.split('\n');
  return [
    h('div', { class: 'peekmeta' }, h('span', { style: 'font-family:var(--mono)' }, d.branch || t.branch || '?'), h('button', { type: 'button', class: 'btn', style: 'min-height:32px', onclick: () => (S.diffs.delete(rowKey(p.name, t.crew)), loadDiff(p.name, t.crew)) }, 'Refresh')),
    files.length ? h('table', { class: 'files' }, h('tbody', {}, files.map((x) => h('tr', {}, h('td', {}, x.path), h('td', { class: 'n add' }, `+${x.add}`), h('td', { class: 'n del' }, `−${x.del}`))),
      h('tr', {}, h('td', {}, h('b', {}, `${files.length} file${files.length === 1 ? '' : 's'}`)), h('td', { class: 'n add' }, `+${add}`), h('td', { class: 'n del' }, `−${del}`)))) : null,
    h('pre', { class: 'hunk' }, lines.slice(0, DIFF_LINES).join('\n')),
    lines.length > DIFF_LINES ? h('p', { class: 'note' }, `${lines.length - DIFF_LINES} more lines: the whole diff is in Office.`) : null,
    h('div', { class: 'links' }, ext(officeCrew(p.name, t.crew), 'Whole diff in Office ↗')),
  ];
}

// City Hall: topics from every Mate, the projects, adding one.
VIEWS.hall = () => {
  const tab = S.sel.tab ?? 'topics';
  const waiting = topics().filter((x) => x.group === 'waiting');
  if (S.sel.topic) {
    const x = topicOf(S.sel.topic);
    if (x) return topicView(x);
  }
  return {
    crumbs: [{ text: 'City' }, { text: 'City Hall' }],
    title: 'City Hall',
    sub: 'Your desk: what the Mates ask you, and the projects of this workspace',
    body: [
      ...bands(),
      tabs([['topics', 'Topics', waiting.length], ['projects', 'Projects']], tab, (id) => select({ kind: 'hall', tab: id }, { fly: false })),
      ...(tab === 'topics' ? topicList() : projectList()),
    ],
  };
};
function topicList() {
  const all = topics();
  const groups = [['waiting', 'Waiting on you'], ['on_it', 'The Mate is on it'], ['earlier', 'Earlier']];
  const out = groups.flatMap(([st, label]) => {
    const rows = all.filter((x) => x.group === st).slice(0, st === 'earlier' ? 20 : undefined);
    if (!rows.length) return [];
    const open = (x) => select({ kind: 'hall', tab: 'topics', topic: x.key }, { fly: false });
    return [h('div', { class: 'group-h' }, `${label} · ${rows.length}`), h('ul', { class: 'list' }, rows.map((x) => h('li', { class: `click ${st === 'waiting' ? 'k-topic' : 'quiet'}`, tabindex: '0', role: 'button', onclick: () => open(x), onkeydown: (e) => e.key === 'Enter' && open(x) },
      h('span', { class: 'g', 'aria-hidden': 'true' }, st === 'waiting' ? '?' : st === 'on_it' ? '▶' : x.status === 'dropped' ? '⊘' : '✓'), h('b', {}, x.question || x.title), h('div', { class: 'side' }, h('small', {}, ago(x.at))), h('small', {}, `${x.project} · ${x.title}${x.answer ? ` · you said: ${x.answer}` : ''}`))))];
  });
  return out.length ? out : [h('p', { class: 'note' }, 'No Mate has asked you anything.')];
}

// One topic: the Mate's question, its options, an answer; or talk it over
// first in a read-only fork of the Mate (the REPL's session, CON-104).
function topicView(x) {
  const pend = S.pending.get(`topic:${x.key}`);
  const pick = S.drafts.get(`opt-${x.key}`);
  const own = (S.drafts.get(`own-${x.key}`) ?? '').trim();
  const opt = x.options?.find((o) => o.key === pick);
  const answer = own || (opt ? `[${opt.key}] ${opt.text}` : '');
  const say = S.acts.get(x.project)?.mate_more?.find((b) => b.action === 'say');
  const can = blockedBy(`${x.project}#chat`) || (say && !say.available ? say.reason : !say ? '?' : '');
  const why = can || (!answer ? 'pick an option or type an answer' : '');
  const st = { waiting: ['?', 'Waiting on you', 'need'], on_it: ['▶', 'The Mate is on it', 'ok'], earlier: ['✓', x.status === 'dropped' ? 'Dropped' : 'Done', 'off'] }[x.group];
  const fork = S.forks.get(x.key);
  return {
    crumbs: [{ text: 'City Hall', go: () => select({ kind: 'hall', tab: 'topics' }, { fly: false }) }, { text: 'topic' }],
    title: x.title || x.project,
    sub: `${x.project} · #${x.id} · asked ${ago(x.opened_at)} ago by its Mate`,
    body: [
      ...bands(),
      stateEl('', st),
      x.question ? h('p', { class: 'topic-q' }, x.question) : null,
      ...(x.context ?? []).map((c) => h('p', { class: 'topic-ctx' }, c)),
      ...(x.notes ?? []).map((n) => h('p', { class: 'note' }, `The Mate · ${clock(n.at)}: ${n.text}`)),
      x.group !== 'waiting' ? (x.answer ? h('div', { class: 'result', dataset: { kind: 'info' } }, h('span', { class: 'g' }, '✓'), h('span', {}, `You answered ${clock(x.answered_at)}: ${x.answer}`)) : null) : h('div', { class: 'form' },
        x.options?.length ? h('fieldset', { class: 'opts' }, h('legend', { class: 'sr' }, 'Options'), x.options.map((o) => h('label', {}, h('input', { type: 'radio', name: `opt-${x.key}`, id: `opt-${x.key}-${o.key}`, checked: pick === o.key, onchange: () => (S.drafts.set(`opt-${x.key}`, o.key), render()) }), `[${o.key}] ${o.text}${o.recommended ? ' · recommended' : ''}`))) : null,
        h('label', {}, x.options?.length ? 'Or your own answer' : 'Your answer', textarea(`own-${x.key}`, { rows: 2, placeholder: 'Type to answer in your words', oninput: (e) => {
          S.drafts.set(`own-${x.key}`, e.target.value);
          clearTimeout(topicView.t);
          topicView.t = setTimeout(render, 250);
        } })),
        pend ? h('div', { class: 'msg pending' }, pend.text, h('div', { class: 'meta' }, `Sending in ${pend.left}s`, h('button', { type: 'button', id: 'undo-topic', onclick: () => undo(`topic:${x.key}`, `own-${x.key}`) }, 'Undo'))) : null,
        h('div', { class: 'actions' },
          h('div', { class: 'act' }, h('button', { type: 'button', class: 'primary', id: `send-${x.key}`, disabled: Boolean(why) || Boolean(pend), onclick: () => {
            S.drafts.delete(`own-${x.key}`);
            S.drafts.delete(`opt-${x.key}`);
            sendLater(`topic:${x.key}`, answer, async (a) => {
              try {
                const { line } = await press({ action: 'answer', project: x.project, news: x.id, text: a });
                S.results.set(`topic:${x.key}`, { kind: 'ok', text: line, at: nowISO() });
              } catch (e) {
                S.results.set(`topic:${x.key}`, { kind: outcome(e), text: e.message, at: nowISO() });
                S.drafts.set(`own-${x.key}`, a);
              }
              refreshNews();
            });
          } }, 'Send answer'), h('small', {}, why)),
          h('div', { class: 'act' }, h('button', { type: 'button', id: `fork-${x.key}`, disabled: Boolean(fork?.open) || Boolean(can), onclick: () => openFork(x) }, 'Discuss privately'), h('small', {}, can || (fork?.open ? 'session open below' : 'a read-only fork of the Mate')))),
        fork && (fork.open || fork.items?.length) ? forkView(x, fork) : null),
      resultEl(`topic:${x.key}`),
    ],
  };
}

// A topic's private session: the REPL's own (CON-104), opened by the first
// question asked in it. The fork reads and answers; it cannot act.
async function loadFork(key) {
  const x = topicOf(key);
  if (!x) return;
  try {
    const body = await api(`/api/projects/${enc(x.project)}/forks/${x.id}`);
    S.forks.set(key, { ...(S.forks.get(key) ?? {}), ...body, open: Boolean(body.open) || Boolean(S.forks.get(key)?.asking) });
  } catch (e) {
    S.forks.set(key, { ...(S.forks.get(key) ?? { items: [] }), error: e.message });
  }
  render();
  const f = S.forks.get(key);
  if (f?.running && S.sel?.topic === key) setTimeout(() => S.sel?.topic === key && loadFork(key), 2500);
}
function openFork(x) {
  S.forks.set(x.key, { ...(S.forks.get(x.key) ?? { items: [] }), open: true });
  render();
  document.getElementById(`fork-in-${x.key}`)?.focus();
}
async function askFork(x) {
  const q = (S.drafts.get(`fork-in-${x.key}`) ?? '').trim();
  if (!q) return;
  S.drafts.set(`fork-in-${x.key}`, '');
  const f = S.forks.get(x.key);
  f.asking = true;
  f.note = '';
  render();
  try {
    await press({ action: 'fork', project: x.project, news: x.id, text: q });
  } catch (e) {
    f.note = `${outcome(e) === 'failed' ? 'Ran and failed' : 'Refused, nothing changed'}: ${e.message}`;
    S.drafts.set(`fork-in-${x.key}`, q);
  }
  f.asking = false;
  await loadFork(x.key);
}
async function closeFork(x) {
  try {
    await press({ action: 'fork_close', project: x.project, news: x.id });
  } catch {}
  S.forks.delete(x.key);
  render();
}
function forkView(x, fork) {
  const items = fork.items ?? [];
  return h('div', { class: 'fork', role: 'region', 'aria-label': 'Private session' },
    h('h4', {}, '◉ Private session · read-only fork of the Mate · nothing here reaches it'),
    ...items.flatMap((m) => [
      h('div', { class: 'msg captain' }, m.text),
      m.reply ? h('div', { class: 'msg mate' }, m.reply, h('div', { class: 'meta' }, h('button', { type: 'button', onclick: () => {
        S.drafts.set(`own-${x.key}`, m.reply);
        render();
        document.getElementById(`own-${x.key}`)?.focus();
      } }, 'Use as my answer'))) : null,
    ]),
    fork.running ? h('div', { class: 'msg captain' }, fork.running.text) : null,
    fork.running || fork.asking ? h('div', { class: 'turn' }, h('span', { class: 'g' }, '◐'), 'thinking…') : null,
    fork.note ? h('p', { class: 'note' }, fork.note) : null,
    fork.error ? h('p', { class: 'note' }, fork.error) : null,
    S.readOnly ? null : h('div', { class: 'composer', style: 'position:static;margin:0;padding:0;border:0;background:none' }, textarea(`fork-in-${x.key}`, { rows: 2, placeholder: 'Ask the fork', 'aria-label': 'Ask the private session', onkeydown: (e) => {
      if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        askFork(x);
      }
    } }),
      h('div', { class: 'row' }, h('small', {}, 'the fork reads; it cannot act'),
        fork.running ? h('button', { type: 'button', class: 'btn', onclick: () => press({ action: 'interrupt', project: x.project, news: x.id }).catch(() => {}).then(() => loadFork(x.key)) }, 'Interrupt') : null,
        h('button', { type: 'button', class: 'btn', onclick: () => closeFork(x) }, 'Close session'),
        h('button', { type: 'button', class: 'btn solid', disabled: Boolean(fork.running) || fork.asking, onclick: () => askFork(x) }, 'Ask'))));
}

function projectList() {
  const why = blockedBy('workspace');
  return [
    h('ul', { class: 'list' }, (S.ws?.projects ?? []).map((p) => {
      const n = openCrews(p.name).length;
      const g = !hasMate(p) ? '+' : p.mate.running ? '●' : '◌';
      const rm = S.acts.get(p.name)?.mate_more?.find((b) => b.action === 'remove_project');
      return h('li', { class: 'click', tabindex: '0', role: 'button', onclick: (e) => !e.target.closest('button') && select({ kind: 'mate', project: p.name }), onkeydown: (e) => e.key === 'Enter' && e.target === e.currentTarget && select({ kind: 'mate', project: p.name }) },
        h('span', { class: 'g' }, g), h('b', {}, p.name),
        S.readOnly ? null : h('div', { class: 'side' }, h('button', { type: 'button', onclick: () => removeProject(p.name), disabled: Boolean(why) || !rm?.available, title: rm && !rm.available ? rm.reason : '' }, 'Remove…')),
        h('small', {}, `${!hasMate(p) ? 'no Mate yet' : p.mate.running ? `Mate ${stateWord(p.mate.state)}` : 'Mate stopped'} · ${n} crew${n === 1 ? '' : 's'} · ${hasMate(p) ? unk(p.mate.tokens_today, formatTokens) : '?'} today · ${unk(p.mode)}`));
    })),
    S.readOnly ? readOnlyBand() : h('div', { class: 'act' }, h('button', { type: 'button', class: 'btn', id: 'add-open', onclick: () => select({ kind: 'add', back: { kind: 'hall', tab: 'projects' } }, { fly: false }), disabled: Boolean(why) }, '+ Add project…'), h('small', {}, why || 'it takes the next empty lot')),
    resultEl('workspace'),
  ];
}

// Add a project: a park lot becomes a block.
const NAME = /^[a-z][a-z0-9-]{0,31}$/;
VIEWS.add = VIEWS.lot = () => {
  const name = (S.drafts.get('add-name') ?? '').trim();
  const repo = (S.drafts.get('add-path') ?? '').trim();
  const bad = name && !NAME.test(name) ? 'a lowercase letter first, then letters, digits and -; 32 at most' : projectOf(name) ? `${name} is already a project` : '';
  const running = S.busy.get('workspace');
  const why = blockedBy('workspace') || (!name ? 'a name is needed' : bad);
  return {
    crumbs: [{ text: 'City Hall', go: () => select({ kind: 'hall', tab: 'projects' }) }, { text: S.sel.kind === 'lot' ? 'empty lot' : 'add project' }],
    title: S.sel.kind === 'lot' ? 'Empty lot' : 'Add project',
    sub: 'A park becomes a block with a tent per repo and a plaza for its Mate.',
    body: [
      ...bands(),
      S.readOnly ? readOnlyBand() : h('div', { class: 'form' },
        h('label', {}, 'Name', h('input', { type: 'text', id: 'add-name', value: S.drafts.get('add-name') ?? '', autocomplete: 'off', oninput: (e) => (S.drafts.set('add-name', e.target.value), render()) }), bad ? h('span', { class: 'err' }, bad) : h('span', { class: 'hint' }, 'shown on the block; lowercase')),
        h('label', {}, 'Repo path (optional)', h('input', { type: 'text', id: 'add-path', value: S.drafts.get('add-path') ?? '', placeholder: '/path/to/repo, or relative to the workspace', autocomplete: 'off', oninput: (e) => S.drafts.set('add-path', e.target.value) }), h('span', { class: 'hint' }, 'leave empty to add the repo later')),
        h('div', { class: 'act' }, h('button', { type: 'button', id: 'add-go', class: running ? 'running' : 'primary', disabled: Boolean(running) || Boolean(why), onclick: () => run('workspace', 'Add project…', { action: 'add_project', project: name, repo }, 'hall', (ok) => {
          if (!ok) return;
          S.drafts.delete('add-name');
          S.drafts.delete('add-path');
          flash(`block:${name}`, 'new', '+', `new · ${name}`, 7000);
          select({ kind: 'mate', project: name, tab: 'project' }, { fly: false });
          const b = city.blocks.get(name);
          if (b) fly(b.centre.clone().setY(0.5), b.centre.clone().add(new THREE.Vector3(0, 16, 22)));
        }) }, running ? h('span', { class: 'g' }, '⟳') : null, running ? 'Running add…' : 'Add project'), h('small', {}, running ? 'registering the project…' : why))),
      resultEl('workspace'),
    ],
  };
};

VIEWS.block = () => {
  const p = projectOf(S.sel.project);
  if (!p) return { title: '?', body: [] };
  return {
    crumbs: [{ text: 'City' }, { text: p.name }],
    title: p.name,
    sub: `${tentRepos(p).length} tent${tentRepos(p).length === 1 ? '' : 's'} · ${openCrews(p.name).length} crews at work`,
    body: [
      h('ul', { class: 'list' },
        h('li', { class: 'click', tabindex: '0', role: 'button', onclick: () => select({ kind: 'mate', project: p.name }) }, h('span', { class: 'g' }, '◎'), h('b', {}, 'The Mate on the plaza'), h('small', {}, 'chat, inbox, actions')),
        ...tentRepos(p).map((r) => h('li', { class: 'click', tabindex: '0', role: 'button', onclick: () => enterRoom(`${p.name}/${r}`) }, h('span', { class: 'g' }, '⌂'), h('b', {}, `Tent ${r}`), h('small', {}, 'step in: playback and its crews'))),
        ...openCrews(p.name).map((t) => h('li', { class: 'click', tabindex: '0', role: 'button', onclick: () => select({ kind: 'crew', project: p.name, crew: t.crew }) }, h('span', { class: 'g' }, STATE[t.state]?.[0] ?? '·'), h('b', {}, t.crew), h('small', {}, `${t.role} · ${stateWord(t.state)}`)))),
    ],
  };
};

// The system: is what you see current, and what the auto daemon did.
VIEWS.system = () => {
  const tl = S.timelineStale;
  const rows = [
    ['Dashboard', S.stale ? ['▲', `stale since ${clock(S.staleSince)}: ${S.stale}`, 'warn'] : ['●', 'live', 'ok']],
    ['Timeline', tl.length ? ['▲', tl.map((x) => `${x.project} stuck since ${clock(x.since)}: ${x.error}`).join(' · '), 'warn'] : ['●', 'current · every project', 'ok']],
    ['Actions', S.readOnly ? ['⊘', S.readOnly, 'off'] : ['●', 'served by mate console', 'ok']],
  ];
  const daemons = (S.ws?.projects ?? []).map((p) => [p, S.acts.get(p.name)?.daemon]);
  return {
    crumbs: [{ text: 'City' }, { text: 'System' }],
    title: 'System',
    sub: 'Whether the city is current, and what the auto daemon did',
    body: [
      h('ul', { class: 'list' }, rows.map(([k, s]) => h('li', {}, h('span', { class: 'g' }, s[0]), h('b', {}, k), h('small', {}, s[1])))),
      h('h3', { class: 'sec' }, 'Auto daemon'),
      h('ul', { class: 'list' }, daemons.map(([p, d]) => h('li', { class: d?.notice ? '' : 'quiet' },
        h('span', { class: 'g' }, p.mode !== 'auto' ? '○' : d?.notice ? '⊘' : '→'), h('b', {}, p.name),
        h('div', { class: 'side' }, h('small', {}, d?.last_sent_at ? clock(d.last_sent_at) : '')),
        h('small', {}, p.mode !== 'auto' ? 'manual: the daemon sends nothing' : d ? `${d.sends} digest${d.sends === 1 ? '' : 's'} sent${d.notice ? ` · ${d.notice}` : ''}` : S.readOnly ? '?' : 'nothing sent yet')))),
    ],
  };
};
function renderSystemChip() {
  const tl = S.timelineStale.length;
  const bad = S.stale || tl;
  const chip = $('#sys');
  chip.setAttribute('aria-label', S.stale ? `System: stale, ${S.stale}` : tl ? 'System: timeline stuck' : 'System: live');
  chip.dataset.tone = bad ? 'warn' : 'ok';
  chip.querySelector('.g').textContent = bad ? '▲' : '●';
  $('#sys-t').textContent = S.stale ? 'Stale' : tl ? 'Timeline stuck' : 'System';
}
$('#sys').addEventListener('click', () => select({ kind: 'system' }));

// Go to: every place by name, Needs you first. On a phone this is how you get to a crew.
VIEWS.goto = () => {
  const q = (S.drafts.get('goto-q') ?? '').toLowerCase();
  const items = [
    ...attention().map((x) => ({ g: '?', k: 'topic', title: x.title, sub: x.sub, go: x.open })),
    { g: '⌂', title: 'City Hall', sub: 'topics · projects', go: () => select({ kind: 'hall' }) },
    ...(S.ws?.projects ?? []).flatMap((p) => [
      { g: '◎', title: `Mate · ${p.name}`, sub: hasMate(p) ? harnessName(p.mate.harness) : 'no Mate yet', go: () => select({ kind: 'mate', project: p.name }) },
      ...openCrews(p.name).map((t) => ({ g: STATE[t.state]?.[0] ?? '·', title: t.crew, sub: `${p.name} · ${t.role} · ${stateWord(t.state)}`, go: () => select({ kind: 'crew', project: p.name, crew: t.crew }) })),
      ...tentRepos(p).map((r) => ({ g: '⌂', title: `Tent ${r}`, sub: p.name, go: () => enterRoom(`${p.name}/${r}`) })),
    ]),
  ].filter((x) => !q || `${x.title} ${x.sub}`.toLowerCase().includes(q));
  const input = h('input', { type: 'text', id: 'goto-q', value: S.drafts.get('goto-q') ?? '', placeholder: 'crew, project, tent…', 'aria-label': 'Go to', autocomplete: 'off', oninput: (e) => (S.drafts.set('goto-q', e.target.value), render()), onkeydown: (e) => e.key === 'Enter' && items[0]?.go() });
  return {
    crumbs: [{ text: 'City' }, { text: 'Go to' }],
    title: 'Go to',
    sub: 'The camera flies there; no need to turn it',
    body: [h('div', { class: 'form' }, input), h('ul', { class: 'list' }, items.map((x) => h('li', { class: `click k-${x.k ?? ''}`, tabindex: '0', role: 'button', onclick: x.go, onkeydown: (e) => e.key === 'Enter' && x.go() }, h('span', { class: 'g' }, x.g), h('b', {}, x.title), h('small', {}, x.sub))))],
  };
};
$('#goto').addEventListener('click', () => select({ kind: 'goto' }, { fly: false }));
let nextI = 0;
function nextAttention() {
  const items = attention();
  if (items.length) items[nextI++ % items.length].open();
}
$('#needs').addEventListener('click', () => (phone() ? select({ kind: 'goto' }, { fly: false }) : nextAttention()));

// ---- Inside a tent ------------------------------------------------------------------------

let room = null;
let roomT = 0;
let cityView = null;
let roomToken = 0;
const roomScene = new THREE.Scene();
roomScene.background = new THREE.Color(PALETTE.paper);
lights(roomScene, renderer);
const feedEl = h('ol', { id: 'feed' });
const KIND = { spawn: 'spawn', status: 'status', ask: 'ask', answer: 'answer', handback: 'hand back', review: 'review', verdict: 'verdict', merge: 'merge', close: 'close', sleep: 'idle' };

VIEWS.tent = () => {
  const { project, repo } = S.sel;
  const tab = S.sel.tab ?? 'feed';
  const p = projectOf(project);
  const crews = p ? openCrews(project).filter(onTent(p, repo)) : [];
  const open = (t) => select({ kind: 'crew', project, crew: t.crew, back: { ...S.sel } });
  const inbox = (S.inbox.get(project) ?? []).length;
  const talk = (e) => (e.preventDefault(), select({ kind: 'mate', project, back: { ...S.sel } }));
  return {
    crumbs: [{ text: project, go: () => (leaveRoom(), select({ kind: 'block', project })) }, { text: 'tent' }],
    title: repo,
    sub: room ? `${project} · ${room.summary}` : `${project} · ${S.sel.error || 'loading…'}`,
    body: [
      ...bands(mode !== 'live' ? [h('div', { class: 'band replay' }, h('span', { class: 'g' }, '⏸'), h('span', {}, `Replaying ${$('#when').textContent}. Crews and actions below are now. `), h('button', { type: 'button', class: 'btn', style: 'min-height:32px', onclick: goLive }, 'Go Live'))] : []),
      p && hasMate(p) ? h('div', { class: 'links' }, h('a', { href: '#', onclick: talk }, `Talk to the Mate${inbox ? ` · ${inbox} in its inbox` : ''}`)) : null,
      tabs([['feed', 'Playback'], ['crews', 'Crews here', crews.length]], tab, (id) => ((S.sel = { ...S.sel, tab: id }), render())),
      tab === 'feed' ? feedEl : h('ul', { class: 'list' }, crews.length ? crews.map((t) => {
        const [g, words, k] = crewNote(project, t);
        return h('li', { class: `click${k ? ` k-${k}` : ''}`, tabindex: '0', role: 'button', onclick: () => open(t), onkeydown: (e) => e.key === 'Enter' && open(t) },
          h('span', { class: 'g' }, g), h('b', {}, `${t.crew} · ${t.role}`), h('small', {}, words));
      }) : h('li', {}, h('span', { class: 'g' }, '○'), h('b', {}, 'Nobody is working here now'))),
    ],
  };
};

// A crew in the tent's list: what it wants seen first - an incident, its
// work ready to merge, a question to its Mate - else its state.
function crewNote(project, t) {
  const f = crewFacts(project, t);
  const state = stateWord(t.state);
  if (f.incident) return ['!', `${f.incident.verb || 'incident'} ${ago(f.incident.at)} · ${state}`, 'incident'];
  if (f.ready && t.role !== 'review') return ['⇢', `ready to merge · ${t.branch ?? '?'}`, 'merge'];
  if (f.status?.verb === 'needs-decision') return ['◆', `asks its Mate · ${state}`, 'decision'];
  return [STATE[t.state]?.[0] ?? '·', state, ''];
}

// Histories read so far, by tent: kept while the project's task rows are the
// same ones, so a second visit, or a click after a hover, opens at once.
const histories = new Map(); // tentKey → { rows, promise }
function historyFor(key) {
  const tent = city?.tents.get(key);
  const p = projectOf(tent?.project);
  if (!tent || !p) return Promise.resolve({ since: '', tasks: [], events: [] });
  const rows = S.tasks.get(tent.project) ?? [];
  const have = histories.get(key);
  if (have && have.rows === rows) return have.promise;
  const promise = readHistory(api, tent.project, rows, onTent(p, tent.repo));
  histories.set(key, { rows, promise });
  promise.catch(() => histories.delete(key));
  return promise;
}

async function enterRoom(key) {
  const tent = city?.tents.get(key);
  if (!tent) return;
  const token = ++roomToken;
  if (!room) cityView = { target: controls.target.clone(), position: camera.position.clone() };
  document.body.classList.add('in-room', 'docked');
  for (const m of city.mascots.values()) if (m.chatAt === Infinity) m.chatAt = undefined;
  S.sel = { kind: 'tent', project: tent.project, repo: tent.repo, tab: 'feed' };
  ring.visible = false;
  tip.style.display = 'none';
  if (location.hash !== `#tent=${key}`) history.replaceState(null, '', `#tent=${key}`);
  render();
  const next = await buildRoom(key, token);
  if (!next) return;
  // Open on the last few events, playing up to now.
  setMode('live');
  roomT = next.events.at(-3)?.at ?? 0;
  flight = null;
  controls.target.copy(next.view.target);
  camera.position.copy(next.view.position);
  Object.assign(controls, ROOM_LIMITS);
  resize();
  render();
}

// Read the tent's history and put up its room in place of the one showing;
// null when another tent was asked for meanwhile.
async function buildRoom(key, token) {
  const tent = city.tents.get(key);
  const p = projectOf(tent.project);
  let h0 = { since: '', tasks: [], events: [] };
  try {
    // A click before the project's page arrived reads it first.
    if (!S.tasks.has(tent.project)) S.tasks.set(tent.project, (await api(`/api/projects/${enc(tent.project)}`)).tasks ?? []);
    h0 = await historyFor(key);
  } catch (e) {
    if (S.sel?.kind === 'tent') S.sel.error = e.message;
  }
  if (token !== roomToken) return null;
  const was = room;
  if (was) {
    roomScene.remove(was.group);
    was.dispose();
  }
  room = createTentRoom({ project: tent.project, repo: tent.repo, floor: city.blocks.get(tent.project).floor, mateHarness: p.mate?.harness, mateState: p.mate?.running ? p.mate.state : 'asleep', history: h0 });
  room.key = key;
  room.project = tent.project;
  roomScene.add(room.group);
  shown = -1;
  feedEl.replaceChildren(...room.events.map((ev, i) => h('li', { dataset: { i: String(i) }, onclick: () => {
    roomT = room.events[i].at;
    if (mode === 'live') setMode('play');
  } }, h('i', { dataset: { kind: ev.kind } }, KIND[ev.kind] ?? ev.kind), h('b', {}, ev.clock ? `${ev.who} · ${ev.clock}` : ev.who), h('span', {}, ev.text))));
  drawTicks();
  return room;
}

// New events on the tent's project: read the history again and keep the
// view where it was - at the same event when scrubbed back, or, live and
// caught up, on the first new event so it plays out.
let refreshTimer = 0;
function refreshRoom() {
  clearTimeout(refreshTimer);
  refreshTimer = setTimeout(async () => {
    if (!room) return;
    const old = room;
    const caughtUp = mode === 'live' && roomT >= old.duration;
    const at = old.events[old.indexAt(roomT)];
    const next = await buildRoom(old.key, roomToken);
    if (!next) return;
    if (caughtUp) roomT = next.events[old.events.length]?.at ?? next.duration;
    else if (at?.iso) {
      const j = next.events.findIndex((e) => e.iso === at.iso && e.crew === at.crew && e.kind === at.kind);
      if (j >= 0) roomT = next.events[j].at + (roomT - at.at);
    }
  }, 800);
}

function leaveRoom() {
  if (!room && !document.body.classList.contains('in-room')) return;
  roomToken++;
  document.body.classList.remove('in-room');
  if (room) {
    roomScene.remove(room.group);
    room.dispose();
    room = null;
  }
  setMode('live');
  Object.assign(controls, CITY_LIMITS, { maxDistance: 170 * ((city?.size ?? 3) / 3) });
  if (cityView) {
    controls.target.copy(cityView.target);
    camera.position.copy(cityView.position);
    cityView = null;
  }
  S.sel = null;
  document.body.classList.remove('docked', 'sheet-peek', 'sheet-full');
  resize();
  render();
  if (location.hash) history.replaceState(null, '', location.pathname + location.search);
}

// The scrub bar. Acting is for Live only: a scrubbed-back tent shows the
// past, and every button waits until it is now again.
const seek = $('#seek');
const playBtn = $('#play');
const speedBtn = $('#speed');
let mode = 'live';
let speed = 1;
const SPEEDS = [1, 4, 16, 64];
let dragging = null; // the mode before a drag
function setMode(next) {
  const was = mode;
  mode = next;
  playBtn.textContent = mode === 'pause' ? '▶' : '❚❚';
  playBtn.setAttribute('aria-label', mode === 'pause' ? 'Play' : 'Pause');
  $('#golive').classList.toggle('on', mode === 'live');
  if ((was === 'live') !== (mode === 'live')) render();
}
function goLive() {
  if (room) roomT = room.duration;
  setMode('live');
}
playBtn.addEventListener('click', () => {
  if (!room) return;
  if (mode !== 'pause') return setMode('pause');
  // Play from the start when already at now.
  if (roomT >= room.duration) roomT = 0;
  setMode('play');
});
speedBtn.addEventListener('click', () => {
  speed = SPEEDS[(SPEEDS.indexOf(speed) + 1) % SPEEDS.length];
  speedBtn.textContent = `${speed}×`;
  if (mode === 'live' && room && roomT < room.duration) setMode('play');
});
$('#golive').addEventListener('click', goLive);
seek.addEventListener('input', () => {
  if (!room) return;
  dragging ??= mode;
  setMode('pause');
  roomT = Number(seek.value) * room.duration;
});
seek.addEventListener('change', () => {
  setMode(dragging === 'pause' ? 'pause' : Number(seek.value) < 0.999 ? 'play' : 'live');
  dragging = null;
});
// Day marks and merges along the bar.
function drawTicks() {
  const marks = [];
  let day = '';
  let lastDay = -Infinity;
  // A day's label needs about 40px; one closer to the last is left out.
  const gap = 4000 / Math.max(1, seek.clientWidth);
  for (const ev of room.events) {
    if (!ev.iso) continue;
    const x = (100 * ev.at) / room.duration;
    const d = ev.clock.slice(0, 5);
    if (d !== day && x - lastDay < gap) day = d;
    if (d !== day) {
      lastDay = x;
      day = d;
      marks.push(h('span', { class: 'day', style: `left:${x}%` }, d));
    }
    if (ev.kind === 'merge') marks.push(h('i', { class: 'merge', style: `left:${x}%`, title: ev.text }));
  }
  $('#ticks').replaceChildren(...marks);
}
function fromHash() {
  let key = '';
  try {
    key = decodeURIComponent(location.hash.match(/^#tent=(.+)$/)?.[1] ?? '');
  } catch {}
  if (city?.tents.has(key)) enterRoom(key);
}
addEventListener('hashchange', fromHash);

// ---- The merge crate: carried from the crew to the tent's sign -------------------------

function startCrate(project, crew) {
  const m = city?.mascots.get(`${project}/${crew}`);
  const t = taskOf(project, crew);
  const tent = city?.tents.get(`${project}/${t?.repo || tentRepos(projectOf(project))[0]}`);
  if (!m || !tent || room) return;
  const from = m.root.position.clone().setY(1.3);
  const to = new THREE.Vector3(tent.x - tent.w / 2 + 0.7, 1.1, tent.z + tent.d / 2 + 0.45);
  crateFlight = { from, to, t0: performance.now() };
  crate.visible = true;
}
function crateBack() {
  if (crateFlight) Object.assign(crateFlight, { back: true, t0: performance.now() });
}

// ---- Pointer -----------------------------------------------------------------------------

const ray = new THREE.Raycaster();
const pointer = new THREE.Vector2();
const ground = new THREE.Plane(new THREE.Vector3(0, 1, 0), 0);
// The distance from one block's centre to the next: the first ring of blocks sits one pitch out.
const pitchOf = () => Math.max(...[...city.blocks.values()].map((b) => Math.max(Math.abs(b.centre.x), Math.abs(b.centre.z))), 1);
// What is under the pointer: { mascot } | { tent } | { hall } | { block } | { lot } | null.
function pick(e) {
  const r = canvas.getBoundingClientRect();
  pointer.set(((e.clientX - r.left) / r.width) * 2 - 1, -((e.clientY - r.top) / r.height) * 2 + 1);
  ray.setFromCamera(pointer, camera);
  const list = [...(room ? room.mascots : city?.mascots ?? new Map()).values()].filter((m) => m.root.visible);
  const hit = ray.intersectObjects(list.map((m) => m.body))[0];
  if (hit) return { mascot: list.find((m) => m.body === hit.object).id };
  if (room || !city) return null;
  let o = ray.intersectObjects([...[...city.tents.values()].map((b) => b.group), ...city.hall], true)[0]?.object;
  while (o && !o.userData.tent && !city.hall.includes(o)) o = o.parent;
  if (o) return o.userData.tent ? { tent: o.userData.tent } : { hall: true };
  // A block, or an empty park lot: a place to add a project.
  const at = ray.ray.intersectPlane(ground, new THREE.Vector3());
  if (!at || !city.blocks.size) return null;
  const pitch = pitchOf();
  const slot = [Math.round(at.x / pitch), Math.round(at.z / pitch)];
  const half = (city.size - 1) / 2;
  if (Math.abs(slot[0]) > half || Math.abs(slot[1]) > half || (slot[0] === 0 && slot[1] === 0)) return null;
  for (const [name, b] of city.blocks) if (Math.round(b.centre.x / pitch) === slot[0] && Math.round(b.centre.z / pitch) === slot[1]) return { block: name };
  return city.hidden ? null : { lot: new THREE.Vector3(slot[0] * pitch, 0.5, slot[1] * pitch) };
}
const tip = $('#tip');
function describe(tg) {
  const seats = room ? room.seats : city.seats;
  if (tg.mascot) {
    const s = seats.get(tg.mascot);
    if (!s) return null;
    if (s.kind === 'mate') return [`Mate · ${s.project}`, `${harnessName(s.harness)}${s.state ? ` · ${stateWord(s.state)}` : ''} · click for chat and actions`];
    return [`${s.crew} · ${s.role}`, `${[harnessName(s.harness), s.model, s.effort].filter(Boolean).join(' · ')}${s.state ? ` · ${stateWord(s.state)}` : ''} · click for actions`];
  }
  if (tg.tent) {
    const b = city.tents.get(tg.tent);
    return [`${b.repo} · ${b.project}`, `${b.merged ?? '?'} merged · ${b.crews.size} at work · click to step in`];
  }
  if (tg.hall) return ['City Hall', 'topics from the Mates · projects · click to open'];
  if (tg.block) return [tg.block, 'click for its Mate, tents and crews'];
  if (tg.lot) return ['Empty lot', 'click to add a project here'];
  return null;
}
let hoverTimer = 0;
canvas.addEventListener('pointermove', (e) => {
  const tg = pick(e);
  const words = tg && describe(tg);
  // Read a tent's history while the pointer rests on it, so the click opens at once.
  clearTimeout(hoverTimer);
  if (tg?.tent && S.tasks.has(city.tents.get(tg.tent)?.project)) hoverTimer = setTimeout(() => historyFor(tg.tent).catch(() => {}), 150);
  canvas.style.cursor = words ? 'pointer' : '';
  if (words && !phone()) {
    tip.replaceChildren(h('b', {}, words[0]), h('span', {}, words[1]));
    tip.style.display = 'block';
    tip.style.left = `${e.clientX}px`;
    tip.style.top = `${e.clientY}px`;
  } else tip.style.display = 'none';
});
canvas.addEventListener('pointerleave', () => (tip.style.display = 'none'));
// A click is a press that did not drag the view.
let down = null;
canvas.addEventListener('pointerdown', (e) => (down = [e.clientX, e.clientY]));
canvas.addEventListener('click', (e) => {
  if (down && Math.hypot(e.clientX - down[0], e.clientY - down[1]) > 5) return;
  const tg = pick(e);
  if (!tg) return;
  tip.style.display = 'none';
  if (tg.tent) return enterRoom(tg.tent);
  if (tg.hall) return select({ kind: 'hall' });
  if (tg.block) return select({ kind: 'block', project: tg.block });
  if (tg.lot) return select({ kind: 'lot', at: tg.lot });
  const s = (room ? room.seats : city.seats).get(tg.mascot);
  if (!s) return;
  // In a tent, the panel opened keeps the tent behind it to go back to.
  const back = room ? (S.sel?.kind === 'tent' ? { ...S.sel } : S.sel?.back?.kind === 'tent' ? S.sel.back : { kind: 'tent', project: room.project, repo: room.repo, tab: 'feed' }) : null;
  if (s.kind === 'mate') return select({ kind: 'mate', project: s.project, back }, { fly: !room });
  select({ kind: 'crew', project: s.project, crew: s.crew, back }, { fly: !room });
});

// ---- Keyboard -----------------------------------------------------------------------------

addEventListener('keydown', (e) => {
  if (scrim.classList.contains('on')) return;
  const typing = /^(INPUT|TEXTAREA|SELECT)$/.test(e.target.tagName);
  if (e.key === 'Escape') {
    if (typing) return e.target.blur();
    if (S.sel?.topic) return select({ kind: 'hall', tab: 'topics' }, { fly: false });
    if (S.sel) return closeDock();
    return;
  }
  if (typing || e.metaKey || e.ctrlKey || e.altKey) return;
  if (e.key === ' ' && room && (e.target === document.body || e.target === canvas)) {
    e.preventDefault();
    playBtn.click();
  } else if ((e.key === 'l' || e.key === 'L') && room) goLive();
  else if (e.key === '/' || e.key === 'g') {
    e.preventDefault();
    select({ kind: 'goto' }, { fly: false });
    document.getElementById('goto-q')?.focus();
  } else if (e.key === 'n') nextAttention();
  else if (e.key === 'h') select({ kind: 'hall' });
});

// ---- Layout ----------------------------------------------------------------------------------

// The dock covers the right of a wide screen, the sheet the bottom of a phone:
// shift the view so the thing in focus sits in the middle of what is left.
function resize() {
  const w = canvas.clientWidth;
  const hh = canvas.clientHeight;
  renderer.setSize(w, hh, false);
  camera.aspect = w / hh;
  const docked = document.body.classList.contains('docked');
  if (docked && !phone()) camera.setViewOffset(w, hh, (dock.offsetWidth + 20) / 2, 0, w, hh);
  else if (docked && phone()) camera.setViewOffset(w, hh, 0, dock.offsetHeight / 2 - 20, w, hh);
  else camera.clearViewOffset();
  camera.updateProjectionMatrix();
  $('#scrub').style.bottom = room && phone() ? `${dock.offsetHeight + 8}px` : '';
}
addEventListener('resize', resize);
new ResizeObserver(resize).observe(dock);

// ---- Frame loop --------------------------------------------------------------------------------

const ease = (x) => x * x * x * (x * (x * 6 - 15) + 10);
let shown = -1;
let last = performance.now();
const drawn = new WeakSet();
renderer.setAnimationLoop((ms) => {
  if (!city) return;
  const dt = (ms - last) / 1000;
  last = ms;
  if (room) {
    if (mode !== 'pause') roomT += Math.min(dt, 0.1) * (mode === 'play' ? speed : 1);
    if (mode === 'play' && roomT >= room.duration) setMode('live');
    room.update(roomT);
    if (dragging === null) seek.value = String(Math.min(1, roomT / room.duration));
    // Light up the feed up to the playhead; its entry is marked.
    const upto = room.indexAt(roomT) + 1;
    if (upto !== shown) {
      const items = feedEl.children;
      const lo = Math.max(0, Math.min(shown, upto) - 1);
      const hi = shown < 0 ? items.length : Math.min(items.length, Math.max(shown, upto) + 1);
      for (let i = lo; i < hi; i++) {
        items[i].classList.toggle('past', i < upto);
        items[i].classList.toggle('now', i === upto - 1);
      }
      if (feedEl.isConnected) feedEl.querySelector('.now')?.scrollIntoView({ block: 'nearest', behavior: mode === 'play' && speed > 4 ? 'auto' : 'smooth' });
      shown = upto;
    }
    const ev = room.events[shown - 1];
    const words = mode === 'live' && roomT >= room.duration ? 'now' : ev?.clock ? `${ev.clock} · ${ev.who}` : '';
    if ($('#when').textContent !== words) {
      $('#when').textContent = words;
      if (mode !== 'live' && S.sel?.kind === 'crew') render();
    }
  } else city.update(now());
  if (flight) {
    const u = ease(Math.min(1, (ms - flight.t0) / 800));
    controls.target.lerpVectors(flight.from[0], flight.to[0], u);
    camera.position.lerpVectors(flight.from[1], flight.to[1], u);
    if (u >= 1) flight = null;
  }
  if (crateFlight) {
    const u = Math.min(1, (ms - crateFlight.t0) / 1400);
    const k = crateFlight.back ? 1 - ease(u) : ease(u);
    crate.position.lerpVectors(crateFlight.from, crateFlight.to, k);
    crate.position.y += Math.sin(Math.PI * k) * 1.2;
    crate.rotation.y = k * 3;
    if (u >= 1 && (crateFlight.back || !S.busy.size)) {
      crate.visible = false;
      crateFlight = null;
    }
  }
  controls.update();
  renderer.render(room ? roomScene : scene, camera);
  if (!room) placePins();
  // Marks for the browser's performance timeline: the first frame of the city, and of each tent.
  if (!drawn.has(room ?? city)) {
    drawn.add(room ?? city);
    performance.mark(room ? `city:tent ${room.key}` : 'city:drawn');
  }
});

// ---- Boot -------------------------------------------------------------------------------------

Object.assign(controls, CITY_LIMITS);
resize();
loadAll().then(fromHash, (e) => {
  setLive('stale', e.message);
  $('#boot').textContent = `Agent City could not load: ${e.message}`;
}).finally(poll);
refreshNews();
refreshDuty();
refreshBoard();
