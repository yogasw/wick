/* Which rail panels to re-fetch when the open panel changes.

   The rail's panels show state that moves while the page sits open — a
   schedule fires, a process exits, a sub-agent finishes, a ticket is synced
   from somewhere else. Their data used to be loaded once and then only on
   the events that happened to reach this tab, so opening a panel could show
   something minutes old until the page was reloaded.

   Re-fetch on OPEN, so what you look at is current, and on CLOSE, so the
   badge counts on the strip are current once the panel is out of the way.
   Opening one panel while another is open closes the first, so both
   refresh. Notes is here because it reads the same data as Ticket. */
export type RefreshableRailTab = "subagents" | "ticket" | "notes" | "process" | "scheduled";

const REFRESHABLE = new Set<string>(["subagents", "ticket", "notes", "process", "scheduled"]);

export function railRefreshTargets(prev: string | null, next: string | null): RefreshableRailTab[] {
  if (prev === next) return [];
  const out: RefreshableRailTab[] = [];
  for (const t of [prev, next]) {
    if (t && REFRESHABLE.has(t) && !out.includes(t as RefreshableRailTab)) out.push(t as RefreshableRailTab);
  }
  return out;
}
