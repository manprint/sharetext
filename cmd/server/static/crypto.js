/* End-to-end encryption helpers built on the Web Crypto API.
 *
 * Wire format: `enc:v1:<base64url-iv>:<base64url-ciphertext>` for text.
 *              Raw `[iv(12)|ciphertext+tag]` Uint8Array for binary blobs.
 *
 * Server never sees the key — it lives in the URL fragment (`#k=…`), which
 * the browser refuses to forward in HTTP requests.
 */

export const ENC_PREFIX = "enc:v1:";
const IV_BYTES = 12;

const subtle =
  (typeof crypto !== "undefined" && crypto.subtle) ||
  (typeof globalThis !== "undefined" && globalThis.crypto && globalThis.crypto.subtle);

function getRandomValues(buf) {
  // Browsers + Node 24 expose `crypto.getRandomValues`. Tests run in Node ESM
  // where `crypto` is the global; we look it up lazily to avoid hard-failing
  // when a polyfill is present.
  const c =
    (typeof crypto !== "undefined" && crypto) ||
    (typeof globalThis !== "undefined" && globalThis.crypto);
  c.getRandomValues(buf);
  return buf;
}

export function b64urlEncode(bytes) {
  // bytes: Uint8Array → base64url (no padding).
  let bin = "";
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
}

export function b64urlDecode(str) {
  let s = str.replace(/-/g, "+").replace(/_/g, "/");
  // Restore '=' padding (atob is strict on length).
  const mod = s.length % 4;
  if (mod === 2) s += "==";
  else if (mod === 3) s += "=";
  else if (mod === 1) throw new Error("invalid base64url length");
  const bin = atob(s);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

export async function generateKey() {
  return subtle.generateKey({ name: "AES-GCM", length: 256 }, true, ["encrypt", "decrypt"]);
}

export async function exportKey(key) {
  const raw = new Uint8Array(await subtle.exportKey("raw", key));
  return b64urlEncode(raw);
}

export async function importKey(b64urlRaw) {
  const raw = b64urlDecode(b64urlRaw);
  if (raw.length !== 32) throw new Error("expected 256-bit key");
  return subtle.importKey("raw", raw, { name: "AES-GCM" }, true, ["encrypt", "decrypt"]);
}

export async function encryptText(key, plaintext) {
  const iv = getRandomValues(new Uint8Array(IV_BYTES));
  const buf = new TextEncoder().encode(plaintext);
  const ct = new Uint8Array(await subtle.encrypt({ name: "AES-GCM", iv }, key, buf));
  return ENC_PREFIX + b64urlEncode(iv) + ":" + b64urlEncode(ct);
}

export async function decryptText(key, payload) {
  if (!isCiphertext(payload)) throw new Error("not encrypted with this scheme");
  const rest = payload.slice(ENC_PREFIX.length);
  const sep = rest.indexOf(":");
  if (sep <= 0) throw new Error("malformed ciphertext envelope");
  const iv = b64urlDecode(rest.slice(0, sep));
  const ct = b64urlDecode(rest.slice(sep + 1));
  const plain = await subtle.decrypt({ name: "AES-GCM", iv }, key, ct);
  return new TextDecoder().decode(plain);
}

export async function encryptBytes(key, bytes) {
  const iv = getRandomValues(new Uint8Array(IV_BYTES));
  const ct = new Uint8Array(await subtle.encrypt({ name: "AES-GCM", iv }, key, bytes));
  const out = new Uint8Array(IV_BYTES + ct.length);
  out.set(iv, 0);
  out.set(ct, IV_BYTES);
  return out;
}

export async function decryptBytes(key, bytes) {
  if (bytes.length < IV_BYTES + 16) throw new Error("ciphertext too short");
  const iv = bytes.subarray(0, IV_BYTES);
  const ct = bytes.subarray(IV_BYTES);
  const plain = await subtle.decrypt({ name: "AES-GCM", iv }, key, ct);
  return new Uint8Array(plain);
}

export function isCiphertext(value) {
  return typeof value === "string" && value.startsWith(ENC_PREFIX);
}

// Encrypt only the filename portion of a file marker; the marker id is left
// untouched because the server still needs to match `[file:<id>:` to GC
// orphaned files. base64url is safe inside the `[A-Za-z0-9_-]` slot.
export async function encryptName(key, plaintextName) {
  const iv = getRandomValues(new Uint8Array(IV_BYTES));
  const ct = new Uint8Array(
    await subtle.encrypt({ name: "AES-GCM", iv }, key, new TextEncoder().encode(plaintextName)),
  );
  return b64urlEncode(iv) + "." + b64urlEncode(ct);
}

export async function decryptName(key, encodedName) {
  const dot = encodedName.indexOf(".");
  if (dot <= 0) throw new Error("malformed encrypted name");
  const iv = b64urlDecode(encodedName.slice(0, dot));
  const ct = b64urlDecode(encodedName.slice(dot + 1));
  const plain = await subtle.decrypt({ name: "AES-GCM", iv }, key, ct);
  return new TextDecoder().decode(plain);
}

export function isEncryptedName(encodedName) {
  // Heuristic: encrypted names have `<b64url>.<b64url>` shape with a single
  // dot separator and no whitespace. Plaintext URL-encoded filenames may
  // contain `%`, `(`, `)`, `,`, `+` etc. that base64url forbids.
  if (typeof encodedName !== "string") return false;
  const dot = encodedName.indexOf(".");
  if (dot <= 0 || dot === encodedName.length - 1) return false;
  return /^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$/.test(encodedName);
}
