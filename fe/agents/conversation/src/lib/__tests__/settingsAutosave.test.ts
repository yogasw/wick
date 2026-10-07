import { describe, test, expect } from "vitest";
import { sameValue, patchKey } from "../settingsAutosave.js";

describe("settings autosave compare", () => {
  test("object key order does not matter", () => {
    const sent = { kind: "blob", shape: "cloud", expression: "happy", color: "#ef4444" };
    const echoed = { kind: "blob", shape: "cloud", color: "#ef4444", expression: "happy" };
    expect(JSON.stringify(sent)).not.toBe(JSON.stringify(echoed));
    expect(sameValue(sent, echoed)).toBe(true);
    expect(patchKey({ avatar: sent })).toBe(patchKey({ avatar: echoed }));
  });
  test("real differences still count", () => {
    expect(sameValue({ a: 1 }, { a: 2 })).toBe(false);
    expect(sameValue({ a: 1 }, { a: 1, b: 2 })).toBe(false);
    expect(sameValue([1, 2], [2, 1])).toBe(false);
    expect(sameValue([{ x: 1, y: 2 }], [{ y: 2, x: 1 }])).toBe(true);
    expect(sameValue(null, {})).toBe(false);
  });
  test("undefined keys count as absent", () => {
    expect(sameValue({ a: 1, b: undefined }, { a: 1 })).toBe(true);
  });
});
