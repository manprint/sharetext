/**
 * File-marker parsing helpers for the lines view.
 *
 * Marker grammar (exact full line, ignoring surrounding whitespace):
 *
 *     [file:<id>:<url-encoded-name>]
 *
 * - <id> is the server-generated attachment ID (alphanumeric).
 * - <name> is the original filename, encoded with URL path escaping so it
 *   may contain spaces, ":", "]" safely.
 */

const FILE_RE = /^\s*\[file:([A-Za-z0-9_-]+):([^\]]+)\]\s*$/;

/**
 * parseFileMarker(line) → { id, name, encodedName } when the line is a file
 * marker, otherwise null. `encodedName` is the raw second-slot content; in
 * E2E mode the caller passes it through `decryptName` to recover the
 * plaintext filename.
 *
 * @param {string} line
 * @returns {{id:string, name:string, encodedName:string}|null}
 */
export function parseFileMarker(line) {
  if (typeof line !== 'string') return null;
  const m = FILE_RE.exec(line);
  if (!m) return null;
  let name;
  try {
    name = decodeURIComponent(m[2]);
  } catch {
    name = m[2];
  }
  return { id: m[1], name, encodedName: m[2] };
}

/**
 * buildFileMarker(id, name) → the canonical marker string used in the
 * editor for an uploaded attachment.
 *
 * @param {string} id
 * @param {string} name
 * @returns {string}
 */
export function buildFileMarker(id, name) {
  return `[file:${id}:${encodeURIComponent(String(name == null ? 'file.bin' : name))}]`;
}

/**
 * buildFileMarkerRaw(id, encodedName) → marker string with `encodedName`
 * inserted into the name slot as-is (no URL encoding). Used by the E2E
 * upload path which already produces a base64url-encoded `iv.ct` payload
 * compatible with the marker grammar.
 *
 * @param {string} id
 * @param {string} encodedName
 * @returns {string}
 */
export function buildFileMarkerRaw(id, encodedName) {
  return `[file:${id}:${encodedName}]`;
}

/**
 * insertMarkersAtPosition(text, at, markers) → new string with the markers
 * inserted at offset `at`, each on its own line, prefixing/suffixing only the
 * minimum amount of `\n` needed to keep markers on a whole line.
 *
 * The file-marker grammar requires each marker to be the only content on its
 * line (see `FILE_RE` above), so the function:
 *   - prepends `\n` when the preceding content doesn't already end with one,
 *   - appends `\n` only when the following content doesn't already start with
 *     one (avoids creating a stray blank line when inserting just before an
 *     existing newline).
 *
 * @param {string} text
 * @param {number} at
 * @param {string[]} markers
 * @returns {string}
 */
export function insertMarkersAtPosition(text, at, markers) {
  if (typeof text !== 'string') text = '';
  if (!Array.isArray(markers) || markers.length === 0) return text;
  const max = text.length;
  let pos = Number.isFinite(at) ? at : max;
  if (pos < 0) pos = 0;
  if (pos > max) pos = max;
  const before = text.slice(0, pos);
  const after = text.slice(pos);
  const block = markers.join('\n');
  const prefix = before.length === 0 || before.endsWith('\n') ? '' : '\n';
  const suffix = after.startsWith('\n') ? '' : '\n';
  return before + prefix + block + suffix + after;
}

/**
 * formatBytes(n) → human-readable size.
 *
 * @param {number} n
 * @returns {string}
 */
export function formatBytes(n) {
  if (!Number.isFinite(n) || n < 0) return '0 B';
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(2)} MB`;
  return `${(n / 1024 / 1024 / 1024).toFixed(2)} GB`;
}
