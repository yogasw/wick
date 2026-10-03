import { describe, it, expect } from "vitest";
import { duplicateHandle, duplicateBody, shiftColor } from "../agentDuplicate.js";
import type { AgentItem } from "../api/team.js";

describe("duplicateHandle", () => {
  it("appends -2 to a fresh handle", () => {
    expect(duplicateHandle("ops", ["ops", "captain"])).toBe("ops-2");
  });

  it("skips suffixes already taken", () => {
    expect(duplicateHandle("ops", ["ops", "ops-2", "ops-3"])).toBe("ops-4");
  });

  it("counts on from the root of a numbered handle", () => {
    expect(duplicateHandle("ops-2", ["ops", "ops-2"])).toBe("ops-3");
  });

  it("stays within the 31-char handle limit", () => {
    const long = "a".repeat(31);
    const got = duplicateHandle(long, [long]);
    expect(got).toBe("a".repeat(29) + "-2");
    expect(got).toMatch(/^[a-z0-9][a-z0-9-]{1,30}$/);
  });

  it("does not leave a double dash when the cut lands on one", () => {
    const h = "a".repeat(28) + "-bc";
    expect(duplicateHandle(h, [h])).toBe("a".repeat(28) + "-2");
  });
});

describe("shiftColor", () => {
  it("turns the hue a little", () => {
    expect(shiftColor("#ff0000")).toBe("#ff6600");
  });

  it("expands #rgb", () => {
    expect(shiftColor("#f00")).toBe("#ff6600");
  });

  it("leaves greys and non-hex values alone", () => {
    expect(shiftColor("#808080")).toBe("#808080");
    expect(shiftColor("")).toBe("");
    expect(shiftColor("red")).toBe("red");
  });
});

describe("duplicateBody", () => {
  const agent = {
    id: "p1",
    handle: "ops",
    is_captain: true,
    project_id: "proj-1",
    name: "Ops",
    description: "jaga ops",
    system_prompt: "kamu ops",
    provider: "claude/default",
    model: "opus",
    preset: "default",
    avatar: { shape: "diamond", color: "#ff0000" },
    features: { source: true },
    allowed_connectors: null,
    include_new_connectors: true,
    run_as: "owner",
    disabled: true,
    main_session_id: "s1",
  } as unknown as AgentItem;

  it("copies persona into a new project, plus look and access", () => {
    expect(duplicateBody(agent, ["ops"])).toEqual({
      handle: "ops-2",
      name: "Ops (salinan)",
      description: "jaga ops",
      system_prompt: "kamu ops",
      provider: "claude/default",
      model: "opus",
      preset: "default",
      avatar: { shape: "diamond", color: "#ff6600" },
      features: { source: true },
      allowed_connectors: [],
      include_new_connectors: true,
      run_as: "owner",
    });
  });

  it("never reuses the project, captain, disabled or sessions", () => {
    const b = duplicateBody(agent, ["ops"]);
    for (const k of ["project_id", "is_captain", "disabled", "main_session_id"]) {
      expect(b).not.toHaveProperty(k);
    }
  });
});
