import test from 'node:test';
import assert from 'node:assert/strict';
import { b64urlEncode } from './crypto.js';
import { findUrls } from './linkify.js';
import {
  ALPHABET, KEY_CHARS, CHECK_CHARS,
  encodeKey, decodeKey, checksum, shareFragment, sessionPath, parseSessionLocation,
} from './share-link.js';

const KEY = b64urlEncode(Uint8Array.from({ length: 32 }, (_, i) => (i * 37 + 11) & 0xff));
const SLUG = 'misc-notes_x-wwj7KQNgH52udcr3';
const ONLY_ALPHABET = new RegExp('^[' + ALPHABET + ']+$');

test('encodeKey/decodeKey roundtrip, fixed width, alphabet only', () => {
  const samples = [new Uint8Array(32), new Uint8Array(32).fill(255)];
  for (let i = 0; i < 200; i++) samples.push(crypto.getRandomValues(new Uint8Array(32)));
  for (const bytes of samples) {
    const text = encodeKey(bytes);
    assert.equal(text.length, KEY_CHARS);
    assert.match(text, ONLY_ALPHABET);
    assert.deepEqual(decodeKey(text), bytes);
  }
});

test('decodeKey rejects non-keys', () => {
  const ok = encodeKey(new Uint8Array(32).fill(7));
  assert.equal(decodeKey(ok.slice(1)), null);
  assert.equal(decodeKey(ok + 'A'), null);
  for (const bad of ['0', 'O', 'l', '1', '-', '_', '=']) assert.equal(decodeKey(bad + ok.slice(1)), null, bad);
  assert.equal(decodeKey('9'.repeat(KEY_CHARS)), null, 'values >= 2^256');
  assert.equal(decodeKey(null), null);
});

test('share fragment roundtrips and uses only letters and digits after the slug', () => {
  for (const slug of [SLUG, 'pGf3Q627CePBLZaP', 'a']) {
    const frag = shareFragment(slug, KEY);
    assert.ok(frag.startsWith('#!' + slug));
    assert.match(frag.slice(2 + slug.length), ONLY_ALPHABET);
    assert.equal(frag.length, 2 + slug.length + KEY_CHARS + CHECK_CHARS);
    assert.deepEqual(parseSessionLocation({ hash: frag }), { slug, keyB64: KEY, fromLink: true });
  }
  for (let i = 0; i < 50; i++) {
    const key = b64urlEncode(crypto.getRandomValues(new Uint8Array(32)));
    assert.equal(parseSessionLocation({ hash: shareFragment(SLUG, key) }).keyB64, key);
  }
});

test('a truncated share link is reported as damaged, never misread', () => {
  const frag = shareFragment(SLUG, KEY);
  for (let cut = 1; cut < frag.length - 2; cut++) {
    assert.deepEqual(parseSessionLocation({ hash: frag.slice(0, -cut) }), { error: 'damaged' }, `cut ${cut}`);
  }
  assert.deepEqual(parseSessionLocation({ hash: '#!' }), { error: 'damaged' });
});

test('an altered character is reported as damaged', () => {
  const frag = shareFragment(SLUG, KEY);
  for (let i = 2; i < frag.length; i++) {
    for (const ch of ['A', 'z', '7']) {
      if (frag[i] === ch) continue;
      const bad = frag.slice(0, i) + ch + frag.slice(i + 1);
      assert.deepEqual(parseSessionLocation({ hash: bad }), { error: 'damaged' }, `pos ${i} -> ${ch}`);
    }
  }
});

test('copy/paste debris around a share link is tolerated', () => {
  const frag = shareFragment(SLUG, KEY);
  const want = { slug: SLUG, keyB64: KEY, fromLink: true };
  for (const tail of ['.', ',', ';', ':', ')', ').', '!', '?', '"', "'", '>', '*', ']', '%20', '%0A', ' ', '0', 'l']) {
    assert.deepEqual(parseSessionLocation({ hash: frag + tail }), want, JSON.stringify(tail));
  }
  const body = frag.slice(2);
  assert.deepEqual(parseSessionLocation({ hash: '#%21' + body }), want, 'percent-encoded !');
  assert.deepEqual(parseSessionLocation({ hash: '#!' + body.slice(0, 20) + '%0A' + body.slice(20) }), want, 'line break');
  assert.deepEqual(parseSessionLocation({ hash: '#!' + body.slice(0, 30) + '\u200B' + body.slice(30) }), want, 'zero-width space');
  assert.deepEqual(parseSessionLocation({ hash: '#!' + body.slice(0, 9) + '%20' + body.slice(9) }), want, 'space');
  assert.deepEqual(parseSessionLocation({ hash: '#!abc%E0%A4%A' }), { error: 'damaged' }, 'malformed escape must not throw');
});

test('legacy #k= links and keyless links keep working', () => {
  assert.deepEqual(parseSessionLocation({ hash: '#k=' + KEY, pathSlug: SLUG }), { slug: SLUG, keyB64: KEY, fromLink: true });
  assert.deepEqual(parseSessionLocation({ hash: '#x=1&k=' + KEY, pathSlug: SLUG }), { slug: SLUG, keyB64: KEY, fromLink: true });
  assert.deepEqual(parseSessionLocation({ hash: '#k=short', pathSlug: SLUG }), { slug: SLUG, keyB64: null, fromLink: false });
  assert.deepEqual(parseSessionLocation({ hash: '', pathSlug: SLUG }), { slug: SLUG, keyB64: null, fromLink: false });
});

test('the share page without an address reports missing', () => {
  assert.deepEqual(parseSessionLocation({ hash: '' }), { error: 'missing' });
  assert.deepEqual(parseSessionLocation({ hash: '#k=' + KEY }), { error: 'missing' });
  assert.deepEqual(parseSessionLocation(), { error: 'missing' });
});

test('sessionPath puts the key in the fragment only when given one', () => {
  assert.equal(sessionPath(SLUG, KEY), '/s/' + shareFragment(SLUG, KEY));
  assert.equal(sessionPath(SLUG), '/s/' + SLUG);
  assert.equal(sessionPath(SLUG, 'not-a-key'), '/s/' + SLUG);
});

test('checksum is stable and alphabet-only', () => {
  assert.equal(checksum('abc'), checksum('abc'));
  assert.notEqual(checksum('abc'), checksum('abd'));
  assert.match(checksum(''), ONLY_ALPHABET);
  assert.equal(checksum('x').length, CHECK_CHARS);
});

test('the in-session linkifier keeps the whole share link', () => {
  const url = 'https://sharetext.example/s/' + shareFragment(SLUG, KEY);
  for (const text of [`link: ${url}`, `(${url})`, `${url}.`, `vedi ${url}!`, `"${url}"`]) {
    assert.deepEqual(findUrls(text).map((m) => m.url), [url], text);
  }
});
