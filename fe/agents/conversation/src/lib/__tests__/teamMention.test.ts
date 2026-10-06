import { describe, test, expect } from "vitest";
import { teamMentionAgents, teamSender, handoffState, subAgentTitle, subAgentTurns, speakerVia } from "../teamMention.js";

describe("teamMentionAgents", () => {
  const peers = [
    { id: "a1", handle: "captain", name: "Captain", description: "Leads\nmore", disabled: false, avatar: { shape: "blob", color: "#000" } },
    { id: "a2", handle: "anton", name: "Anton", description: "", disabled: false },
    { id: "a3", handle: "sleepy", name: "Sleepy", description: "", disabled: true },
  ];
  test("drops the agent itself and disabled agents, keeps avatar and tagline", () => {
    const out = teamMentionAgents(peers, "a2");
    expect(out.map((a) => a.handle)).toEqual(["captain"]);
    expect(out[0]).toMatchObject({ label: "Captain", hint: "Leads", group: "team", avatar: { shape: "blob", color: "#000" } });
  });
  test("no peers → nothing", () => {
    expect(teamMentionAgents(null, "x")).toEqual([]);
  });
});

describe("teamSender / handoffState", () => {
  test("only a team-sourced framed turn is from an agent", () => {
    expect(teamSender("team", "Message from Anton (@anton):\nhi")).toEqual({ name: "Anton", handle: "anton", body: "hi" });
    expect(teamSender("ui", "Message from Anton (@anton):\nhi")).toBeNull();
  });
  test("a teammate's reply handed back over the Team link is from that agent", () => {
    const text = "Reply from Luna (@luna) [task 01a1, completed]:\n\nHalo!\nSiap.";
    expect(teamSender("subagent", text)).toEqual({ name: "Luna", handle: "luna", body: "Halo!\nSiap." });
    // Typed by a person, or another source: stays a person's message.
    expect(teamSender("ui", text)).toBeNull();
    expect(teamSender("subagent", "Reply from Luna: hi")).toBeNull();
  });
  test("A2A states map to short words", () => {
    expect(handoffState("TASK_STATE_COMPLETED")).toBe("completed");
    expect(handoffState("TASK_STATE_INPUT_REQUIRED")).toBe("needs input");
    expect(handoffState("working")).toBe("working");
  });
});

describe("sub-agent card", () => {
  test("title prefers the first task over resume text", () => {
    expect(subAgentTitle({ label: "Your previous run ended in an error. x", title: "Do X" })).toBe("Do X");
    expect(subAgentTitle({ label: "Your previous run ended in an error. x" })).toContain("Continued task");
    expect(subAgentTitle({ label: "Do Y" })).toBe("Do Y");
  });
  test("turns are per leg once continued", () => {
    expect(subAgentTurns({ turns_used: 5, max_turns: 20 })).toBe("5/20 turns");
    expect(subAgentTurns({ turns_used: 51, max_turns: 101, resumes: 1, leg_base_turns: 40 })).toBe("11 turns this leg · 51 total");
  });
});

describe("speakerVia", () => {
  test("names the teammate behind the user turn a via-mention reply answers", () => {
    const turns = [
      { role: "user", source: "team", text: "Message from Captain (@captain):\ncheck 401s" },
      { role: "assistant", text: "on it", speaker: { via: "mention" } },
      { role: "assistant", text: "direct", speaker: { via: "direct" } },
    ];
    expect(speakerVia(turns, 1)).toBe("captain");
    expect(speakerVia(turns, 2)).toBe("");
  });
  test("a person's message never names a teammate", () => {
    const turns = [{ role: "user", text: "Message from Captain (@captain):\nx" }, { role: "assistant", text: "y", speaker: { via: "mention" } }];
    expect(speakerVia(turns, 1)).toBe("");
  });
});
