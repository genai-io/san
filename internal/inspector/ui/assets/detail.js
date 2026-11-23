function section(title, ...nodes) {
  const el = document.createElement('section');
  el.appendChild(textElement('h3', title, 'section-header'));
  for (const node of nodes) el.appendChild(node);
  return el;
}

function detailGroup(title, note, ...nodes) {
  const group = document.createElement('section');
  group.className = 'detail-group';
  group.appendChild(textElement('h3', title, 'detail-group-title'));
  if (note) group.appendChild(textElement('p', note, 'detail-scope'));
  for (const node of nodes) group.appendChild(node);
  return group;
}

function disclosure(label, build, open = false) {
  const el = document.createElement('details');
  el.className = 'detail-disclosure';
  el.appendChild(label instanceof Element ? label : textElement('summary', label));
  const populate = () => {
    if (!el.open || el.children.length > 1) return;
    for (const node of build()) el.appendChild(node);
  };
  el.addEventListener('toggle', populate);
  el.open = open;
  populate();
  return el;
}

// Index the original tokens rather than re-serializing a parsed object: JSON
// numbers, escaped strings, and even duplicate keys stay as recorded.
function jsonTokens(text) {
  if (typeof text !== 'string' || text.length > 2_000_000) return;
  try { JSON.parse(text); } catch (_) { return; }
  const lexemes = /"(?:[^"\\]+|\\[\s\S])*"|[{}\[\],:]|[^\s{}\[\],:"]+/g;
  const stack = [];
  let root, count = 0;
  for (const [token] of text.matchAll(lexemes)) {
    if (token === '}' || token === ']') { stack.pop(); continue; }
    if (token === ',' || token === ':') continue;
    const parent = stack.at(-1);
    if (parent?.token === '{' && parent.nextKey === undefined) {
      parent.nextKey = token;
      continue;
    }
    if (++count > 20_000) return;
    const node = {token, key: parent?.nextKey};
    if (parent) {
      parent.children.push(node);
      parent.nextKey = undefined;
    } else {
      root = node;
    }
    if (token === '{' || token === '[') {
      node.children = [];
      stack.push(node);
    }
  }
  return root;
}

function jsonRow(tag, key, index, value) {
  const row = textElement(tag, '', 'json-row');
  if (key !== undefined) row.appendChild(textElement('span', key + ': ', 'json-key'));
  else if (index !== undefined) row.appendChild(textElement('span', '[' + index + ']: ', 'json-index'));
  row.appendChild(textElement('span', value));
  return row;
}

function jsonBranch(node, {index, open = false} = {}) {
  const {token, key, children} = node;
  if (!children || !children.length) {
    const value = children ? token + (token === '{' ? '}' : ']') : token;
    if (value.length <= 160) return jsonRow('div', key, index, value);
    const summary = jsonRow('summary', key, index, value.slice(0, 72) + '…');
    const long = disclosure(summary, () => [textElement('pre', value)]);
    long.className = 'json-branch';
    return long;
  }
  const label = token === '{' ? '{…}' : '[…]';
  const unit = token === '{' ? 'field' : 'item';
  const summary = jsonRow('summary', key, index, label + ' ');
  summary.appendChild(textElement('span', children.length + ' ' + unit + (children.length === 1 ? '' : 's'), 'json-count'));
  const branch = disclosure(summary, () => {
    const body = document.createElement('div');
    body.className = 'json-children';
    const more = textElement('button', '', 'detail-link');
    more.type = 'button';
    const page = document.createElement('div');
    let next = 0;
    const appendPage = () => {
      for (const end = Math.min(next + 100, children.length); next < end; next++) {
        page.appendChild(jsonBranch(children[next], {index: next}));
      }
      more.hidden = next === children.length;
      more.textContent = more.hidden ? '' : 'Show next ' + Math.min(100, children.length - next);
    };
    more.addEventListener('click', appendPage);
    appendPage();
    body.appendChild(page);
    body.appendChild(more);
    return [body];
  }, open);
  branch.className = 'json-branch';
  return branch;
}

function jsonView(text, { original = false, rawPanel = false, textClass } = {}) {
  const root = jsonTokens(text);
  if (!root && !rawPanel) return textElement('pre', text, textClass);
  const view = document.createElement('div');
  view.className = 'json-view';
  if (root) view.appendChild(jsonBranch(root, {open: true}));
  else view.appendChild(textElement('p', 'Tree unavailable for oversized or invalid JSON.', 'detail-note'));
  if (original) {
    const source = disclosure('Original JSON', () => [textElement('pre', text)]);
    source.className = 'json-original';
    view.appendChild(source);
  }
  return view;
}

function jsonBlock(value) { return jsonView(JSON.stringify(value, null, 2)); }

function fields(entries) {
  const el = document.createElement('dl');
  el.className = 'detail-fields';
  for (const [label, value] of entries) {
    if (value === undefined || value === '') continue;
    el.appendChild(textElement('dt', label));
    const item = textElement('dd', value instanceof Element ? '' : display(value));
    if (value instanceof Element) item.appendChild(value);
    el.appendChild(item);
  }
  return el;
}

function namedFields(object) {
  return fields(Object.entries(object || {}).map(([key, value]) => [key.charAt(0).toUpperCase() + key.slice(1).replaceAll('_', ' '),
    value == null ? 'Cleared' : typeof value === 'object' ? jsonBlock(value) : value]));
}

function configNodes(config) {
  const {generation_options, autopilot_policy, ...base} = config;
  return [namedFields(base), ...Object.entries({'Generation options':generation_options, 'Autopilot policy':autopilot_policy})
    .filter(([, value]) => value !== undefined)
    .map(([label, value]) => disclosure(label, () => [value ? namedFields(value) : textElement('p', 'Cleared', 'detail-note')]))];
}

function contentBlock(block) {
  switch (block?.type) {
    case 'text':
      return textElement('pre', block.text || '(empty text)', 'message-text');
    case 'thinking':
      return disclosure('Thinking', () => [textElement('pre', block.text || '(no readable text)'),
        fields([['Provider signature', block.signature]])], true);
    case 'tool_call': {
      const call = block.tool_call || {};
      return section('Tool call · ' + (call.name || 'Unknown tool'), fields([['Call ID', call.id]]),
        jsonView(call.input ?? '(no arguments)', {original: true}),
        ...(call.signature ? [disclosure('Provider signature', () => [textElement('pre', call.signature)])] : []));
    }
    case 'tool_result': {
      const result = block.tool_result || {};
      return section('Tool result' + (result.tool_name ? ' · ' + result.tool_name : ''),
        fields([['Call ID', result.tool_call_id], ['Error result', result.is_error || undefined]]),
        ...contentNodes(result.content));
    }
    case 'image': {
      const image = block.image || {};
      const el = section(image.file_name || 'Image', fields([['Media type', image.media_type]]));
      // Only preview inline raster bytes; never fetch a recorded URL or render SVG.
      if (/^image\/(png|jpeg|gif|webp)$/.test(image.media_type || '') && /^[A-Za-z0-9+/]+={0,2}$/.test(image.data || '')) {
        const preview = document.createElement('img');
        preview.alt = image.file_name || 'Recorded image';
        preview.loading = 'lazy';
        preview.src = 'data:' + image.media_type + ';base64,' + image.data;
        el.appendChild(preview);
      } else {
        el.appendChild(textElement('p', 'Preview unavailable. See the raw record.', 'detail-note'));
      }
      return el;
    }
    case 'reasoning': {
      const {summary, ...rest} = block.reasoning || {};
      return section('Provider reasoning', textElement('pre', summary || '(no readable summary)'),
        disclosure('Opaque provider state', () => [jsonBlock(rest)]));
    }
    default:
      return section('Unknown content block', jsonBlock(block));
  }
}

const reminderTitles = new Map([
  ['memory-user', 'User instructions'], ['memory-project', 'Project instructions'],
  ['memory-auto', 'Auto Memory index'], ['memory-recall', 'Auto Memory recall'],
  ['skills-directory', 'Skills directory'], ['group', 'Session group'],
  ['hook-session-start', 'Session start hook'], ['hook-user-prompt-submit', 'User prompt hook'],
  ['restored-files', 'Files restored after compaction'],
]);

function contentNodes(content, role) {
  if (!Array.isArray(content) || !content.length) return [textElement('p', 'No content.', 'detail-note')];
  return content.flatMap(block => {
    if (role !== 'user' || block?.type !== 'text' || typeof block.text !== 'string') return [contentBlock(block)];
    const reminders = [];
    const text = splitTrailingReminders(block.text, (source, body) => reminders.push({source, body}));
    if (!reminders.length) return [contentBlock(block)];
    reminders.reverse();
    return [...(text ? [textElement('pre', text, 'message-text')] : []),
      disclosure('System reminders · ' + reminders.length, () => reminders.map(({source, body}) => section(reminderTitles.get(source) || source || 'Source not recorded',
        textElement('pre', body, 'message-text'))))];
  });
}

function messageTitle(message) {
  const results = Array.isArray(message?.content) && message.content.length && message.content.every(b => b?.type === 'tool_result');
  return (results ? 'Tool results' : message?.role === 'user' ? 'User' : message?.role === 'assistant' ? 'Assistant' : 'Message')
    + (message?.id ? ' · ' + message.id : '');
}

function messageViews(messages, open = false) {
  if (!Array.isArray(messages) || !messages.length) return [textElement('p', 'No messages.', 'detail-note')];
  return messages.map(message => disclosure(messageTitle(message), () => contentNodes(message?.content, message?.role), open));
}

function jumpButton(label, action) {
  const button = textElement('button', label, 'detail-link');
  button.type = 'button';
  button.addEventListener('click', action);
  return button;
}

function eventReference(ref, sessionID) {
  if (!ref || !Number.isSafeInteger(ref.seq) || ref.seq < 1) return undefined;
  const label = (ref.session ? ref.session + ' ' : '') + '#' + ref.seq;
  const id = ref.session || sessionID;
  if (id === state.currentSession && !state.bySequence.has(ref.seq)) return label;
  return jumpButton(label, () => selectEvent(id, ref.seq));
}

function outcomeFields(p) {
  return [['Outcome', p.outcome], ['Duration', p.duration_ms == null ? undefined : p.duration_ms + ' ms'],
    ['Reason', p.reason], ['Error', p.error?.message], ['Retryable', p.error ? (p.error.retryable ? 'Yes' : 'No') : undefined]];
}

function catalogue(title, note, p, renderEntry) {
  const set = Object.entries(p.set || {});
  const remove = Array.isArray(p.remove) ? p.remove : [];
  const trigger = p.cause === 'boot' ? 'Startup' : p.cause === 'resume' ? 'Session resume' : p.cause;
  return section(title, textElement('p', note, 'detail-note'),
    ...set.map(renderEntry),
    ...(!set.length && !remove.length ? [textElement('p', 'No entries changed.', 'detail-note')] : []),
    fields([['Removed', remove.length ? remove.join(', ') : undefined], ['Triggered by', trigger]]));
}

function toolDetails(details) {
  if (typeof details?.Path === 'string') {
    const diff = textElement('pre', '', 'tool-diff');
    if (typeof details.UnifiedDiff === 'string') {
      for (const line of details.UnifiedDiff.split(/(?<=\n)/)) {
        const kind = line.startsWith('+++ ') || line.startsWith('--- ') || line.startsWith('@@') ? 'diff-header'
          : line.startsWith('+') ? 'diff-add' : line.startsWith('-') ? 'diff-remove' : '';
        diff.appendChild(textElement('span', line, kind));
      }
    }
    return section(details.IsNewFile ? 'New file' : 'File changes', fields([['Path', details.Path], ['Replacements', details.EditCount || undefined],
      ['Lines added', details.AddedLines], ['Lines removed', details.RemovedLines], ['Omitted diff lines', details.TruncatedDiffLines || undefined]]), diff);
  }
  if (Number.isInteger(details?.lineCount)) {
    return section('Shell output', fields([['Shell', details.shellLabel], ['Lines', details.lineCount],
      ['Background task', details.taskId], ['Process error', details.error]]),
      ...(details.tail ? [textElement('p', details.tailTruncated ? 'Recorded tail (truncated)' : 'Recorded output', 'detail-note'),
        textElement('pre', details.tail, 'message-text')] : []));
  }
  if (details?.Identity && typeof details.Identity === 'object') {
    return section('Agent result', fields([['Agent', details.Identity.Name], ['Background task', details.TaskID],
      ['Stopped', details.Stopped ? 'Yes' : undefined], ['Error', details.Error]]),
      ...(typeof details.Response === 'string' && details.Response ? [textElement('pre', details.Response, 'message-text')] : []));
  }
  return section('Execution details', typeof details === 'string' ? textElement('pre', details) : jsonBlock(details));
}

function relatedTool(index, id, callID, title) {
  const entry = index.get(callID);
  return entry ? [section(title, fields([['Event', eventReference({seq: entry.seq}, id)]]), contentBlock(entry.block))] : [];
}

function eventDetails(rec, id) {
  const p = rec.payload || {};
  const toolName = state.calls.get(p.call_id)?.block.tool_call?.name;
  switch (rec.type) {
    case 'session.create':
      return [section('Session identity', fields([['Agent', p.agent_id], ['Project', p.project], ['Workspace', p.workspace],
        ['Working directory', p.initial_cwd === p.workspace ? undefined : p.initial_cwd],
        ['Parent session', p.parent?.session ? jumpButton(p.parent.session, () => openSession(p.parent.session)) : undefined],
        ['Parent call', p.parent?.call_id], ['Forked at', eventReference(p.fork, id)]])),
        ...(p.config ? [section('Session settings', ...configNodes(p.config))] : [])];
    case 'session.update':
      return [
        ...(p.config ? [section('Session settings', ...configNodes(p.config))] : []),
        ...(p.state ? [section('Session state', namedFields(p.state))] : []),
      ];
    case 'message.append':
      return [section(p.accepted ? 'Accepted input · queued for consumption' : 'Recorded messages', ...messageViews(p.messages, true))];
    case 'context.compact':
      return [section('Compaction', fields([['Compacted through', eventReference({seq: p.through}, id)]]),
        ...messageViews(p.summary ? [p.summary] : [], true))];
    case 'inference.start':
      return [section('Model call', fields([['Purpose', p.purpose], ['Operation', asides.get(p.purpose)?.operation], ['Turn', p.turn_id], ['Provider', p.request?.provider],
        ['Model', p.request?.model], ['Input', p.input?.standalone ? 'Standalone' : p.input?.conversation ? 'Conversation' : undefined],
        ['Standalone blob', p.input?.standalone], ['Instructions digest', p.instructions_digest], ['Toolset digest', p.toolset_digest]]),
        ...(p.request?.generation_options ? [disclosure('Generation options', () => [namedFields(p.request.generation_options)])] : []))];
    case 'inference.finish':
      return [section('Inference outcome', fields([['Operation', asides.get(inferenceStart(rec)?.payload?.purpose)?.operation],
        ...outcomeFields(p), ['Stop reason', p.stop_reason]]))];
    case 'permission.decide':
      return [section('Permission decision', fields([['Tool', toolName], ['Call ID', p.call_id], ['Decision', p.decision], ['Authority', p.by],
        ['Scope', p.scope], ['Reason', p.reason]])),
        ...(p.asked ? [section('What the user was shown', fields([['Options', p.asked.options?.join(', ')],
          ['Asked at', formatTime(p.asked.at)]]), ...(p.asked.detail == null ? [] : [jsonBlock(p.asked.detail)]))] : []),
        ...relatedTool(state.calls, id, p.call_id, 'Requested invocation')];
    case 'tool.finish':
      return [section('Tool execution', fields([['Tool', toolName], ['Call ID', p.call_id], ...outcomeFields(p),
        ['Child session', p.child_session ? jumpButton(p.child_session, () => openSession(p.child_session)) : undefined]])),
        ...(p.details == null ? [] : [toolDetails(p.details)]),
        ...relatedTool(state.calls, id, p.call_id, 'Requested invocation'),
        ...relatedTool(state.results, id, p.call_id, 'Model-facing result')];
    case 'instructions.update':
      return [catalogue('Instruction changes', 'Parts of the system prompt. See Model · Request for the assembled prompt.', p,
        ([name, value]) => disclosure(name, () => [textElement('pre', value?.content || '(empty)'), fields([['Prompt order', value?.slot]])], true))];
    case 'toolset.update':
      return [catalogue('Tool changes', 'Tool definitions, not calls.', p, ([name, value]) => schemaView(name, value))];
    case 'skills.update':
      return [catalogue('Skill changes', 'Does not rewrite the system prompt. Check input messages for skill content.', p, ([name, value]) => fields([[name, value]]))];
    case 'hook.finish':
      return [section('Hook execution', fields([['Trigger', p.trigger], ['Matcher', p.matcher], ...outcomeFields(p)]),
        textElement('p', 'This event records the hook outcome, not any text it may have added to context.', 'detail-note')),
        ...(p.hook ? [section('Hook source', textElement('pre', p.hook, 'message-text'))] : [])];
    case 'annotation.add':
      return [section('Annotation', fields([['By', p.by_agent ? 'Agent' : 'User'], ['After message', p.after_message]]),
        textElement('pre', p.text || '(empty annotation)', 'message-text'))];
    default:
      return [];
  }
}

function schemaView(name, schema) {
  return disclosure(name, () => [fields([['Description', schema?.description], ['Strict', schema?.strict]]), jsonBlock(schema?.definition ?? schema)]);
}

function promptNodes(request) {
  const tools = request.Tools ?? [];
  return [textElement('pre', request.System || '(empty system prompt)'),
    section('Available tools · ' + tools.length, ...tools.map(tool => schemaView(tool.name || 'Unnamed tool', tool)))];
}

function requestNodes(request, messageLabel = 'Input messages') {
  const messages = request.Messages ?? [];
  const tools = request.Tools ?? [];
  return [disclosure('System prompt', () => [textElement('pre', request.System || '(empty system prompt)')]),
    disclosure('Tool definitions · ' + tools.length, () => tools.length
      ? tools.map(tool => schemaView(tool.name || 'Unnamed tool', tool))
      : [textElement('p', 'No tools.', 'detail-note')]),
    section(messageLabel + ' · ' + messages.length, ...messageViews(messages))];
}

function rawRequest(load) {
  return disclosure('Raw SDK request', () => {
    const body = document.createElement('div');
    loadSnapshot(body, load, json => [jsonView(json, {original: true, rawPanel: true})]);
    return [textElement('p', 'As admitted to the SDK, before provider-specific encoding.', 'detail-note'), body];
  });
}

function recallFileList(title, files) {
  if (!files.length) return section(title + ' · 0', textElement('p', 'None.', 'detail-note'));
  const list = document.createElement('ul');
  for (const file of files) list.appendChild(textElement('li', file));
  return section(title + ' · ' + files.length, list);
}

function recallRequestNodes(request) {
  const messages = request.Messages;
  if (!Array.isArray(messages) || ![1, 2, 3].includes(messages.length) || !messages.every(message =>
    message?.role === 'user' && Array.isArray(message.content) && message.content.every(b => b?.type === 'text' && typeof b.text === 'string'))) return [];
  const header = 'Memory files:\n', queryHeader = 'User query:\n', activeHeader = 'Already in main context:\n';
  const shortlistHeader = 'Candidates for new recall:\n';
  const text = message => message.content.map(b => b.text);
  let candidates, known, question;
  if (messages.length === 3) {
    if (!messages.every(message => message.content.length === 1)) return [];
    const [index, context, query] = messages.map(message => message.content[0].text);
    if (!index.startsWith(header) || !context.startsWith(activeHeader)) return [];
    candidates = index.slice(header.length);
    known = context.slice(activeHeader.length);
    question = query;
  } else if (messages.length === 2) {
    const [index] = text(messages[0]);
    const suffix = text(messages[1]);
    if (messages[0].content.length !== 1 || !index.startsWith(header) || suffix.at(-2) !== queryHeader) return [];
    candidates = index.slice(header.length);
    question = suffix.at(-1);
    for (const block of suffix.slice(0, -2)) {
      if (block.startsWith(shortlistHeader)) candidates = block.slice(shortlistHeader.length);
      else if (block.startsWith(activeHeader)) known = block.slice(activeHeader.length);
      else return [];
    }
  } else {
    const blocks = text(messages[0]);
    const [index, query] = blocks.slice(-2);
    const footer = '\n' + queryHeader;
    if (![2, 3].includes(blocks.length) || !index.startsWith(header) || !index.endsWith(footer) ||
        (blocks.length === 3 && !blocks[0].startsWith(activeHeader))) return [];
    candidates = index.slice(header.length, -footer.length);
    question = query;
    if (blocks.length === 3) known = blocks[0].slice(activeHeader.length);
  }
  const items = value => value.split('\n').filter(line => line.startsWith('- ')).map(line => line.slice(2));
  const active = known ? items(known) : [];
  return [recallFileList('Candidates for new recall', items(candidates).filter(line => !active.some(id => line.startsWith(id + ': ')))),
    ...(known ? [recallFileList('Already in main context', active)] : []),
    section('User question', textElement('pre', question, 'message-text'))];
}

function recallSelection(output) {
  if (output?.truncated || typeof output?.text !== 'string') return null;
  const start = output.text.indexOf('{'), end = output.text.lastIndexOf('}');
  if (start < 0 || end < start) return null;
  try {
    const files = JSON.parse(output.text.slice(start, end + 1))?.selected_memories;
    if (Array.isArray(files) && files.every(file => typeof file === 'string')) {
      return recallFileList('Files selected by the model', [...new Set(files)]);
    }
  } catch (_) { /* Keep malformed selector output visible in the raw view. */ }
  return null;
}

function responseMetrics(usage) {
  const tokens = key => Number.isFinite(usage?.[key]) ? usage[key].toLocaleString() : usage == null ? 'Unavailable' : 'Not reported';
  const read = tokens('cache_read');
  return section('Token usage', fields([
    ['Input tokens', tokens('input')], ['Output tokens', tokens('output')],
    ['Cache-read tokens', usage?.cache_read > 0 ? `${read} · reported hit` : read],
    ['Cache-write tokens', tokens('cache_write')],
  ]), ...(usage == null ? [] : [disclosure('Raw usage', () => [jsonBlock(usage)])]));
}

function maintainPrefixPanel(start, id) {
  const body = document.createElement('div');
  const signal = state.contextAbort?.signal;
  loadSnapshot(body, async () => JSON.parse(await readSessionJSON(id, 'request', start.seq + '?view=update-prefix', signal)), result => {
    if (result.status === 'match') return [fields([
      ['SDK prefix', 'Matches completed main request and its assistant output'],
      ['Matching messages', result.count], ['Compared main request', eventReference({seq: result.source}, id)],
    ])];
    const summary = {
      'mismatch': 'Does not match a completed main request and its assistant output on this route',
      'route-mismatch': 'No completed main request on this provider and model',
      'no-completed-turn': 'No completed main request before Update admission',
    }[result.status] ?? 'Comparison unavailable';
    return [fields([['SDK prefix', summary]])];
  });
  return section('Update SDK prefix', body,
    textElement('p', 'Compares system prompt, tool definitions and message prefix before provider encoding. A match does not guarantee a provider cache hit.', 'detail-note'));
}

function updateModelOutput() {
  const panel = state.outputPanel;
  if (!panel) return;
  const pair = state.inferences.get(panel.start?.seq);
  const finish = panel.finish ?? pair?.finish;
  const isAside = asides.has(panel.start?.payload?.purpose);
  const count = (pair?.messages.length ?? 0) + (pair?.annotations.length ?? 0);
  if (finish === panel.rendered && count === panel.resultCount) return;
  panel.rendered = finish;
  panel.resultCount = count;
  const id = state.currentSession;
  const link = !finish ? null : panel.finish ? fields([['Request', eventReference(finish.source, id)]])
    : fields([['Response', eventReference({seq: finish.seq}, id)]]);
  const output = finish?.payload?.output;
  const recordedMessages = rec => rec.type === 'context.compact' ? [rec.payload.summary] : rec.payload.messages;
  const resultView = rec => section(rec.type === 'annotation.add' ? 'Annotation' : 'Recorded messages',
    fields([['Event', eventReference({seq: rec.seq}, id)]]),
    ...(rec.type === 'annotation.add' ? [textElement('pre', rec.payload.text, 'message-text')] : messageViews(recordedMessages(rec), true)));
  let nodes;
  if (!finish) {
    nodes = [textElement('p', 'No response recorded yet.', 'detail-note')];
  } else if (output && typeof output.text === 'string') {
    nodes = [output.text ? jsonView(output.text, {original: true, textClass: 'message-text'}) : textElement('p', 'No text returned.', 'detail-note')];
    if (output.truncated) nodes.push(textElement('p', 'Recorded output was truncated.', 'detail-note'));
  } else if (!isAside && pair?.messages.length) {
    nodes = messageViews(pair.messages.flatMap(recordedMessages), true);
  } else if (!isAside && pair?.annotations.length) {
    nodes = pair.annotations.map(resultView);
  } else {
    const label = finish.payload?.outcome === 'success' ? 'No model output was recorded.' : 'No completed output.';
    nodes = [textElement('p', label, 'detail-note')];
  }
  if (isAside) {
    const selection = panel.start?.payload?.purpose === 'recall' ? recallSelection(output) : null;
    const results = [...(pair?.messages ?? []), ...(pair?.annotations ?? [])].sort((a, b) => a.seq - b.seq);
    nodes = [...(selection ? [selection] : []), section('Raw model output', ...nodes), section('Accepted result', ...(results.length ? results.map(resultView)
      : [textElement('p', 'No accepted result recorded.', 'detail-note')]))];
  }
  if (finish) {
    nodes.unshift(responseMetrics(finish.payload?.usage));
    if (panel.start?.payload?.purpose === 'memory-update') {
      panel.prefix ??= maintainPrefixPanel(panel.start, id);
      nodes.splice(1, 0, panel.prefix);
    }
  }
  panel.node.replaceChildren(...(link ? [link] : []), ...nodes);
}

function detailPanel(title, build, open = false) {
  const panel = disclosure(title, build, open);
  panel.className = 'detail-group detail-panel';
  return panel;
}

function contextPanel(load, open = true) {
  return detailPanel('Context', () => {
    const body = document.createElement('div');
    loadSnapshot(body, load, snapshot => [
      textElement('p', 'Session state after the selected step, not a model request.', 'detail-scope'),
      ...requestNodes(snapshot.context, 'Messages'),
    ]);
    return [body];
  }, open);
}

function eventPanel(rec, id, meta) {
  return detailPanel('Event', () => {
    try {
      return [...(supported(rec) ? eventDetails(rec, id) : []), meta];
    } catch (_) {
      return [textElement('p', 'Malformed payload. See Raw batch.', 'detail-note'), meta];
    }
  });
}

function rawBatch(rec) {
  return detailPanel('Raw batch', () => [
    textElement('p', 'Committed batch as streamed. It can contain multiple events.', 'detail-note'),
    jsonView(rec._batchJSON, {original: true, rawPanel: true}),
  ]);
}
