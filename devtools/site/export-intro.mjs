#!/usr/bin/env node
// Export the README's two GIFs from the website's deterministic HTML timeline.
import { spawn, execFile } from 'node:child_process';
import { once } from 'node:events';
import { createServer } from 'node:http';
import { copyFile, mkdir, mkdtemp, readFile, rename, rm, stat, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';

const repo = fileURLToPath(new URL('../../', import.meta.url));
const chromePath = process.env.SAN_CHROME || (process.platform === 'darwin'
  ? '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' : 'google-chrome');
const run = promisify(execFile);
const fps = 12;

async function connect(url) {
  const socket = new WebSocket(url);
  await once(socket, 'open');
  let nextID = 0;
  const pending = new Map();
  const events = new Map();
  const errors = [];
  socket.addEventListener('message', event => {
    const message = JSON.parse(event.data);
    if (message.method === 'Runtime.exceptionThrown') errors.push(message.params.exceptionDetails);
    const listener = events.get(message.method);
    if (listener) {
      events.delete(message.method);
      clearTimeout(listener.timer);
      listener.resolve(message.params);
    }
    const call = pending.get(message.id);
    if (!call) return;
    pending.delete(message.id);
    clearTimeout(call.timer);
    if (message.error) call.reject(new Error(JSON.stringify(message.error)));
    else call.resolve(message.result);
  });
  return {
    errors,
    close: () => socket.close(),
    waitFor(method) {
      return new Promise((resolve, reject) => {
        const timer = setTimeout(() => {
          events.delete(method);
          reject(new Error(`${method} timed out`));
        }, 30000);
        events.set(method, { resolve, reject, timer });
      });
    },
    send(method, params = {}) {
      return new Promise((resolve, reject) => {
        const id = ++nextID;
        const timer = setTimeout(() => {
          pending.delete(id);
          reject(new Error(`${method} timed out`));
        }, 30000);
        pending.set(id, { resolve, reject, timer });
        socket.send(JSON.stringify({ id, method, params }));
      });
    },
  };
}

async function evaluate(client, expression) {
  const value = await client.send('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
  if (value.exceptionDetails) throw new Error(JSON.stringify(value.exceptionDetails));
  return value.result.value;
}

async function capture(theme, origin, directory) {
  const frames = join(directory, theme);
  await mkdir(frames);
  const chrome = spawn(chromePath, [
    '--headless=new', '--disable-gpu', '--hide-scrollbars', '--no-first-run',
    '--no-default-browser-check', '--remote-debugging-port=0',
    `--user-data-dir=${join(directory, 'chrome-' + theme)}`, 'about:blank',
  ], { stdio: ['ignore', 'ignore', 'pipe'] });
  let client;
  try {
    const endpoint = await new Promise((resolve, reject) => {
      let log = '';
      const timer = setTimeout(() => reject(new Error('Chrome did not start')), 15000);
      chrome.once('error', error => { clearTimeout(timer); reject(error); });
      chrome.once('exit', code => { clearTimeout(timer); reject(new Error(`Chrome exited: ${code}`)); });
      chrome.stderr.on('data', chunk => {
        log += chunk;
        const match = log.match(/DevTools listening on (ws:\/\/[^\s]+)/);
        if (match) { clearTimeout(timer); resolve(match[1]); }
      });
    });
    const debugOrigin = new URL(endpoint).origin.replace('ws:', 'http:');
    const pages = await fetch(debugOrigin + '/json/list').then(response => response.json());
    client = await connect(pages.find(page => page.type === 'page').webSocketDebuggerUrl);
    await client.send('Page.enable');
    await client.send('Runtime.enable');
    await client.send('Emulation.setDeviceMetricsOverride', { width: 1280, height: 720, deviceScaleFactor: 1, mobile: false });
    const loaded = client.waitFor('Page.loadEventFired');
    await Promise.all([
      loaded,
      client.send('Page.navigate', { url: `${origin}/intro.html?variant=readme&theme=${theme}&t=0` }),
    ]);
    // Wait for fonts rather than exporting a fallback-font animation.
    await evaluate(client, `(async () => {
      for (let i = 0; i < 200; i++) {
        if (window.sanIntro) {
          await document.fonts.ready;
          if (![...document.fonts].some(font => font.family.includes('Bangers') && font.status === 'loaded'))
            throw new Error('The comic display font did not load');
          return true;
        }
        await new Promise(resolve => setTimeout(resolve, 50));
      }
      throw new Error('The intro did not load');
    })()`);
    const duration = await evaluate(client, 'window.sanIntro.duration');
    const count = Math.ceil(duration * fps);
    console.log(`${theme}: capturing ${count} frames (${duration.toFixed(1)}s)`);
    for (let index = 0; index < count; index++) {
      await evaluate(client, `window.sanIntro.seek(${index / fps})`);
      const { data } = await client.send('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
      await writeFile(join(frames, `${String(index).padStart(5, '0')}.png`), Buffer.from(data, 'base64'));
      if ((index + 1) % 240 === 0) console.log(`${theme}: ${index + 1}/${count} frames`);
    }
    if (client.errors.length) throw new Error(JSON.stringify(client.errors));
  } finally {
    client?.close();
    if (chrome.pid && chrome.exitCode === null && chrome.signalCode === null) {
      const stopped = once(chrome, 'exit');
      chrome.kill('SIGTERM');
      await stopped;
    }
  }
  const gif = join(directory, `${theme}.gif`);
  await run('ffmpeg', [
    '-hide_banner', '-loglevel', 'error', '-y', '-framerate', String(fps),
    '-i', join(frames, '%05d.png'), '-filter_complex',
    'split[a][b];[a]palettegen=stats_mode=full[p];[b][p]paletteuse=dither=none:diff_mode=rectangle',
    '-loop', '0', gif,
  ]);
  return gif;
}

if (typeof WebSocket === 'undefined') throw new Error('Use Node.js with a built-in WebSocket implementation (Node 24+).');
const directory = await mkdtemp(join(tmpdir(), 'san-intro-export-'));
const html = await readFile(join(repo, 'site/intro.html'));
const server = createServer((request, response) => {
  if (new URL(request.url, 'http://localhost').pathname !== '/intro.html') {
    response.writeHead(404); response.end(); return;
  }
  response.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' });
  response.end(html);
});
server.listen(0, '127.0.0.1');
await once(server, 'listening');
try {
  const origin = `http://127.0.0.1:${server.address().port}`;
  const results = [];
  for (const theme of ['light', 'dark']) results.push(await capture(theme, origin, directory));
  const names = ['san-intro.gif', 'san-intro-dark.gif'];
  // Prepare both exports before replacing either checked-in asset.
  for (const [index, gif] of results.entries()) {
    const target = join(repo, 'assets', names[index]);
    const staged = `${target}.${process.pid}.tmp`;
    await copyFile(gif, staged);
    await rename(staged, target);
    console.log(`${names[index]}: ${((await stat(target)).size / 1024 / 1024).toFixed(2)} MB`);
  }
} finally {
  await new Promise(resolve => server.close(resolve));
  await rm(directory, { recursive: true, force: true });
}
