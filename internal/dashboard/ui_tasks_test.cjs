// The project page's task table (CON-155): sortable columns and pages that
// hold across polls. Run with:
// node --test internal/dashboard/ui_tasks_test.cjs
const { test } = require("node:test");
const assert = require("node:assert/strict");
const { tokens, at, keyed, boot } = require("./ui_testdom.cjs");

// n tasks, task-0 spawned first; every third one closed. Tokens fall as the
// index rises so the token sort is not the spawn order.
const task = i => ({ crew: "task-" + i, text: "Task number " + i, state: "at_desk_working", closed: i % 3 === 2,
  close_state: i % 3 === 2 ? "finished" : "", spawned_at: at(i), age_ms: (60 - i) * 1000, tokens: tokens(1000 * (60 - i)),
  cost: null, question_count: i % 4, waited_ms: 0, branch: i % 5 ? "b-" + i : "" });
const project = n => ({ project: "shop", mode: "auto", mate: {}, inbox: [], tasks: Array.from({ length: n }, (_, i) => task(i)) });

const ids = view => view.querySelectorAll(".clickable").map(tr => tr.querySelector("a").textContent);
const range = view => view.querySelector(".pager").querySelector(".range");
const chip = (view, label) => view.querySelectorAll(".chip").find(b => b.textContent.startsWith(label + " "));

test("the table opens on newest spawn first, 25 rows a page", async () => {
  const app = await boot(project(30), "#/p/shop");
  chip(app.view, "all").click();
  assert.deepEqual(ids(app.view).slice(0, 3), ["task-29", "task-28", "task-27"]);
  assert.equal(ids(app.view).length, 25);
  assert.equal(range(app.view).textContent, "1–25 of 30");
  assert.equal(keyed(app.view, "data-page", "prev").getAttribute("disabled"), "", "the first page has no previous page");
  assert.equal(app.view.querySelectorAll("th[aria-sort]").length, 0, "no column claims the default order");

  keyed(app.view, "data-page", "next").click();
  assert.deepEqual(ids(app.view), ["task-4", "task-3", "task-2", "task-1", "task-0"]);
  assert.equal(range(app.view).textContent, "26–30 of 30");
  assert.equal(keyed(app.view, "data-page", "next").getAttribute("disabled"), "");
});

test("a short list has no pager", async () => {
  const app = await boot(project(4), "#/p/shop");
  assert.equal(app.view.querySelector(".pager"), null);
});

test("a header sorts its column, and a second click turns it round", async () => {
  const app = await boot(project(30), "#/p/shop");
  chip(app.view, "all").click();
  keyed(app.view, "data-page", "next").click();

  keyed(app.view, "data-sort", "tokens").click();
  const th = keyed(app.view, "data-sort", "tokens").parentNode;
  assert.equal(th.getAttribute("aria-sort"), "descending", "numbers start high");
  assert.deepEqual(ids(app.view).slice(0, 2), ["task-0", "task-1"], "the most tokens first");
  assert.equal(range(app.view).textContent, "1–25 of 30", "a new order starts at page one");

  keyed(app.view, "data-sort", "tokens").click();
  assert.equal(keyed(app.view, "data-sort", "tokens").parentNode.getAttribute("aria-sort"), "ascending");
  assert.deepEqual(ids(app.view).slice(0, 2), ["task-29", "task-28"]);

  keyed(app.view, "data-sort", "id").click();
  assert.equal(keyed(app.view, "data-sort", "id").parentNode.getAttribute("aria-sort"), "ascending", "words start at A");
  assert.deepEqual(ids(app.view).slice(0, 3), ["task-0", "task-1", "task-10"]);
});

test("a task with no value sorts last both ways", async () => {
  const app = await boot(project(6), "#/p/shop");
  chip(app.view, "all").click();
  keyed(app.view, "data-sort", "branch").click();
  assert.equal(ids(app.view).pop(), "task-5", "task-0 and task-5 have no branch; ties keep the server's order");
  keyed(app.view, "data-sort", "branch").click();
  assert.deepEqual(ids(app.view).slice(-2), ["task-0", "task-5"]);
});

test("a filter chip returns to page one; sort and page hold across a poll", async () => {
  const app = await boot(project(60), "#/p/shop");
  chip(app.view, "all").click();
  keyed(app.view, "data-sort", "asked").click();
  keyed(app.view, "data-page", "next").click();
  const before = ids(app.view);
  await app.refresh(project(60), true);
  assert.deepEqual(ids(app.view), before, "a poll keeps the order and the page");
  assert.equal(range(app.view).textContent, "26–50 of 60");

  await app.refresh(project(30), true);
  assert.equal(range(app.view).textContent, "26–30 of 30", "a shorter list keeps the reader on its last page");

  await app.refresh(project(60), true);
  chip(app.view, "open").click();
  assert.equal(range(app.view).textContent, "1–25 of 40");
  assert.equal(keyed(app.view, "data-sort", "asked").parentNode.getAttribute("aria-sort"), "descending", "the filter keeps the order");
});
