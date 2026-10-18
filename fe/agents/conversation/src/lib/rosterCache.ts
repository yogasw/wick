/* The last roster this tab read (sessionStorage), shared by the sidebar
   and a chat: a chat seeds its teammates' avatars from it, so a picture
   chip (delegation card, task tray, task thread) draws the same versioned
   src the sidebar already loaded — straight from the browser cache —
   instead of a placeholder until the chat's own roster read returns. */
import type { AvatarImageView } from "@wick-fe/common-avatar";
import type { AgentItem } from "./api/team.js";
import type { TeamPeer } from "./teamMention.js";

export const ROSTER_CACHE = "wick.team.roster";

export type RosterCache = { agents?: AgentItem[]; captain_id?: string };

/** readRosterCache is the cached roster, null when there is none. */
export function readRosterCache(): RosterCache | null {
  try {
    const c = JSON.parse(sessionStorage.getItem(ROSTER_CACHE) ?? "null") as RosterCache | null;
    return c?.agents?.length ? c : null;
  } catch {
    return null;
  }
}

export function writeRosterCache(c: RosterCache): void {
  try {
    sessionStorage.setItem(ROSTER_CACHE, JSON.stringify(c));
  } catch {
    /* storage full or off */
  }
}

export type TeamAgentView = { id: string; name: string; kind?: string; shape?: string; color?: string; expression?: string; image?: AvatarImageView | null };

/** teamAgentsByHandle is the avatar map the chat's team components read,
    the image passed through as the roster has it (src with its ?v=). */
export const teamAgentsByHandle = (peers: TeamPeer[]): Record<string, TeamAgentView> =>
  Object.fromEntries(
    peers.map((p) => [p.handle, { id: p.id, name: p.name || p.handle, kind: p.avatar?.kind, shape: p.avatar?.shape, color: p.avatar?.color, expression: p.avatar?.expression, image: p.avatar?.image }]),
  );
