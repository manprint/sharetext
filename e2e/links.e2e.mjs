import { test, before, after } from 'node:test';
import assert from 'node:assert/strict';
import { LinkifyIt } from 'linkify-it';
import * as linkifyjs from 'linkifyjs';
import { ALPHABET, KEY_CHARS, CHECK_CHARS } from '../cmd/server/static/share-link.js';
import {
  BASE, launch, decryptWith, waitEditable, waitContent, waitStored,
  createSession, uniqueName, shareLink,
} from './helpers.mjs';

const TEXT = 'link data';
let browser;
let session; // { slug, key, url } of a link-key session holding TEXT

const reEscape = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
const waitHref = (page, href) =>
  page.waitForFunction((h) => location.href === h, href, { timeout: 10000 });

async function makeSession(prefix, text, opts = {}) {
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  const s = await createSession(page, { name: uniqueName(prefix), ...opts });
  await waitEditable(page);
  await page.click('#content');
  await page.keyboard.type(text);
  if (s.key) await waitStored(s.slug, async (c) => (await decryptWith(s.key, c)) === text);
  else await page.waitForTimeout(1500);
  return { ...s, ctx, page };
}

async function fresh(url) {
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  const calls = [];
  page.on('request', (r) => { if (/\/(api|ws)\//.test(r.url())) calls.push(r.url()); });
  await page.goto(url);
  return { ctx, page, calls };
}

before(async () => {
  browser = await launch();
  const s = await makeSession('e2e-links', TEXT);
  await s.ctx.close();
  session = { slug: s.slug, key: s.key, url: s.url };
});

after(async () => { await browser?.close(); });

test('creation lands on /s/#!<slug><key><check>, letters and digits only after the slug', () => {
  const tail = `[${ALPHABET}]{${KEY_CHARS + CHECK_CHARS}}`;
  assert.match(session.url, new RegExp(`^${reEscape(BASE)}/s/#!${reEscape(session.slug)}${tail}$`));
  assert.equal(session.url, shareLink(session.slug, session.key));
});

test('a bookmarked share link opens the session in a fresh browser', async () => {
  const { ctx, page } = await fresh(session.url);
  assert.ok(await waitContent(page, TEXT));
  await waitEditable(page);
  assert.equal(page.url(), session.url);
  assert.equal(await page.title(), `sharetext · ${session.slug}`);
  assert.equal(await page.getAttribute('link[rel="manifest"]', 'href'), `/manifest/session/${session.slug}.webmanifest`);
  await ctx.close();
});

test('copy/paste debris around the link is tolerated and dropped from the address', async () => {
  const url = session.url;
  const mid = url.length - 20;
  const variants = {
    'trailing dot': url + '.',
    'trailing ).': url + ').',
    'trailing >': url + '>',
    'trailing space': url + '%20',
    'escaped !': url.replace('#!', '#%21'),
    'line break inside': url.slice(0, mid) + '%0A' + url.slice(mid),
    'zero-width space inside': url.slice(0, mid) + '%E2%80%8B' + url.slice(mid),
    'slash before # dropped': url.replace('/s/#!', '/s#!'),
  };
  for (const [name, variant] of Object.entries(variants)) {
    const { ctx, page } = await fresh(variant);
    assert.ok(await waitContent(page, TEXT), name);
    await waitHref(page, url).catch(() => assert.fail(`${name}: address ${page.url()}`));
    await ctx.close();
  }
});

test('a truncated or altered link says so and never opens an editor', async () => {
  const url = session.url;
  const i = url.length - 20;
  const other = url[i] === 'A' ? 'B' : 'A';
  const variants = {
    'last char cut': url.slice(0, -1),
    'checksum cut': url.slice(0, -CHECK_CHARS),
    'half key': url.slice(0, -(KEY_CHARS / 2)),
    'slug only': `${BASE}/s/#!${session.slug}`,
    'altered char': url.slice(0, i) + other + url.slice(i + 1),
  };
  for (const [name, variant] of Object.entries(variants)) {
    const { ctx, page, calls } = await fresh(variant);
    await page.waitForSelector('#expired-overlay:not([hidden])', { timeout: 10000 });
    assert.equal(await page.textContent('#expired-overlay h2'), 'Link incompleto', name);
    assert.equal(await page.isDisabled('#content'), true, name);
    await page.waitForTimeout(300);
    assert.deepEqual(calls, [], `${name}: no session request`);
    const pins = await page.evaluate(() => Object.keys(localStorage).filter((k) => k.startsWith('sharetext.pin:')));
    assert.deepEqual(pins, [], `${name}: nothing remembered`);
    await ctx.close();
  }
});

test('/s/ without the address explains what is missing; /s redirects there', async () => {
  for (const path of ['/s/', '/s']) {
    const { ctx, page } = await fresh(`${BASE}${path}`);
    await page.waitForSelector('#expired-overlay:not([hidden])', { timeout: 10000 });
    assert.equal(page.url(), `${BASE}/s/`);
    assert.match(await page.textContent('#expired-overlay p'), /manca la parte dopo/);
    await ctx.close();
  }
});

test('a share link opened before reopens offline, read-only', async () => {
  const { ctx, page } = await fresh(session.url);
  assert.ok(await waitContent(page, TEXT));
  await page.waitForFunction(() => navigator.serviceWorker.controller != null, null, { timeout: 10000 });
  await page.reload();
  assert.ok(await waitContent(page, TEXT));
  await ctx.setOffline(true);
  await page.reload();
  assert.ok(await waitContent(page, TEXT));
  await page.waitForSelector('#offline-banner:not([hidden])', { timeout: 10000 });
  assert.equal(page.url(), session.url);
  await ctx.close();
});

test('the server refuses the _escaped_fragment_ crawler form of a share link', async () => {
  const frag = new URL(session.url).hash.slice(2);
  const r = await fetch(`${BASE}/s/?_escaped_fragment_=${frag}`);
  assert.equal(r.status, 404);
  assert.ok(!(await r.text()).includes(frag));
});

test('a legacy #k= link is rewritten to the share link, and "Copia link" copies it whole', async () => {
  const ctx = await browser.newContext();
  await ctx.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: BASE });
  const page = await ctx.newPage();
  await page.goto(`${BASE}/s/${session.slug}#k=${session.key}`);
  assert.ok(await waitContent(page, TEXT));
  await waitHref(page, session.url);
  await page.click('#copy-link');
  await page.waitForFunction(async (u) => (await navigator.clipboard.readText()) === u, session.url, { timeout: 5000 })
    .catch(async () => assert.fail(`clipboard: ${await page.evaluate(() => navigator.clipboard.readText())}`));
  await ctx.close();
});

test('following another share link in the same tab loads that session', async () => {
  const b = await makeSession('e2e-links-b', 'other data');
  await b.ctx.close();
  const { ctx, page } = await fresh(session.url);
  assert.ok(await waitContent(page, TEXT));
  await page.evaluate((u) => { location.href = u; }, b.url);
  assert.ok(await waitContent(page, 'other data'));
  assert.equal(page.url(), b.url);
  await ctx.close();
});

test('the home-screen launcher reopens link sessions by link, password sessions without key', async () => {
  const { ctx, page } = await fresh(session.url);
  assert.ok(await waitContent(page, TEXT));
  await page.goto(`${BASE}/launch/${session.slug}`);
  await page.waitForURL((u) => u.href === session.url);
  assert.ok(await waitContent(page, TEXT));

  const pass = 'correct horse battery';
  const p = await createSession(page, { name: uniqueName('e2e-links-pw'), passphrase: pass });
  await waitEditable(page);
  assert.equal(p.url, `${BASE}/s/${p.slug}`);
  await page.click('#content');
  await page.keyboard.type('pw data');
  await page.waitForTimeout(1500);
  await page.goto(`${BASE}/launch/${p.slug}`);
  await page.waitForURL((u) => u.href === `${BASE}/s/${p.slug}`);
  assert.ok(await waitContent(page, 'pw data'));
  assert.equal(page.url(), `${BASE}/s/${p.slug}`, 'the password key stays out of the address');
  await ctx.close();
});

test('link detection used by chat and mail apps keeps the whole share link', () => {
  const url = 'https://sharetext.example.com/s/' + new URL(session.url).hash;
  const contexts = [
    `vedi ${url}`, `vedi ${url}.`, `(${url})`, `${url}!`, `link: ${url}, grazie`,
    `"${url}"`, `<${url}>`, `${url}?`, `[${url}]`, `${url}\nciao`,
  ];
  const li = new LinkifyIt();
  for (const text of contexts) {
    assert.deepEqual((li.match(text) || []).map((m) => m.url), [url], `linkify-it: ${text}`);
    assert.deepEqual(linkifyjs.find(text).map((m) => m.href), [url], `linkifyjs: ${text}`);
  }
});
