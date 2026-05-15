import { test } from "node:test";
import assert from "node:assert/strict";
import { createOfflineState } from "./offline-guard.js";

test("initial state reflects constructor arg", () => {
  assert.equal(createOfflineState(true).isOnline(), true);
  assert.equal(createOfflineState(false).isOnline(), false);
  assert.equal(createOfflineState(undefined).isOnline(), false);
});

test("setOnline returns true only on actual transition", () => {
  const s = createOfflineState(true);
  assert.equal(s.setOnline(true), false, "no-op when already online");
  assert.equal(s.setOnline(false), true, "transition online→offline");
  assert.equal(s.setOnline(false), false, "no-op when already offline");
  assert.equal(s.setOnline(true), true, "transition offline→online");
});

test("setOnline coerces truthy/falsy inputs", () => {
  const s = createOfflineState(false);
  assert.equal(s.setOnline(1), true);
  assert.equal(s.isOnline(), true);
  assert.equal(s.setOnline(0), true);
  assert.equal(s.isOnline(), false);
});
