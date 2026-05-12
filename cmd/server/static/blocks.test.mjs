import test from 'node:test';
import assert from 'node:assert/strict';
import { parseBlocks } from './blocks.js';

test('empty input → empty list', () => {
  assert.deepEqual(parseBlocks(''), []);
});

test('single line', () => {
  assert.deepEqual(parseBlocks('hello'), [{ type: 'line', text: 'hello' }]);
});

test('multiple plain lines', () => {
  assert.deepEqual(parseBlocks('a\nb\nc'), [
    { type: 'line', text: 'a' },
    { type: 'line', text: 'b' },
    { type: 'line', text: 'c' },
  ]);
});

test('balanced block → single item without delimiters', () => {
  const text = 'before\n-----\nfoo\nbar\n-----\nafter';
  assert.deepEqual(parseBlocks(text), [
    { type: 'line', text: 'before' },
    { type: 'block', text: 'foo\nbar' },
    { type: 'line', text: 'after' },
  ]);
});

test('block at start and end of input', () => {
  const text = '-----\nx\ny\n-----';
  assert.deepEqual(parseBlocks(text), [
    { type: 'block', text: 'x\ny' },
  ]);
});

test('empty block (two delimiters in a row)', () => {
  const text = '-----\n-----';
  assert.deepEqual(parseBlocks(text), [
    { type: 'block', text: '' },
  ]);
});

test('two consecutive blocks', () => {
  const text = '-----\na\n-----\n-----\nb\n-----';
  assert.deepEqual(parseBlocks(text), [
    { type: 'block', text: 'a' },
    { type: 'block', text: 'b' },
  ]);
});

test('unmatched delimiter is a plain line', () => {
  const text = 'before\n-----\nstuff';
  assert.deepEqual(parseBlocks(text), [
    { type: 'line', text: 'before' },
    { type: 'line', text: '-----' },
    { type: 'line', text: 'stuff' },
  ]);
});

test('delimiter with surrounding whitespace matches', () => {
  const text = '  -----  \nx\n-----\n';
  assert.deepEqual(parseBlocks(text), [
    { type: 'block', text: 'x' },
    { type: 'line', text: '' },
  ]);
});

test('non-delimiter lines that contain -----', () => {
  const text = 'a -----\n-----b\n-----';
  // Only the third line is an unmatched delimiter
  assert.deepEqual(parseBlocks(text), [
    { type: 'line', text: 'a -----' },
    { type: 'line', text: '-----b' },
    { type: 'line', text: '-----' },
  ]);
});

test('odd number of delimiters: first pair consumed, last remains plain', () => {
  const text = '-----\nA\n-----\n-----\nB';
  assert.deepEqual(parseBlocks(text), [
    { type: 'block', text: 'A' },
    { type: 'line', text: '-----' },
    { type: 'line', text: 'B' },
  ]);
});

test('round-trip example from spec', () => {
  const text = [
    '-----',
    'services:',
    '  sharetext:',
    '    build: .',
    '-----',
  ].join('\n');
  const got = parseBlocks(text);
  assert.equal(got.length, 1);
  assert.equal(got[0].type, 'block');
  assert.equal(got[0].text, 'services:\n  sharetext:\n    build: .');
});
