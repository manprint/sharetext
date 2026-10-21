export function shouldApplyRemoteContent({
  currentContent,
  incomingContent,
  hasPendingLocalChanges,
}) {
  if (currentContent === incomingContent) return false;
  // Unsent local edits are never overwritten, by a snapshot or a live update:
  // either they reach the server and supersede this content, or the editor
  // lock turns out to be someone else's and they are rolled back explicitly
  // to the last confirmed server text.
  return !hasPendingLocalChanges;
}

export function shouldFlushPendingLocalChanges({
  initialSnapshot,
  hasPendingLocalChanges,
}) {
  return initialSnapshot && hasPendingLocalChanges;
}
