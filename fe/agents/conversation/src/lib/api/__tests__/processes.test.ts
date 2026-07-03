import { describe, test, expect } from "vitest";
import { liveProcesses } from "../processes.js";
import type { ProcessInfo } from "../../types/agents.js";

const base: ProcessInfo = {
  session_id: "s",
  agent_name: "a",
  provider: "claude/claude",
  pid: 0,
  queued: 0,
  lifecycle: "idle",
  alive: false,
};

const process = (o: Partial<ProcessInfo>): ProcessInfo => ({ ...base, kind: "process", ...o });
const idle = (o: Partial<ProcessInfo>): ProcessInfo => ({ ...base, kind: "idle", ...o });

describe("liveProcesses", () => {
  test("drops idle-fallback rows", () => {
    const out = liveProcesses([idle({ agent_name: "main" })]);
    expect(out).toHaveLength(0);
  });

  test("keeps a running process row", () => {
    const out = liveProcesses([process({ pid: 4242, lifecycle: "working", alive: true })]);
    expect(out).toHaveLength(1);
    expect(out[0].pid).toBe(4242);
  });

  test("keeps a queued row (pending is a real process)", () => {
    const out = liveProcesses([process({ lifecycle: "queued", queued: 2, alive: true })]);
    expect(out).toHaveLength(1);
  });

  test("mixed list keeps only the process row", () => {
    const out = liveProcesses([
      process({ agent_name: "live", pid: 100, alive: true }),
      idle({ agent_name: "toolbar-only" }),
    ]);
    expect(out.map((p) => p.agent_name)).toEqual(["live"]);
  });

  test("treats a row with no kind as a process (backward compat)", () => {
    const noKind: ProcessInfo = { ...base, pid: 9, alive: true, lifecycle: "working" };
    expect(liveProcesses([noKind])).toHaveLength(1);
  });

  test("a killed session that fell back to idle drops to zero live processes", () => {
    // Reproduces the reported bug: after Kill, the pool goes empty and the
    // endpoint returns a single idle-fallback row. The rail badge/count and
    // process card must both drop to 0.
    const out = liveProcesses([idle({ lifecycle: "idle", pid: 0, alive: false })]);
    expect(out).toHaveLength(0);
  });
});
