import { parseBlocks } from './blocks.js';
import { formatRemaining, msUntil, isExpired } from './countdown.js';
import { buildFilename, downloadText } from './download.js';

const slug = window.__SLUG__;
const $content = document.getElementById('content');
const $lines = document.getElementById('lines');
const $status = document.getElementById('status');
const $copyAll = document.getElementById('copy-all');
const $copyLink = document.getElementById('copy-link');
const $downloadAll = document.getElementById('download-all');
const $countdown = document.getElementById('countdown');
const $overlay = document.getElementById('expired-overlay');
const $session = document.getElementById('session');
const $toggleView = document.getElementById('toggle-view');

function setEditing(on) {
  if (!$session || !$toggleView) return;
  $session.classList.toggle('editing', on);
  $toggleView.textContent = on ? 'Righe' : 'Modifica';
  $toggleView.setAttribute('aria-pressed', String(on));
  if (on) {
    requestAnimationFrame(() => { try { $content.focus({ preventScroll: true }); } catch {} });
  }
}

if ($toggleView) {
  $toggleView.addEventListener('click', () => {
    const isEditing = $session.classList.contains('editing');
    setEditing(!isEditing);
  });
}

let ws = null;
let suppressSend = false;
let debounceTimer = null;
let lastSent = '';
let expiresAt = null;
let countdownTimer = null;
let expiredHandled = false;

function setStatus(online) {
  $status.textContent = online ? 'online' : 'offline';
  $status.classList.toggle('online', online);
}

function toast(msg) {
  let el = document.querySelector('.toast');
  if (!el) {
    el = document.createElement('div');
    el.className = 'toast';
    document.body.appendChild(el);
  }
  el.textContent = msg;
  el.classList.add('show');
  clearTimeout(toast._t);
  toast._t = setTimeout(() => el.classList.remove('show'), 1200);
}

async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text);
    toast('Copiato');
  } catch {
    const ta = document.createElement('textarea');
    ta.value = text;
    document.body.appendChild(ta);
    ta.select();
    try { document.execCommand('copy'); toast('Copiato'); }
    finally { ta.remove(); }
  }
}

function renderItems(text) {
  $lines.innerHTML = '';
  const items = parseBlocks(text);
  if (items.length === 0) {
    const li = document.createElement('li');
    li.innerHTML = '<span class="txt empty">(vuoto)</span>';
    $lines.appendChild(li);
    return;
  }
  items.forEach((item, idx) => {
    const li = document.createElement('li');
    li.className = item.type === 'block' ? 'item block' : 'item line';

    const num = document.createElement('span');
    num.className = 'num';
    num.textContent = String(idx + 1);

    const txt = document.createElement(item.type === 'block' ? 'pre' : 'span');
    txt.className = 'txt';
    if (item.type === 'block') {
      txt.textContent = item.text;
    } else {
      txt.textContent = item.text === '' ? ' ' : item.text;
    }

    const isEmptyLine = item.type === 'line' && item.text === '';
    if (isEmptyLine) {
      li.append(num, txt);
      $lines.appendChild(li);
      return;
    }

    const actions = document.createElement('span');
    actions.className = 'actions';

    const copyBtn = document.createElement('button');
    copyBtn.className = 'ghost copy';
    copyBtn.type = 'button';
    copyBtn.textContent = item.type === 'block' ? 'Copia blocco' : 'Copia';
    copyBtn.addEventListener('click', () => copyText(item.text));

    const dlBtn = document.createElement('button');
    dlBtn.className = 'ghost copy';
    dlBtn.type = 'button';
    dlBtn.textContent = item.type === 'block' ? 'Scarica blocco' : 'Scarica';
    dlBtn.addEventListener('click', () => {
      const kind = item.type === 'block' ? 'blocco' : 'riga';
      downloadText(item.text, buildFilename(slug, kind, idx + 1));
    });

    actions.append(copyBtn, dlBtn);
    li.append(num, txt, actions);
    $lines.appendChild(li);
  });
}

function applyRemote(content) {
  if ($content.value === content) return;
  const sel = [$content.selectionStart, $content.selectionEnd];
  suppressSend = true;
  $content.value = content;
  try { $content.setSelectionRange(sel[0], sel[1]); } catch {}
  suppressSend = false;
  renderItems(content);
  lastSent = content;
}

function send(content) {
  if (!ws || ws.readyState !== WebSocket.OPEN) return;
  if (content === lastSent) return;
  ws.send(JSON.stringify({ content }));
  lastSent = content;
}

function handleExpired() {
  if (expiredHandled) return;
  expiredHandled = true;
  if (countdownTimer) { clearInterval(countdownTimer); countdownTimer = null; }
  if (ws) { try { ws.close(); } catch {} }
  $content.disabled = true;
  $copyAll.disabled = true;
  if ($countdown) {
    $countdown.textContent = 'Scaduta';
    $countdown.classList.add('expired');
  }
  if ($overlay) $overlay.hidden = false;
}

function startCountdown(iso) {
  expiresAt = iso || null;
  if (!expiresAt) {
    if ($countdown) $countdown.hidden = true;
    return;
  }
  if ($countdown) {
    $countdown.hidden = false;
    $countdown.classList.remove('expired');
  }
  const tick = () => {
    if (isExpired(expiresAt)) {
      handleExpired();
      return;
    }
    if ($countdown) {
      const remaining = msUntil(expiresAt);
      $countdown.textContent = `⏱ ${formatRemaining(remaining)}`;
      $countdown.classList.toggle('warning', remaining < 60_000);
    }
  };
  tick();
  if (countdownTimer) clearInterval(countdownTimer);
  countdownTimer = setInterval(tick, 1000);
}

function applySessionPayload(s) {
  if (typeof s.content === 'string') applyRemote(s.content);
  if (s.expires_at !== undefined) {
    if (s.expires_at) startCountdown(s.expires_at);
    else if ($countdown) $countdown.hidden = true;
  }
}

function connect() {
  if (expiredHandled) return;
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  ws = new WebSocket(`${proto}://${location.host}/ws/${encodeURIComponent(slug)}`);
  ws.addEventListener('open', () => setStatus(true));
  ws.addEventListener('close', () => {
    setStatus(false);
    if (expiredHandled) return;
    // If the session expired in the meantime, GET will return 410 → overlay.
    fetch(`/api/sessions/${encodeURIComponent(slug)}`).then((r) => {
      if (r.status === 410 || r.status === 404) {
        handleExpired();
        return;
      }
      setTimeout(connect, 1500);
    }).catch(() => setTimeout(connect, 1500));
  });
  ws.addEventListener('error', () => ws.close());
  ws.addEventListener('message', (ev) => {
    try {
      const msg = JSON.parse(ev.data);
      applySessionPayload(msg);
    } catch {}
  });
}

$content.addEventListener('input', () => {
  if (suppressSend || expiredHandled) return;
  renderItems($content.value);
  clearTimeout(debounceTimer);
  debounceTimer = setTimeout(() => send($content.value), 250);
});

$copyAll.addEventListener('click', () => copyText($content.value));
$copyLink.addEventListener('click', () => copyText(location.href));
if ($downloadAll) {
  $downloadAll.addEventListener('click', () => {
    downloadText($content.value, buildFilename(slug));
  });
}

fetch(`/api/sessions/${encodeURIComponent(slug)}`)
  .then((r) => {
    if (r.status === 410 || r.status === 404) {
      handleExpired();
      return null;
    }
    return r.ok ? r.json() : Promise.reject(new Error('load failed'));
  })
  .then((s) => { if (s) applySessionPayload(s); })
  .catch(() => {})
  .finally(() => { if (!expiredHandled) connect(); });
