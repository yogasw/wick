/* Settings › Captain and Settings › Access › History: what the toggles say
   and how a history row reads. Pure, so the rules are tested here and the
   drawer stays thin. */

import type { AccessHistoryItem, AutoApprove, CaptainCan, CaptainPolicy } from "./api/team.js";

export const DEFAULT_CAPTAIN_CAN: CaptainCan = { persona: true, access: false, routines: true };

/** captainCanOf is the agent's stored permissions, defaults for an older
    server that sends none. */
export function captainCanOf(v: Partial<CaptainCan> | null | undefined): CaptainCan {
  return { ...DEFAULT_CAPTAIN_CAN, ...(v ?? {}) };
}

export const CAPTAIN_CAN_OPTIONS: { key: keyof CaptainCan; label: string; hint: string }[] = [
  { key: "persona", label: "Edit persona", hint: "Name, tagline, description and system prompt." },
  { key: "access", label: "Propose access changes", hint: "Every change still waits for your Accept on a card." },
  { key: "routines", label: "Manage routines", hint: "Scheduled messages of this agent." },
];

/** captainPolicyOf is the Captain's stored auto-approve rule; anything
    unknown (or an older server) reads as off. */
export function captainPolicyOf(v: Partial<CaptainPolicy> | null | undefined): CaptainPolicy {
  const m = v?.auto_approve;
  return { auto_approve: m === "created" || m === "all" ? m : "off" };
}

export const AUTO_APPROVE_OPTIONS: { value: AutoApprove; label: string; hint: string }[] = [
  { value: "off", label: "Always ask me", hint: "Every access or tool change the Captain proposes waits for your Accept on a card." },
  { value: "created", label: "Auto-approve agents it creates", hint: "Access and tools (Bash, Edit, Write…) suggested when the Captain creates an agent apply at once. Later changes still ask." },
  { value: "all", label: "Auto-approve every proposal", hint: "Every proposal applies at once, for agents whose Settings › Captain allows access changes." },
];

export const AUTO_APPROVE_NOTE =
  "Auto-approved changes still never exceed your own access, and each one is listed in the agent's Access history and announced in its chat.";

export const CAPTAIN_ACCESS_NOTE =
  "Access never changes without you: the Captain can only propose it, you accept or decline on a card, and it can never exceed your own access.";

export const MANAGE_AGENTS_NOTE =
  "Lets this agent list, create and edit your other agents and propose access changes for you to approve. Its sub-agents never get it.";

/** historyLine is one row as "Notion (read), -Slack": the diff, joined. */
export function historyLine(it: AccessHistoryItem): string {
  return (it.diff ?? []).join(", ") || "no visible change";
}

/** historyStatus labels a row's status and who decided a proposal. */
export function historyStatus(it: AccessHistoryItem): string {
  switch (it.status) {
    case "pending":
      return "waiting for approval";
    case "declined":
      return it.decided_by ? `declined by ${it.decided_by}` : "declined";
    default:
      return it.decided_by ? `approved by ${it.decided_by}` : "applied";
  }
}

/* Captain is a role, not a fixed agent: the first Wick agent takes it and
   "Make Captain" (Settings › Captain) moves it. */

/** CAPTAIN_FACTS is the wizard's "What is a Captain?" list. */
export const CAPTAIN_FACTS = [
  "Coordinates your Team: answers when nobody else is @mentioned and hands tasks to the right agent.",
  "Can manage teammates' personas and routines (you control this in its Captain tab).",
  "One per Team, always a Wick agent, never shared.",
  "Not permanent: any other agent can take over via Settings → Captain → Make Captain.",
];

/** CAPTAIN_STARTER is the optional example persona the wizard fills in. */
export const CAPTAIN_STARTER = {
  name: "Captain",
  handle: "captain",
  description: "Lead agent that coordinates the Team.",
  system_prompt: "Lead agent: coordinates the Team, routes work to the right agent and keeps the user posted.",
};

/** REMOTE_NOT_CAPTAIN is said wherever a remote agent meets the role. */
export const REMOTE_NOT_CAPTAIN = "Remote agents (Slack / A2A) can join the Team, but cannot be the Captain.";

/** captainBlock says why `agent` can't take the Captain role, "" when it
    can. sharedCount is how many people it is shared with. */
export function captainBlock(agent: { kind?: string; role?: string }, sharedCount: number): string {
  if (agent.kind) return "A remote agent can't be the Captain — the Captain is always a Wick agent.";
  if (agent.role === "viewer") return "Only the owner can make this agent Captain.";
  if (sharedCount > 0) return "This agent is shared. The Captain runs your Team and can't be shared — stop sharing it first.";
  return "";
}
