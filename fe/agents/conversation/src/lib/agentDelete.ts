/* Pure helpers behind Settings' "Delete agent…" dialog. */

/** What /projects/{id}/delete-preview says about the agent's project. */
export type AgentProjectPreview = { chats: number; channels?: string[] | null; schedules?: number };

/** deleteAlert is the red warning shown while "Also delete its project"
    is ticked: what goes, and what stops with it. */
export function deleteAlert(p: AgentProjectPreview | null): string {
  const n = p?.chats ?? 0;
  let s = `This also deletes ${p ? `${n} chat${n === 1 ? "" : "s"}` : "its chats"}, their history, files and the agent's memory for this project. This cannot be undone.`;
  const ch = p?.channels ?? [];
  if (ch.length) s += ` Connected channels stop: ${ch.join(", ")}.`;
  const sc = p?.schedules ?? 0;
  if (sc) s += ` ${sc} schedule${sc === 1 ? "" : "s"} firing into it ${sc === 1 ? "is" : "are"} cancelled.`;
  return s;
}

/** canDeleteAgent: deleting the project too needs the agent's name typed. */
export function canDeleteAgent(alsoProject: boolean, typed: string, name: string): boolean {
  return !alsoProject || typed.trim() === name.trim();
}
