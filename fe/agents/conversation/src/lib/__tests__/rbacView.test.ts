import { describe, expect, it } from "vitest";
import { cellOf, roleOrder, whoIs } from "../rbacView.js";
import type { RBACDoc } from "../api/team.js";

const doc: RBACDoc = {
  roles: { trusted: { rank: 70, lend: "all" }, member: { rank: 40, lend: "read", write_needs_approval: true } },
  rules: [{ match: "user", value: "u-a", role: "trusted" }, { match: "shared", role: "member" }],
  lend: { docs: {}, tickets: { member: "all" }, files: { member: { level: "pick", ops: ["get", "list"] } } },
};

describe("rbacView", () => {
  it("orders roles owner, by rank, guest", () => {
    expect(roleOrder(doc)).toEqual(["owner", "trusted", "member", "guest"]);
  });
  it("says who lands in each role", () => {
    const w = whoIs(doc, (id) => (id === "u-a" ? "Person A" : id));
    expect(w.owner).toEqual(["You"]);
    expect(w.trusted).toEqual(["Person A"]);
    expect(w.member).toEqual(["everyone it's shared with"]);
    expect(w.guest[0]).toBe("everyone else");
  });
  it("reads a cell the way the server lends", () => {
    expect(cellOf(doc, "trusted", "docs").text).toBe("Write");
    expect(cellOf(doc, "member", "docs").text).toBe("Read");
    expect(cellOf(doc, "member", "tickets")).toEqual({ text: "Read", tone: "read", approval: true });
    expect(cellOf(doc, "member", "files").text).toBe("2 ops");
    expect(cellOf(doc, "guest", "docs").text).toBe("Off");
    expect(cellOf(doc, "member", "chat").text).toBe("Off");
    expect(cellOf(doc, "owner", "chat").text).toBe("Write");
  });
  it("shows a guest lend and a co-owner", () => {
    const d: RBACDoc = { ...doc, rules: [{ match: "user", value: "u-b", role: "owner" }, ...doc.rules], lend: { ...doc.lend, music: { guest: "all" } } };
    expect(cellOf(d, "guest", "music")).toEqual({ text: "Write", tone: "on", approval: false });
    expect(cellOf(d, "guest", "docs").text).toBe("Off");
    expect(whoIs(d, (id) => (id === "u-b" ? "Person B" : id)).owner).toEqual(["You", "Person B"]);
  });
});
