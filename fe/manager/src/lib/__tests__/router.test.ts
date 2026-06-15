import { describe, it, expect } from "vitest";
import { routeFromPath, match } from "../router.js";

describe("routeFromPath", () => {
  it("returns / for exact base match", () => {
    expect(routeFromPath("/modules/manager/app", "/modules/manager/app")).toBe("/");
  });

  it("returns / for base with trailing slash", () => {
    expect(routeFromPath("/modules/manager/app/", "/modules/manager/app")).toBe("/");
  });

  it("returns sub-path for nested route", () => {
    expect(routeFromPath("/modules/manager/app/connectors/slack", "/modules/manager/app")).toBe("/connectors/slack");
  });

  it("returns / for path outside the base", () => {
    expect(routeFromPath("/modules/manager/", "/modules/manager/app")).toBe("/");
  });

  it("handles empty base", () => {
    expect(routeFromPath("/", "")).toBe("/");
    expect(routeFromPath("/connectors/slack", "")).toBe("/connectors/slack");
  });
});

describe("match", () => {
  it("extracts named params from a matching pattern", () => {
    expect(match("/connectors/:key", "/connectors/slack")).toEqual({ key: "slack" });
  });

  it("returns null when segment counts differ", () => {
    expect(match("/connectors/:key", "/connectors")).toBeNull();
  });

  it("returns null when a literal segment differs", () => {
    expect(match("/connectors/:key", "/runs/slack")).toBeNull();
  });

  it("decodes encoded param values", () => {
    expect(match("/connectors/:key", "/connectors/my%2Fconn")).toEqual({ key: "my/conn" });
  });
});
