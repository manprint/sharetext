import test from 'node:test';
import assert from 'node:assert/strict';
import { shouldApplyRemoteContent, shouldFlushPendingLocalChanges } from './sync.js';

test('shouldApplyRemoteContent: applies live updates when content differs', () => {
  assert.equal(shouldApplyRemoteContent({
    currentContent: 'local',
    incomingContent: 'remote',
    hasPendingLocalChanges: false,
    initialSnapshot: false,
  }), true);
});

test('shouldApplyRemoteContent: ignores initial snapshot while local changes are pending', () => {
  assert.equal(shouldApplyRemoteContent({
    currentContent: 'pasted text',
    incomingContent: 'server text',
    hasPendingLocalChanges: true,
    initialSnapshot: true,
  }), false);
});

test('shouldApplyRemoteContent: ignores identical content', () => {
  assert.equal(shouldApplyRemoteContent({
    currentContent: 'same',
    incomingContent: 'same',
    hasPendingLocalChanges: false,
    initialSnapshot: true,
  }), false);
});

test('shouldFlushPendingLocalChanges: flushes only after an initial snapshot', () => {
  assert.equal(shouldFlushPendingLocalChanges({
    initialSnapshot: true,
    hasPendingLocalChanges: true,
  }), true);
  assert.equal(shouldFlushPendingLocalChanges({
    initialSnapshot: false,
    hasPendingLocalChanges: true,
  }), false);
  assert.equal(shouldFlushPendingLocalChanges({
    initialSnapshot: true,
    hasPendingLocalChanges: false,
  }), false);
});