import test from 'node:test';
import assert from 'node:assert/strict';
import { safeFilename, buildFilename } from './download.js';

test('safeFilename: plain text passes through', () => {
  assert.equal(safeFilename('team-alpha'), 'team-alpha');
});

test('safeFilename: forbidden characters replaced with underscore', () => {
  assert.equal(safeFilename('a/b\\c:d*e?f"g<h>i|j'), 'a_b_c_d_e_f_g_h_i_j');
});

test('safeFilename: control chars replaced', () => {
  assert.equal(safeFilename('abc'), 'a_b_c');
});

test('safeFilename: whitespace collapsed to underscore', () => {
  assert.equal(safeFilename('  hello   world  '), '_hello_world_');
});

test('safeFilename: empty / nullish → fallback', () => {
  assert.equal(safeFilename(''), 'sharetext');
  assert.equal(safeFilename(null), 'sharetext');
  assert.equal(safeFilename(undefined), 'sharetext');
});

test('safeFilename: leading dots stripped', () => {
  assert.equal(safeFilename('..hidden'), 'hidden');
});

test('safeFilename: truncated to 80 chars', () => {
  const long = 'a'.repeat(200);
  assert.equal(safeFilename(long).length, 80);
});

test('buildFilename: slug only → <slug>.txt', () => {
  assert.equal(buildFilename('team-alpha'), 'team-alpha.txt');
});

test('buildFilename: with kind+index', () => {
  assert.equal(buildFilename('abc', 'riga', 3), 'abc-riga-3.txt');
  assert.equal(buildFilename('abc', 'blocco', 12), 'abc-blocco-12.txt');
});

test('buildFilename: missing index defaults to 1', () => {
  assert.equal(buildFilename('abc', 'riga'), 'abc-riga-1.txt');
});

test('buildFilename: index floored, min 1', () => {
  assert.equal(buildFilename('abc', 'riga', 0), 'abc-riga-1.txt');
  assert.equal(buildFilename('abc', 'riga', -5), 'abc-riga-1.txt');
  assert.equal(buildFilename('abc', 'riga', 2.9), 'abc-riga-2.txt');
});

test('buildFilename: slug sanitized', () => {
  assert.equal(buildFilename('a/b:c'), 'a_b_c.txt');
});

test('buildFilename: empty slug falls back to sharetext', () => {
  assert.equal(buildFilename(''), 'sharetext.txt');
  assert.equal(buildFilename(null, 'riga', 2), 'sharetext-riga-2.txt');
});
