import { describe, test, expect } from "vitest";
import {
  bareToolName,
  latestTodoGoal,
  mergeTodoItems,
  mergeTodoItemsWithSteps,
  parseTodoGoal,
  parseTodoItems,
  stripTodoBlocks,
  todoProgress,
} from "../todoGroups.js";
import type { ThreadBlock } from "../types/agents.js";

type ToolBlock = Extract<ThreadBlock, { kind: "tool" }>;

function todoBlock(id: string, items: { id?: string; step: string; status: string }[]): ToolBlock {
  return { kind: "tool", toolUseId: id, toolName: "todo", toolInput: JSON.stringify({ items }) };
}

function toolBlock(id: string, name: string): ToolBlock {
  return { kind: "tool", toolUseId: id, toolName: name, toolInput: "{}" };
}

const thinking = (text: string): ThreadBlock => ({ kind: "thinking", text });

describe("parseTodoItems", () => {
  test("parses a well-formed todo input", () => {
    const items = parseTodoItems(todoBlock("t1", [{ step: "a", status: "pending" }]));
    expect(items).toEqual([{ step: "a", status: "pending" }]);
  });

  test("returns [] for malformed JSON", () => {
    const b: ToolBlock = { kind: "tool", toolUseId: "t1", toolName: "todo", toolInput: "not json" };
    expect(parseTodoItems(b)).toEqual([]);
  });

  test("returns [] for missing toolInput", () => {
    const b: ToolBlock = { kind: "tool", toolUseId: "t1", toolName: "todo", toolInput: "" };
    expect(parseTodoItems(b)).toEqual([]);
  });
});

describe("todoProgress", () => {
  test("counts done/total and finds the in_progress step", () => {
    const p = todoProgress([
      { step: "a", status: "completed" },
      { step: "b", status: "in_progress" },
      { step: "c", status: "pending" },
    ]);
    expect(p).toEqual({ done: 1, total: 3, current: "b" });
  });

  test("current is null when nothing is in_progress", () => {
    const p = todoProgress([{ step: "a", status: "completed" }]);
    expect(p.current).toBeNull();
  });

  test("empty list", () => {
    expect(todoProgress([])).toEqual({ done: 0, total: 0, current: null });
  });
});

describe("mergeTodoItems", () => {
  test("a single todo call returns its own items in order", () => {
    const t1 = todoBlock("t1", [
      { id: "1", step: "a", status: "pending" },
      { id: "2", step: "b", status: "pending" },
    ]);
    expect(mergeTodoItems([t1])).toEqual([
      { id: "1", step: "a", status: "pending" },
      { id: "2", step: "b", status: "pending" },
    ]);
  });

  test("later calls update earlier items' status by id, keeping original order", () => {
    const t1 = todoBlock("t1", [
      { id: "1", step: "a", status: "pending" },
      { id: "2", step: "b", status: "pending" },
    ]);
    const t2 = todoBlock("t2", [
      { id: "1", step: "a", status: "completed" },
      { id: "2", step: "b", status: "in_progress" },
    ]);
    const merged = mergeTodoItems([t1, toolBlock("s1", "shell"), t2]);
    expect(merged).toEqual([
      { id: "1", step: "a", status: "completed" },
      { id: "2", step: "b", status: "in_progress" },
    ]);
  });

  test("falls back to matching by step text when no id is given", () => {
    const t1 = todoBlock("t1", [{ step: "delay 1 minute", status: "in_progress" }]);
    const t2 = todoBlock("t2", [{ step: "delay 1 minute", status: "completed" }]);
    const merged = mergeTodoItems([t1, t2]);
    expect(merged).toEqual([{ step: "delay 1 minute", status: "completed" }]);
  });

  test("a step present in an earlier call but dropped from a later call keeps its last known status", () => {
    const t1 = todoBlock("t1", [
      { id: "1", step: "a", status: "completed" },
      { id: "2", step: "b", status: "pending" },
    ]);
    // Second call only re-sends item 2 — item 1 isn't repeated.
    const t2 = todoBlock("t2", [{ id: "2", step: "b", status: "in_progress" }]);
    const merged = mergeTodoItems([t1, t2]);
    expect(merged).toEqual([
      { id: "1", step: "a", status: "completed" },
      { id: "2", step: "b", status: "in_progress" },
    ]);
  });

  test("a new item introduced in a later call is appended, not inserted at the front", () => {
    const t1 = todoBlock("t1", [{ id: "1", step: "a", status: "completed" }]);
    const t2 = todoBlock("t2", [
      { id: "1", step: "a", status: "completed" },
      { id: "2", step: "b", status: "pending" },
    ]);
    const merged = mergeTodoItems([t1, t2]);
    expect(merged.map((it) => it.id)).toEqual(["1", "2"]);
  });

  test("no todo calls returns an empty list", () => {
    expect(mergeTodoItems([thinking("hi"), toolBlock("s1", "shell")])).toEqual([]);
  });
});

describe("mergeTodoItemsWithSteps", () => {
  test("attributes blocks between two todo calls to the in_progress item at the opening call", () => {
    const t1 = todoBlock("t1", [
      { id: "1", step: "a", status: "in_progress" },
      { id: "2", step: "b", status: "pending" },
    ]);
    const shellCall = toolBlock("s1", "shell");
    const readCall = toolBlock("r1", "read_file");
    const t2 = todoBlock("t2", [
      { id: "1", step: "a", status: "completed" },
      { id: "2", step: "b", status: "in_progress" },
    ]);
    const writeCall = toolBlock("w1", "write_file");

    const result = mergeTodoItemsWithSteps([t1, shellCall, readCall, t2, writeCall]);

    expect(result).toHaveLength(2);
    expect(result[0].item).toEqual({ id: "1", step: "a", status: "completed" });
    expect(result[0].relatedBlocks).toEqual([shellCall, readCall]);
    expect(result[1].item).toEqual({ id: "2", step: "b", status: "in_progress" });
    expect(result[1].relatedBlocks).toEqual([writeCall]);
  });

  test("no in_progress item in a span: blocks are dropped, not misattributed", () => {
    const t1 = todoBlock("t1", [{ id: "1", step: "a", status: "pending" }]);
    const shellCall = toolBlock("s1", "shell");
    const result = mergeTodoItemsWithSteps([t1, shellCall]);
    expect(result[0].relatedBlocks).toEqual([]);
  });

  test("blocks before the first todo call are not attributed to anything", () => {
    const lead = thinking("intro");
    const t1 = todoBlock("t1", [{ id: "1", step: "a", status: "in_progress" }]);
    const result = mergeTodoItemsWithSteps([lead, t1]);
    expect(result).toHaveLength(1);
    expect(result[0].relatedBlocks).toEqual([]);
  });

  test("mergeTodoItems is equivalent to mapping mergeTodoItemsWithSteps to just the item", () => {
    const t1 = todoBlock("t1", [{ id: "1", step: "a", status: "in_progress" }]);
    const t2 = todoBlock("t2", [{ id: "1", step: "a", status: "completed" }]);
    expect(mergeTodoItems([t1, t2])).toEqual(mergeTodoItemsWithSteps([t1, t2]).map((s) => s.item));
  });
});

describe("stripTodoBlocks", () => {
  test("removes todo tool_use blocks but keeps everything else", () => {
    const t1 = todoBlock("t1", [{ step: "a", status: "pending" }]);
    const shellCall = toolBlock("s1", "shell");
    const think = thinking("hi");
    const out = stripTodoBlocks([t1, shellCall, think]);
    expect(out).toEqual([shellCall, think]);
  });

  test("no-op when there are no todo blocks", () => {
    const blocks = [thinking("a"), toolBlock("s1", "shell")];
    expect(stripTodoBlocks(blocks)).toEqual(blocks);
  });
});

/* ── MCP-namespaced tool names ─────────────────────────────────────────
   Providers reaching wick over MCP report the tool as mcp__<server>__todo
   (e.g. a Slack channel session), while the in-process wick provider
   reports a bare "todo". Both must be treated identically — otherwise the
   namespaced calls miss the checklist AND leak into the flat trace. */
describe("bareToolName", () => {
  test("strips an mcp server namespace", () => {
    expect(bareToolName("mcp__wick__todo")).toBe("todo");
  });

  test("leaves a bare name untouched", () => {
    expect(bareToolName("todo")).toBe("todo");
  });

  test("strips only the server segment, keeping underscores in the tool name", () => {
    expect(bareToolName("mcp__wick__wick_search")).toBe("wick_search");
  });

  test("leaves a non-mcp name with underscores untouched", () => {
    expect(bareToolName("write_file")).toBe("write_file");
  });
});

describe("namespaced todo blocks", () => {
  function nsTodoBlock(id: string, items: { id?: string; step: string; status: string }[]): ToolBlock {
    return { kind: "tool", toolUseId: id, toolName: "mcp__wick__todo", toolInput: JSON.stringify({ items }) };
  }

  test("mcp__wick__todo is merged into the checklist", () => {
    const t1 = nsTodoBlock("t1", [{ id: "1", step: "a", status: "in_progress" }]);
    expect(mergeTodoItems([t1])).toEqual([{ id: "1", step: "a", status: "in_progress" }]);
  });

  test("mcp__wick__todo is stripped from the flat trace", () => {
    const t1 = nsTodoBlock("t1", [{ step: "a", status: "pending" }]);
    const shellCall = toolBlock("s1", "shell");
    expect(stripTodoBlocks([t1, shellCall])).toEqual([shellCall]);
  });

  test("bare and namespaced calls merge into one list, not two", () => {
    const bare = todoBlock("t1", [{ id: "1", step: "a", status: "pending" }]);
    const ns = nsTodoBlock("t2", [{ id: "1", step: "a", status: "completed" }]);
    expect(mergeTodoItems([bare, ns])).toEqual([{ id: "1", step: "a", status: "completed" }]);
  });

  test("related blocks attribute across a namespaced todo call", () => {
    const t1 = nsTodoBlock("t1", [{ id: "1", step: "a", status: "in_progress" }]);
    const shellCall = toolBlock("s1", "shell");
    const result = mergeTodoItemsWithSteps([t1, shellCall]);
    expect(result[0].relatedBlocks).toEqual([shellCall]);
  });
});

/* ── goal mode ────────────────────────────────────────────────────────
   The same tool carries the checklist and the durable goal latch. A
   goal-only call has no items at all. */
describe("parseTodoGoal", () => {
  function goalBlock(input: Record<string, unknown>): ToolBlock {
    return { kind: "tool", toolUseId: "g1", toolName: "mcp__wick__todo", toolInput: JSON.stringify(input) };
  }

  test("reads an opened goal with its criterion", () => {
    expect(parseTodoGoal(goalBlock({ goal: "find 3 houses", items: [] }))).toEqual({
      status: "open",
      goal: "find 3 houses",
      note: undefined,
    });
  });

  test("reads goal_done", () => {
    expect(parseTodoGoal(goalBlock({ goal_done: true, note: "found them" }))).toEqual({
      status: "done",
      note: "found them",
    });
  });

  test("reads goal_abandon", () => {
    expect(parseTodoGoal(goalBlock({ goal_abandon: true }))?.status).toBe("abandoned");
  });

  test("goal_done wins over a goal string in the same call, matching the server", () => {
    expect(parseTodoGoal(goalBlock({ goal: "x", goal_done: true }))?.status).toBe("done");
  });

  test("returns null for a checklist-only call", () => {
    expect(parseTodoGoal(todoBlock("t1", [{ step: "a", status: "pending" }]))).toBeNull();
  });

  test("returns null for malformed json", () => {
    const b: ToolBlock = { kind: "tool", toolUseId: "g1", toolName: "todo", toolInput: "not json" };
    expect(parseTodoGoal(b)).toBeNull();
  });

  test("ignores a blank goal string", () => {
    expect(parseTodoGoal(goalBlock({ goal: "   " }))).toBeNull();
  });
});

describe("latestTodoGoal", () => {
  function goalBlock(id: string, input: Record<string, unknown>): ToolBlock {
    return { kind: "tool", toolUseId: id, toolName: "mcp__wick__todo", toolInput: JSON.stringify(input) };
  }

  test("null when no call touched the goal fields", () => {
    expect(latestTodoGoal([todoBlock("t1", [{ step: "a", status: "pending" }])])).toBeNull();
  });

  test("a later goal_done supersedes the earlier open state", () => {
    const open = goalBlock("g1", { goal: "find 3 houses" });
    const done = goalBlock("g2", { goal_done: true });
    expect(latestTodoGoal([open, done])?.status).toBe("done");
  });

  test("carries the opening criterion forward, since goal_done omits it", () => {
    const open = goalBlock("g1", { goal: "find 3 houses" });
    const done = goalBlock("g2", { goal_done: true });
    expect(latestTodoGoal([open, done])?.goal).toBe("find 3 houses");
  });

  test("a goal-only trace yields a goal but no checklist items", () => {
    const only = goalBlock("g1", { goal_done: true, note: "done" });
    expect(latestTodoGoal([only])?.status).toBe("done");
    expect(mergeTodoItems([only])).toEqual([]);
  });

  test("a goal-only call between checklist calls doesn't reset item attribution", () => {
    const t1 = todoBlock("t1", [{ id: "1", step: "a", status: "in_progress" }]);
    const goalOnly = goalBlock("g1", { goal: "keep going" });
    const shellCall = toolBlock("s1", "shell");
    const result = mergeTodoItemsWithSteps([t1, goalOnly, shellCall]);
    expect(result).toHaveLength(1);
    expect(result[0].relatedBlocks).toEqual([shellCall]);
  });
});
