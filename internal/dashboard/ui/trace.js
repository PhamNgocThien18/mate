/*
 * trace.js - the Trace tab of the task page (docs/design/task-page.md).
 *
 * Trace answers "what did each step of the agent cost, repeat and get
 * wrong". One spine and three readings of it:
 *
 *   - Stages: the recording's stages in prompt order, each a block as wide
 *     as its tokens and coloured by its kind of work, with skill loads
 *     pinned on it and a failed command marked. A band per prompt carries
 *     the prompt's clock, calls, tokens and time; the gap between two
 *     prompts is drawn as idle. Clicking a block opens it below the strip:
 *     its numbers, then its work segments with their commands and calls.
 *     A recording older than stages draws its segments the same way.
 *   - Worth a look: the projector's findings, with the most expensive
 *     step and the repeats as two findings of their own; three open, the
 *     rest one click away.
 *   - Where the effort went: one table by kind of work, summed from the
 *     blocks so it agrees with the strip by construction; a row filters
 *     the strip to its kind.
 *
 * Everything else - tool mix by prompt, recording details, executions
 * without a prompt, the ledger totals, status lines, questions, raw model
 * calls - is folded under the fold. The header owns the task's totals, so
 * nothing here repeats them; the tab only splits them.
 *
 * Like task.js it is a classic script declaring one global, mateTraceView,
 * which app.js calls once with the helpers the page shares. The state of
 * the tab (which block, finding, segment and prompt are open, which kind
 * filters the strip) lives here and is reset when the page moves to
 * another crew.
 */
function mateTraceView(h) {
  var selectedBlock = "";    // the block open below the strip
  var selectedSegment = "";  // the segment the reader asked for by name
  var selectedFinding = "";  // the finding whose evidence is open
  var selectedPrompt = "";   // the prompt band Journey pointed at
  var kindFilter = "all";    // "all" or one kind of work

  // The effort table's fixed order: real kinds first, then the two the
  // classifier could not name.
  var EFFORT_ORDER = ["research", "write_code", "edit_code", "test", "review", "coordination",
    "wait", "instructions", "response", "mixed", "unknown"];
  var OPEN_FINDINGS = 3;

  function reset() {
    selectedBlock = selectedSegment = selectedFinding = selectedPrompt = "";
    kindFilter = "all";
  }

  // ------------------------------------------------------------ the tab

  function render(body) {
    var p = body.performance;
    var out = h.el("div", { class: "crew-performance" });
    if (!p) {
      out.appendChild(h.el("div", { class: "notice", "data-tone": "quiet" }, [
        h.el("div", { class: "what", text: "Work diagnostics are not available yet" }),
        h.el("div", { class: "why", text: "The observer has not recorded segment and execution evidence for this Crew." })
      ]));
    } else {
      var blocks = blocksOf(body, p);
      out.appendChild(currentWork(body, p));
      out.appendChild(stagesCard(body, p, blocks));
      out.appendChild(findingsPanel(body, p, blocks));
      out.appendChild(effortSection(p, blocks));
    }
    details(body, p).forEach(function (node) { out.appendChild(node); });
    return out;
  }

  // The fold: the readings a diagnosis sometimes needs and a glance never
  // does. Each is a saved <details>, closed until opened.
  function details(body, p) {
    var out = [];
    if (p) {
      out.push(h.savedDetails("tool-mix", "Tool mix by prompt", [toolMix(body, p)], "section diagnostics"));
      var unplaced = (p.executions || []).filter(function (e) { return !e.prompt_id && !e.call_id; });
      if (unplaced.length) out.push(h.savedDetails("unplaced-executions", "Executions without a confirmed prompt (" + unplaced.length + ")", [
        h.el("p", { class: "coverage-note", text: "These records are not assigned to a nearby prompt or segment. Their token cost is unknown." }),
        h.executionList(p, unplaced)
      ], "section diagnostics"));
      out.push(recordingDetails(p));
    }
    var ledger = body.ledger || {};
    out.push(h.savedDetails("ledger", "Crew totals and branch", [h.ledgerHeader(ledger, body.branch || {})], "section diagnostics"));
    var lines = body.status_lines || [];
    if (lines.length) out.push(h.savedDetails("status-lines", "Status lines (" + lines.length + ")", [h.statusList(lines)], "section diagnostics"));
    var questions = body.questions || [];
    out.push(h.savedDetails("questions", "Questions (" + questions.length + ")", [h.questionList(questions)], "section diagnostics"));
    out.push(h.el("div", { class: "section" }, [h.el("h2", { text: "diff" }), h.diffPanel(body)]));
    out.push(h.savedDetails("raw-calls", "Raw model calls and events (" + (body.turns || []).length + ")", [h.turnTimeline(body)], "section diagnostics"));
    return out;
  }

  // A ⓘ beside a heading holds the sentence that used to be a paragraph
  // under it: how the block measures what it shows.
  function info(text) {
    return h.el("span", { class: "info", role: "img", title: text, "aria-label": text, text: "ⓘ" });
  }

  function sectionHead(title, note, aside) {
    return h.el("div", { class: "card-head" }, [h.el("span", { class: "head-info" }, [h.el("h2", { text: title }), info(note)]),
      aside ? h.el("span", { class: "sc-aside", text: aside }) : null]);
  }

  // ------------------------------------------------------ current work

  // What the crew is doing now, or did last. The totals are the header's;
  // this strip says only what the latest segment is and, for an open task,
  // how fresh the recording is.
  function currentWork(body, p) {
    var current = h.byID(p.segments, p.current_segment_id);
    var latest = current || (p.segments || [])[Math.max(0, (p.segments || []).length - 1)];
    var freshness = p.freshness || {};
    var closed = body.ledger && body.ledger.closed;
    var stale = !closed && (freshness.stale || (h.timestamp(freshness.last_observed_at) != null && Date.now() - h.timestamp(freshness.last_observed_at) > 30000));
    var card = h.el("section", { class: "card work-current", "aria-label": "Current Crew work" });
    card.appendChild(h.el("div", { class: "card-head" }, [
      h.el("h2", { text: current ? "Current work" : "Last recorded work" }),
      h.el("span", { class: "recording-state", "data-observed-at": closed ? null : freshness.last_observed_at || "",
        "data-recording-error": freshness.stale ? "true" : "false", "data-stale": stale ? "true" : "false",
        text: closed ? "Historical recording" : !freshness.last_observed_at ? "Recording time unknown" : stale ? "Recording is stale" : "Recorded recently" })
    ]));
    card.appendChild(h.el("div", { class: "work-current-title" }, [
      latest ? h.el("button", { class: "text-button", type: "button", "data-focus": "current-work", text: h.segmentLabel(latest, p),
        onclick: function () { revealSegments([latest.id]); } }) : h.txt("No work segment recorded")
    ]));
    if (latest) card.appendChild(h.el("div", { class: "facts" }, [
      h.el("span", { title: latest.started_at || "", text: "since " + (h.clock(latest.started_at) || "unknown") }),
      h.el("span", {}, [h.txt(current ? "elapsed " : "recorded span "), h.elapsedNode(latest.started_at, latest.ended_at, latest.elapsed_ms, !!current)]),
      h.el("span", { text: latest.model_calls === 0 ? "Usage not reported for this activity yet" : latest.model_calls + " model calls · " + h.fmtTokens(latest.tokens && latest.tokens.total) + " tokens" })
    ]));
    var running = closed ? [] : (p.executions || []).filter(function (e) { return !e.is_wrapper && (e.status === "running" || e.status === "in_progress"); });
    running.slice(-2).forEach(function (e) {
      var process = h.processForExecution(p, e);
      card.appendChild(h.el("div", { class: "running-execution" }, [h.el("span", { text: "Last seen running: " }),
        h.el("code", { text: h.excerpt((e.poll && process && process.command) || e.command || e.target || e.tool || "Unknown command", 150) }),
        h.txt(" · "), h.elapsedNode(e.started_at, e.ended_at, e.duration_ms, true),
        e.process_id ? h.el("span", { text: " · process " + e.process_id }) : null]));
    });
    // A closed task's recording ages for ever; saying how long ago the
    // observer last looked would be noise.
    if (!closed) card.appendChild(h.el("div", { class: "freshness-line" }, [
      h.el("span", {}, [h.txt("Observer "), h.ageNode(freshness.last_observed_at)]),
      h.el("span", {}, [h.txt("Ingest "), h.ageNode(freshness.last_ingested_at)]),
      h.el("span", {}, [h.txt("Usage "), h.ageNode(freshness.last_usage_at)])
    ]));
    var missing = freshness.missing || [];
    if (missing.length) card.appendChild(h.el("p", { class: "coverage-note", text: "Coverage: " + missing.slice(0, 2).join(" · ") + (missing.length > 2 ? " · " + (missing.length - 2) + " more in recording details" : "") }));
    return card;
  }

  // ------------------------------------------------------------ blocks

  // A block is one piece of the strip: a stage when the projector recorded
  // stages (a run of consecutive calls doing one kind of work), else one
  // work segment. Both partition the calls, so their numbers add up to the
  // prompt and the task. Blocks are numbered across the task in prompt
  // order; a block knows its segments, its calls, the skills loaded in it
  // and whether a native command in it failed.
  function blocksOf(body, p) {
    var prompts = p.prompt_turns || [];
    var recorded = prompts.filter(function (prompt) { return prompt.overview && Array.isArray(prompt.overview.steps); });
    var blocks = [];
    if (recorded.length) {
      recorded.forEach(function (prompt) {
        prompt.overview.steps.forEach(function (s) {
          blocks.push(block("stage-" + (blocks.length + 1), s.kind, s.parts, s.tokens, s.model_calls, s.executions, s.elapsed_ms, s.started_at, s.ended_at,
            s.open, s.segment_ids || [], s.call_ids || [], prompt, "stage"));
        });
      });
    } else {
      (p.segments || []).forEach(function (s) {
        var prompt = h.byID(prompts, s.prompt_id) || null;
        var tools = (s.execution_ids || []).map(function (id) { return h.byID(p.executions, id); })
          .filter(function (e) { return e && !e.is_wrapper && !e.poll; }).length;
        blocks.push(block(s.id, s.kind, [s.kind], s.model_calls === 0 ? null : s.tokens, s.model_calls, tools, s.elapsed_ms, s.started_at, s.ended_at,
          s.id === p.current_segment_id, [s.id], s.call_ids || [], prompt, "segment"));
      });
    }
    blocks.forEach(function (b, i) { b.n = i + 1; });
    var byCall = {};
    blocks.forEach(function (b) { b.callIDs.forEach(function (id) { byCall[id] = b; }); });
    var skills = Array.isArray(p.skills) ? p.skills : [];
    skills.forEach(function (skill, i) {
      (skill.loads || []).forEach(function (load) {
        var b = load.call_id ? byCall[load.call_id] : null;
        if (b && !b.pins.some(function (pin) { return pin.index === i + 1; })) b.pins.push({ index: i + 1, name: skill.name });
      });
    });
    blocks.forEach(function (b) {
      b.failed = b.segmentIDs.some(function (id) {
        var s = h.byID(p.segments, id);
        return s && (s.execution_ids || []).some(function (eid) { var e = h.byID(p.executions, eid); return e && h.isFailedNative(e); });
      });
    });
    return { blocks: blocks, byCall: byCall, source: recorded.length ? "stage" : "segment", prompts: prompts, recorded: recorded.length > 0 };
  }

  function block(id, kind, parts, tokens, calls, tools, elapsed, started, ended, open, segmentIDs, callIDs, prompt, source) {
    return { id: id, n: 0, kind: kind || "unknown", parts: parts || [], tokens: tokens || null, calls: calls || 0, tools: tools,
      elapsed: elapsed, started_at: started, ended_at: ended, open: !!open, segmentIDs: segmentIDs, callIDs: callIDs, prompt: prompt,
      source: source, pins: [], failed: false };
  }

  function blockTokens(b) { return Number(b.tokens && b.tokens.total) || 0; }
  function blockCache(b) { return Number(b.tokens && b.tokens.cache_read) || 0; }
  function blockOf(blocks, id) { return blocks.find(function (b) { return b.id === id; }); }
  function blockWithSegment(blocks, segmentID) { return blocks.find(function (b) { return b.segmentIDs.indexOf(segmentID) !== -1; }); }

  // A count the recording always has: zero is a dash, never "0".
  function dashed(n) { return n == null ? "?" : n ? String(n) : "–"; }
  function dashedTokens(n) { return n ? h.fmtTokens(n) : "–"; }
  function unknownable(text) {
    return text === "?" ? h.el("span", { class: "sc-unknown", title: "not recorded", text: "?" }) : h.txt(text);
  }
  function stageTime(ms) { return ms == null ? "?" : ms === 0 ? "–" : h.fmtMs(ms); }

  // A skill pin is the skill's 1-based place in the skills list, so the
  // same skill loaded in two blocks shows the same number twice.
  function skillPin(index, name) {
    return h.el("span", { class: "sc-pin", "data-pin": String(index), title: "skill " + index + ": " + name + " loaded here",
      "aria-label": "skill " + index + " " + name, text: String(index) });
  }

  function kindSwatch(kind) {
    return h.el("i", { class: "sc-swatch", "data-kind": kind || "unknown", "aria-hidden": "true" });
  }

  // ------------------------------------------------------------ stages

  function stagesCard(body, p, model) {
    var blocks = model.blocks;
    var open = blocks.filter(function (b) { return b.open; });
    var what = model.source === "stage" ? "stage" : "segment";
    var aside = !blocks.length ? (model.prompts.length ? "stages not recorded yet" : "no prompt recorded yet")
      : h.count(blocks.length, what) + (model.source === "segment" ? " · stages not recorded" : "") + (open.length ? " · #" + open[open.length - 1].n + " in progress" : "");
    var card = h.el("section", { class: "card stages-card", "aria-label": "Stages of this task" });
    card.appendChild(sectionHead("Stages", "One block per " + what + ", in prompt order, as wide as its tokens and coloured by its kind of work. " +
      "A numbered pin is a skill loaded in it; a red edge is a failed native command. Click a block to open its segments and evidence.", aside));
    if (!blocks.length) {
      card.appendChild(h.el("p", { class: "coverage-note", text: model.prompts.length ? "Steps not recorded yet" : "No prompt has been recorded yet." }));
    } else {
      card.appendChild(strip(p, model));
      card.appendChild(legend(blocks));
      var selected = blockOf(blocks, selectedBlock);
      if (selected) card.appendChild(drill(body, p, selected));
    }
    card.appendChild(skillsBand(p, model));
    return card;
  }

  // The strip: a band per prompt, blocks side by side inside it, idle
  // between two prompts when the gap is at least five seconds.
  function strip(p, model) {
    var wrap = h.el("div", { class: "tl", role: "list", "aria-label": h.count(model.blocks.length, "block") + " in prompt order, sized by tokens" });
    var total = model.blocks.reduce(function (sum, b) { return sum + blockTokens(b); }, 0);
    var pinned = model.blocks.some(function (b) { return b.pins.length; });
    if (pinned) wrap.setAttribute("data-pins", "true");
    var bands = [];
    model.blocks.forEach(function (b) {
      var key = b.prompt ? b.prompt.id : "";
      var band = bands.length && bands[bands.length - 1].key === key ? bands[bands.length - 1] : null;
      if (!band) { band = { key: key, prompt: b.prompt, blocks: [] }; bands.push(band); }
      band.blocks.push(b);
    });
    bands.forEach(function (band, i) {
      if (i > 0) {
        var previous = bands[i - 1].prompt, next = band.prompt;
        var gap = previous && next ? (h.timestamp(next.prompt_at || next.started_at) || 0) - (h.timestamp(previous.ended_at) || 0) : 0;
        if (gap >= 5000) wrap.appendChild(h.el("div", { class: "tl-idle", role: "listitem", title: "no model call between the two prompts for " + h.fmtMs(gap), text: "idle " + h.fmtMs(gap) }));
      }
      wrap.appendChild(promptBand(p, model, band, total));
    });
    return wrap;
  }

  function promptBand(p, model, band, total) {
    var prompt = band.prompt;
    var index = prompt ? model.prompts.indexOf(prompt) : -1;
    var node = h.el("div", { class: "tl-prompt", role: "listitem", "data-prompt": prompt ? prompt.id : "", "data-selected": prompt && selectedPrompt === prompt.id ? "true" : "false" });
    if (prompt) {
      var clock = h.clock(prompt.prompt_at || prompt.started_at);
      var tag = "P" + (index + 1) + " " + (clock ? clock.slice(0, 5) : "time unknown");
      var facts = tag + " · " + h.count(prompt.model_calls || 0, "call") + " · " +
        (prompt.model_calls === 0 ? "usage pending" : h.fmtTokens(prompt.tokens && prompt.tokens.total)) + " · " + h.fmtMs(prompt.elapsed_ms);
      node.appendChild(h.el("div", { class: "tl-prompt-head" }, [
        h.el("button", { class: "text-button tl-prompt-tag mono", type: "button", "data-focus": "prompt:" + prompt.id, title: "Mark this prompt's band",
          text: facts, onclick: function () { selectedPrompt = selectedPrompt === prompt.id ? "" : prompt.id; h.draw(); } }),
        h.el("span", { class: "tl-prompt-text", title: prompt.prompt || "", text: h.excerpt(prompt.prompt || "Prompt text unavailable", 90) })
      ]));
    } else node.appendChild(h.el("div", { class: "tl-prompt-head" }, [h.el("span", { class: "tl-prompt-tag mono", text: "P? · prompt not identified" })]));
    node.appendChild(h.el("div", { class: "tl-strip" }, band.blocks.map(function (b) { return blockButton(p, model, b, total); })));
    return node;
  }

  function blockButton(p, model, b, total) {
    var t = blockTokens(b);
    var dim = kindFilter !== "all" && b.kind !== kindFilter;
    var title = "#" + b.n + " " + h.kindLabel(b.kind) + (b.kind === "mixed" ? " (" + mixedParts(b).map(h.kindLabel).join(" + ") + ")" : "") +
      " · " + (b.tokens ? h.fmtTokens(t) + " tokens" : "usage pending") + " · " + h.count(b.calls, "call") + " · " + stageTime(b.elapsed) +
      (b.started_at ? " · " + h.promptTag(p, b.prompt && b.prompt.id, b.started_at, true) : "") +
      (b.failed ? " · a native command failed" : "") + (b.open ? " · in progress" : "");
    return h.el("button", { class: "tl-stage", type: "button", role: "listitem", "data-stage": String(b.n), "data-block": b.id, "data-kind": b.kind,
      "data-open": b.open ? "true" : null, "data-failed": b.failed ? "true" : null, "data-dim": dim ? "true" : null,
      "data-focus": "stage:" + b.id, "aria-pressed": selectedBlock === b.id ? "true" : "false", title: title, "aria-label": title,
      style: "flex-grow:" + (total > 0 ? Math.max(t, 1) : 1),
      onclick: function () { openBlock(b.id, b.segmentIDs); } }, [
      h.el("span", { class: "tl-pins", "aria-hidden": "true" }, b.pins.map(function (pin) { return skillPin(pin.index, pin.name); })),
      h.el("span", { class: "tl-fill" }, [h.el("span", { class: "tl-n", text: String(b.n) })])
    ]);
  }

  function mixedParts(b) { return (b.parts || []).filter(function (k) { return k && k !== "mixed" && k !== b.kind; }); }

  function legend(blocks) {
    var kinds = Object.keys(h.KINDS).filter(function (kind) { return blocks.some(function (b) { return b.kind === kind; }); });
    blocks.forEach(function (b) { if (!h.KINDS[b.kind] && kinds.indexOf(b.kind) === -1) kinds.push(b.kind); });
    var items = kinds.map(function (kind) {
      return h.el("span", { class: "item" }, [h.el("span", { class: "swatch", "data-kind": kind, "aria-hidden": "true" }), h.txt(h.kindLabel(kind))]);
    });
    if (blocks.some(function (b) { return b.failed; })) items.push(h.el("span", { class: "item" }, [h.el("span", { class: "swatch", "data-failed": "true", "aria-hidden": "true" }), h.txt("failed command")]));
    if (blocks.some(function (b) { return b.pins.length; })) items.push(h.el("span", { class: "item" }, [h.el("span", { class: "sc-pin", "aria-hidden": "true", text: "1" }), h.txt("skill load")]));
    if (blocks.some(function (b) { return b.open; })) items.push(h.el("span", { class: "item" }, [h.el("span", { class: "sc-open", text: "in progress" })]));
    return h.el("div", { class: "legend tl-legend", "aria-label": "Kinds of work on the strip" }, items);
  }

  // The block opened below the strip: its numbers, then its segments with
  // their evidence. Opening a block opens its segments; a segment's
  // commands and calls are built when its row is opened.
  function drill(body, p, b) {
    var box = h.el("section", { class: "tl-drill", "data-drill": b.id, tabindex: "-1", "aria-label": "Block " + b.n });
    var parts = mixedParts(b);
    box.appendChild(h.el("div", { class: "card-head" }, [
      h.el("h3", { class: "tl-drill-title" }, [h.txt("#" + b.n), kindSwatch(b.kind), h.txt(h.kindLabel(b.kind)),
        parts.length ? h.el("span", { class: "sc-parts", text: parts.map(h.kindLabel).join(" + ") }) : null,
        b.open ? h.el("span", { class: "sc-open", text: "in progress" }) : null]),
      h.el("span", { class: "segment-clock mono", title: b.started_at || "time unknown", text: h.promptTag(p, b.prompt && b.prompt.id, b.started_at, true) }),
      h.el("button", { class: "chip", type: "button", text: "Close", "data-focus": "close-stage", onclick: function () { selectedBlock = ""; h.draw(); } })
    ]));
    var pending = b.calls === 0 || !b.tokens;
    box.appendChild(h.el("div", { class: "segment-numbers" }, [
      number("Calls", dashed(b.calls)), number("Tool calls", b.tools == null ? "?" : dashed(b.tools)),
      number("Tokens", pending ? "?" : h.fmtTokens(b.tokens.total)), number("Cache read", pending ? "?" : h.fmtTokens(b.tokens.cache_read)),
      number("Work", pending ? "?" : h.fmtTokens((Number(b.tokens.total) || 0) - (Number(b.tokens.cache_read) || 0))),
      number("Output", pending ? "?" : h.fmtTokens(b.tokens.output)), number("Thinking", pending ? "?" : h.fmtTokens(b.tokens.thinking)),
      h.el("span", { class: "segment-number" }, [h.el("span", { text: "Time" }), b.open && b.source === "segment"
        ? h.elapsedNode(b.started_at, b.ended_at, b.elapsed, true) : h.el("strong", { class: "num" }, [unknownable(stageTime(b.elapsed))])])
    ]));
    if (b.pins.length) box.appendChild(h.el("p", { class: "sc-note" }, [h.txt("Skills loaded here: ")].concat(b.pins.map(function (pin) {
      return h.el("span", { class: "sc-load" }, [skillPin(pin.index, pin.name), h.txt(pin.name)]);
    }))));
    var segments = b.segmentIDs.map(function (id) { return h.byID(p.segments, id); }).filter(Boolean);
    if (segments.length) {
      if (b.source === "stage") box.appendChild(h.el("h3", { class: "tl-drill-sub", text: h.count(segments.length, "work segment") }));
      segments.forEach(function (s) { box.appendChild(segmentRow(body, p, s)); });
    } else {
      var calls = b.callIDs.map(function (id) { return h.byID(body.turns, id); }).filter(Boolean);
      if (calls.length) box.appendChild(h.savedDetails("block-calls:" + b.id, "Model calls in this block (" + calls.length + ")",
        [h.el("ol", { class: "turns" }, calls.map(function (call) { return h.turnRow(body, call); }))], "segment-calls", true));
      else box.appendChild(h.el("p", { class: "coverage-note", text: "No segment or call is linked to this block." }));
    }
    return box;
  }

  function number(label, value) {
    return h.el("span", { class: "segment-number" }, [h.el("span", { text: label }), h.el("strong", { class: "num" }, [unknownable(value)])]);
  }

  // skillsBand lists every skill the recording shows being loaded, how,
  // and the block each load happened in. A load is all a transcript can
  // show.
  function skillsBand(p, model) {
    var skills = Array.isArray(p.skills) ? p.skills : null;
    var loads = 0;
    (skills || []).forEach(function (skill) { loads += (skill.loads || []).length; });
    var band = h.el("div", { class: "sc-band", "data-band": "skills", "data-skills": "true" }, [
      h.el("div", { class: "sc-band-head" }, [h.el("h3", { class: "sc-label", text: "Skills" }),
        skills && skills.length ? h.el("span", { class: "sc-aside", text: h.count(skills.length, "skill") + " · " + h.count(loads, "load") }) : null])
    ]);
    if (!skills) {
      band.appendChild(h.el("p", { class: "coverage-note", text: "Skills not recorded yet" }));
      return band;
    }
    if (!skills.length) {
      band.appendChild(h.el("p", { class: "coverage-note", text: "No skill was loaded in this recording." }));
      return band;
    }
    band.appendChild(h.el("ul", { class: "sc-skills" }, skills.map(function (skill, i) {
      var via = [];
      var chips = (skill.loads || []).map(function (load) {
        var how = load.via === "tool" ? "skill tool" : "read SKILL.md";
        if (via.indexOf(how) === -1) via.push(how);
        var b = load.call_id ? model.byCall[load.call_id] : null;
        if (!b) return h.el("span", { class: "sc-load", "data-linked": "false", title: load.at ? "Loaded " + h.day(load.at) : null,
          text: model.recorded ? "stage not linked" : "stage not recorded" });
        return h.el("button", { class: "sc-load", type: "button", "data-stage": String(b.n), "data-focus": "skill-load:" + skill.name + ":" + b.n,
          title: load.at ? "Loaded " + h.day(load.at) : null, onclick: function () { openBlock(b.id, []); } }, [
          h.el("span", { class: "sc-load-n", text: "#" + b.n }), kindSwatch(b.kind), h.txt(h.kindLabel(b.kind))
        ]);
      });
      return h.el("li", { "data-skill": skill.name }, [
        h.el("div", { class: "sc-skill-head" }, [skillPin(i + 1, skill.name), h.el("strong", { class: "sc-skill-name", text: skill.name }),
          h.el("span", { class: "sc-skill-count", text: "×" + (skill.count || (skill.loads || []).length) })]),
        h.el("div", { class: "sc-skill-where" }, [h.el("span", { text: (via.join(" and ") || "load not recorded") + " · loaded in" })].concat(chips))
      ]);
    })));
    band.appendChild(h.el("p", { class: "sc-note", text: "A load is the harness's skill tool being called or a SKILL.md being read. It does not show that the skill was followed." }));
    return band;
  }

  // ---------------------------------------------------------- segments

  function segmentTokens(s, bucket) {
    return s.model_calls === 0 ? "?" : h.fmtTokens(s.tokens && s.tokens[bucket]);
  }

  // segmentToolCalls sums the ledger's tool count over the segment's
  // calls. A call the page does not hold makes the sum unknown, not smaller.
  function segmentToolCalls(body, s) {
    if (s.model_calls === 0) return "?";
    var ids = s.call_ids || [];
    var sum = 0;
    for (var i = 0; i < ids.length; i++) {
      var call = h.byID(body.turns, ids[i]);
      if (!call || call.tool_count == null) return "?";
      sum += Number(call.tool_count) || 0;
    }
    return String(sum);
  }

  function segmentRow(body, p, s) {
    var f = h.byID(p.findings, selectedFinding);
    var related = f && (f.segment_ids || []).indexOf(s.id) !== -1;
    var open = h.detailOpen("segment:" + s.id);
    var details = h.savedDetails("segment:" + s.id, "", [], "work-segment", false);
    details.setAttribute("data-segment", s.id);
    details.setAttribute("data-selected", selectedSegment === s.id || related ? "true" : "false");
    details.setAttribute("tabindex", "-1");
    var summary = details.querySelector("summary");
    h.append(summary, [h.el("div", { class: "segment-heading" }, [
      h.el("span", { class: "segment-label", text: h.segmentLabel(s, p) }),
      h.kindChip(s.kind),
      h.el("span", { class: "segment-clock mono", title: s.started_at || "time unknown", text: h.promptTag(p, s.prompt_id, s.started_at, true) }),
      h.el("span", { class: "badge", text: s.outcome || "unknown" })]),
      h.el("div", { class: "segment-numbers" }, [
        segmentNumber("Fresh input", segmentTokens(s, "input")), segmentNumber("Cache read", segmentTokens(s, "cache_read")),
        segmentNumber("Cache write", segmentTokens(s, "cache_write")), segmentNumber("Output", segmentTokens(s, "output")),
        segmentNumber("Thinking", segmentTokens(s, "thinking")), segmentNumber("Total", segmentTokens(s, "total")),
        h.el("span", { class: "segment-number" }, [h.el("span", { text: "Elapsed" }), h.elapsedNode(s.started_at, s.ended_at, s.elapsed_ms, s.id === p.current_segment_id)]),
        segmentNumber("Calls", String(s.model_calls || 0)), segmentNumber("Tool calls", segmentToolCalls(body, s)),
        segmentNumber("Repeats", s.repeat_count == null ? "?" : String(s.repeat_count))
      ])]);
    // Build expensive execution and call detail only while it is open.
    if (open) details.appendChild(segmentEvidence(body, p, s, f));
    details.addEventListener("toggle", function () {
      if (details.isConnected && details.open && !details.querySelector(".segment-evidence")) details.appendChild(segmentEvidence(body, p, s, f));
    });
    return details;
  }

  function segmentNumber(label, value) {
    return h.el("span", { class: "segment-number" }, [h.el("span", { text: label }), h.el("strong", { class: "num", text: value })]);
  }

  function segmentEvidence(body, p, s, f) {
    var box = h.el("div", { class: "segment-evidence" });
    if (s.model_calls === 0) box.appendChild(h.el("p", { class: "coverage-note", text: "Usage not reported for this activity yet. The native execution is visible before a model response reports tokens." }));
    var prompt = h.byID(p.prompt_turns, s.prompt_id);
    box.appendChild(h.el("div", { class: "facts" }, [
      h.el("span", { text: "Tool elapsed " + h.fmtMs(s.tool_elapsed_ms) }), h.el("span", { text: "Summed invocation time " + h.fmtMs(s.invocation_ms) }),
      h.el("span", { text: "Classification: " + (s.rule || "unknown") })
    ]));
    if (prompt) box.appendChild(h.savedDetails("segment-prompt:" + s.id, "Prompt that started this work", [
      h.el("p", { class: "prompt-text", text: prompt.prompt || "Prompt text unavailable" }), h.sourceNote(prompt.source_ref)
    ], "evidence-prompt"));
    var executions = (s.execution_ids || []).map(function (id) { return h.byID(p.executions, id); }).filter(Boolean);
    box.appendChild(h.el("h3", { text: "Commands and tool evidence" }));
    box.appendChild(executions.length ? h.executionList(p, executions, f && f.execution_ids) :
      h.el("p", { class: "empty", text: "No execution is linked to these model calls. Inspect the call records for available tool evidence." }));
    var calls = (s.call_ids || []).map(function (id) { return h.byID(body.turns, id); }).filter(Boolean);
    var callList = h.el("ol", { class: "turns" });
    calls.forEach(function (call) {
      var row = h.turnRow(body, call);
      if (f && (f.call_ids || []).indexOf(call.id) !== -1) row.setAttribute("data-highlight", "true");
      callList.appendChild(row);
    });
    box.appendChild(h.savedDetails("segment-calls:" + s.id, "Model calls in this segment (" + calls.length + ")", [callList], "segment-calls"));
    return box;
  }

  // ---------------------------------------------------------- findings

  // The projector's findings plus two the page derives: the most expensive
  // step and the repeats. The most expensive step and the projector's top
  // findings are open; everything else is one click away.
  function findingsPanel(body, p, model) {
    var findings = p.findings || [];
    var top = (p.top_finding_ids || []).map(function (id) { return h.byID(findings, id); }).filter(Boolean);
    if (!top.length) top = findings.slice(0, OPEN_FINDINGS);
    var rest = findings.filter(function (f) { return top.indexOf(f) === -1; });
    var ordered = [expensiveFinding(body, p)].concat(top, [repeatsFinding(body, p)], rest);
    var open = ordered.slice(0, OPEN_FINDINGS), folded = ordered.slice(OPEN_FINDINGS);
    var section = h.el("section", { class: "section findings", "aria-label": "Findings to inspect" }, [
      sectionHead("Worth a look", "Patterns that crossed a detector threshold, the step that cost the most and what the crew repeated. " +
        "Findings can share the same calls, so their tokens are not additive; repetition alone does not establish waste.",
        findings.length ? h.count(findings.length, "finding") + " recorded" : "no pattern crossed a threshold")
    ]);
    section.appendChild(h.el("div", { class: "finding-list" }, open.map(function (f) { return findingCard(body, p, f); })));
    if (folded.length) section.appendChild(h.savedDetails("more-findings", "Show " + folded.length + " more findings",
      folded.map(function (f) { return findingCard(body, p, f); }), "more-findings"));
    var selected = ordered.find(function (f) { return f.id === selectedFinding; });
    if (selected) section.appendChild(selected.synthetic ? derivedEvidence(body, p, selected) : findingEvidence(body, p, selected));
    return section;
  }

  function findingCard(body, p, f) {
    return h.el("button", { class: "finding-card", type: "button", "aria-pressed": selectedFinding === f.id ? "true" : "false",
      "data-finding": f.id, "data-focus": "finding:" + f.id, onclick: function () {
        selectedFinding = f.id;
        revealSegments(f.segment_ids || [], true);
      } }, [
      h.findingKindChip(f),
      h.el("span", { class: "finding-title", text: f.title || f.kind }),
      h.el("span", { class: "finding-detail", text: f.detail || "" }),
      f.synthetic ? null : h.el("span", { class: "finding-usage", text: h.usageText(f.tokens) }),
      h.el("span", { class: "finding-footer", text: f.synthetic ? "Derived from the recording · Inspect →"
        : (f.ongoing ? "Ongoing" : "Recorded") + " · " + (f.confidence || "unknown") + " confidence · Inspect evidence →" })
    ]);
  }

  // The most expensive step is told twice, because the two rankings answer
  // different questions: output + thinking is what the model wrote, total
  // tokens is the context it was sent again and again.
  function expensiveFinding(body, p) {
    var f = { id: "expensive", kind: "cost", severity: "info", synthetic: true, title: "Most expensive step", detail: "", segment_ids: [], call_ids: [], lines: [] };
    var callIDs = p.top_output_call_ids, callID = (callIDs || [])[0];
    var call = callID ? h.byID(body.turns, callID) : null;
    var callSegment = callID ? (p.segments || []).find(function (s) { return (s.call_ids || []).indexOf(callID) !== -1; }) : null;
    var outputSegment = h.byID(p.segments, (p.top_output_segment_ids || [])[0]);
    var contextSegment = h.byID(p.segments, (p.top_segment_ids || [])[0]);
    if (!callIDs && !p.top_output_segment_ids && !p.top_segment_ids) {
      f.detail = "not ranked yet";
      f.lines.push({ text: "This recording ranks neither calls nor segments yet." });
      return f;
    }
    if (call) {
      f.title = "Most expensive step: call #" + call.ordinal + " · " + h.fmtTokens(call.tokens && call.tokens.output) + " output + " + h.fmtTokens(call.tokens && call.tokens.thinking) + " thinking";
      f.call_ids = [call.id];
    } else if (contextSegment) {
      f.title = "Most expensive step: " + h.segmentLabel(contextSegment, p) + " · " + h.fmtTokens(contextSegment.tokens && contextSegment.tokens.total) + " re-sent";
    }
    f.detail = "by output + thinking: " + (!callIDs ? "calls not ranked yet" : call ? "call #" + call.ordinal + " · " + h.fmtMs(call.duration_ms) + " · context after " + h.fmtKnown(call.context_tokens_after) : callID ? "call not in this page's ledger" : "no model call recorded") +
      " · by context re-sent: " + (!p.top_segment_ids ? "not ranked yet" : contextSegment ? h.segmentLabel(contextSegment, p) + " · " + (contextSegment.model_calls === 0 ? "usage pending" : h.fmtTokens(contextSegment.tokens && contextSegment.tokens.total)) : "no segment recorded");
    [callSegment, outputSegment, contextSegment].forEach(function (s) { if (s && f.segment_ids.indexOf(s.id) === -1) f.segment_ids.push(s.id); });
    f.lines.push({ label: "By output + thinking", text: !callIDs ? "calls not ranked yet" : call
      ? "call #" + call.ordinal + " · " + h.fmtTokens(call.tokens && call.tokens.output) + " output + " + h.fmtTokens(call.tokens && call.tokens.thinking) + " thinking · " +
        h.fmtMs(call.duration_ms) + " · context after " + h.fmtKnown(call.context_tokens_after) + (callSegment ? " · " + h.promptTag(p, callSegment.prompt_id, call.started_at) : "")
      : callID ? "call not in this page's ledger" : "no model call recorded", segment: callSegment, focus: "answer-top-call" });
    f.lines.push({ label: "Segment with most output", text: !p.top_output_segment_ids ? "segments not ranked yet" : outputSegment
      ? (outputSegment.model_calls === 0 ? "usage pending" : h.fmtTokens((Number(outputSegment.tokens && outputSegment.tokens.output) || 0) + (Number(outputSegment.tokens && outputSegment.tokens.thinking) || 0)) + " output + thinking") +
        " · " + h.count(outputSegment.model_calls || 0, "call") + " · " + h.fmtMs(outputSegment.elapsed_ms)
      : "no segment recorded", segment: outputSegment, focus: "answer-top-output-segment" });
    f.lines.push({ label: "By context re-sent", text: !p.top_segment_ids ? "not ranked yet" : contextSegment
      ? (contextSegment.model_calls === 0 ? "usage pending" : h.fmtTokens(contextSegment.tokens && contextSegment.tokens.total) + " tokens") + " · " +
        h.count(contextSegment.model_calls || 0, "call") + " · " + h.count(contextSegment.repeat_count, "repeat") + " · " + h.fmtMs(contextSegment.elapsed_ms)
      : "no segment recorded", segment: contextSegment, focus: "answer-top-context-segment" });
    return f;
  }

  function processForFinding(p, f) {
    var ids = f.execution_ids || [];
    return (p.processes || []).find(function (item) {
      return ids.some(function (id) {
        return (item.poll_ids || []).indexOf(id) !== -1 || (item.execution_ids || []).indexOf(id) !== -1;
      });
    });
  }

  function repeatsFinding(body, p) {
    var loops = p.loops;
    var f = { id: "repeats", kind: "repeat", severity: "info", synthetic: true, title: "Repeats", detail: "", segment_ids: [], call_ids: [], lines: [] };
    if (!loops) {
      f.detail = "Repeats not recorded yet";
      f.lines.push({ text: "This recording has no loop counters." });
      return f;
    }
    var counters = "Same content re-read: " + h.numText(loops.repeated_reads) + " · same-error retries: " + h.numText(loops.repeated_errors) +
      " · repair loops: " + h.numText(loops.repair_loops) + " · repeats inside segments: " + h.numText(loops.segment_repeats);
    if (!loops.measured) {
      // A harness without native process identities cannot have its polling
      // counted; printing 0 chains would claim it never polled.
      f.title = "Repeats: polling not measurable";
      f.detail = counters;
      f.lines.push({ text: loops.coverage || "Polling chains need native process identities, which this harness does not report; repeated reads and retries are still detected." });
      f.lines.push({ text: counters });
      return f;
    }
    var polls = Number(loops.polls) || 0;
    var total = Number(p.tokens && p.tokens.total) || 0;
    f.title = "Repeats: " + h.count(loops.chains, "chain") + " · " + h.count(loops.polls, "poll");
    f.detail = h.count(loops.unchanged_polls, "poll") + " saw no new output (" + h.fmtPct(polls > 0 ? 100 * (Number(loops.unchanged_polls) || 0) / polls : null) + ") · " +
      h.count(loops.processes_polled, "process was polled", "processes were polled");
    f.lines.push({ text: f.detail });
    if (loops.poll_tokens) f.lines.push({ text: "Poll calls: " + h.numText(loops.poll_calls) + " · " + h.fmtTokens(loops.poll_tokens.total) + " tokens · " +
      h.fmtPct(total > 0 ? 100 * (Number(loops.poll_tokens.total) || 0) / total : null) + " of the Crew" });
    var longest = null;
    (p.findings || []).forEach(function (x) {
      if (x.kind === "process_polling" && (!longest || (x.count || 0) > (longest.count || 0))) longest = x;
    });
    if (longest) {
      var process = processForFinding(p, longest);
      f.segment_ids = (longest.segment_ids || []).slice();
      f.lines.push({ label: "Longest", text: h.excerpt(longest.title || longest.kind, 72) + " · " + (process
        ? h.count(process.polls, "poll") + " · " + h.numText(process.unchanged_polls) + " unchanged · " + h.fmtMs(process.elapsed_ms) +
          (process.tokens ? " · " + h.fmtTokens(process.tokens.total) + " tokens" : "")
        : "process record not linked"), finding: longest, focus: "answer-longest-chain" });
    }
    f.lines.push({ text: counters });
    return f;
  }

  // The evidence of a derived finding: its lines, each with a button to
  // the segment or finding it names.
  function derivedEvidence(body, p, f) {
    var box = h.el("section", { class: "card finding-evidence", "data-evidence-panel": "", tabindex: "-1" }, [
      h.el("div", { class: "card-head" }, [h.el("h3", { text: f.title }), h.el("button", { class: "chip", type: "button", text: "Close evidence",
        "data-focus": "close-evidence", onclick: function () { selectedFinding = ""; h.draw(); } })])
    ]);
    box.appendChild(h.el("div", { class: "answer-lines" }, f.lines.map(function (line) {
      var children = [];
      if (line.label) children.push(h.el("strong", { text: line.label + ": " }));
      if (line.segment) children.push(h.el("button", { class: "text-button", type: "button", "data-focus": line.focus, text: h.segmentLabel(line.segment, p),
        onclick: function () { revealSegments([line.segment.id]); } }), h.txt(" · "));
      if (line.finding) children.push(h.el("button", { class: "text-button", type: "button", "data-focus": line.focus, text: line.text,
        onclick: function () { selectedFinding = line.finding.id; revealSegments(line.finding.segment_ids || [], true); } }));
      else children.push(h.txt(line.text));
      return h.el("span", {}, children);
    })));
    return box;
  }

  function findingEvidence(body, p, f) {
    var box = h.el("section", { class: "card finding-evidence", "data-evidence-panel": "", tabindex: "-1" }, [
      h.el("div", { class: "card-head" }, [h.el("h3", { text: f.title }), h.el("button", { class: "chip", type: "button", text: "Close evidence",
        "data-focus": "close-evidence", onclick: function () { selectedFinding = ""; h.draw(); } })]),
      h.el("p", { text: f.review || "Inspect the commands and source records below." }),
      h.el("div", { class: "facts" }, [h.el("span", { text: (f.count || 0) + " observations" }),
        h.el("span", { text: (h.clock(f.started_at) || "unknown start") + " → " + (h.clock(f.ended_at) || "last observed ongoing") }),
        h.el("span", { text: "Rule " + (f.rule || "unknown") })])
    ]);
    var segments = (f.segment_ids || []).map(function (id) { return h.byID(p.segments, id); }).filter(Boolean);
    if (segments.length) box.appendChild(h.el("div", { class: "chips evidence-links" }, segments.map(function (s) {
      return h.el("button", { class: "chip", type: "button", "data-focus": "evidence-segment:" + s.id, text: "Open " + h.segmentLabel(s, p),
        onclick: function () { revealSegments([s.id]); } });
    })));
    var executions = (f.execution_ids || []).map(function (id) { return h.byID(p.executions, id); }).filter(Boolean);
    if (executions.length) box.appendChild(h.executionList(p, executions, f.execution_ids));
    var evidence = f.evidence || [];
    if (evidence.length) box.appendChild(h.savedDetails("finding-sources:" + f.id, "Source evidence (" + evidence.length + ")",
      [h.el("ul", { class: "evidence-sources" }, evidence.map(function (e) { return h.el("li", {}, [
        h.el("span", { text: e.label || e.kind || e.id }), h.sourceNote(e.source_ref)
      ]); }))], "evidence-sources-wrap"));
    if (!segments.length && !executions.length) box.appendChild(h.el("p", { class: "coverage-note", text: "This finding has no confirmed segment or execution link. Its source evidence is shown above." }));
    return box;
  }

  // ------------------------------------------------------------ effort

  // Cache read is the conversation re-read on every call, 95-99% of a long
  // task's tokens; work is what the call added - input, cache write and
  // output. The table shows both per kind (CON-75). Mixed is one row that
  // says which kinds its calls mixed; no token is split among them. A kind
  // with no call is not listed. Clicking a row filters the strip.
  function effortSection(p, model) {
    var blocks = model.blocks;
    var section = h.el("section", { class: "section effort", "aria-label": "Effort by work type" });
    section.appendChild(sectionHead("Where the effort went", "Each block's calls belong to one kind of work, so the kinds add up to the task. " +
      "Cache read is the conversation re-read on each call; Work is input, cache write and output - what the call added. The bar draws work solid and cache read faded. " +
      "Mixed is one row and names the kinds its calls mixed; no token is split among them. Click a row to filter the strip.",
      kindFilter === "all" ? null : "strip filtered to " + h.kindLabel(kindFilter)));
    if (!blocks.length) {
      section.appendChild(h.el("p", { class: "coverage-note", text: model.prompts.length ? "Steps not recorded yet" : "No model call recorded for this task yet." }));
      return section;
    }
    var sums = {}, extra = [], combos = [];
    var total = { calls: 0, tools: 0, tokens: 0, cache: 0 };
    blocks.forEach(function (b) {
      var kind = b.kind;
      if (!sums[kind]) {
        sums[kind] = { calls: 0, tools: 0, tokens: 0, cache: 0 };
        if (EFFORT_ORDER.indexOf(kind) === -1) extra.push(kind);
      }
      if (kind === "mixed") {
        var combo = mixedParts(b).slice().sort().join("+");
        if (combo && combos.indexOf(combo) === -1) combos.push(combo);
      }
      var t = blockTokens(b), c = blockCache(b), tools = Number(b.tools) || 0;
      sums[kind].calls += b.calls; sums[kind].tools += tools; sums[kind].tokens += t; sums[kind].cache += c;
      total.calls += b.calls; total.tools += tools; total.tokens += t; total.cache += c;
    });
    function share(tokens) { return total.tokens > 0 && tokens > 0 ? h.fmtPct(100 * tokens / total.tokens) : "–"; }
    function bar(row, kind) {
      var work = row.tokens - row.cache;
      return h.el("span", { class: "sc-track", role: "cell", "aria-hidden": "true" }, [
        total.tokens > 0 && work > 0 ? h.el("i", { "data-kind": kind, style: "width:" + (100 * work / total.tokens).toFixed(1) + "%" }) : null,
        total.tokens > 0 && row.cache > 0 ? h.el("i", { class: "sc-cache", "data-kind": kind, style: "width:" + (100 * row.cache / total.tokens).toFixed(1) + "%" }) : null
      ]);
    }
    combos.sort();
    var order = EFFORT_ORDER.slice(0, -2).concat(extra, EFFORT_ORDER.slice(-2)).filter(function (kind) { return sums[kind]; });
    var rows = order.map(function (kind) {
      var row = sums[kind];
      var pressed = kindFilter === kind;
      var mixes = kind === "mixed" && combos.length ? combos.map(function (combo) { return combo.split("+").map(h.kindLabel).join(" + "); }).join(" · ") : "";
      return h.el("div", { class: "sc-row sc-effort", role: "row", "data-effort": kind, "data-parts": kind === "mixed" && combos.length ? combos.join(" · ") : null,
        "data-focus": "effort:" + kind, "aria-pressed": pressed ? "true" : "false", tabindex: "0", title: pressed ? "Show every kind on the strip" : "Show only " + h.kindLabel(kind) + " on the strip",
        onclick: function () { kindFilter = pressed ? "all" : kind; h.draw(); } }, [
        h.el("span", { class: "sc-what", role: "cell" }, [kindSwatch(kind), h.el("span", { text: h.kindLabel(kind) }),
          mixes ? h.el("span", { class: "sc-parts", text: mixes }) : null]),
        h.el("span", { class: "sc-num", role: "cell", text: dashed(row.calls) }),
        h.el("span", { class: "sc-num", role: "cell", text: dashed(row.tools) }),
        h.el("span", { class: "sc-num", role: "cell", text: dashedTokens(row.tokens) }),
        h.el("span", { class: "sc-num", role: "cell", text: dashedTokens(row.cache) }),
        h.el("span", { class: "sc-num", role: "cell", text: dashedTokens(row.tokens - row.cache) }),
        bar(row, kind),
        h.el("span", { class: "sc-num", role: "cell", text: share(row.tokens) })
      ]);
    });
    rows.push(h.el("div", { class: "sc-row sc-effort sc-total", role: "row", "data-effort": "total" }, [
      h.el("span", { role: "cell", text: "Total" }),
      h.el("span", { class: "sc-num", role: "cell", text: dashed(total.calls) }),
      h.el("span", { class: "sc-num", role: "cell", text: dashed(total.tools) }),
      h.el("span", { class: "sc-num", role: "cell", text: dashedTokens(total.tokens) }),
      h.el("span", { class: "sc-num", role: "cell", text: dashedTokens(total.cache) }),
      h.el("span", { class: "sc-num", role: "cell", text: dashedTokens(total.tokens - total.cache) }),
      h.el("span", { role: "cell", "aria-hidden": "true" }),
      h.el("span", { class: "sc-num", role: "cell", text: total.tokens > 0 ? "100%" : "–" })
    ]));
    section.appendChild(h.el("div", { class: "card sc-card" }, [
      h.el("div", { class: "sc-table sc-efforts", role: "table", "aria-label": "Effort by work type" }, [
        h.el("div", { class: "sc-row sc-th", role: "row" }, ["Type", "Mdl", "Tool", "Tokens", "Cache read", "Work", "", "Share"].map(function (label, i) {
          return h.el("span", { role: "columnheader", class: i > 0 && i !== 6 ? "sc-num" : null, title: label === "Mdl" ? "model (API) calls" : label === "Tool" ? "tool calls" : null, text: label });
        }))
      ].concat(rows))
    ]));
    // A prompt recorded before steps existed leaves calls outside any
    // block; say how many rather than let the totals disagree silently.
    var missingCalls = (p.model_calls || 0) - total.calls;
    var missingTokens = (Number(p.tokens && p.tokens.total) || 0) - total.tokens;
    if (missingCalls > 0 || missingTokens > 0) section.appendChild(h.el("p", { class: "coverage-note",
      text: h.count(Math.max(missingCalls, 0), "model call") + " and " + h.fmtTokens(Math.max(missingTokens, 0)) + " tokens are not in a recorded " + (model.source === "stage" ? "stage" : "segment") + "." }));
    return section;
  }

  // ---------------------------------------------------------- tool mix

  // toolMix: the overview's categories per prompt. Each model call belongs
  // to exactly one work type, so a row adds up to its prompt and the Crew
  // row adds up to the ledger.
  function byTokensDesc(a, b) {
    var av = a.tokens ? Number(a.tokens.total) || 0 : -1;
    var bv = b.tokens ? Number(b.tokens.total) || 0 : -1;
    return bv - av;
  }

  function toolMix(body, p) {
    var section = h.el("div", { class: "tool-mix-wrap", "aria-label": "Tool mix by prompt" });
    var prompts = p.prompt_turns || [];
    if (!prompts.length) {
      section.appendChild(h.el("p", { class: "empty", text: "Prompt boundaries have not been recorded." }));
      return section;
    }
    var overview = p.overview;
    if (!overview) section.appendChild(h.el("p", { class: "coverage-note", text: "Work types not recorded yet; calls, tokens, repeats and failed commands are still listed per prompt." }));
    // A kind no model call was ever attributed to would be a column of
    // dashes.
    var kinds = ((overview && overview.categories) || []).filter(function (c) { return c.model_calls || c.tokens; })
      .sort(byTokensDesc).map(function (c) { return c.kind; });
    var head = [h.el("th", { text: "Prompt" }), h.el("th", { class: "n", text: "Calls" }), h.el("th", { class: "n", text: "Tokens" })]
      .concat(kinds.map(function (kind) { return h.el("th", { class: "n" }, [h.kindChip(kind)]); }))
      .concat([h.el("th", { class: "n", text: "Repeats" }), h.el("th", { class: "n", text: "Failed cmds" })]);
    function row(label, calls, tokensText, categories, repeats, failed, className) {
      var cells = [h.el("td", { "data-label": "Prompt", class: "tool-mix-prompt", text: label }),
        h.el("td", { "data-label": "Calls", class: "n", text: calls }), h.el("td", { "data-label": "Tokens", class: "n", text: tokensText })];
      kinds.forEach(function (kind) {
        var category = categories ? categories.find(function (c) { return c.kind === kind; }) : null;
        // No overview for the prompt is unknown ("?"); a kind the prompt
        // never attributed a call to is nothing ("–"), not zero tokens.
        var text = !categories ? "?" : !category || (!category.model_calls && !category.tokens) ? "–"
          : (category.model_calls || 0) + " · " + (category.tokens ? h.fmtTokens(category.tokens.total) : "?");
        cells.push(h.el("td", { "data-label": h.kindLabel(kind), class: "n", text: text }));
      });
      cells.push(h.el("td", { "data-label": "Repeats", class: "n", text: repeats }));
      cells.push(h.el("td", { "data-label": "Failed cmds", class: "n", text: failed }));
      return h.el("tr", { class: className }, cells);
    }
    var repeatsOf = function (segments) { return segments.reduce(function (sum, s) { return sum + (Number(s.repeat_count) || 0); }, 0); };
    var tbody = h.el("tbody");
    prompts.forEach(function (prompt, i) {
      var segments = (p.segments || []).filter(function (s) { return s.prompt_id === prompt.id; });
      var failed = (p.executions || []).filter(function (e) { return e.prompt_id === prompt.id && h.isFailedNative(e); }).length;
      tbody.appendChild(row("P" + (i + 1) + " · " + h.excerpt(prompt.prompt || "Prompt text unavailable", 24), String(prompt.model_calls || 0),
        prompt.model_calls === 0 ? "?" : h.fmtTokens(prompt.tokens && prompt.tokens.total),
        prompt.overview ? prompt.overview.categories || [] : null, String(repeatsOf(segments)), String(failed), null));
    });
    tbody.appendChild(row("Crew", String(p.model_calls || 0), p.model_calls === 0 ? "?" : h.fmtTokens(p.tokens && p.tokens.total),
      overview ? overview.categories || [] : null, String(repeatsOf(p.segments || [])),
      String((p.executions || []).filter(h.isFailedNative).length), "tool-mix-crew"));
    section.appendChild(h.el("div", { class: "table-wrap" }, [h.el("table", { class: "tool-mix" }, [h.el("thead", {}, [h.el("tr", {}, head)]), tbody])]));
    section.appendChild(h.el("p", { class: "actions-note", text: "Each model call belongs to exactly one work type, so rows and columns add up to the ledger. Failed commands are native exit codes; repeats are the segments' own repeat counts." }));
    return section;
  }

  // --------------------------------------------------------- recording

  // unclassifiedCoverage says how much of the recording the classifier
  // could not name: mixed and unknown segments are counted here, never
  // folded into another kind.
  function unclassifiedCoverage(p) {
    var segments = p.segments || [];
    var unnamed = segments.filter(function (s) { return !s.kind || s.kind === "mixed" || s.kind === "unknown"; });
    var sum = function (key) { return unnamed.reduce(function (acc, s) { return acc + (Number(s.tokens && s.tokens[key]) || 0); }, 0); };
    var total = Number(p.tokens && p.tokens.total) || 0, output = Number(p.tokens && p.tokens.output) || 0;
    return "Unclassified segments: " + unnamed.length + " of " + segments.length + " · " +
      h.fmtPct(total > 0 ? 100 * sum("total") / total : null) + " of tokens · " + h.fmtPct(output > 0 ? 100 * sum("output") / output : null) + " of output";
  }

  function recordingDetails(p) {
    var freshness = p.freshness || {};
    var time = p.time || {};
    var children = [h.el("div", { class: "recording-body" }, [
      h.el("p", { text: "Capabilities: " + ((freshness.capabilities || []).join(" · ") || "not reported") }),
      (freshness.missing || []).length ? h.el("ul", {}, freshness.missing.map(function (item) { return h.el("li", { text: item }); })) : null,
      h.el("div", { class: "facts" }, [h.el("span", { text: "Decision wait " + h.fmtMs(time.decision_wait_ms) }),
        h.el("span", { text: "Unallocated prompt time " + h.fmtMs(time.unallocated_ms) }),
        h.el("span", { text: "Summed invocation time " + h.fmtMs(time.invocation_ms) })]),
      h.el("p", { class: "actions-note", text: "Concurrent tools and decision waits may overlap. Unallocated time is not a measurement of model latency. Usage changes only when the harness reports it." }),
      h.el("p", { class: "coverage-note", text: unclassifiedCoverage(p) })
    ])];
    if (p.runtime) children.push(h.el("div", { class: "recording-body" }, [
      h.el("h3", { text: "Observed runtime" }),
      h.el("p", { text: "Harness version " + (p.runtime.harness_version || "unknown") + " · model " + (p.runtime.model || "unknown") + " · effort " + (p.runtime.effort || "unknown") })
    ]));
    var inputs = p.observed_inputs || [];
    if (inputs.length) children.push(h.savedDetails("observed-inputs", "Observed instruction inputs (" + inputs.length + ")", [
      h.el("p", { class: "coverage-note", text: "These inputs were observed in harness records. Byte sizes describe content, not measured token cost." }),
      h.el("ul", { class: "evidence-sources" }, inputs.map(function (input) { return h.el("li", {}, [
        h.el("span", { class: "mono", text: input.path || input.kind || "Unnamed instruction" }),
        h.el("span", { text: " · " + h.fmtBytes(input.bytes) + " · " + (h.day(input.at) || "time unknown") }),
        input.sha256 ? h.el("span", { class: "source-ref mono", text: "sha256 " + input.sha256 }) : null,
        h.sourceNote(input.source_ref)
      ]); }))
    ], "recording-body"));
    if (p.profile) children.push(runProfile(p.profile));
    return h.savedDetails("recording-details", "Recording details and coverage", children, "section diagnostics");
  }

  function runProfile(profile) {
    var details = [h.el("p", { class: "coverage-note", text: "Configured launch inputs were captured " + (h.day(profile.captured_at) || "at an unknown time") +
      ". A configured file is not proof that the harness loaded it." }),
      h.el("div", { class: "facts" }, [
        h.el("span", { text: "Repo " + (profile.repo || "unknown") }),
        h.el("span", { text: "Harness " + (profile.harness || "unknown") }),
        h.el("span", { text: "Requested model " + (profile.requested_model || "harness default") }),
        h.el("span", { text: "Requested effort " + (profile.requested_effort || "harness default") + (profile.effort_omitted ? " · omitted from launch" : "") }),
        h.el("span", { class: "mono", text: "Commit " + (profile.repo_commit || "unknown") }),
        h.el("span", { text: profile.repo_dirty == null ? "Working tree state unknown" : profile.repo_dirty ? "Working tree dirty at launch" : "Working tree clean at launch" })
      ]),
      profile.git_error ? h.el("p", { class: "coverage-note", text: profile.git_error }) : null,
      profile.fingerprint ? h.el("p", { class: "source-ref mono", text: "Profile fingerprint " + profile.fingerprint }) : null,
      h.el("ul", { class: "evidence-sources" }, (profile.documents || []).map(function (doc) { return h.el("li", {}, [
        h.el("span", { class: "mono", text: doc.path || doc.role || "Unnamed configured file" }),
        h.el("span", { text: " · " + (doc.state || "unknown state") + " · " + (doc.state === "configured" ? h.fmtBytes(doc.bytes) : "size unknown") }),
        doc.sha256 ? h.el("span", { class: "source-ref mono", text: "sha256 " + doc.sha256 }) : null
      ]); }))];
    return h.savedDetails("run-profile", "Configured launch profile", details, "recording-body");
  }

  // -------------------------------------------------------- navigation

  // openBlock opens a block below the strip and, with segment ids, those
  // segments inside it.
  function openBlock(id, segmentIDs) {
    selectedBlock = id;
    segmentIDs.forEach(function (sid) { h.openDetail("segment:" + sid); });
    h.draw();
    var target = h.query(".tl-drill");
    if (target) { target.scrollIntoView({ block: "start", behavior: "auto" }); target.focus({ preventScroll: true }); }
  }

  // revealSegments opens the segments named, in the block that holds the
  // first of them, and scrolls to it; with `finding` it scrolls to the
  // finding's evidence panel instead.
  function revealSegments(ids, finding) {
    var p = h.performance();
    if (p && ids.length) {
      var b = blockWithSegment(blocksOf(h.body(), p).blocks, ids[0]);
      if (b) selectedBlock = b.id;
    }
    ids.forEach(function (id) { h.openDetail("segment:" + id); });
    selectedSegment = ids[0] || "";
    h.draw();
    var target = finding ? h.find("data-evidence-panel", "") : h.find("data-segment", selectedSegment);
    if (target) { target.scrollIntoView({ block: "start", behavior: "auto" }); target.focus({ preventScroll: true }); }
  }

  // revealPrompt marks a prompt's band on the strip and scrolls to it; the
  // Journey tab calls it when a crew bar is clicked.
  function revealPrompt(id) {
    selectedPrompt = id;
    h.draw();
    var target = h.find("data-prompt", id);
    if (target) target.scrollIntoView({ block: "start", behavior: "auto" });
  }

  return { render: render, reset: reset, revealSegments: revealSegments, revealPrompt: revealPrompt };
}
