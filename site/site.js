'use strict';

document.documentElement.classList.add('js');

const zh = document.documentElement.lang === 'zh';
document.getElementById('year').textContent = new Date().getFullYear();

const nav = document.getElementById('nav');
const toggle = document.querySelector('.nav-toggle');
function closeNav() {
  nav.classList.remove('open');
  toggle.setAttribute('aria-expanded', 'false');
  closeLanguage();
}
toggle.addEventListener('click', () => {
  toggle.setAttribute('aria-expanded', String(nav.classList.toggle('open')));
});
document.querySelectorAll('#nav-links a, .logo a').forEach(link => link.addEventListener('click', closeNav));

const tabs = [...document.querySelectorAll('.install-tab')];
function selectTab(tab) {
  tabs.forEach(item => {
    const selected = item === tab;
    item.classList.toggle('active', selected);
    item.setAttribute('aria-selected', String(selected));
    item.tabIndex = selected ? 0 : -1;
  });
  document.getElementById('install-cmd').textContent = tab.dataset.cmd;
  document.getElementById('install-panel').setAttribute('aria-labelledby', tab.id);
}
tabs.forEach((tab, index) => {
  tab.addEventListener('click', () => selectTab(tab));
  tab.addEventListener('keydown', event => {
    let target;
    if (event.key === 'ArrowRight') target = tabs[(index + 1) % tabs.length];
    if (event.key === 'ArrowLeft') target = tabs[(index + tabs.length - 1) % tabs.length];
    if (event.key === 'Home') target = tabs[0];
    if (event.key === 'End') target = tabs[tabs.length - 1];
    if (!target) return;
    event.preventDefault();
    selectTab(target);
    target.focus();
  });
});

document.querySelectorAll('[data-copy]').forEach(button => {
  button.addEventListener('click', async () => {
    const label = zh ? '复制' : 'Copy';
    clearTimeout(button.copyTimer);
    button.disabled = true;
    try {
      await navigator.clipboard.writeText(document.getElementById(button.dataset.copy).textContent);
      button.textContent = zh ? '已复制' : 'Copied';
    } catch {
      button.textContent = zh ? '请手动复制' : 'Select to copy';
      const range = document.createRange();
      range.selectNodeContents(document.getElementById(button.dataset.copy));
      const selection = window.getSelection();
      selection.removeAllRanges();
      selection.addRange(range);
    }
    button.disabled = false;
    button.copyTimer = setTimeout(() => { button.textContent = label; }, 3000);
  });
});

const introPlay = document.getElementById('intro-play');
if (introPlay) {
  const player = document.getElementById('intro-player');
  const pause = document.getElementById('intro-pause');
  const replay = document.getElementById('intro-replay');
  const status = document.getElementById('intro-status');
  let frame;
  const caption = document.createElement('p');
  caption.className = 'intro-caption';
  caption.hidden = true;
  player.after(caption);
  let loadTimer;
  function failed() {
    clearTimeout(loadTimer);
    frame?.remove();
    frame = null;
    introPlay.removeAttribute('aria-disabled');
    status.textContent = zh ? '加载失败，请重试或打开独立页面。' : 'Could not load. Try again or open standalone.';
  }
  // intro.html owns a 1280 × 720 stage and scales it to this 16:9 viewport.
  function syncPause() {
    const isPaused = frame.contentWindow.sanIntro.paused;
    pause.textContent = isPaused ? (zh ? '继续播放' : 'Play') : (zh ? '暂停' : 'Pause');
  }
  introPlay.setAttribute('role', 'button');
  introPlay.addEventListener('keydown', event => {
    if (event.key === ' ') { event.preventDefault(); introPlay.click(); }
  });
  introPlay.addEventListener('click', event => {
    event.preventDefault();
    if (frame) return;
    const focusBeforeLoad = document.activeElement;
    status.textContent = zh ? '正在加载动画…' : 'Loading intro…';
    introPlay.setAttribute('aria-disabled', 'true');
    frame = document.createElement('iframe');
    const loadingFrame = frame;
    frame.title = zh ? 'San 动画介绍（英文漫画）' : 'San animated introduction';
    frame.src = 'intro.html?embed=1' + (zh ? '&lang=zh' : '');
    frame.hidden = true;
    loadTimer = setTimeout(failed, 15000);
    frame.addEventListener('error', failed, { once: true });
    frame.addEventListener('load', () => {
      if (frame !== loadingFrame) return;
      try {
        if (!frame.contentWindow.sanIntro) { failed(); return; }
      } catch { failed(); return; }
      clearTimeout(loadTimer);
      introPlay.removeAttribute('aria-disabled');
      frame.contentWindow.addEventListener('sanintrochapter', event => {
        caption.textContent = event.detail;
      });
      caption.hidden = false;
      // Match the poster's settled opening exactly before continuing the timeline.
      frame.contentWindow.sanIntro.seek(3.6);
      frame.hidden = false;
      player.querySelector('img').hidden = true;
      caption.textContent = frame.contentWindow.sanIntro.caption;
      if (!matchMedia('(prefers-reduced-motion: reduce)').matches) frame.contentWindow.sanIntro.play();
      syncPause();
      const restoreFocus = document.activeElement === introPlay || document.activeElement === focusBeforeLoad;
      introPlay.hidden = true;
      pause.hidden = replay.hidden = false;
      status.textContent = '';
      if (restoreFocus) {
        pause.focus({ preventScroll: true });
      }
      // Reflect both the embedded player's keyboard and pointer controls.
      new MutationObserver(syncPause).observe(frame.contentDocument.getElementById('play-toggle'), { childList: true });
    }, { once: true });
    player.appendChild(frame);
  });
  pause.addEventListener('click', () => {
    frame.contentDocument.getElementById('play-toggle').click();
    syncPause();
  });
  replay.addEventListener('click', () => {
    frame.contentDocument.getElementById('play-restart').click();
    syncPause();
  });
}

const languageButton = document.getElementById('lang-btn');
const languageMenu = document.getElementById('lang-menu');
const page = location.pathname.split('/').pop() || 'index.html';
const base = page.replace(/(?:\.zh)?\.html$/, '');
languageMenu.replaceChildren();
[['en', 'EN'], ['zh', '中文']].forEach(([code, label]) => {
  const link = document.createElement('a');
  link.textContent = label;
  link.lang = code;
  link.hreflang = code;
  link.href = base + (code === 'zh' ? '.zh.html' : '.html') + location.hash;
  if (code === document.documentElement.lang) link.setAttribute('aria-current', 'page');
  languageMenu.appendChild(link);
});
function closeLanguage() {
  languageMenu.classList.remove('open');
  languageButton.setAttribute('aria-expanded', 'false');
}
languageButton.addEventListener('click', event => {
  event.stopPropagation();
  languageMenu.querySelectorAll('a').forEach(link => { link.hash = location.hash; });
  languageButton.setAttribute('aria-expanded', String(languageMenu.classList.toggle('open')));
});
document.addEventListener('click', event => {
  if (!event.target.closest('.lang-switch')) closeLanguage();
  if (!nav.contains(event.target)) closeNav();
});
nav.addEventListener('focusout', event => {
  if (!nav.contains(event.relatedTarget)) closeNav();
  else if (!event.relatedTarget.closest('.lang-switch')) closeLanguage();
});
matchMedia('(max-width: 900px)').addEventListener('change', closeNav);
languageButton.setAttribute('aria-label', zh ? '选择语言' : 'Choose language');
nav.setAttribute('aria-label', zh ? '主导航' : 'Main navigation');
document.addEventListener('keydown', event => {
  if (event.key !== 'Escape') return;
  if (languageMenu.classList.contains('open')) {
    closeLanguage();
    languageButton.focus();
  } else if (nav.classList.contains('open')) {
    closeNav();
    toggle.focus();
  }
});
