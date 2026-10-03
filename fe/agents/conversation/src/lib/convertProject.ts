import type { AgentConnector, ConnectorGrant } from "./api/team.js";

/* Pure helpers behind "Make this an agent…" (AgentWizard convert mode). */

/** convertGrants is the Access a converted project starts with: every
    connector the owner sees at Write ("all"), so its channel chats keep
    doing what they did. Platform/System rows keep their tier defaults. */
export function convertGrants(catalog: AgentConnector[]): ConnectorGrant[] {
  return catalog
    .filter((c) => !c.tier && !c.tool)
    .map((c) => ({ connector_id: c.id, accounts: [], level: "all" as const, ops: [] }));
}

/** convertSummary says what moves over to the agent and keeps running. */
export function convertSummary(chats: number, channels: string[], schedules: number): string {
  const parts = [`${chats} chat${chats === 1 ? " becomes" : "s become"} the agent's`];
  if (channels.length) parts.push(`channels keep running as the agent: ${channels.join(", ")}`);
  if (schedules) parts.push(`${schedules} schedule${schedules === 1 ? "" : "s"} keep firing into it`);
  return parts.join("; ") + ".";
}
