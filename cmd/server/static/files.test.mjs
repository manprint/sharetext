import test from 'node:test';
import assert from 'node:assert/strict';
import { parseFileMarker, buildFileMarker, buildFileMarkerRaw, formatBytes, insertMarkersAtPosition } from './files.js';

test('parseFileMarker: valid plain', () => {
  assert.deepEqual(parseFileMarker('[file:abc123:notes.txt]'),
    { id: 'abc123', name: 'notes.txt', encodedName: 'notes.txt' });
});

test('parseFileMarker: url-encoded name with spaces and special chars', () => {
  const m = parseFileMarker('[file:abc:my%20notes%20%26%20stuff.txt]');
  assert.deepEqual(m,
    { id: 'abc', name: 'my notes & stuff.txt', encodedName: 'my%20notes%20%26%20stuff.txt' });
});

test('parseFileMarker: leading/trailing whitespace tolerated', () => {
  assert.deepEqual(parseFileMarker('   [file:id1:a.txt]   '),
    { id: 'id1', name: 'a.txt', encodedName: 'a.txt' });
});

test('parseFileMarker: e2e encrypted name slot preserved verbatim', () => {
  // The encrypted-name shape is `<b64url-iv>.<b64url-ct>`; verify it parses
  // and the raw slot is exposed for client-side decryption.
  const m = parseFileMarker('[file:fid:abcDEF_123-xyz.AaBbCc-_QwErTy]');
  assert.equal(m.id, 'fid');
  assert.equal(m.encodedName, 'abcDEF_123-xyz.AaBbCc-_QwErTy');
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
  assert.equal(m.id, 'id42');
  assert.equal(m.name, 'weird:name].txt');
});

test('buildFileMarkerRaw: embeds slot content verbatim (no URL encoding)', () => {
  const raw = 'iv_part.ct_part';
  const marker = buildFileMarkerRaw('idX', raw);
  assert.equal(marker, '[file:idX:iv_part.ct_part]');
  const m = parseFileMarker(marker);
  assert.equal(m.encodedName, raw);
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

const MARKER_A = '[file:a:x.txt]';
const MARKER_B = '[file:b:y.txt]';

test('insertMarkersAtPosition: empty text, no markers needed → unchanged', () => {
  assert.equal(insertMarkersAtPosition('', 0, []), '');
});

test('insertMarkersAtPosition: empty text → marker followed by newline', () => {
  assert.equal(insertMarkersAtPosition('', 0, [MARKER_A]), `${MARKER_A}\n`);
});

test('insertMarkersAtPosition: mid-line insert prefixes \\n', () => {
  const out = insertMarkersAtPosition('hello world', 5, [MARKER_A]);
  assert.equal(out, `hello\n${MARKER_A}\n world`);
});

test('insertMarkersAtPosition: at start-of-line position does NOT add leading \\n', () => {
  const out = insertMarkersAtPosition('first\nsecond', 6, [MARKER_A]);
  // before "first\n" ends with \n, so no prefix; after "second" doesn't start
  // with \n, so trailing \n is appended.
  assert.equal(out, `first\n${MARKER_A}\nsecond`);
});

test('insertMarkersAtPosition: insert immediately before \\n does NOT add trailing \\n (no blank line)', () => {
  // Cursor right after "ciao: " in "ciao: \ntime...": we want the marker on
  // its own line directly followed by "time", with no extra blank line.
  const text = 'ciao: \ntime : 12:00';
  const out = insertMarkersAtPosition(text, 6, [MARKER_A]);
  assert.equal(out, `ciao: \n${MARKER_A}\ntime : 12:00`);
});

test('insertMarkersAtPosition: multiple markers, joined on their own lines', () => {
  const out = insertMarkersAtPosition('x\ny', 2, [MARKER_A, MARKER_B]);
  assert.equal(out, `x\n${MARKER_A}\n${MARKER_B}\ny`);
});

test('insertMarkersAtPosition: at end of buffer with trailing content empty', () => {
  assert.equal(insertMarkersAtPosition('hello', 5, [MARKER_A]), `hello\n${MARKER_A}\n`);
});

test('insertMarkersAtPosition: at start of buffer', () => {
  assert.equal(insertMarkersAtPosition('hello', 0, [MARKER_A]), `${MARKER_A}\nhello`);
});

test('insertMarkersAtPosition: clamps out-of-range offsets', () => {
  assert.equal(insertMarkersAtPosition('abc', -10, [MARKER_A]), `${MARKER_A}\nabc`);
  assert.equal(insertMarkersAtPosition('abc', 999, [MARKER_A]), `abc\n${MARKER_A}\n`);
});

test('insertMarkersAtPosition: non-string text falls back to empty', () => {
  assert.equal(insertMarkersAtPosition(null, 0, [MARKER_A]), `${MARKER_A}\n`);
});

test('insertMarkersAtPosition: every produced marker line is a parseable file marker', () => {
  const out = insertMarkersAtPosition('ciao: \ntime', 6, [MARKER_A, MARKER_B]);
  const lines = out.split('\n');
  assert.ok(parseFileMarker(lines[1]));
  assert.ok(parseFileMarker(lines[2]));
});
