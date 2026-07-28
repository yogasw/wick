/*
 * Purpose:    Merges every "todo" tool call in a trace into ONE
 *             checklist state, instead of rendering a separate
 *             checklist card per call. A long turn can call todo many
 *             times (checkpoint after checkpoint); without merging that
 *             stacked N nearly-identical cards, one per call, which
 *             reads as noise rather than progress. Also strips todo
 *             tool_use blocks out of the flat trace so they don't ALSO
 *             show up as a generic ToolCard alongside the merged widget.
 * Caller:     ThreadMessage.svelte / ConversationThread.svelte's trace
 *             renderers
 * Dependencies: ThreadBlock
 */

import type { ThreadBlock } from "./types/agents.js";

export type TodoSubstep = { step: string; status: "pending" | "in_progress" | "completed" | string };

/** Strips an MCP namespace prefix off a tool name. Providers that reach
    wick over MCP report the tool as `mcp__<server>__todo` (e.g.
    `mcp__wick__todo` from a channel session), while the in-process wick
    provider reports a bare `todo`. Both are the same tool, so every
    todo-vs-not check has to compare the bare name — otherwise the
    namespaced calls miss the merged checklist AND leak into the flat
    trace as generic tool cards. */
export function bareToolName(name: string): string {
  const m = /^mcp__.+?__(.+)$/.exec(name);
  return m ? m[1] : name;
}

/** True when a trace block is a todo tool call, under either the bare or
    the MCP-namespaced name. */
export function isTodoBlock(b: ThreadBlock): b is Extract<ThreadBlock, { kind: "tool" }> {
  return b.kind === "tool" && bareToolName(b.toolName) === "todo";
}

export type TodoItem = {
  id?: string;
  /** Short checklist label. `step` is the deprecated pre-nested-todo field
      name — kept as a fallback so older recorded traces still render. */
  title?: string;
  step?: string;
  /** 1-2 sentence explanation shown when the item is expanded. */
  description?: string;
  status: "pending" | "in_progress" | "completed" | string;
  /** Optional concrete actions under this task — its own nested checklist. */
  substeps?: TodoSubstep[];
};

/** The label to render for an item — title if the model set one, else the
    deprecated step text. */
export function itemLabel(it: TodoItem): string {
  return it.title?.trim() || it.step?.trim() || "";
}

/** Parses a todo tool_use block's toolInput into its item list. Returns
    [] if toolInput is missing or malformed — never throws. A goal-only
    call (goal / goal_done / goal_abandon with no checklist) legitimately
    carries no items and yields [] here; see parseTodoGoal. */
export function parseTodoItems(block: Extract<ThreadBlock, { kind: "tool" }>): TodoItem[] {
  if (!block.toolInput) return [];
  try {
    const parsed = JSON.parse(block.toolInput) as { items?: TodoItem[] };
    return Array.isArray(parsed.items) ? parsed.items : [];
  } catch {
    return [];
  }
}

/** The goal latch state a todo call reported, or null if it touched no
    goal fields. The same tool carries both the checklist and the latch,
    so one call can produce items AND a goal event. */
export type TodoGoal = {
  status: "open" | "done" | "abandoned";
  /** Success criterion — only present when the call opened the latch. */
  goal?: string;
  note?: string;
};

/** Extracts the goal latch event from a todo tool_use block, or null when
    the call was checklist-only. `goal_abandon` and `goal_done` win over a
    `goal` string in the same call, matching the server's switch order in
    internal/mcp/handlers/todo.go. */
export function parseTodoGoal(block: Extract<ThreadBlock, { kind: "tool" }>): TodoGoal | null {
  if (!block.toolInput) return null;
  let parsed: { goal?: unknown; goal_done?: unknown; goal_abandon?: unknown; note?: unknown };
  try {
    parsed = JSON.parse(block.toolInput);
  } catch {
    return null;
  }
  const note = typeof parsed.note === "string" && parsed.note.trim() ? parsed.note.trim() : undefined;
  if (parsed.goal_abandon === true) return { status: "abandoned", note };
  if (parsed.goal_done === true) return { status: "done", note };
  const goal = typeof parsed.goal === "string" ? parsed.goal.trim() : "";
  if (goal) return { status: "open", goal, note };
  return null;
}

/** The latest goal latch state across a trace, or null if no todo call in
    it touched the goal fields. Later calls supersede earlier ones, so an
    opened-then-completed goal reports "done". The opening call's criterion
    text is carried forward, since goal_done calls don't repeat it. */
export function latestTodoGoal(blocks: ThreadBlock[]): TodoGoal | null {
  let current: TodoGoal | null = null;
  let criterion = "";
  for (const b of blocks) {
    if (!isTodoBlock(b)) continue;
    const g = parseTodoGoal(b);
    if (!g) continue;
    if (g.goal) criterion = g.goal;
    current = { ...g, goal: g.goal ?? (criterion || undefined) };
  }
  return current;
}

/** The key an item is tracked by across calls: its own id when the
    model supplied one (recommended — stable even if the step text gets
    reworded), else the step text itself (works for the common case
    where the model just re-sends the same wording with a new status,
    but two calls that both reword the same step will be treated as
    different items — an accepted tradeoff without a real id). */
function itemKey(it: TodoItem): string {
  return it.id?.trim() || itemLabel(it);
}

/** Merges every todo call's items into one ordered list — the latest
    status for each item (by itemKey), in the order items were first
    introduced. A step whose latest call dropped it (the model stopped
    listing it) is kept at its last known status rather than vanishing,
    since a completed step disappearing would look like the work was
    undone. */
export function mergeTodoItems(blocks: ThreadBlock[]): TodoItem[] {
  return mergeTodoItemsWithSteps(blocks).map((s) => s.item);
}

export type TodoItemWithSteps = { item: TodoItem; relatedBlocks: ThreadBlock[] };

/** Same merge as mergeTodoItems, but each item also carries the trace
    blocks (tool calls, thinking) that happened while it was the
    in_progress step — the work the agent did "for" that item, so
    expanding one item shows what it actually involved.
    Association heuristic: between one todo call and the next, whichever
    item was in_progress in the call that OPENED that span owns every
    non-todo block in it (assumes a linear list — one in_progress item at
    a time, which is the tool's stated usage pattern). Blocks before the
    first todo call, or in a span with no in_progress item, are dropped
    (nothing to attribute them to) — the flat trace still shows them via
    stripTodoBlocks, they're just not duplicated under an item. */
export function mergeTodoItemsWithSteps(blocks: ThreadBlock[]): TodoItemWithSteps[] {
  const order: string[] = [];
  const byKey = new Map<string, TodoItem>();
  const relatedByKey = new Map<string, ThreadBlock[]>();

  let activeKey: string | null = null;
  for (const block of blocks) {
    if (isTodoBlock(block)) {
      const items = parseTodoItems(block);
      // A goal-only call carries no checklist. Leave the active item
      // alone so blocks after it still attribute to whatever step was
      // already running, instead of being dropped.
      if (items.length === 0) continue;
      for (const it of items) {
        const key = itemKey(it);
        if (!byKey.has(key)) {
          order.push(key);
          relatedByKey.set(key, []);
        }
        byKey.set(key, it);
      }
      const inProgress = items.find((it) => it.status === "in_progress");
      activeKey = inProgress ? itemKey(inProgress) : null;
    } else if (activeKey) {
      relatedByKey.get(activeKey)?.push(block);
    }
  }

  return order.map((k) => ({ item: byKey.get(k)!, relatedBlocks: relatedByKey.get(k) ?? [] }));
}

/** Trace blocks with every "todo" tool_use stripped out — the merged
    checklist widget (built from mergeTodoItems) replaces them, so they
    shouldn't ALSO render as individual generic tool cards. Matches the
    MCP-namespaced name too, so `mcp__wick__todo` calls don't leak
    through as raw tool cards. */
export function stripTodoBlocks(blocks: ThreadBlock[]): ThreadBlock[] {
  return blocks.filter((b) => !isTodoBlock(b));
}

/** Progress summary for a merged item list — used for the "X/Y done"
    + current in_progress step the UI shows without expanding. */
export function todoProgress(items: TodoItem[]): { done: number; total: number; current: string | null } {
  const total = items.length;
  const done = items.filter((it) => it.status === "completed").length;
  const inProgress = items.find((it) => it.status === "in_progress");
  return { done, total, current: inProgress ? itemLabel(inProgress) : null };
}
