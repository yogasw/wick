import { describe, expect, test } from "vitest";
import { AVATAR_EVENTS, CLASSIC_EVENT_POSES, DEFAULT_EVENT_POSES, IMAGE_EVENT_POSES, classicPoseOf, cleanOverrides, eventOf, imagePoseOf, poseFor, resolvePose, toolEvent } from "../events";
import { classicPoseFor } from "../shape";
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
    // The blob reacts with its face; classic / image, faceless, alert (the fail pose).
    expect(resolvePose({ working: true, toolName: "Bash", toolError: true })).toMatchObject({ state: "idle", expression: "sad", event: "error", classic: "alert" });
    expect(resolvePose({ done: true })).toMatchObject({ state: "notify", expression: "happy", event: "done" });
    expect(resolvePose({ working: true, toolName: "wick_compact" })).toMatchObject({ state: "swirl", event: "compact", classic: "thinking" });
  });

  test("no event keeps the agent's own expression", () => {
    const p = resolvePose({ working: true, toolName: "Bash" });
    expect(p.expression).toBeUndefined();
    expect(resolvePose({}).expression).toBeUndefined();
    expect(resolvePose({}).state).toBe("idle");
  });

  test("fallback: an unknown tool orbits, no tool thinks, never idle while working", () => {
    expect(resolvePose({ working: true, toolName: "frobnicate" })).toEqual({ state: "orbit", classic: "orbit", image: "orbit", event: null });
    expect(resolvePose({ working: true, tool: true })).toEqual({ state: "orbit", classic: "orbit", image: "orbit", event: null });
    expect(resolvePose({ working: true })).toEqual({ state: "thinking", classic: "thinking", image: "thinking", event: null });
  });

  test("a row turned off falls back to the plain cue, not idle", () => {
    const off = { read: { off: true }, run: { off: true }, ask_user: { off: true }, done: { off: true }, error: { off: true } };
    expect(resolvePose({ working: true, toolName: "Read", overrides: off })).toEqual({ state: "orbit", classic: "orbit", image: "orbit", event: null });
    expect(resolvePose({ working: true, toolName: "Bash", toolError: true, overrides: off })).toEqual({ state: "orbit", classic: "orbit", image: "orbit", event: null });
    expect(resolvePose({ attention: true, overrides: off }).state).toBe("alert");
    expect(resolvePose({ done: true, overrides: off }).state).toBe("notify");
  });

  test("priority: disabled > needs-attention > error > tool > thinking > idle", () => {
    const all = { disabled: true, attention: true, working: true, toolName: "Read", toolError: true, done: true };
    expect(resolvePose(all)).toEqual({ state: "sleep", classic: "sleep", image: "sleep", event: null });
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
    // A blob-only override draws as its classic look-alike, not the fallback.
    expect(resolvePose({ attention: true, overrides: { ask_user: { state: "play" } } })).toMatchObject({ state: "play", classic: "orbit", event: "ask_user" });
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

describe("classicPoseOf", () => {
  test("a failed tool draws fail (alert) on classic and image; the blob keeps idle + sad", () => {
    expect(classicPoseOf("error", DEFAULT_EVENT_POSES.error)).toBe("alert");
    expect(poseFor("error")).toEqual({ state: "idle", expression: "sad" });
    // An expression-only override still has no pose of its own here.
    expect(classicPoseOf("error", poseFor("error", { error: { expression: "angry" } })!, { error: { expression: "angry" } })).toBe("alert");
  });

  test("the owner's pose wins over the classic default", () => {
    const o = { error: { state: "burst" } };
    expect(classicPoseOf("error", poseFor("error", o)!, o)).toBe("notify");
    expect(resolvePose({ working: true, toolError: true, overrides: o })).toMatchObject({ state: "burst", classic: "notify", event: "error" });
  });

  test("rows without a classic default map their blob pose as before", () => {
    expect(classicPoseOf("read", DEFAULT_EVENT_POSES.read)).toBe("orbit");
    expect(classicPoseOf("run", DEFAULT_EVENT_POSES.run)).toBe("thinking");
  });
});

describe("classic defaults: edge cases", () => {
  test("an unknown override state keeps the classic default", () => {
    const o = { error: { state: "bogus" } };
    expect(classicPoseOf("error", poseFor("error", o)!, o)).toBe("alert");
    expect(resolvePose({ working: true, toolError: true, overrides: o })).toMatchObject({ state: "idle", classic: "alert", event: "error" });
  });

  test("an off error row falls back to the working pose on classic too", () => {
    expect(resolvePose({ working: true, toolName: "Bash", toolError: true, overrides: { error: { off: true } } })).toMatchObject({ classic: "orbit", event: null });
  });

  test("every classic default is a pose classic draws (a typo would be idle)", () => {
    for (const v of Object.values(CLASSIC_EVENT_POSES)) expect(classicPoseFor(v)).not.toBe("idle");
  });

  test("idle picked on the classic error row survives cleaning; the blob still drops it", () => {
    expect(cleanOverrides({ error: { state: "idle" } }, { classic: true })).toEqual({ error: { state: "idle" } });
    expect(cleanOverrides({ error: { state: "idle" } })).toBeUndefined();
    // Rows without a classic default still drop their default state.
    expect(cleanOverrides({ run: { state: "thinking" } }, { classic: true })).toBeUndefined();
  });
});

describe("image poses per event", () => {
  test("defaults as in the mockup", () => {
    expect(IMAGE_EVENT_POSES).toEqual({
      read: "scan", run: "thinking", write: "type", delegate: "send", ask_user: "alert", remote_wait: "wait", error: "fail", done: "notify", compact: "swirl",
    });
    for (const ev of AVATAR_EVENTS) expect(imagePoseOf(ev)).toBe(IMAGE_EVENT_POSES[ev]);
  });

  test("resolvePose carries the image pose; the blob and classic ones are unchanged", () => {
    expect(resolvePose({ working: true, toolName: "Read" })).toMatchObject({ state: "wide", classic: "orbit", image: "scan", event: "read" });
    expect(resolvePose({ working: true, toolError: true })).toMatchObject({ state: "idle", classic: "alert", image: "fail", event: "error" });
    expect(resolvePose({ disabled: true })).toMatchObject({ classic: "sleep", image: "sleep", event: null });
    expect(resolvePose({ working: true, toolName: "Read", overrides: { read: { off: true } } })).toMatchObject({ classic: "orbit", image: "orbit", event: null });
  });

  test("an image pick: the image draws it, classic its look-alike, the blob its default", () => {
    const o = { read: { state: "type" } };
    expect(poseFor("read", o)).toEqual(DEFAULT_EVENT_POSES.read);
    expect(resolvePose({ working: true, toolName: "Read", overrides: o })).toMatchObject({ state: "wide", classic: "orbit", image: "type" });
    expect(classicPoseOf("error", poseFor("error", { error: { state: "squeeze" } })!, { error: { state: "squeeze" } })).toBe("thinking");
  });

  test("legacy picks draw their look-alike on the image", () => {
    expect(imagePoseOf("read", { read: { state: "sleep" } })).toBe("wait");
    expect(imagePoseOf("done", { done: { state: "burst" } })).toBe("notify");
    expect(imagePoseOf("run", { run: { state: "orbit" } })).toBe("orbit");
    expect(imagePoseOf("run", { run: { state: "bogus" } })).toBe("thinking");
    expect(imagePoseOf("done", { done: { expression: "happy" } })).toBe("notify");
  });

  test("cleanOverrides: image defaults save as none, image poses are kept from any table", () => {
    expect(cleanOverrides({ read: { state: "scan" }, error: { state: "fail" } }, { classic: true, image: true })).toBeUndefined();
    expect(cleanOverrides({ read: { state: "wide" } }, { classic: true, image: true })).toEqual({ read: { state: "wide" } });
    expect(cleanOverrides({ read: { state: "scan" }, run: { state: "dance" } })).toEqual({ read: { state: "scan" } });
    // Only the edited row is compared with the table's default: an image
    // pick that equals the blob default (read = wide) survives a blob edit.
    expect(cleanOverrides({ read: { state: "wide" }, done: { off: true } }, { only: "done" })).toEqual({ read: { state: "wide" }, done: { off: true } });
    expect(cleanOverrides({ read: { state: "wide" }, done: { state: "notify" } }, { only: "done" })).toEqual({ read: { state: "wide" } });
    expect(cleanOverrides({ write: { state: "orbit", expression: "attentive" } }, { only: "read" })).toEqual({ write: { state: "orbit", expression: "attentive" } });
    // Classic table unchanged: the failed-tool row's pick is kept.
    expect(cleanOverrides({ error: { state: "idle" } }, { classic: true })).toEqual({ error: { state: "idle" } });
  });
});
