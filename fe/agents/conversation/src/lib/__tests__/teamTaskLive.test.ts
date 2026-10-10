import { describe, test, expect } from "vitest";
import { TEAM_TASK_FALLBACK_POLL_MS, isReconnect, parseTeamTaskEvent, patchTeamTask, teamTaskPollMs } from "../teamTaskLive.js";
import type { TeamTaskItem } from "../types/agents.js";

const task = (o: Partial<TeamTaskItem>): TeamTaskItem => ({
  task_id: "t1", context_id: "c", to_agent_id: "a", to_handle: "anton", to_name: "Anton", title: "x",
  state: "working", turns: 1, max_turns: 8, started_at: "2026-10-10T10:00:00Z", updated_at: "2026-10-10T10:00:00Z", ...o,
});

describe("teamTaskLive", () => {
  test("parseTeamTaskEvent reads a task and rejects anything else", () => {
    expect(parseTeamTaskEvent(JSON.stringify(task({ state: "input_required", needs_you: true })))?.needs_you).toBe(true);
    expect(parseTeamTaskEvent("")).toBeNull();
    expect(parseTeamTaskEvent("not json")).toBeNull();
    expect(parseTeamTaskEvent("null")).toBeNull();
    expect(parseTeamTaskEvent(JSON.stringify({ state: "working" }))).toBeNull();
  });

  test("patchTeamTask replaces the task of the same id", () => {
    const list = [task({ task_id: "t2", started_at: "2026-10-10T11:00:00Z" }), task({})];
    const next = patchTeamTask(list, task({ state: "input_required", needs_you: true, updated_at: "2026-10-10T10:05:00Z" }));
    expect(next).not.toBe(list);
    expect(next.map((t) => t.task_id)).toEqual(["t2", "t1"]);
    expect(next[1].state).toBe("input_required");
    expect(next[1].needs_you).toBe(true);
  });

  test("patchTeamTask adds a new task newest first", () => {
    const next = patchTeamTask([task({})], task({ task_id: "t3", started_at: "2026-10-10T12:00:00Z" }));
    expect(next.map((t) => t.task_id)).toEqual(["t3", "t1"]);
    expect(patchTeamTask([], task({})).map((t) => t.task_id)).toEqual(["t1"]);
  });

  test("patchTeamTask ignores a copy older than the one held", () => {
    const list = [task({ state: "completed", updated_at: "2026-10-10T10:09:00Z" })];
    expect(patchTeamTask(list, task({ state: "working", updated_at: "2026-10-10T10:01:00Z" }))).toBe(list);
    // Same instant (a second change within one clock tick) still applies.
    expect(patchTeamTask(list, task({ state: "failed", updated_at: "2026-10-10T10:09:00Z" }))[0].state).toBe("failed");
  });

  test("the fallback poll runs only while the stream is down", () => {
    expect(teamTaskPollMs("connected", true)).toBeNull();
    expect(teamTaskPollMs("error", true)).toBe(TEAM_TASK_FALLBACK_POLL_MS);
    expect(teamTaskPollMs("connecting", true)).toBe(TEAM_TASK_FALLBACK_POLL_MS);
    expect(teamTaskPollMs("error", false)).toBeNull();
    expect(TEAM_TASK_FALLBACK_POLL_MS).toBeGreaterThanOrEqual(10_000);
    expect(TEAM_TASK_FALLBACK_POLL_MS).toBeLessThanOrEqual(15_000);
  });

  test("isReconnect is a return to connected after a drop", () => {
    expect(isReconnect("error", "connected")).toBe(true);
    expect(isReconnect("connecting", "connected")).toBe(false);
    expect(isReconnect("connected", "error")).toBe(false);
  });
});
