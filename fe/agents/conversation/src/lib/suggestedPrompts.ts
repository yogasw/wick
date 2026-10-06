/* Settings › Persona › Suggested prompts (team/prompts.go). */
import type { SuggestedPrompt } from "./api/team.js";

export const MAX_PROMPTS = 4;

/** promptsToSave mirrors team.NormalizeSuggestedPrompts: trimmed, blank
    rows dropped, a half-filled row taking its other field from the filled
    one. A row being typed therefore saves as soon as it has any text. */
export function promptsToSave(rows: SuggestedPrompt[]): SuggestedPrompt[] {
  const out: SuggestedPrompt[] = [];
  for (const r of rows) {
    const title = r.title.trim();
    const message = r.message.trim();
    if (!title && !message) continue;
    out.push({ title: title || message, message: message || title });
  }
  return out.slice(0, MAX_PROMPTS);
}
