import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { get } from "svelte/store";
import { match, routeFromPath, push, route } from "../router.js";

describe("router.match", () => {
  it("returns params for /skills/:name", () => {
    expect(match("/skills/:name", "/skills/my-skill")).toEqual({ name: "my-skill" });
  });

  it("returns null for non-matching pattern", () => {
    expect(match("/skills/:name", "/")).toBeNull();
    expect(match("/skills/:name", "/skills/foo/files/bar")).toBeNull();
  });

  it("decodes URI-encoded params", () => {
    expect(match("/skills/:name", "/skills/my%20skill")).toEqual({ name: "my skill" });
  });

  it("matches file route with 4 segments", () => {
    expect(match("/skills/:folder/files/:file", "/skills/myfolder/files/myfile.md")).toEqual({
      folder: "myfolder",
      file: "myfile.md",
    });
  });

  it("returns null when file pattern given list path", () => {
    expect(match("/skills/:folder/files/:file", "/skills/foo")).toBeNull();
  });

  it("catch-all :file... captures single segment", () => {
    expect(match("/skills/:folder/files/:file...", "/skills/system-map/files/agents")).toEqual({
      folder: "system-map",
      file: "agents",
    });
  });

  it("catch-all :file... captures nested path", () => {
    expect(match("/skills/:folder/files/:file...", "/skills/system-map/files/agents/sub/x.md")).toEqual({
      folder: "system-map",
      file: "agents/sub/x.md",
    });
  });

  it("catch-all :file... decodes URI-encoded segments", () => {
    expect(match("/skills/:folder/files/:file...", "/skills/my-skill/files/agents%2Fsub/x.md")).toEqual({
      folder: "my-skill",
      file: "agents/sub/x.md",
    });
  });

  it("catch-all :file... returns null when prefix segments do not match", () => {
    expect(match("/skills/:folder/files/:file...", "/skills/foo")).toBeNull();
  });

  it("returns empty object for exact root match", () => {
    expect(match("/", "/")).toEqual({});
  });

  it("returns null when root pattern does not match detail path", () => {
    expect(match("/", "/skills/abc")).toBeNull();
  });
});

describe("router.routeFromPath", () => {
  const base = "/tools/agents";

  it("returns / for base/skills exact", () => {
    expect(routeFromPath(`${base}/skills`, base)).toBe("/");
  });

  it("returns / for base/skills with trailing slash", () => {
    expect(routeFromPath(`${base}/skills/`, base)).toBe("/");
  });

  it("returns /skills/abc for base/skills/abc", () => {
    expect(routeFromPath(`${base}/skills/abc`, base)).toBe("/skills/abc");
  });

  it("returns /skills/my-skill for base/skills/my-skill", () => {
    expect(routeFromPath(`${base}/skills/my-skill`, base)).toBe("/skills/my-skill");
  });

  it("match works correctly on result of routeFromPath for detail", () => {
    const r = routeFromPath(`${base}/skills/abc`, base);
    expect(match("/skills/:name", r)).toEqual({ name: "abc" });
  });

  it("match returns null on result of routeFromPath for list", () => {
    const r = routeFromPath(`${base}/skills`, base);
    expect(match("/skills/:name", r)).toBeNull();
  });
});

describe("router.push + route store", () => {
  const base = "/tools/agents";

  beforeEach(() => {
    const el = document.createElement("div");
    el.id = "app";
    el.dataset.base = base;
    document.body.appendChild(el);

    history.replaceState({}, "", `${base}/skills`);
  });

  afterEach(() => {
    const el = document.getElementById("app");
    if (el) el.remove();
    vi.restoreAllMocks();
  });

  it("push('/skills/my-skill') updates window.location.pathname", () => {
    push("/skills/my-skill");
    expect(window.location.pathname).toBe(`${base}/skills/my-skill`);
  });

  it("push('/') updates pathname to base/skills (list)", () => {
    push("/");
    expect(window.location.pathname).toBe(`${base}/skills`);
  });

  it("push('/skills/my-skill') updates route store to /skills/my-skill", () => {
    push("/skills/my-skill");
    expect(get(route)).toBe("/skills/my-skill");
  });

  it("push('/') updates route store to /", () => {
    push("/");
    expect(get(route)).toBe("/");
  });
});
