import { describe, test, expect } from "vitest";
import { agentMentionRows, fileMentionRows } from "../mention-menu.js";

describe("agentMentionRows", () => {
  const agents = [
    { handle: "helper", label: "helper" },
    { handle: "anton", label: "Anton", hint: "Billing", group: "team" as const },
  ];

  test("Team rows come first, then sub-agents (group defaults to subagent)", () => {
    const rows = agentMentionRows(agents, "");
    expect(rows.map((r) => r.category)).toEqual(["Team", "Sub-agents"]);
    expect(rows[0]).toMatchObject({ value: "anton", label: "Anton", hint: "@anton · Billing", avatar: {} });
    expect(rows[1].avatar).toBeUndefined();
  });

  test("matches a sub-agent by handle only, a Team agent by name too", () => {
    expect(agentMentionRows(agents, "ANT").map((r) => r.value)).toEqual(["anton"]);
    expect(agentMentionRows([{ handle: "x", label: "Anton" }], "ant")).toEqual([]);
  });
});

describe("fileMentionRows", () => {
  test("titles files only when asked", () => {
    expect(fileMentionRows(["a"], true)[0].category).toBe("Files");
    expect(fileMentionRows(["a"], false)[0].category).toBeUndefined();
  });
});
