import test from 'node:test';
import assert from 'node:assert/strict';
import { createConnection, reconnectDelayMs } from './connection.js';

class FakeSocket {
  constructor() {
    this.readyState = 0;
    this.listeners = {};
    this.closeCalls = 0;
  }
  addEventListener(type, fn) {
    (this.listeners[type] ||= []).push(fn);
  }
  emit(type, ev = {}) {
    if (type === 'open') this.readyState = 1;
    if (type === 'close') this.readyState = 3;
    for (const fn of this.listeners[type] || []) fn(ev);
  }
  close() {
    this.closeCalls += 1;
  }
}

async function settle() {
  for (let i = 0; i < 20; i++) await Promise.resolve();
}

function harness({ shouldReconnect } = {}) {
  const h = { sockets: [], timers: [], events: [] };
  h.conn = createConnection({
    open: () => {
      const s = new FakeSocket();
      h.sockets.push(s);
      return s;
    },
    schedule: (fn, ms) => {
      const t = { fn, ms, cancelled: false };
      h.timers.push(t);
      return t;
    },
    cancel: (t) => { t.cancelled = true; },
    shouldReconnect,
    onOpen: () => h.events.push('open'),
    onMessage: (ev) => h.events.push(`msg:${ev.data}`),
    onClose: () => h.events.push('close'),
  });
  h.fire = () => {
    const live = h.timers.filter((t) => !t.cancelled && !t.fired);
    assert.equal(live.length, 1, 'expected exactly one pending timer');
    live[0].fired = true;
    live[0].fn();
  };
  return h;
}

test('reconnectDelayMs backs off exponentially up to the cap', () => {
  assert.equal(reconnectDelayMs(0), 1500);
  assert.equal(reconnectDelayMs(1), 3000);
  assert.equal(reconnectDelayMs(2), 6000);
  assert.equal(reconnectDelayMs(3), 12000);
  assert.equal(reconnectDelayMs(4), 15000);
  assert.equal(reconnectDelayMs(100), 15000);
  assert.equal(reconnectDelayMs(-1), 1500);
  assert.equal(reconnectDelayMs(NaN), 1500);
});

test('connect is idempotent while a socket is alive', () => {
  const h = harness();
  h.conn.connect();
  h.conn.connect();
  assert.equal(h.sockets.length, 1);
  h.sockets[0].emit('open');
  h.conn.connect();
  assert.equal(h.sockets.length, 1);
  assert.equal(h.conn.openSocket(), h.sockets[0]);
});

test('a closed socket is replaced once, after the probe and the delay', async () => {
  const h = harness();
  h.conn.connect();
  h.sockets[0].emit('open');
  h.sockets[0].emit('close');
  assert.equal(h.conn.openSocket(), null);
  await settle();
  assert.equal(h.timers.length, 1);
  assert.equal(h.timers[0].ms, 1500);
  // A kick while the delay runs reconnects at once and only once.
  h.conn.connect();
  assert.equal(h.timers[0].cancelled, true);
  h.conn.connect();
  assert.equal(h.sockets.length, 2);
  assert.deepEqual(h.events, ['open', 'close']);
});

test('events from a replaced socket are ignored', async () => {
  const h = harness();
  h.conn.connect();
  const old = h.sockets[0];
  old.emit('open');
  old.emit('close');
  await settle();
  h.fire();
  const current = h.sockets[1];
  current.emit('open');
  // The browser may still deliver late events for the old socket.
  old.emit('message', { data: 'stale' });
  old.emit('error');
  old.emit('close');
  await settle();
  assert.equal(old.closeCalls, 0);
  assert.equal(h.sockets.length, 2, 'a stale close must not spawn a socket');
  assert.equal(h.conn.openSocket(), current);
  current.emit('message', { data: 'fresh' });
  assert.deepEqual(h.events, ['open', 'close', 'open', 'msg:fresh']);
});

test('repeated failures back off and a successful open resets the delay', async () => {
  const h = harness();
  h.conn.connect();
  for (let i = 0; i < 3; i++) {
    h.sockets[i].emit('close'); // never opened
    await settle();
    h.fire();
  }
  assert.deepEqual(h.timers.map((t) => t.ms), [1500, 3000, 6000]);
  h.sockets[3].emit('open');
  h.sockets[3].emit('close');
  await settle();
  assert.equal(h.timers[3].ms, 1500);
});

test('an error closes the current socket', () => {
  const h = harness();
  h.conn.connect();
  h.sockets[0].emit('error');
  assert.equal(h.sockets[0].closeCalls, 1);
});

test('shouldReconnect=false stops reconnecting for good', async () => {
  const h = harness({ shouldReconnect: async () => false });
  h.conn.connect();
  h.sockets[0].emit('open');
  h.sockets[0].emit('close');
  await settle();
  assert.equal(h.timers.length, 0);
  assert.equal(h.conn.isStopped(), true);
  h.conn.connect();
  assert.equal(h.sockets.length, 1);
});

test('a kick during the probe reconnects right after it, without a second socket', async () => {
  let release;
  const h = harness({ shouldReconnect: () => new Promise((r) => { release = r; }) });
  h.conn.connect();
  h.sockets[0].emit('close');
  h.conn.connect();
  h.conn.connect();
  assert.equal(h.sockets.length, 1, 'no socket while the probe is pending');
  release(true);
  await settle();
  assert.equal(h.sockets.length, 2);
  assert.equal(h.timers.length, 0, 'the kick skipped the delay');
});

test('stop closes the socket and cancels a pending reconnect', async () => {
  const h = harness();
  h.conn.connect();
  h.sockets[0].emit('close');
  await settle();
  h.conn.stop();
  assert.equal(h.timers[0].cancelled, true);

  const h2 = harness();
  h2.conn.connect();
  h2.sockets[0].emit('open');
  h2.conn.stop();
  assert.equal(h2.sockets[0].closeCalls, 1);
  h2.sockets[0].emit('close');
  await settle();
  assert.equal(h2.timers.length, 0);
  assert.deepEqual(h2.events, ['open'], 'no close callback after stop');
});

test('stop during the probe prevents the reconnect', async () => {
  let release;
  const h = harness({ shouldReconnect: () => new Promise((r) => { release = r; }) });
  h.conn.connect();
  h.sockets[0].emit('close');
  h.conn.stop();
  release(true);
  await settle();
  assert.equal(h.sockets.length, 1);
  assert.equal(h.timers.length, 0);
});

test('a throwing socket factory is retried later', async () => {
  let fail = true;
  const timers = [];
  const conn = createConnection({
    open: () => {
      if (fail) throw new Error('SecurityError');
      return new FakeSocket();
    },
    schedule: (fn, ms) => { timers.push({ fn, ms }); return timers.length; },
    cancel: () => {},
  });
  conn.connect();
  await settle();
  assert.equal(timers.length, 1);
  fail = false;
  timers[0].fn();
  assert.equal(conn.openSocket(), null); // connecting, not open yet
});
