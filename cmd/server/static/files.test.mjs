import test from 'node:test';
import assert from 'node:assert/strict';
import { parseFileMarker, buildFileMarker, formatBytes } from './files.js';

test('parseFileMarker: valid plain', () => {
  assert.deepEqual(parseFileMarker('[file:abc123:notes.txt]'), { id: 'abc123', name: 'notes.txt' });
});

test('parseFileMarker: url-encoded name with spaces and special chars', () => {
  const m = parseFileMarker('[file:abc:my%20notes%20%26%20stuff.txt]');
  assert.deepEqual(m, { id: 'abc', name: 'my notes & stuff.txt' });
});

test('parseFileMarker: leading/trailing whitespace tolerated', () => {
  assert.deepEqual(parseFileMarker('   [file:id1:a.txt]   '), { id: 'id1', name: 'a.txt' });
});

test('parseFileMarker: rejects inline (not whole line)', () => {
  assert.equal(parseFileMarker('hello [file:abc:x.txt] world'), null);
});

test('parseFileMarker: rejects malformed', () => {
  assert.equal(parseFileMarker('[file:abc]'), null);
  assert.equal(parseFileMarker('[file::name]'), null);
  assert.equal(parseFileMarker('not a marker'), null);
  assert.equal(parseFileMarker(''), null);
});

test('parseFileMarker: non-string returns null', () => {
  assert.equal(parseFileMarker(null), null);
  assert.equal(parseFileMarker(undefined), null);
  assert.equal(parseFileMarker(42), null);
});

test('buildFileMarker: encodes filename', () => {
  assert.equal(buildFileMarker('abc', 'my notes & stuff.txt'),
    '[file:abc:my%20notes%20%26%20stuff.txt]');
});

test('buildFileMarker: round-trips with parseFileMarker', () => {
  const m = parseFileMarker(buildFileMarker('id42', 'weird:name].txt'));
  assert.deepEqual(m, { id: 'id42', name: 'weird:name].txt' });
});

test('buildFileMarker: nullish filename → fallback', () => {
  assert.equal(buildFileMarker('id1', null), '[file:id1:file.bin]');
  assert.equal(buildFileMarker('id1', undefined), '[file:id1:file.bin]');
});

test('formatBytes', () => {
  assert.equal(formatBytes(0), '0 B');
  assert.equal(formatBytes(512), '512 B');
  assert.equal(formatBytes(1024), '1.0 KB');
  assert.equal(formatBytes(1024 * 1024), '1.00 MB');
  assert.equal(formatBytes(-5), '0 B');
  assert.equal(formatBytes(NaN), '0 B');
});
