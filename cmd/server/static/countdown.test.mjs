import test from 'node:test';
import assert from 'node:assert/strict';
import { formatRemaining, msUntil, isExpired } from './countdown.js';

test('formatRemaining: negative → 00:00', () => {
  assert.equal(formatRemaining(-1000), '00:00');
  assert.equal(formatRemaining(0), '00:00');
});

test('formatRemaining: < 1 hour → MM:SS', () => {
  assert.equal(formatRemaining(59 * 1000), '00:59');
  assert.equal(formatRemaining(60 * 1000), '01:00');
  assert.equal(formatRemaining((30 * 60 + 5) * 1000), '30:05');
});

test('formatRemaining: ≥ 1 hour → HH:MM:SS', () => {
  assert.equal(formatRemaining(60 * 60 * 1000), '01:00:00');
  assert.equal(formatRemaining((2 * 3600 + 3 * 60 + 4) * 1000), '02:03:04');
});

test('formatRemaining: rounding floors seconds', () => {
  assert.equal(formatRemaining(1999), '00:01');
});

test('msUntil: null/empty → 0', () => {
  assert.equal(msUntil(null), 0);
  assert.equal(msUntil(''), 0);
  assert.equal(msUntil(undefined), 0);
});

test('msUntil: unparseable → 0', () => {
  assert.equal(msUntil('not a date'), 0);
});

test('msUntil: future → positive', () => {
  const now = new Date('2026-05-12T12:00:00Z');
  const future = '2026-05-12T12:05:00Z';
  assert.equal(msUntil(future, now), 5 * 60 * 1000);
});

test('msUntil: past → negative', () => {
  const now = new Date('2026-05-12T12:05:00Z');
  const past = '2026-05-12T12:00:00Z';
  assert.equal(msUntil(past, now), -5 * 60 * 1000);
});

test('isExpired: persistent (null) → false', () => {
  assert.equal(isExpired(null), false);
  assert.equal(isExpired(''), false);
});

test('isExpired: future → false', () => {
  const now = new Date('2026-05-12T12:00:00Z');
  assert.equal(isExpired('2026-05-12T12:01:00Z', now), false);
});

test('isExpired: past or now → true', () => {
  const now = new Date('2026-05-12T12:00:00Z');
  assert.equal(isExpired('2026-05-12T11:59:00Z', now), true);
  assert.equal(isExpired('2026-05-12T12:00:00Z', now), true);
});
