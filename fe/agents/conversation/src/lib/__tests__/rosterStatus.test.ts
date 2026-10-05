import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { rosterStatus, withTurn, withLive, liveIds, createLivePoll } from "../rosterStatus.js";

const row = (p: Record<string, unknown> = {}) => ({ id: "a1", status: "idle", disabled: false, ...p });

describe("rosterStatus", () => {
  it("idle agent", () => {
    expect(rosterStatus(row())).toEqual({ unread: false, attention: false, work: "idle", typing: null, tip: "online · idle" });
  });

  it("unread shows the dot and 'new message'", () => {
    const s = rosterStatus(row({ unread: true }));
    expect(s.unread).toBe(true);
    expect(s.tip).toBe("new message");
  });

  it("the open chat is never unread", () => {
    expect(rosterStatus(row({ unread: true }), { activeId: "a1" }).unread).toBe(false);
    expect(rosterStatus(row({ unread: true }), { activeId: "other" }).unread).toBe(true);
  });

  it("a running turn with no tool in flight is thinking, not typing", () => {
    const s = rosterStatus(row({ status: "running" }));
    expect(s.work).toBe("thinking");
    expect(s.typing).toBe("thinking…");
    expect(s.tip).toBe("thinking…");
  });

  it("a running turn on a tool names the tool", () => {
    const s = rosterStatus(row({ status: "queued", current_action: "query_range" }));
    expect(s.work).toBe("tool");
    expect(s.typing).toBe("running query_range…");
    expect(s.tip).toBe("running query_range…");
    expect(rosterStatus(row({ status: "running", current_action: "read_file" })).typing).toBe("reading file…");
    expect(rosterStatus(row({ status: "running", current_action: "mcp__wick__wick_search" })).typing).toBe("searching…");
  });

  it("a remote agent waits for the other side, never types or thinks", () => {
    const s = rosterStatus(row({ status: "running", kind: "a2a-remote", handle: "halodev", current_action: "Bash" }));
    expect(s.work).toBe("waiting");
    expect(s.typing).toBe("Waiting for @halodev's reply…");
  });

  it("current_action is ignored when the turn is not running", () => {
    expect(rosterStatus(row({ current_action: "Bash" })).typing).toBeNull();
  });

  it("attention beats typing and unread", () => {
    const s = rosterStatus(row({ status: "running", needs_attention: true, unread: true }));
    expect(s.tip).toBe("needs your attention");
    expect(s.attention).toBe(true);
    expect(s.typing).toBe("thinking…");
  });

  it("hatching beats everything but disabled", () => {
    expect(rosterStatus(row({ unread: true }), { hatching: true }).tip).toBe("just hatched");
  });

  it("a disabled agent shows no live cue", () => {
    const s = rosterStatus(row({ disabled: true, status: "running", unread: true, needs_attention: true }), { hatching: true });
    expect(s).toEqual({ unread: false, attention: false, work: "idle", typing: null, tip: "disabled" });
  });

  it("rows from an older server (no P13 fields) read as idle", () => {
    expect(rosterStatus({ id: "x", status: "idle", disabled: false }).tip).toBe("online · idle");
  });
});

describe("withTurn", () => {
  const list = [
    { id: "a1", status: "running", current_action: "query_range" },
    { id: "a2", status: "running", current_action: "search" },
  ];

  it("a finished turn reads idle at once and drops its tool", () => {
    const out = withTurn(list, "a1", false);
    expect(out[0]).toEqual({ id: "a1", status: "idle", current_action: "" });
    expect(out[1]).toBe(list[1]);
    expect(rosterStatus({ ...out[0], disabled: false }).typing).toBeNull();
  });

  it("a started turn reads running and keeps the tool it knows", () => {
    const out = withTurn([{ id: "a1", status: "idle", current_action: "x" }], "a1", true);
    expect(out[0]).toEqual({ id: "a1", status: "running", current_action: "x" });
  });
});

describe("liveIds / withLive", () => {
  const list: { id: string; status: string; current_action: string; disabled: boolean; tool_error?: boolean }[] = [
    { id: "a1", status: "running", current_action: "", disabled: false },
    { id: "a2", status: "idle", current_action: "", disabled: false },
    { id: "a3", status: "running", current_action: "", disabled: true },
  ];

  it("follows only the agents working now", () => {
    expect(liveIds(list)).toEqual(["a1"]);
    expect(liveIds([list[1]])).toEqual([]);
  });

  it("thinking → tool → thinking follows the poll", () => {
    let r = withLive(list, [{ id: "a1", status: "running", current_action: "Bash", needs_attention: false }]);
    expect(r.finished).toBe(false);
    expect(rosterStatus(r.agents[0]).typing).toBe("running Bash…");
    expect(r.agents[1]).toBe(list[1]);
    r = withLive(r.agents, [{ id: "a1", status: "running", current_action: "", needs_attention: false }]);
    expect(rosterStatus(r.agents[0]).work).toBe("thinking");
  });

  it("nothing moved = the same list; a finished turn asks for a full read", () => {
    const same = withLive(list, [{ id: "a1", status: "running", current_action: "", needs_attention: false }]);
    expect(same.agents).toBe(list);
    const done = withLive(list, [{ id: "a1", status: "idle", current_action: "Bash", needs_attention: false }]);
    expect(done.finished).toBe(true);
    expect(done.agents[0].current_action).toBe("");
  });

  it("a failed tool rides the poll and clears when the turn ends", () => {
    let r = withLive(list, [{ id: "a1", status: "running", current_action: "", needs_attention: false, tool_error: true }]);
    expect(r.agents[0].tool_error).toBe(true);
    r = withLive(r.agents, [{ id: "a1", status: "idle", current_action: "", needs_attention: false, tool_error: true }]);
    expect(r.agents[0].tool_error).toBe(false);
    expect(withTurn([{ id: "a1", status: "running", tool_error: true }], "a1", false)[0].tool_error).toBe(false);
  });

  it("the agent whose chat streams its tool keeps it; status still comes from the poll", () => {
    const own = [{ id: "a1", status: "running", current_action: "read_file", needs_attention: false }];
    const r = withLive(own, [{ id: "a1", status: "running", current_action: "Read", needs_attention: true }], "a1");
    expect(r.agents[0].current_action).toBe("read_file");
    expect(r.agents[0].needs_attention).toBe(true);
  });
});

describe("createLivePoll", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it("runs while someone works and stops when nobody does", async () => {
    const seen: string[][] = [];
    const p = createLivePoll({ tick: async (ids) => void seen.push(ids), visible: () => true, interval: 2000 });
    p.update([]);
    expect(p.running()).toBe(false);
    p.update(["a1"]);
    expect(p.running()).toBe(true);
    await vi.advanceTimersByTimeAsync(2000);
    p.update(["a1", "a2"]);
    await vi.advanceTimersByTimeAsync(2000);
    expect(seen).toEqual([["a1"], ["a1", "a2"]]);
    p.update([]);
    expect(p.running()).toBe(false);
    await vi.advanceTimersByTimeAsync(6000);
    expect(seen.length).toBe(2);
  });

  it("skips while the tab is hidden and never overlaps a slow tick", async () => {
    let visible = false;
    let calls = 0;
    let release!: () => void;
    const p = createLivePoll({
      tick: () => { calls++; return new Promise<void>((r) => (release = r)); },
      visible: () => visible,
      interval: 1000,
    });
    p.update(["a1"]);
    await vi.advanceTimersByTimeAsync(3000);
    expect(calls).toBe(0);
    visible = true;
    await vi.advanceTimersByTimeAsync(3000);
    expect(calls).toBe(1);
    release();
    await vi.advanceTimersByTimeAsync(1000);
    expect(calls).toBe(2);
    p.stop();
  });
});
