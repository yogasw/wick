import { describe, test, expect } from "vitest";
import { avatarTone } from "../senderTone.js";

describe("avatarTone", () => {
  // The whole point: one person keeps one colour. If this drifted, a thread
  // would reshuffle its avatars on every reload and the colour would stop
  // meaning anything.
  test("is stable for the same key", () => {
    expect(avatarTone("U0104").cls).toBe(avatarTone("U0104").cls);
  });

  test("always returns a usable class pair", () => {
    for (const key of ["U0104", "8812", "wick-uuid-1", "", "   "]) {
      const cls = avatarTone(key).cls;
      expect(cls).toMatch(/\bbg-/);
      expect(cls).toMatch(/\bdark:bg-/);
    }
  });

  // Sequential Slack IDs are the realistic input, and a naive charCode sum
  // buckets them together — a whole team would come out one colour, which is
  // exactly the case the avatar exists to solve.
  test("spreads sequential ids across several tones", () => {
    const ids = Array.from({ length: 12 }, (_, i) => `U010${i}`);
    const distinct = new Set(ids.map((id) => avatarTone(id).cls));
    expect(distinct.size).toBeGreaterThan(2);
  });

  test("ids differing only in the last character do not collide", () => {
    expect(avatarTone("U0104").cls).not.toBe(avatarTone("U0105").cls);
  });
});
