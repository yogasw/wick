/* Make Captain's "Give @x your access" (Settings › Captain confirm and the
   Team empty state): a Team lead works for its owner personally, so by
   default it also runs as the owner with Same as me. Make Captain stays
   its own call; the access is a second, ordinary update, and a failure
   there toasts and keeps the role. */

import { toastError } from "@wick-fe/common-stores";
import { makeCaptain, updateAgent, runApi, type AgentItem } from "./api/team.js";

export const OWNER_ACCESS = { run_as: "owner", access_mode: "owner" } as const;

export const OWNER_ACCESS_HINT = "Run as: Owner · Connector access: Same as me. A Team lead works for you personally.";

/** hasOwnerAccess: the agent already runs as its owner with Same as me. */
export const hasOwnerAccess = (a: Pick<AgentItem, "run_as" | "access_mode">) => a.run_as === "owner" && a.access_mode === "owner";

/** accessToday is the agent's access as the confirm reads it, e.g.
    "Caller · Choose connectors · 0". */
export function accessToday(a: Pick<AgentItem, "run_as" | "access_mode" | "allowed_connectors">): string {
  const runAs = a.run_as === "owner" ? "Owner" : "Caller";
  if (a.access_mode === "owner") return `${runAs} · Same as me`;
  const n = (a.allowed_connectors ?? []).filter((g) => g.level !== "off").length;
  return `${runAs} · Choose connectors · ${n}`;
}

/** makeCaptainWithAccess moves the Captain role to id and, with
    giveAccess, then sets it to run as the owner with Same as me. Returns
    the agent as last saved; throws only when the role did not move.
    accessFailed: the access update failed and was already toasted, so
    the caller skips its own success toast. */
export async function makeCaptainWithAccess(base: string, id: string, giveAccess: boolean): Promise<{ agent: AgentItem; accessFailed: boolean }> {
  const r = await runApi(makeCaptain(base, id));
  if (!giveAccess || hasOwnerAccess(r.agent)) return { agent: r.agent, accessFailed: false };
  try {
    return { agent: await runApi(updateAgent(base, r.agent.id, { ...OWNER_ACCESS })), accessFailed: false };
  } catch (err) {
    const why = err instanceof Error ? err.message : String(err);
    toastError(`@${r.agent.handle} is the Captain, but your access was not given`, `${why} Set it in Settings › Access.`);
    return { agent: r.agent, accessFailed: true };
  }
}
