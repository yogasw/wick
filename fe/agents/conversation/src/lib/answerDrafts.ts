/* The answer someone is typing to a teammate's question, by task id.
   Kept outside the components: the tray, the Tasks rail and the thread's
   delegation block are re-rendered (and remounted) by every refetch and
   team_task event, and a half-typed answer must survive that. Cleared
   only once the answer was sent. */
const drafts = new Map<string, string>();

export function getDraft(taskId: string): string {
  return drafts.get(taskId) ?? "";
}

export function setDraft(taskId: string, text: string): void {
  if (text) drafts.set(taskId, text);
  else drafts.delete(taskId);
}

export function clearDraft(taskId: string): void {
  drafts.delete(taskId);
}
