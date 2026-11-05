import { test, before, after } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { BASE, ADMIN, launch, waitEditable, createSession, uniqueName } from './helpers.mjs';

let browser;
let tmp;

before(async () => {
  browser = await launch();
  tmp = mkdtempSync(join(tmpdir(), 'sharetext-e2e-'));
});

after(async () => {
  await browser?.close();
  if (tmp) rmSync(tmp, { recursive: true, force: true });
});

// unzip reads every entry of a zip into { name: utf8 text } with python's
// zipfile, so the check does not depend on the app's own zip code.
function unzip(path) {
  const py = 'import sys,zipfile,json;z=zipfile.ZipFile(sys.argv[1]);print(json.dumps({n:z.read(n).decode() for n in z.namelist()}))';
  return JSON.parse(execFileSync('python3', ['-I', '-c', py, path]).toString());
}

test('E2E upload, download-all with a dangling marker, then expiry', async () => {
  const ctx = await browser.newContext({ acceptDownloads: true });
  const page = await ctx.newPage();
  const toasts = [];
  await page.exposeFunction('__e2eToast', (t) => { if (!toasts.includes(t)) toasts.push(t); });
  await page.addInitScript(() => {
    new MutationObserver(() => {
      const el = document.querySelector('.toast');
      if (el && el.textContent) window.__e2eToast(el.textContent);
    }).observe(document, { subtree: true, childList: true, characterData: true });
  });
  const { slug } = await createSession(page, { name: uniqueName('e2e-files') });
  await waitEditable(page);
  await page.click('#content');
  await page.keyboard.type('intro\n');
  await page.setInputFiles('#file-input', { name: 'a:b.txt', mimeType: 'text/plain', buffer: Buffer.from('file body') });
  await page.waitForFunction(
    () => [...document.querySelectorAll('.file-name')].some((e) => e.textContent === 'a:b.txt'),
    null, { timeout: 10000 });

  // A marker whose file does not exist must not abort the client-side zip.
  await page.click('#content');
  await page.keyboard.press('Control+End');
  await page.keyboard.type('\n[file:zzzzzzzzzzzz:aa.bb]\n');
  await page.waitForTimeout(1000);
  const text = await page.$eval('#content', (e) => e.value);

  const [download] = await Promise.all([page.waitForEvent('download'), page.click('#download-all')]);
  const zipPath = join(tmp, 'bundle.zip');
  await download.saveAs(zipPath);
  const entries = unzip(zipPath);
  assert.equal(entries[`${slug}.txt`], text);
  assert.equal(entries['files/a:b.txt'], 'file body', 'attachment decrypted under its plaintext name');
  assert.ok(!toasts.some((t) => /esclus/.test(t)), `unexpected skip toast: ${toasts.join(' | ')}`);

  if (ADMIN) {
    const auth = 'Basic ' + Buffer.from(`${ADMIN.user}:${ADMIN.pass}`).toString('base64');
    const res = await fetch(`${BASE}/admin/api/sessions/${slug}`, { method: 'DELETE', headers: { Authorization: auth } });
    assert.equal(res.status, 200);
    await page.waitForSelector('#expired-overlay:not([hidden])', { timeout: 15000 });
    await page.waitForTimeout(500);
    const st = await page.evaluate(() => ({
      upload: document.getElementById('upload-btn').disabled,
      bundle: document.getElementById('download-all').disabled,
      editor: document.getElementById('content').disabled,
    }));
    assert.deepEqual(st, { upload: true, bundle: true, editor: true });
  }
  await ctx.close();
});
