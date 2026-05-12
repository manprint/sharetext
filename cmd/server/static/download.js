/**
 * Helpers to produce safe text-file downloads in the browser.
 * Pure functions (safeFilename / buildFilename) are exported so they can be
 * unit-tested under node; downloadText performs the actual DOM side-effect.
 */

/**
 * safeFilename strips characters that misbehave in OS file names, collapses
 * whitespace, and trims to a sane length.
 *
 * @param {string} name
 * @returns {string}
 */
export function safeFilename(name) {
  const cleaned = String(name == null ? '' : name)
    .replace(/[\\/:*?"<>|\x00-\x1f]/g, '_')
    .replace(/\s+/g, '_')
    .replace(/^\.+/, '')
    .slice(0, 80);
  return cleaned === '' ? 'sharetext' : cleaned;
}

/**
 * buildFilename composes the final `.txt` name.
 *
 * Forms:
 *   buildFilename(slug)                       -> "<slug>.txt"
 *   buildFilename(slug, "riga", 3)            -> "<slug>-riga-3.txt"
 *   buildFilename(slug, "blocco", 2)          -> "<slug>-blocco-2.txt"
 *
 * @param {string} slug
 * @param {string} [kind]
 * @param {number} [index]
 * @returns {string}
 */
export function buildFilename(slug, kind, index) {
  const base = safeFilename(slug);
  if (!kind) return `${base}.txt`;
  const k = safeFilename(kind);
  const idx = Number.isFinite(index) ? Math.max(1, Math.floor(index)) : 1;
  return `${base}-${k}-${idx}.txt`;
}

/**
 * Trigger a browser download of `text` named `filename`.
 *
 * @param {string} text
 * @param {string} filename
 */
export function downloadText(text, filename) {
  const blob = new Blob([text], { type: 'text/plain;charset=utf-8' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = filename;
  a.rel = 'noopener';
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 0);
}
