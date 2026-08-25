import { describe, test, expect } from "vitest";
import { formatSize, formatRelTime } from "../fileMeta.js";

describe("formatSize", () => {
  test("bytes", () => { expect(formatSize(512)).toBe("512 B"); });
  test("kib", () => { expect(formatSize(2048)).toBe("2.0 KB"); });
  test("mib", () => { expect(formatSize(3 * 1024 * 1024)).toBe("3.0 MB"); });
  test("empty when undefined", () => { expect(formatSize(undefined as unknown as number)).toBe(""); });
});

describe("formatRelTime", () => {
  test("empty when 0", () => { expect(formatRelTime(0)).toBe(""); });
  test("just now under a minute", () => { expect(formatRelTime(Date.now())).toBe("just now"); });
});
