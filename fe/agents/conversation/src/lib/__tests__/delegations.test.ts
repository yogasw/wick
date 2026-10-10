import { describe, test, expect, vi } from "vitest";
import { blockHead, chipElapsed, chipLabel, clockTime, foldAvatars, formatDuration, groupTasks, settledParts, statusParts, isUserTask, userTaskRoute, quickReplies, statusSummary, taskRailBadge, tasksByTurn, teamTaskIdsFromEvents, taskStatus, statusLabel, taskLabel, turnSpan } from "../delegations.js";
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
    expect(settledParts([task({ state: "completed", turns: 2 })])).toMatchObject({ head: "Delegated to 1 teammate", allReplied: true, messages: "2 messages" });
  });

  test("the Tasks rail marks: active count (never amber), needs-input count, working ring", () => {
    expect(taskRailBadge([task({}), task({ state: "completed" })])).toEqual({ count: 1, needsInput: 0, working: true });
    expect(taskRailBadge([task({ state: "input_required", needs_you: true })])).toEqual({ count: 1, needsInput: 1, working: false });
    expect(taskRailBadge([task({ state: "failed" })])).toEqual({ count: 0, needsInput: 0, working: false });
    // The mockup's rail: one working, one waiting on the person.
    expect(taskRailBadge([task({}), task({ task_id: "t2", state: "input_required", needs_you: true })])).toEqual({ count: 2, needsInput: 1, working: true });
    // The teammate's own sender answering is not the person's turn, and not working either.
    expect(taskRailBadge([task({ state: "input_required" })]).needsInput).toBe(0);
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

  test("P45: the person's own task needs them at once, never 'answering'", () => {
    const mine = task({ origin: "user", state: "input_required", needs_you: false });
    expect(isUserTask(mine)).toBe(true);
    expect(taskStatus(mine)).toBe("needs_you");
    expect(taskLabel(mine, "Cap")).toBe("needs you");
    expect(userTaskRoute(mine)).toBe("You → @anton");
    const agents = task({ state: "input_required", needs_you: false });
    expect(isUserTask(agents)).toBe(false);
    expect(taskStatus(agents)).toBe("answering");
    expect(userTaskRoute(agents)).toBeNull();
    expect(taskStatus(task({ origin: "user", state: "completed" }))).toBe("replied");
  });
});

describe("blockHead (P45 N5)", () => {
  test("names the agent's and the person's tasks apart", () => {
    expect(blockHead([task({}), task({})])).toBe("Delegated to 2 teammates");
    expect(blockHead([task({ origin: "user" })])).toBe("You asked 1 teammate");
    expect(blockHead([task({}), task({ origin: "user" })])).toBe("Delegated to 1 teammate · You asked 1");
    expect(settledParts([task({ origin: "user", state: "completed" })])).toMatchObject({ head: "You asked 1 teammate", allReplied: true, messages: "1 message" });
  });
});

describe("P48 helpers", () => {
  const now = Date.parse("2026-10-10T10:05:10Z");

  test("rejected reads as declined, its own summary part", () => {
    const d = task({ state: "rejected" });
    expect(taskStatus(d)).toBe("declined");
    expect(taskLabel(d, "Cap")).toBe("declined");
    expect(statusSummary([d, task({ state: "failed" })])).toBe("1 declined · 1 failed");
  });

  test("statusParts keeps each status apart, answering counted as working", () => {
    expect(
      statusParts([task({}), task({ state: "input_required" }), task({ state: "input_required", needs_you: true }), task({ state: "completed" }), task({ state: "canceled" })]),
    ).toEqual([
      { status: "working", count: 2, label: "2 working" },
      { status: "needs_you", count: 1, label: "1 needs input" },
      { status: "replied", count: 1, label: "1 replied" },
      { status: "canceled", count: 1, label: "1 canceled" },
    ]);
    expect(statusParts([])).toEqual([]);
  });

  test("chipLabel says needs input, the rest like taskLabel", () => {
    expect(chipLabel(task({ state: "input_required", needs_you: true }), "Cap")).toBe("needs input");
    expect(chipLabel(task({ state: "input_required" }), "Cap")).toBe("Cap is answering");
    expect(chipLabel(task({}), "Cap")).toBe("working");
    expect(chipLabel(task({ state: "failed", interrupted: true }), "Cap")).toBe("interrupted by a restart");
  });

  test("chipElapsed: timer while working or asking, clock time once ended", () => {
    expect(chipElapsed(task({ started_at: "2026-10-10T10:03:00Z" }), now)).toBe("2m 10s");
    expect(chipElapsed(task({ started_at: "2026-10-10T10:05:00Z" }), now)).toBe("10s");
    expect(chipElapsed(task({ started_at: "2026-10-10T08:00:00Z" }), now)).toBe("2h 5m");
    expect(chipElapsed(task({ state: "input_required", needs_you: true, updated_at: "2026-10-10T10:04:00Z" }), now)).toBe("1m 10s");
    const end = "2026-10-10T10:08:00Z";
    expect(chipElapsed(task({ state: "completed", updated_at: end }), now)).toBe(clockTime(Date.parse(end)));
    expect(clockTime(new Date(2026, 9, 10, 9, 7).getTime())).toBe("09:07");
    expect(clockTime(NaN)).toBe("");
  });

  test("settledParts adds the block's duration", () => {
    const p = settledParts([
      task({ state: "completed", turns: 5, started_at: "2026-10-10T09:31:00Z", updated_at: "2026-10-10T09:40:00Z" }),
      task({ state: "completed", turns: 7, started_at: "2026-10-10T09:32:00Z", updated_at: "2026-10-10T09:44:00Z" }),
    ]);
    expect(p).toEqual({ head: "Delegated to 2 teammates", allReplied: true, parts: [{ status: "replied", count: 2, label: "2 replied" }], messages: "12 messages", duration: "13m" });
    const q = settledParts([task({ state: "rejected", turns: 0, started_at: "2026-10-10T09:00:00Z", updated_at: "2026-10-10T10:05:00Z" })]);
    expect(q.allReplied).toBe(false);
    expect(q.parts.map((x) => x.label)).toEqual(["1 declined"]);
    expect(q.messages).toBe("");
    expect(q.duration).toBe("1h 5m");
    expect(formatDuration(3_600_000)).toBe("1h");
    expect(formatDuration(40_000)).toBe("40s");
  });

  test("settledParts prefers ended_at and message_count once the server sends them", () => {
    const p = settledParts([
      task({ state: "completed", turns: 1, message_count: 4, started_at: "2026-10-10T09:31:00Z", updated_at: "2026-10-10T11:00:00Z", ended_at: "2026-10-10T09:40:00Z" }),
      task({ state: "completed", turns: 2, started_at: "2026-10-10T09:32:00Z", updated_at: "2026-10-10T09:44:00Z" }),
    ]);
    expect(p.messages).toBe("6 messages");
    expect(p.duration).toBe("13m");
  });

  test("turnSpan: first start to the last end or the turn's commit, local HH:MM", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date(2026, 9, 10, 10, 0));
    try {
      const at = (h: number, m: number) => new Date(2026, 9, 10, h, m).toISOString();
      const tasks = [
        task({ state: "completed", started_at: at(9, 31), updated_at: at(9, 40) }),
        task({ state: "completed", started_at: at(9, 33), updated_at: at(9, 41), ended_at: at(9, 44) }),
      ];
      expect(turnSpan(tasks)).toBe("09:31 – 09:44");
      expect(turnSpan(tasks, new Date(2026, 9, 10, 9, 50).getTime())).toBe("09:31 – 09:50");
      expect(turnSpan([task({ state: "completed", started_at: at(9, 31), updated_at: at(9, 31) })])).toBe("09:31");
      expect(turnSpan([task({ state: "completed", started_at: "", updated_at: "" })], new Date(2026, 9, 10, 9, 5).getTime())).toBe("09:05");
      expect(turnSpan([task({ state: "completed", started_at: "", updated_at: "" })])).toBe("");
      // Live: a task still runs (or waits on a question), so no end yet.
      const live = [tasks[0], task({ state: "working", started_at: at(9, 35), updated_at: at(9, 58) })];
      expect(turnSpan(live, new Date(2026, 9, 10, 9, 36).getTime())).toBe("09:31");
      expect(turnSpan([task({ state: "input_required", needs_you: true, started_at: at(10, 2), updated_at: at(10, 7) })])).toBe("10:02");
    } finally {
      vi.useRealTimers();
    }
  });

  test("foldAvatars: one per teammate, first-asked order", () => {
    const av = foldAvatars([
      task({ task_id: "1", to_agent_id: "b", to_handle: "bob", to_name: "Bob" }),
      task({ task_id: "2", to_agent_id: "a", to_handle: "anton", to_name: "Anton" }),
      task({ task_id: "3", to_agent_id: "b", to_handle: "bob", to_name: "Bob" }),
    ]);
    expect(av).toEqual([
      { to_agent_id: "b", to_handle: "bob", to_name: "Bob" },
      { to_agent_id: "a", to_handle: "anton", to_name: "Anton" },
    ]);
    const noId = foldAvatars([
      task({ to_agent_id: "", to_handle: "bob", to_name: "Bob" }),
      task({ to_agent_id: "", to_handle: "luna", to_name: "Luna" }),
      task({ to_agent_id: "", to_handle: "bob", to_name: "Bob" }),
    ]);
    expect(noId.map((a) => a.to_handle)).toEqual(["bob", "luna"]);
  });

  test("groupTasks: new labels, Done today vs a folded Earlier, declined with failed", () => {
    const noon = new Date(2026, 9, 10, 12, 0).getTime();
    const today = new Date(2026, 9, 10, 0, 30).toISOString();
    const yesterday = new Date(2026, 9, 9, 23, 30).toISOString();
    const groups = groupTasks(
      [
        task({ task_id: "w" }),
        task({ task_id: "n", state: "input_required", needs_you: true }),
        task({ task_id: "d", state: "completed", updated_at: today }),
        task({ task_id: "e", state: "completed", updated_at: yesterday }),
        task({ task_id: "r", state: "rejected" }),
        task({ task_id: "c", state: "canceled" }),
      ],
      noon,
    );
    expect(groups.map((g) => [g.key, g.label, g.tasks.map((t) => t.task_id), !!g.collapsed])).toEqual([
      ["needs_you", "Needs input", ["n"], false],
      ["working", "Working", ["w"], false],
      ["done", "Done today", ["d"], false],
      ["earlier", "Earlier", ["e"], true],
      ["failed", "Failed · Canceled", ["r", "c"], false],
    ]);
    expect(groupTasks([task({})], noon).map((g) => g.key)).toEqual(["working"]);
  });
});
