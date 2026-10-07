import { describe, expect, it } from "vitest";
import { sendModeNote } from "../sendmode";

describe("sendModeNote", () => {
  it("server mode marks append as supported for omp and opencode", () => {
    expect(sendModeNote("omp", "append", true)).toMatchObject({ level: "ok" });
    expect(sendModeNote("omp", "default", true).text).toContain("steered into");
    expect(sendModeNote("opencode", "queue", true).text).toContain("added to");
  });
  it("run / -p marks append as not supported (queue + combine)", () => {
    const n = sendModeNote("omp", "append", false);
    expect(n.level).toBe("changed");
    expect(n.text).toContain("not supported");
    expect(sendModeNote("codex", "append", true).level).toBe("changed");
    expect(sendModeNote("opencode", "default", false).text).toContain("Queue + combine");
  });
  it("claude appends; spawn is parallel everywhere", () => {
    expect(sendModeNote("claude", "default", false).text).toContain("Append");
    expect(sendModeNote("omp", "spawn", true).text).toContain("parallel");
  });
});
