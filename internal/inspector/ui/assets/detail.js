function section(title, ...nodes) {
  const el = document.createElement('section');
  el.appendChild(textElement('h3', title, 'section-header'));
  for (const node of nodes) el.appendChild(node);
  return el;
}

function disclosure(title, build, open = false) {
  const el = document.createElement('details');
  el.className = 'detail-disclosure';
  el.appendChild(textElement('summary', title));
  const populate = () => {
    if (!el.open || el.children.length > 1) return;
    for (const node of build()) el.appendChild(node);
  };
  el.addEventListener('toggle', populate);
  el.open = open;
  populate();
  return el;
}

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
        textElement('pre', call.input ?? '(no arguments)'),
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

function contentNodes(content) {
  return Array.isArray(content) && content.length ? content.map(contentBlock) : [textElement('p', 'No content.', 'detail-note')];
}

function messageTitle(message) {
  const results = Array.isArray(message?.content) && message.content.length && message.content.every(b => b?.type === 'tool_result');
  return (results ? 'Tool results' : message?.role === 'user' ? 'User' : message?.role === 'assistant' ? 'Assistant' : 'Message')
    + (message?.id ? ' · ' + message.id : '');
}

function messageViews(messages, open = false) {
  if (!Array.isArray(messages) || !messages.length) return [textElement('p', 'No messages.', 'detail-note')];
  return messages.map(message => disclosure(messageTitle(message), () => contentNodes(message?.content), open));
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

function catalogue(p, renderEntry) {
  const set = Object.entries(p.set || {});
  const remove = Array.isArray(p.remove) ? p.remove : [];
  return section('Catalogue changes', fields([['Cause', p.cause], ['Removed', remove.length ? remove.join(', ') : undefined]]),
    ...set.map(renderEntry),
    ...(!set.length && !remove.length ? [textElement('p', 'No entries changed.', 'detail-note')] : []));
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
        ...(p.config ? [section('Initial configuration', ...configNodes(p.config))] : [])];
    case 'session.update':
      return [section('Session changes', ...[p.config && section('Configuration', ...configNodes(p.config)),
        p.state && section('State', namedFields(p.state))].filter(Boolean))];
    case 'message.append':
      return [section(p.accepted ? 'Accepted input · queued for consumption' : 'Recorded messages', ...messageViews(p.messages, true))];
    case 'context.compact':
      return [section('Compaction', fields([['Compacted through', eventReference({seq: p.through}, id)]]),
        ...messageViews(p.summary ? [p.summary] : [], true))];
    case 'inference.start':
      return [section('Model call', fields([['Purpose', p.purpose], ['Turn', p.turn_id], ['Provider', p.request?.provider],
        ['Model', p.request?.model], ['Input', p.input?.standalone ? 'Standalone' : p.input?.conversation ? 'Conversation' : undefined],
        ['Standalone blob', p.input?.standalone], ['Instructions digest', p.instructions_digest], ['Toolset digest', p.toolset_digest]]),
        ...(p.request?.generation_options ? [disclosure('Generation options', () => [namedFields(p.request.generation_options)])] : []))];
    case 'inference.finish':
      return [section('Inference outcome', fields([...outcomeFields(p), ['Stop reason', p.stop_reason]])),
        ...(p.usage ? [section('Token usage', namedFields(p.usage))] : [])];
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
      return [catalogue(p, ([name, value]) => disclosure(name, () => [fields([['Slot', value?.slot]]), textElement('pre', value?.content || '(empty)')], true))];
    case 'toolset.update':
      return [catalogue(p, ([name, value]) => schemaView(name, value))];
    case 'skills.update':
      return [catalogue(p, ([name, value]) => fields([[name, value]]))];
    case 'hook.finish':
      return [section('Hook execution', fields([['Trigger', p.trigger], ['Matcher', p.matcher], ...outcomeFields(p)])),
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
  return [disclosure('System prompt and tools', () => promptNodes(request)),
    section(messageLabel + ' · ' + messages.length, ...messageViews(messages))];
}

function snapshotDisclosure(title, load, render) {
  return disclosure(title, () => {
    const body = document.createElement('div');
    loadSnapshot(body, load, render);
    return [body];
  });
}

function recordData(rec, load) {
  const raw = disclosure('Event payload', () => [jsonBlock(rec._raw)], !supported(rec));
  const batch = disclosure('Committed batch JSON', () => [rec._exactJSON ? textElement('pre', rec._exactJSON) : jsonBlock(rec._batch)]);
  if (!supported(rec)) return [raw, batch];
  return [disclosure('Technical details', () => [raw, batch, ...(load ? [
    ...(rec.type === 'context.compact' ? [] : [snapshotDisclosure('Context after this step', load,
      snapshot => requestNodes(snapshot.context, 'Context messages'))]),
    snapshotDisclosure('Exact snapshot JSON', load, snapshot => [textElement('pre', snapshot._exactJSON)]),
  ] : [])])];
}
