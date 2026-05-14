import test from 'node:test';
import assert from 'node:assert/strict';
import {
  registerCommand,
  unregisterCommand,
  clearCommands,
  hasCommand,
  listCommands,
  filterCommands,
  findSlashTokenAtCaret,
  formatTimestamp,
  dispatchCommand,
} from './commands.js';

function isolate(fn) {
  return async () => {
    clearCommands();
    try { await fn(); } finally { clearCommands(); }
  };
}

test('findSlashTokenAtCaret: caret right after `/cmd` at end of buffer', () => {
  const text = 'hello /timestamp';
  const got = findSlashTokenAtCaret(text, text.length);
  assert.deepEqual(got, { slashAt: 6, nameEnd: 16, name: 'timestamp' });
});

test('findSlashTokenAtCaret: caret right after `/` (empty name)', () => {
  const text = 'foo /';
  const got = findSlashTokenAtCaret(text, text.length);
  assert.deepEqual(got, { slashAt: 4, nameEnd: 5, name: '' });
});

test('findSlashTokenAtCaret: partial name (autocomplete prefix)', () => {
  const text = 'ciao /ti';
  const got = findSlashTokenAtCaret(text, text.length);
  assert.deepEqual(got, { slashAt: 5, nameEnd: 8, name: 'ti' });
});

test('findSlashTokenAtCaret: slash at buffer start', () => {
  const text = '/upload';
  const got = findSlashTokenAtCaret(text, text.length);
  assert.deepEqual(got, { slashAt: 0, nameEnd: 7, name: 'upload' });
});

test('findSlashTokenAtCaret: slash after newline', () => {
  const text = 'line1\n/upload';
  const got = findSlashTokenAtCaret(text, text.length);
  assert.deepEqual(got, { slashAt: 6, nameEnd: 13, name: 'upload' });
});

test('findSlashTokenAtCaret: caret in the middle of a token returns null', () => {
  const text = '/timestamp';
  // caret sits between "/time" and "stamp" → next char is name char, no match
  assert.equal(findSlashTokenAtCaret(text, 5), null);
});

test('findSlashTokenAtCaret: URL pattern (slash preceded by letter) is rejected', () => {
  const text = 'https://example.com/path';
  assert.equal(findSlashTokenAtCaret(text, 6), null); // after "https:"
  assert.equal(findSlashTokenAtCaret(text, 7), null); // after "https:/"
  // After the second '/', preceded by ':' (not whitespace) → rejected.
  assert.equal(findSlashTokenAtCaret(text, 8), null);
});

test('findSlashTokenAtCaret: slash preceded by non-whitespace returns null', () => {
  assert.equal(findSlashTokenAtCaret('abc/foo', 7), null);
});

test('findSlashTokenAtCaret: caret beyond token (no slash near) returns null', () => {
  const text = 'plain text without commands';
  assert.equal(findSlashTokenAtCaret(text, text.length), null);
});

test('findSlashTokenAtCaret: caret clamps out-of-range values', () => {
  const text = '/x';
  // Caret < 0 clamps to 0 → sits BEFORE the slash → no token.
  assert.equal(findSlashTokenAtCaret(text, -10), null);
  // Caret > length clamps to length → sits at end of `/x` → token matches.
  assert.equal(findSlashTokenAtCaret(text, 999).name, 'x');
});

test('findSlashTokenAtCaret: non-string returns null', () => {
  assert.equal(findSlashTokenAtCaret(null, 0), null);
});

test('findSlashTokenAtCaret: caret right before a name char (end of token) returns null', () => {
  const text = '/ab cd';
  // caret at 1 → next char "a" is name char, so caret is INSIDE a token, reject
  assert.equal(findSlashTokenAtCaret(text, 1), null);
});

test('formatTimestamp: pads zero components', () => {
  const d = new Date(2026, 0, 5, 3, 7, 9);
  assert.equal(formatTimestamp(d), '05-01-2026_03-07-09');
});

test('formatTimestamp: full numbers', () => {
  const d = new Date(2026, 11, 31, 23, 59, 59);
  assert.equal(formatTimestamp(d), '31-12-2026_23-59-59');
});

test('formatTimestamp: defaults to now without throwing', () => {
  assert.match(formatTimestamp(), /^\d{2}-\d{2}-\d{4}_\d{2}-\d{2}-\d{2}$/);
});

test('registerCommand + hasCommand + listCommands', isolate(() => {
  registerCommand('alpha', () => {});
  registerCommand('Beta', () => {});
  assert.ok(hasCommand('alpha'));
  assert.ok(hasCommand('BETA'));
  assert.deepEqual(listCommands(), ['alpha', 'beta']);
}));

test('filterCommands: prefix match case-insensitive', isolate(() => {
  registerCommand('timestamp', () => {});
  registerCommand('upload', () => {});
  registerCommand('topic', () => {});
  assert.deepEqual(filterCommands(''), ['timestamp', 'topic', 'upload']);
  assert.deepEqual(filterCommands('t'), ['timestamp', 'topic']);
  assert.deepEqual(filterCommands('Ti'), ['timestamp']);
  assert.deepEqual(filterCommands('up'), ['upload']);
  assert.deepEqual(filterCommands('zzz'), []);
}));

test('registerCommand rejects invalid name', isolate(() => {
  assert.throws(() => registerCommand('9bad', () => {}));
  assert.throws(() => registerCommand('', () => {}));
  assert.throws(() => registerCommand('with space', () => {}));
}));

test('registerCommand requires function handler', isolate(() => {
  assert.throws(() => registerCommand('x', null));
}));

test('unregisterCommand removes entry', isolate(() => {
  registerCommand('z', () => {});
  assert.ok(unregisterCommand('Z'));
  assert.ok(!hasCommand('z'));
  assert.ok(!unregisterCommand('nope'));
}));

test('dispatchCommand invokes registered handler with ctx', isolate(async () => {
  let received = null;
  registerCommand('echo', (ctx) => { received = ctx; });
  const ctx = { name: 'echo', args: '', text: 'hi', tokenStart: 0, tokenEnd: 5 };
  const ok = await dispatchCommand(ctx);
  assert.equal(ok, true);
  assert.equal(received, ctx);
}));

test('dispatchCommand returns false for unknown command', isolate(async () => {
  assert.equal(await dispatchCommand({ name: 'nope' }), false);
}));

test('dispatchCommand awaits async handler', isolate(async () => {
  let done = false;
  registerCommand('slow', async () => {
    await new Promise((r) => setTimeout(r, 5));
    done = true;
  });
  await dispatchCommand({ name: 'slow' });
  assert.equal(done, true);
}));

test('dispatchCommand: missing/invalid ctx returns false', isolate(async () => {
  assert.equal(await dispatchCommand(null), false);
  assert.equal(await dispatchCommand({}), false);
}));
