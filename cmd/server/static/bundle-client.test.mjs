import { test } from "node:test";
import assert from "node:assert/strict";
import { buildZip, uniqueEntryName } from "./bundle-client.js";
import { execFileSync } from "node:child_process";
import { writeFileSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const td = new TextDecoder();
const te = new TextEncoder();

function u16(view, off) { return view.getUint16(off, true); }
function u32(view, off) { return view.getUint32(off, true); }

function parseZip(bytes) {
  // Find EOCD by scanning the tail for the 0x06054b50 signature; the comment
  // is always empty in our builds so it sits at offset (length - 22).
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  const eocdOff = bytes.length - 22;
  assert.equal(u32(view, eocdOff), 0x06054b50, "EOCD signature");
  const count = u16(view, eocdOff + 10);
  const cdSize = u32(view, eocdOff + 12);
  const cdOff = u32(view, eocdOff + 16);

  // Walk central directory.
  const entries = [];
  let p = cdOff;
  while (p < cdOff + cdSize) {
    assert.equal(u32(view, p), 0x02014b50, "central dir signature");
    const method = u16(view, p + 10);
    const crc = u32(view, p + 16);
    const compSize = u32(view, p + 20);
    const uncompSize = u32(view, p + 24);
    const nameLen = u16(view, p + 28);
    const extraLen = u16(view, p + 30);
    const commentLen = u16(view, p + 32);
    const localOff = u32(view, p + 42);
    const name = td.decode(bytes.subarray(p + 46, p + 46 + nameLen));
    entries.push({ name, method, crc, compSize, uncompSize, localOff });
    p += 46 + nameLen + extraLen + commentLen;
  }
  assert.equal(entries.length, count);

  // Resolve each entry's payload via its local header.
  for (const e of entries) {
    assert.equal(u32(view, e.localOff), 0x04034b50, "local header signature for " + e.name);
    const lhNameLen = u16(view, e.localOff + 26);
    const lhExtraLen = u16(view, e.localOff + 28);
    const dataStart = e.localOff + 30 + lhNameLen + lhExtraLen;
    e.data = bytes.subarray(dataStart, dataStart + e.uncompSize);
  }
  return entries;
}

test("buildZip emits a parseable archive with correct entries (STORE)", () => {
  const a = te.encode("hello world\n");
  const b = new Uint8Array([0xff, 0x00, 0x11, 0x22]);
  const zip = buildZip([
    { name: "session.txt", data: a },
    { name: "files/blob.bin", data: b },
  ]);
  const entries = parseZip(zip);
  assert.equal(entries.length, 2);
  const byName = Object.fromEntries(entries.map((e) => [e.name, e]));
  assert.ok(byName["session.txt"]);
  assert.ok(byName["files/blob.bin"]);
  assert.equal(byName["session.txt"].method, 0, "STORE");
  assert.deepEqual(Array.from(byName["session.txt"].data), Array.from(a));
  assert.deepEqual(Array.from(byName["files/blob.bin"].data), Array.from(b));
});

test("empty archive is also well-formed", () => {
  const zip = buildZip([]);
  const view = new DataView(zip.buffer, zip.byteOffset, zip.byteLength);
  assert.equal(zip.length, 22);
  assert.equal(u32(view, 0), 0x06054b50);
});

test("buildZip output is recognised by system unzip (if available)", () => {
  let ok = true;
  try { execFileSync("unzip", ["-h"], { stdio: "ignore" }); }
  catch { ok = false; }
  if (!ok) return; // tolerate environments without unzip
  const a = te.encode("ciao\n");
  const zip = buildZip([{ name: "hello.txt", data: a }]);
  const dir = mkdtempSync(join(tmpdir(), "sharetext-zip-"));
  const path = join(dir, "out.zip");
  try {
    writeFileSync(path, zip);
    const list = execFileSync("unzip", ["-l", path], { encoding: "utf8" });
    assert.match(list, /hello\.txt/);
    const dump = execFileSync("unzip", ["-p", path, "hello.txt"], { encoding: "utf8" });
    assert.equal(dump, "ciao\n");
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("uniqueEntryName: keeps distinct names and numbers duplicates", () => {
  const used = new Set();
  assert.equal(uniqueEntryName("report.pdf", used), "report.pdf");
  assert.equal(uniqueEntryName("notes", used), "notes");
  assert.equal(uniqueEntryName("report.pdf", used), "report (2).pdf");
  assert.equal(uniqueEntryName("Report.PDF", used), "Report (3).PDF");
  assert.equal(uniqueEntryName("notes", used), "notes (2)");
  assert.equal(uniqueEntryName(".env", used), ".env");
  assert.equal(uniqueEntryName(".env", used), ".env (2)");
  // A generated name that collides with a real one is skipped too.
  const used2 = new Set();
  uniqueEntryName("a (2).txt", used2);
  uniqueEntryName("a.txt", used2);
  assert.equal(uniqueEntryName("a.txt", used2), "a (3).txt");
});
