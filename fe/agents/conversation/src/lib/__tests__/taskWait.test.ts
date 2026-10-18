import { describe, test, expect } from "vitest";
import { chipLabel, taskLabel, taskProgress, taskWait } from "../delegations.js";
import type { TeamTaskItem } from "../types/agents.js";

const task = (over: Partial<TeamTaskItem> = {}): TeamTaskItem => ({
  task_id: "t1", context_id: "c1", to_agent_id: "a1", to_handle: "jaya", to_name: "Jaya",
  title: "learn", state: "working", turns: 1, max_turns: 10,
  started_at: "2026-10-17T10:00:00Z", updated_at: "2026-10-17T10:00:00Z", ...over,
});

describe("taskWait", () => {
  test("restarting teammate reads amber restarting, not working or failed", () => {
    const t = task({ restart_attempt: 1, restart_max: 2, wait_note: "target restarting after an idle timeout, retry 1/2" });
    expect(taskWait(t)).toEqual({ kind: "restarting", label: "restarting · retry 1/2", detail: "target restarting after an idle timeout, retry 1/2" });
    expect(chipLabel(t, "Captain")).toBe("restarting · retry 1/2");
    expect(taskLabel(t, "Captain")).toBe("restarting · retry 1/2");
  });
  test("queued teammate shows its place", () => {
    const t = task({ queue_pos: 2, wait_note: "target queued for a free slot (#2)" });
    expect(taskWait(t)?.label).toBe("queued · #2");
    expect(taskProgress(t)).toBe("target queued for a free slot (#2)");
  });
  test("a task in line at the teammate's chat says where and which place", () => {
    const t = task({ gate_pos: 2, wait_note: "waiting for its turn at @jaya (#2 in line)" });
    expect(taskWait(t)).toEqual({ kind: "queued", label: "in line · #2 at @jaya", detail: "waiting for its turn at @jaya (#2 in line)" });
    expect(chipLabel(t, "Captain")).toBe("in line · #2 at @jaya");
  });
  test("own progress line wins over the wait detail", () => {
    expect(taskProgress(task({ queue_pos: 1, progress: "Reading Notion…" }))).toBe("Reading Notion…");
  });
  test("no wait on a finished task or without fields", () => {
    expect(taskWait(task())).toBeNull();
    expect(taskWait(task({ state: "failed", restart_attempt: 1 }))).toBeNull();
    expect(chipLabel(task(), "Captain")).toBe("working");
  });
});
