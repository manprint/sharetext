/* Online/offline guard: wraps navigator.onLine + window events into a small
 * state machine whose transitions are deduplicated, so a flapping radio does
 * not spam the UI. Pure helpers are exported for node:test.
 */

export function createOfflineState(initialOnline) {
  let online = !!initialOnline;
  return {
    isOnline() { return online; },
    // Returns true iff the state actually changed; lets the caller decide
    // whether to fire a transition handler.
    setOnline(next) {
      const v = !!next;
      if (v === online) return false;
      online = v;
      return true;
    },
  };
}

export function initOfflineGuard({ onOnline, onOffline } = {}) {
  const state = createOfflineState(
    typeof navigator === "undefined" ? true : navigator.onLine !== false
  );
  function notify(nextOnline) {
    if (!state.setOnline(nextOnline)) return;
    if (nextOnline) { if (onOnline) onOnline(); }
    else { if (onOffline) onOffline(); }
  }
  if (typeof window !== "undefined") {
    window.addEventListener("online", () => notify(true));
    window.addEventListener("offline", () => notify(false));
  }
  // Fire initial offline callback if we boot already offline, so the UI is
  // consistent with the network state on first paint.
  if (!state.isOnline() && onOffline) {
    queueMicrotask(() => onOffline());
  }
  return {
    isOnline: () => state.isOnline(),
    // Test/manual hook for forcing a transition without dispatching events.
    notify,
  };
}

export function setOfflineBanner(visible, text) {
  if (typeof document === "undefined") return;
  const el = document.getElementById("offline-banner");
  if (!el) return;
  if (text != null) el.textContent = text;
  el.hidden = !visible;
  document.body.classList.toggle("offline", !!visible);
}
