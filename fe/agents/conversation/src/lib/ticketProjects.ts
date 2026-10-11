import { getProjectOptions } from "./api/options.js";
import { runApi } from "./api/team.js";
import type { ProjectOption } from "./types/agents.js";

/* The projects the Team Tickets page can combine: every project the
   caller can open (GET /projects/options already lists only those) that
   has ticket mode on (ticket_enabled). The server checks the same two
   things again when ticket_projects is saved. */

/** ticketProjectOptions lists the pickable projects, by name. */
export async function ticketProjectOptions(base: string): Promise<ProjectOption[]> {
  const all = await runApi(getProjectOptions(base));
  return all.filter((p) => p.ticketEnabled).sort((a, b) => a.name.localeCompare(b.name));
}

/** withProject turns id on or off in a selection, keeping the order picked. */
export function withProject(sel: readonly string[], id: string, on: boolean): string[] {
  const rest = sel.filter((x) => x !== id);
  return on ? [...rest, id] : rest;
}

/** ticketsSubtitle is the Tickets header's second line: the picked
    projects by name (each clipped, past `max` as "+N"). */
export function ticketsSubtitle(ids: readonly string[], names: Record<string, string>, max = 3): string {
  if (!ids.length) return "No projects selected";
  const clip = (s: string) => (s.length > 24 ? s.slice(0, 23) + "…" : s);
  const shown = ids.slice(0, max).map((id) => "📁 " + clip(names[id] ?? id)).join(" · ");
  const more = ids.length > max ? ` +${ids.length - max}` : "";
  return `${shown}${more} · each ticket stays in its project`;
}

/** projectCount is the Tickets row's caption ("" = nothing picked). */
export function projectCount(n: number): string {
  return n ? `${n} project${n === 1 ? "" : "s"}` : "";
}
