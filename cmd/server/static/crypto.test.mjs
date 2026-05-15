import { test } from "node:test";
import assert from "node:assert/strict";
import {
  generateKey,
  exportKey,
  importKey,
  encryptText,
  decryptText,
  encryptBytes,
  decryptBytes,
  isCiphertext,
  encryptName,
  decryptName,
  isEncryptedName,
  b64urlEncode,
  b64urlDecode,
  ENC_PREFIX,
} from "./crypto.js";

test("generateKey + export/import roundtrip yields equivalent material", async () => {
  const k1 = await generateKey();
  const raw1 = await exportKey(k1);
  const k2 = await importKey(raw1);
  const raw2 = await exportKey(k2);
  assert.equal(raw1, raw2);
});

test("encrypt/decrypt text roundtrip", async () => {
  const k = await generateKey();
  const ct = await encryptText(k, "hello world — ciao");
  assert.ok(ct.startsWith(ENC_PREFIX));
  assert.equal(await decryptText(k, ct), "hello world — ciao");
});

test("two encrypts of same plaintext produce different IVs and ciphertexts", async () => {
  const k = await generateKey();
  const a = await encryptText(k, "same");
  const b = await encryptText(k, "same");
  assert.notEqual(a, b);
});

test("encryptText('') roundtrips and stays prefixed", async () => {
  const k = await generateKey();
  const ct = await encryptText(k, "");
  assert.ok(ct.startsWith(ENC_PREFIX));
  assert.equal(await decryptText(k, ct), "");
});

test("tampered ciphertext is rejected", async () => {
  const k = await generateKey();
  const ct = await encryptText(k, "secret");
  // Flip one char in the ciphertext portion (after last colon).
  const lastColon = ct.lastIndexOf(":");
  const flipped =
    ct.slice(0, lastColon + 1) + (ct[lastColon + 1] === "A" ? "B" : "A") + ct.slice(lastColon + 2);
  await assert.rejects(decryptText(k, flipped));
});

test("encrypt/decrypt bytes roundtrip", async () => {
  const k = await generateKey();
  const data = new Uint8Array([0xff, 0x00, 0xde, 0xad, 0xbe, 0xef]);
  const enc = await encryptBytes(k, data);
  const dec = await decryptBytes(k, enc);
  assert.deepEqual(Array.from(dec), Array.from(data));
});

test("decryptBytes rejects truncated input", async () => {
  const k = await generateKey();
  await assert.rejects(decryptBytes(k, new Uint8Array(8)));
});

test("isCiphertext discriminates plaintext from ciphertext", () => {
  assert.equal(isCiphertext("enc:v1:abc:def"), true);
  assert.equal(isCiphertext("plain text"), false);
  assert.equal(isCiphertext(""), false);
  assert.equal(isCiphertext(null), false);
  assert.equal(isCiphertext(undefined), false);
});

test("encryptName/decryptName roundtrip with marker-compatible alphabet", async () => {
  const k = await generateKey();
  const enc = await encryptName(k, "report.final.pdf");
  assert.match(enc, /^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/);
  assert.equal(isEncryptedName(enc), true);
  assert.equal(await decryptName(k, enc), "report.final.pdf");
});

test("isEncryptedName rejects URL-encoded plaintext names", () => {
  assert.equal(isEncryptedName("my%20file.txt"), false);
  assert.equal(isEncryptedName("file (1).pdf"), false);
  assert.equal(isEncryptedName("a.b.c"), false);
  assert.equal(isEncryptedName(".foo"), false);
  assert.equal(isEncryptedName("foo."), false);
});

test("base64url helpers roundtrip arbitrary bytes", () => {
  const samples = [
    new Uint8Array([]),
    new Uint8Array([0]),
    new Uint8Array([0xff, 0x00, 0xff]),
    new Uint8Array(Array.from({ length: 256 }, (_, i) => i)),
  ];
  for (const s of samples) {
    const enc = b64urlEncode(s);
    assert.equal(/[+/=]/.test(enc), false, "no standard base64 chars");
    const dec = b64urlDecode(enc);
    assert.deepEqual(Array.from(dec), Array.from(s));
  }
});
