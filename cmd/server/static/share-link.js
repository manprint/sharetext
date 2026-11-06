/**
 * Share links carry the whole session address in the URL fragment, which
 * browsers never send to the server:
 *
 *   /s/#!<slug><key><check>
 *
 * <key> is the 256-bit AES key written with the slug alphabet (letters and
 * digits only, no look-alikes), always KEY_CHARS long; <check> is a short
 * checksum over slug and key. Letters and digits survive chat apps, mail
 * clients and bookmarks untouched (no '=', '_' or trailing punctuation to trim),
 * and the checksum turns a truncated or mangled link into a clear error
 * instead of a wrong key or a wrong session.
 *
 * Legacy links (/s/<slug>#k=<base64url key>) and keyless links (/s/<slug>,
 * password sessions) keep working.
 */
import { b64urlEncode, b64urlDecode } from './crypto.js';

export const ALPHABET = 'ABCDEFGHIJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789';
export const KEY_CHARS = 44;
export const CHECK_CHARS = 3;
const TAIL_CHARS = KEY_CHARS + CHECK_CHARS;
const BASE = BigInt(ALPHABET.length);
const KEY_LIMIT = 1n << 256n;
const SLUG_RE = /^[A-Za-z0-9_-]+$/;
const LEGACY_KEY_RE = /(?:^#|&)k=([A-Za-z0-9_-]+)/;
const INVISIBLE_RE = /[\s\u00AD\u200B-\u200F\u2060\uFEFF]/g;
const NOT_ALPHABET_TAIL_RE = new RegExp('[^' + ALPHABET + ']+$');

function encodeNumber(n, width) {
  let out = '';
  for (let i = 0; i < width; i++) {
    out = ALPHABET[Number(n % BASE)] + out;
    n /= BASE;
  }
  return out;
}

/** encodeKey turns 32 key bytes into KEY_CHARS alphabet characters. */
export function encodeKey(bytes) {
  if (!(bytes instanceof Uint8Array) || bytes.length !== 32) throw new Error('expected 32-byte key');
  let n = 0n;
  for (const b of bytes) n = (n << 8n) | BigInt(b);
  return encodeNumber(n, KEY_CHARS);
}

/** decodeKey is the inverse of encodeKey; null for anything that is not a key. */
export function decodeKey(text) {
  if (typeof text !== 'string' || text.length !== KEY_CHARS) return null;
  let n = 0n;
  for (const ch of text) {
    const d = ALPHABET.indexOf(ch);
    if (d < 0) return null;
    n = n * BASE + BigInt(d);
  }
  if (n >= KEY_LIMIT) return null;
  const out = new Uint8Array(32);
  for (let i = 31; i >= 0; i--) {
    out[i] = Number(n & 0xffn);
    n >>= 8n;
  }
  return out;
}

/** checksum: FNV-1a over slug and encoded key, as CHECK_CHARS characters. */
export function checksum(text) {
  let h = 0x811c9dc5;
  for (let i = 0; i < text.length; i++) {
    h ^= text.charCodeAt(i);
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return encodeNumber(BigInt(h) % (BASE ** BigInt(CHECK_CHARS)), CHECK_CHARS);
}

/** shareFragment builds '#!<slug><key><check>' from a base64url key. */
export function shareFragment(slug, keyB64) {
  const key = encodeKey(b64urlDecode(keyB64));
  return '#!' + slug + key + checksum(slug + key);
}

/**
 * sessionPath is where a session lives in the address bar: the share link
 * when the key may travel with it, the bare /s/<slug> otherwise.
 */
export function sessionPath(slug, keyB64 = null) {
  if (keyB64) {
    try { return '/s/' + shareFragment(slug, keyB64); } catch {}
  }
  return '/s/' + encodeURIComponent(slug);
}

function cleanFragment(hash) {
  let h = String(hash || '');
  try { h = decodeURIComponent(h); } catch {}
  return h.replace(INVISIBLE_RE, '');
}

function parseBang(body) {
  const tail = body.replace(NOT_ALPHABET_TAIL_RE, '');
  if (tail.length <= TAIL_CHARS) return null;
  const slug = tail.slice(0, -TAIL_CHARS);
  const key = tail.slice(-TAIL_CHARS, -CHECK_CHARS);
  const check = tail.slice(-CHECK_CHARS);
  if (!SLUG_RE.test(slug) || checksum(slug + key) !== check) return null;
  const bytes = decodeKey(key);
  if (!bytes) return null;
  return { slug, keyB64: b64urlEncode(bytes) };
}

/**
 * parseSessionLocation reads the session address from the page URL.
 * pathSlug is the slug the server rendered (empty on the /s/ share page).
 *
 * Returns { slug, keyB64, fromLink } on success, where keyB64 is null for
 * keyless links and fromLink tells whether the key came from the link, or
 * { error } with 'missing' (no address at all) or 'damaged' (truncated or
 * altered share link).
 */
export function parseSessionLocation({ hash = '', pathSlug = '' } = {}) {
  const h = cleanFragment(hash);
  if (h.startsWith('#!')) {
    const parsed = parseBang(h.slice(2));
    return parsed ? { ...parsed, fromLink: true } : { error: 'damaged' };
  }
  if (!pathSlug) return { error: 'missing' };
  const m = h.match(LEGACY_KEY_RE);
  let keyB64 = null;
  if (m) {
    try { if (b64urlDecode(m[1]).length === 32) keyB64 = m[1]; } catch {}
  }
  return { slug: pathSlug, keyB64, fromLink: keyB64 != null };
}
