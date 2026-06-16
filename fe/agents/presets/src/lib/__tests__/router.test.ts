import { describe, it, expect } from "vitest";
import { routeFromPath } from "$lib/router.js";

describe("routeFromPath", () => {
  const base = "/tools/agents";

  it("returns / for exact presets path", () => {
    expect(routeFromPath(`${base}/presets`, base)).toBe("/");
  });

  it("returns / for presets trailing slash", () => {
    expect(routeFromPath(`${base}/presets/`, base)).toBe("/");
  });

  it("returns /presets/foo for preset detail", () => {
    expect(routeFromPath(`${base}/presets/foo`, base)).toBe("/presets/foo");
  });

  it("returns / for unrecognized paths", () => {
    expect(routeFromPath(`${base}/other`, base)).toBe("/");
  });
});
