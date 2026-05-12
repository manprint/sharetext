/**
 * Pure helpers for the temporary-session countdown.
 * Kept side-effect free so they can be unit-tested under node.
 */

/**
 * formatRemaining(msRemaining) → human-readable "HH:MM:SS" or "MM:SS".
 * Returns "00:00" for any non-positive value.
 *
 * @param {number} msRemaining
 * @returns {string}
 */
export function formatRemaining(msRemaining) {
  const total = Math.max(0, Math.floor(msRemaining / 1000));
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  const pad = (n) => String(n).padStart(2, '0');
  if (h > 0) return `${pad(h)}:${pad(m)}:${pad(s)}`;
  return `${pad(m)}:${pad(s)}`;
}

/**
 * msUntil(expiresAtISO, now) returns milliseconds between now and the parsed
 * ISO timestamp. Returns 0 when expiresAtISO is falsy or unparseable.
 *
 * @param {string|null|undefined} expiresAtISO
 * @param {Date} [now]
 * @returns {number}
 */
export function msUntil(expiresAtISO, now = new Date()) {
  if (!expiresAtISO) return 0;
  const t = Date.parse(expiresAtISO);
  if (Number.isNaN(t)) return 0;
  return t - now.getTime();
}

/**
 * isExpired(expiresAtISO, now) returns true if the timestamp is in the past.
 * Returns false when expiresAtISO is falsy (persistent sessions never expire).
 *
 * @param {string|null|undefined} expiresAtISO
 * @param {Date} [now]
 * @returns {boolean}
 */
export function isExpired(expiresAtISO, now = new Date()) {
  if (!expiresAtISO) return false;
  return msUntil(expiresAtISO, now) <= 0;
}
