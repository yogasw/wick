import { describe, expect, test } from "vitest";
import { AVATAR_EVENTS, DEFAULT_EVENT_POSES, cleanOverrides, eventOf, poseFor, resolvePose, toolEvent } from "../events";
import { EXPRESSIONS, STATES } from "../blob/core/types";

describe("toolEvent", () => {
  test.each([
    ["Read", "read"], ["Grep", "read"], ["WebFetch", "read"], ["mcp__wick__wick_search", "read"], ["query_range", "read"], ["get_thread", "read"],
    ["Bash", "run"], ["shell", "run"], ["run_tests", "run"], ["buildProject", "run"],
    ["Edit", "write"], ["Write", "write"], ["write_file", "write"], ["send_message", "write"], ["git-commit", "write"], ["update_node", "write"],
    ["Task", "delegate"], ["wick_delegate", "delegate"], ["mcp__wick__wick_agent_delegate", "delegate"],
    ["ask_user", "ask_user"], ["mcp__wick__ask_user", "ask_user"], ["AskUserQuestion", "ask_user"],
    ["wick_compact", "compact"],
  ])("%s → %s", (name, ev) => expect(toolEvent(name)).toBe(ev));

  test("a name with no known verb is a generic tool", () => {
    expect(toolEvent("frobnicate")).toBeNull();
    expect(toolEvent("mcp__x__zzz")).toBeNull();
    expect(toolEvent("")).toBeNull();
    expect(toolEvent(undefined)).toBeNull();
  });
});

describe("resolvePose", () => {
  test("each event takes its default pose", () => {
    expect(resolvePose({ working: true, toolName: "Read" })).toMatchObject({ state: "wide", expression: "curious", event: "read" });
    expect(resolvePose({ working: true, toolName: "Bash" })).toMatchObject({ state: "thinking", classic: "thinking", event: "run" });
    expect(resolvePose({ working: true, toolName: "Edit" })).toMatchObject({ state: "orbit", expression: "attentive", event: "write" });
    expect(resolvePose({ working: true, toolName: "wick_delegate" })).toMatchObject({ state: "orbit", event: "delegate" });
    expect(resolvePose({ attention: true })).toMatchObject({ state: "alert", classic: "alert", event: "ask_user" });
    expect(resolvePose({ working: true, remote: true })).toMatchObject({ state: "wide", expression: "attentive", event: "remote_wait", classic: "orbit" });
    expect(resolvePose({ working: true, toolName: "Bash", toolError: true })).toMatchObject({ state: "idle", expression: "sad", event: "error" });
    expect(resolvePose({ done: true })).toMatchObject({ state: "notify", expression: "happy", event: "done" });
    expect(resolvePose({ working: true, toolName: "wick_compact" })).toMatchObject({ state: "swirl", event: "compact", classic: "orbit" });
  });

  test("no event keeps the agent's own expression", () => {
    const p = resolvePose({ working: true, toolName: "Bash" });
    expect(p.expression).toBeUndefined();
    expect(resolvePose({}).expression).toBeUndefined();
    expect(resolvePose({}).state).toBe("idle");
  });

  test("fallback: an unknown tool orbits, no tool thinks, never idle while working", () => {
    expect(resolvePose({ working: true, toolName: "frobnicate" })).toEqual({ state: "orbit", classic: "orbit", event: null });
    expect(resolvePose({ working: true, tool: true })).toEqual({ state: "orbit", classic: "orbit", event: null });
    expect(resolvePose({ working: true })).toEqual({ state: "thinking", classic: "thinking", event: null });
  });

  test("a row turned off falls back to the plain cue, not idle", () => {
    const off = { read: { off: true }, run: { off: true }, ask_user: { off: true }, done: { off: true }, error: { off: true } };
    expect(resolvePose({ working: true, toolName: "Read", overrides: off })).toEqual({ state: "orbit", classic: "orbit", event: null });
    expect(resolvePose({ working: true, toolName: "Bash", toolError: true, overrides: off })).toEqual({ state: "orbit", classic: "orbit", event: null });
    expect(resolvePose({ attention: true, overrides: off }).state).toBe("alert");
    expect(resolvePose({ done: true, overrides: off }).state).toBe("notify");
  });

  test("priority: disabled > needs-attention > error > tool > thinking > idle", () => {
    const all = { disabled: true, attention: true, working: true, toolName: "Read", toolError: true, done: true };
    expect(resolvePose(all)).toEqual({ state: "sleep", classic: "sleep", event: null });
    expect(resolvePose({ ...all, disabled: false }).event).toBe("ask_user");
    expect(resolvePose({ ...all, disabled: false, attention: false }).event).toBe("error");
    expect(resolvePose({ ...all, disabled: false, attention: false, toolError: false }).event).toBe("read");
    expect(resolvePose({ working: true, done: true }).state).toBe("thinking");
    expect(eventOf({ toolError: true })).toBeNull(); // a failure only counts inside a turn
    expect(resolvePose({ hatching: true, working: true }).classic).toBe("egg");
  });

  test("an override changes the pose and the expression; '' = own expression", () => {
    const o = { read: { state: "play", expression: "suspicious" }, write: { expression: "" } };
    expect(resolvePose({ working: true, toolName: "Grep", overrides: o })).toMatchObject({ state: "play", expression: "suspicious", classic: "orbit" });
    const w = resolvePose({ working: true, toolName: "Edit", overrides: o });
    expect(w.state).toBe("orbit");
    expect(w.expression).toBeUndefined();
  });

  test("an override with an unknown value keeps that field's default", () => {
    expect(poseFor("read", { read: { state: "moonwalk", expression: "smug" } })).toEqual(DEFAULT_EVENT_POSES.read);
  });
});

describe("defaults and cleanOverrides", () => {
  test("every default is a pose the blob can draw", () => {
    for (const ev of AVATAR_EVENTS) {
      const p = DEFAULT_EVENT_POSES[ev];
      expect(STATES).toContain(p.state);
      if (p.expression) expect(EXPRESSIONS).toContain(p.expression);
    }
  });

  test("a table back at its defaults is no override", () => {
    expect(cleanOverrides({ read: { state: "wide", expression: "curious" }, run: { off: false } })).toBeUndefined();
    expect(cleanOverrides({ read: { state: "play" }, error: { off: true, state: "wide" }, bogus: { off: true } })).toEqual({ read: { state: "play" }, error: { off: true } });
    expect(cleanOverrides({ done: { expression: "" } })).toEqual({ done: { expression: "" } });
  });
});
