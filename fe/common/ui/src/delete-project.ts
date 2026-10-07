/* What the project delete dialog says and when it lets the user confirm.
   Kept out of the component so the wording and the type-the-name gate are
   tested without rendering anything. */

/* GET <base>/projects/<id>/delete-preview */
export type DeleteProjectPreview = {
  id: string;
  name: string;
  chats: number;
  custom_path?: string;
  protected: boolean;
};

export function deleteProjectTitle(p: { name: string }): string {
  return `Delete project ${p.name}?`;
}

export function deleteProjectBody(p: DeleteProjectPreview): string {
  const chats = `${p.chats} chat${p.chats === 1 ? "" : "s"}`;
  if (p.custom_path) {
    return (
      `This deletes ${chats} and the agents' memory for this project. ` +
      `Its folder ${p.custom_path} is yours and stays on disk. This cannot be undone.`
    );
  }
  return `This deletes ${chats}, its files folder and the agents' memory for this project. This cannot be undone.`;
}

/* Typing the name is asked only when there is something to lose: an empty
   project goes with a plain confirm. */
export function needsTypedName(p: DeleteProjectPreview): boolean {
  return p.chats > 0;
}

export function canConfirmDelete(p: DeleteProjectPreview, typed: string): boolean {
  if (p.protected) return false;
  return !needsTypedName(p) || typed.trim() === p.name.trim();
}
