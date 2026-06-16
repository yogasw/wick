import { describe, it, expect } from "vitest";
import { routeFromPath } from "../router.js";

describe("routeFromPath", () => {
  it("returns / for exact prefix match", () => {
    expect(routeFromPath("/tools/agents/workflow/providers", "/tools/agents/workflow")).toBe("/");
  });

  it("returns / for prefix with trailing slash", () => {
    expect(routeFromPath("/tools/agents/workflow/providers/", "/tools/agents/workflow")).toBe("/");
  });

  it("returns sub-path for nested route", () => {
    expect(routeFromPath("/tools/agents/workflow/providers/claude/default", "/tools/agents/workflow")).toBe("/claude/default");
  });

  it("returns / for unknown path", () => {
    expect(routeFromPath("/tools/agents/workflow/overview", "/tools/agents/workflow")).toBe("/");
  });

  it("handles empty base", () => {
    expect(routeFromPath("/providers", "")).toBe("/");
    expect(routeFromPath("/providers/claude/default", "")).toBe("/claude/default");
  });
});
