import type { RosterStatus } from "./rosterStatus.js";
import type { TaskStatus } from "./delegations.js";

/* What another session's avatar shows while it is busy (mockup `.av`
   states): tool = wobble + orbit ring, remote = a slow dashed ring (wick
   waits on the other side), alert = an amber pulse ring (a question waits
   for the person). idle = static, the presence dot alone says it is there.
   thinking only labels the row: in the roster the live avatar acts out
   thinking and tools itself, and AvatarActivity adds just the remote and
   alert rings. The main chat on screen never animates here: its thread
   already shows the typing. */
export type AvatarActivity = "idle" | "thinking" | "tool" | "remote" | "alert";

/** rosterActivity reads a roster row's status. open = this agent's main
    chat is the one on screen (see mainChatOpen). */
export function rosterActivity(st: Pick<RosterStatus, "presence" | "attention" | "work">, open = false): AvatarActivity {
  if (open || st.presence === "disabled") return "idle";
  if (st.attention) return "alert";
  switch (st.work) {
    case "thinking":
      return "thinking";
    case "tool":
    case "subagent":
      return "tool";
    case "waiting":
      return "remote";
  }
  return "idle";
}

/** mainChatOpen: the roster row's status comes from the agent's main
    session, so only that chat on screen silences the row. Another chat of
    the same agent (route.session) or a group chat leaves its cues on. */
export function mainChatOpen(
  agent: { id: string; main_session_id?: string },
  selectedId: string | undefined,
  route: { session: string | null; group?: string | null },
): boolean {
  if (route.group || selectedId !== agent.id) return false;
  return !route.session || route.session === agent.main_session_id;
}

/** taskActivity reads a team task's status (delegation chips, task tray).
    answering = the sending agent is on the question, so the teammate only
    waits: static, like a settled task. */
export function taskActivity(s: TaskStatus): AvatarActivity {
  if (s === "needs_you") return "alert";
  if (s === "working") return "tool";
  return "idle";
}

