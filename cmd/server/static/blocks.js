export const BLOCK_DELIMITER = '-----';

function isDelimiter(line) {
  return line.trim() === BLOCK_DELIMITER;
}

/**
 * Parse text into logical items.
 * Lines between a matched pair of `-----` lines are coalesced into one "block".
 * Unmatched delimiters are emitted as plain lines.
 *
 * @param {string} text
 * @returns {Array<{type:'line'|'block', text:string}>}
 */
export function parseBlocks(text) {
  if (text === '') return [];
  const lines = text.split('\n');
  const out = [];
  let i = 0;
  while (i < lines.length) {
    if (isDelimiter(lines[i])) {
      let j = i + 1;
      while (j < lines.length && !isDelimiter(lines[j])) j++;
      if (j < lines.length) {
        out.push({ type: 'block', text: lines.slice(i + 1, j).join('\n') });
        i = j + 1;
        continue;
      }
    }
    out.push({ type: 'line', text: lines[i] });
    i++;
  }
  return out;
}
