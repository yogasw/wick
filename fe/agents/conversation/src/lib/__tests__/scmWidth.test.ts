import { describe, test, expect, beforeEach } from "vitest";
import {
  clampScmWidth, readScmWidth, writeScmWidth,
  SCM_MIN_W, SCM_MAX_W, SCM_DEFAULT_W, SCM_WIDTH_KEY,
} from "../scmWidth.js";

describe("clampScmWidth", () => {
  test("returns value within range unchanged", () => {
    expect(clampScmWidth(400)).toBe(400);
  });
  test("clamps below min to min", () => {
    expect(clampScmWidth(100)).toBe(SCM_MIN_W);
  });
  test("clamps above max to max", () => {
    expect(clampScmWidth(9999)).toBe(SCM_MAX_W);
  });
  test("rounds fractional input", () => {
    expect(clampScmWidth(400.6)).toBe(401);
  });
  test("falls back to default for non-finite input", () => {
    expect(clampScmWidth(NaN)).toBe(SCM_DEFAULT_W);
    expect(clampScmWidth(Infinity)).toBe(SCM_MAX_W);
  });
});

describe("readScmWidth / writeScmWidth", () => {
  beforeEach(() => { localStorage.clear(); });

  test("read returns default when nothing persisted", () => {
    expect(readScmWidth()).toBe(SCM_DEFAULT_W);
  });
  test("read returns persisted clamped value", () => {
    localStorage.setItem(SCM_WIDTH_KEY, "500");
    expect(readScmWidth()).toBe(500);
  });
  test("read clamps out-of-range persisted value", () => {
    localStorage.setItem(SCM_WIDTH_KEY, "5000");
    expect(readScmWidth()).toBe(SCM_MAX_W);
  });
  test("read falls back to default for garbage value", () => {
    localStorage.setItem(SCM_WIDTH_KEY, "not-a-number");
    expect(readScmWidth()).toBe(SCM_DEFAULT_W);
  });
  test("write persists clamped value and returns it", () => {
    expect(writeScmWidth(600)).toBe(600);
    expect(localStorage.getItem(SCM_WIDTH_KEY)).toBe("600");
  });
  test("write clamps before persisting", () => {
    expect(writeScmWidth(50)).toBe(SCM_MIN_W);
    expect(localStorage.getItem(SCM_WIDTH_KEY)).toBe(String(SCM_MIN_W));
  });
});
