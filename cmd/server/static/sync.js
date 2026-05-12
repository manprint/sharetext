export function shouldApplyRemoteContent({
  currentContent,
  incomingContent,
  hasPendingLocalChanges,
  initialSnapshot,
}) {
  if (currentContent === incomingContent) return false;
  if (initialSnapshot && hasPendingLocalChanges) return false;
  return true;
}

export function shouldFlushPendingLocalChanges({
  initialSnapshot,
  hasPendingLocalChanges,
}) {
  return initialSnapshot && hasPendingLocalChanges;
}