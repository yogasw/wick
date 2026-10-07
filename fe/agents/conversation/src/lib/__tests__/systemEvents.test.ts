import { describe, expect, test } from "vitest";
import { foldSystemEvents, systemEventParts, getSystemEvent, isSystemEventKind, registerSystemEvent } from "../systemEvents.js";

describe("system event registry", () => {
  test("known kinds have their own icon; unknown kinds fall back to the server text", () => {
    expect(getSystemEvent("agent_created").icon).toBe("user-plus");
    expect(getSystemEvent("hop_limit").tone).toBe("warn");
    expect(getSystemEvent("scheduled_fired").icon).toBe("clock");
    expect(getSystemEvent("routine_fired").icon).toBe("clock");
    expect(isSystemEventKind("access_changed")).toBe(true);
    expect(isSystemEventKind("persona_changed")).toBe(true);
    expect(isSystemEventKind("access_change_declined")).toBe(true);
    expect(getSystemEvent("access_change_declined").tone).toBe("warn");
    expect(isSystemEventKind("input_request")).toBe(false);
    expect(systemEventParts({ kind: "nope", text: "raw words" })).toEqual(["raw words"]);
  });

  test("mention_handoff names the sender and keeps the target a handle", () => {
    const parts = systemEventParts(
      { kind: "mention_handoff", text: "", extras: { from: "captain", to: "anton", state: "TASK_STATE_COMPLETED" } },
      { names: { captain: "Captain" } },
    );
    expect(parts).toEqual(["Captain", " → ", { handle: "anton" }, " · completed"]);
  });

  test("a new kind is one register call", () => {
    registerSystemEvent("custom_x", { icon: "plug", parts: (t) => ["custom: " + t.text] });
    expect(systemEventParts({ kind: "custom_x", text: "hi" })).toEqual(["custom: hi"]);
  });
});

describe("foldSystemEvents", () => {
  const h = (id: string, task: string, state: string) => ({ turn_id: id, role: "system", kind: "mention_handoff", text: state, extras: { task_id: task, state } });
  test("one row per task: first position, latest state", () => {
    const turns = [h("1", "t1", "working"), { turn_id: "2", role: "user", text: "hi" }, h("3", "t1", "completed"), h("4", "t2", "working")];
    const out = foldSystemEvents(turns as any[]);
    expect(out.map((t) => t.turn_id)).toEqual(["1", "2", "4"]);
    expect(out[0].extras.state).toBe("completed");
  });

  test("input_request and approval_request fold per ask_id / approval_id", () => {
    const turns = [
      { turn_id: "1", role: "system", kind: "input_request", text: "Deploy?", extras: { ask_id: "a", state: "pending" } },
      { turn_id: "2", role: "system", kind: "approval_request", text: "Bash: ls", extras: { approval_id: "p", state: "pending" } },
      { turn_id: "3", role: "system", kind: "input_request", text: "answered: Ship it", extras: { ask_id: "a", state: "answered", answer: "Ship it" } },
      { turn_id: "4", role: "system", kind: "approval_request", text: "declined", extras: { approval_id: "p", state: "block" } },
    ];
    const out = foldSystemEvents(turns);
    expect(out.map((t) => t.turn_id)).toEqual(["1", "2"]);
    expect(out[0].extras).toMatchObject({ state: "answered", answer: "Ship it" });
    expect(out[1].text).toBe("declined");
  });

  test("the same system turn delivered twice is drawn once", () => {
    const t = { turn_id: "7", role: "system", kind: "agent_created", text: "Rekap joined the team" };
    expect(foldSystemEvents([t, { ...t }])).toHaveLength(1);
  });

  test("no foldable turns → same rows", () => {
    const turns = [{ role: "user", text: "x" }];
    expect(foldSystemEvents(turns)).toEqual(turns);
  });
});
