import { describe, test, expect } from "vitest";
import { quickReplies, settledLine, statusSummary, taskRailBadge, tasksByTurn, teamTaskIdsFromEvents, taskStatus, statusLabel, taskLabel } from "../delegations.js";
import type { ConversationTurn, TeamTaskItem } from "../types/agents.js";

const task = (o: Partial<TeamTaskItem>): TeamTaskItem => ({
  task_id: "t1", context_id: "c", to_agent_id: "a", to_handle: "anton", to_name: "Anton", title: "x",
  state: "working", turns: 1, max_turns: 8, started_at: "2026-10-10T10:00:00Z", updated_at: "2026-10-10T10:00:00Z", ...o,
});
const turn = (id: string, role: string, ts: string, events: ConversationTurn["events"] = []): ConversationTurn =>
  ({ turn_id: id, role, agent: "", provider: "", text: "", ts, timestamp: 0, truncated: false, interrupted: false, has_trace: false, events, attachments: [] }) as ConversationTurn;

describe("delegations", () => {
  test("reads task ids from team_message results, MCP-wrapped too", () => {
    const ids = teamTaskIdsFromEvents([
      { type: "tool_use", tool_use_id: "u1", tool_name: "mcp__wick__team_message" },
      { type: "tool_result", tool_use_id: "u1", text: '[{"type":"text","text":"{\\"task_id\\":\\"t9\\",\\"state\\":\\"working\\"}"}]' },
      { type: "tool_use", tool_use_id: "u2", tool_name: "Bash" },
      { type: "tool_result", tool_use_id: "u2", text: '{"task_id":"nope"}' },
    ] as ConversationTurn["events"]);
    expect(ids).toEqual(["t9"]);
  });

  test("places a task by its turn's events, else by the user turns around it", () => {
    const turns = [
      turn("u1", "user", "2026-10-10T09:59:00Z"),
      turn("a1", "assistant", "2026-10-10T10:01:00Z", [
        { type: "tool_use", tool_use_id: "x", tool_name: "team_message" },
        { type: "tool_result", tool_use_id: "x", text: '{"task_id":"late"}' },
      ] as ConversationTurn["events"]),
      turn("u2", "user", "2026-10-10T10:05:00Z"),
      turn("a2", "assistant", "2026-10-10T10:06:00Z"),
    ];
    const m = tasksByTurn(turns, [
      task({ task_id: "early", started_at: "2026-10-10T10:00:00Z" }),
      task({ task_id: "late", started_at: "2026-10-10T10:07:00Z" }),
      task({ task_id: "next", started_at: "2026-10-10T10:05:30Z" }),
    ]);
    expect(m.get("a1")?.map((t) => t.task_id)).toEqual(["late", "early"]);
    expect(m.get("a2")?.map((t) => t.task_id)).toEqual(["next"]);
  });

  test("input_required splits into needs you and captain answering", () => {
    expect(taskStatus(task({ state: "input_required", needs_you: true }))).toBe("needs_you");
    expect(taskStatus(task({ state: "input_required" }))).toBe("answering");
    expect(statusSummary([task({}), task({ state: "input_required" }), task({ state: "input_required", needs_you: true }), task({ state: "completed" })])).toBe(
      "2 working · 1 needs input · 1 replied",
    );
    expect(settledLine([task({ state: "completed", turns: 2 })])).toBe("Delegated to 1 teammate · all replied · 2 messages");
  });

  test("the Tasks rail badge counts active tasks and turns amber on needs you", () => {
    expect(taskRailBadge([task({}), task({ state: "completed" })])).toEqual({ count: 1, needsYou: false });
    expect(taskRailBadge([task({ state: "input_required", needs_you: true })])).toEqual({ count: 1, needsYou: true });
    expect(taskRailBadge([task({ state: "failed" })])).toEqual({ count: 0, needsYou: false });
  });

  test("quick replies only for a short option list", () => {
    expect(quickReplies("Which environment? (prod / staging)")).toEqual(["prod", "staging"]);
    expect(quickReplies("What should the report say about the outage?")).toEqual([]);
  });
  test("answering names the agent that sent the task", () => {
    expect(statusLabel(taskStatus(task({ state: "input_required" })), "Anton")).toBe("Anton is answering");
    expect(statusLabel(taskStatus(task({ state: "input_required", needs_you: true })), "Anton")).toBe("needs you");
    expect(taskLabel(task({ state: "failed", interrupted: true }), "Anton")).toBe("interrupted by a restart");
    expect(taskLabel(task({ state: "failed" }), "Anton")).toBe("failed");
    expect(statusLabel("replied", "Anton")).toBe("replied");
  });
});
