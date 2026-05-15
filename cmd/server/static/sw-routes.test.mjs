import { test } from "node:test";
import assert from "node:assert/strict";
import { classifyRequest } from "./sw-routes.js";

const BASE = "https://example.com";

function cls(path, method = "GET") {
  return classifyRequest({ url: BASE + path, method });
}

test("static assets are cached", () => {
  assert.equal(cls("/static/style.css"), "static-asset");
  assert.equal(cls("/static/app.js"), "static-asset");
  assert.equal(cls("/static/icon-192.png"), "static-asset");
});

test("shells (landing + session) are network-first cacheable", () => {
  assert.equal(cls("/"), "shell");
  assert.equal(cls("/s/abc-123"), "shell");
});

test("session snapshot GET is SWR-eligible", () => {
  assert.equal(cls("/api/sessions/abc-123"), "api-snapshot");
});

test("file metadata listing is passthrough (peer uploads must be reflected immediately)", () => {
  // Critical regression guard: SWR here caused freshly-uploaded attachments
  // to render as "(allegato cifrato)" on peers because the stale empty list
  // was served back to loadFileMeta(). Keep it network-only.
  assert.equal(cls("/api/sessions/abc-123/files"), "passthrough");
});

test("file blob download is cache-first blob", () => {
  assert.equal(cls("/api/sessions/abc-123/files/42"), "api-file-blob");
  assert.equal(cls("/api/sessions/abc-123/files/d4e5f6"), "api-file-blob");
});

test("bundle zip is passthrough (never cached)", () => {
  assert.equal(cls("/api/sessions/abc-123/bundle"), "passthrough");
});

test("session creation POST is passthrough", () => {
  assert.equal(cls("/api/sessions", "POST"), "passthrough");
});

test("any write method goes through untouched", () => {
  for (const m of ["POST", "PUT", "DELETE", "PATCH"]) {
    assert.equal(cls("/api/sessions/abc-123", m), "passthrough");
    assert.equal(cls("/api/sessions/abc-123/files", m), "passthrough");
  }
});

test("admin is always bypassed regardless of method", () => {
  assert.equal(cls("/admin"), "bypass");
  assert.equal(cls("/admin/api/sessions"), "bypass");
  assert.equal(cls("/admin/api/sessions/abc", "DELETE"), "bypass");
});

test("websocket upgrade is passthrough", () => {
  assert.equal(cls("/ws/abc-123"), "passthrough");
});

test("sw.js and manifest never re-enter the cache layer", () => {
  assert.equal(cls("/sw.js"), "passthrough");
  assert.equal(cls("/manifest.webmanifest"), "passthrough");
});

test("healthz is passthrough", () => {
  assert.equal(cls("/healthz"), "passthrough");
});

test("malformed and unknown URLs are passthrough", () => {
  assert.equal(classifyRequest({ url: "", method: "GET" }), "passthrough");
  assert.equal(classifyRequest({ url: null, method: "GET" }), "passthrough");
  assert.equal(cls("/something/else"), "passthrough");
});

test("query string does not change classification", () => {
  assert.equal(cls("/api/sessions/abc-123?nocache=1"), "api-snapshot");
  assert.equal(cls("/static/app.js?v=2"), "static-asset");
});
