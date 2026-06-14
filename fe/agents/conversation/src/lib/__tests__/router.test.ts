import { describe, it, expect } from "vitest";
import { match } from "../router.js";

describe("router.match", () => {
  it("returns params for /sessions/:id", () => {
    expect(match("/sessions/:id", "/sessions/abc")).toEqual({ id: "abc" });
  });

  it("returns null for non-matching pattern", () => {
    expect(match("/sessions/:id", "/")).toBeNull();
    expect(match("/sessions/:id", "/sessions/abc/extra")).toBeNull();
    expect(match("/sessions/:id", "/other/abc")).toBeNull();
  });

  it("decodes URI-encoded params", () => {
    expect(match("/sessions/:id", "/sessions/abc%20def")).toEqual({ id: "abc def" });
  });

  it("handles root / matching /", () => {
    expect(match("/", "/")).toEqual({});
  });

  it("returns null when root pattern does not match detail path", () => {
    expect(match("/", "/sessions/abc")).toBeNull();
  });
});
