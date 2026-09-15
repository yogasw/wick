import { describe, it, expect } from "vitest";
import { exitStatus, exitBadgeClass } from "../exitstatus.js";

describe("exitStatus", () => {
  it("treats an empty reason as still running", () => {
    const st = exitStatus("");
    expect(st.label).toBe("running");
    expect(st.tone).toBe("running");
  });

  it("calls every normal reap 'ended' instead of leaking the pool's wording", () => {
    for (const reason of ["clean", "idle", "respawn"]) {
      const st = exitStatus(reason);
      expect(st.label).toBe("ended");
      expect(st.tone).toBe("ended");
      // the raw reason still has to be discoverable
      expect(st.title).not.toBe("");
    }
  });

  it("keeps the idle reap explainable in the tooltip", () => {
    expect(exitStatus("idle").title).toMatch(/idle window/);
  });

  it("prefers the log's detail over the canned explanation", () => {
    expect(exitStatus("idle", "custom detail").title).toBe("custom detail");
  });

  it("flags the failures in red", () => {
    expect(exitStatus("unclean").tone).toBe("bad");
    expect(exitStatus("error").tone).toBe("bad");
    expect(exitStatus("oom").label).toBe("out of memory");
  });

  it("shows the exit code next to an error when there is one", () => {
    expect(exitStatus("error", "", 137).label).toBe("error (137)");
    expect(exitStatus("error", "", 0).label).toBe("error");
  });

  it("keeps 'stopped' distinct from a finished turn", () => {
    expect(exitStatus("stopped").label).toBe("stopped");
  });

  it("shows an unknown reason verbatim", () => {
    expect(exitStatus("weird-new-reason").label).toBe("weird-new-reason");
  });

  it("maps tones to badge classes", () => {
    expect(exitBadgeClass("running")).toMatch(/green/);
    expect(exitBadgeClass("bad")).toMatch(/red/);
    expect(exitBadgeClass("ended")).toMatch(/navy/);
  });
});
