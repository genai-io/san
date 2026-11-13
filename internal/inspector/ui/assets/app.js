// Native typed-event viewer. SDK batches are transport; event payloads stay native.
const $ = (selector) => document.querySelector(selector);
const state = {
  currentSession: null, selectedRecordID: null, detailRequest: 0, promptRequest: 0,
  records: [], seenIDs: new Set(), eventSource: null,
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
function passesFilter(rec) { return state.filters[classify(rec.type)] !== false; }
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
function openSession(id) {
  if (state.eventSource) state.eventSource.close();
  state.currentSession = id; state.selectedRecordID = null;
  state.detailRequest++; state.promptRequest++;
  state.records = []; state.seenIDs.clear();
  timeline.replaceChildren(); detail.replaceChildren(); $('#prompt-body').replaceChildren();
  $('#session-meta').textContent = id;
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
  state.seenIDs.add(key);
  const records = envelope.batch.records;
  for (let ri = 0; ri < records.length; ri++) {
    const record = records[ri];
    // Keep unknown application records and malformed event containers visible.
    const events = record.name === 'san.events' && Array.isArray(record.data?.events) ? record.data.events : [record];
    for (let ei = 0; ei < events.length; ei++) {
      const raw = events[ei];
      const rec = raw && typeof raw === 'object' ? raw : {raw};
      state.records.push({ ...rec, _key: key + ':' + ri + ':' + ei, _batch: envelope.tail, _exactJSON: exactJSON });
    }
  }
  renderTimeline();
}
function eventLabel(rec) {
  const p = rec.payload || {};
  let summary = '';
  if (rec.type === 'message.append' && Array.isArray(p.messages)) {
    summary = p.messages.map(m => m ? display(m.role) + ':' + display(m.id) : '?').join(', ');
    if (p.accepted === true) summary = 'accepted · ' + summary;
  }
  else if (rec.type === 'context.compact') summary = 'through #' + p.through + ' → ' + (p.summary?.id || 'summary');
  else if (rec.type === 'inference.start') summary = p.purpose + ' · ' + (p.input?.standalone ? 'standalone' : 'conversation') + ' · ' + (p.request?.model || '');
  else if (rec.type === 'inference.finish' || rec.type === 'tool.finish') summary = [p.call_id, p.outcome, p.reason].filter(Boolean).join(' · ');
  else if (rec.type === 'permission.decide') summary = [p.call_id, p.decision, p.by, p.scope].filter(Boolean).join(' · ');
  else if (rec.type === 'hook.finish') summary = [p.hook, p.trigger, p.outcome].filter(Boolean).join(' · ');
  else if (rec.type === 'annotation.add') summary = p.text || '';
  else if (p.cause) summary = p.cause;
  return summary ? ' · ' + String(summary).slice(0, 160) : '';
}
function renderTimeline() {
  const nodes = [];
  state.records.forEach((rec, index) => {
    if (!passesFilter(rec)) return;
    const source = rec.source ? ' ← ' + display(rec.source.session || 'local') + '#' + display(rec.source.seq) : '';
    const unknown = !families.has(rec.type) || rec.v !== 1 ? ' · unsupported (raw)' : '';
    let label = '';
    try { label = eventLabel(rec); } catch (_) { label = ' · malformed payload (raw)'; }
    const row = textElement('div', '#' + display(rec.seq ?? '?') + ' ' + display(rec.type || 'unknown') + source + label + unknown, 'timeline-row');
    row.dataset.kind = classify(rec.type);
    row.classList.toggle('active', rec._key === state.selectedRecordID);
    row.addEventListener('click', () => showDetail(index, row)); nodes.push(row);
  });
  timeline.replaceChildren(...nodes);
}
async function readState(id, seq) {
  const response = await fetch('/api/sessions/' + encodeURIComponent(id) + '/state/' + seq);
  if (!response.ok) throw new Error(await response.text());
  if (response.text) {
    const exactJSON = await response.text();
    return { ...JSON.parse(exactJSON), _exactJSON: exactJSON };
  }
  return response.json();
}
async function showDetail(index) {
  const rec = state.records[index]; if (!rec) return;
  const id = state.currentSession, key = rec._key;
  state.selectedRecordID = key;
  renderTimeline();
  const generation = ++state.detailRequest;
  const current = () => state.currentSession === id && state.selectedRecordID === key && state.detailRequest === generation;
  const title = textElement('h2', display(rec.type || 'Unknown event') + ' #' + display(rec.seq ?? '?'));
  const raw = rec._exactJSON ? textElement('pre', rec._exactJSON) : jsonBlock(rec);
  detail.replaceChildren(title, textElement('h3', 'Raw event and committed batch provenance'), raw);
  refreshPromptPanel(rec);
  if (!Number.isSafeInteger(rec.seq) || rec.seq < 1) return;
  try {
    const snapshot = await readState(id, rec.seq);
    if (!current()) return;
    detail.replaceChildren(title,
      textElement('h3', 'Raw event and committed batch provenance'), raw,
      textElement('h3', snapshot.request ? 'Exact admitted inference request' : 'Active native context at this event'),
      jsonBlock(snapshot.request || snapshot.context),
      textElement('h3', 'Full committed message history (latest, including pre-compaction)'), jsonBlock(snapshot.history),
      textElement('h3', 'Exact native snapshot JSON (preserves arbitrary numeric values)'),
      snapshot._exactJSON ? textElement('pre', snapshot._exactJSON) : jsonBlock(snapshot));
  } catch (error) {
    if (!current()) return;
    detail.replaceChildren(title, raw, textElement('p', 'Native reconstruction unavailable: ' + error.message));
  }
}
async function refreshPromptPanel(rec) {
  const id = state.currentSession, key = state.selectedRecordID;
  const generation = ++state.promptRequest;
  const current = () => state.currentSession === id && state.selectedRecordID === key && generation === state.promptRequest;
  try {
    const snapshot = await readState(id, rec?.seq || 0);
    if (!current()) return;
    const request = snapshot.request || snapshot.context;
    $('#prompt-body').replaceChildren(textElement('pre', request.system || '(empty system prompt)'),
      textElement('h3', 'Native tools'), jsonBlock(request.tools || []));
    $('#prompt-meta').textContent = rec ? 'at event #' + rec.seq : 'latest committed context';
  } catch (error) {
    if (current()) $('#prompt-body').replaceChildren(textElement('p', 'Native reconstruction unavailable: ' + error.message));
  }
}
$('#toggle-prompt').addEventListener('click', () => {
  const main = $('main'); main.classList.toggle('show-prompt', !main.classList.contains('show-prompt'));
  if (state.currentSession) refreshPromptPanel(state.records.find(r => r._key === state.selectedRecordID));
});
$('#prompt-close').addEventListener('click', () => $('main').classList.remove('show-prompt'));
for (const [id, kind] of Object.entries({'filter-state':'state','filter-tools':'tool','filter-system':'system',
  'filter-inference':'inference','filter-message':'message','filter-hook':'hook','filter-permission':'permission'})) {
  $('#' + id).addEventListener('change', event => { state.filters[kind] = event.target.checked; renderTimeline(); });
}
loadSessions(); setInterval(loadSessions, 2000);
