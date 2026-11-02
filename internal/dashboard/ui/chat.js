// chat.js - the Mate's chat as the console's REPL draws it
// (internal/matehost/repl_view.go), for Mate City and Mate Office: the
// little markdown a reply has, and the one muted line under a turn.
//
// It is a classic script, not a module, for the same reason as
// humanize.js: a page with no build step can load it, the Office as a
// plain script and the City before its module. It declares chatMarkdown
// and chatMeta on the global scope.

/**
 * chatMarkdown renders the markdown the REPL does - headings and bold in
 * bold, `code`, bullets and numbered lines with a hanging indent, fenced
 * code unwrapped - as DOM nodes, never as HTML: a reply is text the page
 * did not write.
 */
function chatMarkdown(text) {
  var root = document.createElement("div");
  root.setAttribute("class", "md");
  var fence = null;
  var lines = String(text || "").replace(/\s+$/, "").split("\n");
  for (var i = 0; i < lines.length; i++) {
    var line = lines[i];
    if (/^\s*```/.test(line)) {
      if (fence) fence = null;
      else {
        fence = document.createElement("pre");
        root.appendChild(fence);
      }
      continue;
    }
    if (fence) {
      fence.appendChild(document.createTextNode((fence.firstChild ? "\n" : "") + line));
      continue;
    }
    var g;
    if (!line.trim()) root.appendChild(mdBlock("md-gap"));
    else if ((g = /^#{1,6} (.*)$/.exec(line))) root.appendChild(mdBlock("md-h", g[1]));
    else if ((g = /^(\s*)[-*] (.*)$/.exec(line))) root.appendChild(mdItem(g[1], "•", g[2]));
    else if ((g = /^(\s*)(\d+[.)]) (.*)$/.exec(line))) root.appendChild(mdItem(g[1], g[2], g[3]));
    else root.appendChild(mdBlock("md-p", line));
  }
  return root;
}

function mdBlock(cls, text) {
  var el = document.createElement("div");
  el.setAttribute("class", cls);
  if (text != null) mdInline(el, text);
  return el;
}

// mdItem is a bullet or numbered line: its mark, then its words hanging
// off it; leading spaces nest it.
function mdItem(indent, mark, text) {
  var el = document.createElement("div");
  el.setAttribute("class", "md-li");
  if (indent.length) el.setAttribute("style", "margin-left: " + (Math.floor(indent.length / 2) * 1.2) + "em");
  var m = document.createElement("span");
  m.setAttribute("class", "md-mark");
  m.textContent = mark;
  var body = document.createElement("span");
  mdInline(body, text);
  el.appendChild(m);
  el.appendChild(body);
  return el;
}

function mdInline(el, text) {
  var re = /\*\*([^*]+)\*\*|`([^`]+)`/g;
  var at = 0;
  var g;
  while ((g = re.exec(text))) {
    if (g.index > at) el.appendChild(document.createTextNode(text.slice(at, g.index)));
    var span = document.createElement(g[1] != null ? "b" : "code");
    span.textContent = g[1] != null ? g[1] : g[2];
    el.appendChild(span);
    at = re.lastIndex;
  }
  if (at < text.length) el.appendChild(document.createTextNode(text.slice(at)));
}

/**
 * chatMeta is what the REPL writes under a finished turn, the time it
 * ended aside: how long it took and its tokens ("4.1s · 1.2k tok").
 */
function chatMeta(item) {
  var parts = [];
  var ms = item.ms || 0;
  if (ms > 0) {
    var s = ms / 1000;
    parts.push(s < 10 ? s.toFixed(1) + "s" : s < 60 ? Math.floor(s) + "s" : Math.floor(s / 60) + "m" + ("0" + (Math.floor(s) % 60)).slice(-2) + "s");
  }
  var n = item.tokens || 0;
  if (n > 0) parts.push((n >= 1e6 ? (n / 1e6).toFixed(1) + "M" : n >= 1000 ? (n / 1e3).toFixed(1) + "k" : String(n)) + " tok");
  return parts.join(" · ");
}
