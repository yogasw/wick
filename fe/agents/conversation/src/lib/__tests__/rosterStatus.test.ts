import { describe, expect, it } from "vitest";
import { rosterStatus, withTurn } from "../rosterStatus.js";

const row = (p: Record<string, unknown> = {}) => ({ id: "a1", status: "idle", disabled: false, ...p });

describe("rosterStatus", () => {
  it("idle agent", () => {
    expect(rosterStatus(row())).toEqual({ unread: false, attention: false, typing: null, tip: "online · idle" });
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

  it("running turn types, with the current tool when known", () => {
    expect(rosterStatus(row({ status: "running" })).typing).toBe("Typing");
    expect(rosterStatus(row({ status: "running" })).tip).toBe("typing");
    const s = rosterStatus(row({ status: "queued", current_action: "query_range" }));
    expect(s.typing).toBe("query_range");
    expect(s.tip).toBe("query_range");
  });

  it("current_action is ignored when the turn is not running", () => {
    expect(rosterStatus(row({ current_action: "Bash" })).typing).toBeNull();
  });

  it("attention beats typing and unread", () => {
    const s = rosterStatus(row({ status: "running", needs_attention: true, unread: true }));
    expect(s.tip).toBe("needs your attention");
    expect(s.attention).toBe(true);
    expect(s.typing).toBe("Typing");
  });

  it("hatching beats everything but disabled", () => {
    expect(rosterStatus(row({ unread: true }), { hatching: true }).tip).toBe("just hatched");
  });

  it("a disabled agent shows no live cue", () => {
    const s = rosterStatus(row({ disabled: true, status: "running", unread: true, needs_attention: true }), { hatching: true });
    expect(s).toEqual({ unread: false, attention: false, typing: null, tip: "disabled" });
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
