import test from 'node:test';
import assert from 'node:assert/strict';

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

test('classifyLock returns free when snapshot is missing or unheld', () => {
  assert.equal(classifyLock(null, 'me'), LOCK_STATE_FREE);
  assert.equal(classifyLock({ held: false }, 'me'), LOCK_STATE_FREE);
  assert.equal(classifyLock(undefined, 'me'), LOCK_STATE_FREE);
});

test('classifyLock distinguishes own holder from other holder', () => {
  assert.equal(classifyLock({ held: true, holder: 'me' }, 'me'), LOCK_STATE_MINE);
  assert.equal(classifyLock({ held: true, holder: 'them' }, 'me'), LOCK_STATE_THEIRS);
  // Unknown client id treats any held lock as "theirs".
  assert.equal(classifyLock({ held: true, holder: 'x' }, ''), LOCK_STATE_THEIRS);
});

test('canEditNow allows free + mine, blocks theirs', () => {
  assert.equal(canEditNow(LOCK_STATE_FREE), true);
  assert.equal(canEditNow(LOCK_STATE_MINE), true);
  assert.equal(canEditNow(LOCK_STATE_THEIRS), false);
});

test('shouldRequestLock only when free', () => {
  assert.equal(shouldRequestLock(LOCK_STATE_FREE), true);
  assert.equal(shouldRequestLock(LOCK_STATE_MINE), false);
  assert.equal(shouldRequestLock(LOCK_STATE_THEIRS), false);
});

test('nextHeartbeatDelayMs: only when mine', () => {
  assert.equal(nextHeartbeatDelayMs({ state: LOCK_STATE_FREE, expiresAt: null, nowMs: 0 }), null);
  assert.equal(nextHeartbeatDelayMs({ state: LOCK_STATE_THEIRS, expiresAt: null, nowMs: 0 }), null);
});

test('nextHeartbeatDelayMs: returns half of remaining clamped', () => {
  const now = 1_000_000;
  // 10s remaining → half = 5000 → clamped to max 7000 keeps 5000
  const d1 = nextHeartbeatDelayMs({
    state: LOCK_STATE_MINE,
    expiresAt: new Date(now + 10_000).toISOString(),
    nowMs: now,
  });
  assert.equal(d1, 5000);
  // 1s remaining → half = 500 → clamped to min 1000
  const d2 = nextHeartbeatDelayMs({
    state: LOCK_STATE_MINE,
    expiresAt: new Date(now + 1000).toISOString(),
    nowMs: now,
  });
  assert.equal(d2, 1000);
  // 60s remaining → half = 30000 → clamped to max 7000
  const d3 = nextHeartbeatDelayMs({
    state: LOCK_STATE_MINE,
    expiresAt: new Date(now + 60_000).toISOString(),
    nowMs: now,
  });
  assert.equal(d3, 7000);
});

test('nextHeartbeatDelayMs: invalid expires_at falls back to maxMs', () => {
  const d = nextHeartbeatDelayMs({ state: LOCK_STATE_MINE, expiresAt: null, nowMs: 0 });
  assert.equal(d, 7000);
});

test('shouldAutoRelease: only when mine and idle', () => {
  const now = 10_000;
  assert.equal(shouldAutoRelease({ state: LOCK_STATE_MINE, lastInputAt: now - 6000, nowMs: now, idleMs: 5000 }), true);
  assert.equal(shouldAutoRelease({ state: LOCK_STATE_MINE, lastInputAt: now - 1000, nowMs: now, idleMs: 5000 }), false);
  assert.equal(shouldAutoRelease({ state: LOCK_STATE_FREE, lastInputAt: now - 6000, nowMs: now, idleMs: 5000 }), false);
  assert.equal(shouldAutoRelease({ state: LOCK_STATE_THEIRS, lastInputAt: now - 6000, nowMs: now, idleMs: 5000 }), false);
  assert.equal(shouldAutoRelease({ state: LOCK_STATE_MINE, lastInputAt: NaN, nowMs: now, idleMs: 5000 }), false);
});

test('parseIdleReleaseMs returns fallback for missing/invalid input', () => {
  assert.equal(parseIdleReleaseMs(undefined, 3000), 3000);
  assert.equal(parseIdleReleaseMs(null, 3000), 3000);
  assert.equal(parseIdleReleaseMs('', 3000), 3000);
  assert.equal(parseIdleReleaseMs('abc', 3000), 3000);
  assert.equal(parseIdleReleaseMs('0', 3000), 3000);
  assert.equal(parseIdleReleaseMs('-5', 3000), 3000);
});

test('parseIdleReleaseMs parses positive integers, flooring below minMs', () => {
  assert.equal(parseIdleReleaseMs('3000', 3000), 3000);
  assert.equal(parseIdleReleaseMs('7000', 3000), 7000);
  // Below minMs (default 1000) gets clamped up.
  assert.equal(parseIdleReleaseMs('200', 3000), 1000);
  // Custom minMs.
  assert.equal(parseIdleReleaseMs('500', 3000, 100), 500);
});
