// Shared setup for the browser end-to-end suite. Configuration (env):
//   SHARETEXT_URL  server under test (default http://127.0.0.1:8080)
//   CHROME_PATH    Chromium binary; default: the one `playwright-core install` fetched
//   PW_PROXY       HTTP proxy for the browser (e.g. testing a public endpoint)
//   ADMIN_USER / ADMIN_PASS  admin credentials; admin-only tests skip without them
import { chromium } from 'playwright-core';
import { webcrypto as wc } from 'node:crypto';

export const BASE = (process.env.SHARETEXT_URL || 'http://127.0.0.1:8080').replace(/\/$/, '');
export const ADMIN = process.env.ADMIN_USER && process.env.ADMIN_PASS
  ? { user: process.env.ADMIN_USER, pass: process.env.ADMIN_PASS }
  : null;

export async function launch() {
  return chromium.launch({
    executablePath: process.env.CHROME_PATH || undefined,
    proxy: process.env.PW_PROXY ? { server: process.env.PW_PROXY } : undefined,
  });
}

export const randomKey = () => Buffer.from(wc.getRandomValues(new Uint8Array(32))).toString('base64url');

export async function apiContent(slug) {
  const r = await fetch(`${BASE}/api/sessions/${slug}`);
  if (!r.ok) throw new Error(`GET session ${slug}: ${r.status}`);
  return (await r.json()).content;
}

// decryptWith mirrors crypto.js decryptText: enc:v1:<base64url(iv || ct)>.
export async function decryptWith(b64key, ciphertext) {
  const key = await wc.subtle.importKey('raw', Buffer.from(b64key, 'base64url'), 'AES-GCM', false, ['decrypt']);
  const raw = Buffer.from(ciphertext.slice('enc:v1:'.length), 'base64url');
  const pt = await wc.subtle.decrypt({ name: 'AES-GCM', iv: raw.subarray(0, 12) }, key, raw.subarray(12));
  return new TextDecoder().decode(pt);
}

export const editorState = (page) => page.evaluate(() => {
  const c = document.getElementById('content');
  const f = document.getElementById('unlock-form');
  return { readOnly: c.readOnly, value: c.value, unlockShown: !!f && !f.hidden };
});

export const waitEditable = (page) =>
  page.waitForFunction(() => !document.getElementById('content').readOnly, null, { timeout: 10000 });

export const waitContent = (page, text, timeout = 10000) =>
  page.waitForFunction((t) => document.getElementById('content').value === t, text, { timeout }).then(() => true, () => false);

// waitStored polls the server until the session content satisfies pred.
export async function waitStored(slug, pred, timeout = 10000) {
  const end = Date.now() + timeout;
  for (;;) {
    const content = await apiContent(slug);
    if (await pred(content).catch(() => false)) return content;
    if (Date.now() > end) return content;
    await new Promise((r) => setTimeout(r, 200));
  }
}

// createSession goes through the landing page form, like a user would.
export async function createSession(page, { name, passphrase } = {}) {
  await page.goto(`${BASE}/`);
  await page.fill('#name', name);
  if (passphrase) {
    await page.fill('#passphrase', passphrase);
    await page.fill('#passphrase2', passphrase);
  }
  await page.click('#create-btn');
  await page.waitForURL(/\/s\/[^/]+/);
  const url = new URL(page.url());
  const m = url.hash.match(/k=([A-Za-z0-9_-]+)/);
  return { slug: url.pathname.split('/').pop(), key: m ? m[1] : null };
}

export const uniqueName = (prefix) => `${prefix}-${Date.now().toString(36)}`;
