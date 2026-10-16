import { describe, test, expect } from "vitest";
import {
  assigneeParam,
  assigneeSummary,
  assigneesOf,
  migrateTicketFilter,
  withAssignees,
} from "../ticketAssigneeFilter.js";

describe("ticketAssigneeFilter", () => {
  test("an old single assignee reads as a one-entry list", () => {
    expect(assigneesOf({ assignee: "me" })).toEqual(["me"]);
    expect(assigneesOf({ assignee: "" })).toEqual([]);
    expect(assigneesOf({})).toEqual([]);
    // The list wins over a stale single value.
    expect(assigneesOf({ assignee: "u1", assignees: ["u2", "none"] })).toEqual(["u2", "none"]);
  });

  test("migrating drops the old key and keeps the rest", () => {
    expect(migrateTicketFilter({ assignee: "u7", view_mode: "card" })).toEqual({ assignees: ["u7"], view_mode: "card" });
    expect(migrateTicketFilter({ assignee: "", statuses: ["open"] })).toEqual({ statuses: ["open"] });
  });

  test("an empty pick is no filter at all", () => {
    expect(withAssignees({ assignees: ["me"], view_mode: "card" }, [])).toEqual({ view_mode: "card" });
    expect(withAssignees({ assignee: "me" }, ["u1", "none"])).toEqual({ assignees: ["u1", "none"] });
    expect(assigneeParam([])).toBeUndefined();
    expect(assigneeParam(["me", "u1", "none"])).toBe("me,u1,none");
  });

  test("the summary names two by first name and counts the rest", () => {
    const names = { w: "Wildhan Ibrahim Amin", i: "Iqbal Khairil Putra" };
    expect(assigneeSummary([], names)).toBe("Everyone");
    expect(assigneeSummary(["me"], names)).toBe("Me");
    expect(assigneeSummary(["me", "w", "i"], names)).toBe("Me, Wildhan +1");
    expect(assigneeSummary(["none", "w"], names)).toBe("Unassigned, Wildhan");
    // The viewer's own id reads as Me; an unknown id as itself.
    expect(assigneeSummary(["u-me", "ghost"], names, "u-me")).toBe("Me, ghost");
    // "me" plus the viewer's own id is one person.
    expect(assigneeSummary(["me", "u-me", "w"], names, "u-me")).toBe("Me, Wildhan");
  });
});
