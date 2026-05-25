import { describe, it, expect } from "vitest";
import { match } from "./router";

describe("router.match", () => {
  it("returns params for /edit/:id", () => {
    expect(match("/edit/:id", "/edit/abc")).toEqual({ id: "abc" });
  });

  it("returns null for non-matching pattern", () => {
    expect(match("/edit/:id", "/")).toBeNull();
    expect(match("/edit/:id", "/edit/abc/extra")).toBeNull();
    expect(match("/edit/:id", "/other/abc")).toBeNull();
  });

  it("decodes URI-encoded params", () => {
    expect(match("/edit/:id", "/edit/abc%20def")).toEqual({ id: "abc def" });
  });

  it("handles root /", () => {
    expect(match("/", "/")).toEqual({});
  });
});
