import { test, before, after } from 'node:test';
import assert from 'node:assert/strict';
import {
  BASE, launch, randomKey, apiContent, decryptWith, editorState, waitEditable,
  waitContent, waitStored, createSession, uniqueName,
} from './helpers.mjs';

let browser;
let session; // { slug, key } of a link-key session holding 'top secret'
const wrongKey = randomKey();

before(async () => {
  browser = await launch();
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  session = await createSession(page, { name: uniqueName('e2e-keys') });
  await waitEditable(page);
  session.seeded = await apiContent(session.slug);
  await page.click('#content');
  await page.keyboard.type('top secret');
  await waitStored(session.slug, async (c) => (await decryptWith(session.key, c)) === 'top secret');
  await ctx.close(); // closing releases the creator's editor lock
});

after(async () => { await browser?.close(); });

test('link-key sessions are seeded encrypted and edits stored as ciphertext', async () => {
  assert.ok(session.key, 'creation redirects to a link with the key');
  assert.equal(await decryptWith(session.key, session.seeded), '', 'seeded with encrypted empty text');
  const content = await apiContent(session.slug);
  assert.match(content, /^enc:v1:/);
  assert.equal(await decryptWith(session.key, content), 'top secret');
});

test('a wrong #k= key locks the session and cannot overwrite it', async () => {
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  await page.goto(`${BASE}/s/${session.slug}#k=${wrongKey}`);
  await page.waitForSelector('#unlock-form:not([hidden])', { timeout: 10000 });
  const st = await editorState(page);
  assert.equal(st.readOnly, true);
  assert.equal(st.value, '');
  const pin = await page.evaluate((s) => localStorage.getItem(`sharetext.pin:${s}`), session.slug);
  assert.ok(!pin || !JSON.parse(pin).key, 'the wrong key must not be remembered');
  await page.click('#content').catch(() => {});
  await page.keyboard.type('OVERWRITE');
  await page.waitForTimeout(1000);
  assert.equal(await decryptWith(session.key, await apiContent(session.slug)), 'top secret');
  await ctx.close();
});

test('a wrong #k= key falls back to the key remembered on this device', async () => {
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  await page.goto(`${BASE}/s/${session.slug}#k=${session.key}`);
  assert.ok(await waitContent(page, 'top secret'));
  await page.goto(`${BASE}/s/${session.slug}#k=${wrongKey}`);
  await page.reload();
  assert.ok(await waitContent(page, 'top secret'));
  const pin = await page.evaluate((s) => JSON.parse(localStorage.getItem(`sharetext.pin:${s}`)).key, session.slug);
  assert.equal(pin, session.key);
  await ctx.close();
});

test('the bare link without a key is read-only', async () => {
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  await page.goto(`${BASE}/s/${session.slug}`);
  await page.waitForSelector('#decrypt-banner:not([hidden])');
  await page.waitForTimeout(500);
  const st = await editorState(page);
  assert.equal(st.readOnly, true);
  assert.equal(st.value, '');
  await ctx.close();
});

test('password sessions unlock with the password and drop a stale #k=', async () => {
  const pass = 'correct horse battery';
  const c1 = await browser.newContext();
  const p1 = await c1.newPage();
  const { slug } = await createSession(p1, { name: uniqueName('e2e-pass'), passphrase: pass });
  await waitEditable(p1);
  const seeded = await apiContent(slug);
  assert.match(seeded, /^enc:v1:/, 'password sessions are seeded encrypted');
  await p1.click('#content');
  await p1.keyboard.type('pw data');
  await waitStored(slug, async (c) => c !== seeded);
  await p1.waitForTimeout(500);
  await c1.close();

  const c2 = await browser.newContext();
  const p2 = await c2.newPage();
  await p2.goto(`${BASE}/s/${slug}#k=${wrongKey}`);
  await p2.waitForSelector('#unlock-form:not([hidden])', { timeout: 10000 });
  await p2.fill('#unlock-pass', 'not the password');
  await p2.press('#unlock-pass', 'Enter');
  await p2.waitForSelector('#unlock-error:not([hidden])');
  await p2.fill('#unlock-pass', pass);
  await p2.press('#unlock-pass', 'Enter');
  assert.ok(await waitContent(p2, 'pw data'));
  assert.equal(p2.url(), `${BASE}/s/${slug}`, 'a password key never shows up in the address');
  await c2.close();
});
