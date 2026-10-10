/* DetailView owns the Team tasks of the open chat; the thread's
   delegation blocks, deep inside ThreadMessage, read them through this
   context instead of a prop passed down every layer. Absent (the
   /sessions pages, tests) = no blocks. */
import { getContext, setContext } from "svelte";
import type { TeamTaskItem } from "./types/agents.js";

export type TeamTasksCtx = {
  /** The tasks a turn sent (tasksByTurn), [] for none. */
  forTurn: (turnId: string) => TeamTaskItem[];
  /** Team agents by handle, for the chips' avatars. */
  agents: () => Record<string, { name: string; kind?: string; shape?: string; color?: string; expression?: string }>;
  /** The agent this chat belongs to — "Captain is answering…" names it. */
  captainName: () => string;
  /** Absent in a read-only chat: the actions are hidden, not refused. */
  answer?: (taskId: string, text: string) => Promise<void>;
  cancel?: (taskId: string) => Promise<void>;
  /** Opens the teammate's chat the task runs in. */
  openChat?: (t: TeamTaskItem) => void;
};

/** Exported for tests that mount a block without DetailView. */
export const TEAM_TASKS_KEY = Symbol("team-tasks");
export const setTeamTasksCtx = (c: TeamTasksCtx) => setContext(TEAM_TASKS_KEY, c);
export const getTeamTasksCtx = (): TeamTasksCtx | undefined => getContext<TeamTasksCtx | undefined>(TEAM_TASKS_KEY);
