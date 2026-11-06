import { sessionPath } from './share-link.js';

const PIN_PREFIX = 'sharetext.pin:';
// Where a remembered key came from. Only a key that arrived in a link may go
// back into a link: a password-derived key in the address bar (and so in
// "Copia link") would open the session without the password.
export const KEY_FROM_LINK = 'link';
export const KEY_FROM_PASSWORD = 'password';
const SLUG_RE = /^[A-Za-z0-9_-]+$/;
const KEY_RE = /^[A-Za-z0-9_-]+$/;

function isValidSlug(slug) {
  return typeof slug === 'string' && SLUG_RE.test(slug);
}

function normaliseKey(key) {
  if (typeof key !== 'string' || key === '') return null;
  return KEY_RE.test(key) ? key : null;
}

export function buildPinnedLaunchPath(slug) {
  return `/launch/${encodeURIComponent(String(slug || ''))}`;
}

export function buildPinnedManifestPath(slug) {
  return `/manifest/session/${encodeURIComponent(String(slug || ''))}.webmanifest`;
}

export function buildPinnedSessionURL(slug, key) {
  return sessionPath(String(slug || ''), normaliseKey(key));
}

function normaliseSource(source) {
  return source === KEY_FROM_LINK || source === KEY_FROM_PASSWORD ? source : null;
}

export function pinStorageKey(slug) {
  return PIN_PREFIX + String(slug || '');
}

export function loadPinnedSession(storage, slug) {
  if (!storage || !isValidSlug(slug)) return null;
  try {
    const raw = storage.getItem(pinStorageKey(slug));
    if (!raw) return null;
    const parsed = JSON.parse(raw);
    if (!parsed || parsed.slug !== slug) return null;
    const key = normaliseKey(parsed.key);
    return {
      slug,
      key,
      source: key ? normaliseSource(parsed.source) : null,
      updatedAt: Number.isFinite(parsed.updatedAt) ? parsed.updatedAt : 0,
    };
  } catch {
    return null;
  }
}

export function savePinnedSession(storage, slug, key, source = null) {
  if (!storage || !isValidSlug(slug)) return false;
  try {
    const normalizedKey = normaliseKey(key);
    const entry = {
      slug,
      key: normalizedKey,
      source: normalizedKey ? normaliseSource(source) : null,
      updatedAt: Date.now(),
    };
    storage.setItem(pinStorageKey(slug), JSON.stringify(entry));
    return true;
  } catch {
    return false;
  }
}

export function rememberPinnedSession(storage, slug, key, source = null) {
  if (!storage || !isValidSlug(slug)) return false;
  const normalizedKey = normaliseKey(key);
  const existing = loadPinnedSession(storage, slug);
  if (normalizedKey) return savePinnedSession(storage, slug, normalizedKey, source);
  if (existing) return true;
  return savePinnedSession(storage, slug, null);
}

export function resolvePinnedLaunch(storage, slug) {
  const entry = loadPinnedSession(storage, slug);
  // Keys of unknown origin (saved before sources were recorded) stay out of
  // the URL too: the session page still finds them in storage.
  const linkKey = entry && entry.source === KEY_FROM_LINK ? entry.key : null;
  return {
    found: entry != null,
    hasPinnedKey: !!(entry && entry.key),
    redirectURL: buildPinnedSessionURL(slug, linkKey),
  };
}
// safeLocalStorage returns the scope's localStorage, or null when reading the
// property itself throws (blocked storage, sandboxed iframe, some private
// modes): callers then work without remembered keys instead of crashing.
export function safeLocalStorage(scope = globalThis) {
  try {
    return scope.localStorage || null;
  } catch {
    return null;
  }
}
