import { describe, it, expect } from "vitest";
import { clampText, newTextId, resizeRect, toPx, toRelative, STICKY_TEXT_W } from "./stickyText";

describe("clampText", () => {
  it("keeps the card inside the board", () => {
    expect(clampText({ id: "a", content: "", x: 0.9, y: -0.2, width: 0.4 })).toEqual({ id: "a", content: "", x: 0.6, y: 0, width: 0.4 });
  });
  it("clamps y by the card height and width to 1", () => {
    const t = clampText({ id: "a", content: "", x: -1, y: 0.95, width: 3, color: "blue" }, 0.25);
    expect(t).toEqual({ id: "a", content: "", x: 0, y: 0.75, width: 1, color: "blue" });
  });
  it("defaults a missing width and enforces the minimum", () => {
    expect(clampText({ id: "a", content: "", x: 0, y: 0 }).width).toBe(STICKY_TEXT_W);
    expect(clampText({ id: "a", content: "", x: 0, y: 0, width: 0.01 }, 0, 0.1).width).toBe(0.1);
  });
});

describe("px <-> relative", () => {
  it("round-trips against the board size", () => {
    expect(toRelative(120, 480)).toBe(0.25);
    expect(toPx(0.25, 480)).toBe(120);
    expect(toRelative(10, 0)).toBe(0);
  });
});

describe("newTextId", () => {
  it("skips used ids", () => {
    expect(newTextId([{ id: "t2", content: "", x: 0, y: 0 }])).toBe("t3");
    expect(newTextId([{ id: "t1", content: "", x: 0, y: 0 }, { id: "t3", content: "", x: 0, y: 0 }])).toBe("t4");
  });
});

describe("resizeRect", () => {
  const r = { x: 100, y: 50, w: 400, h: 300 };
  it("right/bottom edges grow in place", () => {
    expect(resizeRect("se", r, 40, 20)).toEqual({ x: 100, y: 50, w: 440, h: 320 });
    expect(resizeRect("e", r, 40, 20)).toEqual({ x: 100, y: 50, w: 440, h: 300 });
  });
  it("left/top edges move x/y and keep the opposite edge", () => {
    expect(resizeRect("nw", r, 30, 10)).toEqual({ x: 130, y: 60, w: 370, h: 290 });
    expect(resizeRect("w", r, -50, 0)).toEqual({ x: 50, y: 50, w: 450, h: 300 });
  });
  it("stops at the minimum without drifting the anchored edge", () => {
    expect(resizeRect("nw", r, 1000, 1000, 160, 100)).toEqual({ x: 340, y: 250, w: 160, h: 100 });
    expect(resizeRect("se", r, -1000, -1000, 160, 100)).toEqual({ x: 100, y: 50, w: 160, h: 100 });
  });
});
