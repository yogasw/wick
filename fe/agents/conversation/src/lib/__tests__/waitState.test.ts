import { describe, expect, it } from "vitest";
import { parseWaitState, waitNote, formatSilence, type WaitState } from "../waitState.js";

const base: WaitState = {
  queue_pos: 0, queue_len: 0, provider: "claude/claude_waba", used: 4, max: 4,
  global_used: 6, global_max: 20, hold: false, mem_pct: 50, cpu_pct: 40, mem_pressure: 0, cpu_pressure: 0,
};

describe("waitNote", () => {
  it("a queued turn says its place and the provider's slots instead of thinking", () => {
    expect(waitNote({ ...base, queue_pos: 2, queue_len: 3 }, 0)).toEqual({
      label: "queued #2 · claude/claude_waba 4/4",
      replaces: true,
    });
  });

  it("a running turn says nothing until it has been silent for 30s", () => {
    expect(waitNote(base, 29_000)).toBeNull();
    expect(waitNote(base, 100_000)).toEqual({ label: "no output for 1m 40s", replaces: false });
  });

  it("names a strained host, with the guard's reason when it holds new agents", () => {
    const held = { ...base, hold: true, hold_reason: "memory pressure full avg10 60%" };
    expect(waitNote(held, 120_000)!.label).toBe("no output for 2m 0s · host strained (memory pressure full avg10 60%)");
    const stalling = { ...base, mem_pct: 93, cpu_pct: 97, mem_pressure: 40 };
    expect(waitNote(stalling, 45_000)!.label).toBe("no output for 45s · host strained (memory 93%, CPU 97%)");
  });

  it("an uncapped provider reads as ∞, and no wait state still reports the silence", () => {
    expect(waitNote({ ...base, queue_pos: 1, max: 0 }, 0)!.label).toBe("queued #1 · claude/claude_waba 4/∞");
    expect(waitNote(null, 61_000)!.label).toBe("no output for 1m 1s");
  });

  it("a restart after an idle kill is named", () => {
    expect(waitNote({ ...base, restart_attempt: 1, restart_max: 2 }, 0)!.label).toBe("restarting after no output, retry 1/2");
  });
});

describe("parseWaitState / formatSilence", () => {
  it("parses the event body and rejects junk", () => {
    expect(parseWaitState(JSON.stringify(base))!.provider).toBe("claude/claude_waba");
    expect(parseWaitState("not json")).toBeNull();
    expect(parseWaitState(undefined)).toBeNull();
  });

  it("formats seconds, minutes and hours", () => {
    expect(formatSilence(5_000)).toBe("5s");
    expect(formatSilence(25 * 60_000)).toBe("25m 0s");
    expect(formatSilence(3_720_000)).toBe("1h 2m");
  });
});
