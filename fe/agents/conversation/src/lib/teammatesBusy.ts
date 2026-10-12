import type { TeamTaskItem } from "./types/agents.js";
import { taskStatus } from "./delegations.js";

/* "Waiting on teammates": the teammates this chat sent work to that are
   still on it. Counted per teammate (handle), not per task — two tasks to
   one teammate is one teammate working, the same count the channels use. */
export function workingTeammates(tasks: TeamTaskItem[]): string[] {
  const out: string[] = [];
  for (const t of tasks) {
    if (taskStatus(t) === "working" && t.to_handle && !out.includes(t.to_handle)) out.push(t.to_handle);
  }
  return out;
}

const teammates = (n: number) => `${n} ${n === 1 ? "teammate" : "teammates"}`;

/** Header chip: "working · waiting on 3 teammates". */
export function waitingHeaderLabel(n: number): string {
  return n > 0 ? `working · waiting on ${teammates(n)}` : "";
}

/** Tray line above the composer: "Captain is waiting on 2 teammates". */
export function waitingTrayLine(agentName: string, n: number): string {
  return n > 0 ? `${agentName || "Agent"} is waiting on ${teammates(n)}` : "";
}

export type StopDialog = { title: string; body: string; confirm: string };

/** The Stop confirmation when the agent still has sub-agents and/or
    teammates working. null = nothing in the background: stop at once. */
export function stopDialog(subAgents: number, teammatesWorking: number): StopDialog | null {
  if (subAgents <= 0 && teammatesWorking <= 0) return null;
  const parts: string[] = [];
  if (subAgents > 0) parts.push(`${subAgents} ${subAgents === 1 ? "sub-agent is" : "sub-agents are"} still working and will be stopped too.`);
  if (teammatesWorking > 0) parts.push(`${teammates(teammatesWorking)} still working will have their tasks canceled.`);
  const title =
    subAgents > 0 && teammatesWorking > 0
      ? "Stop this agent, its sub-agents and teammates' tasks?"
      : subAgents > 0
        ? "Stop this agent and its sub-agents?"
        : "Stop this agent and its teammates' tasks?";
  return { title, body: `${parts.join(" ")} The chat history is kept.`, confirm: "Stop all" };
}

/** B.3: the muted note on "Sent to <Channel>" when the agent's "Show
    teammates in channels" is off, so the owner knows what the channel saw. */
export function teammatesHiddenNote(channelLabel: string): string {
  const where = channelLabel.replace(/^(Sent|Sending|Not sent) to /, "").replace(/[…:].*$/, "").trim() || "the channel";
  return `teammates hidden in ${where}`;
}

export const TEAMMATES_HIDDEN_TIP = "Show teammates in channels is off for this agent — Advanced";
