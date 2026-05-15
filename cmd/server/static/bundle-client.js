/* Client-side ZIP bundle assembly for E2E sessions.
 *
 * Server's /bundle endpoint produces a zip of ciphertext blobs that cannot be
 * opened without the per-session key. For E2E sessions we instead fetch each
 * file, decrypt it in the browser, then assemble a plaintext zip locally and
 * push it to the user as a download.
 *
 * Encoding: STORE method only (no deflate). Avoids the streaming overhead and
 * an extra dependency on CompressionStream quirks across browsers; the saved
 * bytes from deflate over small text+attachments are not worth the complexity.
 */

const CRC_TABLE = (() => {
  const t = new Uint32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    t[n] = c >>> 0;
  }
  return t;
})();

function crc32(bytes) {
  let c = 0xffffffff;
  for (let i = 0; i < bytes.length; i++) {
    c = CRC_TABLE[(c ^ bytes[i]) & 0xff] ^ (c >>> 8);
  }
  return (c ^ 0xffffffff) >>> 0;
}

function dosDateTime(d) {
  // ZIP packs mtime as MS-DOS fields: time = (h<<11)|(m<<5)|(s/2); date =
  // ((y-1980)<<9)|(mo<<5)|day. Sub-second precision is lost; that's expected.
  const date = ((d.getFullYear() - 1980) << 9) | ((d.getMonth() + 1) << 5) | d.getDate();
  const time = (d.getHours() << 11) | (d.getMinutes() << 5) | Math.floor(d.getSeconds() / 2);
  return { date, time };
}

function utf8(s) {
  return new TextEncoder().encode(s);
}

function u16(view, off, v) { view.setUint16(off, v, true); }
function u32(view, off, v) { view.setUint32(off, v, true); }

/**
 * Build a ZIP archive from a list of entries.
 * @param {{name: string, data: Uint8Array}[]} entries
 * @returns {Uint8Array}
 */
export function buildZip(entries) {
  const now = new Date();
  const { date, time } = dosDateTime(now);
  const parts = [];
  const centralRecords = [];
  let offset = 0;
  for (const e of entries) {
    if (!(e.data instanceof Uint8Array)) {
      throw new Error("zip entry data must be Uint8Array");
    }
    const nameBytes = utf8(e.name);
    const crc = crc32(e.data);
    const size = e.data.length;

    // Local file header (30 bytes + name).
    const lh = new Uint8Array(30 + nameBytes.length);
    const lv = new DataView(lh.buffer);
    u32(lv, 0, 0x04034b50);
    u16(lv, 4, 20);            // version needed
    u16(lv, 6, 0x0800);        // flags: bit 11 = UTF-8 filename
    u16(lv, 8, 0);             // method: STORE
    u16(lv, 10, time);
    u16(lv, 12, date);
    u32(lv, 14, crc);
    u32(lv, 18, size);         // compressed size = uncompressed
    u32(lv, 22, size);
    u16(lv, 26, nameBytes.length);
    u16(lv, 28, 0);            // extra field length
    lh.set(nameBytes, 30);
    parts.push(lh, e.data);
    const localOffset = offset;
    offset += lh.length + size;

    // Central directory record (46 bytes + name).
    const cd = new Uint8Array(46 + nameBytes.length);
    const cv = new DataView(cd.buffer);
    u32(cv, 0, 0x02014b50);
    u16(cv, 4, 20);            // version made by (DOS)
    u16(cv, 6, 20);            // version needed
    u16(cv, 8, 0x0800);
    u16(cv, 10, 0);            // method
    u16(cv, 12, time);
    u16(cv, 14, date);
    u32(cv, 16, crc);
    u32(cv, 20, size);
    u32(cv, 24, size);
    u16(cv, 28, nameBytes.length);
    u16(cv, 30, 0);            // extra
    u16(cv, 32, 0);            // comment
    u16(cv, 34, 0);            // disk number start
    u16(cv, 36, 0);            // internal file attrs
    u32(cv, 38, 0);            // external file attrs
    u32(cv, 42, localOffset);
    cd.set(nameBytes, 46);
    centralRecords.push(cd);
  }

  const cdStart = offset;
  let cdSize = 0;
  for (const cd of centralRecords) {
    parts.push(cd);
    cdSize += cd.length;
  }
  offset += cdSize;

  // End of central directory record (22 bytes, no comment).
  const eocd = new Uint8Array(22);
  const ev = new DataView(eocd.buffer);
  u32(ev, 0, 0x06054b50);
  u16(ev, 4, 0);
  u16(ev, 6, 0);
  u16(ev, 8, entries.length);
  u16(ev, 10, entries.length);
  u32(ev, 12, cdSize);
  u32(ev, 16, cdStart);
  u16(ev, 20, 0);
  parts.push(eocd);

  // Concatenate.
  const total = parts.reduce((n, p) => n + p.length, 0);
  const out = new Uint8Array(total);
  let pos = 0;
  for (const p of parts) {
    out.set(p, pos);
    pos += p.length;
  }
  return out;
}

/**
 * Trigger a browser download for a ZIP built from `entries`.
 */
export function downloadZip(filename, entries) {
  if (typeof document === "undefined") return;
  const zip = buildZip(entries);
  const blob = new Blob([zip], { type: "application/zip" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  a.style.display = "none";
  document.body.appendChild(a);
  a.click();
  // Defer revocation so Firefox actually starts the download first.
  setTimeout(() => {
    a.remove();
    URL.revokeObjectURL(url);
  }, 0);
}
