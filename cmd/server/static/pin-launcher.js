import { resolvePinnedLaunch } from './pinning.js';

const slug = document.body.dataset.slug || '';
const $status = document.getElementById('launch-status');
const $help = document.getElementById('launch-help');
const $open = document.getElementById('launch-open');

function showMissingPinMessage(redirectURL) {
  $status.textContent = 'Sessione pinnata non ancora associata a questo dispositivo.';
  $help.hidden = false;
  $help.textContent = 'Apri almeno una volta il link completo della sessione su questo dispositivo, poi aggiungila di nuovo alla home. In alternativa puoi aprire ora la sessione senza chiave locale.';
  $open.hidden = false;
  $open.href = redirectURL;
}

const resolved = resolvePinnedLaunch(window.localStorage, slug);
if (!resolved.found) {
  showMissingPinMessage(resolved.redirectURL);
} else {
  window.location.replace(resolved.redirectURL);
}