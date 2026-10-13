import { describe, it, expect } from "vitest";
import { agentOrigin, shortSessionId, unresolvedTitle } from "../origin.js";

describe("agentOrigin", () => {
  it("trusts the pool first", () => {
    expect(agentOrigin({ from_wick: true })).toBe("wick");
    expect(agentOrigin({ from_wick: true, wick_session: { detected: true } })).toBe("wick");
  });

  it("labels a resolved wick session the pool does not know", () => {
    expect(
      agentOrigin({
        from_wick: false,
        wick_session: { detected: true, session_id: "s1", title: "Debug", project_id: "p1", project: "Ops" },
      }),
    ).toBe("wick-session");
    // No project at all is a normal session, not a resolution failure.
    expect(agentOrigin({ wick_session: { detected: true, session_id: "s1", title: "Debug" } })).toBe("wick-session");
  });

  it("warns when the title or project cannot be resolved", () => {
    expect(agentOrigin({ wick_session: { detected: true, session_id: "s1" } })).toBe("wick-unresolved");
    expect(
      agentOrigin({ wick_session: { detected: true, session_id: "s1", title: "Debug", project_id: "p1" } }),
    ).toBe("wick-unresolved");
  });

  it("keeps a process with no wick token outside wick", () => {
    expect(agentOrigin({ from_wick: false })).toBe("outside");
    expect(agentOrigin({ wick_session: { detected: false } })).toBe("outside");
  });
});

describe("unresolvedTitle", () => {
  it("names the session by a short id only", () => {
    const t = unresolvedTitle({ wick_session: { detected: true, session_id: "abcdefghijklmnopqrstuvwxyz" } });
    expect(t).toContain("abcdefghijkl…");
    expect(t).not.toContain("mnop");
    expect(shortSessionId(undefined)).toBe("unknown");
    expect(shortSessionId("short")).toBe("short");
  });
});
