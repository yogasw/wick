import type { ConversationTurn, ServerSessionAgent } from "./types/agents.js";

export type { ServerSessionAgent };

/* Ordinary ticket sessions (PLAN-team-tickets T12b): one home agent plus
   @mentions. Identity is (agent, owner), never the handle: everyone's
   Captain is "captain". */

/** speakerLabel names an agent turn: "Name · @owner" for an agent that is
    not the viewer's own (mine), its name otherwise. */
export function speakerLabel(sp: { handle: string; name?: string; owner_handle?: string }, mine: boolean): string {
  const name = sp.name || (sp.handle ? "@" + sp.handle : "Agent");
  return !mine && sp.owner_handle ? `${name} · @${sp.owner_handle}` : name;
}

/** One agent other than the home one in the rail's Session tab. */
export type SessionAgent = {
  key: string;
  name: string;
  /** "@owner" when it is not the viewer's own agent; "" otherwise. */
  owner: string;
  /** How it came in: "via @mention", "joined when @hana wrote", or "". */
  joined: string;
  replies: number;
};

/** sessionAgents is the Session tab's list of agents other than the home
    one: each (agent, owner) that answered, once, in first-reply order, with
    who owns it, how it joined and how many replies. Only turns carrying an
    owner (posted by the ticket session router) count; the home agent's own
    turns carry none.

    The loaded turns are only the pages scrolled so far, so the server's
    whole-history count comes first and the loaded turns add what it has not
    seen yet (replies since it was read). A reply count is the larger of the
    two: each is a lower bound. */
export function sessionAgents(
  turns: ConversationTurn[],
  isMine: (agentId: string, handle: string) => boolean,
  server: ServerSessionAgent[] = [],
): SessionAgent[] {
  const out: SessionAgent[] = [];
  const at = new Map<string, number>();
  const add = (sp: { agent_id: string; handle?: string; name?: string; owner_id?: string; owner_handle?: string; via?: string }) => {
    const key = `${sp.agent_id}\u0000${sp.owner_id}`;
    let i = at.get(key);
    if (i === undefined) {
      const handle = sp.handle ?? "";
      const mine = isMine(sp.agent_id, handle);
      i = out.length;
      at.set(key, i);
      out.push({
        key,
        name: sp.name || (handle ? "@" + handle : "Agent"),
        owner: !mine && sp.owner_handle ? "@" + sp.owner_handle : "",
        joined: sp.via === "mention" ? "via @mention" : sp.owner_handle ? `joined when @${sp.owner_handle} wrote` : "",
        replies: 0,
      });
    }
    return out[i];
  };
  for (const a of server) {
    if (a.owner_id) add(a).replies = a.replies;
  }
  const loaded = new Map<string, number>();
  for (const t of turns) {
    const sp = t.role === "assistant" ? t.speaker : undefined;
    if (!sp || !sp.owner_id) continue;
    const row = add(sp);
    loaded.set(row.key, (loaded.get(row.key) ?? 0) + 1);
  }
  for (const row of out) row.replies = Math.max(row.replies, loaded.get(row.key) ?? 0);
  return out;
}
