import { describe, it, expect } from "vitest";
import { match, initialRoute } from "../router.js";

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

describe("router.initialRoute", () => {
  it("returns session path when hash is / and initialSession is set", () => {
    expect(initialRoute("/", "abc")).toBe("/sessions/abc");
  });

  it("returns session path when hash is empty and initialSession is set", () => {
    expect(initialRoute("", "abc")).toBe("/sessions/abc");
  });

  it("returns null when explicit hash route is present (hash wins)", () => {
    expect(initialRoute("/sessions/xyz", "abc")).toBeNull();
  });

  it("returns null when initialSession is empty string", () => {
    expect(initialRoute("/", "")).toBeNull();
  });

  it("returns null when initialSession is null", () => {
    expect(initialRoute("/", null)).toBeNull();
  });
});
