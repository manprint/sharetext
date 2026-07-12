export const BLOCK_DELIMITER = '-----';

function isDelimiter(line) {
  return line.trim() === BLOCK_DELIMITER;
}

function parseBlockEntries(text) {
  if (text === '') return [];
  const lines = text.split('\n');
  const entries = [];
  let i = 0;
  while (i < lines.length) {
    if (isDelimiter(lines[i])) {
      let j = i + 1;
      while (j < lines.length && !isDelimiter(lines[j])) j++;
      if (j < lines.length) {
        entries.push({ type: 'block', text: lines.slice(i + 1, j).join('\n'), start: i, end: j });
        i = j + 1;
        continue;
      }
    }
    entries.push({ type: 'line', text: lines[i], start: i, end: i });
    i++;
  }
  return entries;
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
  return parseBlockEntries(text).map(({ type, text }) => ({ type, text }));
}

export function logicalLineLabels(text) {
  if (text === '') return ['1'];
  const labels = Array(text.split('\n').length).fill('');
  parseBlockEntries(text).forEach((entry, index) => {
    labels[entry.start] = String(index + 1);
  });
  return labels;
}

export function logicalLineStarts(text) {
  if (text === '') return [0];
  return parseBlockEntries(text).map((entry) => entry.start);
}
