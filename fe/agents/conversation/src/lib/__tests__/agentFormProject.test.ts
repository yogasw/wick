import { describe, expect, it } from "vitest";
import { projectOptionLabel } from "../agentForm.js";

describe("projectOptionLabel", () => {
  it("flags a shared project with its owner", () => {
    expect(projectOptionLabel({ id: "p", name: "Ops", shared: true, owner_name: "Ana" })).toBe("Ops · Shared by Ana");
    expect(projectOptionLabel({ id: "p", name: "Ops", shared: true })).toBe("Ops · Shared");
  });
  it("leaves the caller's own project as is", () => {
    expect(projectOptionLabel({ id: "p", name: "Ops" })).toBe("Ops");
  });
});
