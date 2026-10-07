import { bareToolName } from "./todoGroups.js";

/* What an agent is doing, in the words the thread's working bubble, the
   Team header and the roster row all use, so the three never disagree. */

const TOOL_LABELS: Record<string, string> = {
  write_file: "writing file…",
  read_file: "reading file…",
  edit_file: "editing file…",
  shell: "running command…",
  todo: "updating task list…",
  ask_user: "waiting for your input…",
  wick_list: "listing…",
  wick_search: "searching…",
  wick_schedule_message: "scheduling message…",
  wick_delegate: "delegating to a sub-agent…",
  wick_agents: "checking available sub-agents…",
};

/** The label of a turn with nothing in flight. */
export const THINKING_LABEL = "thinking…";

/** toolActivityLabel names a tool call: a friendly phrase for the known
    ones, "running <name>…" for the rest. MCP-namespaced names
    (mcp__wick__todo) match on their bare name. */
export function toolActivityLabel(toolName: string): string {
  const bare = bareToolName(toolName);
  return TOOL_LABELS[bare] ?? `running ${bare}…`;
}

/** activityLabel is the label of a running turn from its live substate
    and the tool it waits on. */
export function activityLabel(substate?: string, toolName?: string): string {
  if (toolName) return toolActivityLabel(toolName);
  if (!substate || substate === "thinking" || substate === "idle") return THINKING_LABEL;
  if (substate === "spawning") return "spawning…";
  // "running_tool" is the backend's generic lifecycle substate for "a tool
  // is executing" (agents/state.State.String()) — it's not a tool name.
  // It normally arrives together with a tool_use event that fills in
  // toolName above; this is only the fallback for a race where the
  // lifecycle ping lands before that event, so it must not render as
  // "running running_tool…".
  if (substate === "running_tool") return "running a tool…";
  return `running ${substate}…`;
}
