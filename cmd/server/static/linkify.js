/**
 * Detect http(s) URLs anywhere inside a text run and render them as <a> nodes.
 *
 * Output is built with DOM APIs (no innerHTML / no HTML strings) so user text is
 * never interpreted as markup.
 */

const URL_RE = /https?:\/\/[^\s<>"'`]+/gi;
// Strip trailing punctuation that usually belongs to the surrounding sentence,
// not the URL itself. Closing brackets are trimmed only when they aren't
// balanced by an opening bracket inside the URL.
const TRAILING_PUNCT = /[.,;:!?'"`]+$/;
const TRAILING_PAIRED = { ')': '(', ']': '[', '}': '{' };

function trimUrl(url) {
  let end = url.length;
  while (end > 0) {
    const ch = url[end - 1];
    if (TRAILING_PUNCT.test(ch)) { end--; continue; }
    if (TRAILING_PAIRED[ch]) {
      const open = TRAILING_PAIRED[ch];
      let opens = 0;
      let closes = 0;
      for (let i = 0; i < end; i++) {
        if (url[i] === open) opens++;
        else if (url[i] === ch) closes++;
      }
      if (closes > opens) { end--; continue; }
    }
    break;
  }
  return url.slice(0, end);
}

/**
 * findUrls(text) → array of { start, end, url } for every http(s) URL.
 * Pure function: safe to test outside the browser.
 *
 * @param {string} text
 * @returns {Array<{start:number, end:number, url:string}>}
 */
export function findUrls(text) {
  const out = [];
  if (typeof text !== 'string' || text.length === 0) return out;
  URL_RE.lastIndex = 0;
  let m;
  while ((m = URL_RE.exec(text)) !== null) {
    const url = trimUrl(m[0]);
    if (url.length <= 'https://'.length) continue;
    out.push({ start: m.index, end: m.index + url.length, url });
  }
  return out;
}

/**
 * Append `text` to `parent`, replacing every http(s) URL with an <a> node.
 * Anchors open in a new tab with `noopener noreferrer` to avoid window.opener
 * leaks. Non-link runs are added as plain Text nodes.
 *
 * @param {Node} parent
 * @param {string} text
 */
export function appendLinkified(parent, text) {
  if (typeof text !== 'string' || text.length === 0) return;
  const matches = findUrls(text);
  if (matches.length === 0) {
    parent.appendChild(document.createTextNode(text));
    return;
  }
  let cursor = 0;
  for (const m of matches) {
    if (m.start > cursor) {
      parent.appendChild(document.createTextNode(text.slice(cursor, m.start)));
    }
    const a = document.createElement('a');
    a.className = 'auto-link';
    a.href = m.url;
    a.target = '_blank';
    a.rel = 'noopener noreferrer';
    a.textContent = m.url;
    parent.appendChild(a);
    cursor = m.end;
  }
  if (cursor < text.length) {
    parent.appendChild(document.createTextNode(text.slice(cursor)));
  }
}
