import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { get } from "svelte/store";
import { match, routeFromPath, push, route } from "../router.js";

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

describe("router.routeFromPath", () => {
  const base = "/tools/agents";

  it("returns / for base/sessions exact", () => {
    expect(routeFromPath(`${base}/sessions`, base)).toBe("/");
  });

  it("returns / for base/sessions with trailing slash", () => {
    expect(routeFromPath(`${base}/sessions/`, base)).toBe("/");
  });

  it("returns /sessions/abc for base/sessions/abc", () => {
    expect(routeFromPath(`${base}/sessions/abc`, base)).toBe("/sessions/abc");
  });

  it("returns /sessions/xyz-123 for base/sessions/xyz-123", () => {
    expect(routeFromPath(`${base}/sessions/xyz-123`, base)).toBe("/sessions/xyz-123");
  });

  it("match works correctly on result of routeFromPath for detail", () => {
    const r = routeFromPath(`${base}/sessions/abc`, base);
    expect(match("/sessions/:id", r)).toEqual({ id: "abc" });
  });

  it("match returns null on result of routeFromPath for list", () => {
    const r = routeFromPath(`${base}/sessions`, base);
    expect(match("/sessions/:id", r)).toBeNull();
  });
});

describe("router.push + route store", () => {
  const base = "/tools/agents";

  beforeEach(() => {
    /* stub #app element so router can read base */
    const el = document.createElement("div");
    el.id = "app";
    el.dataset.base = base;
    document.body.appendChild(el);

    /* reset pathname to list path before each test */
    history.replaceState({}, "", `${base}/sessions`);
  });

  afterEach(() => {
    const el = document.getElementById("app");
    if (el) el.remove();
    vi.restoreAllMocks();
  });

  it("push('/sessions/xyz') updates window.location.pathname", () => {
    push("/sessions/xyz");
    expect(window.location.pathname).toBe(`${base}/sessions/xyz`);
  });

  it("push('/') updates pathname to base/sessions (list)", () => {
    push("/");
    expect(window.location.pathname).toBe(`${base}/sessions`);
  });

  it("push('/sessions/xyz') updates route store to /sessions/xyz", () => {
    push("/sessions/xyz");
    expect(get(route)).toBe("/sessions/xyz");
  });

  it("push('/') updates route store to /", () => {
    push("/");
    expect(get(route)).toBe("/");
  });
});
