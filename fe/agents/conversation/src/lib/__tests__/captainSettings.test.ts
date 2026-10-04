import { describe, it, expect } from "vitest";
import { captainCanOf, historyLine, historyStatus, CAPTAIN_CAN_OPTIONS, DEFAULT_CAPTAIN_CAN } from "../captainSettings.js";
import { captainBlock } from "../captainSettings.js";

describe("captainCanOf", () => {
  it("defaults to persona + routines on, access off", () => {
    expect(captainCanOf(undefined)).toEqual({ persona: true, access: false, routines: true });
    expect(DEFAULT_CAPTAIN_CAN.access).toBe(false);
  });
  it("keeps stored values", () => {
    expect(captainCanOf({ persona: false, access: true, routines: false })).toEqual({ persona: false, access: true, routines: false });
  });
  it("has one toggle per permission", () => {
    expect(CAPTAIN_CAN_OPTIONS.map((o) => o.key)).toEqual(["persona", "access", "routines"]);
  });
});

describe("history rows", () => {
  const base = { id: "h", actor: "@captain", at: "2026-10-03T10:00:00Z" };
  it("joins the diff", () => {
    expect(historyLine({ ...base, status: "applied", diff: ["+Notion (read)", "-Slack"] })).toBe("+Notion (read), -Slack");
    expect(historyLine({ ...base, status: "applied", diff: [] })).toBe("no visible change");
  });
  it("names the decision", () => {
    expect(historyStatus({ ...base, status: "applied", diff: [], decided_by: "Yoga" })).toBe("approved by Yoga");
    expect(historyStatus({ ...base, actor: "Yoga", status: "applied", diff: [] })).toBe("applied");
    expect(historyStatus({ ...base, status: "declined", diff: [], decided_by: "Yoga" })).toBe("declined by Yoga");
    expect(historyStatus({ ...base, status: "pending", diff: [] })).toBe("waiting for approval");
  });
});

describe("captainBlock", () => {
  it("a Wick agent of the owner that is not shared may take the role", () => {
    expect(captainBlock({ kind: "" }, 0)).toBe("");
  });
  it("remote, viewer and shared agents are refused with a reason", () => {
    expect(captainBlock({ kind: "a2a-remote" }, 0)).toContain("remote agent can't be the Captain");
    expect(captainBlock({ role: "viewer" }, 0)).toContain("Only the owner");
    expect(captainBlock({}, 2)).toContain("stop sharing it first");
  });
});
