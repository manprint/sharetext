/* Landing-page form handler. Extracted from an inline <script> so the
 * page-wide CSP can drop `'unsafe-inline'`. Also bootstraps the per-session
 * AES-256-GCM key and tucks it into the URL fragment before redirecting,
 * so the resulting session is end-to-end encrypted by default.
 */
import { generateKey, exportKey, deriveKeyFromPassphrase, encryptText, MIN_PASSPHRASE_LENGTH } from './crypto.js';
import { savePinnedSession } from './pinning.js';
import { initOfflineGuard, setOfflineBanner } from './offline-guard.js';

const form = document.getElementById('create-form');
if (form) {
  const err = document.getElementById('error');
  const modes = form.querySelectorAll('input[name="type"]');
  const fields = form.querySelectorAll('.field[data-show-for]');

  function syncMode() {
    const sel = form.querySelector('input[name="type"]:checked').value;
    fields.forEach((f) => {
      const active = f.dataset.showFor === sel;
      f.hidden = !active;
      f.querySelectorAll('input').forEach((inp) => { inp.disabled = !active; });
    });
  }
  modes.forEach((m) => m.addEventListener('change', syncMode));
  syncMode();

  form.addEventListener('submit', async (ev) => {
    ev.preventDefault();
    err.hidden = true;
    const type = form.querySelector('input[name="type"]:checked').value;
    const payload = { type };
    if (type === 'persistent') {
      const name = document.getElementById('name').value.trim();
      if (!/^[A-Za-z0-9_-]{1,32}$/.test(name)) {
        err.textContent = 'Nome non valido. Usa 1-32 caratteri tra lettere, numeri, "-", "_".';
        err.hidden = false;
        return;
      }
      payload.name = name;
    } else {
      const minutes = parseInt(document.getElementById('minutes').value, 10);
      if (!Number.isInteger(minutes) || minutes < 1 || minutes > 10080) {
        err.textContent = 'Durata non valida. Inserisci un numero di minuti fra 1 e 10080.';
        err.hidden = false;
        return;
      }
      payload.minutes = minutes;
    }
    const passphrase = document.getElementById('passphrase').value;
    const passphrase2 = document.getElementById('passphrase2').value;
    if (passphrase || passphrase2) {
      if (passphrase.length < MIN_PASSPHRASE_LENGTH) {
        err.textContent = `Password troppo corta: minimo ${MIN_PASSPHRASE_LENGTH} caratteri.`;
        err.hidden = false;
        return;
      }
      if (passphrase !== passphrase2) {
        err.textContent = 'Le due password non coincidono.';
        err.hidden = false;
        return;
      }
    }
    try {
      // Generate the per-session key BEFORE the network round-trip so a
      // slow Web Crypto call doesn't make the redirect feel laggy.
      let keyFragment = '';
      if (!passphrase) try {
        const key = await generateKey();
        const raw = await exportKey(key);
        keyFragment = '#k=' + raw;
      } catch (cryptoErr) {
        // If Web Crypto is unavailable (very old browser, insecure context),
        // fall back to the legacy plaintext flow rather than break creation.
        console.warn('E2E key generation unavailable, falling back to plaintext session', cryptoErr);
      }

      const res = await fetch('/api/sessions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });
      if (!res.ok) {
        const msg = (await res.text()).trim() || 'creazione fallita';
        throw new Error(msg);
      }
      const data = await res.json();
      if (passphrase) {
        // The salt is the slug, so the key can only be derived now. Seed an
        // encrypted empty body at once: an empty keyless session would be
        // treated as plaintext by the first peer who opens the bare link.
        const key = await deriveKeyFromPassphrase(passphrase, data.slug);
        const seed = await fetch('/api/sessions/' + encodeURIComponent(data.slug), {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ content: await encryptText(key, '') }),
        });
        if (!seed.ok) throw new Error('inizializzazione cifratura fallita (' + seed.status + ')');
        savePinnedSession(window.localStorage, data.slug, await exportKey(key));
      }
      window.location.href = data.url + keyFragment;
    } catch (e) {
      err.textContent = e.message;
      err.hidden = false;
    }
  });
}

initOfflineGuard({
  onOnline:  () => setOfflineBanner(false),
  onOffline: () => setOfflineBanner(true, 'Offline'),
});

if ('serviceWorker' in navigator) {
  window.addEventListener('load', () => {
    navigator.serviceWorker.register('/sw.js').catch(() => {});
  });
}
