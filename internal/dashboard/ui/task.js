/*
 * task.js - the task tier of the admin dashboard (docs/design/task-page.md).
 *
 * One header, two tabs. "Journey" answers who handed what to whom, when,
 * and at what price: a swimlane of Captain, Mate, Crew, Machine and GitHub,
 * a numbered table of the hand-offs, the project's workflow checked, and
 * three cards that read the task at a glance. "Trace" is the per-step
 * diagnostics trace.js draws (the stages strip, findings, effort by kind,
 * the fold of raw records); `/usage` opens it.
 *
 * It is a classic script like humanize.js: it declares mateTaskView on the
 * global scope, and app.js calls it once with the helpers the two share
 * (element building, number words, the Trace tab's renderer), so the task
 * page can be read and tested on its own while app.js keeps the routing,
 * the polling and the saved state.
 *
 * The Journey tab reads the task body: ledger, prompt turns, status lines,
 * questions, executions, and `journey` (brief, hand-back, meta, reviews,
 * the Mate's turns about this crew, incidents, the project's workflow
 * checked). What could not be read is said to be missing rather than drawn
 * as empty. Each number appears once: the header owns the totals, the tabs
 * only split them.
 */
function mateTaskView(h) {
  var tab = "journey";

  // A command that kept the crew waiting for at least this long is drawn
  // on the Machine lane; shorter ones are noise there and stay in Trace.
  var MACHINE_MIN_MS = 5000;
  // A gap between one prompt's last call and the next prompt's first is
  // drawn as idle when it is at least this long.
  var IDLE_MIN_MS = 5000;

  var LANES = [
    { key: "captain", label: "Captain", sub: "questions and answers" },
    { key: "mate", label: "Mate", sub: "turns about this crew" },
    { key: "observer", label: "Observer", sub: "incidents" },
    { key: "crew", label: "Crew", sub: "stages by work type" },
    { key: "machine", label: "Machine", sub: "commands of 5 s or more" },
    { key: "github", label: "GitHub", sub: "pull request" }
  ];
  var LANE_LABEL = { captain: "Captain", mate: "Mate", observer: "Observer", crew: "Crew", machine: "Machine", github: "GitHub" };

  function reset() { tab = "journey"; }
  function openTab(name) { tab = name === "trace" ? "trace" : "journey"; }
  function current() { return tab; }

  // ------------------------------------------------------------ the page

  function render(body) {
    var out = h.el("div", { class: "task-page" });
    out.appendChild(header(body));
    out.appendChild(tabBar(body));
    out.appendChild(tab === "trace" ? tracePanel(body) : journeyPanel(body));
    return out;
  }

  function header(body) {
    var ledger = body.ledger || {};
    var p = body.performance;
    var out = h.el("div", { class: "task-header" });
    out.appendChild(h.el("div", { class: "card-head crew-head" }, [
      h.el("h1", { text: body.crew }),
      h.taskPill(ledger),
      h.runtimeChips(body)
    ]));
    out.appendChild(h.el("p", { class: "muted task-title", text: ledger.text || "" }));
    out.appendChild(kpis(body, ledger, p));
    return out;
  }

  // The seven numbers the page owns. Unknown is "?", a true zero is "–".
  function kpis(body, ledger, p) {
    var tokens = ledger.tokens || {};
    var span = h.spawnSpanMs(ledger);
    var time = (p && p.time) || null;
    var peak = contextPeak(body);
    var when = ledger.spawned_at ? h.clock(ledger.spawned_at).slice(0, 5) : "?";
    when += " → " + (ledger.closed ? (ledger.closed_at ? h.clock(ledger.closed_at).slice(0, 5) : "?") : "open");
    var machine = time && time.tool_elapsed_ms != null ? h.fmtMs(time.tool_elapsed_ms) : "?";
    var machineSub = time && time.tool_elapsed_ms != null && time.prompt_elapsed_ms
      ? h.fmtPct(100 * time.tool_elapsed_ms / time.prompt_elapsed_ms) + " of the crew's time" : "not measured";
    return h.el("div", { class: "task-kpis", role: "list" }, [
      kpi("tokens", "crew tokens", ledger.turns === 0 ? "–" : h.fmtTokens(tokens.total),
        "thinking " + h.fmtTokens(tokens.thinking) + " inside output"),
      mateKpi(body),
      kpi("calls", "model calls", dashed(ledger.turns), h.count(ledger.tool_count || 0, "tool call")),
      kpi("time", ledger.closed ? "spawn → close" : "open for", span == null ? "?" : h.fmtMs(span), when),
      kpi("machine", "waiting on the machine", machine, machineSub),
      kpi("context", "context peak", peak == null ? "?" : h.fmtTokens(peak),
        ledger.context_tokens_last == null ? "window unknown" : "after the last call " + h.fmtTokens(ledger.context_tokens_last)),
      kpi("asked", "asked · handed back", dashed(ledger.question_count) + " · " + dashed(ledger.handback_count),
        ledger.waited_ms ? "waited " + h.fmtMs(ledger.waited_ms) : "no decision wait")
    ]);
  }

  // Mate's turns about this crew, whole turns: what else the Mate did in
  // the same turn is not separated out, and the sub-line says so.
  function mateKpi(body) {
    var turns = journeyOf(body).mate_turns;
    if (!turns) return kpi("mate", "Mate around this task", "?", "Mate's turns could not be read");
    if (!turns.length) return kpi("mate", "Mate around this task", "–", "no Mate turn names this crew");
    var total = 0, calls = 0;
    turns.forEach(function (t) { total += (t.tokens && t.tokens.total) || 0; calls += t.model_calls || 0; });
    return kpi("mate", "Mate around this task", h.fmtTokens(total), h.count(turns.length, "whole turn") + " · " + h.count(calls, "call"));
  }

  function journeyOf(body) { return body.journey || {}; }

  function kpi(key, label, value, sub) {
    return h.el("div", { class: "kpi", role: "listitem", "data-kpi": key }, [
      h.el("span", { class: "kpi-label", text: label }),
      h.el("span", { class: "kpi-value num", text: value }),
      h.el("span", { class: "kpi-sub", text: sub })
    ]);
  }

  function contextPeak(body) {
    var peak = null;
    (body.turns || []).forEach(function (t) {
      if (t.context_tokens_after != null && (peak == null || t.context_tokens_after > peak)) peak = t.context_tokens_after;
    });
    if (peak == null && body.ledger && body.ledger.context_tokens_last != null) peak = body.ledger.context_tokens_last;
    return peak;
  }

  function dashed(n) { return n == null ? "?" : n ? String(n) : "–"; }

  function tabBar(body) {
    var tabs = [
      { key: "journey", label: "Journey", sub: "who handed what to whom" },
      { key: "trace", label: "Trace", sub: "every step of the agent" }
    ];
    return h.el("div", { class: "tabs", role: "tablist" }, tabs.map(function (t) {
      return h.el("button", { class: "tab", type: "button", role: "tab", "data-tab": t.key, "data-focus": "tab:" + t.key,
        "aria-selected": tab === t.key ? "true" : "false",
        onclick: function () { if (tab !== t.key) { tab = t.key; h.draw(); } } }, [
        h.txt(t.label), h.el("small", { text: t.sub })
      ]);
    }));
  }

  // ------------------------------------------------------------ trace tab

  // The diagnostics app.js has always drawn for a crew, unchanged; the
  // trace tab's own shape is the third PR of docs/design/task-page.md.
  function tracePanel(body) {
    return h.el("div", { class: "tab-panel", role: "tabpanel", "data-panel": "trace" }, [h.traceBody(body)]);
  }

  // ---------------------------------------------------------- journey tab

  function journeyPanel(body) {
    var out = h.el("div", { class: "tab-panel", role: "tabpanel", "data-panel": "journey" });
    var j = journey(body);
    if (!j) {
      out.appendChild(h.el("div", { class: "notice", "data-tone": "quiet" }, [
        h.el("div", { class: "what", text: "Nothing has happened to this crew yet" }),
        h.el("div", { class: "why", text: "No spawn time, prompt, status line or question has been recorded." })
      ]));
      return out;
    }
    out.appendChild(h.el("section", { class: "section journey-lanes", "aria-label": "Swimlane of the task" }, [
      h.el("h2", {}, [h.txt("Journey"), h.el("small", { text: j.t0 === j.t1 ? "" : h.clock(j.t0iso) + " → " + h.clock(j.t1iso) })]),
      h.el("div", { class: "card lanes-card" }, [swimlane(body, j)]),
      laneLegend(j)
    ]));
    out.appendChild(h.el("section", { class: "section", "aria-label": "Hand-offs of the task" }, [
      h.el("h2", {}, [h.txt("Step by step"), h.el("small", { text: "one row per numbered arrow" })]),
      stepsTable(body, j)
    ]));
    if (j.journey.workflow) out.appendChild(h.el("section", { class: "section", "aria-label": "The project's workflow" }, [
      h.el("h2", {}, [h.txt("Repo workflow"), h.el("small", { text: "the steps this repo asks every crew to take" })]),
      h.el("p", { class: "workflow-about muted", text: "The steps come from the workflow list in the project's project.yaml. Each one is ticked from the crew's recording: " +
        "a command it ran, a file it read, a skill it loaded, or a line of its hand-back. Under each step is what would have counted." }),
      workflowTable(j.journey.workflow),
      workflowLegend()
    ]));
    out.appendChild(h.el("section", { class: "section", "aria-label": "The task at a glance" }, [
      h.el("h2", { text: "At a glance" }),
      glanceCards(body, j)
    ]));
    if ((j.journey.missing || []).length) out.appendChild(h.el("p", { class: "journey-missing muted", text: "Not in this answer: " + j.journey.missing.join(" · ") }));
    return out;
  }

  var WORKFLOW_GLYPH = { done: "✓", missed: "✗", "n/a": "–", unknown: "?" };

  function workflowTable(checks) {
    var table = h.el("table", { class: "journey-steps workflow" });
    table.appendChild(h.el("tr", {}, [h.el("th", { text: "step the repo asks for" }), h.el("th", { text: "crew" }), h.el("th", { text: "evidence" })]));
    checks.forEach(function (c) {
      table.appendChild(h.el("tr", { "data-workflow": c.state }, [
        h.el("td", { class: "step" }, [h.el("span", { class: "what", text: c.step }),
          c.rule ? h.el("span", { class: "sub rule", text: "counts when the crew " + c.rule }) : null]),
        h.el("td", { class: "st", "data-state": c.state }, [h.el("span", { class: "glyph", "aria-hidden": "true", text: WORKFLOW_GLYPH[c.state] || "?" }), h.txt(" " + c.state)]),
        h.el("td", { class: "ev" }, [c.at ? h.el("span", { class: "n num", text: h.clock(c.at) + " " }) : null, h.txt(c.evidence || "")])
      ]));
    });
    var done = checks.filter(function (c) { return c.state === "done"; }).length;
    var applicable = checks.filter(function (c) { return c.state === "done" || c.state === "missed"; }).length;
    table.appendChild(h.el("tr", { class: "total" }, [h.el("td", { text: "taken" }), h.el("td", { class: "st num", text: done + " / " + applicable }), h.el("td", { class: "ev muted", text: "of the steps that apply" })]));
    return table;
  }

  var WORKFLOW_MEANS = [
    ["done", "found in the recording"],
    ["missed", "looked for and not found"],
    ["n/a", "does not apply to this task: no changed file it is about, or the repo added it after the task started"],
    ["unknown", "could not be checked: nothing recorded to look in, or a bad pattern"]
  ];

  function workflowLegend() {
    return h.el("ul", { class: "workflow-legend", "aria-label": "what the states mean" }, WORKFLOW_MEANS.map(function (m) {
      return h.el("li", { "data-state": m[0] }, [h.el("span", { class: "glyph", "aria-hidden": "true", text: WORKFLOW_GLYPH[m[0]] }),
        h.el("b", { text: " " + m[0] }), h.txt(": " + m[1])]);
    }));
  }

  // journey builds the view-model once from the body: the time span, the
  // bars and dots of every lane, and the hand-offs as numbered steps. Every
  // entry carries the fact it was read from, so a reader can check it in
  // Trace.
  function journey(body) {
    var ledger = body.ledger || {};
    var p = body.performance || {};
    var prompts = p.prompt_turns || [];
    var execs = p.executions || [];
    var turns = body.turns || [];
    var lines = body.status_lines || [];
    var questions = body.questions || [];
    var j = journeyOf(body);
    var mateTurns = j.mate_turns || [];
    var incidents = j.incidents || [];
    var meta = j.meta || {};

    var times = [];
    function note(ts) { var n = h.timestamp(ts); if (n != null) times.push(n); }
    note(ledger.spawned_at); note(ledger.closed_at);
    prompts.forEach(function (prompt) { note(prompt.prompt_at); note(prompt.started_at); note(prompt.ended_at); });
    turns.forEach(function (t) { note(t.started_at); note(t.ended_at); });
    execs.forEach(function (e) { note(e.started_at); note(e.ended_at); });
    lines.forEach(function (s) { note(s.at); });
    questions.forEach(function (q) { note(q.asked_at); note(q.answered_at); });
    mateTurns.forEach(function (t) { note(t.prompt_at); note(t.started_at); note(t.ended_at); });
    incidents.forEach(function (i) { note(i.at); });
    if (!times.length) return null;
    var t0 = Math.min.apply(null, times);
    var t1 = Math.max.apply(null, times);
    if (!ledger.closed) t1 = Math.max(t1, Date.now());
    if (t1 - t0 < 60000) t1 = t0 + 60000;

    var steps = [], bars = [], dots = [], idle = [];

    // Prompts: the first one is the spawn; a later one is either a
    // background command reporting back (the harness wraps that in a
    // task-notification) or a new message from Mate.
    prompts.forEach(function (prompt, i) {
      var at = prompt.prompt_at || prompt.started_at;
      var text = prompt.prompt || "";
      var notification = /<task-notification>/i.test(text);
      var cost = prompt.model_calls === 0 ? "usage pending"
        : h.fmtMs(prompt.elapsed_ms) + " · " + h.fmtTokens(prompt.tokens && prompt.tokens.total) + " · " + h.count(prompt.model_calls || 0, "call");
      var step = { at: i === 0 && ledger.spawned_at ? ledger.spawned_at : at, prompt: prompt,
        from: notification ? "machine" : "mate", to: "crew",
        what: i === 0 ? "Spawned the crew" : notification ? "A background command finished" : "Prompt " + (i + 1),
        sub: h.excerpt(notification ? notificationText(text) : plainText(text), 160), cost: cost,
        brief: i === 0 ? j.brief : null };
      steps.push(step);
      (prompt.overview && prompt.overview.steps || []).forEach(function (stage) {
        if (!stage.started_at) return;
        bars.push({ lane: "crew", from: stage.started_at, to: stage.ended_at || (ledger.closed ? stage.started_at : null), kind: stage.kind,
          label: h.kindLabel(stage.kind), prompt: prompt,
          title: h.kindLabel(stage.kind) + " · " + stageCost(stage) });
      });
      if (i > 0) {
        var prev = prompts[i - 1];
        var gap = h.timestamp(at) - h.timestamp(prev.ended_at);
        if (prev.ended_at && gap >= IDLE_MIN_MS) idle.push({ lane: "crew", from: prev.ended_at, to: at, label: "idle " + h.fmtMs(gap) + ", no tokens" });
      }
    });
    if (!prompts.length && ledger.spawned_at) {
      steps.push({ at: ledger.spawned_at, from: "mate", to: "crew", what: "Spawned the crew", sub: "no prompt recorded yet", cost: "–", brief: j.brief });
    }

    // The Mate's turns about this crew: a bar on its lane, and a step from
    // whoever woke it. The one that spawned the crew is the spawn step's
    // price on the Mate's side, so it is not a second row.
    mateTurns.forEach(function (t) {
      var spawned = (t.activities || []).some(function (a) { return a.kind === "crew.spawned" && a.crew === body.crew; });
      var what = mateTurnWhat(t, body.crew);
      bars.push({ lane: "mate", from: t.started_at || t.prompt_at, to: t.ended_at || null, label: what, mate: t,
        title: what + " · " + mateTurnCost(t) + (t.prompt ? " · " + h.excerpt(t.prompt, 120) : "") });
      if (spawned) {
        var spawn = steps.filter(function (s) { return s.what === "Spawned the crew"; })[0];
        if (spawn) { spawn.mate = t; return; }
      }
      steps.push({ at: t.prompt_at || t.started_at, from: t.source === "captain" ? "captain" : t.source === "crew" ? "crew" : "mate", to: "mate",
        what: what, sub: h.excerpt(t.prompt || "", 160) + (t.response ? " → " + h.excerpt(t.response, 120) : ""), cost: mateTurnCost(t), mate: t });
    });

    incidents.forEach(function (inc) {
      var open = inc.state === "open";
      dots.push({ lane: "observer", at: inc.at, label: (open ? "" : "resolved ") + inc.kind, tone: open ? "critical" : "muted",
        title: inc.kind + " " + inc.state + " · " + (inc.text || "") });
      if (open) steps.push({ at: inc.at, from: "observer", to: "mate", what: "Incident " + inc.kind.replace(/_/g, " "), sub: inc.text || "", cost: "–", bad: true });
    });

    questions.forEach(function (q) {
      steps.push({ at: q.asked_at, from: "crew", to: "captain", what: "Asked a question", sub: h.excerpt(q.text || "", 160),
        cost: q.waited_ms == null ? "still waiting" : "waited " + h.fmtMs(q.waited_ms), bad: !q.answered_at });
      if (q.answered_at) steps.push({ at: q.answered_at, from: "captain", to: "crew",
        what: "Answered" + (q.answered_by ? " by " + q.answered_by : ""), sub: h.excerpt(q.answer || "", 160), cost: "–" });
    });

    lines.forEach(function (s) {
      var verb = lineVerb(s);
      var pr = prNumber(s.text || s.line || "");
      if (verb === "pr-open") {
        steps.push({ at: s.at, from: "crew", to: "github", what: "Opened " + (pr ? "PR #" + pr : "a pull request"), sub: s.text || "", href: s.text, cost: "–", handback: j.handback });
        dots.push({ lane: "github", at: s.at, label: pr ? "PR #" + pr : "PR", tone: "good", title: s.line || "" });
      } else if (verb === "pr-merged") {
        steps.push({ at: s.at, from: "github", to: "mate", what: "Merged " + (pr ? "PR #" + pr : "the pull request"), sub: s.text || "", href: s.text, cost: "–" });
        dots.push({ lane: "github", at: s.at, label: "merged", tone: "good", title: s.line || "" });
      } else if (verb === "needs-decision" || verb === "blocked") {
        steps.push({ at: s.at, from: "crew", to: "captain", what: "Needs a decision", sub: s.text || "", cost: "–", bad: true });
      } else if (verb === "wait-mate" || verb === "handback" || verb === "done") {
        steps.push({ at: s.at, from: "crew", to: "mate", what: "Handed back", sub: s.text || "", cost: "–", handback: j.handback });
      } else {
        dots.push({ lane: "crew", at: s.at, label: s.line || verb, tone: "muted", title: s.line || "", small: true });
      }
    });

    execs.forEach(function (e) {
      if (e.is_wrapper || e.poll || !e.started_at) return;
      var failed = e.status === "failed" || (e.exit_code != null && e.exit_code !== 0);
      var ms = e.duration_ms != null ? e.duration_ms : (e.ended_at ? h.timestamp(e.ended_at) - h.timestamp(e.started_at) : null);
      if (ms != null && ms >= MACHINE_MIN_MS) {
        bars.push({ lane: "machine", from: e.started_at, to: e.ended_at || null, label: machineLabel(e.command),
          failed: failed, execution: e, title: h.fmtMs(ms) + (failed ? " · failed" : "") + " · " + h.excerpt(e.command || "", 160) });
      } else if (failed) {
        dots.push({ lane: "crew", at: e.started_at, label: "failed", tone: "critical", title: "failed · " + h.excerpt(e.command || "", 160), small: true });
      }
    });

    if (meta.pr_state === "merged" && !lines.some(function (s) { return lineVerb(s) === "pr-merged"; }) && (ledger.closed_at || meta.stopped_at)) {
      var mergedAt = ledger.closed_at || meta.stopped_at;
      dots.push({ lane: "github", at: mergedAt, label: "merged", tone: "good", title: "merged · " + (meta.pr_sync || "") + (meta.merge_commit ? " · " + meta.merge_commit.slice(0, 7) : "") });
    }
    if (ledger.spawned_at) dots.push({ lane: "mate", at: ledger.spawned_at, label: "spawn", tone: "info", title: "spawned " + ledger.spawned_at });
    if (ledger.closed && ledger.closed_at) {
      var close = ledger.close_state || "closed";
      steps.push({ at: ledger.closed_at, from: "mate", to: null, what: "Closed the crew · " + close.replace(/[_-]/g, " "),
        sub: body.branch && body.branch.name ? "branch " + body.branch.name + (body.branch.exists ? " still exists" : " is gone") : "", cost: "–" });
      dots.push({ lane: "mate", at: ledger.closed_at, label: "close", tone: "info", title: "closed " + ledger.closed_at });
    }

    steps.sort(function (a, b) { return (h.timestamp(a.at) || 0) - (h.timestamp(b.at) || 0); });
    steps.forEach(function (s, i) { s.n = i + 1; });

    var machineMs = 0;
    bars.forEach(function (b) { if (b.lane === "machine" && b.to) machineMs += h.timestamp(b.to) - h.timestamp(b.from); });

    return { t0: t0, t1: t1, t0iso: new Date(t0).toISOString(), t1iso: new Date(t1).toISOString(),
      steps: steps, bars: bars, dots: dots, idle: idle, machineMs: machineMs, journey: j };
  }

  function mateTurnWhat(t, crew) {
    var acts = (t.activities || []).filter(function (a) { return !a.crew || a.crew === crew; });
    var words = acts.map(function (a) {
      if (a.kind === "crew.spawned") return "spawned " + (a.crew || "the crew");
      if (a.kind === "crew.answer") return "answered the crew";
      if (a.kind === "merge.done") return a.text.toLowerCase();
      if (a.kind === "crew.finished") return "closed the crew";
      if (a.kind === "crew.failed") return "closed the crew as failed";
      if (a.kind === "review.started") return "started a review";
      return a.text || a.kind;
    });
    if (!words.length && t.source === "crew") words.push("read the crew's digest");
    if (!words.length && t.source === "captain") words.push("answered the captain");
    var others = (t.activities || []).filter(function (a) { return a.crew && a.crew !== crew && a.kind === "crew.spawned"; });
    if (others.length) words.push("spawned " + others.map(function (a) { return a.crew; }).join(", "));
    if (words.length) return "Mate " + words.join(", ");
    return "Mate turn";
  }

  function mateTurnCost(t) {
    var ms = t.duration_ms != null ? t.duration_ms : (t.ended_at && t.started_at ? h.timestamp(t.ended_at) - h.timestamp(t.started_at) : null);
    return (ms == null ? "?" : h.fmtMs(ms)) + " · " + h.fmtTokens(t.tokens && t.tokens.total) + " · " + h.count(t.model_calls || 0, "call");
  }

  // The words of a prompt without its markup; a task-notification keeps
  // only its status and summary, not the ids and paths the harness wraps
  // them in.
  function plainText(text) {
    return unescapeHTML(String(text || "").replace(/<[^>]*>/g, "")).replace(/\s+/g, " ").trim();
  }

  function notificationText(text) {
    var status = /<status>([\s\S]*?)<\/status>/.exec(text);
    var summary = /<summary>([\s\S]*?)<\/summary>/.exec(text);
    if (!status && !summary) return plainText(text);
    return [status ? plainText(status[1]) : "", summary ? plainText(summary[1]) : ""].filter(Boolean).join(" · ");
  }

  function unescapeHTML(s) {
    return s.replace(/&lt;/g, "<").replace(/&gt;/g, ">").replace(/&quot;/g, "\"").replace(/&#39;/g, "'").replace(/&amp;/g, "&");
  }

  function stageCost(stage) {
    return (stage.elapsed_ms == null ? "?" : h.fmtMs(stage.elapsed_ms)) + " · " +
      (stage.model_calls === 0 ? "?" : h.fmtTokens(stage.tokens && stage.tokens.total)) + " · " + h.count(stage.model_calls || 0, "call");
  }

  // The verb of a status line is the word before the colon in the line as
  // the crew wrote it; the API's own verb is the normalised one (pr-open
  // arrives as wait-mate), and the crew's word is the one a reader knows.
  function lineVerb(s) {
    var line = s.line || "";
    var colon = line.indexOf(":");
    return (colon > 0 ? line.slice(0, colon) : s.verb || "").trim();
  }

  function prNumber(text) {
    var m = /\/pull\/(\d+)|#(\d+)/.exec(text);
    return m ? (m[1] || m[2]) : "";
  }

  // One word for what the machine was doing, from the command.
  function machineLabel(command) {
    var c = command || "";
    if (/pre-merge|gate/.test(c)) return "gate";
    if (/\btest\b|xctest|-only-testing|pytest|go test|npm test|vitest|jest/.test(c)) return "test";
    if (/xcodebuild|\bbuild\b|go build|cargo build|npm run build|make\b/.test(c)) return "build";
    if (/swift format|lint|gofmt|go vet|prettier|eslint/.test(c)) return "lint";
    if (/screenshot|\bshot\b|simctl io|SHOTS/.test(c)) return "shot";
    if (/git (push|fetch|pull|clone)|gh pr/.test(c)) return "git";
    var word = c.replace(/^\s*(cd\s+\S+\s*;\s*|\S+=\S+\s+)*/, "").split(/\s+/)[0] || "command";
    return word.length > 12 ? word.slice(0, 11) + "…" : word;
  }

  // ------------------------------------------------------------ swimlane

  var W = 1000, LEFT = 104, RIGHT = 12, TOP = 18, LANE_H = 44, BAR_H = 14;

  function swimlane(body, j) {
    var height = TOP + LANES.length * LANE_H + 22;
    var svg = svgEl("svg", { class: "journey-svg", viewBox: "0 0 " + W + " " + height, role: "img",
      "aria-label": "Swimlane: " + j.steps.length + " hand-offs across " + LANES.length + " lanes" });
    var defs = svgEl("defs");
    var marker = svgEl("marker", { id: "journey-arrowhead", viewBox: "0 0 8 8", refX: "7", refY: "4", markerWidth: "6", markerHeight: "6", orient: "auto-start-reverse" });
    marker.appendChild(svgEl("path", { d: "M0,0.5 L8,4 L0,7.5 z", class: "journey-arrowhead" }));
    defs.appendChild(marker);
    svg.appendChild(defs);

    var span = Math.max(1, j.t1 - j.t0);
    function x(ts) { var n = typeof ts === "number" ? ts : h.timestamp(ts); return n == null ? null : LEFT + (n - j.t0) / span * (W - LEFT - RIGHT); }
    function laneY(key) { return TOP + LANES.findIndex(function (l) { return l.key === key; }) * LANE_H + LANE_H / 2; }

    // Ticks at a round interval, never more than about ten.
    var tick = tickInterval(span);
    for (var t = Math.ceil(j.t0 / tick) * tick; t <= j.t1; t += tick) {
      svg.appendChild(svgEl("line", { class: "journey-tick", x1: x(t), x2: x(t), y1: TOP, y2: TOP + LANES.length * LANE_H }));
      svg.appendChild(svgEl("text", { class: "journey-tick-label", x: x(t), y: TOP + LANES.length * LANE_H + 14, "text-anchor": "middle" },
        h.clock(new Date(t).toISOString()).slice(0, 5)));
    }

    LANES.forEach(function (lane, i) {
      var y = TOP + i * LANE_H;
      svg.appendChild(svgEl("line", { class: "journey-lane-line", x1: LEFT, x2: W - RIGHT, y1: y + LANE_H, y2: y + LANE_H }));
      svg.appendChild(svgEl("text", { class: "journey-lane-label", x: 8, y: y + LANE_H / 2 - 2, "data-lane": lane.key }, lane.label));
      svg.appendChild(svgEl("text", { class: "journey-lane-sub", x: 8, y: y + LANE_H / 2 + 11 }, lane.sub));
    });

    j.idle.forEach(function (gap) {
      var y = laneY(gap.lane);
      var g = svgEl("g", { class: "journey-idle-group" });
      g.appendChild(svgEl("title", {}, gap.label));
      g.appendChild(svgEl("line", { class: "journey-idle", x1: x(gap.from), x2: x(gap.to), y1: y, y2: y }));
      var mid = (x(gap.from) + x(gap.to)) / 2;
      if (x(gap.to) - x(gap.from) > gap.label.length * 6) g.appendChild(svgEl("text", { class: "journey-idle-label", x: mid, y: y - 10, "text-anchor": "middle" }, gap.label));
      svg.appendChild(g);
    });

    j.bars.forEach(function (b) {
      var x1 = x(b.from), x2 = b.to ? x(b.to) : x(j.t1);
      if (x1 == null) return;
      var w = Math.max(2, x2 - x1);
      var y = laneY(b.lane) - BAR_H / 2;
      var g = svgEl("g", { class: "journey-bar", "data-lane": b.lane, "data-kind": b.kind || null, "data-failed": b.failed ? "true" : null,
        "data-prompt": b.prompt ? b.prompt.id : null, "data-execution": b.execution ? b.execution.id : null, tabindex: b.prompt ? "0" : null,
        role: b.prompt ? "button" : null });
      var title = svgEl("title", {}, b.title);
      g.appendChild(title);
      g.appendChild(svgEl("rect", { x: x1, y: y, width: w, height: BAR_H, rx: 3 }));
      if (w > b.label.length * 6.4 + 8) g.appendChild(svgEl("text", { class: "journey-bar-label", x: x1 + 4, y: y + BAR_H - 3.5 }, b.label));
      if (b.prompt) g.addEventListener("click", function () { tab = "trace"; h.revealPrompt(b.prompt.id); });
      svg.appendChild(g);
    });

    // Two labelled dots close together on one lane (a PR opened and merged
    // in the same second) take turns above and below the line.
    var lastLabel = {};
    j.dots.slice().sort(function (a, b) { return (h.timestamp(a.at) || 0) - (h.timestamp(b.at) || 0); }).forEach(function (d) {
      var cx = x(d.at);
      if (cx == null) return;
      var cy = laneY(d.lane);
      var g = svgEl("g", { class: "journey-dot", "data-lane": d.lane, "data-tone": d.tone || "" });
      g.appendChild(svgEl("title", {}, d.title || d.label));
      g.appendChild(svgEl("circle", { cx: cx, cy: cy, r: d.small ? 3 : 4.5 }));
      if (!d.small) {
        var last = lastLabel[d.lane];
        var below = last && cx - last.x < 40 && !last.below;
        g.appendChild(svgEl("text", { class: "journey-dot-label", x: cx, y: below ? cy + 16 : cy - 8, "text-anchor": "middle" }, d.label));
        lastLabel[d.lane] = { x: cx, below: below };
      }
      svg.appendChild(g);
    });

    j.steps.forEach(function (s) {
      var cx = x(s.at);
      if (cx == null) return;
      var y1 = laneY(s.from), y2 = s.to ? laneY(s.to) : y1;
      var g = svgEl("g", { class: "journey-arrow", "data-step": s.n, "data-bad": s.bad ? "true" : null });
      g.appendChild(svgEl("title", {}, s.n + ". " + s.what));
      if (s.to) {
        var dir = y2 > y1 ? 1 : -1;
        g.appendChild(svgEl("line", { x1: cx, x2: cx, y1: y1 + dir * (BAR_H / 2 + 1), y2: y2 - dir * (BAR_H / 2 + 1), "marker-end": "url(#journey-arrowhead)" }));
      }
      var by = s.to ? (y1 + y2) / 2 : y1 + 14;
      g.appendChild(svgEl("circle", { class: "journey-num-bg", cx: cx, cy: by, r: 7 }));
      g.appendChild(svgEl("text", { class: "journey-num", x: cx, y: by + 3.2, "text-anchor": "middle" }, String(s.n)));
      svg.appendChild(g);
    });
    return svg;
  }

  // svgEl is h.svgNode without the attributes that are null: an absent
  // role or data-kind is no attribute, not the word "null".
  function svgEl(tag, attrs, text) {
    var clean = {};
    Object.keys(attrs || {}).forEach(function (k) { if (attrs[k] != null) clean[k] = attrs[k]; });
    return h.svgNode(tag, clean, text);
  }

  function tickInterval(span) {
    var steps = [30000, 60000, 120000, 300000, 600000, 900000, 1800000, 3600000, 7200000, 14400000, 43200000, 86400000];
    for (var i = 0; i < steps.length; i++) if (span / steps[i] <= 10) return steps[i];
    return steps[steps.length - 1] * Math.ceil(span / steps[steps.length - 1] / 10);
  }

  function laneLegend(j) {
    return h.el("p", { class: "journey-legend muted", text:
      "bar = at work · dot = event · numbered arrow = hand-off (the table below) · dashed = idle, no tokens · click a crew stage to open it in Trace" +
      (j.machineMs ? " · machine bars " + h.fmtMs(j.machineMs) : "") });
  }

  // ---------------------------------------------------------- steps table

  function stepsTable(body, j) {
    if (!j.steps.length) return h.el("p", { class: "empty", text: "no hand-off has been recorded yet" });
    var table = h.el("table", { class: "journey-steps" });
    table.appendChild(h.el("tr", {}, [
      h.el("th", { text: "#" }), h.el("th", { text: "when" }), h.el("th", { text: "from → to" }),
      h.el("th", { text: "what" }), h.el("th", { class: "cost", text: "price" })
    ]));
    j.steps.forEach(function (s) {
      var who = [laneBadge(s.from)];
      if (s.to) { who.push(h.txt(" → ")); who.push(laneBadge(s.to)); }
      var what = [h.el("span", { class: "what", text: s.what })];
      if (s.href) { what.push(h.txt(" · ")); what.push(h.el("a", { href: s.href, target: "_blank", rel: "noopener", text: "on GitHub" })); }
      if (s.sub) what.push(h.el("span", { class: "sub", text: s.sub }));
      if (s.brief) what.push(briefDetails(s.brief));
      if (s.handback) what.push(handbackDetails(s.handback));
      if (s.mate && s.what === "Spawned the crew") what.push(h.el("span", { class: "sub", text: "Mate's turn: " + mateTurnWhat(s.mate, body.crew).replace(/^Mate /, "") + " · " + mateTurnCost(s.mate) }));
      table.appendChild(h.el("tr", { "data-step": s.n, "data-bad": s.bad ? "true" : null }, [
        h.el("td", { class: "n num", text: String(s.n) }),
        h.el("td", { class: "n num", title: s.at || "", text: h.clock(s.at) || "?" }),
        h.el("td", { class: "who" }, who),
        h.el("td", {}, what),
        h.el("td", { class: "cost num", text: s.cost })
      ]));
    });
    return table;
  }

  function briefDetails(b) {
    return h.savedDetails("journey:brief", "The brief · " + h.fmtBytes(b.bytes) + (b.captain_words ? " · captain: " + h.excerpt(b.captain_words.replace(/\s+/g, " "), 80) : ""),
      [b.captain_words ? h.el("pre", { class: "journey-text", text: b.captain_words }) : null,
        h.el("pre", { class: "journey-text", text: b.task || "(no # Task section)" })], "journey-details");
  }

  function handbackDetails(hb) {
    var passed = (hb.criteria || []).filter(function (c) { return c.pass; }).length;
    var label = "The hand-back · " + (hb.criteria && hb.criteria.length ? passed + "/" + hb.criteria.length + " criteria pass" : "no criteria table") +
      ((hb.deviations || []).length ? " · " + h.count(hb.deviations.length, "deviation") : "") +
      ((hb.still_open || []).length ? " · " + h.count(hb.still_open.length, "still open") : "") +
      (hb.learning ? " · learning: " + h.excerpt(hb.learning, 60) : "");
    var children = [];
    if (hb.criteria && hb.criteria.length) {
      var table = h.el("table", { class: "journey-steps criteria" });
      table.appendChild(h.el("tr", {}, [h.el("th", { text: "criterion" }), h.el("th", { text: "result" }), h.el("th", { text: "evidence" })]));
      hb.criteria.forEach(function (c) {
        table.appendChild(h.el("tr", { "data-pass": c.pass ? "true" : "false" }, [
          h.el("td", { class: "step", text: c.name }),
          h.el("td", { class: "st", text: (c.pass ? "✓ " : "✗ ") + c.result }),
          h.el("td", { class: "ev", text: [c.command, c.output].filter(Boolean).join(" → ") })
        ]));
      });
      children.push(table);
    }
    if ((hb.deviations || []).length) children.push(h.el("p", { class: "answer-sub" }, [h.el("b", { text: "Deviations: " }), h.txt(hb.deviations.join(" · "))]));
    if ((hb.still_open || []).length) children.push(h.el("p", { class: "answer-sub" }, [h.el("b", { text: "Still open: " }), h.txt(hb.still_open.join(" · "))]));
    children.push(h.savedDetails("journey:handback-md", "the whole hand-back · " + h.fmtBytes(hb.bytes), [h.el("pre", { class: "journey-text", text: hb.markdown })], "journey-details"));
    return h.savedDetails("journey:handback", label, children, "journey-details");
  }

  function laneBadge(key) {
    return h.el("span", { class: "lane-badge", "data-lane": key, text: LANE_LABEL[key] || key });
  }

  // --------------------------------------------------------- glance cards

  function glanceCards(body, j) {
    var ledger = body.ledger || {};
    var p = body.performance || {};
    var lines = body.status_lines || [];
    var opened = lines.filter(function (s) { return lineVerb(s) === "pr-open"; }).length > 0;
    var merged = lines.filter(function (s) { return lineVerb(s) === "pr-merged"; }).length > 0;
    var failed = (p.executions || []).filter(function (e) {
      return !e.is_wrapper && !e.poll && (e.status === "failed" || (e.exit_code != null && e.exit_code !== 0));
    }).length;
    var skills = Array.isArray(p.skills) ? p.skills.length : null;
    var jj = j.journey || {};
    var meta = jj.meta || {};
    var prState = meta.pr_state || "";
    opened = opened || !!meta.pr_url;
    merged = merged || prState === "merged";
    var reviews = jj.reviews || [];
    var workflow = jj.workflow || null;
    var round = [
      tick(!!ledger.spawned_at, "spawned" + (ledger.spawned_at ? " " + h.clock(ledger.spawned_at).slice(0, 5) : "")),
      tick(opened, opened ? "pull request opened" + (meta.pr_url ? " · " + (prNumber(meta.pr_url) ? "#" + prNumber(meta.pr_url) : "") : "") : "no pull request yet"),
      reviews.length ? line("reviewed: " + reviews.map(function (r) { return r.crew + " · " + (r.verdict || "pending"); }).join(", ")) : null,
      tick(merged, merged ? "merged" + (meta.pr_sync ? " · " + meta.pr_sync : "") : prState ? "PR " + prState : "not merged" + (ledger.closed ? "" : " yet")),
      tick(ledger.closed, ledger.closed ? "closed · " + (ledger.close_state || "closed").replace(/[_-]/g, " ") : "still open"),
      line((ledger.question_count ? "asked the captain " + h.count(ledger.question_count, "time") : "asked the captain nothing") +
        " · handed back " + (ledger.handback_count ? h.count(ledger.handback_count, "time") : "never")),
      line(skills == null ? "skills: not recorded" : skills ? "loaded " + h.count(skills, "skill") : "loaded no skill"),
      workflow ? line("repo workflow: " + workflow.filter(function (c) { return c.state === "done"; }).length + " of " +
        workflow.filter(function (c) { return c.state === "done" || c.state === "missed"; }).length + " steps that apply") : null,
      line(failed ? h.count(failed, "failed command") : "no failed command"),
      meta.teardown ? line("teardown " + meta.teardown + (meta.cleanup ? " · cleanup " + h.excerpt(meta.cleanup, 60) : "")) : null
    ];
    var time = p.time || {};
    var longest = j.bars.filter(function (b) { return b.lane === "machine" && b.to; }).map(function (b) {
      return { ms: h.timestamp(b.to) - h.timestamp(b.from), label: b.label, at: b.from };
    }).sort(function (a, b) { return b.ms - a.ms; }).slice(0, 3);
    return h.el("div", { class: "journey-cards" }, [
      h.el("section", { class: "card glance", "data-glance": "round", "aria-label": "Did the task go round" }, [
        h.el("h3", { text: "Did it go round?" }),
        h.el("ul", { class: "glance-lines" }, round)
      ]),
      h.el("section", { class: "card glance", "data-glance": "tokens", "aria-label": "Tokens" }, [
        h.el("h3", { text: "Who paid" }),
        h.el("div", { class: "big num", text: ledger.turns === 0 ? "–" : h.fmtTokens((ledger.tokens || {}).total) }),
        h.el("p", { class: "answer-sub", text: "the crew, over " + h.count(ledger.turns || 0, "model call") }),
        mateGlance(jj.mate_turns)
      ]),
      h.el("section", { class: "card glance", "data-glance": "machine", "aria-label": "Waiting on the machine" }, [
        h.el("h3", { text: "Waiting on the machine" }),
        h.el("div", { class: "big num", text: time.tool_elapsed_ms == null ? "?"
          : h.fmtMs(time.tool_elapsed_ms) + (time.prompt_elapsed_ms ? " / " + h.fmtMs(time.prompt_elapsed_ms) : "") }),
        h.el("ul", { class: "glance-lines" }, longest.length ? longest.map(function (e) {
          return h.el("li", { text: e.label + " · " + h.fmtMs(e.ms) + " at " + h.clock(e.at) });
        }) : [h.el("li", { class: "muted", text: "no command of 5 s or more" })])
      ])
    ]);
  }

  function mateGlance(turns) {
    if (!turns) return h.el("p", { class: "answer-sub missing", text: "Mate's turns could not be read" });
    if (!turns.length) return h.el("p", { class: "answer-sub", text: "no Mate turn names this crew" });
    var total = 0;
    turns.forEach(function (t) { total += (t.tokens && t.tokens.total) || 0; });
    return h.el("p", { class: "answer-sub", text: "Mate " + h.fmtTokens(total) + " over " + h.count(turns.length, "whole turn") + " about this crew; what else those turns did is counted in, not separated out" });
  }

  function tick(ok, text) {
    return h.el("li", { "data-ok": ok ? "true" : "false" }, [
      h.el("span", { class: "glyph", "aria-hidden": "true", text: ok ? "✓" : "–" }), h.txt(" " + text)
    ]);
  }
  function line(text) { return h.el("li", { text: text }); }

  return { render: render, reset: reset, openTab: openTab, current: current };
}
