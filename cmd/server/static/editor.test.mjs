import test from 'node:test';
import assert from 'node:assert/strict';
import { anchoredScrollTop } from './editor.js';

test('anchoredScrollTop aligns logical rows with different visual heights', () => {
  const editor = [0, 20, 40, 100];
  const rows = [0, 400, 420, 800];
  assert.equal(anchoredScrollTop(10, editor, rows), 200);
  assert.equal(anchoredScrollTop(30, editor, rows), 410);
  assert.equal(anchoredScrollTop(200, rows, editor), 10);
  assert.equal(anchoredScrollTop(410, rows, editor), 30);
  assert.equal(anchoredScrollTop(100, editor, rows), 800);
});

test('anchoredScrollTop handles non-scrollable and mismatched anchor sets', () => {
  assert.equal(anchoredScrollTop(0, [0, 0], [0, 100]), 0);
  assert.equal(anchoredScrollTop(10, [0], [0]), 0);
});
