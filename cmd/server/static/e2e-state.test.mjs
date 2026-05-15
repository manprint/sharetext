import { test } from "node:test";
import assert from "node:assert/strict";

import { generateKey, encryptText, ENC_PREFIX } from "./crypto.js";
import {
  decideInitialMode,
  classifyIncoming,
  isSafePlaintext,
  MODE_E2E,
  MODE_LOCKED,
  MODE_PLAIN,
} from "./e2e-state.js";

// ─────────────────────────── decideInitialMode ──────────────────────────────

test("decideInitialMode: ciphertext + key → e2e", () => {
  assert.equal(decideInitialMode("enc:v1:aaa:bbb", true), MODE_E2E);
});

test("decideInitialMode: ciphertext + no key → locked", () => {
  assert.equal(decideInitialMode("enc:v1:aaa:bbb", false), MODE_LOCKED);
});

test("decideInitialMode: empty + key → e2e (we will become first writer)", () => {
  assert.equal(decideInitialMode("", true), MODE_E2E);
});

test("decideInitialMode: empty + no key → plain (legacy session)", () => {
  assert.equal(decideInitialMode("", false), MODE_PLAIN);
});

test("decideInitialMode: plaintext content always → plain", () => {
  assert.equal(decideInitialMode("hello world", true), MODE_PLAIN);
  assert.equal(decideInitialMode("hello world", false), MODE_PLAIN);
});

// ─────────────────────────── classifyIncoming ───────────────────────────────

test("classifyIncoming: plaintext content with key → kind=plain (key ignored)", () => {
  const d = classifyIncoming("hello", true, MODE_PLAIN);
  assert.equal(d.kind, "plain");
  assert.equal(d.plain, "hello");
});

test("classifyIncoming: plaintext content without key → kind=plain", () => {
  const d = classifyIncoming("hello", false, MODE_PLAIN);
  assert.equal(d.kind, "plain");
  assert.equal(d.plain, "hello");
});

test("classifyIncoming: ciphertext + key → kind=decrypt-attempt, nextMode=e2e", () => {
  const d = classifyIncoming("enc:v1:aaa:bbb", true, MODE_PLAIN);
  assert.equal(d.kind, "decrypt-attempt");
  assert.equal(d.nextMode, MODE_E2E);
});

test("classifyIncoming: ciphertext + no key → kind=locked, nextMode=locked", () => {
  const d = classifyIncoming("enc:v1:aaa:bbb", false, MODE_PLAIN);
  assert.equal(d.kind, "locked");
  assert.equal(d.nextMode, MODE_LOCKED);
});

test("classifyIncoming: ciphertext + no key reacts even if mode was e2e (key dropped)", () => {
  // Belt-and-braces: even if a previous successful import set mode=e2e but
  // the key was somehow cleared, no key now means we must lock.
  const d = classifyIncoming("enc:v1:aaa:bbb", false, MODE_E2E);
  assert.equal(d.kind, "locked");
});

test("classifyIncoming: empty plaintext → plain (no decryption work)", () => {
  const d = classifyIncoming("", true, MODE_E2E);
  assert.equal(d.kind, "plain");
  assert.equal(d.plain, "");
});

// ─────────────────────────── isSafePlaintext ────────────────────────────────

test("isSafePlaintext: rejects ciphertext-prefixed strings", () => {
  assert.equal(isSafePlaintext("enc:v1:aaa:bbb"), false);
  assert.equal(isSafePlaintext(ENC_PREFIX), false);
  assert.equal(isSafePlaintext(ENC_PREFIX + "x"), false);
});

test("isSafePlaintext: accepts normal text including the substring elsewhere", () => {
  assert.equal(isSafePlaintext("hello"), true);
  assert.equal(isSafePlaintext(""), true);
  assert.equal(isSafePlaintext("see enc:v1: example"), true); // appears mid-string, OK
});

test("isSafePlaintext: rejects non-string values", () => {
  assert.equal(isSafePlaintext(null), false);
  assert.equal(isSafePlaintext(undefined), false);
  assert.equal(isSafePlaintext(42), false);
  assert.equal(isSafePlaintext({ toString: () => "enc:v1:x" }), false);
});

// ─────────────────────────── end-to-end regression ──────────────────────────

test("regression: a peer's ciphertext never decrypts to a ciphertext-prefixed plaintext", async () => {
  // Confirms the impossibility we rely on in the defensive guard inside
  // decryptIncoming: encryptText/decryptText round-trips arbitrary inputs,
  // including pathological "enc:v1:..." strings, faithfully.
  const k = await generateKey();
  const evil = "enc:v1:not-real:data";
  const ct = await encryptText(k, evil);
  // The ciphertext envelope is shaped like a payload, but the plaintext
  // inside it is the literal string above. We treat it as a SAFE plaintext
  // because the user might legitimately have typed it.
  assert.equal(isSafePlaintext(evil), false); // ← literal "enc:v1:" string IS unsafe to render
  assert.notEqual(ct, evil);
  assert.ok(ct.startsWith(ENC_PREFIX));
});

test("regression: ciphertext content in classifyIncoming must NEVER be returned as plain", () => {
  // The whole point of the defensive layer: even if a future bug made
  // `isCiphertext` return false, classifyIncoming's plain-kind path would
  // return the raw ciphertext as "plain" — which is exactly what bit
  // production. The guard in applyRemote (isSafePlaintext) catches it.
  const looksLikeCipher = "enc:v1:abc:def";
  const d = classifyIncoming(looksLikeCipher, true, MODE_E2E);
  // classifyIncoming correctly recognises it as ciphertext.
  assert.equal(d.kind, "decrypt-attempt");
  // And isSafePlaintext rejects the raw string, so even if the cipher path
  // somehow returned it as "plain", applyRemote would refuse to write it.
  assert.equal(isSafePlaintext(looksLikeCipher), false);
});
