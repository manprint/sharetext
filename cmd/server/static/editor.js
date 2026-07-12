export function anchoredScrollTop(scrollTop, sourceAnchors, targetAnchors) {
  const count = Math.min(sourceAnchors.length, targetAnchors.length);
  if (count < 2 || sourceAnchors[count - 1] <= sourceAnchors[0]) return targetAnchors[0] || 0;

  const top = Math.min(sourceAnchors[count - 1], Math.max(sourceAnchors[0], scrollTop));
  let next = 1;
  while (next < count && sourceAnchors[next] <= top) next++;
  if (next === count) return targetAnchors[count - 1];

  const previous = next - 1;
  const sourceSpan = sourceAnchors[next] - sourceAnchors[previous];
  const progress = sourceSpan === 0 ? 0 : (top - sourceAnchors[previous]) / sourceSpan;
  return targetAnchors[previous] + progress * (targetAnchors[next] - targetAnchors[previous]);
}
