import { describe, test, expect } from "vitest";
import { isValidFileName } from "../fileName.js";

describe("isValidFileName", () => {
  test("accepts a plain name", () => {
    expect(isValidFileName("notes.txt")).toBe(true);
    expect(isValidFileName("README")).toBe(true);
  });

  test("trims surrounding whitespace before validating", () => {
    expect(isValidFileName("  notes.txt  ")).toBe(true);
  });

  test("rejects empty or whitespace-only names", () => {
    expect(isValidFileName("")).toBe(false);
    expect(isValidFileName("   ")).toBe(false);
  });

  test("rejects names containing a forward slash", () => {
    expect(isValidFileName("a/b")).toBe(false);
  });

  test("rejects names containing a backslash", () => {
    expect(isValidFileName("a\\b")).toBe(false);
  });

  test("rejects names containing a parent-dir token", () => {
    expect(isValidFileName("..")).toBe(false);
    expect(isValidFileName("a..b")).toBe(false);
  });
});
