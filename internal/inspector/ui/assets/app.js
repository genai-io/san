// Native typed-event viewer. SDK batches are transport; event payloads stay native.
const $ = (selector) => document.querySelector(selector);
const state = {
  currentSession: null, selectedRecordID: null, detailRequest: 0, promptRequest: 0,
  records: [], seenIDs: new Set(), bySequence: new Map(), rows: new Map(), eventSource: null,
  calls: new Map(), results: new Map(), selectedState: null, pendingSequence: null,
  filters: {message:true, inference:true, tool:true, system:true, state:true, hook:true, permission:true},
};
const timeline = $('#timeline'), detail = $('#detail-body');
const families = new Set(['session.create','session.update','message.append','context.compact',
  'inference.start','inference.finish','permission.decide','tool.finish','instructions.update',
  'toolset.update','skills.update','hook.finish','annotation.add']);
function classify(type) {
  if (type === 'session.create' || type === 'session.update' || type === 'skills.update') return 'state';
  if (type === 'message.append' || type === 'context.compact' || type === 'annotation.add') return 'message';
  if (type === 'toolset.update' || type === 'tool.finish') return 'tool';
  if (type === 'instructions.update') return 'system';
  if (type === 'inference.start' || type === 'inference.finish') return 'inference';
  if (type === 'hook.finish') return 'hook';
  if (type === 'permission.decide') return 'permission';
  return 'unknown';
}
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
async function loadSessions() {
  try {
    const response = await fetch('/api/sessions');
    if (!response.ok) throw new Error(response.statusText);
    const list = await response.json();
    const nodes = list.map(item => {
      const node = textElement('li', (item.title || item.id) + (item.attrsError ? ' · attributes unreadable: ' + item.attrsError : ''));
      node.addEventListener('click', () => openSession(item.id)); return node;
    });
    $('#session-list').replaceChildren(...nodes);
  } catch (error) { setStatus('Sessions unavailable: ' + error.message); }
}
function openSession(id, sequence = null) {
  if (state.eventSource) state.eventSource.close();
  state.currentSession = id; state.selectedRecordID = null;
  state.detailRequest++; state.promptRequest++;
  state.selectedState = null; state.pendingSequence = sequence;
  $('#show-history').disabled = false;
  $('#show-history').setAttribute('aria-pressed', 'false');
  state.records = []; state.seenIDs.clear(); state.bySequence.clear(); state.rows.clear();
  state.calls.clear(); state.results.clear();
  timeline.replaceChildren();
  updateTimelineCount();
  detail.replaceChildren(textElement('p', 'Select a step to inspect its details.', 'detail-empty'));
  $('#prompt-body').replaceChildren(); $('#prompt-meta').textContent = '';
  if ($('main').classList.contains('show-prompt')) refreshPromptPanel();
  $('#session-meta').textContent = id;
  setStatus('connecting');
  const source = new EventSource('/api/sessions/' + encodeURIComponent(id) + '/stream');
  state.eventSource = source;
  const current = () => state.currentSession === id && state.eventSource === source;
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
  state.seenIDs.add(key);
  const start = state.records.length;
  for (let ri = 0; ri < records.length; ri++) {
    const record = records[ri];
    // Keep unknown application records and malformed event containers visible.
    const events = record.name === 'san.events' && Array.isArray(record.data?.events) ? record.data.events : [record];
    for (let ei = 0; ei < events.length; ei++) {
      const raw = events[ei];
      const rec = raw && typeof raw === 'object' ? raw : {raw};
      if (Number.isSafeInteger(rec.seq) && rec.seq > 0 && !state.bySequence.has(rec.seq)) {
        state.bySequence.set(rec.seq, state.records.length);
      }
      state.records.push({ ...rec, _raw: raw, _key: key + ':' + ri + ':' + ei, _batch: envelope.tail, _exactJSON: exactJSON });
      indexToolMessages(rec);
    }
  }
  for (let i = start; i < state.records.length; i++) appendTimelineRow(state.records[i], i);
  updateTimelineCount();
  if (state.bySequence.has(state.pendingSequence)) selectEvent(state.currentSession, state.pendingSequence);
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
function withoutTrailingReminders(text) {
  const trimmedEnd = text.trimEnd().length;
  let end = trimmedEnd;
  const close = '\n</system-reminder>';
  while (end >= close.length && text.startsWith(close, end - close.length)) {
    const start = text.lastIndexOf('<system-reminder', end - close.length);
    if (start < 0 || (start !== 0 && text.slice(start - 2, start) !== '\n\n')) break;
    const headerEnd = text.indexOf('\n', start);
    if (headerEnd < 0 || !/^<system-reminder(?: source="[^"]*")?>$/.test(text.slice(start, headerEnd)) ||
      text.indexOf(close, headerEnd) !== end - close.length) break;
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
  const label = payload.accepted === true ? 'Input queued' : toolResult ? 'Tool result' : toolCall ? 'Tool call'
    : role === 'user' ? 'User message' : role === 'assistant' ? 'Assistant message' : 'Messages recorded';
  const previews = [];
  let hasReminder = false;
  for (const message of messages) {
    if (!Array.isArray(message?.content)) continue;
    for (const block of message.content) {
      if (previews.length >= 2) break;
      if (block?.type === 'text' && typeof block.text === 'string') {
        // Omit only complete, trailing harness reminders from the user preview.
        const text = message.role === 'user' ? withoutTrailingReminders(block.text) : block.text;
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
function timelineOverview(rec) {
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
    case 'session.create': return {label:'Session created', preview:join(p.config?.model, p.fork && 'Forked session', p.parent && 'Child session')};
    case 'session.update': {
      const changes = {...p.config, ...p.state};
      return {label:'Session updated', preview:Object.keys(changes).map(key => key.replaceAll('_', ' ')).join(', ')};
    }
    case 'message.append': return messageOverview(p);
    case 'context.compact': return {label:'Context compacted', preview:p.through ? 'Through #' + p.through : ''};
    case 'inference.start': return {label:'Inference started', preview:join(p.purpose, p.request?.model,
      p.input?.standalone ? 'standalone' : p.input?.conversation ? 'conversation' : '')};
    case 'inference.finish': return {label:'Inference finished', preview:join(p.stop_reason?.replaceAll('_', ' '), duration, p.reason), ...terminal};
    case 'permission.decide': return {label:join('Permission', toolName), preview:join(p.by && 'By ' + p.by, p.scope, p.reason), ...terminal};
    case 'tool.finish': return {label:join('Tool finished', toolName), preview:join(duration, p.details?.Path, p.reason), ...terminal};
    case 'hook.finish': return {label:join('Hook', p.trigger), preview:join(duration, p.matcher, p.reason) ||
      (typeof p.hook === 'string' && /^[\w./-]+$/.test(p.hook) ? p.hook : ''), ...terminal};
    case 'instructions.update':
    case 'toolset.update':
    case 'skills.update': {
      const labels = {'instructions.update':'Instructions updated', 'toolset.update':'Tool catalogue updated', 'skills.update':'Skills updated'};
      const set = Object.keys(p.set || {}).length, removed = Array.isArray(p.remove) ? p.remove.length : 0;
      return {label:labels[rec.type], preview:join(p.cause, set && set + ' set', removed && removed + ' removed')};
    }
    case 'annotation.add': return {label:'Annotation', preview:p.text};
    default: return {label:display(rec.type || 'Unknown event')};
  }
}
function describeStep(rec) {
  const type = display(rec.type || 'Unknown event');
  if (!supported(rec)) return {label:type, status:'Unsupported (raw)'};
  try { return timelineOverview(rec); }
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
function supported(rec) { return families.has(rec.type) && rec.v === 1; }
async function readState(id, seq) {
  const response = await fetch('/api/sessions/' + encodeURIComponent(id) + '/state/' + seq);
  const exactJSON = await response.text();
  if (!response.ok) throw new Error(exactJSON.trim());
  return { ...JSON.parse(exactJSON), _exactJSON: exactJSON };
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
  state.selectedState = null;
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
  const hasSequence = supported(rec) && Number.isSafeInteger(rec.seq) && rec.seq > 0;
  let snapshot;
  const load = hasSequence ? () => snapshot ??= readState(id, rec.seq) : null;
  state.selectedState = load;
  let overview = [];
  try { if (supported(rec)) overview = eventDetails(rec, id); }
  catch (_) { overview = [textElement('p', 'Malformed payload. See the raw record.', 'detail-note')]; }
  const request = load && rec.type === 'inference.start' ? document.createElement('div') : null;
  const context = load && rec.type === 'context.compact' ? [snapshotDisclosure('Context after compaction', load,
    result => requestNodes(result.context, 'Context messages'))] : [];
  detail.replaceChildren(eventType, title, meta, ...overview, ...(request ? [request] : context), ...recordData(rec, load));
  detail.scrollTop = 0;
  if ($('main').classList.contains('show-prompt')) refreshPromptPanel(rec);
  if (request) await loadSnapshot(request, load, result => result.request
    ? [section('Model request at this step', ...requestNodes(result.request))]
    : [textElement('p', 'No admitted request at this step.', 'detail-note')]);
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
$('#show-history').addEventListener('click', showHistory);
$('#toggle-prompt').addEventListener('click', () => togglePrompt(!$('main').classList.contains('show-prompt')));
$('#prompt-close').addEventListener('click', () => togglePrompt(false));
$('#prompt-backdrop').addEventListener('click', () => togglePrompt(false));
$('#prompt-expand').addEventListener('click', () => {
  const main = $('main'), wide = !main.classList.contains('prompt-wide');
  main.classList.toggle('prompt-wide', wide);
  $('#prompt-expand').setAttribute('aria-pressed', String(wide));
});
for (const [id, kind] of Object.entries({'filter-state':'state','filter-tools':'tool','filter-system':'system',
  'filter-inference':'inference','filter-message':'message','filter-hook':'hook','filter-permission':'permission'})) {
  $('#' + id).addEventListener('change', event => { state.filters[kind] = event.target.checked; renderTimeline(); });
}
loadSessions(); setInterval(loadSessions, 2000);
