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

  it("extracts key + id from the detail route", () => {
    expect(match("/connectors/:key/:id", "/connectors/slack/abc-123")).toEqual({
      key: "slack",
      id: "abc-123",
    });
  });

  it("does not match the detail pattern against the list route", () => {
    expect(match("/connectors/:key/:id", "/connectors/slack")).toBeNull();
  });

  it("extracts key + id from the test route", () => {
    expect(match("/connectors/:key/:id/test", "/connectors/slack/abc-123/test")).toEqual({
      key: "slack",
      id: "abc-123",
    });
  });

  it("extracts key + id from the history route", () => {
    expect(match("/connectors/:key/:id/history", "/connectors/slack/abc-123/history")).toEqual({
      key: "slack",
      id: "abc-123",
    });
  });

  it("does not match the test route against the bare detail route", () => {
    expect(match("/connectors/:key/:id/test", "/connectors/slack/abc-123")).toBeNull();
  });

  it("does not match the detail route against the test route", () => {
    expect(match("/connectors/:key/:id", "/connectors/slack/abc-123/test")).toBeNull();
  });

  it("extracts defID from the custom edit route", () => {
    expect(match("/custom/:defID/edit", "/custom/def-123/edit")).toEqual({ defID: "def-123" });
  });

  it("does not match the custom edit route against the static custom routes", () => {
    expect(match("/custom/:defID/edit", "/custom/paste")).toBeNull();
    expect(match("/custom/:defID/edit", "/custom/manual")).toBeNull();
    expect(match("/custom/:defID/edit", "/custom/review")).toBeNull();
  });
});
