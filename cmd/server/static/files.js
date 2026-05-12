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
 * parseFileMarker(line) → { id, name } when the line is a file marker,
 * otherwise null.
 *
 * @param {string} line
 * @returns {{id:string, name:string}|null}
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
  return { id: m[1], name };
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
