/*
 * office.js - Mate Office: the scene of docs/mvp.md M5 drawn as an office,
 * from the same read-only API the admin UI at / reads (docs/dashboard.md
 * section 11).
 *
 * Every project is a room. Its Mate sits at the big desk by the door, its
 * open crews at the desks below, and its finished tasks are in the filing
 * cabinet. The hallway holds the captain's desk and any crew that has gone
 * to the Mate's door with a question. Clicking anyone opens a drawer with
 * what the API knows about them; nothing on this page writes anything.
 *
 * Where each actor stands is `v_now`'s scene state (internal/timeline/scene,
 * docs/timeline.md section 9). A value the API does not have is drawn as
 * unknown (`?`, a hatched track), never as zero.
 *
 * The DOM is built node by node: a workspace quotes whatever the captain
 * typed and whatever a harness printed, so no string from the API ever
 * reaches anything but textContent or an attribute value.
 */

(function () {
  "use strict";

  var SVG_NS = "http://www.w3.org/2000/svg";

  // ------------------------------------------------------------ drawing

  // Icon paths are the design's own (24x24, stroked).
  var IC = {
    office: "M4 21V5a2 2 0 0 1 2-2h12a2 2 0 0 1 2 2v16M2 21h20M9 7h1M14 7h1M9 11h1M14 11h1M10 21v-4h4v4",
    arriving: "M15 3h4a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2h-4M10 17l5-5-5-5M15 12H3",
    leaving: "M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9",
    working: "M3 6h18a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H3a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1zM6 10h.01M10 10h.01M14 10h.01M18 10h.01M7 14h10",
    walking: "M5 12h14M13 6l6 6-6 6",
    ask: "M21 11.5a8.4 8.4 0 0 1-12.2 7.5L3 21l2-5.3A8.5 8.5 0 1 1 21 11.5zM9.6 9a2.5 2.5 0 0 1 4.8.8c0 1.7-2.4 2.2-2.4 3.7M12 16.5h.01",
    review: "M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8zM14 3v5h5M9 13h6M9 17h4",
    idle: "M12 3a9 9 0 1 0 0 18a9 9 0 1 0 0-18zM10 9v6M14 9v6",
    blocked: "M10.3 3.9L1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0zM12 9v4M12 17h.01",
    bang: "M12 6v8M12 18h.01",
    asleep: "M4 6h6l-6 7h6M13 11h7l-7 8h7",
    gone: "M12 3a9 9 0 1 0 0 18a9 9 0 1 0 0-18zM5.6 5.6l12.8 12.8",
    unknown: "M12 3a9 9 0 1 0 0 18a9 9 0 1 0 0-18zM9.6 9a2.5 2.5 0 0 1 4.8.8c0 1.7-2.4 2.2-2.4 3.7M12 16.5h.01",
    think: "M9 18h6M10 21h4M12 3a6 6 0 0 0-3.6 10.8c.7.6 1.1 1.3 1.1 2.2h5c0-.9.4-1.6 1.1-2.2A6 6 0 0 0 12 3z",
    read: "M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12zM12 9a3 3 0 1 0 0 6a3 3 0 1 0 0-6z",
    answer: "M9 17l-5-5 5-5M4 12h11a5 5 0 0 1 5 5v2",
    merge: "M6 3v12M18 9a3 3 0 1 0 0-6a3 3 0 0 0 0 6zM6 21a3 3 0 1 0 0-6a3 3 0 0 0 0 6zM18 9a9 9 0 0 1-9 9",
    phone: "M22 16.9v3a2 2 0 0 1-2.2 2 19.8 19.8 0 0 1-8.6-3.1 19.5 19.5 0 0 1-6-6A19.8 19.8 0 0 1 2.1 4.2 2 2 0 0 1 4.1 2h3a2 2 0 0 1 2 1.7c.1.9.4 1.8.7 2.7a2 2 0 0 1-.5 2.1L8 9.8a16 16 0 0 0 6 6l1.3-1.3a2 2 0 0 1 2.1-.4c.9.3 1.8.6 2.7.7a2 2 0 0 1 1.7 2z",
    mail: "M4 4h16a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2zM22 6l-10 7L2 6",
    merged: "M20 6L9 17l-5-5",
    done: "M12 3a9 9 0 1 0 0 18a9 9 0 1 0 0-18zM8 12.5l2.7 2.7L16 9.5",
    edit: "M12 20h9M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z",
    failed: "M18 6L6 18M6 6l12 12",
    close: "M18 6L6 18M6 6l12 12",
    maximize: "M15 3h6v6M9 21H3v-6M21 3l-7 7M3 21l7-7",
    minimize: "M4 14h6v6M20 10h-6V4M14 10l7-7M3 21l7-7",
    sun: "M12 8a4 4 0 1 0 0 8a4 4 0 1 0 0-8zM12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4",
    moon: "M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z",
    arrowR: "M5 12h14M13 6l6 6-6 6",
    usage: "M4 20V11M10 20V4M16 20v-7M22 20H2",
    ship: "M16 18l6-6-6-6M8 6l-6 6 6 6",
    scout: "M12 3a9 9 0 1 0 0 18a9 9 0 1 0 0-18zM15.5 8.5l-2 5-5 2 2-5z",
    reviewer: "M9 3h6v3H9zM9 4.5H6a1 1 0 0 0-1 1V20a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V5.5a1 1 0 0 0-1-1h-3M9 14l2 2 4-4",
    cabinet: "M5 3h14a1 1 0 0 1 1 1v16a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1zM4 9h16M4 15h16M10 6h4M10 12h4M10 18h4"
  };

  // Icons for the control slots (CON-141's buttons and the redesign's).
  IC.play = "M6 4l14 8-14 8z";
  IC.stopsq = "M6 6h12v12H6z";
  IC.restart = "M3 12a9 9 0 1 0 3-6.7L3 8M3 3v5h5";
  IC.mode = "M7 7h10a5 5 0 0 1 0 10H7A5 5 0 0 1 7 7zM16 12h.01";
  IC.eraser = "M20 20H8l-5-5a2 2 0 0 1 0-2.8L12.2 3a2 2 0 0 1 2.8 0l5 5a2 2 0 0 1 0 2.8L12 19M6 11l7 7";
  IC.copy = "M9 9h11v11H9zM5 15H4a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h10a1 1 0 0 1 1 1v1";
  IC.trash = "M3 6h18M8 6V4h8v2M19 6l-1 14H6L5 6M10 11v6M14 11v6";
  IC.wrench = "M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.8-3.8a6 6 0 0 1-7.9 7.9l-6.9 6.9a2.1 2.1 0 0 1-3-3l6.9-6.9a6 6 0 0 1 7.9-7.9z";
  IC.diff = "M12 3v6M9 6h6M9 18h6M5 21h14a1 1 0 0 0 1-1V4a1 1 0 0 0-1-1H5a1 1 0 0 0-1 1v16a1 1 0 0 0 1 1z";
  IC.code = "M16 18l6-6-6-6M8 6l-6 6 6 6";
  IC.plus = "M12 5v14M5 12h14";
  IC.filter = "M3 5h18l-7 8v6l-4 2v-8z";
  IC.lock = "M6 11h12a1 1 0 0 1 1 1v8a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1v-8a1 1 0 0 1 1-1zM8 11V7a4 4 0 0 1 8 0v4";
  IC.inbox = "M22 12h-6l-2 3h-4l-2-3H2M5.5 5h13L22 12v6a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2v-6z";
  IC.send = "M22 2L11 13M22 2l-7 20-4-9-9-4z";
  IC.fork = "M6 3v6a3 3 0 0 0 3 3h6a3 3 0 0 1 3 3v6M6 21v-6";
  IC.clock = "M12 3a9 9 0 1 0 0 18a9 9 0 1 0 0-18zM12 7v5l3 2";
  IC.chevR = "M9 6l6 6-6 6";
  IC.chat = "M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z";
  IC.assign = "M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2M9 3a4 4 0 1 0 0 8a4 4 0 1 0 0-8zM19 8v6M22 11h-6";

  // The harness is the head, drawn with its own mark (the design's "harness
  // is the face"). A harness this page has no mark for keeps a plain head
  // with its initial, and an unrecorded one a "?".
  var FACE = {
    claude: "m4.7144 15.9555 4.7174-2.6471.079-.2307-.079-.1275h-.2307l-.7893-.0486-2.6956-.0729-2.3375-.0971-2.2646-.1214-.5707-.1215-.5343-.7042.0546-.3522.4797-.3218.686.0608 1.5179.1032 2.2767.1578 1.6514.0972 2.4468.255h.3886l.0546-.1579-.1336-.0971-.1032-.0972L6.973 9.8356l-2.55-1.6879-1.3356-.9714-.7225-.4918-.3643-.4614-.1578-1.0078.6557-.7225.8803.0607.2246.0607.8925.686 1.9064 1.4754 2.4893 1.8336.3643.3035.1457-.1032.0182-.0728-.164-.2733-1.3539-2.4467-1.445-2.4893-.6435-1.032-.17-.6194c-.0607-.255-.1032-.4674-.1032-.7285L6.287.1335 6.6997 0l.9957.1336.419.3642.6192 1.4147 1.0018 2.2282 1.5543 3.0296.4553.8985.2429.8318.091.255h.1579v-.1457l.1275-1.706.2368-2.0947.2307-2.6957.0789-.7589.3764-.9107.7468-.4918.5828.2793.4797.686-.0668.4433-.2853 1.8517-.5586 2.9021-.3643 1.9429h.2125l.2429-.2429.9835-1.3053 1.6514-2.0643.7286-.8196.85-.9046.5464-.4311h1.0321l.759 1.1293-.34 1.1657-1.0625 1.3478-.8804 1.1414-1.2628 1.7-.7893 1.36.0729.1093.1882-.0183 2.8535-.607 1.5421-.2794 1.8396-.3157.8318.3886.091.3946-.3278.8075-1.967.4857-2.3072.4614-3.4364.8136-.0425.0304.0486.0607 1.5482.1457.6618.0364h1.621l3.0175.2247.7892.522.4736.6376-.079.4857-1.2142.6193-1.6393-.3886-3.825-.9107-1.3113-.3279h-.1822v.1093l1.0929 1.0686 2.0035 1.8092 2.5075 2.3314.1275.5768-.3218.4554-.34-.0486-2.2039-1.6575-.85-.7468-1.9246-1.621h-.1275v.17l.4432.6496 2.3436 3.5214.1214 1.0807-.17.3521-.6071.2125-.6679-.1214-1.3721-1.9246L14.38 17.959l-1.1414-1.9428-.1397.079-.674 7.2552-.3156.3703-.7286.2793-.6071-.4614-.3218-.7468.3218-1.4753.3886-1.9246.3157-1.53.2853-1.9004.17-.6314-.0121-.0425-.1397.0182-1.4328 1.9672-2.1796 2.9446-1.7243 1.8456-.4128.164-.7164-.3704.0667-.6618.4008-.5889 2.386-3.0357 1.4389-1.882.929-1.0868-.0062-.1579h-.0546l-6.3385 4.1164-1.1293.1457-.4857-.4554.0608-.7467.2307-.2429 1.9064-1.3114Z",
    codex: "M8.086.457a6.105 6.105 0 013.046-.415c1.333.153 2.521.72 3.564 1.7a.117.117 0 00.107.029c1.408-.346 2.762-.224 4.061.366l.063.03.154.076c1.357.703 2.33 1.77 2.918 3.198.278.679.418 1.388.421 2.126a5.655 5.655 0 01-.18 1.631.167.167 0 00.04.155 5.982 5.982 0 011.578 2.891c.385 1.901-.01 3.615-1.183 5.14l-.182.22a6.063 6.063 0 01-2.934 1.851.162.162 0 00-.108.102c-.255.736-.511 1.364-.987 1.992-1.199 1.582-2.962 2.462-4.948 2.451-1.583-.008-2.986-.587-4.21-1.736a.145.145 0 00-.14-.032c-.518.167-1.04.191-1.604.185a5.924 5.924 0 01-2.595-.622 6.058 6.058 0 01-2.146-1.781c-.203-.269-.404-.522-.551-.821a7.74 7.74 0 01-.495-1.283 6.11 6.11 0 01-.017-3.064.166.166 0 00.008-.074.115.115 0 00-.037-.064 5.958 5.958 0 01-1.38-2.202 5.196 5.196 0 01-.333-1.589 6.915 6.915 0 01.188-2.132c.45-1.484 1.309-2.648 2.577-3.493.282-.188.55-.334.802-.438.286-.12.573-.22.861-.304a.129.129 0 00.087-.087A6.016 6.016 0 015.635 2.31C6.315 1.464 7.132.846 8.086.457zm-.804 7.85a.848.848 0 00-1.473.842l1.694 2.965-1.688 2.848a.849.849 0 001.46.864l1.94-3.272a.849.849 0 00.007-.854l-1.94-3.393zm5.446 6.24a.849.849 0 000 1.695h4.848a.849.849 0 000-1.696h-4.848z",
    pi: "M0 0v24h6v-6h6v-6H6V6h6v6h6V0Zm18 12v12h6V12Z"
  };

  var HARNESS = {
    claude: { name: "Claude", tag: "tg-c" },
    codex: { name: "Codex", tag: "tg-x" },
    pi: { name: "Pi", tag: "tg-p" }
  };

  function harnessInfo(h) {
    var key = h ? String(h).toLowerCase() : "";
    if (HARNESS[key]) return { key: key, name: HARNESS[key].name, tag: HARNESS[key].tag, mark: true };
    if (!key) return { key: "", name: "harness ?", tag: "tg-u", mark: false, letter: "?" };
    return { key: key, name: String(h), tag: "tg-u", mark: false, letter: key.charAt(0).toUpperCase() };
  }

  // Shirts are decoration, not data: a crew keeps the same colour on every
  // load because the colour is picked by its name. The palette is the
  // design's own set of shirts.
  var SHIRTS = ["#8EA6BF", "#C4A07A", "#B48DA0", "#9BB096", "#C7A46A", "#89A9A3", "#B9917F", "#A9B57E", "#9FA6C7", "#8E9AA6", "#5F8378", "#7E86B4"];

  function shirt(name) {
    var s = String(name || "");
    var h = 0;
    for (var i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) >>> 0;
    return SHIRTS[h % SHIRTS.length];
  }

  // ------------------------------------------------------------ vocabulary

  // Crew scene states (docs/timeline.md 9.1), each with a glyph as well as a
  // tone so colour is never the only signal. The words are the design's,
  // except walking_to_ceo: the scene sends a crew to its Mate (the CEO of
  // the room), not to the captain, so it says so.
  var CREW_STATES = {
    arriving: { word: "arriving", tone: "t-info", icon: "arriving" },
    at_desk_working: { word: "working", tone: "t-ok", icon: "working" },
    walking_to_ceo: { word: "to the Mate", tone: "t-info", icon: "walking" },
    waiting_at_ceo: { word: "needs an answer", tone: "t-wait", icon: "ask" },
    waiting_review: { word: "waiting for review", tone: "t-wait", icon: "review" },
    idle: { word: "idle", tone: "t-idle", icon: "idle" },
    blocked: { word: "stuck", tone: "t-bad", icon: "blocked" },
    asleep: { word: "asleep", tone: "t-slp", icon: "asleep" },
    leaving: { word: "leaving", tone: "t-gone", icon: "leaving" },
    gone: { word: "agent gone", tone: "t-gone", icon: "gone" }
  };
  var LEGEND = ["arriving", "at_desk_working", "walking_to_ceo", "waiting_at_ceo", "waiting_review", "idle", "blocked", "asleep", "gone"];

  // The Mate's states (docs/timeline.md 9.1 and 9.4).
  var MATE_STATES = {
    idle: { word: "idle", tone: "t-idle", icon: "idle" },
    deciding: { word: "deciding", tone: "t-ok", icon: "think" },
    reading: { word: "reading", tone: "t-info", icon: "read" },
    answering: { word: "answering", tone: "t-info", icon: "answer" },
    reviewing: { word: "reviewing", tone: "t-wait", icon: "review" },
    merging: { word: "merging", tone: "t-ok", icon: "merge" },
    on_phone: { word: "on the phone", tone: "t-info", icon: "phone" },
    receiving_digest: { word: "note on the desk", tone: "t-wait", icon: "mail" },
    asleep: { word: "asleep", tone: "t-slp", icon: "asleep" },
    blocked: { word: "stuck", tone: "t-bad", icon: "blocked" },
    gone: { word: "agent gone", tone: "t-gone", icon: "gone" }
  };

  var UNKNOWN_STATE = { word: "unknown", tone: "t-gone", icon: "unknown" };

  function crewState(s) { return CREW_STATES[s] || (s ? { word: String(s).replace(/_/g, " "), tone: "t-gone", icon: "unknown" } : UNKNOWN_STATE); }
  function mateState(s) { return MATE_STATES[s] || (s ? { word: String(s).replace(/_/g, " "), tone: "t-gone", icon: "unknown" } : UNKNOWN_STATE); }

  // The scene's `detail` in words: why a crew is asleep or stuck, what a
  // note on the Mate's desk is. The incident kinds are the console's own
  // phrases (internal/ui/console's inbox).
  var DETAILS = {
    stale: "quiet too long",
    runtime_lost: "agent unreachable",
    wedged: "send wedged",
    no_turn: "no turn yet",
    context_high: "context high",
    question: "with a question",
    handback: "with finished work",
    digest: "a digest",
    assign: "the captain's note"
  };
  function detailWord(d) { return d ? (DETAILS[d] || String(d).replace(/_/g, " ")) : ""; }

  // The present tense of docs/timeline.md 9.5's scene phrases, for the
  // Mate drawer's "Current activity".
  function mateSentence(m) {
    var who = m.target || "a crew";
    switch (m.state) {
      case "idle": return "Alone in its office";
      case "deciding": return m.target ? "Thinking it over, facing " + m.target : "Thinking it over";
      case "reading": return "Reading " + who + "'s note";
      case "answering": return "Answering " + who;
      case "reviewing": return "Reviewing " + who + "'s work";
      case "merging": return "Landing " + who + "'s work";
      case "on_phone": return "On the phone with the captain";
      case "receiving_digest":
        return (m.detail === "assign" ? "The captain's note" : "A digest") + " about " + who + " is on the desk, unread";
      case "asleep": return "Asleep: nothing has moved" + (m.detail ? " (" + detailWord(m.detail) + ")" : "");
      case "blocked": return "Cannot be reached" + (m.detail ? " (" + detailWord(m.detail) + ")" : "");
      case "gone": return "Out of the building";
      case "": case undefined: case null: return "Not placed in the scene yet";
      default: return String(m.state).replace(/_/g, " ");
    }
  }

  // ------------------------------------------------------------ numbers

  // Tokens and cost are humanize.js's, the admin UI's own copy of
  // internal/query's formatters, so no number here can read differently
  // from `mate usage`. A null is unknown and prints `?`.
  function tok(n) { return n == null ? "?" : humanizeTokens(n); }
  function cost(v) { return v == null ? "?" : humanizeCost(v); }

  // formatAge is cmd/mate's formatAge (the AGE column): the largest whole
  // unit, rounded down - a crew in a state for 3 days 23 hours has been in
  // it for 3 days.
  function formatAge(ms) {
    if (ms == null || !isFinite(ms)) return "?";
    if (ms < 0) ms = 0;
    if (ms < 60000) return Math.floor(ms / 1000) + "s";
    if (ms < 3600000) return Math.floor(ms / 60000) + "m";
    if (ms < 86400000) return Math.floor(ms / 3600000) + "h";
    return Math.floor(ms / 86400000) + "d";
  }

  // formatRun is how long a task ran, to two units: a crew that ran 1 hour
  // 23 minutes reads "1h 23m" where formatAge would say "1h". Unknown (age_ms
  // 0: no spawn time, or one after the close) reads "?".
  function formatRun(ms) {
    if (!ms || !isFinite(ms) || ms < 0) return "?";
    if (ms < 60000) return Math.floor(ms / 1000) + "s";
    if (ms < 3600000) return Math.floor(ms / 60000) + "m";
    var h = Math.floor(ms / 3600000);
    if (ms < 86400000) return h + "h " + Math.floor(ms % 3600000 / 60000) + "m";
    return Math.floor(ms / 86400000) + "d " + h % 24 + "h";
  }

  function parseAt(ts) {
    if (!ts) return null;
    var t = Date.parse(ts);
    return isNaN(t) ? null : t;
  }

  function age(ts) {
    var t = parseAt(ts);
    return t == null ? "?" : formatAge(Date.now() - t);
  }

  function pad(n) { return n < 10 ? "0" + n : String(n); }

  function clock(ts) {
    var t = typeof ts === "number" ? ts : parseAt(ts);
    if (t == null) return "?";
    var d = new Date(t);
    return pad(d.getHours()) + ":" + pad(d.getMinutes()) + ":" + pad(d.getSeconds());
  }

  var MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
  function monthDay(ts) {
    var t = parseAt(ts);
    if (t == null) return "?";
    var d = new Date(t);
    return MONTHS[d.getMonth()] + " " + d.getDate();
  }

  function plural(n, one, many) { return n + " " + (n === 1 ? one : many); }

  // ------------------------------------------------------------ DOM tools

  function el(tag, attrs, children) {
    var node = document.createElement(tag);
    setAttrs(node, attrs);
    append(node, children);
    return node;
  }

  function setAttrs(node, attrs) {
    if (!attrs) return;
    Object.keys(attrs).forEach(function (k) {
      var v = attrs[k];
      if (v == null || v === false) return;
      if (k === "text") { node.textContent = String(v); return; }
      if (k === "class") { node.setAttribute("class", String(v)); return; }
      if (k === "onclick") { node.addEventListener("click", v); return; }
      node.setAttribute(k, v === true ? "" : String(v));
    });
  }

  function append(node, children) {
    if (children == null) return node;
    if (!Array.isArray(children)) children = [children];
    children.forEach(function (c) {
      if (c == null || c === false) return;
      node.appendChild(typeof c === "string" || typeof c === "number" ? document.createTextNode(String(c)) : c);
    });
    return node;
  }

  function svgPath(cls, d, viewBox) {
    var svg = document.createElementNS(SVG_NS, "svg");
    svg.setAttribute("class", cls);
    svg.setAttribute("viewBox", viewBox || "0 0 24 24");
    svg.setAttribute("aria-hidden", "true");
    var p = document.createElementNS(SVG_NS, "path");
    p.setAttribute("d", d);
    svg.appendChild(p);
    return svg;
  }

  function icon(name) { return svgPath("i", IC[name] || IC.unknown); }

  function chip(info, text, extra) {
    return el("span", { class: "chip " + info.tone + (extra ? " " + extra : "") }, [icon(info.icon), text == null ? info.word : text]);
  }

  function icoChip(info) {
    return el("span", { class: "chip ico " + info.tone, title: info.word }, icon(info.icon));
  }

  function htag(h) {
    return el("span", { class: "htag " + h.tag, text: h.name });
  }

  // face draws a head: the harness mark on its own colour, or a plain head.
  function face(cls, h, extra) {
    if (h.mark) {
      return el("span", { class: cls + " fh-" + h.key + (extra ? " " + extra : "") }, svgPath("fc", FACE[h.key]));
    }
    return el("span", { class: cls + " fh-unknown" + (extra ? " " + extra : ""), text: h.letter });
  }

  // pick wires a selectable figure: clicking it opens its drawer and does
  // not fall through to the floor, whose click closes the drawer.
  function pickable(node, key) {
    node.setAttribute("data-key", key);
    node.setAttribute("data-focus", "pick:" + key);
    node.addEventListener("click", function (e) {
      if (e && e.stopPropagation) e.stopPropagation();
      select(key);
    });
    return node;
  }

  // ------------------------------------------------------------ the API

  function api(path) {
    // Every read is a GET to this page's own origin; the one write is
    // press() below.
    return request(path, { method: "GET", headers: { accept: "application/json" } });
  }

  // press is POST /api/actions (CON-141): one action button, run by the
  // console's own action function. A JSON body and this origin are what
  // the server asks of a write.
  function press(body) {
    return request("/api/actions", {
      method: "POST",
      headers: { accept: "application/json", "content-type": "application/json" },
      body: JSON.stringify(body)
    });
  }

  function request(path, init) {
    return fetch(path, init).catch(function () {
      var e = new Error("the dashboard is not answering");
      e.reason = "the dashboard is not answering";
      e.status = 0;
      throw e;
    }).then(function (r) {
      return r.json().catch(function () {
        var bad = new Error("the dashboard answered " + r.status + " with something that is not JSON");
        bad.status = r.status;
        throw bad;
      }).then(function (body) {
        if (!r.ok) {
          var e = new Error(body.error || ("the dashboard answered " + r.status));
          e.reason = body.reason || body.error || "";
          e.status = r.status;
          throw e;
        }
        return body;
      });
    });
  }

  function enc(s) { return encodeURIComponent(s); }
  function projectPath(p) { return "/api/projects/" + enc(p); }
  function taskPath(p, c) { return "/api/projects/" + enc(p) + "/tasks/" + enc(c); }
  function matePath(p) { return "/api/projects/" + enc(p) + "/mate"; }
  function pluginsPath(p) { return "/api/plugins?project=" + enc(p); }
  function actionsPath(p) { return "/api/projects/" + enc(p) + "/actions"; }
  function historyPath(p, since) { return "/api/projects/" + enc(p) + "/history?since=" + enc(since); }

  // ------------------------------------------------------------ state

  var root = document.getElementById("office");
  var ws = null;              // /api/workspace
  var now = null;             // actor id -> SceneRow, or null when /api/now failed
  var projects = {};          // name -> /api/projects/{p} body, or {__error}
  var fatal = null;           // the workspace could not be read at all
  var lastEventID = 0;
  var pollToken = 0;
  var detailToken = 0;
  var detail = null;          // {key, body} | {key, error} | {key, loading}
  var apps = null;            // the open Mate drawer's /api/plugins: {key, body} | {key, error}
  var live = { state: "loading", at: null, reason: "" };
  var sel = parseHash();
  var lastOpener = null;
  var cabFilter = { project: "", word: "" }; // the cabinet's final-state filter, for one room
  var board = null;           // /api/board: {days, body} | {days, error}
  var boardDays = storedBoardDays();
  var boardProject = "";      // "" is the whole workspace
  var boardDay = null;        // /api/board/day: {date, body} | {date, error}
  var news = null;            // /api/news: {body} | {error}
  var newsOpen = {};          // "<project>/<id>" -> true for each news opened up in the captain drawer
  var newsEarlierAll = false; // the captain drawer lists every closed news, not only the latest
  var acts = null;            // the open drawer's /api/projects/{p}/actions: {key, body} | {key, error}
  var asking = null;          // {key, name, crew, button}: the destructive action whose question is open
  var running = {};           // drawer key -> the button running there; that drawer's other buttons wait
  var outcomes = {};          // drawer key -> {kind, text}: the last action's one line on that drawer
  var diffView = null;        // {key, body} | {key, error} | {key, loading}: a crew's branch diff
  var wsReadOnly = null;      // why this dashboard cannot act ("" when it can, null until known)
  var mateTab = "details";    // the Mate drawer's tab: "details" or "talk"
  var talkOpen = {};          // exchange id -> true when an older turn is opened up
  var mateFull = storedFlag("mate-office-mate-full"); // the Mate drawer fills the page, replies unclipped
  var matesOnly = storedFlag("mate-office-mates-only"); // the console's `v`: rooms show only their Mate
  var inboxMode = {};         // project -> "inbox" | "log"
  var inboxLog = null;        // the open inbox's full log: {key, body} | {key, error}
  var newsDraft = {};         // "<project>/<id>" -> {opt, own}: an answer being written
  var addForm = { name: "", repo: "" };
  var staleList = [];         // /api/events' timeline_stale: [{project, since, error}]
  var toast = null;           // {project, id, title}: news that arrived while the page was open
  var knownNews = null;       // "<project>/<id>" -> true for news already seen by this page

  function storedFlag(k) {
    try { return window.localStorage.getItem(k) === "1"; } catch (e) { return false; }
  }

  function parseHash() {
    var h = (window.location.hash || "").replace(/^#\/?/, "");
    if (!h) return null;
    var parts = h.split("/").map(function (x) {
      try { return decodeURIComponent(x); } catch (e) { return x; }
    });
    if (parts[0] === "mate" && parts[1]) return { kind: "mate", project: parts[1] };
    if (parts[0] === "cabinet" && parts[1]) return { kind: "cab", project: parts[1] };
    if (parts[0] === "captain") return { kind: "captain" };
    if (parts[0] === "inbox" && parts[1]) return { kind: "inbox", project: parts[1] };
    if (parts[0] === "add") return { kind: "add" };
    if (parts[0] === "board") return parts[1] ? { kind: "board", date: parts[1] } : { kind: "board" };
    if (parts[0] === "crew" && parts[1] && parts[2]) return { kind: "crew", project: parts[1], crew: parts[2] };
    return null;
  }

  function selKey(s) {
    if (!s) return "";
    if (s.kind === "crew") return "crew/" + enc(s.project) + "/" + enc(s.crew);
    if (s.kind === "cab") return "cabinet/" + enc(s.project);
    if (s.kind === "board") return "board" + (s.date ? "/" + enc(s.date) : "");
    if (s.kind === "captain") return "captain";
    if (s.kind === "inbox") return "inbox/" + enc(s.project);
    if (s.kind === "add") return "add";
    return "mate/" + enc(s.project);
  }

  function select(key) {
    var cur = selKey(sel);
    if (key === cur) return;
    var active = document.activeElement;
    lastOpener = active && active.getAttribute ? active.getAttribute("data-focus") : null;
    window.location.hash = key ? "#" + key : "#";
    // hashchange does the rest, so the back button and a pasted link take
    // the same path as a click.
    if (!("onhashchange" in window)) onHash();
  }

  function onHash() {
    var next = parseHash();
    var changed = selKey(next) !== selKey(sel);
    sel = next;
    if (changed) {
      detail = null;
      asking = null;
      diffView = null;
      loadDetail().then(draw);
      draw();
      var close = root.querySelector("[data-focus=\"drawer-close\"]");
      if (sel && close && close.focus) close.focus({ preventScroll: true });
      if (!sel && lastOpener) {
        var back = root.querySelector("[data-focus=\"" + lastOpener + "\"]");
        if (back && back.focus) back.focus({ preventScroll: true });
      }
    }
  }

  // ------------------------------------------------------------ loading

  function loadAll() {
    var token = pollToken;
    return Promise.all([
      api("/api/workspace"),
      api("/api/now").catch(function (e) { return { __error: e }; })
    ]).then(function (res) {
      if (token !== pollToken) return null;
      var wsBody = res[0];
      var nowBody = res[1];
      return Promise.all((wsBody.projects || []).map(function (p) {
        return api(projectPath(p.name)).then(function (b) { return [p.name, b]; }, function (e) { return [p.name, { __error: e }]; });
      })).then(function (list) {
        if (token !== pollToken) return;
        ws = wsBody;
        fatal = null;
        if (nowBody.__error) {
          now = null;
        } else {
          now = {};
          (nowBody.now || []).forEach(function (r) { now[r.actor_id] = r; });
        }
        var next = {};
        list.forEach(function (pair) { next[pair[0]] = pair[1]; });
        projects = next;
        var ids = [wsBody.last_event_id || 0];
        if (!nowBody.__error) ids.push(nowBody.last_event_id || 0);
        // The smallest id any of the bodies was built at: polling from it
        // can only fetch an event twice, never miss one.
        lastEventID = Math.min.apply(null, ids);
        // An open captain drawer reads the news in loadDetail already.
        return Promise.all([loadDetail(), sel && sel.kind === "captain" ? null : loadNews(), probeActions(wsBody)]);
      });
    }).then(function () {
      if (token !== pollToken) return;
      markLive();
      draw();
    }, function (err) {
      if (token !== pollToken) return;
      if (!ws) fatal = err;
      markStale(err.reason || err.message);
      draw();
    });
  }

  function loadDetail() {
    var s = sel;
    var key = selKey(s);
    var token = ++detailToken;
    if (s && s.kind === "captain") return loadNews();
    if (s && s.kind === "add") return Promise.resolve();
    if (s && s.kind === "inbox") return (inboxMode[s.project] === "log" ? loadInboxLog(s.project) : Promise.resolve());
    if (!s || s.kind === "cab") return Promise.resolve();
    if (s.kind === "board") return Promise.all([loadBoard(), s.date ? loadBoardDay(s.date) : null]);
    if (!detail || detail.key !== key) detail = { key: key, loading: true };
    var path = s.kind === "crew" ? taskPath(s.project, s.crew) : matePath(s.project);
    var reads = [api(path).then(function (body) {
      if (token !== detailToken) return;
      detail = { key: key, body: body };
    }, function (err) {
      if (token !== detailToken) return;
      detail = { key: key, error: err };
    })];
    // The buttons are the console's snapshot, read beside the drawer and on
    // their own: a failure leaves the rest of the drawer drawn.
    reads.push(api(actionsPath(s.project)).then(function (body) {
      noteReadOnly(body);
      if (token === detailToken) acts = { key: key, body: body };
    }, function (err) {
      if (token === detailToken) acts = { key: key, error: err };
    }));
    // The project's apps are read only while its Mate drawer is open, and
    // on their own: a failure here leaves the rest of the drawer drawn.
    if (s.kind === "mate") {
      reads.push(api(pluginsPath(s.project)).then(function (body) {
        if (token === detailToken) apps = { key: key, body: body };
      }, function (err) {
        if (token === detailToken) apps = { key: key, error: err };
      }));
    }
    return Promise.all(reads);
  }

  // loadNews reads every Mate's news (GET /api/news) for the captain's desk
  // and drawer. It never fails the page: an error is kept and shown, and
  // the promise says whether anything changed.
  function loadNews() {
    var before = news ? JSON.stringify(news.body ? news.body.projects : news.error.message) : "";
    return api("/api/news").then(function (body) {
      news = { body: body };
      noticeNews(body);
    }, function (err) {
      news = { error: err };
    }).then(function () {
      return JSON.stringify(news.body ? news.body.projects : news.error.message) !== before;
    });
  }

  // What the last /api/events said about timelines that stopped moving
  // (CON-139): the page answers, but what it draws for them is old.
  var timelineStale = "";

  function staleTimelines(list) {
    return (list || []).map(function (s) {
      return "timeline " + s.project + " since " + clock(s.since) + ": " + s.error;
    }).join("; ");
  }

  function markLive() {
    if (timelineStale) {
      live = { state: "stale", at: Date.now(), reason: timelineStale };
      return;
    }
    live = { state: "live", at: Date.now(), reason: "" };
  }

  function markStale(reason) {
    live = { state: "stale", at: live.at, reason: reason || "the dashboard is not answering" };
    drawLive();
  }

  // One long poll on /api/events, as the admin UI keeps itself live. A
  // moved scene or a new event reloads the scene; a quiet poll with a Crew
  // or Mate drawer open reloads only that drawer, because a running
  // command's clock advances before any new event is written.
  function poll() {
    var token = pollToken;
    // The cabinet and the board change only when an event lands, which
    // reloads them through loadAll; nothing in them ticks between events.
    var watching = sel && sel.kind !== "cab" && sel.kind !== "board";
    api("/api/events?since=" + enc(lastEventID) + "&wait=" + (watching ? "5" : "20")).then(function (body) {
      if (token !== pollToken) return;
      timelineStale = staleTimelines(body.timeline_stale);
      var staleMoved = JSON.stringify(body.timeline_stale || []) !== JSON.stringify(staleList);
      staleList = body.timeline_stale || [];
      var serverID = body.last_event_id || 0;
      var moved = (body.events && body.events.length) || (body.now && body.now.length);
      if (serverID < lastEventID || moved) {
        // A smaller id is a `mate reindex` under us (docs/dashboard.md 10):
        // the old cursor will never come round again.
        lastEventID = serverID < lastEventID ? serverID : Math.max(lastEventID, serverID);
        loadAll().then(function () { if (token === pollToken) poll(); });
        return;
      }
      markLive();
      if (watching) {
        loadDetail().then(function () { if (token === pollToken) { draw(); poll(); } });
        return;
      }
      // News is a file the REPL writes, not an event: a quiet poll reads
      // it again, and redraws only when it moved.
      loadNews().then(function (changed) {
        if (token !== pollToken) return;
        if (changed || staleMoved) draw(); else drawLive();
        poll();
      });
    }, function (err) {
      if (token !== pollToken) return;
      markStale(err.reason || err.message);
      setTimeout(function () { if (token === pollToken) poll(); }, 3000);
    });
  }

  // ------------------------------------------------------------ the model

  function projectNames() { return ws ? (ws.projects || []).map(function (p) { return p.name; }) : []; }

  function card(name) {
    var list = ws ? ws.projects || [] : [];
    for (var i = 0; i < list.length; i++) if (list[i].name === name) return list[i];
    return null;
  }

  function body(name) {
    var b = projects[name];
    return b && !b.__error ? b : null;
  }

  function sceneRow(actorID) { return now ? now[actorID] || null : null; }

  function tasksOf(name) { var b = body(name); return b ? b.tasks || [] : []; }
  function openTasks(name) { return tasksOf(name).filter(function (t) { return !t.closed; }); }
  function closedTasks(name) {
    return tasksOf(name).filter(function (t) { return t.closed; }).sort(function (a, b) {
      return (parseAt(closedAt(b)) || 0) - (parseAt(closedAt(a)) || 0);
    });
  }
  function closedAt(t) { return t.closed_at || t.merged_at || ""; }

  function findTask(name, crew) {
    var list = tasksOf(name);
    for (var i = 0; i < list.length; i++) if (list[i].crew === crew) return list[i];
    return null;
  }

  // The Mate as the room needs it: the project page's block when it loaded,
  // else the workspace card's thinner one.
  function mateOf(name) {
    var b = body(name);
    var c = card(name) || {};
    var m = b ? b.mate : c.mate || {};
    return m || {};
  }

  function crewTokensToday(name, crew) {
    var row = sceneRow("crew:" + name + ":" + crew);
    return row ? row.tokens_today : null;
  }

  // projectTokens is the total line of `mate usage`: the Mate's own turns
  // plus every task this project has recorded, open and closed.
  // tokens.total already leaves thinking out. Unknown when the project
  // page has not been read, or when any part of the sum is missing, so a
  // room never shows a partial total as if it were the whole.
  function projectTokens(name) {
    var b = body(name);
    if (!b || !b.mate || !b.mate.tokens || b.mate.tokens.total == null) return null;
    if (!Array.isArray(b.tasks)) return null;
    var sum = b.mate.tokens.total;
    for (var i = 0; i < b.tasks.length; i++) {
      var t = b.tasks[i].tokens;
      if (!t || t.total == null) return null;
      sum += t.total;
    }
    return sum;
  }

  function spentLabel(name) {
    return el("span", {
      class: "ptok",
      title: "tokens this project has consumed: the Mate plus every task (the total line of mate usage)"
    }, [el("b", { text: tok(projectTokens(name)) }), " tokens"]);
  }

  // A reviewer's conclusion, from the Verdict line its report ends with.
  var REVIEW_FINAL = {
    pass: { word: "approved", tone: "t-ok", icon: "merged", why: "the review passed" },
    "changes-requested": { word: "changes requested", tone: "t-wait", icon: "edit", why: "the review asked for changes" },
    incomplete: { word: "review incomplete", tone: "t-gone", icon: "review", why: "the reviewer could not check everything" }
  };

  // A cabinet row's final state, in the reader's words rather than the
  // crew state machine's. A `failed` crew whose meta says why is a start
  // that never came up, the one row that reads red. A reviewer says what it
  // concluded, however it was closed. Otherwise: merged when the timeline
  // saw the merge, and a `failed` crew is work closed with --discard - a
  // decision, not a fault.
  function finalState(t) {
    if (t.close_state === "failed" && t.failed_reason) return { word: "didn't start", tone: "t-bad", icon: "failed", why: t.failed_reason };
    if (t.review) {
      return REVIEW_FINAL[t.review] || { word: t.review === "pending" ? "no verdict" : String(t.review).replace(/-/g, " "),
        tone: "t-gone", icon: "unknown", why: t.review === "pending" ? "the review closed without a Verdict" : "the review's own Verdict" };
    }
    if (t.merged_at) return { word: "merged", tone: "t-ok", icon: "merge", why: "the work landed" };
    if (t.close_state === "failed") return { word: "discarded", tone: "t-gone", icon: "gone", why: "closed without landing its work" };
    if (t.close_state === "reviewed") return { word: "reviewed", tone: "t-ok", icon: "review", why: "the review was delivered" };
    if (t.close_state) return { word: String(t.close_state).replace(/_/g, " "), tone: "t-gone", icon: "done" };
    return { word: "closed", tone: "t-gone", icon: "done" };
  }

  // What a task was for (Task.role): a change to a repo, an investigation
  // that leaves a report, or a review of another task. A row older than
  // the field reads as unknown rather than as a ship.
  var ROLES = {
    ship: { word: "coding", icon: "ship", why: "a change to the repo" },
    scout: { word: "scout", icon: "scout", why: "an investigation; it leaves a report and commits nothing" },
    review: { word: "review", icon: "reviewer", why: "a review of another task" }
  };

  function roleMark(t) {
    var r = ROLES[t.role] || { word: "role unknown", icon: "unknown", why: "" };
    var label = r.word + (t.role === "review" && t.review_of ? " of " + t.review_of : r.why ? ": " + r.why : "");
    return el("span", { class: "rl tip rl-" + (ROLES[t.role] ? t.role : "unknown"), "data-tip": label, role: "img", "aria-label": label }, icon(r.icon));
  }

  // The kinds of agent a crew delegated to (Task.agents, CON-124): a badge
  // each, in the order they were first used, worn beside the crew wherever
  // it is drawn. The kind is the project's own name for the agent
  // (backend-reviewer, ios-reviewer) or a harness built-in (Explore), shown
  // as written: the page does not know which agents a project defines. A
  // crew that delegated to none wears none.
  function agentBadges(t, extra) {
    var list = t && t.agents ? t.agents : [];
    if (!list.length) return null;
    return el("span", { class: "ags" + (extra ? " " + extra : ""), role: "list", "aria-label": "Agents delegated to" }, list.map(function (name) {
      var label = "delegated to the " + name + " agent";
      return el("span", { class: "ag tip", role: "listitem", "data-tip": label, "aria-label": label, text: String(name) });
    }));
  }

  // A cabinet row's final state is its glyph alone: the column is narrow
  // and "changes requested" does not fit it. Every final state has its own
  // glyph, the summary above the table names each one, and the word and why
  // are the chip's tooltip and accessible name.
  function finalChip(f) {
    var label = f.word + (f.why ? ": " + f.why : "");
    return el("span", { class: "chip ico tip " + f.tone, "data-tip": label, role: "img", "aria-label": label }, icon(f.icon));
  }

  // A task's tokens with its reviewers' (tokens_with_reviews, CON-75), and
  // how many reviews that counts; a task without reviews reads as before.
  function tokensWithReviews(t) {
    var own = t.tokens ? t.tokens.total : null;
    var n = t.reviews ? t.reviews.length : 0;
    if (!n || t.tokens_with_reviews == null) return tok(own) + " tokens";
    return tok(t.tokens_with_reviews) + " tokens incl. " + n + (n === 1 ? " review" : " reviews");
  }

  function isAway(state) { return state === "walking_to_ceo" || state === "waiting_at_ceo"; }

  function waitingCount(name) {
    return openTasks(name).filter(function (t) { return t.state === "waiting_at_ceo" || t.state === "waiting_review"; }).length;
  }

  function mobile() {
    try { return !!(window.matchMedia && window.matchMedia("(max-width: 759px)").matches); } catch (e) { return false; }
  }

  // ------------------------------------------------------------ theme

  function storedTheme() {
    try { return window.localStorage.getItem("mate-office-theme") || ""; } catch (e) { return ""; }
  }

  function applyTheme(t) {
    var html = document.documentElement;
    if (t === "dark" || t === "light") html.setAttribute("data-theme", t);
    else html.removeAttribute("data-theme");
  }

  function isDark() {
    var t = document.documentElement.getAttribute("data-theme");
    if (t) return t === "dark";
    try { return !!(window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches); } catch (e) { return false; }
  }

  function toggleTheme() {
    var next = isDark() ? "light" : "dark";
    applyTheme(next);
    try { window.localStorage.setItem("mate-office-theme", next); } catch (e) { /* a private window keeps the choice for this page only */ }
    draw();
  }

  // ------------------------------------------------------------ the top bar

  function liveNode() {
    if (live.state === "stale") {
      return el("div", { class: "live stale", id: "live", role: "status", "aria-live": "polite", title: live.reason }, [
        icon("blocked"), "stale · ", el("span", { class: "rs", text: live.reason })
      ]);
    }
    if (live.state === "loading") {
      return el("div", { class: "live loading", id: "live", role: "status" }, [el("i", { class: "dot" }), "loading"]);
    }
    return el("div", { class: "live", id: "live", role: "status", "aria-live": "polite" }, [el("i", { class: "dot" }), "live · " + clock(live.at)]);
  }

  function drawLive() {
    var old = root.querySelector("#live");
    if (old && old.parentNode) old.parentNode.replaceChild(liveNode(), old);
  }

  // Tokens today across the workspace: every Mate's and crew's
  // `v_now.tokens_today`. Unknown when the scene could not be read.
  function tokensToday() {
    if (!now) return null;
    var sum = 0;
    Object.keys(now).forEach(function (id) {
      var r = now[id];
      if (r.actor_kind === "crew" || r.actor_kind === "mate") sum += r.tokens_today || 0;
    });
    return sum;
  }

  function adminHref() {
    if (!sel || !sel.project) return "../";
    if (sel.kind === "crew") return "../#/p/" + enc(sel.project) + "/t/" + enc(sel.crew);
    if (sel.kind === "mate") return "../#/p/" + enc(sel.project) + "/mate";
    return "../#/p/" + enc(sel.project);
  }

  function segNav() {
    return el("nav", { class: "seg", "aria-label": "View" }, [
      el("a", { class: "on", href: "./", "aria-current": "page", text: "Office" }),
      el("a", { href: "../city/", text: "City", title: "the same workspace drawn as a 3D city" }),
      el("a", { href: adminHref(), text: "Admin", title: "the admin dashboard" })
    ]);
  }

  function themeButton() {
    var dark = isDark();
    var b = el("button", { class: "ib", type: "button", "aria-label": dark ? "Switch to light theme" : "Switch to dark theme", "data-focus": "theme" },
      icon(dark ? "sun" : "moon"));
    b.addEventListener("click", toggleTheme);
    return b;
  }

  function wsName() {
    if (!ws || !ws.root) return "?";
    var parts = String(ws.root).split(/[\\/]/).filter(Boolean);
    return parts.length ? parts[parts.length - 1] : String(ws.root);
  }

  function topBar() {
    var today = tokensToday();
    if (mobile()) {
      return [
        el("header", { class: "mtop" }, [
          el("span", { class: "mark" }, icon("office")),
          el("b", { class: "mname", text: "Mate Office" }),
          el("span", { class: "grow" }),
          liveNode(),
          themeButton()
        ]),
        el("div", { class: "msub" }, [
          segNav(),
          matesButton(true),
          el("span", { class: "kpi" }, ["tokens today", el("b", { text: tok(today) })])
        ])
      ].concat(bands());
    }
    return [el("header", { class: "top" }, [
      el("div", { class: "brand" }, [el("span", { class: "mark" }, icon("office")), "Mate Office"]),
      el("div", { class: "ws", title: ws ? ws.root : "" }, ["workspace ", el("b", { text: wsName() })]),
      liveNode(),
      el("div", { class: "grow" }),
      el("div", { class: "kpi", title: "tokens spent today by every Mate and crew (v_now.tokens_today)" }, ["Today ", el("b", { text: tok(today) }), " tokens"]),
      matesButton(false),
      segNav(),
      themeButton()
    ])].concat(bands());
  }

  // ------------------------------------------------------------ the hallway

  function legend() {
    return el("div", { class: "board", role: "group", "aria-label": "Legend" }, [
      el("div", { class: "bh" }, [
        el("b", { text: "Crew states" }),
        el("span", { class: "hkey" }, ["face =",
          face("hk", harnessInfo("claude")), "Claude",
          face("hk", harnessInfo("codex")), "Codex",
          face("hk", harnessInfo("pi")), "Pi"])
      ]),
      el("div", { class: "lg" }, LEGEND.map(function (k) {
        var s = CREW_STATES[k];
        return el("span", { class: "li" }, [icoChip(s), s.word]);
      }).concat([el("span", { class: "li" }, [el("span", { class: "selsw" }), "selected"])]))
    ]);
  }

  // inboxSummary reads every project's inbox: the things waiting on a
  // decision, which is the captain's tray.
  function inboxSummary() {
    var items = [];
    var known = true;
    projectNames().forEach(function (n) {
      var b = body(n);
      if (b) items = items.concat(b.inbox || []);
      else known = false;
    });
    var questions = items.filter(function (i) { return i.kind === "status" && (i.verb === "needs-decision" || i.verb === "blocked"); }).length;
    return { n: items.length, questions: questions, known: known };
  }

  // short is the phone's wording, as the design's bottom-sheet board has it.
  function inboxWords(s, short) {
    if (!s.known && s.n === 0) return "inbox ?";
    if (s.n === 0) return "inbox empty";
    if (s.questions === s.n) return plural(s.n, "question", "questions") + (short ? "" : " waiting");
    return s.n + (short ? " in the inbox" : " waiting in the inbox");
  }

  // newsSummary counts every Mate's news by what the captain owes it:
  // unread (new), read but not answered (waiting), answered and with the
  // Mate (working). known is false until /api/news has answered.
  function newsSummary() {
    var s = { known: false, unread: 0, waiting: 0, working: 0, closed: 0 };
    if (!news || !news.body) return s;
    s.known = true;
    (news.body.projects || []).forEach(function (p) {
      if (p.error) s.known = false;
      (p.news || []).forEach(function (n) {
        if (n.status === "new") s.unread++;
        else if (n.status === "waiting") s.waiting++;
        else if (n.status === "working") s.working++;
        else s.closed++;
      });
    });
    return s;
  }

  function newsWords(s) {
    if (!s.known && !s.unread && !s.waiting) return "news ?";
    if (s.unread) return plural(s.unread, "news", "news") + " unread";
    if (s.waiting) return plural(s.waiting, "news", "news") + " to answer";
    return "no news waiting";
  }

  function captainDesk() {
    var s = inboxSummary();
    var ns = newsSummary();
    var btn = el("button", { class: "cap" + (sel && sel.kind === "captain" ? " sel" : ""), type: "button",
      "aria-label": "Captain's desk: " + inboxWords(s) + ", " + newsWords(ns) }, [
      el("span", { class: "mart", "aria-hidden": "true" }, [
        el("span", { class: "m-chair" }), el("span", { class: "m-head" }), el("span", { class: "c-hat" }),
        el("span", { class: "m-body", style: "background:#46566B" }), el("span", { class: "m-desk" }),
        el("span", { class: "m-mon m1" }),
        el("span", { class: "tray" }, s.n > 0 ? el("b", { text: String(s.n) }) : null),
        el("span", { class: "m-plate", text: "Captain · you" })
      ]),
      el("span", { class: "capq" + (s.n > 0 ? "" : " none") }, [icon("ask"), inboxWords(s)]),
      el("span", { class: "capq capn" + (ns.unread || ns.waiting ? "" : " none") }, [icon("mail"), newsWords(ns)])
    ]);
    return pickable(btn, "captain");
  }

  function hallFigures() {
    var figs = [];
    projectNames().forEach(function (n) {
      openTasks(n).forEach(function (t) {
        if (isAway(t.state)) figs.push(figure(n, t));
      });
    });
    return el("div", { class: "hfigs" }, figs.length ? figs : [el("span", { class: "hnote", text: "Nobody at a Mate's door" })]);
  }

  function figure(project, t) {
    var key = "crew/" + enc(project) + "/" + enc(t.crew);
    var s = crewState(t.state);
    var h = harnessInfo(t.harness);
    var asks = t.state === "waiting_at_ceo";
    var fig = el("button", {
      class: "fig" + (asks ? "" : " walk") + (selKey(sel) === key ? " sel" : ""), type: "button",
      "aria-label": t.crew + " of " + project + ", " + s.word, "data-room": project
    }, [
      asks ? el("span", { class: "bubble" }, [icon("ask"), s.word]) : null,
      el("span", { class: "fart", "aria-hidden": "true" }, [
        el("span", { class: "f-shadow" }), el("span", { class: "f-leg l1" }), el("span", { class: "f-leg l2" }),
        el("span", { class: "f-body", style: "background:" + shirt(t.crew) }),
        face("f-head", h),
        asks ? null : el("span", { class: "f-motion" }, [el("i"), el("i"), el("i")])
      ]),
      el("span", { class: "lbl" }, [el("span", { class: "id", text: t.crew }), el("span", { class: "tok", text: age(t.since) })]),
      asks ? null : chip(s),
      agentBadges(t)
    ]);
    return pickable(fig, key);
  }

  // ------------------------------------------------------------ rooms

  // States in which an agent is doing something rather than waiting on
  // someone or on nothing.
  var MATE_RESTING = { idle: true, asleep: true, blocked: true, gone: true, "": true };
  var CREW_WORKING = { arriving: true, at_desk_working: true, walking_to_ceo: true, leaving: true };

  // The light on a room's door plate: the project, not just its Mate. It is
  // "running" while the Mate or any open crew is at work, and otherwise says
  // how long since anyone in the room last moved, so a project left alone
  // for days does not light up like one that is busy.
  function roomLight(running, m, open) {
    if (!running) return el("span", { class: "light off" }, [el("i"), "stopped"]);
    var busy = !MATE_RESTING[m.state || ""] || open.some(function (t) { return CREW_WORKING[t.state]; });
    var last = [m.since].concat(open.map(function (t) { return t.since; }))
      .map(parseAt).filter(function (t) { return t != null; });
    if (busy || !last.length) return el("span", { class: "light" }, [el("i"), "running"]);
    var at = Math.max.apply(null, last);
    return el("span", { class: "light idle", title: "nobody has moved since " + new Date(at).toISOString() },
      [el("i"), "idle " + formatAge(Date.now() - at)]);
  }

  function roomCols(n) { return n <= 1 ? 1 : n <= 6 ? 2 : 3; }

  function room(name, index) {
    var c = card(name) || {};
    var m = mateOf(name);
    var b = projects[name];
    var open = openTasks(name);
    var closed = closedTasks(name);
    var running = !!(c.mate && c.mate.running);
    var cols = roomCols(open.length);
    var lowCab = cols === 1;
    var wait = waitingCount(name);
    if (matesOnly) {
      var box = inboxCount(name);
      return el("section", { class: "room compact r" + (index % 3) + (running ? "" : " r-stopped"), "aria-label": name + " room", "data-room": name }, [
        el("div", { class: "door", "data-door": name }, [el("i", { class: "dl" }), el("i", { class: "dr2" })]),
        el("div", { class: "plate" }, [el("b", { text: name }), roomLight(running, m, open), spentLabel(name),
          wait > 0 ? el("span", { class: "wb" }, [icon("ask"), wait + " waiting"]) : null]),
        staleNote(name),
        mateDesk(name, m, running),
        el("p", { class: "mates-line", text: plural(open.length, "open crew", "open crews") + " · " + (box.n == null ? "?" : box.n) + " in the inbox" })
      ]);
    }
    var node = el("section", {
      class: "room r" + (index % 3) + (running ? "" : " r-stopped") + " has-cab" + (lowCab ? " low-cab" : ""),
      "aria-label": name + " room", "data-room": name,
      style: "--basis:" + (cols === 1 ? 300 : cols === 2 ? 440 : 620) + "px;--cols:" + cols
    }, [
      el("div", { class: "door", "data-door": name }, [el("i", { class: "dl" }), el("i", { class: "dr2" })]),
      el("div", { class: "plate" }, [
        el("b", { text: name }),
        roomLight(running, m, open),
        spentLabel(name),
        wait > 0 ? el("span", { class: "wb" }, [icon("ask"), wait + " waiting"]) : null
      ]),
      mailbox(name, false),
      lowCab ? null : cabinet(name, closed, false),
      staleNote(name),
      mateDesk(name, m, running),
      b && b.__error ? el("p", { class: "room-err", text: "This room could not be read: " + (b.__error.reason || b.__error.message) })
        : open.length ? el("div", { class: "crews" }, open.map(function (t) { return desk(name, t); }))
          : el("p", { class: "empty-room", text: b ? "No open crews" : "loading…" }),
      running ? null : el("div", { class: "off" }, [icon("idle"), "Lights off · project stopped"]),
      lowCab ? cabinet(name, closed, true) : null
    ]);
    return node;
  }

  function mateDesk(name, m, running) {
    var key = "mate/" + enc(name);
    var s = mateState(m.state);
    var h = harnessInfo(m.harness);
    var present = running && m.state !== "gone";
    var btn = el("button", {
      class: "mate" + (present ? "" : " dim") + (selKey(sel) === key ? " sel" : ""), type: "button",
      "aria-label": "Mate of " + name + ", " + s.word
    }, [
      el("span", { class: "mart", "aria-hidden": "true" }, [
        el("span", { class: "m-chair" }),
        present ? face("m-head", h) : el("span", { class: "m-ghost" }),
        present ? el("span", { class: "m-body", style: "background:" + shirt("mate:" + name) }) : null,
        el("span", { class: "m-desk" }), el("span", { class: "m-mon m1" }), el("span", { class: "m-mon m2" }),
        el("span", { class: "m-plate", text: "Mate · " + name }),
        present && m.state === "deciding" ? el("span", { class: "think" }, [el("i"), el("i"), el("i")]) : null
      ]),
      el("span", { class: "mmeta" }, [chip(s), htag(h), el("span", { class: "tok", text: tok(m.tokens_today) + " today" })])
    ]);
    return pickable(btn, key);
  }

  function desk(project, t) {
    var key = "crew/" + enc(project) + "/" + enc(t.crew);
    var st = t.state || "";
    var s = crewState(st);
    var h = harnessInfo(t.harness);
    var away = isAway(st) || st === "arriving" || st === "leaving";
    var seated = st === "at_desk_working" || st === "idle" || st === "blocked" || st === "waiting_review" || st === "";
    var art = [el("span", { class: "a-chair" })];
    if (st === "asleep") {
      art.push(el("span", { class: "a-body slump", style: "background:" + shirt(t.crew) }));
    } else if (seated) {
      art.push(face("a-head", h), el("span", { class: "a-body", style: "background:" + shirt(t.crew) }));
    } else if (st === "gone") {
      art.push(el("span", { class: "a-ghost" }));
    }
    art.push(el("span", { class: "a-desk" }), el("span", { class: "a-paper" }));
    var lapOpen = st === "at_desk_working" || st === "idle" || st === "blocked" || isAway(st) || st === "";
    art.push(el("span", { class: lapOpen ? "a-lap" : "a-lapc" }));
    if (st !== "asleep" && st !== "gone" && st !== "arriving" && st !== "leaving") art.push(el("span", { class: "a-mug" }));
    if (st === "at_desk_working" || st === "idle" || st === "blocked") art.push(el("span", { class: "a-hand h1" }), el("span", { class: "a-hand h2" }));
    if (st === "waiting_review") {
      art.push(el("span", { class: "a-doc" }, [el("i"), el("i"), el("i"), el("i")]), el("span", { class: "a-hand h3" }), el("span", { class: "a-hand h4" }));
    }
    if (st === "asleep") {
      art.push(el("span", { class: "a-arms", style: "background:" + shirt(t.crew) }), face("a-head", h, "sleep"),
        el("span", { class: "a-zz" }, [el("i", { text: "z" }), el("i", { text: "z" }), el("i", { text: "Z" })]));
    }
    if (st === "blocked") art.push(el("span", { class: "a-alert" }, icon("bang")));
    if (away) art.push(el("span", { class: "a-note" }, icon(st === "arriving" ? "arriving" : st === "leaving" ? "leaving" : st === "walking_to_ceo" ? "walking" : "ask")));

    var btn = el("button", {
      class: "desk s-" + (st || "unknown") + (st === "gone" ? " dim" : "") + (isAway(st) ? " away" : "") + (selKey(sel) === key && !isAway(st) ? " sel" : ""),
      type: "button", "aria-label": t.crew + ", " + s.word + (st === "blocked" && t.detail ? ", " + detailWord(t.detail) : "")
    }, [
      el("span", { class: "art", "aria-hidden": "true" }, art),
      el("span", { class: "lbl" }, [el("span", { class: "id", text: t.crew }), el("span", { class: "tok", text: tok(t.tokens ? t.tokens.total : null) })]),
      chip(s),
      agentBadges(t),
      st === "blocked" ? el("span", { class: "quiet", text: (detailWord(t.detail) || "cannot be reached") + " · " + age(t.since) }) : null
    ]);
    return pickable(btn, key);
  }

  function cabinet(name, closed, low) {
    var key = "cabinet/" + enc(name);
    var on = selKey(sel) === key;
    var peek = el("span", { class: "peek" }, [el("span", { class: "pk-h", text: "Latest in the cabinet" })].concat(
      closed.length ? closed.slice(0, 3).map(function (t) {
        var f = finalState(t);
        return el("span", { class: "pk" }, [
          el("span", { class: "tk" }, [roleMark(t), el("span", { class: "tn", text: t.crew }), agentBadges(t)]),
          el("span", { class: "pk-s " + f.tone, text: f.word }),
          el("span", { class: "tok", text: age(closedAt(t)) + " ago · " + tokensWithReviews(t) })
        ]);
      }) : [el("span", { class: "pk-empty", text: "Nothing finished yet" })]));
    var btn = el("button", {
      class: "cab" + (low ? " low" : "") + (on ? " open sel" : ""), type: "button",
      "aria-label": "Filing cabinet, " + plural(closed.length, "finished task", "finished tasks")
    }, [
      el("span", { class: "cbody", "aria-hidden": "true" }, [el("span", { class: "dr" }, el("i")), el("span", { class: "dr" }, el("i")), el("span", { class: "dr" }, el("i"))]),
      el("span", { class: "cnt", text: String(closed.length) }),
      el("span", { class: "clbl", text: "finished" }),
      peek
    ]);
    return pickable(btn, key);
  }

  // ------------------------------------------------------------ phone

  function tile(project, t, standing) {
    var st = t.state || "";
    var s = crewState(st);
    var h = harnessInfo(t.harness);
    var parts = [];
    var seated = st === "at_desk_working" || st === "idle" || st === "blocked" || st === "waiting_review" || st === "asleep" || st === "";
    if (standing) {
      parts.push(el("span", { class: "s-legs" }), el("span", { class: "t-b", style: "background:" + shirt(t.crew) }), face("t-h", h));
    } else if (st === "gone") {
      parts.push(el("span", { class: "t-ghost" }));
    } else if (seated) {
      parts.push(el("span", { class: "t-b", style: "background:" + shirt(t.crew) }), face("t-h", h));
      if (st === "at_desk_working" || st === "idle" || st === "blocked") parts.push(el("span", { class: "t-lap" }));
      if (st === "waiting_review") parts.push(el("span", { class: "t-doc" }));
      if (st === "asleep") parts.push(el("span", { class: "t-z", text: "zZ" }));
    } else {
      parts.push(el("span", { class: "t-seat" }));
    }
    parts.push(el("span", { class: "t-st " + s.tone }, icon(s.icon)));
    var key = "crew/" + enc(project) + "/" + enc(t.crew);
    var cls = "mc" + (st === "at_desk_working" ? " p-working" : "") + (st === "asleep" ? " p-sleep" : "") +
      (st === "blocked" ? " p-blocked" : "") + (st === "gone" ? " dimt" : "") + (standing && st === "walking_to_ceo" ? " walk" : "") +
      (selKey(sel) === key ? " sel" : "");
    var btn = el("button", { class: cls, type: "button", style: "--rb2:" + (standing ? "var(--hall)" : "var(--rb)"),
      "aria-label": t.crew + ", " + s.word }, [
      el("span", { class: "tile" + (standing ? " tstand" : ""), "aria-hidden": "true" }, parts),
      el("span", { class: "id", text: t.crew }),
      el("span", { class: "mw " + s.tone, text: s.word }),
      agentBadges(t)
    ]);
    return pickable(btn, key);
  }

  function mobileHall() {
    var s = inboxSummary();
    var ns = newsSummary();
    var figs = [];
    projectNames().forEach(function (n) {
      openTasks(n).forEach(function (t) { if (isAway(t.state)) figs.push(tile(n, t, true)); });
    });
    return el("section", { class: "mhall", "aria-label": "Hallway" }, [
      pickable(el("button", { class: "mcap" + (sel && sel.kind === "captain" ? " sel" : ""), type: "button",
        "aria-label": "Captain's desk: " + inboxWords(s) + ", " + newsWords(ns) }, [
        el("span", { class: "cav", "aria-hidden": "true" }, [el("span", { class: "c-b" }), el("span", { class: "c-h" }), el("span", { class: "c-hat2" })]),
        el("span", { class: "mct" }, [el("b", { text: "Captain · you" }),
          el("span", { class: "capq" + (s.n > 0 ? "" : " none") }, [icon("ask"), inboxWords(s, true)]),
          el("span", { class: "capq capn" + (ns.unread || ns.waiting ? "" : " none") }, [icon("mail"), newsWords(ns)])])
      ]), "captain"),
      figs.length ? el("div", { class: "mfigs" }, figs) : null,
      boardButton(true)
    ]);
  }

  function mobileRoom(name, index) {
    var c = card(name) || {};
    var m = mateOf(name);
    var b = projects[name];
    var running = !!(c.mate && c.mate.running);
    var open = openTasks(name);
    var closed = closedTasks(name);
    var wait = waitingCount(name);
    var ms = mateState(m.state);
    var h = harnessInfo(m.harness);
    var present = running && m.state !== "gone";
    var mkey = "mate/" + enc(name);
    var cab = pickable(el("button", { class: "mcab" + (selKey(sel) === "cabinet/" + enc(name) ? " sel" : ""), type: "button",
      "aria-label": "Filing cabinet, " + plural(closed.length, "finished task", "finished tasks") },
    [icon("cabinet"), String(closed.length), el("small", { text: "done" })]), "cabinet/" + enc(name));
    var mate = pickable(el("button", { class: "mr-mate" + (selKey(sel) === mkey ? " sel" : ""), type: "button",
      "aria-label": "Mate of " + name + ", " + ms.word, style: "--rb2:var(--surface)" }, [
      el("span", { class: "tile lead", "aria-hidden": "true" }, present
        ? [el("span", { class: "t-b", style: "background:" + shirt("mate:" + name) }), face("t-h", h)]
        : [el("span", { class: "t-ghost" })]),
      el("span", { class: "mm-t" }, [el("span", { class: "mm-1" }, [el("b", { text: "Mate" }), htag(h)]), chip(ms)]),
      el("span", { class: "mm-v" }, [el("b", { text: tok(m.tokens_today) }), "today"])
    ]), mkey);
    return el("section", { class: "mroom r" + (index % 3) + (running ? "" : " r-stopped"), "aria-label": name + " room" }, [
      el("div", { class: "mr-h" }, [
        el("b", { text: name }),
        roomLight(running, m, open),
        spentLabel(name),
        wait > 0 ? el("span", { class: "wb" }, [icon("ask"), wait + " waiting"]) : null,
        el("span", { class: "grow" }),
        mailbox(name, true),
        cab
      ]),
      staleNote(name),
      mate,
      matesOnly ? el("p", { class: "m-empty", text: plural(open.length, "open crew", "open crews") }) : b && b.__error ? el("p", { class: "room-err", text: "This room could not be read: " + (b.__error.reason || b.__error.message) })
        : open.length ? el("div", { class: "mr-crews" }, open.map(function (t) { return tile(name, t, false); }))
          : el("p", { class: "m-empty", text: b ? "No open crews" : "loading…" })
    ]);
  }

  // ------------------------------------------------------------ the drawer

  // full, when given, adds the button that lets the drawer fill the page.
  function drawerHead(avatar, role, title, stateChip, since, full) {
    var close = el("button", { class: "ib", type: "button", "aria-label": "Close details", "data-focus": "drawer-close" }, icon("close"));
    close.addEventListener("click", function () { select(""); });
    return el("div", { class: "dh" }, [
      avatar,
      el("div", { class: "dt" }, [
        el("div", { class: "role" }, role),
        el("h2", { text: title }),
        el("div", { class: "dstate" }, [stateChip, since ? el("span", { class: "since", text: since }) : null])
      ]),
      el("div", { class: "dbtns" + (full ? " two" : "") }, [full || null, close])
    ]);
  }

  function fullToggle() {
    var label = mateFull ? "Back to the side" : "Full screen";
    var b = el("button", { class: "ib", type: "button", "aria-label": label, title: label, "aria-pressed": mateFull ? "true" : "false",
      "data-focus": "drawer-full" }, icon(mateFull ? "minimize" : "maximize"));
    b.addEventListener("click", function () {
      mateFull = !mateFull;
      try { window.localStorage.setItem("mate-office-mate-full", mateFull ? "1" : "0"); } catch (e) { /* this page only */ }
      draw();
    });
    return b;
  }

  function personAvatar(h, colour, present) {
    return el("span", { class: "dav", "aria-hidden": "true" }, present
      ? [face("d-head", h), el("span", { class: "d-body", style: "background:" + colour })]
      : [el("span", { class: "m-ghost", style: "left:11px;top:11px" })]);
  }

  // usageSection is the design's "Token usage now": the total, the cost,
  // the four buckets in their fixed order, the context window and today.
  // The admin page's token consumption for one task, in a new tab. The
  // Mate has no task page, so it gets no such link.
  function usageLink(name, crew, extra) {
    return el("a", { class: "link usage-link" + (extra ? " " + extra : ""), href: "../#/p/" + enc(name) + "/t/" + enc(crew) + "/usage",
      target: "_blank", rel: "noopener" }, [icon("usage"), "Usage details"]);
  }

  function usageSection(o) {
    var t = o.tokens || null;
    var total = t ? t.total : null;
    var names = [["input", "b-in", "input"], ["cache_read", "b-cr", "cache read"], ["cache_write", "b-cw", "cache write"], ["output", "b-out", "output"]];
    var segs = [];
    var rows = [];
    names.forEach(function (n) {
      var v = t ? t[n[0]] : null;
      var pct = v == null || !total ? "–" : v > 0 && v / total < 0.01 ? "<1%" : Math.round(100 * v / total) + "%";
      if (v > 0) segs.push(el("i", { class: n[1], style: "flex-grow:" + v, title: n[2] + " " + tok(v) }));
      rows.push(el("div", null, [el("i", { class: "sw " + n[1] }), n[2], el("b", { text: tok(v) }), el("em", { text: pct })]));
    });
    var stackLabel = names.map(function (n) { return n[2] + " " + tok(t ? t[n[0]] : null); }).join(", ");

    var pct = o.contextPct;
    var ctxTokens = o.contextTokens;
    var ctxOf;
    if (pct != null && ctxTokens != null && pct > 0) {
      ctxOf = tok(ctxTokens) + " of " + tok(Math.round(ctxTokens * 100 / pct));
    } else if (ctxTokens != null) {
      ctxOf = tok(ctxTokens) + " used · window unknown";
    } else {
      ctxOf = "window unknown";
    }
    var ctxKnown = pct != null;

    return el("section", { class: "ds", "aria-label": "Token usage now" }, [
      el("h3", { class: "sh" }, ["Token usage now ", el("span", { text: "updated " + clock(o.updated) })]),
      el("div", { class: "big" }, [
        el("span", { class: "n", text: tok(total) }),
        el("span", { class: "u", text: "tokens" }),
        el("span", { class: "c" }, [el("b", { text: cost(o.cost) }), el("span", { text: o.cost == null ? "cost not reported" : "estimated cost" })])
      ]),
      el("div", { class: "stack" + (segs.length ? "" : " none"), role: "img", "aria-label": stackLabel }, segs),
      el("div", { class: "bl" }, rows),
      t && t.thinking > 0 ? el("p", { class: "think-note", text: "thinking " + tok(t.thinking) + ", inside output already" }) : null,
      el("div", { class: "meter" }, [
        el("div", { class: "mh" }, [el("span", { text: "Context window" }),
          el("span", null, [el("b", { text: "context " + (ctxKnown ? Math.round(pct) + "%" : "?") }), " · " + ctxOf])]),
        el("div", { class: "bar" + (ctxKnown ? "" : " unk"), role: "img", "aria-label": "context used, " + (ctxKnown ? Math.round(pct) + "%" : "unknown") },
          ctxKnown ? el("i", { style: "width:" + Math.max(0, Math.min(100, pct)).toFixed(1) + "%" }) : null)
      ]),
      el("div", { class: "today" }, [
        el("div", { class: "tv" }, ["Tokens today", el("b", { text: tok(o.today) })]),
        el("div", null, [sparkNode(o.spark), el("div", { class: "sx" }, [el("span", { text: "6h ago" }), el("span", { text: "now" })])])
      ]),
      o.link || null
    ]);
  }

  // spark buckets dated token counts into twelve half hours ending now.
  // points is [{at, tokens}] or null while unknown.
  var SPARK_BINS = 12;
  var SPARK_BIN_MS = 30 * 60 * 1000;

  function sparkBins(points) {
    var end = Date.now();
    var start = end - SPARK_BINS * SPARK_BIN_MS;
    var bins = [];
    for (var i = 0; i < SPARK_BINS; i++) bins.push(0);
    points.forEach(function (p) {
      var t = parseAt(p.at);
      if (t == null || t < start || t > end) return;
      var i = Math.min(SPARK_BINS - 1, Math.floor((t - start) / SPARK_BIN_MS));
      bins[i] += p.tokens || 0;
    });
    return bins;
  }

  function sparkNode(spark) {
    if (!spark || spark.state === "loading") return el("div", { class: "spark unk", role: "img", "aria-label": "loading", text: "loading…" });
    if (spark.state === "error") return el("div", { class: "spark unk", role: "img", "aria-label": "unknown", text: "?" });
    var bins = sparkBins(spark.points);
    var max = Math.max.apply(null, bins);
    var label = max > 0 ? "Tokens per 30 minutes over the last 6 hours, " + spark.by : "No tokens recorded in the last 6 hours";
    return el("div", { class: "spark", role: "img", "aria-label": label, title: label }, bins.map(function (v) {
      return el("i", { style: "height:" + Math.max(4, max > 0 ? Math.round(100 * v / max) : 0) + "%", title: tok(v) });
    }));
  }

  function crewSpark() {
    if (!detail || detail.loading) return { state: "loading" };
    if (detail.error) return { state: "error" };
    return {
      by: "by model call",
      points: (detail.body.turns || []).map(function (u) { return { at: u.started_at, tokens: u.tokens ? u.tokens.total : 0 }; })
    };
  }

  // The Mate's calls are not in the API one by one, so its spark is built
  // from the prompt steps (internal/diagnostics' runs of consecutive calls
  // of one type), dated by when each step began.
  function mateSpark() {
    if (!detail || detail.loading) return { state: "loading" };
    if (detail.error) return { state: "error" };
    var points = [];
    (detail.body.exchanges || []).forEach(function (x) {
      ((x.overview && x.overview.steps) || []).forEach(function (s) {
        points.push({ at: s.started_at, tokens: s.tokens ? s.tokens.total : 0 });
      });
    });
    return { by: "by prompt step", points: points };
  }

  function mateDrawer(name) {
    var m = mateOf(name);
    var c = card(name) || {};
    var b = body(name);
    var running = !!(c.mate && c.mate.running);
    var s = mateState(m.state);
    var h = harnessInfo(m.harness);
    var present = running && m.state !== "gone";
    var head = drawerHead(personAvatar(h, shirt("mate:" + name), present),
      ["Mate · team lead", htag(h)], name, chip(s), m.since ? "since " + age(m.since) : null, fullToggle());
    var ex = exchangesOf(name);
    var top = [head, actionsSection(name, ""), mateTabs(name, ex.list ? ex.list.length : null)];
    if (mateTab === "talk") return top.concat([talkSection(name), composer(name)]);
    var nodes = top.concat([
      usageSection({
        tokens: b ? m.tokens : null, cost: b ? m.cost : null, updated: b ? b.generated_at : null,
        contextPct: m.context_pct, contextTokens: m.context_tokens, today: m.tokens_today, spark: mateSpark()
      })
    ]);
    var last = m.last_turn;
    nodes.push(el("section", { class: "ds", "aria-label": "Current activity" }, [
      el("h3", { class: "sh", text: "Current activity" }),
      el("p", { class: "stl", text: mateSentence(m) }),
      el("dl", { class: "kv" }, [
        el("dt", { text: "Facing" }), el("dd", { class: "mono", text: m.target || "nobody" }),
        el("dt", { text: "Last turn" }), el("dd", { text: last ? age(last.ended_at || last.started_at) + " ago" : (b ? "none recorded" : "?") }),
        el("dt", { text: "Mode" }), el("dd", { text: (b && b.mode) || c.mode || "?" })
      ])
    ]));
    nodes.push(el("section", { class: "ds", "aria-label": "Inbox daemon" }, [el("h3", { class: "sh", text: "Inbox daemon" }), daemonLine(name)]));
    var open = openTasks(name);
    var max = Math.max.apply(null, [1].concat(open.map(function (t) { return t.tokens ? t.tokens.total : 0; })));
    nodes.push(el("section", { class: "ds", "aria-label": "Crews in this room" }, [
      el("h3", { class: "sh" }, ["Crews in this room ", el("span", { text: "total tokens" })]),
      open.length ? el("div", null, open.map(function (t) {
        var cs = crewState(t.state);
        var v = t.tokens ? t.tokens.total : null;
        var row = el("button", { class: "cr", type: "button", "aria-label": t.crew + ", " + cs.word + ", " + tok(v) + " tokens" }, [
          icoChip(cs),
          el("span", null, [el("span", { class: "id", text: t.crew }), el("small", { text: cs.word })]),
          el("span", { class: "crbar", "aria-hidden": "true" }, el("i", { style: "width:" + Math.max(3, Math.round(100 * (v || 0) / max)) + "%" })),
          el("span", { class: "crv", text: tok(v) })
        ]);
        return pickable(row, "crew/" + enc(name) + "/" + enc(t.crew));
      })) : el("p", { class: "loading-note", text: b ? "No open crews" : "loading…" })
    ]));
    var appBlock = appsSection(name);
    if (appBlock) nodes.push(appBlock);
    return nodes;
  }

  // Apps: the project's plugins as plugins.yaml configures them
  // (GET /api/plugins). A status is about configuration only; whether the
  // Mate is logged in to an app is not known here, so the block says where
  // to look instead of guessing.
  var APP_STATES = {
    configured: { word: "configured", tone: "t-info", icon: "review" },
    "configuration error": { word: "configuration error", tone: "t-bad", icon: "blocked" },
    "harness unsupported": { word: "harness unsupported", tone: "t-wait", icon: "gone" }
  };

  function appsSection(name) {
    var a = apps && apps.key === "mate/" + enc(name) ? apps : null;
    if (!a) return null;
    if (a.error) {
      return el("section", { class: "ds", "aria-label": "Apps" },
        el("p", { class: "err-note", text: "Apps: ?", title: a.error.reason || a.error.message }));
    }
    var list = a.body.apps || [];
    if (!list.length && !a.body.error) return null;
    var nodes = [el("h3", { class: "sh", text: "Apps" })];
    if (!list.length) nodes.push(el("p", { class: "err-note", text: a.body.error }));
    if (list.length) {
      nodes.push(el("dl", { class: "kv apps" }, list.reduce(function (acc, x) {
        var st = APP_STATES[x.status] || { word: x.status || "unknown", tone: "t-gone", icon: "unknown" };
        var url = x.open && x.open.url;
        var open = x.status === "configured" && url && /^https?:\/\//i.test(url)
          ? el("a", { class: "link", href: url, target: "_blank", rel: "noopener", title: x.open.title || null, "aria-label": x.open.title || "Open " + x.name, style: "margin:0 0 0 8px" }, ["Open", icon("arrowR")])
          : null;
        return acc.concat([
          el("dt", { text: x.name, title: x.instance }),
          el("dd", null, [chip(st), open, x.error ? el("p", { class: "err-note", text: x.error }) : null])
        ]);
      }, [])));
    }
    if (list.some(function (x) { return x.mcp; })) {
      nodes.push(el("p", { class: "loading-note", text: "Login and tool status: check /mcp in the Mate's Claude pane" }));
    }
    return el("section", { class: "ds", "aria-label": "Apps" }, nodes);
  }

  function lastOf(list) { return list && list.length ? list[list.length - 1] : null; }

  // ------------------------------------------------------------ actions

  // Every drawer that can act shows one fixed set of slots, in one order:
  // the console's buttons where GET .../actions offers them (CON-141), and
  // the redesign's other buttons, which the web cannot run yet, drawn
  // dimmed with the reason under them. A button never moves and never
  // disappears; the console's key is on it as a hint.
  var NOT_WIRED = "not wired yet";
  var ACT_KEYS = { start: "s", onboard: "s", resume: "s", stop: "x", restart_mate: "R", mode: "m", clear_composer: "C",
    explain: "e", copy: "y", remove_project: "X", restart_crew: "R", merge: "M", repair: "p", diff: "d", peek: "↵", zed: "z" };
  var ACT_ICONS = { start: "play", onboard: "play", resume: "play", stop: "stopsq", restart_mate: "restart", mode: "mode",
    clear_composer: "eraser", explain: "think", copy: "copy", remove_project: "trash", restart_crew: "restart", merge: "merge",
    repair: "wrench", diff: "diff", peek: "read", zed: "code" };
  var ACT_DANGER = { stop: true, remove_project: true };

  function serverButton(list, action, label) {
    for (var i = 0; i < list.length; i++) if (list[i].action === action) return list[i];
    return { action: action, label: label, available: false, reason: "?" };
  }

  function mateSlots(list) {
    var start = list[0] && /^(start|onboard|resume)$/.test(list[0].action) ? list[0] : serverButton(list, "start", "Start mate");
    var mode = null;
    list.forEach(function (b) { if (b.action === "mode") mode = b; });
    return [
      start,
      serverButton(list, "stop", "Stop mate…"),
      serverButton(list, "restart_mate", "Restart mate…"),
      mode || { action: "mode", label: "Mode", available: false, reason: "?" },
      serverButton(list, "clear_composer", "Clear composer"),
      { action: "explain", label: "Explain notice (Jev)", available: false, reason: NOT_WIRED },
      { action: "copy", label: "Copy agent id", available: false, reason: "no agent id in the API yet" },
      { action: "remove_project", label: "Remove project…", available: false, reason: NOT_WIRED }
    ];
  }

  function crewSlots(list) {
    return [
      serverButton(list, "stop", "Stop crew…"),
      serverButton(list, "restart_crew", "Restart crew…"),
      serverButton(list, "merge", "Merge…"),
      { action: "repair", label: "Repair binding…", available: false, reason: NOT_WIRED },
      serverButton(list, "diff", "Diff"),
      { action: "peek", label: "Peek", available: false, reason: NOT_WIRED },
      { action: "zed", label: "Open in Zed", available: false, reason: NOT_WIRED },
      { action: "copy", label: "Copy crew id", available: true }
    ];
  }

  function actionsSection(name, crew) {
    var key = selKey(sel);
    var a = acts && acts.key === key ? acts : null;
    var busy = running[key];
    var nodes = [el("h3", { class: "sh" }, ["Actions", busy ? el("span", { text: "one at a time" }) : null])];
    if (!a) nodes.push(el("p", { class: "loading-note", text: "loading…" }));
    else if (a.error) nodes.push(el("p", { class: "err-note", text: "Actions: ?", title: a.error.reason || a.error.message }));
    else if (a.body.read_only) nodes.push(roLine(a.body.read_only));
    else {
      var list = crew ? (a.body.crews || {})[crew] : a.body.mate;
      if (!list) nodes.push(el("p", { class: "loading-note", text: "This crew is not open in the console; there is nothing to act on." }));
      else {
        var slots = crew ? crewSlots(list) : mateSlots(list);
        nodes.push(el("div", { class: "acts", role: "group", "aria-label": "Actions" }, slots.map(function (btn, i) {
          return actionSlot(name, crew, key, btn, i);
        })));
      }
    }
    if (outcomes[key]) nodes.push(resultLine(outcomes[key]));
    if (live.state === "stale" && a && a.body && !a.body.read_only) {
      nodes.push(el("p", { class: "stale-line" }, [icon("clock"), "This page is stale. Each action re-reads first and refuses if things moved."]));
    }
    if (crew && diffView && diffView.key === key) nodes.push(diffBlock(diffView));
    return el("section", { class: "ds", "aria-label": "Actions" }, nodes);
  }

  function actionSlot(name, crew, key, btn, i) {
    var busy = running[key];
    var isRun = !!busy && busy.action === btn.action;
    var disabled = !btn.available || !!busy;
    var why = !btn.available ? (btn.reason || "?") : busy && !isRun ? "waiting on " + busy.label : "";
    var label = isRun ? "Running " + btn.label.replace(/…$/, "") + "…" : btn.label;
    var whyId = "why-" + i;
    var b = el("button", {
      class: "act" + (ACT_DANGER[btn.action] ? " danger" : "") + (isRun ? " running" : ""), type: "button",
      disabled: disabled, "aria-busy": isRun ? "true" : null, "aria-describedby": why ? whyId : null,
      "data-focus": "act:" + btn.action
    }, [
      el("span", { class: "spin", "aria-hidden": "true" }),
      icon(ACT_ICONS[btn.action] || "arrowR"),
      el("span", { class: "l", text: label }),
      ACT_KEYS[btn.action] ? el("kbd", { class: "k", "aria-hidden": "true", text: ACT_KEYS[btn.action] }) : null
    ]);
    b.addEventListener("click", function () {
      if (disabled) return;
      if (btn.action === "diff") { toggleDiff(name, crew, key); return; }
      if (btn.action === "copy") { copyText(crew || name, key); return; }
      if (btn.confirm) {
        asking = { key: key, name: name, crew: crew, button: btn };
        delete outcomes[key];
        draw();
        focusOn("act-cancel");
        return;
      }
      runAction(name, crew, key, btn);
    });
    return el("div", { class: "act-slot" }, [b, el("small", { class: "why", id: whyId, text: why })]);
  }

  // One line per action, and three kinds of not-done told apart: refused
  // (the service said no and changed nothing), failed (it ran and broke),
  // and no answer (the page cannot tell whether it ran).
  var RESULT = {
    ok: ["t-ok", "done", "Done ·"],
    refused: ["t-wait", "blocked", "Refused · nothing changed ·"],
    failed: ["t-bad", "failed", "Failed after running ·"],
    unknown: ["t-wait", "unknown", "No answer ·"],
    info: ["t-info", "copy", ""]
  };

  function resultLine(o) {
    var r = RESULT[o.kind] || RESULT.failed;
    var good = o.kind === "ok" || o.kind === "info";
    return el("p", { class: "res " + r[0], role: "status" }, [
      icon(r[1]),
      el("span", null, [r[2] ? el("b", { text: r[2] }) : null, r[2] ? " " : null, el("span", { class: good ? "act-out" : "act-err", text: o.text })])
    ]);
  }

  function outcomeOf(err) {
    var text = err.message + (err.reason && err.reason !== err.message ? " · " + err.reason : "");
    if (!err.status) return { kind: "unknown", text: "the dashboard did not answer, so this page cannot tell whether it ran. Look at the room before pressing again." };
    if (err.status === 422 || err.status >= 500) return { kind: "failed", text: text };
    return { kind: "refused", text: text.replace(/^Action refused: /, "").replace(/ · nothing started$/, "") };
  }

  function focusOn(focus) {
    var n = root.querySelector("[data-focus=\"" + focus + "\"]");
    if (n && n.focus) n.focus({ preventScroll: true });
  }

  function runAction(name, crew, key, btn) {
    asking = null;
    delete outcomes[key];
    running[key] = btn;
    draw();
    var body = { action: btn.action, project: name };
    if (crew) body.crew = crew;
    return press(body).then(function (res) {
      outcomes[key] = { kind: "ok", text: res.line || btn.label.replace(/…$/, "") + " done" };
    }, function (err) {
      outcomes[key] = outcomeOf(err);
    }).then(function () {
      delete running[key];
      draw();
      // What the action changed shows up in the floor and the buttons at
      // once rather than at the next event.
      return Promise.all([loadAll(), loadDetail()]).then(draw);
    });
  }

  function cancelAsk() {
    var a = asking;
    asking = null;
    draw();
    if (a) focusOn("act:" + a.button.action);
  }

  // The question before a destructive action: the console's own words, the
  // object it acts on, focus on Cancel, and only the action's name runs it.
  function confirmDialog() {
    var a = asking;
    var btn = a.button;
    var verb = btn.label.replace(/…$/, "");
    var obj = a.crew ? a.name + "/" + a.crew : a.name;
    var cancel = el("button", { class: "btn ghost", type: "button", "data-focus": "act-cancel" }, "Cancel");
    cancel.addEventListener("click", function (e) { if (e && e.stopPropagation) e.stopPropagation(); cancelAsk(); });
    var yes = el("button", { class: "btn " + (ACT_DANGER[btn.action] ? "dangerP" : "primary"), type: "button", "data-focus": "act-confirm" }, verb);
    yes.addEventListener("click", function (e) {
      if (e && e.stopPropagation) e.stopPropagation();
      var x = asking;
      if (x) runAction(x.name, x.crew, x.key, x.button);
    });
    var dlg = el("div", { class: "dlg", role: "alertdialog", "aria-modal": "true", "aria-labelledby": "dlg-t", "aria-describedby": "dlg-d" }, [
      el("h3", { id: "dlg-t", text: verb + "?" }),
      el("p", { class: "dlg-obj" }, [el("span", { text: a.crew ? "Crew" : "Project" }), el("b", { class: "mono", text: obj })]),
      el("p", { class: "prompt", id: "dlg-d", text: btn.confirm }),
      el("div", { class: "frow" }, [cancel, yes]),
      el("small", { class: "hint", text: "Esc or Cancel changes nothing. Only “" + verb + "” runs it." })
    ]);
    var wrap = el("div", { class: "mwrap" }, dlg);
    wrap.addEventListener("click", function (e) { if (e && e.target === wrap) cancelAsk(); });
    return wrap;
  }

  function copyText(text, key) {
    var done = function () { outcomes[key] = { kind: "ok", text: "copied " + text }; draw(); };
    var fallback = function () { outcomes[key] = { kind: "info", text: text + " · select it to copy (the clipboard needs https or localhost)" }; draw(); };
    try {
      if (window.navigator && window.navigator.clipboard && window.isSecureContext) {
        window.navigator.clipboard.writeText(text).then(done, fallback);
        return;
      }
    } catch (e) { /* fall through */ }
    fallback();
  }

  function toggleDiff(name, crew, key) {
    if (diffView && diffView.key === key && !diffView.loading) { diffView = null; draw(); return; }
    diffView = { key: key, loading: true };
    draw();
    api(taskPath(name, crew) + "/diff").then(function (body) {
      if (diffView && diffView.key === key) diffView = { key: key, body: body };
    }, function (err) {
      if (diffView && diffView.key === key) diffView = { key: key, error: err };
    }).then(draw);
  }

  function diffBlock(v) {
    if (v.loading) return el("p", { class: "loading-note", text: "reading the diff…" });
    if (v.error) return el("p", { class: "err-note", text: "Diff: " + (v.error.reason || v.error.message) });
    if (!v.body.exists) return el("p", { class: "err-note", text: v.body.reason || "the branch is gone" });
    return el("pre", { class: "act-diff", "aria-label": "Diff of " + (v.body.branch || "the branch"), text: v.body.text || "no changes" });
  }

  // ------------------------------------------------------------ read-only

  function roLine(reason) {
    return el("p", { class: "ro-line" }, [icon("lock"), el("span", { text: reason })]);
  }

  function noteReadOnly(body) {
    if (body && typeof body.read_only === "string") wsReadOnly = body.read_only;
  }

  // probeActions asks the first room's buttons once, so the band can say
  // from the start that this dashboard only reads.
  function probeActions(wsBody) {
    if (wsReadOnly !== null) return null;
    var first = (wsBody.projects || [])[0];
    if (!first) return null;
    return api(actionsPath(first.name)).then(noteReadOnly, function () { /* the drawers say it when they open */ });
  }

  function bands() {
    var out = [];
    if (wsReadOnly) out.push(el("div", { class: "bn ro", role: "status" }, [icon("lock"), el("b", { text: "Read-only." }), el("span", { text: wsReadOnly })]));
    return out;
  }

  // ------------------------------------------------------------ Mates only

  function toggleMatesOnly() {
    matesOnly = !matesOnly;
    try { window.localStorage.setItem("mate-office-mates-only", matesOnly ? "1" : "0"); } catch (e) { /* this page only */ }
    draw();
  }

  function matesButton(small) {
    var b = el("button", { class: "tog", type: "button", "aria-pressed": matesOnly ? "true" : "false", "data-focus": "mates-only",
      "aria-label": "Mates only", title: "Show only each room's Mate (the console's v)" },
    small ? icon("filter") : [icon("filter"), "Mates only", el("kbd", { class: "k", "aria-hidden": "true", text: "v" })]);
    b.addEventListener("click", toggleMatesOnly);
    return b;
  }

  // ------------------------------------------------------------ talking to the Mate

  function mateTabs(name, n) {
    var tab = function (id, label, count) {
      var b = el("button", { class: mateTab === id ? "on" : "", type: "button", role: "tab", "aria-selected": mateTab === id ? "true" : "false",
        "data-focus": "tab:" + id }, [label, count != null ? el("span", { class: "n", text: String(count) }) : null]);
      b.addEventListener("click", function () { mateTab = id; draw(); });
      return b;
    };
    return el("div", { class: "tabs", role: "tablist", "aria-label": "Mate of " + name }, [tab("talk", "Talk", n), tab("details", "Details", null)]);
  }

  function exchangesOf(name) {
    var d = detail && detail.key === "mate/" + enc(name) ? detail : null;
    if (!d || d.loading) return { loading: true };
    if (d.error) return { error: d.error };
    return { list: d.body.exchanges || [] };
  }

  var TURNS_SHOWN = 20;

  function talkSection(name) {
    var x = exchangesOf(name);
    var nodes = [el("p", { class: "chat-note", text: "Each message runs one headless turn of this Mate. Older turns fold to one line." })];
    if (x.loading) nodes.push(el("p", { class: "loading-note", text: "loading…" }));
    else if (x.error) nodes.push(el("p", { class: "err-note", text: "The turns could not be read: " + (x.error.reason || x.error.message) }));
    else if (!x.list.length) nodes.push(el("p", { class: "chat-note", text: "No turns recorded yet." }));
    else {
      var list = x.list.slice(-TURNS_SHOWN);
      list.forEach(function (ex, i) {
        var last = i === list.length - 1;
        var open = last || !!talkOpen[ex.id];
        var at = ex.prompt_at || ex.started_at;
        var t = ex.tokens ? tok(ex.tokens.total) + " tok" : "? tok";
        var who = ex.source || "?";
        if (!open) {
          var row = el("button", { class: "tc", type: "button", "aria-expanded": "false", "data-focus": "turn:" + ex.id }, [
            el("span", { class: "mono", text: at ? clock(at) : "?" }),
            el("span", { class: "who", text: who }),
            el("b", { text: firstLine(ex.prompt) }),
            el("span", { class: "mono", text: t })
          ]);
          row.addEventListener("click", function () { talkOpen[ex.id] = true; draw(); });
          nodes.push(row);
          return;
        }
        var head = [el("span", { class: "mono", text: at ? monthDay(at) + " " + clock(at) : "?" }), el("span", { text: "from " + who }), el("span", { text: t })];
        if (!last) {
          var fold = el("button", { class: "fold", type: "button", "data-focus": "turn:" + ex.id }, "Fold");
          fold.addEventListener("click", function () { delete talkOpen[ex.id]; draw(); });
          head.push(fold);
        }
        var steps = [];
        if (ex.tool_calls != null) steps.push(plural(ex.tool_calls, "tool call", "tool calls"));
        if (ex.model_calls != null) steps.push(plural(ex.model_calls, "model call", "model calls"));
        if (ex.duration_ms != null) steps.push(formatRun(ex.duration_ms));
        var ended = !!(ex.ended_at || ex.response_at);
        nodes.push(el("div", { class: "tx" }, [
          el("div", { class: "tx-h" }, head),
          el("div", { class: "msg me" + (who === "captain" ? "" : " other"), text: ex.prompt || "(no prompt recorded)" }),
          steps.length ? el("p", { class: "steps" }, [icon("tool"), steps.join(" · ")]) : null,
          ex.response ? el("div", { class: "msg reply", text: ex.response }) : null,
          el("span", { class: "phase" }, ended ? chip({ word: "done", tone: "t-gone", icon: "done" }) :
            el("span", { class: "chip t-ok" }, [el("span", { class: "dots", "aria-hidden": "true" }, [el("i"), el("i"), el("i")]), "working"]))
        ]));
      });
    }
    return el("section", { class: "chat", "aria-label": "Talk" }, nodes);
  }

  function firstLine(s) {
    var t = String(s || "").split("\n")[0];
    return t.length > 90 ? t.slice(0, 89) + "…" : t || "(no prompt)";
  }

  // The composer keeps its place. Sending is not wired yet (CON-141's
  // follow-up), so it is drawn and dimmed with the reason.
  function composer(name) {
    var a = acts && acts.key === "mate/" + enc(name) ? acts : null;
    if (a && a.body && a.body.read_only) return el("footer", { class: "dfoot" }, roLine(a.body.read_only));
    var send = el("button", { class: "btn primary", type: "button", disabled: true, "aria-describedby": "send-why" }, [icon("send"), "Send"]);
    return el("footer", { class: "dfoot" }, [
      el("textarea", { class: "compose", disabled: true, "aria-label": "Message the Mate of " + name, placeholder: "Message the Mate of " + name + "…" }),
      el("div", { class: "crow" }, [
        el("small", { id: "send-why", text: "Talking to the Mate from the browser is " + NOT_WIRED + " · use its REPL" }),
        send
      ])
    ]);
  }

  // ------------------------------------------------------------ the room's inbox

  var INBOX_VERBS = {
    "needs-decision": { word: "needs-decision", tone: "t-wait", icon: "ask", assign: true },
    blocked: { word: "blocked", tone: "t-bad", icon: "blocked", assign: true },
    "wait-mate": { word: "wait-mate", tone: "t-wait", icon: "review" }
  };

  function inboxInfo(i) {
    if (i.kind === "incident") return { word: i.verb ? i.verb.replace(/_/g, " ") : "incident", tone: "t-bad", icon: "blocked" };
    return INBOX_VERBS[i.verb] || { word: i.verb || i.kind || "?", tone: "t-info", icon: "mail" };
  }

  function inboxCount(name) {
    var b = body(name);
    if (!b) return { n: null, bad: false };
    var items = b.inbox || [];
    return { n: items.length, bad: items.some(function (i) { return inboxInfo(i).tone === "t-bad"; }) };
  }

  function mailbox(name, small) {
    var key = "inbox/" + enc(name);
    var c = inboxCount(name);
    var label = c.n == null ? "?" : String(c.n);
    if (small) {
      return pickable(el("button", { class: "mcab" + (selKey(sel) === key ? " sel" : ""), type: "button",
        "aria-label": "Inbox of " + name + ", " + label + " waiting" }, [icon("inbox"), label, el("small", { text: "inbox" })]), key);
    }
    return pickable(el("button", { class: "mbox" + (selKey(sel) === key ? " sel" : ""), type: "button",
      "aria-label": "Inbox of " + name + ", " + label + " waiting" }, [
      el("span", { class: "mb-body", "aria-hidden": "true" }, [el("span", { class: "mb-paper" }), el("span", { class: "mb-slot" })]),
      el("span", { class: "mb-post", "aria-hidden": "true" }),
      el("span", { class: "cnt" + (c.bad ? " bad" : c.n ? " hot" : ""), text: label }),
      el("span", { class: "clbl", text: "inbox" })
    ]), key);
  }

  function loadInboxLog(name) {
    var key = "inbox/" + enc(name);
    var since = new Date(Date.now() - 86400000).toISOString().replace(/\.\d{3}Z$/, "Z");
    return api(historyPath(name, since)).then(function (b) { inboxLog = { key: key, body: b }; }, function (e) { inboxLog = { key: key, error: e }; });
  }

  function daemonLine(name) {
    var b = body(name);
    var c = card(name) || {};
    var mode = (b && b.mode) || c.mode || "";
    if (mode === "auto") return el("p", { class: "autol t-ok" }, [icon("send"), "auto · the daemon types the box's answers into the Mate"]);
    if (mode === "manual") return el("p", { class: "autol t-idle" }, [icon("send"), "manual · the daemon types nothing; answers wait for the Mate"]);
    return el("p", { class: "autol t-gone" }, [icon("send"), "mode ?"]);
  }

  function inboxDrawer(name) {
    var b = body(name);
    var items = b ? b.inbox || [] : null;
    var mode = inboxMode[name] || "inbox";
    var seg = function (id, label) {
      var x = el("button", { class: mode === id ? "on" : "", type: "button", "aria-pressed": mode === id ? "true" : "false", "data-focus": "inbox-mode:" + id }, label);
      x.addEventListener("click", function () {
        if (mode === id) return;
        inboxMode[name] = id;
        if (id === "log") { inboxLog = null; loadInboxLog(name).then(draw); }
        draw();
      });
      return x;
    };
    var head = drawerHead(el("span", { class: "dav dcab", "aria-hidden": "true" }, icon("inbox")),
      ["Inbox · room " + name], "Message box",
      chip({ word: "", tone: items && items.length ? "t-wait" : "t-gone", icon: "inbox" }, items == null ? "?" : plural(items.length, "waiting", "waiting")), null);
    var nodes = [head,
      el("section", { class: "ds" }, [
        el("div", { class: "inbox-mode" }, [el("nav", { class: "bseg", "aria-label": "Show" }, [seg("inbox", "Inbox" + (items ? " · " + items.length : "")), seg("log", "Full log · 24h")]),
          el("kbd", { class: "k", "aria-hidden": "true", text: "l" })]),
        daemonLine(name)
      ])];
    if (mode === "log") {
      nodes.push(el("section", { class: "ds", "aria-label": "Full log" }, logRows(name)));
      return nodes;
    }
    if (!b) { nodes.push(el("p", { class: "cab-empty", text: projects[name] ? "This room could not be read." : "loading…" })); return nodes; }
    if (b.inbox_error) nodes.push(el("p", { class: "err-note news-err", text: "The box could not be read: " + b.inbox_error }));
    nodes.push(el("section", { class: "ds", "aria-label": "Inbox" }, items.length ? items.map(function (i) { return inboxRow(name, i); })
      : [el("p", { class: "loading-note", text: "Nothing waiting in this room's box." })]));
    return nodes;
  }

  function inboxRow(name, i) {
    var info = inboxInfo(i);
    var action;
    if (info.assign) {
      var ro = wsReadOnly;
      action = [el("button", { class: "btn", type: "button", disabled: true, "aria-label": "Assign to Mate", "data-focus": "assign:" + i.seq }, [icon("assign"), "Assign to Mate"]),
        el("small", { class: "why", text: ro ? "read-only dashboard" : NOT_WIRED })];
    } else if (i.crew) {
      var open = pickable(el("button", { class: "btn", type: "button" }, [icon("arrowR"), "Open crew"]), "crew/" + enc(name) + "/" + enc(i.crew));
      action = [open, el("small", { class: "why", text: i.verb === "wait-mate" ? "Merge… is in the crew drawer" : "" })];
    }
    return el("div", { class: "iitem" }, [
      el("div", { class: "ihead" }, [chip(info), i.crew ? el("span", { class: "id", text: i.crew }) : null, el("span", { class: "at", title: i.at || null, text: i.at ? clock(i.at) : "?" })]),
      el("p", { class: "itext", text: i.text }),
      action ? el("div", { class: "ibtn" }, action) : null
    ]);
  }

  function logRows(name) {
    var l = inboxLog && inboxLog.key === "inbox/" + enc(name) ? inboxLog : null;
    if (!l) return [el("p", { class: "loading-note", text: "loading…" })];
    if (l.error) return [el("p", { class: "err-note", text: "The log could not be read: " + (l.error.reason || l.error.message) })];
    var rows = [];
    Object.keys(l.body.crews || {}).forEach(function (crew) {
      var h = l.body.crews[crew] || {};
      (h.status_lines || []).forEach(function (s) {
        rows.push({ at: s.at, crew: crew, info: INBOX_VERBS[s.verb] || { word: s.verb || "message", tone: "t-info", icon: "working" }, text: s.text });
      });
      (h.questions || []).forEach(function (q) {
        if (q.answered_at) rows.push({ at: q.answered_at, crew: crew, info: { word: "answered", tone: "t-ok", icon: "answer" }, text: q.text });
      });
    });
    rows.sort(function (a, b) { return (parseAt(b.at) || 0) - (parseAt(a.at) || 0); });
    if (!rows.length) return [el("p", { class: "loading-note", text: "Nothing written in the last 24 hours." })];
    return rows.slice(0, 80).map(function (r) {
      return el("div", { class: "logl" }, [el("span", { class: "mono", title: r.at, text: clock(r.at) }), icoChip(r.info),
        el("span", null, [el("b", { class: "mono", text: r.crew }), " · " + r.info.word + " · ", el("span", { text: r.text })])]);
    });
  }

  // ------------------------------------------------------------ a new room

  function newRoomDoor(small) {
    var b = el("button", { class: "newroom" + (selKey(sel) === "add" ? " sel" : ""), type: "button", "aria-label": "New room: add project" }, [
      el("span", { class: "pl", "aria-hidden": "true" }, icon("plus")),
      el("span", { class: "nr-t" }, [el("b", { text: "New room" }),
        el("small", null, ["Add project…", small ? null : el("kbd", { class: "k", "aria-hidden": "true", text: "n" })])])
    ]);
    return pickable(b, "add");
  }

  function addProblem() {
    var n = addForm.name.trim();
    if (!n) return { field: "", why: "name is required" };
    if (!/^[a-z0-9][a-z0-9-]*$/.test(n)) return { field: "Use lowercase letters, digits and dashes.", why: "the name is not valid" };
    if (projectNames().indexOf(n) >= 0) return { field: "A project named " + n + " already exists.", why: "the name is taken" };
    return null;
  }

  function addCommand() {
    var n = addForm.name.trim();
    var r = addForm.repo.trim();
    return "mate project add " + n + (r ? " " + (/\s/.test(r) ? "'" + r.replace(/'/g, "'\\''") + "'" : r) : "");
  }

  function addDrawer() {
    var head = drawerHead(el("span", { class: "dav dcab", "aria-hidden": "true" }, icon("plus")), ["New room"], "Add project",
      chip({ word: "", tone: "t-idle", icon: "office" }, "no Mate is started"), null);
    var name = el("input", { class: "inp", id: "add-name", value: addForm.name, placeholder: "kiosk", autocomplete: "off", spellcheck: "false",
      "aria-describedby": "add-name-why", "data-focus": "add-name" });
    var repo = el("input", { class: "inp mono", id: "add-repo", value: addForm.repo, placeholder: "~/code/kiosk", autocomplete: "off", spellcheck: "false",
      "data-focus": "add-repo" });
    name.addEventListener("input", function (e) { addForm.name = e.target.value; refreshAdd(); });
    repo.addEventListener("input", function (e) { addForm.repo = e.target.value; refreshAdd(); });
    var nodes = [head, el("section", { class: "ds", "aria-label": "Add project" }, [
      el("label", { class: "fld", for: "add-name" }, [el("span", { text: "Name" })]), name,
      el("small", { class: "fhelp", id: "add-name-why", text: "" }),
      el("label", { class: "fld", for: "add-repo" }, [el("span", null, ["Repo path ", el("small", { text: "· optional" })])]), repo,
      el("small", { class: "fhelp", text: "Leave it empty to add the room now and point it at a repo later." }),
      el("div", { class: "frow" }, [
        el("div", { class: "act-slot right" }, [
          el("button", { class: "btn primary", type: "button", id: "add-go", disabled: true, "aria-describedby": "add-go-why" }, "Add project"),
          el("small", { class: "why", id: "add-go-why", text: "" })
        ])
      ]),
      el("div", { class: "cmd", id: "add-cmd" }, [el("span", { text: "Until then, run it in a terminal:" }), el("code", { id: "add-cmd-t", text: "" })])
    ])];
    return nodes;
  }

  // refreshAdd updates the form in place while the captain types, so the
  // caret stays where it is.
  function refreshAdd() {
    var p = addProblem();
    var name = root.querySelector("#add-name");
    var why = root.querySelector("#add-name-why");
    var goWhy = root.querySelector("#add-go-why");
    var cmd = root.querySelector("#add-cmd");
    var cmdT = root.querySelector("#add-cmd-t");
    if (!name) return;
    name.setAttribute("class", "inp" + (p && p.field ? " err" : ""));
    if (why) { why.textContent = p && p.field ? p.field : "Lowercase letters, digits and dashes. It names the room."; why.setAttribute("class", p && p.field ? "ferr" : "fhelp"); }
    if (goWhy) goWhy.textContent = p ? p.why : wsReadOnly ? "read-only dashboard" : "adding a project from the browser is " + NOT_WIRED;
    if (cmd) { if (p) cmd.setAttribute("hidden", ""); else cmd.removeAttribute("hidden"); }
    if (cmdT) cmdT.textContent = p ? "" : addCommand();
  }

  // ------------------------------------------------------------ answering news

  function answerForm(key, n) {
    var d = newsDraft[key] || (newsDraft[key] = { opt: "", own: "" });
    var nodes = [el("p", { class: "lab", text: "Your answer" })];
    (n.options || []).forEach(function (o) {
      var on = d.opt === o.key && !d.own.trim();
      var input = el("input", { type: "radio", name: "ans:" + key, checked: on, "data-focus": "opt:" + key + ":" + o.key });
      input.addEventListener("change", function () { d.opt = o.key; d.own = ""; draw(); });
      nodes.push(el("label", { class: "opt" + (on ? " on" : "") }, [input, el("span", null, [
        el("b", null, [el("span", { class: "mono", text: o.key + " " }), o.text, o.recommended ? el("span", { class: "pick", text: "Mate's pick" }) : null])
      ])]));
    });
    var own = el("textarea", { class: "compose", rows: "2", "aria-label": "Or in your own words", placeholder: "Or in your own words…", "data-focus": "own:" + key });
    own.value = d.own;
    own.textContent = d.own;
    own.addEventListener("input", function (e) { d.own = e.target.value; });
    nodes.push(own);
    var why = wsReadOnly ? "read-only dashboard" : "answering from the browser is " + NOT_WIRED + " · answer in the Mate's REPL (^T)";
    nodes.push(el("div", { class: "tactions" }, [
      el("div", { class: "act-slot" }, [el("button", { class: "btn primary", type: "button", disabled: true }, [icon("send"), "Send to the Mate"]), el("small", { class: "why", text: why })]),
      el("div", { class: "act-slot" }, [el("button", { class: "btn", type: "button", disabled: true }, [icon("fork"), "Talk it over first"]), el("small", { class: "why", text: "side talk is " + NOT_WIRED })])
    ]));
    return el("div", { class: "nans-form" }, nodes);
  }

  // noticeNews keeps which news this page has seen; one that arrives while
  // the page is open shows a toast. The first read toasts nothing.
  function noticeNews(b) {
    var seen = {};
    var fresh = null;
    (b.projects || []).forEach(function (p) {
      (p.news || []).forEach(function (n) {
        var k = p.project + "/" + n.id;
        seen[k] = true;
        if (knownNews && !knownNews[k] && n.status === "new" && !fresh) fresh = { project: p.project, id: n.id, title: n.title };
      });
    });
    if (fresh && !(sel && sel.kind === "captain")) toast = fresh;
    knownNews = seen;
  }

  function toastNode() {
    var t = toast;
    var open = el("button", { class: "btn primary", type: "button", "data-focus": "toast-open" }, "Open news");
    open.addEventListener("click", function () { newsOpen[t.project + "/" + t.id] = true; toast = null; select("captain"); });
    var dismiss = el("button", { class: "btn", type: "button", "data-focus": "toast-dismiss" }, "Dismiss");
    dismiss.addEventListener("click", function () { toast = null; draw(); });
    return el("div", { class: "toast", role: "status" }, [
      el("span", { class: "ico", "aria-hidden": "true" }, icon("mail")),
      el("div", { class: "tt" }, [el("b", { text: "News from the Mate of " + t.project }), el("small", { text: t.title }), el("div", { class: "tb" }, [open, dismiss])])
    ]);
  }

  function staleNote(name) {
    var s = null;
    staleList.forEach(function (x) { if (x.project === name) s = x; });
    return s ? el("p", { class: "rnote", title: s.error || null }, [icon("clock"), "timeline stuck since " + clock(s.since)]) : null;
  }

  function crewDrawer(name, crew) {
    var t = findTask(name, crew);
    var b = body(name);
    if (!t) {
      return [drawerHead(personAvatar(harnessInfo(""), shirt(crew), false), ["Crew · " + name], crew, chip(UNKNOWN_STATE), null),
        el("section", { class: "ds" }, el("p", { class: b ? "err-note" : "loading-note", text: b ? "No crew " + crew + " in " + name + "." : "loading…" }))];
    }
    var st = t.state || "";
    var s = crewState(st);
    var h = harnessInfo(t.harness);
    var word = s.word + ((st === "blocked" || st === "asleep") && t.detail ? ", " + detailWord(t.detail) : "");
    var d = detail && !detail.loading && !detail.error ? detail.body : null;
    var perf = d ? d.performance || {} : null;

    var nodes = [
      drawerHead(personAvatar(h, shirt(crew), st !== "gone"), ["Crew · " + name, htag(h)], crew, chip(s, word), t.since ? "since " + age(t.since) : null),
      actionsSection(name, crew),
      usageSection({
        tokens: t.tokens, cost: t.cost, updated: b ? b.generated_at : null,
        contextPct: t.context_pct, contextTokens: t.turns > 0 ? t.context_tokens_last : null,
        today: crewTokensToday(name, crew), spark: crewSpark(), link: usageLink(name, crew)
      })
    ];

    // Current activity: what the crew last said it was doing, and what the
    // recording shows it doing.
    var activity;
    if (detail && detail.error) {
      activity = [el("p", { class: "err-note", text: "Could not read this crew's work: " + (detail.error.reason || detail.error.message) })];
    } else if (!d) {
      activity = [el("p", { class: "loading-note", text: "loading…" })];
    } else {
      var line = lastOf(d.status_lines);
      var prompt = lastOf(perf.prompt_turns);
      var step = prompt && prompt.overview ? lastOf(prompt.overview.steps) : null;
      var seg = null;
      (perf.segments || []).forEach(function (x) { if (x.id === perf.current_segment_id) seg = x; });
      var lastTurn = lastOf(d.turns);
      activity = [
        el("p", { class: "stl" + (line ? "" : " muted"), text: line ? line.text : "No status line written yet", title: line && line.verb ? line.verb : null }),
        el("dl", { class: "kv" }, [
          el("dt", { text: "Step" }), el("dd", { text: step ? step.label + (step.open ? " · running" : "") : "none recorded" }),
          el("dt", { text: "Target" }), el("dd", { class: "mono", text: seg && seg.target ? seg.target : (t.target || "none recorded") }),
          el("dt", { text: "Last turn" }), el("dd", { text: lastTurn ? age(lastTurn.ended_at || lastTurn.started_at) + " ago" : "none recorded" })
        ])
      ];
    }
    nodes.push(el("section", { class: "ds", "aria-label": "Current activity" }, [el("h3", { class: "sh", text: "Current activity" })].concat(activity)));

    var branch = d ? d.branch || {} : null;
    var skills = perf ? perf.skills || [] : null;
    var link = el("a", { class: "link", href: "../#/p/" + enc(name) + "/t/" + enc(crew) }, ["Open timeline", icon("arrowR")]);
    nodes.push(el("section", { class: "ds", "aria-label": "Task" }, [
      el("h3", { class: "sh", text: "Task" }),
      el("p", { class: "stl", text: t.text || "no task text recorded" }),
      el("dl", { class: "kv" }, [
        el("dt", { text: "Branch" }),
        el("dd", { class: "mono" + (branch && !branch.exists ? " gone" : ""), title: branch && branch.reason ? branch.reason : null,
          text: (t.branch || "none recorded") + (branch && t.branch && !branch.exists ? " · gone" : "") }),
        el("dt", { text: "Turns" }), el("dd", { text: String(t.turns) }),
        el("dt", { text: "Agents" }),
        el("dd", { title: "the kinds of agent this crew delegated to, first use first" },
          t.agents && t.agents.length ? [agentBadges(t, "inrow")] : [el("span", { class: "muted", text: "none delegated to" })]),
        el("dt", { text: "Skills" }),
        el("dd", { title: "a skill counts when the agent loaded it; loading is not proof it was followed",
          text: skills == null ? "?" : skills.length ? skills.map(function (k) { return k.name + (k.count > 1 ? " ×" + k.count : ""); }).join(", ") : "none loaded" })
      ]),
      link
    ]));
    return nodes;
  }

  // The cabinet's drawers, by when a task closed.
  var DRAWERS = [
    { name: "Top drawer", range: "this week", max: 7 },
    { name: "Middle drawer", range: "last week", max: 14 },
    { name: "Bottom drawer", range: "older", max: Infinity }
  ];

  function cabinetDrawer(name) {
    var b = body(name);
    var closed = closedTasks(name);
    var oldest = closed.length ? closedAt(closed[closed.length - 1]) : "";
    var head = drawerHead(el("span", { class: "dav dcab", "aria-hidden": "true" }, icon("cabinet")),
      [name + " · finished tasks"], "Filing cabinet",
      chip({ word: "", tone: "t-gone", icon: "cabinet" }, plural(closed.length, "finished", "finished")),
      closed.length ? "oldest " + monthDay(oldest) : null);
    if (!b) return [head, el("p", { class: "cab-empty", text: projects[name] ? "This room could not be read." : "loading…" })];
    if (!closed.length) return [head, el("p", { class: "cab-empty", text: "Nothing has finished in " + name + " yet." })];

    var counts = {};
    var order = [];
    closed.forEach(function (t) {
      var f = finalState(t);
      if (!counts[f.word]) { counts[f.word] = { f: f, n: 0 }; order.push(f.word); }
      counts[f.word].n++;
    });
    // A filter set in another room, or on a state no longer in this one,
    // shows everything.
    var only = cabFilter.project === name && counts[cabFilter.word] ? cabFilter.word : "";
    var shown = only ? closed.filter(function (t) { return finalState(t).word === only; }) : closed;
    var sum = 0;
    shown.forEach(function (t) { sum += t.tokens ? t.tokens.total : 0; });
    var nowMs = Date.now();
    var groups = DRAWERS.map(function (dr, i) {
      var min = i === 0 ? -Infinity : DRAWERS[i - 1].max;
      return { dr: dr, items: shown.filter(function (t) {
        var at = parseAt(closedAt(t));
        var days = at == null ? Infinity : (nowMs - at) / 86400000;
        return days >= min && days < dr.max || (at == null && dr.max === Infinity);
      }) };
    }).filter(function (g) { return g.items.length; });

    return [head,
      el("section", { class: "ds", "aria-label": "Summary" }, el("div", { class: "csum" }, order.map(function (w) {
        return stateFilter(name, counts[w].f, counts[w].n, only);
      }).concat([el("span", { class: "tt", title: only ? "tokens of the " + only + " tasks shown" : null }, [el("b", { text: tok(sum) }), " tokens"])]))),
      el("div", { class: "cab-table" }, [el("div", { class: "colh", "aria-hidden": "true" }, [el("span", { text: "Task" }), el("span", { text: "Harness / model / effort" }), el("span", { text: "Final state" }), el("span", { text: "Ran" }), el("span", { text: "Closed" }), el("span", { text: "Tokens" })])
    ].concat(groups.map(function (g) {
      return el("div", { class: "cg" }, [
        el("div", { class: "cgh" }, [el("i", { class: "knob" }), el("b", { text: g.dr.name }), el("span", { text: g.dr.range }), el("span", { class: "n", text: plural(g.items.length, "task", "tasks") })]),
        el("ul", { class: "tl" }, g.items.map(function (t) {
          var f = finalState(t);
          return el("li", { class: "tr", title: t.text || null }, [
            el("span", { class: "tk" }, [roleMark(t), el("span", { class: "tn", text: t.crew }), agentBadges(t)]),
            el("span", { class: "ta", "aria-label": "Harness / model / effort" }, [
              el("b", { text: harnessInfo(t.harness).name }),
              el("span", { class: "mono", text: t.last_model || t.model || "model ?",
                title: t.last_model ? "Last observed model" : t.model ? "Requested model" : "Model not recorded" }),
              el("small", { text: t.effort ? "effort: " + t.effort : "effort ?",
                title: "Requested effort; runtime effort may differ" })
            ]),
            el("span", { class: "fs" }, finalChip(f)),
            el("span", { class: "trun", title: "spawned to closed" }, formatRun(t.age_ms)),
            el("span", { class: "td", title: closedAt(t) }, [age(closedAt(t)) + " ago", el("small", { text: monthDay(closedAt(t)) })]),
            el("span", { class: "tt2", text: tok(t.tokens ? t.tokens.total : null) }),
            usageLink(name, t.crew, "tu")
          ]);
        }))
      ]);
    })))
    ];
  }

  // A summary chip is a final state's glyph and count, the word its
  // tooltip; pressing it shows only that state's tasks, pressing it again
  // shows them all.
  function stateFilter(name, f, n, only) {
    var on = only === f.word;
    var label = n + " " + f.word + (f.why ? ": " + f.why : "");
    var b = el("button", { class: "chip cf tip " + f.tone + (on ? " on" : only ? " dim" : ""), type: "button",
      "data-tip": label, "aria-label": label, "aria-pressed": on ? "true" : "false", "data-focus": "cab-filter:" + f.word },
    [icon(f.icon), String(n)]);
    b.addEventListener("click", function () {
      cabFilter = on ? { project: "", word: "" } : { project: name, word: f.word };
      draw();
    });
    return b;
  }

  function drawer() {
    var wide = sel && (sel.kind === "cab" || sel.kind === "board");
    var talky = sel && (sel.kind === "mate" || sel.kind === "inbox");
    var aside = el("aside", { class: "drawer" + (wide ? " drawer-cabinet" : "") + (talky ? " drawer-wide" : "") + (sel && sel.kind === "board" && sel.date ? " drawer-day" : "") + (sel && sel.kind === "mate" && mateFull ? " drawer-full" : ""),
      "aria-label": "Details", id: "drawer" });
    if (!sel) { aside.setAttribute("hidden", ""); return aside; }
    aside.appendChild(el("span", { class: "grab", "aria-hidden": "true" }));
    var nodes = sel.kind === "mate" ? mateDrawer(sel.project) : sel.kind === "crew" ? crewDrawer(sel.project, sel.crew)
      : sel.kind === "board" ? boardDrawer() : sel.kind === "captain" ? captainDrawer()
        : sel.kind === "inbox" ? inboxDrawer(sel.project) : sel.kind === "add" ? addDrawer() : cabinetDrawer(sel.project);
    append(aside, nodes);
    // The head's buttons hang over the drawer as it scrolls; to stick they
    // must be a child of the drawer, not of the head.
    var btns = aside.querySelector(".dbtns");
    if (btns) aside.insertBefore(btns, btns.parentNode);
    return aside;
  }

  // ------------------------------------------------------------ the captain's news

  // A news's status in the captain's words (matehost's News statuses).
  var NEWS_STATES = {
    "new": { word: "unread", tone: "t-wait", icon: "mail" },
    waiting: { word: "read, not answered", tone: "t-info", icon: "read" },
    working: { word: "answered, Mate on it", tone: "t-ok", icon: "answer" },
    done: { word: "closed", tone: "t-gone", icon: "done" },
    dropped: { word: "dropped", tone: "t-gone", icon: "failed" }
  };
  function newsState(status) {
    return NEWS_STATES[status] || { word: status ? String(status) : "unknown", tone: "t-gone", icon: "unknown" };
  }

  var EARLIER_SHOWN = 10;

  // The captain's drawer (CON-97): what the Mates sent the captain with
  // `mate news open`, from every project, and where each stands. It is a
  // view: a news is read and answered in its Mate's REPL, and opening one
  // here marks nothing seen.
  function captainDrawer() {
    var ns = newsSummary();
    var owed = ns.unread + ns.waiting;
    var head = drawerHead(el("span", { class: "dav", "aria-hidden": "true" },
      [el("span", { class: "d-head", style: "background:var(--head)" }), el("span", { class: "d-body", style: "background:#46566B" })]),
    ["Captain · you"], "News from the Mates",
    chip(owed ? newsState(ns.unread ? "new" : "waiting") : { word: "", tone: "t-gone", icon: "mail" },
      ns.unread ? ns.unread + " unread" + (ns.waiting ? ", " + ns.waiting + " to answer" : "")
        : owed ? ns.waiting + " to answer" : news && news.body ? "nothing waiting" : "news ?"), null);
    if (!news) return [head, el("p", { class: "cab-empty", text: "loading…" })];
    if (news.error) return [head, el("p", { class: "cab-empty err-note", text: "The news could not be read: " + (news.error.reason || news.error.message) })];

    var all = [];
    var broken = [];
    var logged = 0;
    (news.body.projects || []).forEach(function (p) {
      if (p.logged) logged++;
      if (p.error) broken.push(p);
      (p.news || []).forEach(function (n) { all.push({ project: p.project, n: n }); });
    });
    all.sort(function (a, b) { return (parseAt(b.n.at) || 0) - (parseAt(a.n.at) || 0); });
    var owedList = all.filter(function (x) { return x.n.status === "new" || x.n.status === "waiting"; });
    var working = all.filter(function (x) { return x.n.status === "working"; });
    var earlier = all.filter(function (x) { return x.n.status !== "new" && x.n.status !== "waiting" && x.n.status !== "working"; });

    var nodes = [head];
    broken.forEach(function (p) {
      nodes.push(el("p", { class: "err-note news-err", text: p.project + ": news ?", title: p.error }));
    });
    if (!all.length) {
      nodes.push(el("p", { class: "cab-empty", text: logged
        ? "No Mate has sent you news yet."
        : "No Mate has opened a news log. News comes from a headless Mate (mate news open)." }));
    } else {
      nodes.push(newsSection("Waiting on you", owedList, "Nothing is waiting on you."));
      if (working.length) nodes.push(newsSection("The Mate is on it", working));
      if (earlier.length) {
        var shown = newsEarlierAll ? earlier : earlier.slice(0, EARLIER_SHOWN);
        var sec = newsSection("Earlier", shown);
        if (earlier.length > shown.length) {
          var more = el("button", { class: "link news-more", type: "button", "data-focus": "news-more" }, "Show all " + earlier.length);
          more.addEventListener("click", function () { newsEarlierAll = true; draw(); });
          sec.appendChild(more);
        }
        nodes.push(sec);
      }
    }
    nodes.push(el("p", { class: "loading-note news-ro", text: "Sending an answer from the browser is " + NOT_WIRED + ": answer a news in its Mate's REPL (^T)." }));
    return nodes;
  }

  function newsSection(title, list, empty) {
    return el("section", { class: "ds", "aria-label": title }, [
      el("h3", { class: "sh" }, [title + " ", el("span", { text: list.length ? String(list.length) : "" })]),
      list.length ? el("ul", { class: "nl" }, list.map(newsRow)) : el("p", { class: "loading-note", text: empty || "" })
    ]);
  }

  function newsRow(x) {
    var n = x.n;
    var key = x.project + "/" + n.id;
    var st = newsState(n.status);
    var open = !!newsOpen[key];
    var meta = [x.project, n.crew || null, "#" + n.id].filter(Boolean).join(" · ");
    var btn = el("button", { class: "nw" + (n.status === "new" ? " unread" : ""), type: "button", "aria-expanded": open ? "true" : "false",
      "aria-label": n.title + ", " + st.word + ", " + meta, "data-focus": "news:" + key }, [
      icoChip(st),
      el("span", { class: "nwt" }, [el("b", { text: n.title }), el("small", { text: meta + " · " + st.word })]),
      el("span", { class: "nwa", title: n.at || null, text: age(n.at) + " ago" })
    ]);
    btn.addEventListener("click", function () {
      if (open) delete newsOpen[key]; else newsOpen[key] = true;
      draw();
    });
    var owed = n.status === "new" || n.status === "waiting";
    return el("li", { class: "nli" + (open ? " open" : "") }, [btn, open ? newsBody(n) : null, open && owed ? answerForm(key, n) : null]);
  }

  function when(ts) {
    return ts ? monthDay(ts) + " " + clock(ts) + " · " + age(ts) + " ago" : null;
  }

  function newsBody(n) {
    var st = newsState(n.status);
    var nodes = [];
    if (n.question) nodes.push(el("p", { class: "stl", text: n.question }));
    if ((n.context || []).length) nodes.push(el("ul", { class: "nctx" }, n.context.map(function (c) { return el("li", { text: c }); })));
    if ((n.options || []).length) {
      nodes.push(el("ul", { class: "nopt" }, n.options.map(function (o) {
        return el("li", null, [el("b", { class: "mono", text: o.key }), " " + o.text, o.recommended ? el("small", { text: " recommended" }) : null]);
      })));
    }
    var kv = [
      el("dt", { text: "Status" }), el("dd", null, chip(st)),
      el("dt", { text: "Sent" }), el("dd", { text: when(n.opened_at) || "?" }),
      el("dt", { text: "Read" }), el("dd", { class: n.seen_at || n.status !== "new" ? null : "gone",
        text: n.seen_at ? when(n.seen_at) : n.status === "new" ? "not yet" : "yes" }),
      el("dt", { text: "Answered" }), el("dd", { class: n.answered_at || n.answer ? null : "gone",
        text: n.answered_at ? when(n.answered_at) : n.answer ? "yes" : "not yet" })
    ];
    if (n.answer) kv.push(el("dt", { text: "Your answer" }), el("dd", { class: "nans", text: n.answer }));
    nodes.push(el("dl", { class: "kv nkv" }, kv));
    if ((n.notes || []).length) {
      nodes.push(el("ul", { class: "nnotes", "aria-label": "Updates from the Mate" }, n.notes.map(function (x) {
        return el("li", null, [el("span", { class: "mono", title: x.at || null, text: x.at ? monthDay(x.at) + " " + clock(x.at) : "?" }), el("span", { text: x.text })]);
      })));
    }
    return el("div", { class: "nwb" }, nodes);
  }

  // ------------------------------------------------------------ the daily board

  // The whiteboard in the hallway (CON-84): /api/board, one column per
  // local day, the workspace or one project. It counts in tokens, never in
  // money, and has no merged column: the timeline sees only the merges
  // mate made itself.
  var BOARD_RANGES = [7, 14, 30];

  function storedBoardDays() {
    try {
      var n = parseInt(window.localStorage.getItem("mate.office.boardDays"), 10);
      if (BOARD_RANGES.indexOf(n) >= 0) return n;
    } catch (e) { /* storage is a convenience */ }
    return 14;
  }

  function loadBoard() {
    var days = boardDays;
    var token = detailToken;
    return api("/api/board?days=" + days).then(function (body) {
      if (token === detailToken) board = { days: days, body: body };
    }, function (err) {
      if (token === detailToken) board = { days: days, error: err };
    });
  }

  function loadBoardDay(date) {
    var token = detailToken;
    return api("/api/board/day?date=" + enc(date)).then(function (body) {
      if (token === detailToken) boardDay = { date: date, body: body };
    }, function (err) {
      if (token === detailToken) boardDay = { date: date, error: err };
    });
  }

  function boardButton(small) {
    var key = "board";
    var b = el("button", { class: (small ? "dbm" : "dbd") + (sel && sel.kind === "board" ? " sel" : ""), type: "button",
      "aria-label": "Daily board: tokens, work and incidents by day", "data-focus": "board" }, small
      ? [icon("usage")]
      : [
          el("span", { class: "db-art", "aria-hidden": "true" }, [6, 10, 7, 13, 9, 15, 11].map(function (h) {
            return el("i", { style: "height:" + h * 2 + "px" });
          })),
          el("b", { text: "Daily board" }),
          el("span", { text: "tokens · work · incidents by day" })
        ]);
    return pickable(b, key);
  }

  // The rows a reader compares, each a number read off one day. good says
  // which way is better, for the arrow's colour; why is the tooltip.
  var BOARD_METRICS = [
    { key: "tokens", label: "Tokens", good: -1, n: function (d) { return d.tokens ? d.tokens.total : 0; }, fmt: tok,
      why: function (d) { return "Mate " + tok(d.mate_tokens) + ", crews " + tok(d.crew_tokens); } },
    { key: "opened", label: "Opened", good: 0, n: function (d) { return sumOf(d.spawned); },
      why: function (d) { return countWords(d.spawned, ROLE_WORDS); } },
    { key: "done", label: "Done", good: 1, n: function (d) { return (d.closed || {}).finished || 0; },
      why: function () { return "ships and scouts closed finished"; } },
    { key: "dropped", label: "Dropped", good: -1, n: function (d) { return sumOf(d.closed) - ((d.closed || {}).finished || 0); },
      why: function (d) { return countWords(d.closed, CLOSED_WORDS, "finished"); } },
    { key: "approved", label: "Approved", good: 1, n: function (d) { return (d.reviews || {}).pass || 0; },
      why: function (d) { return countWords(d.reviews, VERDICT_WORDS); } },
    { key: "rework", label: "Rework", good: -1, n: function (d) { return (d.reviews || {})["changes-requested"] || 0; },
      why: function () { return "reviews that sent the work back (changes requested)"; } },
    { key: "incidents", label: "Incidents", good: -1, n: function (d) { return sumOf(d.incidents); },
      why: function (d) { return countWords(d.incidents, INCIDENT_WORDS); } },
    { key: "budget", label: "Over budget", good: -1, n: function (d) { return (d.incidents || {}).budget || 0; },
      why: function () { return "budget incidents: a crew or the project crossed its token budget"; } }
  ];

  var ROLE_WORDS = { ship: "ship", scout: "scout", review: "review" };
  var CLOSED_WORDS = { finished: "finished", discarded: "discarded", didnt_start: "didn't start" };
  var VERDICT_WORDS = { pass: "approved", "changes-requested": "changes requested", incomplete: "incomplete", no_verdict: "no verdict" };
  var INCIDENT_WORDS = { stale: "stale", runtime_lost: "runtime lost", wedged: "wedged", budget: "over budget" };

  function sumOf(m) {
    var n = 0;
    Object.keys(m || {}).forEach(function (k) { n += m[k]; });
    return n;
  }

  function countWords(m, words, skip) {
    var parts = Object.keys(m || {}).filter(function (k) { return k !== skip && m[k]; }).sort().map(function (k) {
      return m[k] + " " + (words[k] || k.replace(/_/g, " "));
    });
    return parts.length ? parts.join(", ") : "none";
  }

  function boardDayLabel(date) {
    var p = String(date).split("-");
    var d = new Date(+p[0], +p[1] - 1, +p[2]);
    if (isNaN(d.getTime())) return String(date);
    return ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"][d.getDay()] + " " + MONTHS[d.getMonth()] + " " + d.getDate();
  }

  // The days shown: the workspace's totals, or one project's.
  function boardDaysOf(b) {
    if (!boardProject) return b.total || [];
    var p = (b.projects || []).filter(function (x) { return x.name === boardProject; })[0];
    return p ? p.days || [] : [];
  }

  function boardDrawer() {
    var date = sel && sel.date || "";
    var head = drawerHead(el("span", { class: "dav dcab", "aria-hidden": "true" }, icon("usage")),
      [boardProject || "workspace", date ? " · " + date : " · by day"], "Daily board",
      chip({ word: "", tone: "t-info", icon: "usage" }, "last " + boardDays + " days"),
      board && board.body ? "days in " + board.body.zone : null);
    var controls = el("section", { class: "ds bctl", "aria-label": "Board range" }, [
      el("div", { class: "bseg", role: "group", "aria-label": "Days" }, BOARD_RANGES.map(function (n) {
        var b = el("button", { type: "button", class: n === boardDays ? "on" : "", "aria-pressed": n === boardDays ? "true" : "false",
          "data-focus": "board-days:" + n, text: n + " days" });
        b.addEventListener("click", function () {
          if (n === boardDays) return;
          boardDays = n;
          try { window.localStorage.setItem("mate.office.boardDays", String(n)); } catch (e) { /* convenience */ }
          board = null;
          draw();
          loadBoard().then(draw);
        });
        return b;
      })),
      el("div", { class: "bseg", role: "group", "aria-label": "Project" }, [""].concat(projectNames()).map(function (n) {
        var on = n === boardProject;
        var b = el("button", { type: "button", class: on ? "on" : "", "aria-pressed": on ? "true" : "false",
          "data-focus": "board-project:" + n, text: n || "All" });
        b.addEventListener("click", function () { boardProject = n; draw(); });
        return b;
      }))
    ]);
    if (!board || board.days !== boardDays) return [head, controls, el("p", { class: "cab-empty", text: "loading…" })];
    if (board.error) return [head, controls, el("p", { class: "cab-empty", text: "The board could not be read: " + (board.error.reason || board.error.message) })];
    var p = boardProject ? (board.body.projects || []).filter(function (x) { return x.name === boardProject; })[0] : null;
    var days = boardDaysOf(board.body);
    if (!days.length) return [head, controls, el("p", { class: "cab-empty", text: "Nothing recorded for " + (boardProject || "this workspace") + "." })];
    var err = p && p.error ? el("p", { class: "room-err", text: "Tasks could not be read: " + p.error }) : null;
    if (date) return [head, controls, err].concat(boardDayView(days, date), [boardTable(days, date)]);
    return [head, controls, err, boardKpis(days, days.length - 1), boardTable(days, "")];
  }

  // One day against the average of the other days in the window. Today is
  // still going, so a lower number before evening is not news.
  function boardKpis(days, at) {
    var day = days[at];
    var others = days.filter(function (d, i) { return i !== at; });
    var today = at === days.length - 1;
    return el("section", { class: "ds", "aria-label": today ? "Today against the average" : "The day against the average" }, [
      el("h3", { class: "sh" }, [today ? "Today so far" : boardDayLabel(day.date),
        el("span", { text: "vs the average of the " + plural(others.length, today ? "day" : "other day", today ? "days" : "other days") + (today ? " before" : "") })]),
      el("div", { class: "bkpi" }, BOARD_METRICS.map(function (m) {
        var v = m.n(day);
        var avg = null;
        if (others.length) {
          var sum = 0;
          others.forEach(function (d) { sum += m.n(d); });
          avg = sum / others.length;
        }
        var fmt = m.fmt || String;
        var delta = null;
        if (avg != null && avg > 0) delta = Math.round((v - avg) / avg * 100);
        var tone = delta == null || delta === 0 || !m.good ? "" : (delta > 0) === (m.good > 0) ? " up" : " down";
        return el("div", { class: "bk", title: m.why(day) }, [
          el("span", { class: "bkl", text: m.label }),
          el("b", { text: fmt(v) }),
          el("span", { class: "bkd" + tone, text: avg == null ? "no days before" : "avg " + (m.fmt ? m.fmt(Math.round(avg)) : avg.toFixed(1))
            + (delta == null ? "" : " · " + (delta > 0 ? "+" : "") + delta + "%") })
        ]);
      }))
    ]);
  }

  function boardDayName(days, date) {
    return days.length && days[days.length - 1].date === date ? "Today" : boardDayLabel(date);
  }

  // One row per day, newest first; the token bar splits Mate from crews.
  // A row opens its day; the open day's row closes it again.
  function boardTable(days, open) {
    var max = 1;
    days.forEach(function (d) { max = Math.max(max, d.tokens ? d.tokens.total : 0); });
    var cols = BOARD_METRICS.slice(1);
    return el("section", { class: "ds", "aria-label": "By day" }, [
      el("h3", { class: "sh" }, ["By day", el("span", { class: "bleg" }, [
        el("i", { class: "bm" }), "Mate", el("i", { class: "bc" }), "crews"])]),
      el("div", { class: "btab" }, el("table", null, [
        el("thead", null, el("tr", null, [el("th", { text: "Day" }), el("th", { text: "Tokens" })].concat(cols.map(function (m) {
          return el("th", { text: m.label });
        })))),
        el("tbody", null, days.slice().reverse().map(function (d, i) {
          var total = d.tokens ? d.tokens.total : 0;
          var on = d.date === open;
          var name = i === 0 ? "Today" : boardDayLabel(d.date);
          var go = function (e) {
            if (e && e.stopPropagation) e.stopPropagation();
            select(on ? "board" : "board/" + enc(d.date));
          };
          var btn = el("button", { type: "button", class: "bday", "data-focus": "board-day:" + d.date, "aria-expanded": on ? "true" : "false",
            "aria-label": (on ? "Close " : "Open ") + name + ": its tasks, tokens and incidents", text: name });
          btn.addEventListener("click", go);
          var tr = el("tr", { class: "bpick" + (i === 0 ? " btoday" : "") + (on ? " bon" : ""), title: d.date }, [
            el("th", { scope: "row" }, [btn, el("span", { class: "bchev", "aria-hidden": "true" }, icon("arrowR"))]),
            el("td", { class: "btok", title: BOARD_METRICS[0].why(d) }, el("div", null, [
              el("span", { class: "bbar", "aria-hidden": "true" }, [
                el("i", { class: "bm", style: "width:" + (d.mate_tokens / max * 100).toFixed(2) + "%" }),
                el("i", { class: "bc", style: "width:" + (d.crew_tokens / max * 100).toFixed(2) + "%" })
              ]),
              el("span", { class: "mono", text: tok(total) })
            ]))
          ].concat(cols.map(function (m) {
            var v = m.n(d);
            return el("td", { class: "bn" + (v ? "" : " zero"), title: v ? m.why(d) : null, text: String(v) });
          })));
          tr.addEventListener("click", go);
          return tr;
        }))
      ]))
    ]);
  }

  // ------------------------------------------------------------ one day

  // A day opened up (CON-88): /api/board/day listed rather than counted -
  // the day's numbers, then every task that day, who spent the tokens and
  // the incidents, narrowed to the board's project.
  function boardDayView(days, date) {
    var at = -1;
    days.forEach(function (d, i) { if (d.date === date) at = i; });
    var dates = board.body.dates || [];
    var di = dates.indexOf(date);
    var step = function (to, label, glyph) {
      var b = el("button", { type: "button", class: "bstep", disabled: to ? null : "", "aria-label": label + (to ? ": " + boardDayName(days, to) : ""),
        "data-focus": "board-step:" + label }, glyph);
      if (to) b.addEventListener("click", function (e) { e.stopPropagation(); select("board/" + enc(to)); });
      return b;
    };
    var back = el("button", { type: "button", class: "bback", "data-focus": "board-back" }, ["All days"]);
    back.addEventListener("click", function (e) { e.stopPropagation(); select("board"); });
    var nav = el("section", { class: "ds bnav", "aria-label": "Day" }, [
      back,
      el("div", { class: "bnavd" }, [
        step(di > 0 ? dates[di - 1] : "", "Day before", "‹"),
        el("h3", { text: boardDayName(days, date) }),
        el("span", { class: "mono", text: date }),
        step(di >= 0 && di < dates.length - 1 ? dates[di + 1] : "", "Day after", "›")
      ])
    ]);
    var nodes = [nav];
    if (at >= 0) nodes.push(boardKpis(days, at));
    var b = boardDay && boardDay.date === date ? boardDay : null;
    if (!b) return nodes.concat([el("p", { class: "cab-empty", text: "loading…" })]);
    if (b.error) return nodes.concat([el("p", { class: "cab-empty", text: "The day could not be read: " + (b.error.reason || b.error.message) })]);
    var mine = function (x) { return !boardProject || x.project === boardProject; };
    var errs = b.body.errors || {};
    Object.keys(errs).filter(function (n) { return !boardProject || n === boardProject; }).forEach(function (n) {
      nodes.push(el("p", { class: "room-err", text: n + ": tasks could not be read: " + errs[n] }));
    });
    var all = !boardProject;
    return nodes.concat([
      dayTasks((b.body.tasks || []).filter(mine), all),
      dayActors((b.body.actors || []).filter(mine), all),
      dayIncidents((b.body.incidents || []).filter(mine), all)
    ]);
  }

  // What a task did that day, in order: opened, worked, closed and how.
  function dayDid(t) {
    var parts = [];
    if (t.opened) parts.push(el("span", { class: "bev b-open", title: t.spawned_at }, [icon("ship"), "opened " + clock(t.spawned_at).slice(0, 5)]));
    if (t.closed_today) {
      var f = finalState(t);
      parts.push(el("span", { class: "bev " + f.tone, title: (f.why || f.word) + " · " + closedAt(t) }, [icon(f.icon), f.word + " " + clock(closedAt(t)).slice(0, 5)]));
    } else if (!t.closed) {
      var s = crewState(t.state || "");
      parts.push(el("span", { class: "bev b-still", title: "still open" }, [icon(s.icon), "open · " + s.word]));
    } else if (!t.opened) {
      parts.push(el("span", { class: "bev b-still", title: "closed " + closedAt(t) }, ["worked; closed " + monthDay(closedAt(t))]));
    }
    return parts;
  }

  function dayTasks(list, all) {
    var sum = 0;
    list.forEach(function (t) { sum += t.day_tokens ? t.day_tokens.total : 0; });
    var head = [el("th", { text: "Task" })].concat(all ? [el("th", { class: "btl", text: "Project" })] : [],
      [el("th", { class: "btl", text: "Harness / model / effort" }), el("th", { class: "btl", text: "That day" }), el("th", { text: "Turns" }),
        el("th", { text: "Tokens that day" }), el("th", { text: "Task total" })]);
    return el("section", { class: "ds", "aria-label": "Tasks that day" }, [
      el("h3", { class: "sh" }, ["Tasks", el("span", { text: plural(list.length, "task", "tasks") + " · " + tok(sum) + " tokens that day" })]),
      list.length ? el("div", { class: "btab bdt" }, el("table", null, [
        el("thead", null, el("tr", null, head)),
        el("tbody", null, list.map(function (t) {
          var tr = el("tr", { class: "bpick", title: t.text || null }, [
            el("th", { scope: "row" }, el("span", { class: "btask" }, [roleMark(t), el("span", null, [
              el("button", { type: "button", class: "bday", "data-focus": "board-task:" + t.project + "/" + t.crew,
                "aria-label": "Open " + t.crew + " in " + t.project, text: t.crew }),
              el("small", { text: t.text || "no task text recorded" }),
              agentBadges(t)])]))
          ].concat(all ? [el("td", { class: "btl", text: t.project })] : [], [
            el("td", { class: "btl" }, el("span", { class: "ta" }, [
              el("b", { text: harnessInfo(t.harness).name }),
              el("span", { class: "mono", text: t.last_model || t.model || "model ?" }),
              el("small", { text: t.effort ? "effort: " + t.effort : "effort ?" })
            ])),
            el("td", { class: "btl" }, el("span", { class: "bevs" }, dayDid(t))),
            el("td", { class: "bn" + (t.day_turns ? "" : " zero"), text: String(t.day_turns || 0) }),
            el("td", { class: "bn mono" + (t.day_tokens && t.day_tokens.total ? "" : " zero"), text: tok(t.day_tokens ? t.day_tokens.total : 0) }),
            el("td", { class: "bn mono", title: tokensWithReviews(t), text: tok(t.tokens ? t.tokens.total : null) })
          ]));
          tr.addEventListener("click", function (e) {
            if (e && e.stopPropagation) e.stopPropagation();
            select("crew/" + enc(t.project) + "/" + enc(t.crew));
          });
          return tr;
        }))
      ])) : el("p", { class: "cab-empty", text: "No task opened, closed or worked that day." })
    ]);
  }

  // Who spent the day's tokens, biggest first, in the four buckets the
  // total is made of.
  // The biggest spenders first; the long tail of small reviewers waits
  // behind a button.
  var ACTORS_SHOWN = 12;
  var actorsAll = false;

  function dayActors(list, all) {
    var more = null;
    if (list.length > ACTORS_SHOWN) {
      more = el("button", { type: "button", class: "bback bmore", "data-focus": "board-actors-more",
        text: actorsAll ? "Show the top " + ACTORS_SHOWN : "Show all " + list.length });
      more.addEventListener("click", function (e) { e.stopPropagation(); actorsAll = !actorsAll; draw(); });
    }
    var max = 1;
    var sum = { input: 0, cache_read: 0, cache_write: 0, output: 0, total: 0 };
    var turns = 0;
    list.forEach(function (a) {
      max = Math.max(max, a.tokens.total);
      turns += a.turns;
      Object.keys(sum).forEach(function (k) { sum[k] += a.tokens[k] || 0; });
    });
    var cells = function (tk) {
      return ["input", "cache_read", "cache_write", "output"].map(function (k) {
        return el("td", { class: "bn mono" + (tk[k] ? "" : " zero"), text: tok(tk[k] || 0) });
      });
    };
    return el("section", { class: "ds", "aria-label": "Tokens by who" }, [
      el("h3", { class: "sh" }, ["Tokens by who", el("span", { class: "bleg" }, [el("i", { class: "bm" }), "Mate", el("i", { class: "bc" }), "crews"])]),
      list.length ? el("div", { class: "btab bdt" }, el("table", null, [
        el("thead", null, el("tr", null, [el("th", { class: "btl", text: "Who" })].concat(all ? [el("th", { class: "btl", text: "Project" })] : [],
          [el("th", { text: "Turns" }), el("th", { text: "Input" }), el("th", { text: "Cache read" }), el("th", { text: "Cache write" }),
            el("th", { text: "Output" }), el("th", { text: "Total" })]))),
        el("tbody", null, list.slice(0, actorsAll ? list.length : ACTORS_SHOWN).map(function (a) {
          var isMate = a.kind === "mate";
          return el("tr", null, [
            el("th", { scope: "row", text: isMate ? "Mate" + (all ? "" : " · " + a.project) : a.name }),
          ].concat(all ? [el("td", { class: "btl", text: a.project })] : [], [el("td", { class: "bn", text: String(a.turns) })], cells(a.tokens), [
            el("td", { class: "btok" }, el("div", null, [
              el("span", { class: "bbar", "aria-hidden": "true" }, el("i", { class: isMate ? "bm" : "bc", style: "width:" + (a.tokens.total / max * 100).toFixed(2) + "%" })),
              el("span", { class: "mono", text: tok(a.tokens.total) })
            ]))
          ]));
        })),
        el("tfoot", null, el("tr", null, [el("th", { scope: "row", text: "All" })].concat(all ? [el("td")] : [],
          [el("td", { class: "bn", text: String(turns) })], cells(sum), [el("td", { class: "bn mono", text: tok(sum.total) })])))
      ])) : el("p", { class: "cab-empty", text: "No turns that day." }),
      more
    ]);
  }

  function dayIncidents(list, all) {
    return el("section", { class: "ds", "aria-label": "Incidents that day" }, [
      el("h3", { class: "sh" }, ["Incidents", el("span", { text: list.length + " opened that day" })]),
      list.length ? el("div", { class: "btab bdt" }, el("table", null, [
        el("thead", null, el("tr", null, [el("th", { text: "Opened" }), el("th", { class: "btl", text: "Kind" }), el("th", { class: "btl", text: "Who" })]
          .concat(all ? [el("th", { class: "btl", text: "Project" })] : [], [el("th", { text: "Resolved" })]))),
        el("tbody", null, list.map(function (x) {
          var open = !x.resolved_at;
          var lasted = open ? null : (parseAt(x.resolved_at) || 0) - (parseAt(x.opened_at) || 0);
          return el("tr", null, [
            el("th", { scope: "row", class: "mono", title: x.opened_at, text: clock(x.opened_at) }),
            el("td", { class: "btl" }, el("span", { class: "bev " + (x.kind === "budget" || x.kind === "runtime_lost" ? "t-bad" : "t-wait") },
              INCIDENT_WORDS[x.kind] || String(x.kind).replace(/_/g, " "))),
            el("td", { class: "btl", text: x.actor_kind === "mate" ? "Mate" : x.name || "?" })
          ].concat(all ? [el("td", { class: "btl", text: x.project })] : [], [
            el("td", { class: open ? "bn bopen" : "bn", title: x.resolved_at || null, text: open ? "still open" : "after " + formatRun(lasted) })
          ]));
        }))
      ])) : el("p", { class: "cab-empty", text: "No incidents that day." })
    ]);
  }

  // ------------------------------------------------------------ the page

  function floor() {
    var f = el("main", { class: "floor", id: "floor" });
    f.addEventListener("click", function () { if (sel) select(""); });
    if (fatal) {
      f.appendChild(el("div", { class: "nothing" }, [
        el("p", { text: fatal.message }),
        fatal.reason && fatal.reason !== fatal.message ? el("p", { text: fatal.reason }) : null,
        el("p", { text: "The page keeps polling; it fills in as soon as the dashboard answers." })
      ]));
      return f;
    }
    if (!ws) { f.appendChild(el("p", { class: "boot", text: "loading…" })); return f; }
    var names = projectNames();
    if (mobile()) {
      f.setAttribute("class", "floor mfloor");
      f.appendChild(mobileHall());
      names.forEach(function (n, i) { f.appendChild(mobileRoom(n, i)); });
      if (ws) f.appendChild(newRoomDoor(true));
    } else {
      f.appendChild(el("section", { class: "hall", "aria-label": "Hallway" }, [legend(), captainDesk(), boardButton(false), hallFigures(), newRoomDoor(false)]));
      f.appendChild(el("div", { class: "rooms" }, names.map(room)));
    }
    if (!names.length) f.appendChild(el("p", { class: "nothing", text: "This workspace has no projects yet." }));
    return f;
  }

  function draw() {
    var y = window.scrollY || 0;
    var oldDrawer = root.querySelector("#drawer");
    var dy = oldDrawer ? oldDrawer.scrollTop || 0 : 0;
    var active = document.activeElement;
    var focusKey = active && active.getAttribute ? active.getAttribute("data-focus") : null;

    root.textContent = "";
    append(root, topBar());
    root.appendChild(floor());
    var scrim = el("div", { class: "scrim", "aria-hidden": "true" });
    if (!sel) scrim.setAttribute("hidden", "");
    scrim.addEventListener("click", function () { select(""); });
    root.appendChild(scrim);
    var d = drawer();
    root.appendChild(d);
    if (oldDrawer && sel) d.scrollTop = dy;
    if (toast) root.appendChild(toastNode());
    if (asking) root.appendChild(confirmDialog());
    if (sel && sel.kind === "add") refreshAdd();

    if (focusKey) {
      var again = root.querySelector("[data-focus=\"" + focusKey + "\"]");
      if (again && again.focus) again.focus({ preventScroll: true });
    }
    if (window.scrollTo) window.scrollTo(0, y);
    scheduleTrails();
  }

  // Trails: a dotted line from each figure in the hallway back to the door
  // of the room it walked out of, drawn once the layout is known.
  var trailFrame = 0;
  function scheduleTrails() {
    if (!window.requestAnimationFrame) return;
    if (trailFrame) return;
    trailFrame = window.requestAnimationFrame(function () { trailFrame = 0; drawTrails(); });
  }

  function drawTrails() {
    var f = root.querySelector("#floor");
    if (!f || !f.getBoundingClientRect || mobile()) return;
    var old = f.querySelector(".trail");
    if (old && old.parentNode) old.parentNode.removeChild(old);
    var base = f.getBoundingClientRect();
    var svg = document.createElementNS(SVG_NS, "svg");
    svg.setAttribute("class", "trail");
    svg.setAttribute("aria-hidden", "true");
    var any = false;
    var hall = f.querySelector(".hall");
    var hallBottom = hall ? hall.getBoundingClientRect().bottom : 0;
    Array.prototype.forEach.call(f.querySelectorAll(".fig"), function (fig) {
      var door = f.querySelector("[data-door=\"" + cssEscape(fig.getAttribute("data-room")) + "\"]");
      if (!door) return;
      var a = door.getBoundingClientRect();
      var b = fig.getBoundingClientRect();
      // Only a room that opens onto the hallway gets a trail; one wrapped
      // to a lower row would have its line drawn across the rooms above.
      if (a.top - hallBottom > 80) return;
      var x1 = a.left + a.width / 2 - base.left, y1 = a.top - base.top;
      var x2 = b.left + b.width / 2 - base.left, y2 = b.bottom - base.top - 4;
      var p = document.createElementNS(SVG_NS, "path");
      p.setAttribute("d", "M" + x1 + " " + y1 + " C " + x1 + " " + (y1 - 40) + ", " + x2 + " " + (y2 + 40) + ", " + x2 + " " + y2);
      // A crew still walking is drawn in the walking colour; one already
      // waiting at the door leaves a fainter trail behind it.
      if (!/\bwalk\b/.test(fig.getAttribute("class") || "")) p.setAttribute("class", "faint");
      svg.appendChild(p);
      any = true;
    });
    if (any) f.insertBefore(svg, f.firstChild);
  }

  function cssEscape(s) {
    return window.CSS && window.CSS.escape ? window.CSS.escape(s) : String(s).replace(/["\\]/g, "\\$&");
  }

  // ------------------------------------------------------------ start

  applyTheme(storedTheme());
  window.addEventListener("hashchange", onHash);
  window.addEventListener("keydown", function (e) {
    if (e.key === "Escape" && asking) { cancelAsk(); return; }
    if (e.key === "Escape" && toast && !sel) { toast = null; draw(); return; }
    if (e.key === "Escape" && sel) select("");
  });
  window.addEventListener("resize", scheduleTrails);
  try {
    var mq = window.matchMedia && window.matchMedia("(max-width: 759px)");
    if (mq && mq.addEventListener) mq.addEventListener("change", draw);
    var dq = window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)");
    if (dq && dq.addEventListener) dq.addEventListener("change", draw);
  } catch (e) { /* no media queries to follow */ }
  // Ages ("since 23m") move with the clock, not with events.
  setInterval(function () { if (ws) draw(); }, 60000);
  draw();
  loadAll().then(function () { poll(); });
})();
