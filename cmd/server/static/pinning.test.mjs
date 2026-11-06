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
  safeLocalStorage,
  KEY_FROM_LINK,
  KEY_FROM_PASSWORD,
} from './pinning.js';
import { b64urlEncode } from './crypto.js';
import { shareFragment } from './share-link.js';

const KEY = b64urlEncode(new Uint8Array(32).fill(42));

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

test('buildPinnedSessionURL builds a share link only when a key exists', () => {
  assert.equal(buildPinnedSessionURL('tmpfab-abc123', KEY), '/s/' + shareFragment('tmpfab-abc123', KEY));
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

test('resolvePinnedLaunch redirects with a key remembered from a link', () => {
  const storage = makeStorage();
  savePinnedSession(storage, 'tmpfab-abc123', KEY, KEY_FROM_LINK);
  assert.deepEqual(resolvePinnedLaunch(storage, 'tmpfab-abc123'), {
    found: true,
    hasPinnedKey: true,
    redirectURL: '/s/' + shareFragment('tmpfab-abc123', KEY),
  });
});

test('resolvePinnedLaunch never puts a password-derived or unknown key in the URL', () => {
  const storage = makeStorage();
  savePinnedSession(storage, 'room-pw', KEY, KEY_FROM_PASSWORD);
  assert.deepEqual(resolvePinnedLaunch(storage, 'room-pw'), { found: true, hasPinnedKey: true, redirectURL: '/s/room-pw' });
  storage.setItem(pinStorageKey('room-old'), JSON.stringify({ slug: 'room-old', key: KEY, updatedAt: 1 }));
  assert.deepEqual(resolvePinnedLaunch(storage, 'room-old'), { found: true, hasPinnedKey: true, redirectURL: '/s/room-old' });
});

test('the key source is stored, reloaded and kept by a keyless revisit', () => {
  const storage = makeStorage();
  rememberPinnedSession(storage, 'room-abc', KEY, KEY_FROM_PASSWORD);
  assert.equal(loadPinnedSession(storage, 'room-abc').source, KEY_FROM_PASSWORD);
  rememberPinnedSession(storage, 'room-abc', null);
  assert.equal(loadPinnedSession(storage, 'room-abc').source, KEY_FROM_PASSWORD);
  rememberPinnedSession(storage, 'room-abc', KEY, KEY_FROM_LINK);
  assert.equal(loadPinnedSession(storage, 'room-abc').source, KEY_FROM_LINK);
  savePinnedSession(storage, 'room-abc', KEY, 'bogus');
  assert.equal(loadPinnedSession(storage, 'room-abc').source, null);
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
test('passphrase flow: key saved without fragment is restored by loadPinnedSession', () => {
  const storage = makeStorage();
  savePinnedSession(storage, 'room-abc', 'AbC_123-xyz');
  const entry = loadPinnedSession(storage, 'room-abc');
  assert.equal(entry.key, 'AbC_123-xyz');
});

test('a null re-save does not clear a stored key via rememberPinnedSession', () => {
  const storage = makeStorage();
  savePinnedSession(storage, 'room-abc', 'AbC_123-xyz');
  rememberPinnedSession(storage, 'room-abc', null);
  assert.equal(loadPinnedSession(storage, 'room-abc').key, 'AbC_123-xyz');
});

test('safeLocalStorage: throwing getter yields null', () => {
  const scope = {};
  Object.defineProperty(scope, 'localStorage', { get() { throw new Error('SecurityError'); } });
  assert.equal(safeLocalStorage(scope), null);
  const storage = makeStorage();
  assert.equal(safeLocalStorage({ localStorage: storage }), storage);
  assert.equal(safeLocalStorage({}), null);
});
