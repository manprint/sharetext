/* Outgoing edit pipeline for the editor. DOM-free so node:test can drive it.
 *
 * Every transmission goes through one serial queue: encryption is
 * asynchronous, and without the queue a later, faster encrypt could put an
 * older text on the wire last (the server would keep the stale one), or a
 * REST fallback could be overtaken by a WebSocket frame sent after it.
 * Each job sends the editor text as it is when the job runs, so a burst of
 * queued flushes collapses into one transmission.
 *
 * Bookkeeping:
 *   pending     — the editor holds text the server has not been sent, or
 *                 refused: remote content must not overwrite it;
 *   lastSent    — the last text handed to a transport (skips no-op sends);
 *   confirmed   — the last text the server is known to hold: a snapshot or
 *                 peer update, an acknowledged edit, or a 2xx PUT. Rolling
 *                 local edits back returns the editor to it;
 *   outstanding — WebSocket edits awaiting their reply, by sequence number.
 *                 The server answers every edit exactly once (ack,
 *                 edit_rejected or lock_denied, echoing the number).
 *
 * deliver(wire, { allowRest, seq }) resolves to 'ws', 'rest', or a falsy
 * value when the text could not be handed over (it stays pending).
 */
export function createOutbox({ readText, encode, deliver }) {
  let chain = Promise.resolve();
  let pending = false;
  let lastSent = '';
  let confirmed = '';
  let seq = 0;
  const outstanding = new Map();

  function forgetUpTo(n) {
    for (const k of outstanding.keys()) {
      if (k <= n) outstanding.delete(k);
    }
  }

  async function transmit(allowRest) {
    const text = readText();
    if (text === lastSent) {
      pending = false;
      return true;
    }
    let wire;
    try {
      wire = await encode(text);
    } catch {
      return false;
    }
    const n = ++seq;
    let via;
    try {
      via = await deliver(wire, { allowRest, seq: n });
    } catch {
      via = null;
    }
    if (!via) return false;
    lastSent = text;
    if (via === 'rest') {
      // The PUT response is the acknowledgement. It also supersedes any WS
      // edit still waiting for a reply: REST is only used without a socket.
      confirmed = text;
      outstanding.clear();
    } else {
      outstanding.set(n, text);
    }
    // The user may have typed while this job was encrypting or delivering.
    if (readText() === text) pending = false;
    return true;
  }

  return {
    /** Queue a transmission of the current editor text. Resolves to true
     * when the text was handed over (or nothing needed sending). */
    flush({ allowRest = false } = {}) {
      const job = chain.then(() => transmit(allowRest));
      chain = job;
      return job;
    },
    /** The user changed the editor text. */
    markDirty() {
      pending = true;
    },
    isPending() {
      return pending;
    },
    confirmed() {
      return confirmed;
    },
    outstanding() {
      return outstanding.size;
    },
    /** The server stored edit `n`. */
    ack(n) {
      const text = outstanding.get(n);
      if (text === undefined) return;
      forgetUpTo(n);
      confirmed = text;
    },
    /** The server refused edit `n` (lock_denied or edit_rejected). Returns
     * true when `n` was ours and unanswered: the editor text is pending
     * again, to be re-sent with the next flush or rolled back. */
    refused(n) {
      if (!outstanding.has(n)) return false;
      forgetUpTo(n);
      lastSent = null;
      pending = true;
      return true;
    },
    /** A remote snapshot/update was written into the editor. */
    remoteApplied(text) {
      lastSent = text;
      confirmed = text;
      pending = false;
    },
    /** A remote snapshot/update arrived but the editor was left alone. */
    remoteSeen(text) {
      confirmed = text;
      if (readText() === text) {
        lastSent = text;
        pending = false;
      }
    },
    /** The connection dropped. Edits it never answered may or may not have
     * reached the server: keep them pending, so they are re-sent once the
     * next connection's snapshot has arrived instead of being overwritten
     * by it. */
    connectionLost() {
      if (outstanding.size === 0) return;
      outstanding.clear();
      lastSent = null;
      pending = true;
    },
    /** Abandon local edits. Returns the text the editor must show. */
    revert() {
      pending = false;
      outstanding.clear();
      lastSent = confirmed;
      return confirmed;
    },
  };
}
