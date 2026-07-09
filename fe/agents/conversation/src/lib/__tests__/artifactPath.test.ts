import { describe, test, expect } from "vitest";
import { safeReadPath } from "../artifactPath.js";

describe("safeReadPath", () => {
  test("accepts a plain relative path", () => {
    expect(safeReadPath("artifact.json")).toBe("artifact.json");
  });

  test("accepts a nested relative path", () => {
    expect(safeReadPath("data/report.json")).toBe("data/report.json");
  });

  test("normalises backslashes to forward slashes", () => {
    expect(safeReadPath("data\\report.json")).toBe("data/report.json");
  });

  test("trims surrounding whitespace", () => {
    expect(safeReadPath("  a.json  ")).toBe("a.json");
  });

  test.each([
    ["", "empty"],
    ["   ", "whitespace only"],
    [null, "null"],
    [42, "non-string"],
    ["/etc/passwd", "posix absolute"],
    ["C:/Windows/x", "windows drive"],
    ["c:\\secret", "windows drive backslash"],
    ["http://evil.com/x", "http url"],
    ["file:///etc/passwd", "file url"],
    ["data:text/plain,x", "data url"],
    ["../secret.json", "parent traversal"],
    ["a/../../etc/passwd", "nested traversal"],
    ["../../x", "double traversal"],
  ])("rejects %s (%s)", (input, _label) => {
    expect(safeReadPath(input as unknown)).toBeNull();
  });

  test("a filename that merely contains .. as substring is allowed", () => {
    // only a whole `..` path segment is traversal; `..foo` is a real name
    expect(safeReadPath("a..b.json")).toBe("a..b.json");
  });
});
