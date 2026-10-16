import { describe, test, expect } from "vitest";
import { liveBlockTasks, teamTraceOf, traceActive, traceDuration, traceLine, traceStatus, traceTaskOf, withLive } from "../teamTrace.js";
import type { TeamTaskItem } from "../types/agents.js";

const task = (o: object) => JSON.stringify({ task_id: "t1", via: "tool", to_handle: "halodev", state: "completed", started_at: "2026-10-12T09:00:00Z", ended_at: "2026-10-12T09:02:21Z", ...o });

describe("teamTrace", () => {
  test("team_task events group under their call; mention ones apart", () => {
    const tr = teamTraceOf([
      { type: "tool_use", tool_use_id: "u1", tool_name: "team_message" },
      { type: "team_task", tool_use_id: "u1", text: task({}) },
      { type: "team_task", tool_use_id: "u1", text: task({ task_id: "t2" }) },
      { type: "team_handoff", text: "{}" },
      { type: "team_task", text: task({ task_id: "t3", via: "mention" }) },
      { type: "team_task", tool_use_id: "u1", text: "not json" },
    ]);
    expect(tr.byUse.get("u1")!.map((t) => t.task_id)).toEqual(["t1", "t2"]);
    expect(tr.mentions.map((t) => t.task_id)).toEqual(["t3"]);
  });

  test("status, duration and the row's line", () => {
    const t = JSON.parse(task({ entries: [{ role: "user", text: "check" }, { role: "agent", text: "no  bot\noption" }] }));
    expect(traceStatus(t)).toBe("replied");
    expect(traceDuration(t)).toBe("2m 21s");
    expect(traceLine(t)).toBe("no bot option");
    expect(traceDuration({ ...t, ended_at: undefined }, Date.parse("2026-10-12T09:00:10Z"))).toBe("10s");
    expect(traceDuration({ ...t, started_at: undefined })).toBe("");
    expect(traceLine({ ...t, entries: [], title: "the brief" })).toBe("the brief");
    expect(traceStatus({ ...t, state: "working" })).toBe("working");
  });
});

describe("teamTrace live (T16 S2/S6)", () => {
  const item = (o: Partial<TeamTaskItem>): TeamTaskItem => ({ task_id: "t1", context_id: "c", to_agent_id: "a", to_handle: "halodev", to_name: "Halodev", title: "check", state: "working", turns: 1, max_turns: 8, started_at: "2026-10-12T09:00:00Z", updated_at: "2026-10-12T09:01:00Z", ...o });
  const rec = JSON.parse(task({ state: "working", ended_at: undefined, entries: [{ role: "user", text: "check" }] }));

  test("withLive moves a recorded row on and adds the new line once", () => {
    const got = withLive(rec, item({ state: "completed", reply: "all clear", ended_at: "2026-10-12T09:02:21Z" }));
    expect(traceStatus(got)).toBe("replied");
    expect(traceLine(got)).toBe("all clear");
    expect(traceDuration(got)).toBe("2m 21s");
    expect(withLive(got, item({ state: "completed", reply: "all clear" })).entries!.length).toBe(got.entries!.length);
    expect(withLive(rec, undefined)).toBe(rec);
  });

  test("a running turn's team_message block reads its rows from the live list", () => {
    const byId = new Map([["t1", item({ progress: "reading logs" })]]);
    const rows = liveBlockTasks({ kind: "tool", toolUseId: "u1", toolName: "mcp__wick__team_message", toolInput: '{"to":"@halodev"}', result: '{"task_id":"t1"}' }, byId);
    expect(rows.map((r) => [r.task_id, traceLine(r)])).toEqual([["t1", "reading logs"]]);
    expect(liveBlockTasks({ kind: "text", text: "x" }, byId)).toEqual([]);
    expect(traceTaskOf(item({})).entries).toEqual([]);
  });

  test("traceActive: true while a row can still change", () => {
    expect(traceActive([rec])).toBe(true);
    expect(traceActive([{ ...rec, state: "completed" }])).toBe(false);
  });
});
