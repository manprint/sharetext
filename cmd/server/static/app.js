import { parseBlocks } from './blocks.js';
import { appendLinkified } from './linkify.js';
import { formatRemaining, msUntil, isExpired } from './countdown.js';
import { buildFilename, downloadText } from './download.js';
import {
  parseFileMarker,
  buildFileMarker,
  buildFileMarkerRaw,
  formatBytes,
  insertMarkersAtPosition,
} from './files.js';
import { shouldApplyRemoteContent, shouldFlushPendingLocalChanges } from './sync.js';
import {
  importKey,
  encryptText,
  decryptText,
  encryptBytes,
  decryptBytes,
  encryptName,
  decryptName,
  isCiphertext,
  isEncryptedName,
} from './crypto.js';
import { downloadZip } from './bundle-client.js';
import {
  classifyLock,
  canEditNow,
  nextHeartbeatDelayMs,
  shouldAutoRelease,
  shouldRequestLock,
  parseIdleReleaseMs,
  LOCK_STATE_FREE,
  LOCK_STATE_MINE,
  LOCK_STATE_THEIRS,
} from './lock.js';
import {
  registerCommand,
  findSlashTokenAtCaret,
  filterCommands,
  dispatchCommand,
  formatTimestamp,
} from './commands.js';
import { initOfflineGuard, setOfflineBanner } from './offline-guard.js';

const slug = document.body.dataset.slug;
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
const $editPane = document.querySelector('.pane.edit');
const $lockBadge = document.getElementById('lock-badge');

const IDLE_RELEASE_MS = parseIdleReleaseMs(document.body.dataset.idleRelease, 3000);

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
let lastServerContent = '';
let hasPendingLocalChanges = false;
let awaitingInitialSnapshot = false;
let expiresAt = null;
let countdownTimer = null;
let expiredHandled = false;

let clientID = '';
let lockState = LOCK_STATE_FREE;
let lockHolder = '';
let lockExpiresAt = null;
let lastUserInputAt = 0;
let heartbeatTimer = null;
let idleTimer = null;
let pendingUploadOffset = null;
let isOffline = false;

// End-to-end encryption mode. 'pending' until the first snapshot lands, then
// fixed: 'plain' for legacy sessions, 'e2e' once we know the key matches the
// content (or for fresh empty sessions opened with a key), 'locked' when the
// content is encrypted but we don't have the key in the URL fragment.
let cryptoKey = null;
let sessionMode = 'pending';
const $decryptBanner = document.getElementById('decrypt-banner');

function parseKeyFromHash() {
  const h = location.hash || '';
  const m = h.match(/(?:^#|&)k=([A-Za-z0-9_-]+)/);
  return m ? m[1] : null;
}

function setDecryptBanner(text) {
  if (!$decryptBanner) return;
  if (!text) {
    $decryptBanner.hidden = true;
    $decryptBanner.textContent = '';
    return;
  }
  $decryptBanner.hidden = false;
  $decryptBanner.textContent = text;
}

function determineSessionMode(initialContent) {
  if (isCiphertext(initialContent)) {
    sessionMode = cryptoKey ? 'e2e' : 'locked';
  } else if (initialContent === '' && cryptoKey) {
    sessionMode = 'e2e';
  } else {
    sessionMode = 'plain';
  }
}

async function encryptOutgoing(text) {
  if (sessionMode === 'e2e' && cryptoKey) {
    return await encryptText(cryptoKey, text);
  }
  return text;
}

async function decryptIncoming(content) {
  // Mode is settled at initial snapshot but may need to flip when a peer
  // changes the session-wide encryption posture afterwards. The session was
  // empty when we joined? → we picked 'plain' if we had no key, 'e2e' if we
  // did. If a keyed peer then types, we now see ciphertext arrive in 'plain'
  // mode and need to react. The reverse (e2e session goes plaintext) is
  // possible only via a legacy/malicious client; in that case we let the
  // plaintext through so the user is not stuck with stale ciphertext.
  if (isCiphertext(content)) {
    if (cryptoKey) {
      if (sessionMode !== 'e2e') {
        sessionMode = 'e2e';
        applyModeUI();
      }
      try { return await decryptText(cryptoKey, content); }
      catch { return null; }
    }
    if (sessionMode !== 'locked') {
      sessionMode = 'locked';
      applyModeUI();
    }
    return null;
  }
  return content;
}

// Serialise async snapshot/edit applies so a faster second message doesn't
// overtake a slower decrypt of an earlier one.
let applyQueue = Promise.resolve();
function enqueueApply(fn) {
  applyQueue = applyQueue.then(fn).catch((err) => {
    console.error('apply failed', err);
  });
  return applyQueue;
}

function applyModeUI() {
  if (sessionMode === 'locked') {
    setDecryptBanner('Sessione cifrata: chiave mancante nell’URL. Apri il link completo per leggere.');
  } else if (sessionMode === 'pending') {
    setDecryptBanner('Decifratura…');
  } else {
    setDecryptBanner('');
  }
  updateLockUI();
}

function isLockedForEditing() {
  return sessionMode === 'locked' || sessionMode === 'pending';
}

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
    if (item.type === 'line' && item.text === '') {
      txt.textContent = ' ';
    } else {
      appendLinkified(txt, item.text);
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

  const cached = fileMetaCache.get(fm.id);
  // Prefer the plaintext name from the meta cache (populated by
  // loadFileMeta() or the upload response). When the marker carries an
  // encrypted name and the cache hasn't been filled yet, fall back to a
  // placeholder rather than printing the ciphertext.
  const displayName =
    (cached && cached.name) ||
    (isEncryptedName(fm.encodedName) ? '(allegato cifrato)' : fm.name);

  const name = document.createElement('span');
  name.className = 'file-name';
  name.textContent = displayName;

  const meta = document.createElement('span');
  meta.className = 'file-meta';
  meta.textContent = cached && Number.isFinite(cached.size) ? formatBytes(cached.size) : '';

  info.append(icon, name, meta);

  const actions = document.createElement('span');
  actions.className = 'actions';
  const dl = document.createElement('a');
  dl.className = 'ghost copy';
  dl.href = `/api/sessions/${encodeURIComponent(slug)}/files/${encodeURIComponent(fm.id)}`;
  dl.setAttribute('download', displayName);
  dl.textContent = 'Scarica';
  // E2E mode: the server only has ciphertext bytes; intercept the click to
  // fetch, decrypt, and surface a Blob URL with the plaintext filename.
  dl.addEventListener('click', async (ev) => {
    if (sessionMode !== 'e2e' || !cryptoKey) return; // legacy plaintext flow
    ev.preventDefault();
    try {
      const res = await fetch(dl.href);
      if (!res.ok) throw new Error(`HTTP ${res.status}`);
      const ct = new Uint8Array(await res.arrayBuffer());
      const pt = await decryptBytes(cryptoKey, ct);
      const meta2 = fileMetaCache.get(fm.id);
      const mime = (meta2 && meta2.mime) || 'application/octet-stream';
      const blob = new Blob([pt], { type: mime });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = (meta2 && meta2.name) || displayName;
      a.style.display = 'none';
      document.body.appendChild(a);
      a.click();
      setTimeout(() => { a.remove(); URL.revokeObjectURL(url); }, 0);
    } catch (err) {
      console.error('e2e download failed', err);
      toast('Decifratura fallita');
    }
  });
  actions.append(dl);

  li.append(num, info, actions);
  $lines.appendChild(li);
}

async function applyRemote(rawContent, { initialSnapshot = false } = {}) {
  const plain = await decryptIncoming(rawContent);
  if (plain === null) {
    // Locked mode or decrypt failure: keep the editor empty so the user never
    // sees raw ciphertext, and don't disturb pending local state (there
    // shouldn't be any since we forced readOnly).
    return false;
  }
  if (!shouldApplyRemoteContent({
    currentContent: $content.value,
    incomingContent: plain,
    hasPendingLocalChanges,
    initialSnapshot,
  })) {
    lastServerContent = plain;
    return false;
  }
  const sel = [$content.selectionStart, $content.selectionEnd];
  suppressSend = true;
  $content.value = plain;
  try { $content.setSelectionRange(sel[0], sel[1]); } catch {}
  suppressSend = false;
  renderItems(plain);
  lastSent = plain;
  lastServerContent = plain;
  return true;
}

async function send(content) {
  if (!ws || ws.readyState !== WebSocket.OPEN) return false;
  if (content === lastSent) {
    hasPendingLocalChanges = false;
    return true;
  }
  let wire;
  try {
    wire = await encryptOutgoing(content);
  } catch (err) {
    console.error('encrypt failed, not transmitting', err);
    return false;
  }
  try {
    ws.send(JSON.stringify({ type: 'edit', content: wire }));
  } catch {
    return false;
  }
  lastSent = content;
  hasPendingLocalChanges = false;
  return true;
}

function wsSend(payload) {
  if (!ws || ws.readyState !== WebSocket.OPEN) return false;
  try {
    ws.send(JSON.stringify(payload));
    return true;
  } catch {
    return false;
  }
}

function clearHeartbeat() {
  if (heartbeatTimer) { clearTimeout(heartbeatTimer); heartbeatTimer = null; }
}

function scheduleHeartbeat() {
  clearHeartbeat();
  const delay = nextHeartbeatDelayMs({
    state: lockState,
    expiresAt: lockExpiresAt,
    nowMs: Date.now(),
  });
  if (delay == null) return;
  heartbeatTimer = setTimeout(() => {
    if (lockState === LOCK_STATE_MINE) {
      wsSend({ type: 'lock_heartbeat' });
      scheduleHeartbeat();
    }
  }, delay);
}

function clearIdleTimer() {
  if (idleTimer) { clearTimeout(idleTimer); idleTimer = null; }
}

function scheduleIdleRelease() {
  clearIdleTimer();
  if (lockState !== LOCK_STATE_MINE) return;
  idleTimer = setTimeout(() => {
    if (shouldAutoRelease({
      state: lockState,
      lastInputAt: lastUserInputAt,
      nowMs: Date.now(),
      idleMs: IDLE_RELEASE_MS,
    })) {
      wsSend({ type: 'lock_release' });
    } else {
      scheduleIdleRelease();
    }
  }, IDLE_RELEASE_MS);
}

function updateLockUI() {
  const locked = lockState === LOCK_STATE_THEIRS;
  const blocked = locked || isOffline || isLockedForEditing();
  if ($editPane) $editPane.classList.toggle('locked-by-other', locked);
  if ($content) {
    $content.readOnly = blocked;
    $content.setAttribute('aria-readonly', String(blocked));
  }
  if ($uploadBtn) $uploadBtn.disabled = blocked;
  if ($lockBadge) {
    $lockBadge.classList.remove('mine', 'theirs');
    if (lockState === LOCK_STATE_MINE) {
      $lockBadge.hidden = false;
      $lockBadge.classList.add('mine');
      $lockBadge.textContent = '✎ stai modificando';
    } else if (lockState === LOCK_STATE_THEIRS) {
      $lockBadge.hidden = false;
      $lockBadge.classList.add('theirs');
      $lockBadge.textContent = '🔒 in modifica';
    } else {
      $lockBadge.hidden = true;
      $lockBadge.textContent = '';
    }
  }
}

function applyLockSnapshot(snap) {
  const previous = lockState;
  lockState = classifyLock(snap, clientID);
  lockHolder = (snap && snap.holder) || '';
  lockExpiresAt = (snap && snap.expires_at) || null;
  if (lockState === LOCK_STATE_MINE) {
    scheduleHeartbeat();
    scheduleIdleRelease();
  } else {
    clearHeartbeat();
    clearIdleTimer();
  }
  if (previous !== LOCK_STATE_THEIRS && lockState === LOCK_STATE_THEIRS && hasPendingLocalChanges) {
    // We lost the race: drop any pending local edit and restore the last known
    // server content so we don't display ghost text the peer never received.
    revertToServerContent();
  }
  updateLockUI();
}

function revertToServerContent() {
  clearTimeout(debounceTimer);
  hasPendingLocalChanges = false;
  if ($content.value !== lastServerContent) {
    suppressSend = true;
    $content.value = lastServerContent;
    suppressSend = false;
    renderItems(lastServerContent);
    lastSent = lastServerContent;
    toast(isOffline
      ? 'Offline — sola lettura'
      : 'Modifica annullata: editor bloccato da un altro utente');
  }
}

function flushPendingLocalChanges() {
  if (!hasPendingLocalChanges) return;
  clearTimeout(debounceTimer);
  send($content.value);
}

function handleExpired() {
  if (expiredHandled) return;
  expiredHandled = true;
  clearHeartbeat();
  clearIdleTimer();
  if (countdownTimer) { clearInterval(countdownTimer); countdownTimer = null; }
  if (ws) { try { ws.close(); } catch {} }
  $content.disabled = true;
  $copyAll.disabled = true;
  if ($lockBadge) { $lockBadge.hidden = true; $lockBadge.textContent = ''; }
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

async function applySessionPayload(s, { initialSnapshot = false } = {}) {
  if (initialSnapshot && typeof s.client_id === 'string' && s.client_id) {
    clientID = s.client_id;
  }
  if (typeof s.content === 'string') await applyRemote(s.content, { initialSnapshot });
  if (s.expires_at !== undefined) {
    if (s.expires_at) startCountdown(s.expires_at);
    else if ($countdown) $countdown.hidden = true;
  }
  if (s.lock !== undefined) {
    applyLockSnapshot(s.lock);
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
    let msg;
    try { msg = JSON.parse(ev.data); } catch { return; }
    if (msg && msg.type === 'lock') {
      applyLockSnapshot(msg.lock || null);
      return;
    }
    if (msg && msg.type === 'lock_denied') {
      applyLockSnapshot(msg.lock || null);
      return;
    }
    const initialSnapshot = awaitingInitialSnapshot;
    awaitingInitialSnapshot = false;
    // Serialise async applies so a faster decrypt can't overtake a slower one
    // that arrived before it.
    enqueueApply(async () => {
      // Server may send a snapshot whose content is encrypted before we have
      // finished settling the session mode (e.g. WS hello races the initial
      // GET on first load). Settle it now if still pending.
      if (initialSnapshot && sessionMode === 'pending' && typeof msg.content === 'string') {
        determineSessionMode(msg.content);
        applyModeUI();
      }
      await applySessionPayload(msg, { initialSnapshot });
      if (shouldFlushPendingLocalChanges({ initialSnapshot, hasPendingLocalChanges })) {
        flushPendingLocalChanges();
      }
    });
  });
}

function applyProgrammaticEdit(nextValue, caret) {
  suppressSend = true;
  $content.value = nextValue;
  try {
    const c = Math.max(0, Math.min(caret, nextValue.length));
    $content.setSelectionRange(c, c);
  } catch {}
  suppressSend = false;
  hasPendingLocalChanges = true;
  lastUserInputAt = Date.now();
  renderItems(nextValue);
  if (shouldRequestLock(lockState)) {
    wsSend({ type: 'lock_acquire' });
  }
  scheduleIdleRelease();
  clearTimeout(debounceTimer);
  debounceTimer = setTimeout(() => send($content.value), 250);
}

registerCommand('timestamp', (ctx) => {
  const stamp = formatTimestamp();
  const before = ctx.text.slice(0, ctx.tokenStart);
  const after = ctx.text.slice(ctx.tokenEnd);
  applyProgrammaticEdit(before + stamp + after, ctx.tokenStart + stamp.length);
});

registerCommand('upload', (ctx) => {
  if (!$fileInput) return;
  // Strip the `/upload` token. Markers will be inserted at the same position
  // by handleUploads() once files are picked.
  const before = ctx.text.slice(0, ctx.tokenStart);
  const after = ctx.text.slice(ctx.tokenEnd);
  applyProgrammaticEdit(before + after, ctx.tokenStart);
  pendingUploadOffset = ctx.tokenStart;
  try { $fileInput.click(); } catch {}
});

// ───────────────────────────── command palette ─────────────────────────────

const $cmdMenu = document.createElement('div');
$cmdMenu.className = 'cmd-menu';
$cmdMenu.setAttribute('role', 'listbox');
$cmdMenu.hidden = true;
document.body.appendChild($cmdMenu);

let menuItems = [];
let menuIndex = 0;
let menuToken = null;

function caretCoords(textarea, position) {
  // Mirror-div technique: clone textarea styling into an invisible div, copy
  // text up to `position`, append a sentinel span and read its offsetLeft/Top
  // → pixel coords of the caret in viewport space.
  const div = document.createElement('div');
  const style = window.getComputedStyle(textarea);
  const props = [
    'boxSizing', 'width', 'height', 'overflowX', 'overflowY',
    'borderTopWidth', 'borderRightWidth', 'borderBottomWidth', 'borderLeftWidth',
    'paddingTop', 'paddingRight', 'paddingBottom', 'paddingLeft',
    'fontStyle', 'fontVariant', 'fontWeight', 'fontStretch', 'fontSize',
    'lineHeight', 'fontFamily', 'textAlign', 'textTransform', 'textIndent',
    'letterSpacing', 'wordSpacing', 'tabSize',
  ];
  props.forEach((p) => { div.style[p] = style[p]; });
  div.style.position = 'absolute';
  div.style.visibility = 'hidden';
  div.style.whiteSpace = 'pre-wrap';
  div.style.wordWrap = 'break-word';
  div.style.top = '0';
  div.style.left = '-9999px';
  div.textContent = textarea.value.substring(0, position);
  const span = document.createElement('span');
  span.textContent = textarea.value.substring(position) || '.';
  div.appendChild(span);
  document.body.appendChild(div);
  const rect = textarea.getBoundingClientRect();
  const x = rect.left + span.offsetLeft - textarea.scrollLeft;
  const y = rect.top + span.offsetTop - textarea.scrollTop;
  const lineHeight = parseFloat(style.lineHeight) || parseFloat(style.fontSize) * 1.4;
  document.body.removeChild(div);
  return { x, y, height: lineHeight };
}

function renderMenu() {
  $cmdMenu.innerHTML = '';
  menuItems.forEach((name, i) => {
    const it = document.createElement('div');
    it.className = 'cmd-item' + (i === menuIndex ? ' active' : '');
    it.setAttribute('role', 'option');
    it.textContent = '/' + name;
    it.addEventListener('mousedown', (e) => {
      e.preventDefault(); // keep focus in textarea
      menuIndex = i;
      executeMenuSelection();
    });
    $cmdMenu.appendChild(it);
  });
}

function positionMenu(slashAt) {
  const coords = caretCoords($content, slashAt);
  const pad = 4;
  // Default placement: just below the caret line.
  let top = coords.y + coords.height + pad;
  let left = coords.x;
  // Render once so we can measure, then constrain to viewport.
  $cmdMenu.style.top = `${top}px`;
  $cmdMenu.style.left = `${left}px`;
  $cmdMenu.hidden = false;
  const mb = $cmdMenu.getBoundingClientRect();
  if (mb.right > window.innerWidth - pad) {
    left = Math.max(pad, window.innerWidth - mb.width - pad);
  }
  if (mb.bottom > window.innerHeight - pad) {
    top = Math.max(pad, coords.y - mb.height - pad);
  }
  $cmdMenu.style.top = `${top}px`;
  $cmdMenu.style.left = `${left}px`;
}

function showMenu(token) {
  const items = filterCommands(token.name);
  if (items.length === 0) { hideMenu(); return; }
  // Reset index when the matching set changes.
  const prev = menuToken;
  menuToken = token;
  menuItems = items;
  if (!prev || prev.slashAt !== token.slashAt) menuIndex = 0;
  else if (menuIndex >= items.length) menuIndex = 0;
  renderMenu();
  positionMenu(token.slashAt);
}

function hideMenu() {
  if (!$cmdMenu.hidden) {
    $cmdMenu.hidden = true;
    $cmdMenu.innerHTML = '';
  }
  menuToken = null;
  menuItems = [];
  menuIndex = 0;
}

function menuVisible() {
  return !$cmdMenu.hidden && menuItems.length > 0 && menuToken != null;
}

function updateCommandMenu() {
  if (expiredHandled || !canEditNow(lockState)) { hideMenu(); return; }
  const token = findSlashTokenAtCaret($content.value, $content.selectionStart);
  if (!token) { hideMenu(); return; }
  showMenu(token);
}

function executeMenuSelection() {
  if (!menuVisible()) return;
  const cmdName = menuItems[menuIndex];
  const token = menuToken;
  hideMenu();
  Promise.resolve(dispatchCommand({
    name: cmdName,
    args: '',
    text: $content.value,
    caret: $content.selectionStart,
    tokenStart: token.slashAt,
    tokenEnd: token.nameEnd,
  })).catch((err) => {
    console.error('command failed', err);
    toast(`Comando /${cmdName} fallito`);
  });
}

$content.addEventListener('keydown', (ev) => {
  if (expiredHandled || !menuVisible()) return;
  if (ev.isComposing) return;
  switch (ev.key) {
    case 'ArrowDown':
      ev.preventDefault();
      menuIndex = (menuIndex + 1) % menuItems.length;
      renderMenu();
      return;
    case 'ArrowUp':
      ev.preventDefault();
      menuIndex = (menuIndex - 1 + menuItems.length) % menuItems.length;
      renderMenu();
      return;
    case 'Enter':
    case 'Tab':
      ev.preventDefault();
      executeMenuSelection();
      return;
    case 'Escape':
      ev.preventDefault();
      hideMenu();
      return;
  }
});

$content.addEventListener('blur', () => {
  // Delay so a mousedown on the menu can still trigger executeMenuSelection.
  setTimeout(() => { if (document.activeElement !== $content) hideMenu(); }, 100);
});

document.addEventListener('selectionchange', () => {
  if (document.activeElement === $content) updateCommandMenu();
});

window.addEventListener('resize', () => { if (menuVisible()) positionMenu(menuToken.slashAt); });
window.addEventListener('scroll', () => { if (menuVisible()) positionMenu(menuToken.slashAt); }, true);

$content.addEventListener('beforeinput', (ev) => {
  if (expiredHandled) return;
  if (sessionMode === 'locked') {
    ev.preventDefault();
    toast('Sessione cifrata: chiave mancante');
    return;
  }
  if (sessionMode === 'pending') {
    ev.preventDefault();
    return;
  }
  if (isOffline) {
    ev.preventDefault();
    toast('Offline — sola lettura');
    return;
  }
  if (!canEditNow(lockState)) {
    ev.preventDefault();
    toast('Editor bloccato da un altro utente');
  }
});

$content.addEventListener('input', () => {
  if (suppressSend || expiredHandled) return;
  if (isLockedForEditing() || isOffline || !canEditNow(lockState)) {
    // beforeinput should have prevented this, but if a browser doesn't
    // honour it (rare), restore server content as a safety net.
    revertToServerContent();
    return;
  }
  hasPendingLocalChanges = true;
  lastUserInputAt = Date.now();
  renderItems($content.value);
  if (shouldRequestLock(lockState)) {
    wsSend({ type: 'lock_acquire' });
  }
  scheduleIdleRelease();
  clearTimeout(debounceTimer);
  debounceTimer = setTimeout(() => send($content.value), 250);
  updateCommandMenu();
});

window.addEventListener('beforeunload', () => {
  if (lockState === LOCK_STATE_MINE) {
    wsSend({ type: 'lock_release' });
  }
});

$copyAll.addEventListener('click', () => copyText($content.value));
$copyLink.addEventListener('click', () => copyText(location.href));
if ($downloadAll) {
  $downloadAll.addEventListener('click', async () => {
    if (sessionMode !== 'e2e' || !cryptoKey) {
      // Legacy plaintext flow: server-side zip is usable as-is.
      window.location.href = `/api/sessions/${encodeURIComponent(slug)}/bundle`;
      return;
    }
    // E2E: fetch each file, decrypt, assemble plaintext zip in-browser.
    try {
      $downloadAll.disabled = true;
      const entries = [];
      // Session text decrypted from the editor buffer (already plaintext).
      entries.push({ name: `${slug}.txt`, data: new TextEncoder().encode($content.value) });
      for (const [id, meta] of fileMetaCache.entries()) {
        const r = await fetch(`/api/sessions/${encodeURIComponent(slug)}/files/${encodeURIComponent(id)}`);
        if (!r.ok) throw new Error(`fetch ${id}: ${r.status}`);
        const ct = new Uint8Array(await r.arrayBuffer());
        const pt = await decryptBytes(cryptoKey, ct);
        const safeName = (meta.name || `file-${id}.bin`).replace(/[\\/]/g, '_');
        entries.push({ name: `files/${safeName}`, data: pt });
      }
      downloadZip(`${slug}.zip`, entries);
    } catch (err) {
      console.error('e2e bundle failed', err);
      toast('Pacchetto fallito');
    } finally {
      $downloadAll.disabled = false;
    }
  });
}

function clientHeaders(extra = {}) {
  const h = { ...extra };
  if (clientID) h['X-Client-ID'] = clientID;
  return h;
}

async function uploadOne(file) {
  const e2e = sessionMode === 'e2e' && cryptoKey;
  let blob = file;
  let serverName = file.name;
  if (e2e) {
    const raw = new Uint8Array(await file.arrayBuffer());
    const enc = await encryptBytes(cryptoKey, raw);
    blob = new Blob([enc], { type: 'application/octet-stream' });
    serverName = await encryptName(cryptoKey, file.name);
  }
  const fd = new FormData();
  fd.append('file', blob, serverName);
  const res = await fetch(`/api/sessions/${encodeURIComponent(slug)}/files`, {
    method: 'POST',
    body: fd,
    headers: clientHeaders(),
  });
  if (res.status === 409) throw new Error(`Editor bloccato da un altro utente: ${file.name}`);
  if (res.status === 413) throw new Error(`File troppo grande: ${file.name}`);
  if (!res.ok) throw new Error(`Upload fallito (${res.status}) per ${file.name}`);
  const data = await res.json();
  // Server records ciphertext bytes and (in E2E) the encrypted filename. The
  // local meta cache however stores PLAINTEXT name/size so the UI can render
  // human-friendly labels and accurate sizes; the rest of the app never sees
  // the on-wire encrypted values.
  const plainSize = e2e ? file.size : data.size;
  fileMetaCache.set(data.id, {
    name: file.name,
    size: plainSize,
    mime: file.type || data.mime,
    encryptedName: e2e ? data.filename : null,
  });
  // Build a marker the editor will later parse: in E2E use the encrypted
  // filename verbatim so the marker's encodedName carries the iv.ct payload.
  const marker = e2e ? buildFileMarkerRaw(data.id, data.filename) : data.marker;
  return { ...data, marker, plainName: file.name, plainSize };
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


async function pushContent(text) {
  // Persist via PUT so the change survives even when the WebSocket is not yet
  // open (e.g. right after page load on mobile). The server-side hub then
  // broadcasts to all peers, including this client's WS.
  hasPendingLocalChanges = true;
  lastSent = text;
  let wire;
  try {
    wire = await encryptOutgoing(text);
  } catch (err) {
    console.error('encrypt failed, dropping PUT', err);
    return;
  }
  try {
    const res = await fetch(`/api/sessions/${encodeURIComponent(slug)}`, {
      method: 'PUT',
      headers: clientHeaders({ 'Content-Type': 'application/json' }),
      body: JSON.stringify({ content: wire }),
    });
    if (res.status === 409) {
      throw new Error('Editor bloccato da un altro utente');
    }
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
  if (isOffline) {
    toast('Offline — sola lettura');
    return;
  }
  if (!canEditNow(lockState)) {
    toast('Upload bloccato: editor in uso da un altro utente');
    return;
  }
  // Pre-acquire the lock so peers see the busy state before the first byte lands.
  if (shouldRequestLock(lockState)) {
    wsSend({ type: 'lock_acquire' });
  }
  lastUserInputAt = Date.now();
  scheduleIdleRelease();
  const markers = [];
  const errors = [];
  for (const f of files) {
    try {
      const up = await uploadOne(f);
      markers.push(up.marker);
      toast(`Caricato ${up.plainName || up.filename}`);
    } catch (e) {
      errors.push(e.message);
      console.error('upload failed', e);
    }
  }
  if (markers.length > 0) {
    const value = $content.value;
    const next = atOffset == null
      ? appendAtEnd(value, markers)
      : insertMarkersAtPosition(value, atOffset, markers);
    $content.value = next;
    renderItems(next);
    await pushContent(next);
  }
  if (errors.length > 0) toast(errors.join(' · '));
}

if ($uploadBtn && $fileInput) {
  $uploadBtn.addEventListener('click', () => {
    // Capture the textarea caret BEFORE opening the dialog, so the marker is
    // inserted where the user was editing rather than appended at the end.
    // selectionStart survives blur in modern browsers; fall back to end-of-text
    // only when the textarea has never been focused (value is empty / null).
    if (pendingUploadOffset == null) {
      const sel = typeof $content.selectionStart === 'number' ? $content.selectionStart : null;
      pendingUploadOffset = sel == null ? $content.value.length : sel;
    }
    $fileInput.click();
  });
  $fileInput.addEventListener('change', () => {
    const files = Array.from($fileInput.files || []);
    $fileInput.value = '';
    const offset = pendingUploadOffset;
    pendingUploadOffset = null;
    if (files.length === 0) return;
    handleUploads(files, offset);
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
  if (isOffline) {
    toast('Offline — sola lettura');
    return;
  }
  if (!canEditNow(lockState)) {
    toast('Drag&drop bloccato: editor in uso da un altro utente');
    return;
  }
  const offset = caretOffsetFromEvent(ev);
  // Drag&drop keeps the "insert at start of the drop line" semantics; the
  // command-driven /upload path passes a precise inline position instead.
  const lineStart = startOfLine($content.value, offset);
  handleUploads(Array.from(ev.dataTransfer.files), lineStart);
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
    const e2e = sessionMode === 'e2e' && cryptoKey;
    for (const f of data.files || []) {
      let plainName = f.filename;
      let encName = null;
      if (e2e && isEncryptedName(f.filename)) {
        encName = f.filename;
        try {
          plainName = await decryptName(cryptoKey, f.filename);
        } catch {
          plainName = '(allegato cifrato)';
        }
      }
      fileMetaCache.set(f.id, {
        name: plainName,
        size: f.size,
        mime: f.mime,
        encryptedName: encName,
      });
    }
    renderItems($content.value);
  } catch {}
}

// Bootstrap: import the URL-fragment key (if any) BEFORE the initial fetch
// finishes, so the session-mode decision in `applySessionPayload` is made
// against a fully-settled `cryptoKey`. Decifratura banner stays up until
// the snapshot has been applied.
(async () => {
  applyModeUI(); // show "Decifratura…" while we work

  const keyB64 = parseKeyFromHash();
  if (keyB64) {
    try { cryptoKey = await importKey(keyB64); }
    catch (err) {
      console.warn('failed to import key from URL fragment', err);
      cryptoKey = null;
    }
  }

  let snapshot = null;
  try {
    const r = await fetch(`/api/sessions/${encodeURIComponent(slug)}`);
    if (r.status === 410 || r.status === 404) {
      handleExpired();
      return;
    }
    if (!r.ok) throw new Error('load failed');
    snapshot = await r.json();
  } catch (err) {
    console.error('initial fetch failed', err);
  }

  if (snapshot) {
    determineSessionMode(typeof snapshot.content === 'string' ? snapshot.content : '');
    applyModeUI();
    await applySessionPayload(snapshot, { initialSnapshot: true });
  } else {
    // Couldn't reach the server. Stay in pending mode until WS settles it.
    applyModeUI();
  }

  if (expiredHandled) return;
  await loadFileMeta();
  connect();
})();

initOfflineGuard({
  onOnline: () => {
    isOffline = false;
    setOfflineBanner(false);
    updateLockUI();
    // The WS reconnect loop already nudges itself every ~1.5s after close,
    // but if it has gone fully dormant (no close pending) kick it once.
    if (!ws || ws.readyState === WebSocket.CLOSED) {
      try { connect(); } catch {}
    }
  },
  onOffline: () => {
    isOffline = true;
    setOfflineBanner(true, 'Offline — sola lettura');
    clearTimeout(debounceTimer);
    hasPendingLocalChanges = false;
    updateLockUI();
  },
});

if ('serviceWorker' in navigator) {
  window.addEventListener('load', () => {
    navigator.serviceWorker.register('/sw.js').catch(() => {});
  });
}
