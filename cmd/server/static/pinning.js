const PIN_PREFIX = 'sharetext.pin:';
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
  const base = `/s/${encodeURIComponent(String(slug || ''))}`;
  const normalizedKey = normaliseKey(key);
  return normalizedKey ? `${base}#k=${normalizedKey}` : base;
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
    return {
      slug,
      key: normaliseKey(parsed.key),
      updatedAt: Number.isFinite(parsed.updatedAt) ? parsed.updatedAt : 0,
    };
  } catch {
    return null;
  }
}

export function savePinnedSession(storage, slug, key) {
  if (!storage || !isValidSlug(slug)) return false;
  try {
    const entry = {
      slug,
      key: normaliseKey(key),
      updatedAt: Date.now(),
    };
    storage.setItem(pinStorageKey(slug), JSON.stringify(entry));
    return true;
  } catch {
    return false;
  }
}

export function rememberPinnedSession(storage, slug, key) {
  if (!storage || !isValidSlug(slug)) return false;
  const normalizedKey = normaliseKey(key);
  const existing = loadPinnedSession(storage, slug);
  if (normalizedKey) return savePinnedSession(storage, slug, normalizedKey);
  if (existing) return true;
  return savePinnedSession(storage, slug, null);
}

export function resolvePinnedLaunch(storage, slug) {
  const entry = loadPinnedSession(storage, slug);
  return {
    found: entry != null,
    hasPinnedKey: !!(entry && entry.key),
    redirectURL: buildPinnedSessionURL(slug, entry ? entry.key : null),
  };
}