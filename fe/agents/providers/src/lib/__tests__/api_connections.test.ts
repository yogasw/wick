import { describe, it, expect } from "vitest";
import { normalizeConnections } from "../api.js";

describe("normalizeConnections", () => {
  it("maps wire snake_case onto the camelCase view model", () => {
    const got = normalizeConnections({
      connections: [
        {
          type: "claude",
          name: "enginer",
          connected: true,
          email: "dev@abc.com",
          plan: "max",
          org: "abc org",
          auth_method: "Claude AI",
          usage_supported: true,
          windows: [{ key: "five_hour", utilization: 42, resets_at: "2030-01-01T00:00:00Z" }],
        },
      ],
    });
    expect(got).toHaveLength(1);
    const c = got[0];
    expect(c.type).toBe("claude");
    expect(c.name).toBe("enginer");
    expect(c.connected).toBe(true);
    expect(c.email).toBe("dev@abc.com");
    expect(c.plan).toBe("max");
    expect(c.authMethod).toBe("Claude AI");
    expect(c.usageSupported).toBe(true);
    expect(c.windows).toEqual([
      { key: "five_hour", utilization: 42, resetsAt: "2030-01-01T00:00:00Z" },
    ]);
  });

  it("normalizes a null connections list to an empty array", () => {
    expect(normalizeConnections({ connections: null } as never)).toEqual([]);
  });

  it("normalizes null windows to an empty array", () => {
    const got = normalizeConnections({
      connections: [{ type: "codex", name: "cx", connected: true, usage_supported: false, windows: null }],
    } as never);
    expect(got[0].windows).toEqual([]);
  });

  it("defaults missing optional fields to empty strings, not undefined", () => {
    const got = normalizeConnections({
      connections: [{ type: "claude", name: "n", connected: false, usage_supported: true }],
    } as never);
    const c = got[0];
    expect(c.email).toBe("");
    expect(c.plan).toBe("");
    expect(c.org).toBe("");
    expect(c.authMethod).toBe("");
    expect(c.usageErr).toBe("");
  });

  it("carries a usage error through", () => {
    const got = normalizeConnections({
      connections: [
        { type: "claude", name: "n", connected: true, usage_supported: true, usage_err: "usage endpoint: 401" },
      ],
    } as never);
    expect(got[0].usageErr).toBe("usage endpoint: 401");
  });
});
