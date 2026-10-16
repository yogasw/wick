import type { TicketStatus } from "./types/agents.js";

/* Which columns a ticket board draws, and what a status chip click saves.

   Nothing saved draws every column except the terminal one: a finished
   stage only grows, and a board button (Sync) acts on the columns on
   screen, so a Done open by default made the first click reconcile every
   finished ticket the project ever had. Opening it is a chip click, saved
   like any other choice. */

/** terminalKeys is the board's finished stages: every key flagged terminal
    (a combined board can carry one per project), else the last column. */
export function terminalKeys(statuses: readonly TicketStatus[]): string[] {
  const flagged = statuses.filter((s) => s.terminal).map((s) => s.key);
  if (flagged.length > 0) return flagged;
  const last = statuses[statuses.length - 1]?.key;
  return last ? [last] : [];
}

/** activeStatusKeys is the columns on screen for a saved selection — and so
    the statuses a board button sends. Empty saved = the default (all but
    terminal); the single `none` entry = every chip off. A saved key the
    board no longer has is not a column and is dropped. */
export function activeStatusKeys(
  saved: readonly string[] | undefined,
  statusKeys: readonly string[],
  terminal: readonly string[],
  none: string,
): string[] {
  if (!saved || saved.length === 0) return statusKeys.filter((k) => !terminal.includes(k));
  if (saved.length === 1 && saved[0] === none) return [];
  return statusKeys.filter((k) => saved.includes(k));
}

/** toggledStatuses is the selection to save after flipping one chip. The
    default set saves as [] so a stage added later still shows; any other
    set — every column with Done included — is saved as the explicit list. */
export function toggledStatuses(
  active: readonly string[],
  key: string,
  statusKeys: readonly string[],
  terminal: readonly string[],
  none: string,
): string[] {
  const cur = new Set(active);
  if (cur.has(key)) cur.delete(key);
  else cur.add(key);
  const next = statusKeys.filter((k) => cur.has(k));
  if (next.length === 0) return [none];
  const def = statusKeys.filter((k) => !terminal.includes(k));
  if (next.length === def.length && def.every((k) => cur.has(k))) return [];
  return next;
}
