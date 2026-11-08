const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { join } = require('node:path');
const { test } = require('node:test');
const vm = require('node:vm');

class Element {
  constructor() {
    this.children = [];
    this.textContent = '';
    this.innerHTML = '';
    this.style = {};
    this.dataset = {};
    const classes = new Set();
    this.classList = {
      add: (name) => classes.add(name),
      remove: (name) => classes.delete(name),
      contains: (name) => classes.has(name),
      toggle: (name, on) => on ? classes.add(name) : classes.delete(name),
    };
  }
  addEventListener() {}
  querySelectorAll() { return []; }
  appendChild(child) { this.children.push(child); }
  remove() { this.removed = true; }
  replaceChildren(...children) { this.children = children; }
}

async function viewer(typed = false) {
  const elements = new Map();
  const element = (id) => {
    if (!elements.has(id)) elements.set(id, new Element());
    return elements.get(id);
  };
  const requests = [];
  const context = vm.createContext({
    URL,
    document: {
      querySelector: element,
      getElementById: (id) => id === 'integrity-banner' ? null : element('#' + id),
      createElement: () => new Element(),
      addEventListener() {},
    },
    EventSource: class { close() {} },
    setInterval() {},
    fetch: (url) => {
      if (url === '/api/sessions') return Promise.resolve({ ok: true, json: async () => [] });
      if (!typed && url.endsWith('/events')) return Promise.resolve({ ok: false, status: 409, json: async () => ({code: 'legacy_format'}) });
      return new Promise((resolve, reject) => requests.push({
        url, reject,
        resolve: (body, status = 200) => resolve({ ok: status === 200, status, json: async () => body }),
      }));
    },
  });
  vm.runInContext(readFileSync(join(__dirname, 'assets/app.js'), 'utf8') +
    '\n globalThis.viewer = {state, openSession, showDetail, refreshPromptPanel, renderInline, classify, passesFilter};', context);
  await new Promise(setImmediate);
  return { ...context.viewer, element, requests };
}

test('late detail responses cannot overwrite another record or session', async () => {
  for (const change of ['record', 'session']) {
    for (const fail of [false, true]) {
      const v = await viewer();
      await v.openSession('first');
      v.state.records.push({ id: 'old', type: 'inference.requested' }, { id: 'new', type: 'message.appended' });
      const pending = v.showDetail(0, new Element());
      if (change === 'session') await v.openSession('second');
      else await v.showDetail(1, new Element());
      const detail = v.element('#detail-body');
      const before = detail.children;
      if (fail) v.requests[0].reject(new Error('old request failed'));
      else v.requests[0].resolve({ composedSystem: 'old session prompt' });
      await pending;
      assert.equal(detail.children, before, `${change}, failure=${fail}`);
    }
  }
});

test('late prompt responses cannot overwrite the selected record', async () => {
  for (const fail of [false, true]) {
    const v = await viewer();
    await v.openSession('first');
    v.element('main').classList.add('show-prompt');
    v.state.selectedRecordID = 'old';
    const pending = v.refreshPromptPanel({ id: 'old' });
    v.state.selectedRecordID = 'new';
    const current = v.refreshPromptPanel({ id: 'new' });
    v.requests[1].resolve({ composedSystem: 'current prompt' });
    await current;
    const body = v.element('#prompt-body');
    const html = body.innerHTML, children = body.children;
    if (fail) v.requests[0].reject(new Error('old request failed'));
    else v.requests[0].resolve({ composedSystem: 'obsolete prompt' });
    await pending;
    assert.equal(body.innerHTML, html);
    assert.equal(body.children, children);
  }
});

test('retired event streams cannot change a newly selected session', async () => {
  const v = await viewer();
  await v.openSession('first');
  const old = v.state.eventSource;
  await v.openSession('second');
  old.onmessage({ data: JSON.stringify({ type: 'app', name: 'session.started' }) });
  old.onopen();
  old.onerror();
  assert.equal(v.state.records.length, 0);
  assert.equal(v.element('#status').textContent, 'connecting');
});

test('transcript Markdown never creates executable links', async () => {
  const v = await viewer();
  for (const url of ['javascript:alert%281%29', 'JaVaScRiPt:alert%281%29', 'data:text/html,hello', 'vbscript:msgbox']) {
    assert.ok(!v.renderInline(`[link](${url})`).includes('<a '), url);
  }
  for (const url of ['https://example.com', 'http://localhost/', 'mailto:a@example.com', '/relative']) {
    assert.ok(v.renderInline(`[link](${url})`).includes('<a '), url);
  }
});

test('typed pages inspect safe payloads without legacy streams or replay', async () => {
  const v = await viewer(true);
  const opening = v.openSession('typed');
  const event = {id: 'e1', seq: 1, type: 'toolset.update', json: '{"payload":{"text":"<img onerror=alert(1)>","const":9007199254740993}}'};
  v.requests[0].resolve({events: [event], nextCursor: 'opaque/+', throughSeq: 2});
  await opening;
  assert.equal(v.state.eventSource, null);
  assert.match(v.element('#session-meta').textContent, /resume unsupported/);
  const timeline = v.element('#timeline');
  timeline.children[0].onclick();
  const pre = v.element('#detail-body').children[1];
  assert.match(pre.textContent, /<img/);
  assert.match(pre.textContent, /9007199254740993/);
  assert.equal(pre.textContent, event.json);
  assert.equal(pre.innerHTML, '');
  const more = timeline.children[1].children[1];
  const paging = more.onclick();
  assert.match(v.requests[1].url, /cursor=opaque%2F%2B/);
  v.requests[1].resolve({events: [event, {...event, id: 'e2', seq: 2}], nextCursor: '', throughSeq: 2});
  await paging;
  assert.equal(v.state.seenIDs.size, 2);
  await v.refreshPromptPanel(event);
  assert.equal(v.requests.length, 2);
  const refresh = timeline.children.at(-1).children[0].onclick();
  assert.equal(v.state.seenIDs.size, 0);
  v.requests[2].resolve({events: [], nextCursor: '', throughSeq: 3});
  await refresh;
  assert.match(v.element('#session-meta').textContent, /seq 3/);
});

test('typed selection fences stale pages and never hides corruption as legacy', async () => {
  const v = await viewer(true);
  const old = v.openSession('old');
  const current = v.openSession('new');
  v.requests[1].resolve({code: 'invalid_log', error: 'unsupported version'}, 422);
  await current;
  v.requests[0].resolve({events: [{id: 'old', seq: 1}], throughSeq: 1});
  await old;
  assert.equal(v.state.eventSource, null);
  assert.equal(v.element('#detail-body').textContent, 'unsupported version');
  assert.equal(v.state.seenIDs.size, 0);
});

test('the state filter controls session state patches', async () => {
  const v = await viewer();
  v.state.filters.state = false;
  assert.equal(v.passesFilter(v.classify('session.state.patched')), false);
});
