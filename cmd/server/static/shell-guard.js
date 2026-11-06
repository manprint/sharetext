/* A service worker from an older release can serve its own cached app.js on
 * the share page (/s/#!…), which that code cannot read. The new worker takes
 * over right after; reload once onto the current code when that happens.
 * app.js sets window.__sharetextApp, so current code is never reloaded. */
(function () {
  if (location.pathname !== '/s/' || !('serviceWorker' in navigator) || !navigator.serviceWorker.controller) return;
  navigator.serviceWorker.addEventListener('controllerchange', function () {
    if (window.__sharetextApp) return;
    try {
      if (sessionStorage.getItem('sharetext.swreload')) return;
      sessionStorage.setItem('sharetext.swreload', '1');
    } catch (e) {}
    location.reload();
  });
})();
