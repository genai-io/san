// Native typed-event viewer. SDK batches are transport; event payloads stay native.
const $ = (selector) => document.querySelector(selector);
const state = {
  currentSession: null, selectedRecordID: null, detailRequest: 0, promptRequest: 0,
  records: [], seenIDs: new Set(), bySequence: new Map(), rows: new Map(), eventSource: null,
  calls: new Map(), results: new Map(), inferences: new Map(), outputPanel: null,
  selectedState: null, contextAbort: null, pendingSequence: null,
  sessionSummary: null, streamIdentity: null,
  filters: {message:true, inference:true, tool:true, system:true, state:true, hook:true, permission:true},
};
const timeline = $('#timeline'), detail = $('#detail-body');
const eventViews = new Map([
  ['session.create', {label:'Session · Start', kind:'state'}],
  ['session.update', {label:'Session · Update', kind:'state'}],
  ['message.append', {label:'Message', kind:'message'}],
  ['context.compact', {label:'Context · Compaction', kind:'message'}],
  ['inference.start', {label:'Model · Request', kind:'inference'}],
  ['inference.finish', {label:'Model · Response', kind:'inference'}],
  ['permission.decide', {label:'Permission', kind:'permission'}],
  ['tool.finish', {label:'Tool · Execution', kind:'tool'}],
  ['instructions.update', {label:'Instructions', kind:'system'}],
  ['toolset.update', {label:'Tools', kind:'tool'}],
  ['skills.update', {label:'Skills', kind:'state'}],
  ['hook.finish', {label:'Hook', kind:'hook'}],
  ['annotation.add', {label:'Annotation', kind:'message'}],
]);
const hookTriggers = new Map([
  ['SessionStart', 'Session start'], ['SessionEnd', 'Session end'], ['UserPromptSubmit', 'Prompt submit'],
  ['PreToolUse', 'Before tool use'], ['PostToolUse', 'After tool use'], ['PostToolUseFailure', 'Tool failure'],
]);
function classify(type) { return eventViews.get(type)?.kind ?? 'unknown'; }
function passesFilter(rec, summary = describeStep(rec)) {
  return state.filters[summary.accent || classify(rec.type)] !== false;
}
function display(value) { return typeof value === 'string' ? value : JSON.stringify(value) ?? ''; }
function textElement(tag, text, className) {
  const node = document.createElement(tag); node.textContent = text;
  if (className) node.className = className;
  return node;
}
function jsonBlock(value) { return textElement('pre', JSON.stringify(value, null, 2)); }
function setStatus(text) { $('#status').textContent = text; }
function escapeHTML(s) { return String(s).replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c])); }
// Markdown is deliberately limited to inline links. Raw HTML never becomes DOM.
function renderInline(text) {
  return String(text).split(/(\[[^\]\n]*\]\([^\)\n]*\))/g).map(part => {
    const m = /^\[([^\]]*)\]\(([^\)]*)\)$/.exec(part);
    if (!m) return escapeHTML(part);
    try {
      const url = new URL(m[2]);
      if (!['https:', 'http:', 'mailto:'].includes(url.protocol)) return escapeHTML(part);
      return '<a href="' + escapeHTML(url.href) + '" target="_blank" rel="noopener noreferrer">' + escapeHTML(m[1]) + '</a>';
    } catch (_) { return escapeHTML(part); }
  }).join('');
}
const sessionPages = {cursors:[''], index:0, next:'', loading:false, items:new Map()};

function sessionLabel(item) {
  const title = item?.title?.trim();
  return item?.subagent ? (title ? 'Subagent · ' + title : 'Subagent') : (title || 'Untitled session');
}
function displaySession(id) {
  const item = {...(state.sessionSummary || sessionPages.items.get(id)), ...state.streamIdentity};
  const meta = $('#session-meta');
  meta.textContent = sessionLabel(item);
  meta.title = sessionLabel(item) + ' · ' + id;
  const parent = $('#session-parent');
  parent.hidden = !item.parent;
  if (item.parent) {
    parent.href = '?session=' + encodeURIComponent(item.parent);
    parent.title = item.parent;
  }
}
async function loadSessionSummary(id, current) {
  try {
    const response = await fetch('/api/sessions/' + encodeURIComponent(id) + '/summary');
    if (!response.ok) return;
    const summary = await response.json();
    if (!current()) return;
    state.sessionSummary = summary;
    displaySession(id);
  } catch (_) { /* The timeline can still supply identity. */ }
}

function sessionPageControls() {
  $('#sessions-prev').disabled = sessionPages.loading || sessionPages.index === 0;
  $('#sessions-next').disabled = sessionPages.loading || !sessionPages.next;
  $('#sessions-refresh').disabled = sessionPages.loading;
}
async function loadSessions(index = 0, cursor = '') {
  if (sessionPages.loading) return;
  sessionPages.loading = true;
  sessionPageControls();
  const status = $('#sessions-page');
  status.textContent = 'Loading…';
  try {
    const response = await fetch('/api/sessions' + (cursor ? '?cursor=' + encodeURIComponent(cursor) : ''));
    if (!response.ok) throw new Error(response.statusText);
    const page = await response.json();
    sessionPages.items = new Map(page.items.map(item => [item.id, item]));
    if (sessionPages.items.has(state.currentSession)) displaySession(state.currentSession);
    const nodes = page.items.map(item => {
      const node = textElement('li', '');
      node.dataset.session = item.id;
      node.classList.toggle('active', item.id === state.currentSession);
      const button = textElement('button', '', 'session-button');
      button.type = 'button';
      button.title = sessionLabel(item) + ' · ' + item.id;
      button.addEventListener('click', () => openSession(item.id));
      button.appendChild(textElement('span', sessionLabel(item), 'session-label'));
      const date = new Date(item.updatedAt);
      if (!Number.isNaN(date.getTime())) {
        const time = textElement('time', date.toLocaleString(undefined, {month:'short', day:'numeric', hour:'2-digit', minute:'2-digit'}), 'id');
        time.dateTime = date.toISOString();
        time.title = date.toLocaleString();
        button.appendChild(time);
      }
      if (item.attrsError) button.appendChild(textElement('span', 'Attributes unreadable: ' + item.attrsError, 'id'));
      node.appendChild(button);
      return node;
    });
    const list = $('#session-list');
    list.replaceChildren(...(nodes.length ? nodes : [textElement('li', 'No sessions on this page.', 'session-empty')]));
    list.scrollTop = 0;
    sessionPages.index = index;
    sessionPages.cursors = [...sessionPages.cursors.slice(0, index), cursor];
    sessionPages.next = page.next || '';
    status.textContent = 'Page ' + (index + 1);
  } catch (error) {
    status.textContent = 'Sessions unavailable: ' + error.message;
  } finally {
    sessionPages.loading = false;
    sessionPageControls();
  }
}
$('#sessions-prev').addEventListener('click', () => {
  const index = sessionPages.index - 1;
  if (index >= 0) loadSessions(index, sessionPages.cursors[index]);
});
$('#sessions-next').addEventListener('click', () => {
  if (sessionPages.next) loadSessions(sessionPages.index + 1, sessionPages.next);
});
$('#sessions-refresh').addEventListener('click', () => loadSessions());
function openSession(id, sequence = null) {
  if (state.eventSource) state.eventSource.close();
  state.currentSession = id; state.selectedRecordID = null;
  state.sessionSummary = state.streamIdentity = null;
  for (const row of $('#session-list').children) row.classList.toggle('active', row.dataset.session === id);
  state.detailRequest++; state.promptRequest++;
  cancelContextLoad();
  state.pendingSequence = sequence;
  $('#show-history').disabled = false;
  $('#show-history').setAttribute('aria-pressed', 'false');
  state.records = []; state.seenIDs.clear(); state.bySequence.clear(); state.rows.clear();
  state.calls.clear(); state.results.clear(); state.inferences.clear();
  timeline.replaceChildren();
  updateTimelineCount();
  detail.replaceChildren(textElement('p', 'Select a step to inspect its details.', 'detail-empty'));
  $('#prompt-body').replaceChildren(); $('#prompt-meta').textContent = '';
  if ($('main').classList.contains('show-prompt')) refreshPromptPanel();
  displaySession(id);
  setStatus('connecting');
  const source = new EventSource('/api/sessions/' + encodeURIComponent(id) + '/stream');
  state.eventSource = source;
  const current = () => state.currentSession === id && state.eventSource === source;
  loadSessionSummary(id, current);
  source.onmessage = event => {
    if (!current()) return;
    try { ingestBatch(JSON.parse(event.data), event.data); setStatus('live'); }
    catch (error) { setStatus('Unreadable batch: ' + error.message); }
  };
  source.addEventListener?.('diagnostic', event => {
    if (!current()) return;
    try { setStatus('Read stopped: ' + JSON.parse(event.data).error); }
    catch (_) { setStatus('Read stopped: invalid diagnostic'); }
    source.close();
    showDiagnostics(id, source);
  });
  source.onerror = () => { if (current()) setStatus('disconnected · retrying'); };
}
async function showDiagnostics(id, source) {
  const generation = ++state.detailRequest;
  ++state.promptRequest;
  try {
    const response = await fetch('/api/sessions/' + encodeURIComponent(id) + '/records');
    const result = await response.json();
    if (state.currentSession !== id || state.eventSource !== source || state.detailRequest !== generation) return;
    detail.replaceChildren(textElement('h2', 'Read diagnostic'), textElement('p', result.error || ''),
      textElement('h3', 'Unvalidated file text (first 1 MiB; not committed events)'),
      textElement('pre', result.unvalidatedText || ''));
  } catch (_) { /* The stream already reported its error. */ }
}
function ingestBatch(envelope, exactJSON) {
  const key = envelope.tail.sequence + ':' + envelope.tail.hash;
  if (state.seenIDs.has(key)) return;
  const records = envelope.batch.records;
  const batchJSON = exactJSON ?? JSON.stringify(envelope);
  state.seenIDs.add(key);
  const start = state.records.length;
  for (let ri = 0; ri < records.length; ri++) {
    const record = records[ri];
    // Keep unknown application records and malformed event containers visible.
    const events = record.name === 'san.events' && Array.isArray(record.data?.events) ? record.data.events : [record];
    for (let ei = 0; ei < events.length; ei++) {
      const raw = events[ei];
      const rec = raw && typeof raw === 'object' ? raw : {raw};
      if (rec.seq === 1 && rec.type === 'session.create' && rec.v === 1 && state.currentSession) {
        const parent = rec.payload?.parent?.session;
        const agent = rec.payload?.agent_id;
        if (parent || (agent && agent !== 'main')) {
          state.streamIdentity = {subagent:true, parent};
          displaySession(state.currentSession);
        }
      }
      if (Number.isSafeInteger(rec.seq) && rec.seq > 0 && !state.bySequence.has(rec.seq)) {
        state.bySequence.set(rec.seq, state.records.length);
      }
      state.records.push({ ...rec, _key: key + ':' + ri + ':' + ei, _batchJSON: batchJSON });
      indexToolMessages(rec);
      indexInference(rec);
    }
  }
  for (let i = start; i < state.records.length; i++) appendTimelineRow(state.records[i], i);
  updateTimelineCount();
  updateModelOutput();
  if (state.bySequence.has(state.pendingSequence)) selectEvent(state.currentSession, state.pendingSequence);
}
function inferenceStart(rec) {
  if (rec.type === 'inference.start') return rec;
  if (!rec.source || rec.source.session) return null;
  const start = state.records[state.bySequence.get(rec.source.seq)];
  return start?.type === 'inference.start' && supported(start) ? start : null;
}
function indexInference(rec) {
  if (!supported(rec) || !['inference.finish', 'message.append', 'context.compact', 'annotation.add'].includes(rec.type)) return;
  const start = inferenceStart(rec);
  if (!start) return;
  let pair = state.inferences.get(start.seq);
  if (!pair) state.inferences.set(start.seq, pair = {messages: [], annotations: []});
  if (rec.type === 'inference.finish') pair.finish = rec;
  else if (rec.type === 'annotation.add' && typeof rec.payload?.text === 'string' && rec.payload.text.trim()) pair.annotations.push(rec);
  else if (rec.type === 'context.compact' && rec.payload?.summary) pair.messages.push(rec);
  else if (Array.isArray(rec.payload?.messages) && rec.payload.messages.length) pair.messages.push(rec);
}
const asides = new Map([
  ['recall', {name:'Auto Memory', operation:'Recall'}],
  ['memory-maintain', {name:'Auto Memory', operation:'Maintain'}],
  ['auto-skill', {name:'Auto Skill', operation:'Review'}],
  ['btw', {name:'By the Way'}], ['recap', {name:'Recap'}], ['suggest', {name:'Suggestion'}],
  ['flow', {name:'Flow Draft'}], ['autopilot', {name:'AutoPilot'}],
]);
const inferenceLabels = new Map([
  ['judge', 'Judge Side'], ['group', 'Group Side'], ['compaction', 'Compaction'], ['hook', 'Hook'],
]);
function inferenceLabel(rec, label) {
  const purpose = inferenceStart(rec)?.payload?.purpose;
  if (typeof purpose !== 'string' || purpose === 'turn') return label;
  const aside = asides.get(purpose);
  if (aside) return `${rec.type === 'inference.start' ? 'Model Request' : 'Model Response'} · Aside — ${aside.name}`;
  return label + ' · ' + (inferenceLabels.get(purpose) ?? purpose);
}
function indexToolMessages(rec) {
  if (rec.type !== 'message.append' || rec.v !== 1 || !Array.isArray(rec.payload?.messages)) return;
  for (const message of rec.payload.messages) {
    if (!Array.isArray(message?.content)) continue;
    for (const block of message.content) {
      if (block?.type === 'tool_call' && block.tool_call?.id && !state.calls.has(block.tool_call.id)) {
        state.calls.set(block.tool_call.id, {seq: rec.seq, block});
      } else if (block?.type === 'tool_result' && block.tool_result?.tool_call_id && !state.results.has(block.tool_result.tool_call_id)) {
        state.results.set(block.tool_result.tool_call_id, {seq: rec.seq, block});
      }
    }
  }
}
function formatTime(value) {
  if (!Number.isFinite(value) || value <= 0) return undefined;
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? undefined : date.toLocaleString();
}
function splitTrailingReminders(text, onReminder) {
  const trimmedEnd = text.trimEnd().length;
  let end = trimmedEnd;
  const close = '\n</system-reminder>';
  while (end >= close.length && text.startsWith(close, end - close.length)) {
    const start = text.lastIndexOf('<system-reminder', end - close.length);
    if (start < 0 || (start !== 0 && text.slice(start - 2, start) !== '\n\n')) break;
    const headerEnd = text.indexOf('\n', start);
    if (headerEnd < 0) break;
    const header = /^<system-reminder(?: source="([^"]*)")?>$/.exec(text.slice(start, headerEnd));
    if (!header || text.indexOf(close, headerEnd) !== end - close.length) break;
    if (onReminder) onReminder(header[1], text.slice(headerEnd + 1, end - close.length));
    end = text.slice(0, start).trimEnd().length;
  }
  return end === trimmedEnd ? text : text.slice(0, end);
}
function messageOverview(payload) {
  const messages = Array.isArray(payload.messages) ? payload.messages : [];
  const blocks = messages.flatMap(message => Array.isArray(message?.content) ? message.content : []);
  const only = type => blocks.length > 0 && blocks.every(block => block?.type === type);
  const toolResult = only('tool_result'), toolCall = only('tool_call');
  const role = messages.length === 1 ? messages[0]?.role : null;
  const label = payload.accepted === true ? 'Message · Queued' : toolResult ? 'Tool · Result' : toolCall ? 'Tool · Call'
    : role === 'user' ? 'Message · User' : role === 'assistant' ? 'Message · Assistant' : undefined;
  const previews = [];
  let hasReminder = false;
  for (const message of messages) {
    if (!Array.isArray(message?.content)) continue;
    for (const block of message.content) {
      if (previews.length >= 2) break;
      if (block?.type === 'text' && typeof block.text === 'string') {
        // Omit only complete, trailing harness reminders from the user preview.
        const text = message.role === 'user' ? splitTrailingReminders(block.text) : block.text;
        hasReminder ||= text !== block.text;
        if (text.trim()) previews.push(text);
      } else if (block?.type === 'tool_call' && block.tool_call?.name) {
        previews.push(toolCall ? block.tool_call.name : 'Call ' + block.tool_call.name);
      } else if (block?.type === 'tool_result') {
        const name = block.tool_result?.tool_name || state.calls.get(block.tool_result?.tool_call_id)?.block.tool_call?.name;
        if (name) previews.push(toolResult ? name : 'Result: ' + name);
      } else if (block?.type === 'image') {
        previews.push(block.image?.file_name || 'Image');
      }
    }
  }
  const failed = toolResult && blocks.some(block => block.tool_result?.is_error);
  return {label, preview: previews.length ? previews.join(' · ') : hasReminder ? 'System reminder' : messages.length > 1 ? messages.length + ' messages' : '',
    accent: toolCall || toolResult ? 'tool' : undefined, status: failed ? 'Error' : undefined, failed};
}
function timelineOverview(rec, label) {
  const p = rec.payload || {};
  const duration = Number.isFinite(p.duration_ms) ? p.duration_ms + ' ms' : '';
  const toolName = state.calls.get(p.call_id)?.block.tool_call?.name;
  const join = (...parts) => parts.filter(Boolean).join(' · ');
  const status = {success:'Success', failure:'Failed', allow:'Allowed', deny:'Denied', cancelled:'Cancelled',
    ran:'Ran', blocked:'Blocked', error:'Error'};
  const outcome = p.outcome || p.decision;
  const terminal = {status: Object.hasOwn(status, outcome) ? status[outcome] : undefined,
    failed: ['failure', 'deny', 'blocked', 'error'].includes(outcome)};
  switch (rec.type) {
    case 'session.create': return {preview:join(p.config?.model, p.fork && 'Forked session', p.parent && 'Child session')};
    case 'session.update': {
      const changes = {...p.config, ...p.state};
      return {preview:Object.keys(changes).map(key => key.replaceAll('_', ' ')).join(', ')};
    }
    case 'message.append': return messageOverview(p);
    case 'context.compact': return {preview:p.through ? 'Through #' + p.through : ''};
    case 'inference.start': return {label:inferenceLabel(rec, label), preview:join(p.request?.model,
      p.input?.standalone ? 'standalone' : p.input?.conversation ? 'conversation' : '')};
    case 'inference.finish': return {label:inferenceLabel(rec, label),
      preview:join(p.stop_reason?.replaceAll('_', ' '), duration, p.reason), ...terminal,
      status: outcome === 'success' && asides.has(inferenceStart(rec)?.payload?.purpose) ? 'Model completed' : terminal.status};
    case 'permission.decide': return {label:join(label, toolName), preview:join(p.by && 'By ' + p.by, p.scope, p.reason), ...terminal};
    case 'tool.finish': return {preview:join(toolName, duration, p.details?.Path, p.reason), ...terminal};
    case 'hook.finish': return {label:join(label, hookTriggers.get(p.trigger) ?? p.trigger), preview:join(duration, p.matcher, p.reason) ||
      (typeof p.hook === 'string' && /^[\w./-]+$/.test(p.hook) ? p.hook : ''), ...terminal};
    case 'instructions.update':
    case 'toolset.update':
    case 'skills.update': {
      const set = Object.entries(p.set || {}), removed = Array.isArray(p.remove) ? p.remove : [];
      if (rec.type === 'skills.update' && set.length + removed.length === 1) {
        return {preview:set.length ? join(...set[0]) : join(removed[0], 'removed')};
      }
      return {preview:join(set.length && set.length + ' updated', removed.length && removed.length + ' removed')};
    }
    case 'annotation.add': return {preview:p.text};
    default: return {};
  }
}
function describeStep(rec) {
  const type = display(rec.type || 'Unknown event');
  if (!supported(rec)) return {label:type, status:'Unsupported (raw)'};
  try {
    const label = eventViews.get(rec.type).label;
    const overview = timelineOverview(rec, label);
    return {...overview, label:overview.label ?? label};
  }
  catch (_) { return {label:type, status:'Malformed (raw)'}; }
}
function appendTimelineRow(rec, index) {
  const summary = describeStep(rec);
  if (!passesFilter(rec, summary)) return;
  const type = display(rec.type || 'Unknown event');
  const source = rec.source ? '← ' + display(rec.source.session || '') + '#' + display(rec.source.seq) : '';
  const row = textElement('button', '', 'timeline-row');
  row.type = 'button';
  row.title = type;
  row.dataset.kind = summary.accent || classify(rec.type);
  row.appendChild(textElement('span', '#' + display(rec.seq ?? '?'), 'step-seq'));
  const body = textElement('span', '', 'step-body');
  const head = textElement('span', '', 'step-line');
  head.appendChild(textElement('span', summary.label, 'step-label'));
  if (summary.status) head.appendChild(textElement('span', summary.status, summary.failed ? 'step-status failed' : 'step-status'));
  body.appendChild(head);
  if (summary.preview || source) {
    const meta = textElement('span', '', 'step-line step-meta');
    const preview = display(summary.preview || '').trim().replace(/\s+/g, ' ');
    meta.appendChild(textElement('span', preview.length > 180 ? preview.slice(0, 180).trimEnd() + '…' : preview, 'step-preview'));
    if (source) meta.appendChild(textElement('span', source, 'step-source'));
    body.appendChild(meta);
  }
  row.appendChild(body);
  markRow(row, rec._key === state.selectedRecordID);
  row.addEventListener('click', () => showDetail(index));
  state.rows.set(rec._key, row);
  timeline.appendChild(row);
}
function markRow(row, selected) {
  if (!row) return;
  row.classList.toggle('active', selected);
  row.setAttribute('aria-current', selected ? 'true' : 'false');
}
function updateTimelineCount() {
  const total = state.records.length, visible = state.rows.size;
  $('#timeline-count').textContent = (visible === total ? total : visible + ' of ' + total) + (total === 1 ? ' step' : ' steps');
}
function renderTimeline() {
  state.rows.clear();
  timeline.replaceChildren();
  state.records.forEach(appendTimelineRow);
  updateTimelineCount();
}
function selectEvent(id, seq) {
  if (id !== state.currentSession) { openSession(id, seq); return; }
  state.pendingSequence = null;
  const index = state.bySequence.get(seq);
  if (index === undefined) return;
  showDetail(index);
  const row = state.rows.get(state.selectedRecordID);
  row?.scrollIntoView({block: 'nearest'});
}
function supported(rec) { return eventViews.has(rec.type) && rec.v === 1; }
function cancelContextLoad() {
  state.contextAbort?.abort();
  state.contextAbort = null;
  state.selectedState = null;
  state.outputPanel = null;
}
async function readSessionJSON(id, view, seq, signal) {
  const response = await fetch('/api/sessions/' + encodeURIComponent(id) + '/' + view + '/' + seq, {signal});
  const body = await response.text();
  if (!response.ok) throw new Error(body.trim());
  return body;
}
async function readState(id, seq, signal) {
  return JSON.parse(await readSessionJSON(id, 'state', seq, signal));
}
async function loadSnapshot(body, load, render) {
  body.replaceChildren(textElement('p', 'Loading…', 'detail-note'));
  try {
    body.replaceChildren(...render(await load()));
  } catch (error) {
    body.replaceChildren(textElement('p', 'Native reconstruction unavailable: ' + error.message, 'detail-note'));
  }
}
async function showHistory() {
  const id = state.currentSession;
  if (!id) return;
  markRow(state.rows.get(state.selectedRecordID), false);
  state.selectedRecordID = null;
  cancelContextLoad();
  state.detailRequest++;
  state.pendingSequence = null;
  $('#show-history').setAttribute('aria-pressed', 'true');
  const body = document.createElement('div');
  detail.replaceChildren(textElement('p', 'Session overview', 'detail-type'),
    textElement('h2', 'Session history', 'detail-title'), body);
  detail.scrollTop = 0;
  const snapshot = readState(id, 0);
  if ($('main').classList.contains('show-prompt')) refreshPromptPanel(undefined, () => snapshot);
  await loadSnapshot(body, () => snapshot, result => {
    const messages = result.history ?? [];
    return [textElement('p', 'Through step #' + result.seq + ' · Includes messages before compaction.', 'detail-note'),
      section('Messages · ' + messages.length, ...messageViews(messages))];
  });
}
async function showDetail(index) {
  const rec = state.records[index]; if (!rec) return;
  const id = state.currentSession, key = rec._key;
  markRow(state.rows.get(state.selectedRecordID), false);
  state.selectedRecordID = key;
  markRow(state.rows.get(key), true);
  state.detailRequest++;
  state.pendingSequence = null;
  $('#show-history').setAttribute('aria-pressed', 'false');
  const eventType = textElement('p', '#' + display(rec.seq ?? '?') + ' · ' + display(rec.type || 'Unknown event'), 'detail-type');
  const title = textElement('h2', describeStep(rec).label, 'detail-title');
  const meta = fields([['Time', formatTime(rec.time)], ['Source', eventReference(rec.source, id)]]);
  cancelContextLoad();
  const hasSequence = supported(rec) && Number.isSafeInteger(rec.seq) && rec.seq > 0;
  const controller = hasSequence ? new AbortController() : null;
  state.contextAbort = controller;
  let snapshot;
  const load = controller ? () => snapshot ??= readState(id, rec.seq, controller.signal) : null;
  state.selectedState = load;
  const request = load && rec.type === 'inference.start' ? document.createElement('div') : null;
  const response = supported(rec) && rec.type === 'inference.finish' ? rec : null;
  const output = request || response ? document.createElement('div') : null;
  const panels = [];
  if (request) panels.push(detailGroup('Model input', 'Formatted view of the request. Expand Raw SDK request to see the original message and block boundaries.',
    request, rawRequest(() => readSessionJSON(id, 'request', rec.seq, controller.signal))));
  if (output) {
    panels.push(detailGroup('Model output', '', output));
    state.outputPanel = {node: output, start: inferenceStart(rec), finish: response, rendered: null};
    updateModelOutput();
  }
  if (load && !request) panels.push(contextPanel(load, !response));
  detail.replaceChildren(eventType, title, ...panels, eventPanel(rec, id, meta), rawBatch(rec));
  detail.scrollTop = 0;
  if ($('main').classList.contains('show-prompt')) refreshPromptPanel(rec);
  if (request) await loadSnapshot(request, load, result => result.request
    ? requestNodes(result.request)
    : [textElement('p', 'No model request was recorded for this step.', 'detail-note')]);
}
async function refreshPromptPanel(rec, load = rec ? state.selectedState : () => readState(state.currentSession, 0)) {
  const id = state.currentSession, key = state.selectedRecordID;
  const generation = ++state.promptRequest;
  const current = () => state.currentSession === id && state.selectedRecordID === key && generation === state.promptRequest;
  const body = $('#prompt-body');
  const scope = rec ? (rec.type === 'inference.start' ? 'Request at step #' : 'Context after step #') + (rec.seq ?? '?') : 'Latest session context';
  $('#prompt-meta').textContent = scope;
  if (rec && !load) {
    body.replaceChildren(textElement('p', 'No native context for this record.', 'detail-note'));
    return;
  }
  body.replaceChildren(textElement('p', 'Loading…', 'detail-note'));
  try {
    const snapshot = await load();
    if (current()) body.replaceChildren(...promptNodes(rec?.type === 'inference.start' ? snapshot.request : snapshot.context));
  } catch (error) {
    if (current()) body.replaceChildren(textElement('p', 'Native reconstruction unavailable: ' + error.message, 'detail-note'));
  }
}
function togglePrompt(show) {
  $('main').classList.toggle('show-prompt', show);
  $('#toggle-prompt').setAttribute('aria-expanded', String(show));
  if (show && state.currentSession) refreshPromptPanel(state.records.find(r => r._key === state.selectedRecordID));
  if (!show) ++state.promptRequest;
}
function toggleDetailWidth(wide) {
  $('main').classList.toggle('detail-wide', wide);
  const button = $('#detail-expand');
  const label = wide ? 'Restore details width' : 'Expand details';
  button.title = label;
  button.setAttribute('aria-label', label);
  button.setAttribute('aria-expanded', String(wide));
}
$('#detail-expand').addEventListener('click', () => toggleDetailWidth(!$('main').classList.contains('detail-wide')));
$('#show-history').addEventListener('click', showHistory);
$('#toggle-prompt').addEventListener('click', () => togglePrompt(!$('main').classList.contains('show-prompt')));
$('#prompt-close').addEventListener('click', () => togglePrompt(false));
$('#prompt-backdrop').addEventListener('click', () => togglePrompt(false));
$('#prompt-expand').addEventListener('click', () => {
  const main = $('main'), wide = !main.classList.contains('prompt-wide');
  main.classList.toggle('prompt-wide', wide);
  $('#prompt-expand').setAttribute('aria-pressed', String(wide));
});
document.addEventListener('keydown', event => {
  if (event.key !== 'Escape' || event.defaultPrevented) return;
  if ($('main').classList.contains('show-prompt')) togglePrompt(false);
  else if ($('main').classList.contains('detail-wide')) toggleDetailWidth(false);
  else return;
  event.preventDefault();
});
for (const [id, kind] of Object.entries({'filter-state':'state','filter-tools':'tool','filter-system':'system',
  'filter-inference':'inference','filter-message':'message','filter-hook':'hook','filter-permission':'permission'})) {
  $('#' + id).addEventListener('change', event => {
    state.filters[kind] = event.target.checked;
    renderTimeline();
  });
}
const initialSession = new URL(location.href).searchParams.get('session');
if (initialSession) openSession(initialSession);
loadSessions();
