import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { rosterStatus, withTurn, withActivity } from "../rosterStatus.js";
import type { SessionActivity } from "../stores/sessionsStream.js";

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

describe("withActivity (the /stream/sessions activity event)", () => {
  const list: { id: string; status: string; disabled: boolean; main_session_id: string; current_action: string; needs_attention?: boolean; tool_error?: boolean }[] = [
    { id: "a1", status: "idle", disabled: false, main_session_id: "s1", current_action: "" },
    { id: "a2", status: "idle", disabled: false, main_session_id: "s2", current_action: "" },
  ];
  const ev = (p: Partial<SessionActivity>): SessionActivity => ({ session_id: "s1", work: "", action: "", ...p });

  it("thinking → tool → thinking → idle, from stream events alone", () => {
    let r = withActivity(list, ev({ work: "thinking" }));
    expect(rosterStatus(r.agents[0]).work).toBe("thinking");
    expect(r.agents[1]).toBe(list[1]);
    r = withActivity(r.agents, ev({ work: "tool", action: "Bash" }));
    expect(rosterStatus(r.agents[0]).typing).toBe("running Bash…");
    r = withActivity(r.agents, ev({ work: "thinking", tool_error: true }));
    expect(rosterStatus(r.agents[0]).work).toBe("thinking");
    expect(r.agents[0].tool_error).toBe(true);
    expect(r.finished).toBe(false);
    r = withActivity(r.agents, ev({}));
    expect(rosterStatus(r.agents[0]).work).toBe("idle");
    expect(r.agents[0].tool_error).toBe(false);
    expect(r.finished).toBe(true);
  });

  it("a session that is no agent's main chat, or no change, returns the same list", () => {
    expect(withActivity(list, ev({ session_id: "other", work: "tool", action: "Bash" })).agents).toBe(list);
    expect(withActivity(list, ev({ session_id: "" , work: "thinking" })).agents).toBe(list);
    const running = withActivity(list, ev({ work: "thinking" })).agents;
    expect(withActivity(running, ev({ work: "thinking" })).agents).toBe(running);
  });

  it("needs_attention follows the event; a queued row starts running", () => {
    const r = withActivity([{ ...list[0], status: "queued" }], ev({ work: "thinking", needs_attention: true }));
    expect(r.agents[0].status).toBe("running");
    expect(rosterStatus(r.agents[0]).attention).toBe(true);
    expect(withTurn([{ id: "a1", status: "running", tool_error: true }], "a1", false)[0].tool_error).toBe(false);
  });
});

/* The roster no longer polls its live state: the timers AgentsApp keeps are
   the 30s full refresh and a local 1s clock (no request), and the 2s live
   read is gone with its endpoint. */
describe("no quick poll", () => {
  it("AgentsApp has no 2s interval and no /agents/live read", () => {
    const src = readFileSync(resolve(__dirname, "../../AgentsApp.svelte"), "utf8");
    const intervals = [...src.matchAll(/setInterval\([\s\S]*?,\s*(\w+)\s*\)/g)].map((m) => m[1]);
    expect(intervals).toContain("30000");
    expect(intervals.filter((ms) => ms !== "30000" && ms !== "1000")).toEqual([]);
    expect(src).toMatch(/setInterval\(\(\) => \(waitNow = Date\.now\(\)\), 1000\)/);
    expect(src).not.toMatch(/agents\/live|listAgentsLive|createLivePoll/);
    expect(src).toMatch(/connectSessionsStream\(/);
  });
});
