import type { ConversationTurn } from "./types/agents.js";

/* Ordinary ticket sessions (PLAN-team-tickets T12b): one home agent plus
   @mentions. Identity is (agent, owner), never the handle: everyone's
   Captain is "captain". */

export type Replier = { key: string; label: string };

/** speakerLabel names an agent turn: "Name · @owner" for an agent that is
    not the viewer's own (mine), its name otherwise. */
export function speakerLabel(sp: { handle: string; name?: string; owner_handle?: string }, mine: boolean): string {
  const name = sp.name || (sp.handle ? "@" + sp.handle : "Agent");
  return !mine && sp.owner_handle ? `${name} · @${sp.owner_handle}` : name;
}

/** alsoReplied is the session header's read-only "also replied" list:
    each agent other than the home one that answered here, once, in
    first-reply order. Only turns carrying an owner (posted by the ticket
    session router) count; the home agent's own turns carry none. */
export function alsoReplied(turns: ConversationTurn[], isMine: (agentId: string, handle: string) => boolean): Replier[] {
  const out: Replier[] = [];
  const seen = new Set<string>();
  for (const t of turns) {
    const sp = t.role === "assistant" ? t.speaker : undefined;
    if (!sp || !sp.owner_id) continue;
    const key = `${sp.agent_id}\u0000${sp.owner_id}`;
    if (seen.has(key)) continue;
    seen.add(key);
    out.push({ key, label: speakerLabel(sp, isMine(sp.agent_id, sp.handle)) });
  }
  return out;
}
