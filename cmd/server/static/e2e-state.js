/* Pure decision helpers for the end-to-end-encryption state machine.
 *
 * Split out of app.js so the classification logic can be exhaustively
 * unit-tested without a DOM. The runtime still owns side-effects
 * (mutating sessionMode, applying the banner, calling Web Crypto); this
 * module just maps inputs to decisions.
 */

import { isCiphertext, ENC_PREFIX } from "./crypto.js";

export const MODE_PENDING = "pending";
export const MODE_PLAIN = "plain";
export const MODE_E2E = "e2e";
export const MODE_LOCKED = "locked";

/**
 * Decide the session mode at first snapshot.
 *
 *   ciphertext + key   → e2e
 *   ciphertext + no key→ locked (banner up, editor readonly)
 *   empty      + key   → e2e   (we are the first writer; outgoing will encrypt)
 *   anything else      → plain (legacy session, no key on the wire)
 */
export function decideInitialMode(initialContent, hasKey) {
  if (isCiphertext(initialContent)) {
    return hasKey ? MODE_E2E : MODE_LOCKED;
  }
  if (initialContent === "" && hasKey) {
    return MODE_E2E;
  }
  return MODE_PLAIN;
}

/**
 * Classify an incoming WS/REST payload against the current crypto state.
 *
 * Returns one of:
 *   { kind: "plain", plain, nextMode }           — content is not encrypted; render as-is
 *   { kind: "decrypt-attempt", nextMode: "e2e" } — caller should run decryptText and check result
 *   { kind: "locked", nextMode: "locked" }       — encrypted but no key; refuse to render
 *
 * `currentMode` is forwarded back as `nextMode` only for the "plain" kind, so the caller
 * can compare and decide whether a UI refresh is needed.
 */
export function classifyIncoming(rawContent, hasKey, currentMode) {
  if (!isCiphertext(rawContent)) {
    return { kind: "plain", plain: rawContent, nextMode: currentMode };
  }
  if (hasKey) {
    return { kind: "decrypt-attempt", nextMode: MODE_E2E };
  }
  return { kind: "locked", nextMode: MODE_LOCKED };
}

/**
 * Defensive guard: a value the runtime is about to write into the editor
 * must NEVER start with the ciphertext prefix. If it does, decryption
 * silently failed (or never ran) and we are one log-line away from leaking
 * raw `enc:v1:…` into the UI. This regression bit us once in production —
 * see the e2e-state.test.mjs cases that pin it.
 */
export function isSafePlaintext(value) {
  if (typeof value !== "string") return false;
  return !value.startsWith(ENC_PREFIX);
}
