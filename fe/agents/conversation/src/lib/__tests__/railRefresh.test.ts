import { describe, test, expect } from "vitest";
import { railRefreshTargets } from "../railRefresh.js";

describe("railRefreshTargets", () => {
  test("opening a panel refreshes it", () => {
    expect(railRefreshTargets(null, "scheduled")).toEqual(["scheduled"]);
    expect(railRefreshTargets(null, "process")).toEqual(["process"]);
  });

  test("closing a panel refreshes it too, so strip badges are current", () => {
    expect(railRefreshTargets("subagents", null)).toEqual(["subagents"]);
  });

  test("switching panels refreshes the one closed and the one opened", () => {
    expect(railRefreshTargets("ticket", "scheduled")).toEqual(["ticket", "scheduled"]);
  });

  test("notes shares the ticket's data and refreshes the same way", () => {
    expect(railRefreshTargets(null, "notes")).toEqual(["notes"]);
  });

  test("panels without live data are left alone", () => {
    expect(railRefreshTargets(null, "files")).toEqual([]);
    expect(railRefreshTargets("source", "todos")).toEqual([]);
    expect(railRefreshTargets("files", "ticket")).toEqual(["ticket"]);
  });

  test("no change, no request", () => {
    expect(railRefreshTargets("ticket", "ticket")).toEqual([]);
    expect(railRefreshTargets(null, null)).toEqual([]);
  });
});
