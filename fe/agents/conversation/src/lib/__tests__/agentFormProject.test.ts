import { describe, expect, it } from "vitest";
import { grantsOf, projectOptionLabel } from "../agentForm.js";
import type { ConnectorGrant } from "../api/team.js";

describe("projectOptionLabel", () => {
  it("flags a shared project with its owner", () => {
    expect(projectOptionLabel({ id: "p", name: "Ops", shared: true, owner_name: "Ana" })).toBe("Ops · Shared by Ana");
    expect(projectOptionLabel({ id: "p", name: "Ops", shared: true })).toBe("Ops · Shared");
  });
  it("leaves the caller's own project as is", () => {
    expect(projectOptionLabel({ id: "p", name: "Ops" })).toBe("Ops");
  });
});

describe("grantsOf", () => {
  it("turns null lists into empty ones", () => {
    const raw = [{ connector_id: "c1", accounts: null, level: "off", ops: null }] as unknown as ConnectorGrant[];
    expect(grantsOf(raw)).toEqual([{ connector_id: "c1", accounts: [], level: "off", ops: [] }]);
    expect(grantsOf(null)).toEqual([]);
  });
});
