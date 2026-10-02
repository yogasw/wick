import { describe, it, expect } from "vitest";
import { duplicateHandle, duplicateBody } from "../agentDuplicate.js";
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

describe("duplicateBody", () => {
  const agent = {
    id: "p1",
    handle: "ops",
    is_captain: true,
    project_id: "proj-1",
    avatar: { shape: "diamond", color: "#ff0000" },
    features: { source: true },
    allowed_connectors: null,
    include_new_connectors: true,
    run_as: "owner",
    disabled: true,
  } as unknown as AgentItem;

  it("copies project, look and access but not captain or disabled", () => {
    const b = duplicateBody(agent, ["ops"]);
    expect(b).toEqual({
      handle: "ops-2",
      project_id: "proj-1",
      avatar: { shape: "diamond", color: "#ff0000" },
      features: { source: true },
      allowed_connectors: [],
      include_new_connectors: true,
      run_as: "owner",
    });
    expect(b).not.toHaveProperty("is_captain");
    expect(b).not.toHaveProperty("disabled");
  });
});
