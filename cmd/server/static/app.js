import { parseBlocks } from './blocks.js';
import { formatRemaining, msUntil, isExpired } from './countdown.js';
import { buildFilename, downloadText } from './download.js';
import { parseFileMarker, buildFileMarker, formatBytes } from './files.js';
import { shouldApplyRemoteContent, shouldFlushPendingLocalChanges } from './sync.js';

const slug = window.__SLUG__;
const $content = document.getElementById('content');
const $lines = document.getElementById('lines');
const $status = document.getElementById('status');
const $copyAll = document.getElementById('copy-all');
const $copyLink = document.getElementById('copy-link');
const $downloadAll = document.getElementById('download-all');
const $uploadBtn = document.getElementById('upload-btn');
const $fileInput = document.getElementById('file-input');

const fileMetaCache = new Map(); // id -> {name, size, mime}
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
let hasPendingLocalChanges = false;
let awaitingInitialSnapshot = false;
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
    const num = document.createElement('span');
    num.className = 'num';
    num.textContent = String(idx + 1);

    // file marker line → dedicated row
    if (item.type === 'line') {
      const fm = parseFileMarker(item.text);
      if (fm) {
        renderFileRow(li, num, fm);
        return;
      }
    }

    li.className = item.type === 'block' ? 'item block' : 'item line';
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

function renderFileRow(li, num, fm) {
  li.className = 'item file';
  const info = document.createElement('span');
  info.className = 'txt file-info';

  const icon = document.createElement('span');
  icon.className = 'file-icon';
  icon.textContent = '📎';

  const name = document.createElement('span');
  name.className = 'file-name';
  name.textContent = fm.name;

  const meta = document.createElement('span');
  meta.className = 'file-meta';
  const cached = fileMetaCache.get(fm.id);
  meta.textContent = cached && Number.isFinite(cached.size) ? formatBytes(cached.size) : '';

  info.append(icon, name, meta);

  const actions = document.createElement('span');
  actions.className = 'actions';
  const dl = document.createElement('a');
  dl.className = 'ghost copy';
  dl.href = `/api/sessions/${encodeURIComponent(slug)}/files/${encodeURIComponent(fm.id)}`;
  dl.setAttribute('download', fm.name);
  dl.textContent = 'Scarica';
  actions.append(dl);

  li.append(num, info, actions);
  $lines.appendChild(li);
}

function applyRemote(content, { initialSnapshot = false } = {}) {
  if (!shouldApplyRemoteContent({
    currentContent: $content.value,
    incomingContent: content,
    hasPendingLocalChanges,
    initialSnapshot,
  })) {
    return false;
  }
  const sel = [$content.selectionStart, $content.selectionEnd];
  suppressSend = true;
  $content.value = content;
  try { $content.setSelectionRange(sel[0], sel[1]); } catch {}
  suppressSend = false;
  renderItems(content);
  lastSent = content;
  return true;
}

function send(content) {
  if (!ws || ws.readyState !== WebSocket.OPEN) return false;
  if (content === lastSent) {
    hasPendingLocalChanges = false;
    return true;
  }
  try {
    ws.send(JSON.stringify({ content }));
  } catch {
    return false;
  }
  lastSent = content;
  hasPendingLocalChanges = false;
  return true;
}

function flushPendingLocalChanges() {
  if (!hasPendingLocalChanges) return;
  clearTimeout(debounceTimer);
  send($content.value);
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

function applySessionPayload(s, { initialSnapshot = false } = {}) {
  if (typeof s.content === 'string') applyRemote(s.content, { initialSnapshot });
  if (s.expires_at !== undefined) {
    if (s.expires_at) startCountdown(s.expires_at);
    else if ($countdown) $countdown.hidden = true;
  }
}

function connect() {
  if (expiredHandled) return;
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  ws = new WebSocket(`${proto}://${location.host}/ws/${encodeURIComponent(slug)}`);
  ws.addEventListener('open', () => {
    awaitingInitialSnapshot = true;
    setStatus(true);
  });
  ws.addEventListener('close', () => {
    awaitingInitialSnapshot = false;
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
      const initialSnapshot = awaitingInitialSnapshot;
      awaitingInitialSnapshot = false;
      applySessionPayload(msg, { initialSnapshot });
      if (shouldFlushPendingLocalChanges({ initialSnapshot, hasPendingLocalChanges })) {
        flushPendingLocalChanges();
      }
    } catch {}
  });
}

$content.addEventListener('input', () => {
  if (suppressSend || expiredHandled) return;
  hasPendingLocalChanges = true;
  renderItems($content.value);
  clearTimeout(debounceTimer);
  debounceTimer = setTimeout(() => send($content.value), 250);
});

$copyAll.addEventListener('click', () => copyText($content.value));
$copyLink.addEventListener('click', () => copyText(location.href));
if ($downloadAll) {
  $downloadAll.addEventListener('click', () => {
    // Server-side zip bundle (text + all attachments).
    window.location.href = `/api/sessions/${encodeURIComponent(slug)}/bundle`;
  });
}

async function uploadOne(file) {
  const fd = new FormData();
  fd.append('file', file, file.name);
  const res = await fetch(`/api/sessions/${encodeURIComponent(slug)}/files`, {
    method: 'POST',
    body: fd,
  });
  if (res.status === 413) throw new Error(`File troppo grande: ${file.name}`);
  if (!res.ok) throw new Error(`Upload fallito (${res.status}) per ${file.name}`);
  const data = await res.json();
  fileMetaCache.set(data.id, { name: data.filename, size: data.size, mime: data.mime });
  return data; // { id, filename, size, mime, marker, url }
}

function caretOffsetFromEvent(ev) {
  const value = $content.value;
  let offset = null;
  if (typeof document.caretPositionFromPoint === 'function') {
    const pos = document.caretPositionFromPoint(ev.clientX, ev.clientY);
    if (pos && pos.offsetNode === $content) offset = pos.offset;
  }
  if (offset == null && typeof document.caretRangeFromPoint === 'function') {
    const range = document.caretRangeFromPoint(ev.clientX, ev.clientY);
    if (range) offset = range.startOffset;
  }
  if (offset == null) offset = $content.selectionStart || value.length;
  return Math.min(Math.max(0, offset), value.length);
}

function startOfLine(text, offset) {
  if (offset <= 0) return 0;
  const nl = text.lastIndexOf('\n', offset - 1);
  return nl === -1 ? 0 : nl + 1;
}

function insertLines(text, at, lines) {
  let block = lines.join('\n');
  // Ensure trailing newline so the marker becomes its own line.
  if (!block.endsWith('\n')) block += '\n';
  return text.slice(0, at) + block + text.slice(at);
}

async function pushContent(text) {
  // Persist via PUT so the change survives even when the WebSocket is not yet
  // open (e.g. right after page load on mobile). The server-side hub then
  // broadcasts to all peers, including this client's WS.
  hasPendingLocalChanges = true;
  lastSent = text;
  try {
    const res = await fetch(`/api/sessions/${encodeURIComponent(slug)}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ content: text }),
    });
    if (!res.ok) throw new Error(`PUT ${res.status}`);
    hasPendingLocalChanges = false;
  } catch (e) {
    // Fallback: try WS path (no-op if WS not open yet).
    send(text);
  }
}

function appendAtEnd(text, lines) {
  let prefix = text;
  if (prefix && !prefix.endsWith('\n')) prefix += '\n';
  let block = lines.join('\n');
  if (!block.endsWith('\n')) block += '\n';
  return prefix + block;
}

async function handleUploads(files, atOffset) {
  if (!files || files.length === 0) return;
  const markers = [];
  const errors = [];
  for (const f of files) {
    try {
      const up = await uploadOne(f);
      markers.push(up.marker);
      toast(`Caricato ${up.filename}`);
    } catch (e) {
      errors.push(e.message);
      console.error('upload failed', e);
    }
  }
  if (markers.length > 0) {
    const value = $content.value;
    const next = atOffset == null
      ? appendAtEnd(value, markers)
      : insertLines(value, startOfLine(value, atOffset), markers);
    $content.value = next;
    renderItems(next);
    await pushContent(next);
  }
  if (errors.length > 0) toast(errors.join(' · '));
}

if ($uploadBtn && $fileInput) {
  $uploadBtn.addEventListener('click', () => $fileInput.click());
  $fileInput.addEventListener('change', () => {
    const files = Array.from($fileInput.files || []);
    $fileInput.value = '';
    if (files.length === 0) return;
    // Append after the last existing line.
    handleUploads(files, null);
  });
}

let dragDepth = 0;
function onDragEnter(ev) {
  if (!ev.dataTransfer || !Array.from(ev.dataTransfer.types || []).includes('Files')) return;
  ev.preventDefault();
  dragDepth++;
  $content.classList.add('drag-over');
}
function onDragOver(ev) {
  if (!ev.dataTransfer || !Array.from(ev.dataTransfer.types || []).includes('Files')) return;
  ev.preventDefault();
  ev.dataTransfer.dropEffect = 'copy';
}
function onDragLeave() {
  dragDepth = Math.max(0, dragDepth - 1);
  if (dragDepth === 0) $content.classList.remove('drag-over');
}
function onDrop(ev) {
  if (!ev.dataTransfer || !ev.dataTransfer.files || ev.dataTransfer.files.length === 0) return;
  ev.preventDefault();
  dragDepth = 0;
  $content.classList.remove('drag-over');
  const offset = caretOffsetFromEvent(ev);
  handleUploads(Array.from(ev.dataTransfer.files), offset);
}

$content.addEventListener('dragenter', onDragEnter);
$content.addEventListener('dragover', onDragOver);
$content.addEventListener('dragleave', onDragLeave);
$content.addEventListener('drop', onDrop);
// Block whole-page drop so a missed target doesn't open the file in a tab.
window.addEventListener('dragover', (e) => { if (e.dataTransfer && Array.from(e.dataTransfer.types || []).includes('Files')) e.preventDefault(); });
window.addEventListener('drop', (e) => { if (e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files.length) e.preventDefault(); });

async function loadFileMeta() {
  try {
    const res = await fetch(`/api/sessions/${encodeURIComponent(slug)}/files`);
    if (!res.ok) return;
    const data = await res.json();
    (data.files || []).forEach((f) => {
      fileMetaCache.set(f.id, { name: f.filename, size: f.size, mime: f.mime });
    });
    renderItems($content.value);
  } catch {}
}

fetch(`/api/sessions/${encodeURIComponent(slug)}`)
  .then((r) => {
    if (r.status === 410 || r.status === 404) {
      handleExpired();
      return null;
    }
    return r.ok ? r.json() : Promise.reject(new Error('load failed'));
  })
  .then((s) => { if (s) applySessionPayload(s, { initialSnapshot: true }); })
  .catch(() => {})
  .finally(() => {
    if (expiredHandled) return;
    loadFileMeta();
    connect();
  });
