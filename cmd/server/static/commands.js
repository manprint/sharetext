/**
 * Slash-command middleware for the editor.
 *
 * A "slash token" is a `/` followed by zero or more name characters
 * (`[a-zA-Z0-9_-]`) appearing inline anywhere in the editor text. To avoid
 * matching URLs ("https://...") or path fragments inside a word, the slash
 * must sit at buffer start or follow whitespace (` `, tab, newline). The
 * caret must also sit immediately after the last name character (i.e. the
 * caret is at the *end* of the token, not inside a longer word), so
 * `/timestamp foo` with the caret on the `foo` part does not match.
 *
 * The registry is intentionally minimal so new commands can be plugged in by
 * calling `registerCommand(name, handler)`. Handlers receive a context with
 * the token range and helpers supplied by the editor host:
 *
 *   ctx = {
 *     name, args, text, caret,
 *     tokenStart, tokenEnd,    // [tokenStart, tokenEnd) is the `/name` range
 *     host: { ...editor-supplied callbacks }
 *   }
 */

const NAME_RE = /^[a-zA-Z][a-zA-Z0-9_-]*$/;
const NAME_CHAR_RE = /[a-zA-Z0-9_-]/;

const registry = new Map();

export function registerCommand(name, handler) {
  if (typeof name !== 'string' || !NAME_RE.test(name)) {
    throw new Error(`invalid command name: ${name}`);
  }
  if (typeof handler !== 'function') {
    throw new Error(`handler for /${name} must be a function`);
  }
  registry.set(name.toLowerCase(), handler);
}

export function unregisterCommand(name) {
  if (typeof name !== 'string') return false;
  return registry.delete(name.toLowerCase());
}

export function hasCommand(name) {
  if (typeof name !== 'string') return false;
  return registry.has(name.toLowerCase());
}

export function listCommands() {
  return [...registry.keys()].sort();
}

export function clearCommands() {
  registry.clear();
}

/**
 * filterCommands(prefix) → array of registered command names whose name
 * starts with `prefix` (case-insensitive), sorted alphabetically.
 */
export function filterCommands(prefix) {
  const p = (typeof prefix === 'string' ? prefix : '').toLowerCase();
  return [...registry.keys()].filter((n) => n.startsWith(p)).sort();
}

/**
 * findSlashTokenAtCaret(text, caret) → { slashAt, nameEnd, name } when the
 * caret sits at the end of an inline slash token, otherwise null.
 *
 *   - `slashAt`: index of the leading `/`
 *   - `nameEnd`: caret position (also = slashAt + 1 + name.length)
 *   - `name`: the token name (empty string when the user just typed `/`)
 */
export function findSlashTokenAtCaret(text, caret) {
  if (typeof text !== 'string') return null;
  const max = text.length;
  let c = Number.isFinite(caret) ? caret : max;
  if (c < 0) c = 0;
  if (c > max) c = max;
  // Caret must be at the end of the token (no name char immediately after).
  if (c < max && NAME_CHAR_RE.test(text[c])) return null;
  let i = c;
  while (i > 0 && NAME_CHAR_RE.test(text[i - 1])) i--;
  if (i === 0 || text[i - 1] !== '/') return null;
  const slashAt = i - 1;
  if (slashAt > 0) {
    const prev = text[slashAt - 1];
    if (prev !== ' ' && prev !== '\t' && prev !== '\n' && prev !== '\r') {
      return null;
    }
  }
  return { slashAt, nameEnd: c, name: text.slice(i, c) };
}

/**
 * Format a Date as `DD-MM-YYYY_HH-MM-SS` in the local timezone.
 */
export function formatTimestamp(date = new Date()) {
  const d = date instanceof Date ? date : new Date(date);
  const pad = (n) => String(n).padStart(2, '0');
  return (
    `${pad(d.getDate())}-${pad(d.getMonth() + 1)}-${d.getFullYear()}` +
    `_${pad(d.getHours())}-${pad(d.getMinutes())}-${pad(d.getSeconds())}`
  );
}

/**
 * dispatchCommand(ctx) → Promise<boolean>. Invokes the registered handler
 * matching `ctx.name`. Returns `false` (and does not throw) when no handler
 * is registered, so the host can fall back to no-op.
 */
export async function dispatchCommand(ctx) {
  if (!ctx || typeof ctx.name !== 'string') return false;
  const handler = registry.get(ctx.name.toLowerCase());
  if (!handler) return false;
  await handler(ctx);
  return true;
}
