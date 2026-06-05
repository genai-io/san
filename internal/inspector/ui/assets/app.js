// Minimal trace viewer — no build step, vanilla JS.
//
// State machine:
//   1. Fetch /api/sessions, populate sidebar.
//   2. On session click, open SSE /api/sessions/{id}/stream.
//   3. Each SSE event carries one JSONL record line; append to timeline.
//   4. On row click, render the full JSON in the detail pane.

const $ = (sel) => document.querySelector(sel);
const sessions = $("#session-list");
const timeline = $("#timeline");
const detail = $("#detail-body");
const sessionMeta = $("#session-meta");
const statusEl = $("#status");
const mainEl = document.querySelector("main");
const promptToggle = $("#toggle-prompt");
const promptBody = $("#prompt-body");
const promptMeta = $("#prompt-meta");

const state = {
  currentSession: null,
  selectedRecordID: null,
  records: [],
  integrityFailures: 0,
  // Active-chain tracking: incrementally maintained on message.appended,
  // recomputed only on session.compacted. activeLeaf is the chain's tip so
  // we can decide on-chain status in O(1) for the common case.
  activeChain: new Set(),
  activeLeaf: "",
  messageParents: new Map(),
  // messageLookup maps every observed message.appended ID → {role, text
  // snippet}. Maintained incrementally in ingestForChain so the integrity
  // diff renderer doesn't re-scan state.records on every BAD click.
  messageLookup: new Map(),
  compactBoundary: "",
  // seenIDs deduplicates records across SSE reconnects. The server starts
  // each stream from offset 0 and replays the whole file, so on transient
  // disconnects (laptop sleep, localhost blip) we'd otherwise accumulate
  // duplicates. Each JSONL record carries a unique id field — use that.
  seenIDs: new Set(),
  eventSource: null,
  filters: {
    message:    true,
    inference:  true,
    tool:       true,
    system:     true,
    state:      true,
    hook:       true,
    permission: true,
  },
};

// isToolResultMessage reports whether a message.appended record carries
// tool_result content blocks. Such messages are stamped role=user on the
// wire (Anthropic's tool-result shape) but represent a tool execution
// outcome, not a user-typed prompt, and the UI distinguishes them.
function isToolResultMessage(msg) {
  if (!msg || !Array.isArray(msg.content)) return false;
  for (const b of msg.content) {
    if (b && b.type === "tool_result") return true;
  }
  return false;
}

function classify(type) {
  if (!type) return "unknown";
  if (type.startsWith("session."))         return "session";
  if (type.startsWith("message."))         return "message";
  if (type.startsWith("inference."))       return "inference";
  if (type === "tool.added" || type === "tool.removed") return "tool";
  if (type === "tool.invoked" || type === "tool.completed") return "tool";
  if (type.startsWith("system."))          return "system";
  if (type === "session.state.patched") return "state";
  if (type === "hook.fired")            return "hook";
  if (type.startsWith("permission."))   return "permission";
  return "unknown";
}

function fmtTime(s) {
  if (!s) return "";
  const d = new Date(s);
  if (isNaN(d.getTime())) return "";
  return d.toLocaleTimeString();
}

function labelFor(rec) {
  let label = rec.type;
  if (rec.message) {
    // Tool results travel as role=user on the wire (Anthropic API shape)
    // but are functionally distinct from a real user prompt — surface that
    // distinction in the label so the timeline doesn't conflate them.
    label += "  " + (isToolResultMessage(rec.message) ? "tool_result" : (rec.message.role || ""));
  }
  if (rec.system)    label += "  " + (rec.system.name || "");
  if (rec.tool && rec.tool.schema) label += "  " + rec.tool.schema.name;
  if (rec.tool && rec.tool.name)   label += "  " + rec.tool.name;
  if (rec.inference) label += "  turn " + (rec.inference.turn || "?");
  if (rec.hook)      label += "  " + (rec.hook.event || "") + " · " + (rec.hook.outcome || "");
  if (rec.permission) {
    label += "  " + (rec.permission.tool || "") +
      (rec.permission.decision ? " → " + rec.permission.decision : " (ask)");
    if (rec.permission.source === "config") label += " · auto";
    if (rec.permission.scope && rec.permission.scope !== "once") {
      label += " (" + rec.permission.scope + ")";
    }
    const summary = permInputSummary(rec.permission.tool, rec.permission.input);
    if (summary) label += "  " + summary;
    if (rec.permission.requestId) label += "  #" + rec.permission.requestId.slice(0, 8);
  }
  return label;
}

// permInputSummary returns a short, terminal-style preview of the tool args
// for the timeline label. Falls back to the full JSON for unrecognized tools
// so the label is always informative even when a new tool lands.
function permInputSummary(tool, input) {
  if (!input || typeof input !== "object") return "";
  switch (tool) {
    case "Bash":
      return input.command ? "`" + truncate(input.command, 64) + "`" : "";
    case "Read":
    case "Edit":
    case "Write":
      return input.file_path ? truncate(input.file_path, 64) : "";
    case "Skill":
      return input.skill ? "/" + input.skill : "";
    case "Agent":
      return input.subagent_type ? "→ " + input.subagent_type : "";
    case "Glob":
    case "Grep":
      return input.pattern ? truncate(input.pattern, 48) : "";
    default: {
      // Pick the first short string field as a hint.
      for (const k of Object.keys(input)) {
        const v = input[k];
        if (typeof v === "string" && v.length > 0 && v.length < 80) return truncate(v, 64);
      }
      return "";
    }
  }
}

function truncate(s, n) {
  if (typeof s !== "string") return "";
  return s.length > n ? s.slice(0, n - 1) + "…" : s;
}

function setStatus(text, live) {
  statusEl.textContent = text;
  statusEl.classList.toggle("live", !!live);
}

async function loadSessions() {
  setStatus("indexing");
  try {
    const r = await fetch("/api/sessions");
    if (!r.ok) throw new Error(r.statusText);
    const list = await r.json();
    renderSessionList(list);
    setStatus("");
  } catch (e) {
    setStatus("error · " + e.message);
  }
}

// signature is what we diff against to decide whether to re-render. Includes
// every field that can change while the viewer is open (new sessions appearing,
// title updates as user types, size growing).
function sessionsSignature(list) {
  return list.map((s) => s.id + ":" + (s.size || 0) + ":" + (s.title || "")).join("|");
}

let lastSessionsSig = null;

async function pollSessions() {
  try {
    const r = await fetch("/api/sessions");
    if (!r.ok) return;
    const list = await r.json();
    const sig = sessionsSignature(list);
    if (sig === lastSessionsSig) return;
    lastSessionsSig = sig;
    renderSessionList(list);
  } catch (_) {
    // Transient errors are fine — next tick will retry.
  }
}

function renderSessionList(list) {
  lastSessionsSig = sessionsSignature(list);
  // Preserve the active selection across re-renders so polling doesn't yank
  // the user out of the session they're inspecting.
  const activeID = state.currentSession;
  sessions.innerHTML = "";
  if (!list.length) {
    const li = document.createElement("li");
    li.className = "session-empty";
    li.textContent = "No sessions in this project yet.";
    sessions.appendChild(li);
    return;
  }
  for (const s of list) {
    const li = document.createElement("li");
    li.dataset.id = s.id;
    if (s.id === activeID) li.classList.add("active");
    // s.id is derived from the transcripts/ filesystem listing. Today it
    // is always a UUID, but filename-shaped data flowing into innerHTML
    // shouldn't be trusted on principle. Escape consistently with title.
    li.innerHTML =
      '<div>' + escapeHTML(s.title || "(untitled)") + '</div>' +
      '<span class="id">' + escapeHTML(s.id.slice(0, 12)) + '… · ' + Math.round(s.size / 1024) + 'KB</span>';
    li.addEventListener("click", () => openSession(s.id, li));
    sessions.appendChild(li);
  }
}

function openSession(id, li) {
  for (const x of sessions.querySelectorAll("li.active")) x.classList.remove("active");
  if (li) li.classList.add("active");

  if (state.eventSource) {
    state.eventSource.close();
    state.eventSource = null;
  }
  state.currentSession = id;
  state.selectedRecordID = null;
  state.records = [];
  state.seenIDs.clear();
  state.integrityFailures = 0;
  state.activeChain = new Set();
  state.activeLeaf = "";
  state.messageParents = new Map();
  state.messageLookup = new Map();
  state.compactBoundary = "";
  refreshIntegrityBanner();
  timeline.innerHTML = "";
  detail.textContent = "Click a record to inspect its payload.";
  promptBody.replaceChildren(emptyNote("Select a record to see the system prompt the model sees at that point."));
  promptMeta.textContent = "— at selected record";
  sessionMeta.textContent = id;

  const es = new EventSource("/api/sessions/" + encodeURIComponent(id) + "/stream");
  state.eventSource = es;
  setStatus("connecting");

  es.onopen = () => setStatus("recording", true);
  es.onerror = () => setStatus("offline");
  es.onmessage = (ev) => {
    let rec;
    try {
      rec = JSON.parse(ev.data);
    } catch (e) {
      return;
    }
    // Browsers transparently reconnect EventSource on transient drops
    // (laptop sleep, localhost blip). The server starts each stream from
    // offset 0 and replays the whole file, so without dedup the timeline
    // would balloon across reconnects. Each record's id is unique on disk.
    if (rec.id && state.seenIDs.has(rec.id)) return;
    if (rec.id) state.seenIDs.add(rec.id);
    state.records.push(rec);
    ingestForChain(rec);
    appendRow(rec, state.records.length - 1);
  };
}

// ingestForChain keeps the active-chain set incremental. Each new
// message.appended is on the chain if its parent is the current leaf; the
// only event that retroactively kicks messages off the chain is
// session.compacted, where we recompute and refresh the DOM once.
function ingestForChain(rec) {
  if (rec.type === "message.appended" && rec.message && rec.message.messageId) {
    const id = rec.message.messageId;
    const parent = rec.parentId || "";
    state.messageParents.set(id, parent);
    state.messageLookup.set(id, {
      role: isToolResultMessage(rec.message) ? "tool" : (rec.message.role || "?"),
      text: firstTextSnippet(rec.message.content),
    });
    if (parent === "" || parent === state.activeLeaf) {
      state.activeChain.add(id);
      state.activeLeaf = id;
    }
    return;
  }
  if (rec.type === "session.compacted") {
    state.compactBoundary = (rec.session && rec.session.summaryMessageId) || "";
    state.activeChain = computeActiveChain(state.messageParents, state.compactBoundary);
    state.activeLeaf = lastChainLeaf(state.activeChain);
    rehighlightMessageRows();
  }
}

function lastChainLeaf(chain) {
  let leaf = "";
  for (const id of chain) leaf = id;
  return leaf;
}

// computeActiveChain runs only on session.compacted — walks back from the
// last message without a child to the boundary (inclusive).
function computeActiveChain(parents, boundary) {
  if (parents.size === 0) return new Set();
  const hasChild = new Set();
  for (const [, p] of parents) if (p) hasChild.add(p);
  let leaf = "";
  for (const id of parents.keys()) {
    if (!hasChild.has(id)) leaf = id;
  }
  if (!leaf) return new Set();
  const chain = new Set();
  let cur = leaf;
  while (cur && !chain.has(cur)) {
    chain.add(cur);
    if (boundary && cur === boundary) break;
    cur = parents.get(cur) || "";
  }
  return chain;
}

function rehighlightMessageRows() {
  for (const row of timeline.querySelectorAll(".row.message")) {
    const idx = Number(row.dataset.idx);
    const rec = state.records[idx];
    if (!rec || !rec.message) continue;
    row.classList.toggle("off-chain", !state.activeChain.has(rec.message.messageId));
  }
}

function appendRow(rec, idx) {
  const klass = classify(rec.type);
  if (!passesFilter(klass)) return;

  const row = document.createElement("div");
  row.className = "row " + klass;
  row.dataset.idx = idx;
  // Mark messages that aren't on the current active chain so users can see
  // at a glance what the model still sees vs. what was compacted out or
  // belongs to an abandoned branch.
  if (klass === "message" && rec.message && rec.message.messageId &&
      !state.activeChain.has(rec.message.messageId)) {
    row.classList.add("off-chain");
  }
  // Tool-result messages share the "tool" color so they read as part of the
  // tool-call flow rather than a fresh user prompt.
  if (klass === "message" && isToolResultMessage(rec.message)) {
    row.classList.add("tool-result");
  }
  // Auto-decided permissions (rule-matched, no human in the loop) are dimmed
  // so the eye lands on the actual decision points first.
  if (klass === "permission" && rec.permission && rec.permission.source === "config") {
    row.classList.add("auto-decided");
  }
  row.innerHTML =
    '<span class="time">' + fmtTime(rec.time) + '</span>' +
    '<span class="swatch"></span>' +
    '<span class="label">' + escapeHTML(labelFor(rec)) + '</span>';
  row.addEventListener("click", () => showDetail(idx, row));
  timeline.appendChild(row);

  // Auto-scroll if user is near the bottom.
  if (timeline.scrollHeight - timeline.scrollTop - timeline.clientHeight < 100) {
    timeline.scrollTop = timeline.scrollHeight;
  }
}

async function showDetail(idx, row) {
  for (const x of timeline.querySelectorAll(".row.active")) x.classList.remove("active");
  row.classList.add("active");
  const rec = state.records[idx];
  state.selectedRecordID = rec.id;
  // Keep the system-prompt panel in sync with the selected record.
  refreshPromptPanel(rec);
  const raw = "Record\n" + JSON.stringify(rec, null, 2);

  // Replay state only auto-loads for inference.requested (the "what did the
  // model see?" record); other types show only the raw record + an opt-in
  // toggle for the cumulative replay state.
  if (rec.type !== "inference.requested") {
    const nodes = [textBlock(raw)];
    if (rec.type === "message.appended" && rec.message) {
      nodes.push(...messageDetailNodes(rec.message));
    }
    if ((rec.type === "tool.added" || rec.type === "tool.removed") &&
        rec.tool && rec.tool.schema) {
      nodes.push(sectionHeader("Tool schema"));
      nodes.push(renderToolSchema(rec.tool.schema));
    }
    if (rec.type === "permission.required" || rec.type === "permission.decided") {
      const perm = rec.permission;
      if (perm && perm.input) {
        nodes.push(sectionHeader("Tool input · model intent"));
        nodes.push(jsonBlock(perm.input));
      }
      if (perm && perm.detail) {
        nodes.push(sectionHeader("Resolved context · what the user saw"));
        nodes.push(jsonBlock(perm.detail));
      }
      if (perm && Array.isArray(perm.optionsOffered) && perm.optionsOffered.length > 0) {
        nodes.push(sectionHeader("Options offered (" + perm.optionsOffered.length + ")"));
        const wrap = document.createElement("div");
        wrap.style.cssText = "margin: 4px 0 0 1em; font-family: var(--font-mono); font-size: 0.78rem;";
        perm.optionsOffered.forEach((label, i) => {
          const row = document.createElement("div");
          row.style.cssText = "padding: 2px 0; color: var(--bone-2);";
          row.textContent = (i + 1) + ". " + label;
          wrap.appendChild(row);
        });
        nodes.push(wrap);
      }
    }
    nodes.push(replayToggle(rec));
    detail.replaceChildren(...nodes);
    return;
  }

  detail.replaceChildren(textBlock(raw + "\n\nLoading context at this inference…"));
  try {
    const replay = await fetchReplay(rec.id);
    updateIntegrityFromReplay(rec.id, replay);
    detail.replaceChildren(...inferenceDetailNodes(raw, replay));
  } catch (e) {
    detail.replaceChildren(textBlock(raw + "\n\nReplay state unavailable: " + e.message));
  }
}

// updateIntegrityFromReplay folds digest mismatches into the running count
// and refreshes the banner. Counts inferences, not individual check entries.
function updateIntegrityFromReplay(recordID, replay) {
  if (!Array.isArray(replay.integrity)) return;
  const bad = replay.integrity.some((c) => c && c.ok === false);
  // Stash flag on the record so filter re-renders preserve the mark.
  const idx = state.records.findIndex((r) => r.id === recordID);
  if (idx < 0) return;
  const rec = state.records[idx];
  const previously = !!rec._integrityBad;
  rec._integrityBad = bad;
  if (bad && !previously) state.integrityFailures++;
  if (!bad && previously) state.integrityFailures = Math.max(0, state.integrityFailures - 1);
  for (const row of timeline.querySelectorAll(".row.inference")) {
    if (Number(row.dataset.idx) === idx) {
      row.classList.toggle("integrity-bad", bad);
      break;
    }
  }
  refreshIntegrityBanner();
}

function refreshIntegrityBanner() {
  let banner = document.getElementById("integrity-banner");
  if (state.integrityFailures === 0) {
    if (banner) banner.remove();
    return;
  }
  if (!banner) {
    banner = document.createElement("div");
    banner.id = "integrity-banner";
    banner.className = "integrity-banner";
    // Insert above the timeline so it can't be scrolled past.
    timeline.parentNode.insertBefore(banner, timeline);
  }
  banner.textContent = "⚠ " + state.integrityFailures + " inference record(s) failed integrity check — system/tools/messages disagree with what the model claims it saw. Click an inference row to inspect.";
}

// inferenceDetailNodes lays out the inference context as three blocks:
//   1. raw record (what the JSONL holds)
//   2. composed system prompt (the literal string sent to the model)
//   3. tool list (name + description; click a row to expand the schema)
// Plus a small integrity summary and the message-chain IDs.
function inferenceDetailNodes(raw, replay) {
  const nodes = [textBlock(raw)];

  nodes.push(sectionHeader("Composed system prompt"));
  nodes.push(textBlock(replay.composedSystem || "(empty)"));

  nodes.push(sectionHeader("Tools available (" + (replay.tools || []).length + ")"));
  nodes.push(toolsList(replay.tools || []));

  if (replay.messages && replay.messages.length) {
    nodes.push(sectionHeader("Active message chain (" + replay.messages.length + ")"));
    nodes.push(messageChainList(replay.messages));
  }

  if (replay.integrity && replay.integrity.length) {
    nodes.push(sectionHeader("Integrity"));
    nodes.push(renderIntegrity(replay.integrity));
  }

  return nodes;
}

// renderIntegrity renders the per-check results. systemDigest/toolsDigest are
// opaque hashes — we just show OK/BAD plus the hash pair so the user knows to
// inspect the composed system / tools list above for divergence. messageIds
// is the diagnostic-heavy one: when it fails, we LCS-diff the two ID arrays
// and render each row with its role + text snippet so the user can actually
// see which messages were missing/extra.
function renderIntegrity(checks) {
  const wrap = document.createElement("div");
  for (const c of checks) {
    if (c.field === "messageIds" && !c.ok) {
      wrap.appendChild(renderMessageIDsDiff(c, state.messageLookup));
      continue;
    }
    wrap.appendChild(renderDigestCheck(c));
  }
  return wrap;
}

// Diff status enum — shared between diffMessageIDs (writer), renderDiffRow
// (reader), and the CSS class suffix. Keeping them as constants prevents
// silent typos from rendering as default styling.
const DIFF_MATCH   = "match";
const DIFF_MISSING = "missing";
const DIFF_EXTRA   = "extra";
const DIFF_GLYPH = {
  [DIFF_MATCH]:   "✓ ",
  [DIFF_MISSING]: "− ",
  [DIFF_EXTRA]:   "+ ",
};

function renderDigestCheck(c) {
  const line = document.createElement("div");
  line.className = "integrity-line";
  line.classList.add(c.ok ? "integrity-ok" : "integrity-bad-row");
  const status = c.ok ? "OK " : "BAD";
  let body = "  " + status + "  " + c.field;
  if (!c.ok) {
    body += "\n        expected " + JSON.stringify(c.expected) +
            "\n        got      " + JSON.stringify(c.got);
  }
  line.textContent = body;
  return line;
}

function renderMessageIDsDiff(c, lookup) {
  const wrap = document.createElement("div");
  // expected/got arrive as JSON arrays — the wire type is json.RawMessage,
  // not a JSON-encoded string. Older transcripts may still carry strings;
  // unwrap defensively.
  const expected = toIDArray(c.expected);
  const got      = toIDArray(c.got);

  const header = document.createElement("div");
  header.className = "integrity-line integrity-bad-row";
  header.textContent = "  BAD  messageIds — " +
    expected.length + " expected vs " + got.length + " replayed";
  wrap.appendChild(header);

  const rows = diffMessageIDs(expected, got);
  const table = document.createElement("div");
  table.className = "integrity-diff";
  for (const row of rows) {
    table.appendChild(renderDiffRow(row, lookup));
  }
  wrap.appendChild(table);

  const legend = document.createElement("div");
  legend.className = "integrity-legend";
  legend.textContent = "−  missing in replay (recorded but no longer in active chain)        +  extra in replay (in active chain but not recorded)";
  wrap.appendChild(legend);
  return wrap;
}

function toIDArray(v) {
  if (Array.isArray(v)) return v;
  if (typeof v === "string") {
    try { const a = JSON.parse(v); return Array.isArray(a) ? a : []; } catch (_) { return []; }
  }
  return [];
}

function renderDiffRow(row, lookup) {
  const line = document.createElement("div");
  line.className = "integrity-diff-row integrity-" + row.status;
  const glyph = DIFF_GLYPH[row.status] || "  ";

  const idShort = row.id.length > 16 ? row.id.slice(0, 16) + "…" : row.id;
  const info = lookup.get(row.id);
  const role = (info ? info.role : "?").padEnd(10).slice(0, 10);
  const text = info ? info.text : "(no matching message.appended in this transcript)";
  line.textContent = glyph + idShort + "  " + role + "  " + text;
  return line;
}

function firstTextSnippet(content) {
  if (!Array.isArray(content)) return "";
  for (const b of content) {
    if (!b) continue;
    if (b.type === "text" && b.text) {
      const t = b.text.replace(/\s+/g, " ").trim();
      return t.length > 80 ? t.slice(0, 80) + "…" : t;
    }
    if (b.type === "tool_use")    return "[tool_use: " + (b.name || "?") + "]";
    if (b.type === "tool_result") return "[tool_result]";
    if (b.type === "image")       return "[image]";
  }
  return "";
}

// diffMessageIDs returns alignment rows for two ID sequences. Common shapes
// (one side is a prefix or suffix of the other, or sequences are identical)
// are detected in O(n+m) before falling back to LCS DP; the DP is O(n×m)
// memory, which would OOM the browser at multi-thousand-message chains.
function diffMessageIDs(expected, got) {
  const n = expected.length, m = got.length;

  // Exact match.
  if (n === m) {
    let same = true;
    for (let i = 0; i < n; i++) {
      if (expected[i] !== got[i]) { same = false; break; }
    }
    if (same) return expected.map((id) => ({ status: DIFF_MATCH, id }));
  }

  // Common prefix.
  let p = 0;
  while (p < n && p < m && expected[p] === got[p]) p++;
  // Common suffix.
  let s = 0;
  while (s < n - p && s < m - p && expected[n - 1 - s] === got[m - 1 - s]) s++;

  const expectedMid = expected.slice(p, n - s);
  const gotMid      = got.slice(p, m - s);

  // If one side's middle is empty, the other side is purely missing/extra.
  let midRows;
  if (expectedMid.length === 0 || gotMid.length === 0) {
    midRows = []
      .concat(expectedMid.map((id) => ({ status: DIFF_MISSING, id })))
      .concat(gotMid.map((id) => ({ status: DIFF_EXTRA, id })));
  } else {
    midRows = lcsDiff(expectedMid, gotMid);
  }

  const rows = [];
  for (let i = 0; i < p; i++) rows.push({ status: DIFF_MATCH, id: expected[i] });
  rows.push(...midRows);
  for (let i = n - s; i < n; i++) rows.push({ status: DIFF_MATCH, id: expected[i] });
  return rows;
}

function lcsDiff(expected, got) {
  const n = expected.length, m = got.length;
  const dp = new Array(n + 1);
  for (let i = 0; i <= n; i++) dp[i] = new Int32Array(m + 1);
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      dp[i][j] = expected[i] === got[j]
        ? dp[i + 1][j + 1] + 1
        : Math.max(dp[i + 1][j], dp[i][j + 1]);
    }
  }
  const rows = [];
  let i = 0, j = 0;
  while (i < n && j < m) {
    if (expected[i] === got[j]) {
      rows.push({ status: DIFF_MATCH, id: expected[i] });
      i++; j++;
    } else if (dp[i + 1][j] >= dp[i][j + 1]) {
      rows.push({ status: DIFF_MISSING, id: expected[i] });
      i++;
    } else {
      rows.push({ status: DIFF_EXTRA, id: got[j] });
      j++;
    }
  }
  while (i < n) { rows.push({ status: DIFF_MISSING, id: expected[i] }); i++; }
  while (j < m) { rows.push({ status: DIFF_EXTRA, id: got[j] });       j++; }
  return rows;
}

// messageDetailNodes lays out a message.appended record as a per-block
// breakdown so harness-injected content (skill directory, memory blocks,
// hook additionalContext, etc.) is visible at a glance. Without this view
// they're hard to spot inside the raw JSON dump because they live as extra
// text blocks marked with source="reminder" rather than as separate events.
function messageDetailNodes(msg) {
  const blocks = Array.isArray(msg.content) ? msg.content : [];
  if (blocks.length === 0) return [];

  const nodes = [sectionHeader("Content blocks (" + blocks.length + ")")];

  // Group blocks by kind so harness injections cluster together. "author"
  // here means the message's role (user/assistant/etc.) — the label is
  // resolved per group from msg.role so an assistant text block reads as
  // "Assistant text", not the misleading generic "Author text".
  const byKind = {};
  for (const b of blocks) {
    const kind = blockKind(b);
    (byKind[kind] = byKind[kind] || []).push(b);
  }

  const order = ["author", "reminder", "hook", "command", "tool_use", "tool_result", "thinking", "image"];
  const sorted = order.filter((k) => byKind[k]).concat(Object.keys(byKind).filter((k) => !order.includes(k)));

  for (const kind of sorted) {
    const arr = byKind[kind];
    const wrap = document.createElement("div");
    wrap.style.cssText = "margin: 0.5em 0;";
    const tag = document.createElement("div");
    // Collect the distinct subtypes within this kind group so reminder-style
    // groups can show the provider list inline ("Harness reminder · skills-
    // directory, memory-user").
    const subtypes = Array.from(new Set(arr.map(blockSubtype).filter(Boolean)));
    const label = kindLabel(kind, msg.role) +
      (subtypes.length ? " · " + subtypes.join(", ") : "") +
      "  (" + arr.length + ")";
    tag.textContent = label;
    tag.style.cssText = kindStyle(kind);
    wrap.appendChild(tag);
    for (const b of arr) {
      wrap.appendChild(renderContentBlock(b));
    }
    nodes.push(wrap);
  }
  return nodes;
}

function blockKind(b) {
  if (!b || !b.type) return "unknown";
  if (b.type === "text") {
    // source may be "reminder", "reminder:skills-directory", "hook", "command", "".
    // Group by the prefix so all reminder providers cluster together but the
    // subtype shows in the label.
    if (!b.source) return "author";
    const prefix = b.source.split(":")[0];
    return prefix || "author";
  }
  return b.type;
}

// blockSubtype pulls the colon-suffixed detail out of source (e.g. "reminder:skills-directory" → "skills-directory").
function blockSubtype(b) {
  if (!b || b.type !== "text" || !b.source) return "";
  const idx = b.source.indexOf(":");
  return idx >= 0 ? b.source.slice(idx + 1) : "";
}

function kindLabel(kind, role) {
  if (kind === "author") {
    switch (role) {
      case "assistant":   return "Assistant text";
      case "user":        return "User text";
      case "tool":
      case "tool_result": return "Tool result text";
      case "notice":      return "Notice text";
      case "system":      return "System text";
      default:            return role ? (capitalize(role) + " text") : "Author text";
    }
  }
  const labels = {
    reminder: "Harness reminder (<system-reminder>)",
    hook: "Hook-injected text",
    command: "Slash command expansion",
    tool_use: "Tool call",
    tool_result: "Tool result",
    thinking: "Model thinking",
    image: "Image",
  };
  return labels[kind] || ("Other (" + kind + ")");
}

function capitalize(s) {
  return s ? s[0].toUpperCase() + s.slice(1) : s;
}

function kindStyle(kind) {
  // Harness injections in amber so users can spot them; author content stays neutral.
  const harness = kind === "reminder" || kind === "hook" || kind === "command";
  const color = harness ? "#d49a3e" : "var(--fg-dim)";
  return "font-weight: bold; color: " + color + "; margin: 2px 0;";
}

function renderContentBlock(b) {
  // Text blocks (author, reminder, hook, command, etc.) and thinking blocks
  // are all natural prose / markdown — render them formatted by default with
  // a "Show raw" toggle so users can still inspect the literal bytes (tags,
  // whitespace, escaping).
  if (b && b.type === "text") {
    return renderProseBlock(b.text || "");
  }
  if (b && b.type === "thinking") {
    return renderProseBlock(b.thinking || "");
  }

  const pre = document.createElement("pre");
  pre.style.cssText = blockPreStyle();
  let body = "";
  switch (b && b.type) {
    case "tool_use":
      body = "name: " + (b.name || "") + "\nid: " + (b.id || "") +
        "\ninput: " + (typeof b.input === "string" ? b.input : JSON.stringify(b.input, null, 2));
      break;
    case "tool_result":
      body = "tool_use_id: " + (b.tool_use_id || "") +
        (b.is_error ? "\nis_error: true" : "") +
        "\ncontent:\n" + renderToolResultContent(b.content);
      break;
    case "image":
      body = "(image " + (b.imageSource && b.imageSource.media_type || "") + ")";
      break;
    default:
      body = JSON.stringify(b, null, 2);
  }
  pre.textContent = body;
  return pre;
}

function blockPreStyle() {
  return "margin: 2px 0 6px 1em; padding: 4px 8px; background: rgba(255,255,255,0.03); border-left: 2px solid var(--fg-dim); white-space: pre-wrap; word-break: break-word;";
}

// renderProseBlock shows text/thinking content with formatted markdown by
// default and a "Show raw" toggle. Markdown rendering surfaces the structure
// of harness-injected reminders (skill listings, memory blocks) and assistant
// responses; the raw view preserves exact bytes for debugging escape/tag
// issues.
function renderProseBlock(text) {
  const wrap = document.createElement("div");
  wrap.className = "prose-block";

  const formatted = document.createElement("div");
  formatted.className = "prompt-body prose-inline";
  formatted.innerHTML = renderMarkdown(text || "");
  wrap.appendChild(formatted);

  const toggle = document.createElement("details");
  toggle.className = "prose-block-raw";
  const summary = document.createElement("summary");
  summary.textContent = "Show raw";
  toggle.appendChild(summary);
  const rawPre = document.createElement("pre");
  rawPre.style.cssText = blockPreStyle() + " margin-top: 4px;";
  rawPre.textContent = text || "";
  toggle.appendChild(rawPre);
  wrap.appendChild(toggle);

  return wrap;
}

function renderToolResultContent(c) {
  if (!c) return "";
  if (typeof c === "string") return c;
  if (!Array.isArray(c)) return JSON.stringify(c, null, 2);
  return c.map((sub) => (sub && sub.type === "text" ? (sub.text || "") : JSON.stringify(sub, null, 2))).join("\n");
}

function sectionHeader(text) {
  const h = document.createElement("div");
  h.className = "section-header";
  h.textContent = text;
  return h;
}

function toolsList(tools) {
  const wrap = document.createElement("div");
  for (const t of tools) {
    const item = document.createElement("details");
    item.style.margin = "2px 0";
    const summary = document.createElement("summary");
    summary.style.cursor = "pointer";
    const firstLine = (t.description || "").split("\n")[0];
    summary.textContent = (t.name || "(unnamed)") + (firstLine ? " — " + firstLine : "");
    item.appendChild(summary);
    item.appendChild(renderToolSchema(t));
    wrap.appendChild(item);
  }
  return wrap;
}

// messageChainList renders each active-chain message as a collapsible row:
// the summary shows "role  id  — preview" and expanding it reveals the full
// content blocks (text, tool_use, tool_result, …) via renderContentBlock.
function messageChainList(messages) {
  const wrap = document.createElement("div");
  for (const m of messages) {
    const item = document.createElement("details");
    item.style.margin = "2px 0";
    const summary = document.createElement("summary");
    summary.style.cursor = "pointer";
    summary.textContent = m.role + "  " + m.id + messagePreview(m);
    item.appendChild(summary);

    const body = document.createElement("div");
    const blocks = m.content || [];
    if (blocks.length) {
      for (const b of blocks) {
        body.appendChild(renderContentBlock(b));
      }
    } else {
      const empty = document.createElement("pre");
      empty.style.cssText = blockPreStyle();
      empty.textContent = "(no content)";
      body.appendChild(empty);
    }
    item.appendChild(body);
    wrap.appendChild(item);
  }
  return wrap;
}

// messagePreview extracts a short one-line hint for the collapsed summary so a
// row is identifiable without expanding it.
function messagePreview(m) {
  const blocks = m.content || [];
  for (const b of blocks) {
    if (b && b.type === "text" && b.text) {
      const oneLine = b.text.replace(/\s+/g, " ").trim();
      if (oneLine) return "  — " + (oneLine.length > 60 ? oneLine.slice(0, 60) + "…" : oneLine);
    }
  }
  for (const b of blocks) {
    if (b && b.type === "tool_use") return "  — " + (b.name || "tool_use");
    if (b && b.type === "tool_result") return "  — tool_result";
    if (b && b.type === "image") return "  — image";
  }
  return "";
}

// renderToolSchema produces a structured DOM for one tool schema:
//   - Description as rendered markdown (the Agent tool's "Available agent
//     types" listing is markdown-ish, as are most tool descriptions — raw
//     JSON dump made them unreadable).
//   - Input schema as a small JSON pre, collapsed by default for tools whose
//     schemas are large.
// Used both inside the inference-replay tools list and for the
// tool.added/tool.removed record detail view.
function renderToolSchema(t) {
  const wrap = document.createElement("div");
  wrap.className = "tool-schema";

  if (t.description) {
    const descHeader = document.createElement("div");
    descHeader.className = "tool-schema-label";
    descHeader.textContent = "Description";
    wrap.appendChild(descHeader);

    const descBody = document.createElement("div");
    descBody.className = "prompt-body prose-inline";
    descBody.innerHTML = renderMarkdown(t.description);
    wrap.appendChild(descBody);
  }

  const schema = t.input_schema || t.parameters;
  if (schema) {
    const schemaDetails = document.createElement("details");
    schemaDetails.className = "tool-schema-details";
    const schemaSummary = document.createElement("summary");
    schemaSummary.textContent = "Input schema";
    schemaDetails.appendChild(schemaSummary);
    const schemaPre = document.createElement("pre");
    schemaPre.className = "tool-schema-pre";
    schemaPre.textContent = JSON.stringify(schema, null, 2);
    schemaDetails.appendChild(schemaPre);
    wrap.appendChild(schemaDetails);
  }
  return wrap;
}

async function fetchReplay(recordID) {
  const url = "/api/sessions/" + encodeURIComponent(state.currentSession) +
    "/state/" + encodeURIComponent(recordID);
  const r = await fetch(url);
  if (!r.ok) throw new Error(r.statusText);
  return r.json();
}

function textBlock(text) {
  const pre = document.createElement("pre");
  pre.textContent = text;
  return pre;
}

function jsonBlock(value) {
  const pre = document.createElement("pre");
  pre.style.cssText = "margin: 4px 0 0 1em; padding: 6px 10px; background: rgba(255,255,255,0.03); border-left: 2px solid var(--c-permission); white-space: pre-wrap; word-break: break-word;";
  pre.textContent = typeof value === "string" ? value : JSON.stringify(value, null, 2);
  return pre;
}

// "Show cumulative context" button: lets users opt in to the replay state
// dump for non-inference records when they really want it (e.g. confirming a
// system section is in scope at a particular message).
function replayToggle(rec) {
  const wrap = document.createElement("div");
  wrap.style.marginTop = "1em";
  const btn = document.createElement("button");
  btn.textContent = "Show cumulative context at this record";
  btn.style.cssText = "font: inherit; color: var(--fg-dim); background: none; border: 1px solid var(--fg-dim); padding: 4px 8px; cursor: pointer;";
  btn.addEventListener("click", async () => {
    btn.disabled = true;
    btn.textContent = "Loading…";
    try {
      const replay = await fetchReplay(rec.id);
      const pre = textBlock(JSON.stringify(condenseReplay(rec, replay), null, 2));
      wrap.replaceChildren(pre);
    } catch (e) {
      btn.textContent = "Unavailable: " + e.message;
    }
  });
  wrap.appendChild(btn);
  return wrap;
}

// condenseReplay drops fields that exactly duplicate the raw record (so
// session.started doesn't show provider/model/cwd/etc. twice) and rewrites
// the tools list to just names — the viewer is for context overview, not
// schema browsing. The raw record (shown above) still carries the full
// schema for anyone who wants it.
function condenseReplay(rec, replay) {
  const out = Object.assign({}, replay);
  if (rec.type === "session.started" && rec.session) {
    for (const k of ["provider", "model", "maxTokens", "agentId", "cwd"]) {
      if (rec.session[k] === out[k] || rec.cwd === out[k]) delete out[k];
    }
  }
  if (Array.isArray(out.tools)) {
    out.tools = out.tools.map((t) => (t && t.name) || t);
  }
  // Empty arrays/nulls aren't informative when the record itself is the
  // cause (e.g. system is null at session.started before any section is
  // added). Drop them for visual clarity.
  for (const k of ["system", "tools", "messages", "integrity"]) {
    const v = out[k];
    if (v === null || (Array.isArray(v) && v.length === 0)) delete out[k];
  }
  return out;
}

function passesFilter(klass) {
  if (klass === "state")      return state.filters.state;
  if (klass === "tool")       return state.filters.tool;
  if (klass === "system")     return state.filters.system;
  if (klass === "inference")  return state.filters.inference;
  if (klass === "message")    return state.filters.message;
  if (klass === "hook")       return state.filters.hook;
  if (klass === "permission") return state.filters.permission;
  return true;
}

function wireFilters() {
  const map = {
    "filter-state":      "state",
    "filter-tools":      "tool",
    "filter-system":     "system",
    "filter-inference":  "inference",
    "filter-message":    "message",
    "filter-hook":       "hook",
    "filter-permission": "permission",
  };
  for (const [id, key] of Object.entries(map)) {
    const el = document.getElementById(id);
    el.addEventListener("change", () => {
      state.filters[key] = el.checked;
      // Re-render timeline against current filters.
      timeline.innerHTML = "";
      state.records.forEach((r, i) => appendRow(r, i));
    });
  }
}

function escapeHTML(s) {
  return String(s)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

// ─── System prompt panel ───────────────────────────────────────────────────
//
// The panel shows the composed system prompt (the literal string the model
// receives) at the currently selected record. Updating on every record click
// turns the timeline into a scrubber for "how does the system prompt evolve
// over the session?" — much cleaner than re-reading dozens of system.section.*
// raw records.

function wirePromptPanel() {
  const setOpen = (on) => {
    mainEl.classList.toggle("show-prompt", on);
    promptToggle.classList.toggle("on", on);
    promptToggle.textContent = on ? "System prompt ▾" : "System prompt ▸";
    if (on && state.selectedRecordID) {
      const idx = state.records.findIndex((r) => r.id === state.selectedRecordID);
      if (idx >= 0) refreshPromptPanel(state.records[idx]);
    }
  };
  promptToggle.addEventListener("click", () =>
    setOpen(!mainEl.classList.contains("show-prompt")));
  document.getElementById("prompt-close").addEventListener("click", () => setOpen(false));
  document.getElementById("prompt-backdrop").addEventListener("click", () => setOpen(false));
  document.getElementById("prompt-expand").addEventListener("click", () =>
    mainEl.classList.toggle("prompt-wide"));
  // Esc closes the panel — common modal pattern.
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && mainEl.classList.contains("show-prompt")) setOpen(false);
  });
}

async function refreshPromptPanel(rec) {
  if (!mainEl.classList.contains("show-prompt")) return;
  promptMeta.textContent = "— at " + (rec.type || "?") + " " + (rec.id ? rec.id.slice(-12) : "");
  promptBody.replaceChildren(emptyNote("Loading…"));
  try {
    const replay = await fetchReplay(rec.id);
    const md = (replay.composedSystem || "").trim();
    if (!md) {
      promptBody.replaceChildren(emptyNote("(no system sections at this record yet)"));
      return;
    }
    promptBody.innerHTML = renderMarkdown(md);
  } catch (e) {
    promptBody.replaceChildren(emptyNote("Failed to load: " + e.message));
  }
}

function emptyNote(text) {
  const d = document.createElement("div");
  d.className = "empty";
  d.textContent = text;
  return d;
}

// renderMarkdown — minimal, safe-by-construction CommonMark subset. Handles
// the structures used in san system prompts: headings, paragraphs, lists,
// fenced code, inline code, blockquotes, bold/italic, links, hrules. Anything
// it doesn't recognize is rendered as paragraph text. HTML is escaped before
// inline tags are applied so untrusted content is safe.
function renderMarkdown(src) {
  const lines = src.split(/\r?\n/);
  const out = [];
  let i = 0;
  const peek = (n) => lines[i + n];

  while (i < lines.length) {
    const line = lines[i];

    // Fenced code block ```lang ... ```
    const fence = line.match(/^```(\w*)\s*$/);
    if (fence) {
      const lang = fence[1];
      i++;
      const code = [];
      while (i < lines.length && !/^```\s*$/.test(lines[i])) {
        code.push(lines[i]);
        i++;
      }
      if (i < lines.length) i++; // consume closing fence
      out.push('<pre><code' + (lang ? ' class="lang-' + escapeAttr(lang) + '"' : "") + ">" +
        escapeHTML(code.join("\n")) + "</code></pre>");
      continue;
    }

    // ATX heading
    const h = line.match(/^(#{1,6})\s+(.+?)\s*#*\s*$/);
    if (h) {
      const level = h[1].length;
      out.push("<h" + level + ">" + renderInline(h[2]) + "</h" + level + ">");
      i++;
      continue;
    }

    // <environment> block — special-case: contents are key:value lines that
    // should preserve per-line structure (cwd, model, platform, ...). A
    // regular markdown paragraph would collapse them into one wrapped line.
    if (/^<environment>\s*$/.test(line)) {
      out.push('<h1 class="prompt-xml">⟨ environment ⟩</h1>');
      i++;
      const items = [];
      while (i < lines.length && !/^<\/environment>\s*$/.test(lines[i])) {
        const ln = lines[i].trim();
        if (ln) {
          const kv = ln.match(/^([^:]+):\s*(.*)$/);
          if (kv) {
            items.push("<dt>" + escapeHTML(kv[1]) + "</dt><dd>" + escapeHTML(kv[2]) + "</dd>");
          } else {
            items.push("<dt></dt><dd>" + escapeHTML(ln) + "</dd>");
          }
        }
        i++;
      }
      if (i < lines.length) i++; // consume closing tag
      out.push('<dl class="prompt-env">' + items.join("") + "</dl>");
      continue;
    }

    // XML-tagged block markers (<policy>, <guidelines name="tools">, …).
    // san system prompts use these as section dividers for structured
    // / auxiliary content. Translate the opening tag into a styled header
    // and drop the matching closing tag; contents inside render as normal
    // markdown so the prose flow is preserved.
    const xmlOpen = line.match(/^<(\w+)(?:\s+name="([^"]+)")?\s*>\s*$/);
    if (xmlOpen) {
      const label = xmlOpen[2] ? xmlOpen[1] + " · " + xmlOpen[2] : xmlOpen[1];
      out.push('<h1 class="prompt-xml">⟨ ' + escapeHTML(label) + ' ⟩</h1>');
      i++;
      continue;
    }
    if (/^<\/\w+>\s*$/.test(line)) {
      i++;
      continue;
    }

    // Horizontal rule
    if (/^\s*([-*_])(\s*\1){2,}\s*$/.test(line)) {
      out.push("<hr>");
      i++;
      continue;
    }

    // Blockquote (consume contiguous > lines)
    if (/^>\s?/.test(line)) {
      const buf = [];
      while (i < lines.length && /^>\s?/.test(lines[i])) {
        buf.push(lines[i].replace(/^>\s?/, ""));
        i++;
      }
      out.push("<blockquote>" + renderInline(buf.join("\n")) + "</blockquote>");
      continue;
    }

    // Lists (unordered or ordered) — flat, no nesting
    if (/^\s*[-*+]\s+/.test(line) || /^\s*\d+\.\s+/.test(line)) {
      const ordered = /^\s*\d+\.\s+/.test(line);
      const tag = ordered ? "ol" : "ul";
      const items = [];
      const itemRe = ordered ? /^\s*\d+\.\s+(.*)$/ : /^\s*[-*+]\s+(.*)$/;
      while (i < lines.length && itemRe.test(lines[i])) {
        items.push("<li>" + renderInline(lines[i].match(itemRe)[1]) + "</li>");
        i++;
      }
      out.push("<" + tag + ">" + items.join("") + "</" + tag + ">");
      continue;
    }

    // Blank line → paragraph separator
    if (line.trim() === "") { i++; continue; }

    // Paragraph: gobble contiguous non-blank lines
    const para = [];
    while (i < lines.length && lines[i].trim() !== "" &&
           !/^(#{1,6}\s|>|```|\s*[-*+]\s|\s*\d+\.\s)/.test(lines[i]) &&
           !/^\s*([-*_])(\s*\1){2,}\s*$/.test(lines[i])) {
      para.push(lines[i]);
      i++;
    }
    if (para.length) out.push("<p>" + renderInline(para.join(" ")) + "</p>");
  }
  return out.join("\n");
}

function renderInline(text) {
  // Escape HTML once up front, then re-introduce trusted tags via regex.
  // Markers (code first to protect their contents from other regexes).
  let s = escapeHTML(text);
  // Inline code: `code`
  s = s.replace(/`([^`]+)`/g, (_, c) => "<code>" + c + "</code>");
  // Links [text](url)
  s = s.replace(/\[([^\]]+)\]\(([^)\s]+)\)/g,
    (_, t, u) => '<a href="' + escapeAttr(u) + '" target="_blank" rel="noreferrer">' + t + "</a>");
  // Bold **x** / __x__
  s = s.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  s = s.replace(/__([^_]+)__/g, "<strong>$1</strong>");
  // Italic *x* / _x_
  s = s.replace(/(^|[^*])\*([^*\n]+)\*(?!\*)/g, "$1<em>$2</em>");
  s = s.replace(/(^|[^_])_([^_\n]+)_(?!_)/g, "$1<em>$2</em>");
  return s;
}

function escapeAttr(s) {
  return String(s).replace(/[&<>"']/g, (ch) => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  }[ch]));
}

wirePromptPanel();
wireFilters();
loadSessions();
// Poll the session list so new transcripts (e.g. the user starting a fresh
// `gen` in another terminal) show up without a manual refresh. 2s feels
// snappy and the cost is one cheap JSON list read per tick.
setInterval(pollSessions, 2000);
