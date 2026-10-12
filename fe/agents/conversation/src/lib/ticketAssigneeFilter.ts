import type { TicketFilter } from "./types/agents.js";

/* The board's assignee filter: the people ticked, as user ids plus two
   words — "me" (whoever is looking, resolved server-side) and "none" (a
   ticket with no assignee). Nothing ticked = everyone. */

export const ASSIGNEE_ME = "me";
export const ASSIGNEE_NONE = "none";

/** The ticked list of a saved filter. A filter saved before multi-select
    holds one `assignee`; it reads as a one-entry list, so an old "only me"
    keeps meaning only me. */
export function assigneesOf(f: TicketFilter): string[] {
  if (f.assignees && f.assignees.length > 0) return f.assignees;
  return f.assignee ? [f.assignee] : [];
}

/** f with the old single `assignee` folded into `assignees`. */
export function migrateTicketFilter(f: TicketFilter): TicketFilter {
  return withAssignees(f, assigneesOf(f));
}

/** f with a new ticked list. Empty drops the key: "everyone" is the
    absence of a filter, not an empty one. */
export function withAssignees(f: TicketFilter, list: string[]): TicketFilter {
  const { assignee: _old, assignees: _prev, ...rest } = f;
  return list.length > 0 ? { ...rest, assignees: list } : rest;
}

/** The board request's ?assignee= value: a comma list, or undefined for
    everyone. */
export function assigneeParam(list: string[]): string | undefined {
  return list.length > 0 ? list.join(",") : undefined;
}

/** The button's caption: "Everyone", or the first two picks by first name
    and a count of the rest ("Me, Wildhan +1"). */
export function assigneeSummary(list: string[], names: Record<string, string>, me?: string): string {
  if (list.length === 0) return "Everyone";
  const label = (v: string) => {
    if (v === ASSIGNEE_ME || (me && v === me)) return "Me";
    if (v === ASSIGNEE_NONE) return "Unassigned";
    const name = (names[v] ?? "").trim();
    return name ? name.split(/\s+/)[0] : v;
  };
  // "me" and the viewer's own id (an older saved filter may hold the id)
  // are one person, named once.
  const labels = [...new Set(list.map(label))];
  const shown = labels.slice(0, 2).join(", ");
  return labels.length > 2 ? `${shown} +${labels.length - 2}` : shown;
}
