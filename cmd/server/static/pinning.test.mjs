import test from 'node:test';
import assert from 'node:assert/strict';
import {
  buildPinnedLaunchPath,
  buildPinnedManifestPath,
  buildPinnedSessionURL,
  pinStorageKey,
  loadPinnedSession,
  savePinnedSession,
  rememberPinnedSession,
  resolvePinnedLaunch,
} from './pinning.js';

function makeStorage() {
  const map = new Map();
  return {
    getItem(key) {
      return map.has(key) ? map.get(key) : null;
    },
    setItem(key, value) {
      map.set(key, String(value));
    },
    removeItem(key) {
      map.delete(key);
    },
  };
}

test('buildPinnedLaunchPath and buildPinnedManifestPath derive same-session routes', () => {
  assert.equal(buildPinnedLaunchPath('tmpfab-abc123'), '/launch/tmpfab-abc123');
  assert.equal(buildPinnedManifestPath('tmpfab-abc123'), '/manifest/session/tmpfab-abc123.webmanifest');
});

test('buildPinnedSessionURL preserves hash only when a key exists', () => {
  assert.equal(buildPinnedSessionURL('tmpfab-abc123', 'abc_DEF-123'), '/s/tmpfab-abc123#k=abc_DEF-123');
  assert.equal(buildPinnedSessionURL('tmpfab-abc123', null), '/s/tmpfab-abc123');
});

test('savePinnedSession + loadPinnedSession roundtrip', () => {
  const storage = makeStorage();
  assert.equal(savePinnedSession(storage, 'tmpfab-abc123', 'abc_DEF-123'), true);
  assert.equal(pinStorageKey('tmpfab-abc123'), 'sharetext.pin:tmpfab-abc123');
  const entry = loadPinnedSession(storage, 'tmpfab-abc123');
  assert.equal(entry.slug, 'tmpfab-abc123');
  assert.equal(entry.key, 'abc_DEF-123');
  assert.ok(entry.updatedAt > 0);
});

test('rememberPinnedSession preserves an existing key when revisiting bare URL', () => {
  const storage = makeStorage();
  assert.equal(rememberPinnedSession(storage, 'tmpfab-abc123', 'abc_DEF-123'), true);
  assert.equal(rememberPinnedSession(storage, 'tmpfab-abc123', null), true);
  assert.equal(loadPinnedSession(storage, 'tmpfab-abc123').key, 'abc_DEF-123');
});

test('rememberPinnedSession stores a bare launcher entry when no key exists yet', () => {
  const storage = makeStorage();
  assert.equal(rememberPinnedSession(storage, 'tmpfab-abc123', null), true);
  assert.equal(loadPinnedSession(storage, 'tmpfab-abc123').key, null);
});

test('resolvePinnedLaunch redirects with the remembered key when present', () => {
  const storage = makeStorage();
  savePinnedSession(storage, 'tmpfab-abc123', 'abc_DEF-123');
  assert.deepEqual(resolvePinnedLaunch(storage, 'tmpfab-abc123'), {
    found: true,
    hasPinnedKey: true,
    redirectURL: '/s/tmpfab-abc123#k=abc_DEF-123',
  });
});

test('resolvePinnedLaunch falls back to bare session URL when only slug is stored', () => {
  const storage = makeStorage();
  savePinnedSession(storage, 'tmpfab-abc123', null);
  assert.deepEqual(resolvePinnedLaunch(storage, 'tmpfab-abc123'), {
    found: true,
    hasPinnedKey: false,
    redirectURL: '/s/tmpfab-abc123',
  });
});

test('loadPinnedSession tolerates malformed storage payloads', () => {
  const storage = makeStorage();
  storage.setItem(pinStorageKey('tmpfab-abc123'), '{bad json');
  assert.equal(loadPinnedSession(storage, 'tmpfab-abc123'), null);
});