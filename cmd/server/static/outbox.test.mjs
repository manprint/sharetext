import test from 'node:test';
import assert from 'node:assert/strict';
import { createOutbox } from './outbox.js';

// settle lets queued promise jobs run until cond() holds (or a bound is hit).
async function settle(cond) {
  for (let i = 0; i < 50 && !cond(); i++) await Promise.resolve();
}

function deferred() {
  let resolve;
  const promise = new Promise((r) => { resolve = r; });
  return { promise, resolve };
}

// harness wires an outbox to a fake editor and transport. `wire` records what
// reached each transport, in order.
function harness({ encode, socketOpen = true, rest = 'ok' } = {}) {
  const h = { text: '', wire: [], socketOpen, rest };
  h.outbox = createOutbox({
    readText: () => h.text,
    encode: encode || (async (t) => `enc(${t})`),
    deliver: async (wire, { allowRest, seq }) => {
      if (h.socketOpen) {
        h.wire.push({ via: 'ws', wire, seq });
        return 'ws';
      }
      if (!allowRest) return null;
      if (h.rest === 'throw') throw new Error('network');
      if (h.rest !== 'ok') return null;
      h.wire.push({ via: 'rest', wire, seq });
      return 'rest';
    },
  });
  h.type = (t) => {
    h.text = t;
    h.outbox.markDirty();
  };
  return h;
}

test('flush sends the current text once and clears pending', async () => {
  const h = harness();
  h.type('a');
  assert.equal(h.outbox.isPending(), true);
  assert.equal(await h.outbox.flush(), true);
  assert.deepEqual(h.wire.map((w) => w.wire), ['enc(a)']);
  assert.equal(h.outbox.isPending(), false);
  // Nothing changed: a second flush transmits nothing.
  assert.equal(await h.outbox.flush(), true);
  assert.equal(h.wire.length, 1);
});

test('a slow encrypt cannot let an older text reach the wire last', async () => {
  const gates = [];
  const h = harness({
    encode: (t) => {
      const d = deferred();
      gates.push(d);
      return d.promise.then(() => `enc(${t})`);
    },
  });
  h.type('old');
  const first = h.outbox.flush();
  await settle(() => gates.length === 1);
  h.type('new');
  const second = h.outbox.flush();
  // The second job must not start encrypting before the first is delivered,
  // however fast its own encryption would be.
  await settle(() => gates.length > 1);
  assert.equal(gates.length, 1);
  gates[0].resolve();
  await first;
  await settle(() => gates.length === 2);
  gates[1].resolve();
  await second;
  assert.deepEqual(h.wire.map((w) => w.wire), ['enc(old)', 'enc(new)']);
});

test('queued flushes collapse onto the latest text', async () => {
  const h = harness();
  h.type('a');
  const p1 = h.outbox.flush();
  const p2 = h.outbox.flush();
  h.type('ab');
  await Promise.all([p1, p2]);
  // Each job reads the editor when it runs: both see "ab", the second is a no-op.
  assert.deepEqual(h.wire.map((w) => w.wire), ['enc(ab)']);
});

test('typing during encryption keeps the newer text pending', async () => {
  let h;
  h = harness({
    encode: async (t) => {
      h.type('ab'); // a keystroke lands while the text is being encrypted
      return `enc(${t})`;
    },
  });
  h.type('a');
  await h.outbox.flush();
  assert.deepEqual(h.wire.map((w) => w.wire), ['enc(a)']);
  assert.equal(h.outbox.isPending(), true, '"ab" was never sent');
});

test('without a socket the text stays pending unless REST is allowed', async () => {
  const h = harness({ socketOpen: false });
  h.type('marker');
  assert.equal(await h.outbox.flush(), false);
  assert.equal(h.outbox.isPending(), true);
  assert.equal(h.wire.length, 0);
  // A failed attempt must not be mistaken for a sent one.
  assert.equal(await h.outbox.flush({ allowRest: true }), true);
  assert.deepEqual(h.wire.map((w) => w.via), ['rest']);
  assert.equal(h.outbox.isPending(), false);
  assert.equal(h.outbox.confirmed(), 'marker');
});

test('a failing PUT keeps the text pending for the WebSocket', async () => {
  const h = harness({ socketOpen: false, rest: 'throw' });
  h.type('marker');
  assert.equal(await h.outbox.flush({ allowRest: true }), false);
  assert.equal(h.outbox.isPending(), true);
  h.socketOpen = true;
  assert.equal(await h.outbox.flush(), true);
  assert.deepEqual(h.wire.map((w) => w.via), ['ws']);
});

test('confirmed follows acknowledgements, not transmissions', async () => {
  const h = harness();
  h.outbox.remoteApplied('server');
  h.text = 'server';
  h.type('one');
  await h.outbox.flush();
  h.type('two');
  await h.outbox.flush();
  assert.equal(h.outbox.confirmed(), 'server');
  assert.equal(h.outbox.outstanding(), 2);
  const [one, two] = h.wire;
  h.outbox.ack(one.seq);
  assert.equal(h.outbox.confirmed(), 'one');
  h.outbox.ack(two.seq);
  assert.equal(h.outbox.confirmed(), 'two');
  assert.equal(h.outbox.outstanding(), 0);
  // Unknown or repeated numbers are ignored.
  h.outbox.ack(two.seq);
  h.outbox.ack(999);
  assert.equal(h.outbox.confirmed(), 'two');
});

test('a refused edit is pending again and reverts to the confirmed text', async () => {
  const h = harness();
  h.outbox.remoteApplied('server');
  h.text = 'server';
  h.type('ghost');
  await h.outbox.flush();
  assert.equal(h.outbox.isPending(), false);
  assert.equal(h.outbox.refused(h.wire[0].seq), true);
  assert.equal(h.outbox.isPending(), true);
  assert.equal(h.outbox.revert(), 'server');
  assert.equal(h.outbox.isPending(), false);
  assert.equal(h.outbox.outstanding(), 0);
  // Replies to edits dropped by the revert are no longer ours.
  assert.equal(h.outbox.refused(h.wire[0].seq), false);
});

test('a rejected edit is re-sent by the next flush even if the text is unchanged', async () => {
  const h = harness();
  h.type('too big');
  await h.outbox.flush();
  assert.equal(h.outbox.refused(h.wire[0].seq), true);
  await h.outbox.flush();
  assert.deepEqual(h.wire.map((w) => w.wire), ['enc(too big)', 'enc(too big)']);
  assert.notEqual(h.wire[0].seq, h.wire[1].seq);
});

test('edits a dropped connection never answered are re-sent after reconnecting', async () => {
  const h = harness();
  h.type('in flight');
  await h.outbox.flush();
  h.outbox.connectionLost();
  assert.equal(h.outbox.isPending(), true, 'the next snapshot must not overwrite it');
  assert.equal(h.outbox.outstanding(), 0);
  // The new connection's snapshot is older than the edit; the editor keeps
  // the edit and flushes it again.
  h.outbox.remoteSeen('before');
  assert.equal(h.outbox.confirmed(), 'before');
  assert.equal(h.outbox.isPending(), true);
  await h.outbox.flush();
  assert.deepEqual(h.wire.map((w) => w.wire), ['enc(in flight)', 'enc(in flight)']);
});

test('a dropped connection with every edit answered changes nothing', async () => {
  const h = harness();
  h.type('saved');
  await h.outbox.flush();
  h.outbox.ack(h.wire[0].seq);
  h.outbox.connectionLost();
  assert.equal(h.outbox.isPending(), false);
});

test('remote content matching the editor settles pending state', () => {
  const h = harness();
  h.type('same');
  h.outbox.remoteSeen('same');
  assert.equal(h.outbox.isPending(), false);
  assert.equal(h.outbox.confirmed(), 'same');
});

test('a failing encrypt transmits nothing and keeps the text pending', async () => {
  const h = harness({ encode: async () => { throw new Error('no key'); } });
  h.type('secret');
  assert.equal(await h.outbox.flush(), false);
  assert.equal(h.wire.length, 0);
  assert.equal(h.outbox.isPending(), true);
});
