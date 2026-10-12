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

describe("agentMentionRows — a ticket room", () => {
  test("your agents under their own title, others' greyed under 'In this room · others'", () => {
    const rows = agentMentionRows(
      [
        { handle: "captain", label: "Captain", group: "team" as const, section: "Your Team" },
        { handle: "hana/captain", label: "Captain · @hana", hint: "only takes instructions from @hana", group: "others" as const },
        { handle: "helper", label: "helper" },
      ],
      "",
    );
    expect(rows.map((r) => [r.value, r.category, !!r.disabled])).toEqual([
      ["captain", "Your Team", false],
      ["hana/captain", "In this room · others", true],
      ["helper", "Sub-agents", false],
    ]);
  });
});

describe("fileMentionRows", () => {
  test("titles files only when asked", () => {
    expect(fileMentionRows(["a"], true)[0].category).toBe("Files");
    expect(fileMentionRows(["a"], false)[0].category).toBeUndefined();
  });
});
