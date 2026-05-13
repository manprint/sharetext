// Editor-lock client helpers. Pure functions so they can be unit-tested under
// node --test without a DOM. The runtime UI logic in app.js layers on top.

export const LOCK_STATE_FREE = 'free';
export const LOCK_STATE_MINE = 'mine';
export const LOCK_STATE_THEIRS = 'theirs';

/**
 * Derives the local lock state from a server-supplied lock snapshot.
 *
 *   { held: boolean, holder?: string, expires_at?: string }
 *
 * The clientID is the opaque identifier issued by the server on WS hello.
 */
export function classifyLock(snapshot, clientID) {
  if (!snapshot || !snapshot.held) return LOCK_STATE_FREE;
  if (clientID && snapshot.holder === clientID) return LOCK_STATE_MINE;
  return LOCK_STATE_THEIRS;
}

export function canEditNow(state) {
  return state !== LOCK_STATE_THEIRS;
}

/**
 * Computes the next heartbeat delay in ms. Heartbeats are sent only while the
 * caller holds the lock. We aim for half the remaining TTL, clamped to a sane
 * floor/ceiling so a server with a very small or very large TTL still behaves.
 */
export function nextHeartbeatDelayMs({ state, expiresAt, nowMs, minMs = 1000, maxMs = 7000 }) {
  if (state !== LOCK_STATE_MINE) return null;
  const expMs = expiresAt ? Date.parse(expiresAt) : NaN;
  if (!Number.isFinite(expMs)) return maxMs;
  const remaining = expMs - nowMs;
  if (remaining <= 0) return minMs;
  const half = Math.floor(remaining / 2);
  return Math.max(minMs, Math.min(maxMs, half));
}

/**
 * True when the holder has been idle long enough that we should release the
 * lock voluntarily so peers can edit.
 */
export function shouldAutoRelease({ state, lastInputAt, nowMs, idleMs }) {
  if (state !== LOCK_STATE_MINE) return false;
  if (!Number.isFinite(lastInputAt)) return false;
  return nowMs - lastInputAt >= idleMs;
}

/**
 * Decides whether a fresh local edit should trigger an explicit lock_acquire
 * over the WebSocket. We only ask when the lock is free; if it's already ours
 * the heartbeats keep it; if it's theirs the UI blocks the edit upstream.
 */
export function shouldRequestLock(state) {
  return state === LOCK_STATE_FREE;
}
