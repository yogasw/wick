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

export type TodoItem = { id?: string; step: string; status: "pending" | "in_progress" | "completed" | string };

/** Parses a todo tool_use block's toolInput into its item list. Returns
    [] if toolInput is missing or malformed — never throws. */
export function parseTodoItems(block: Extract<ThreadBlock, { kind: "tool" }>): TodoItem[] {
  if (!block.toolInput) return [];
  try {
    const parsed = JSON.parse(block.toolInput) as { items?: TodoItem[] };
    return Array.isArray(parsed.items) ? parsed.items : [];
  } catch {
    return [];
  }
}

/** The key an item is tracked by across calls: its own id when the
    model supplied one (recommended — stable even if the step text gets
    reworded), else the step text itself (works for the common case
    where the model just re-sends the same wording with a new status,
    but two calls that both reword the same step will be treated as
    different items — an accepted tradeoff without a real id). */
function itemKey(it: TodoItem): string {
  return it.id?.trim() || it.step;
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
    if (block.kind === "tool" && block.toolName === "todo") {
      const items = parseTodoItems(block);
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
    shouldn't ALSO render as individual generic tool cards. */
export function stripTodoBlocks(blocks: ThreadBlock[]): ThreadBlock[] {
  return blocks.filter((b) => !(b.kind === "tool" && b.toolName === "todo"));
}

/** Progress summary for a merged item list — used for the "X/Y done"
    + current in_progress step the UI shows without expanding. */
export function todoProgress(items: TodoItem[]): { done: number; total: number; current: string | null } {
  const total = items.length;
  const done = items.filter((it) => it.status === "completed").length;
  const inProgress = items.find((it) => it.status === "in_progress");
  return { done, total, current: inProgress?.step ?? null };
}
