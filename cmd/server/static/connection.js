/* WebSocket lifecycle for the editor. DOM-free (the socket factory and the
 * timers are injected) so node:test can drive it.
 *
 * At most one socket is alive at a time: connect() does nothing while a
 * socket exists or a reconnect is being decided, and events from a socket
 * that is no longer the current one are ignored, so a late "close" can never
 * tear down (or double) its successor. Reconnects back off exponentially and
 * are preceded by shouldReconnect(), which lets the app stop for good (e.g.
 * the session is gone).
 */

const OPEN = 1; // WebSocket.OPEN

export function reconnectDelayMs(attempt, { baseMs = 1500, maxMs = 15000 } = {}) {
  const n = Number.isFinite(attempt) && attempt > 0 ? Math.floor(attempt) : 0;
  return Math.min(maxMs, baseMs * 2 ** Math.min(n, 16));
}

export function createConnection({
  open,
  schedule,
  cancel,
  shouldReconnect = async () => true,
  onOpen,
  onMessage,
  onClose,
  delay = reconnectDelayMs,
}) {
  let socket = null;
  let timer = null;
  let deciding = false;
  let kicked = false;
  let attempt = 0;
  let stopped = false;

  function clearTimer() {
    if (timer !== null) {
      cancel(timer);
      timer = null;
    }
  }

  function connect() {
    if (stopped) return;
    if (deciding) {
      // Reconnect as soon as the pending decision allows, skipping the delay.
      kicked = true;
      return;
    }
    if (socket) return;
    clearTimer();
    let s;
    try {
      s = open();
    } catch {
      retry();
      return;
    }
    socket = s;
    s.addEventListener('open', () => {
      if (socket !== s) return;
      attempt = 0;
      if (onOpen) onOpen(s);
    });
    s.addEventListener('message', (ev) => {
      if (socket !== s) return;
      if (onMessage) onMessage(ev, s);
    });
    s.addEventListener('error', () => {
      if (socket !== s) return;
      try { s.close(); } catch {}
    });
    s.addEventListener('close', (ev) => {
      if (socket !== s) return;
      socket = null;
      if (onClose) onClose(ev);
      retry();
    });
  }

  async function retry() {
    if (stopped) return;
    deciding = true;
    kicked = false;
    let again;
    try {
      again = await shouldReconnect();
    } catch {
      again = true;
    }
    deciding = false;
    if (stopped) return;
    if (!again) {
      stopped = true;
      return;
    }
    if (kicked) {
      connect();
      return;
    }
    timer = schedule(() => {
      timer = null;
      connect();
    }, delay(attempt));
    attempt += 1;
  }

  return {
    /** Open a socket now unless one is alive; also cuts a reconnect delay short. */
    connect,
    /** Close for good: no more sockets, no more reconnects. */
    stop() {
      stopped = true;
      clearTimer();
      const s = socket;
      socket = null;
      if (s) {
        try { s.close(); } catch {}
      }
    },
    /** The open socket, or null. */
    openSocket() {
      return socket && socket.readyState === OPEN ? socket : null;
    },
    isStopped() {
      return stopped;
    },
  };
}
