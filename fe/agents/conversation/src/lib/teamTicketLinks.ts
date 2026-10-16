import { formatAgentsRoute } from "./agentsRouter.js";
import { getSessionMeta } from "./api/sessions.js";
import { listAgentRoster, runApi, saveTeamSettings, type AgentItem } from "./api/team.js";

/* Links between a ticket and Team › Tickets, shared by the Team app, a
   chat's rail and Agents › Projects. */

/** teamTicketsHref is Team › Tickets: the board, or one ticket's page. */
export function teamTicketsHref(base: string, project?: string, ticket?: string): string {
  return formatAgentsRoute(
    { handle: null, session: null, panel: null, tickets: project && ticket ? { project, ticket } : {} },
    base,
  );
}

/** addTicketProject puts project on the caller's Tickets selection when it
    is not there yet, and answers the saved list (null = nothing written).
    An unknown selection (null: never read, or the read failed) is left
    alone — writing it would replace projects nobody has seen. */
export async function addTicketProject(
  base: string,
  current: readonly string[] | null,
  project: string,
): Promise<string[] | null> {
  if (current === null || !project || current.includes(project)) return null;
  const next = [...current, project];
  const s = await runApi(saveTeamSettings(base, { ticket_projects: next }));
  return s.ticket_projects ?? next;
}

/* Whether the caller has a Team: the read-only roster names a Captain.
   No Captain is no Team (the Team app shows its empty page then). Read
   once per page; a failed read counts as no Team, which only hides a
   link. */
let teamRead: Promise<boolean> | null = null;
export function hasTeam(base: string): Promise<boolean> {
  teamRead ??= runApi(listAgentRoster(base))
    .then((r) => !!r.captain_id || (r.agents ?? []).some((a) => a.is_captain))
    .catch(() => false);
  return teamRead;
}
/** Test hook: forget the cached answer. */
export function resetHasTeam() {
  teamRead = null;
}

/** Where a ticket's chat row opens. */
export type SessionTarget =
  | { kind: "team"; handle: string; session: string | null }
  | { kind: "projects"; href: string };

/** sessionTarget opens a chat with one of the caller's Team agents in that
    agent's Team chat (its main chat as the agent's own page), and any other
    chat in Agents › Projects as a ticket page always did. The session's
    agent comes from its meta; a meta that cannot be read falls back to
    Agents › Projects. */
export async function sessionTarget(
  base: string,
  sessionId: string,
  agents: readonly AgentItem[],
  readAgent: (id: string) => Promise<string | undefined> = (id) => runApi(getSessionMeta(base, id)).then((m) => m.agent_id),
): Promise<SessionTarget> {
  let agentId: string | undefined;
  try {
    agentId = await readAgent(sessionId);
  } catch {
    agentId = undefined;
  }
  const a = agentId ? agents.find((x) => x.id === agentId) : undefined;
  if (a) return { kind: "team", handle: a.handle, session: sessionId === a.main_session_id ? null : sessionId };
  return { kind: "projects", href: `${base}/sessions/${encodeURIComponent(sessionId)}` };
}

/** The Team app's way to open a ticket without leaving it (AgentMode). */
export type TicketOpener = (project: string, ticket?: string) => void;

/** ticketJumpFor is a chat's jump to its ticket (or the board). Hosted by
    the Team app (`team` set) it goes to Team › Tickets — through the host
    when it can navigate in place, else by URL; elsewhere to the project's
    board in Agents › Projects, as before. */
export function ticketJumpFor(
  base: string,
  project: string,
  ticket: string | undefined,
  team: { onOpenTicket?: TicketOpener } | undefined,
  go: (href: string) => void = (href) => { window.location.href = href; },
): { label: string; onJump: () => void } {
  const proj = encodeURIComponent(project);
  if (team) {
    return {
      label: ticket ? "Open in Team › Tickets ↗" : "Team › Tickets ↗",
      onJump: () => {
        if (team.onOpenTicket) team.onOpenTicket(project, ticket);
        else go(teamTicketsHref(base, project, ticket));
      },
    };
  }
  if (ticket) {
    return {
      label: `Ticket ${ticket}`,
      onJump: () => go(`${base}/sessions?project=${proj}&ticket=${encodeURIComponent(ticket)}`),
    };
  }
  return { label: "Ticket board", onJump: () => go(`${base}/sessions?project=${proj}`) };
}

/** ticketsEntryShown says whether Team › Tickets exists for this viewer:
    only once the roster has loaded and they have a Team (a Captain). With
    no Team nothing changes — no Tickets entry, no board, no room. */
export function ticketsEntryShown(s: { loaded: boolean; loadError?: unknown; hasCaptain: boolean }): boolean {
  return s.loaded && !s.loadError && s.hasCaptain;
}
