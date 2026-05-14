import test from 'node:test';
import assert from 'node:assert/strict';
import { findUrls } from './linkify.js';

test('empty / missing input → no matches', () => {
  assert.deepEqual(findUrls(''), []);
  assert.deepEqual(findUrls(null), []);
  assert.deepEqual(findUrls(undefined), []);
});

test('no urls → no matches', () => {
  assert.deepEqual(findUrls('just some text'), []);
});

test('bare https url', () => {
  const text = 'https://example.com/path';
  assert.deepEqual(findUrls(text), [
    { start: 0, end: text.length, url: 'https://example.com/path' },
  ]);
});

test('bare http url', () => {
  const text = 'http://example.com';
  assert.deepEqual(findUrls(text), [
    { start: 0, end: text.length, url: 'http://example.com' },
  ]);
});

test('url embedded inside text', () => {
  const text = 'see https://example.com for more';
  assert.deepEqual(findUrls(text), [
    { start: 4, end: 23, url: 'https://example.com' },
  ]);
});

test('trailing sentence punctuation is stripped', () => {
  assert.deepEqual(findUrls('go to https://example.com.'), [
    { start: 6, end: 25, url: 'https://example.com' },
  ]);
  assert.deepEqual(findUrls('https://example.com, then later'), [
    { start: 0, end: 19, url: 'https://example.com' },
  ]);
});

test('unbalanced closing paren is stripped', () => {
  assert.deepEqual(findUrls('(see https://example.com)'), [
    { start: 5, end: 24, url: 'https://example.com' },
  ]);
});

test('balanced parens inside url are kept', () => {
  const text = 'https://en.wikipedia.org/wiki/Foo_(bar)';
  assert.deepEqual(findUrls(text), [
    { start: 0, end: text.length, url: text },
  ]);
});

test('multiple urls on the same line', () => {
  const text = 'a https://one.example b http://two.example c';
  assert.deepEqual(findUrls(text), [
    { start: 2, end: 21, url: 'https://one.example' },
    { start: 24, end: 42, url: 'http://two.example' },
  ]);
});

test('non-http schemes are ignored', () => {
  assert.deepEqual(findUrls('mailto:foo@example.com ftp://x.example'), []);
});

test('whitespace terminates the url', () => {
  assert.deepEqual(findUrls('https://example.com hello'), [
    { start: 0, end: 19, url: 'https://example.com' },
  ]);
});

test('truncated url (just the scheme) is not reported', () => {
  assert.deepEqual(findUrls('https:// is broken'), []);
});
