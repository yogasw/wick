import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { avatarWorking, busyWork, rosterStatus, withTurn, withActivity, type WorkState } from "../rosterStatus.js";
import type { SessionActivity } from "../stores/sessionsStream.js";

const row = (p: Record<string, unknown> = {}) => ({ id: "a1", status: "idle", disabled: false, ...p });

describe("rosterStatus", () => {
  it("idle agent", () => {
    expect(rosterStatus(row())).toEqual({ unread: false, attention: false, work: "idle", typing: null, presence: "online", label: "online, idle" });
  });

  it("unread shows the dot and 'new message'", () => {
    const s = rosterStatus(row({ unread: true }));
    expect(s.unread).toBe(true);
    expect(s.label).toBe("new message");
  });

  it("the open chat is never unread", () => {
    expect(rosterStatus(row({ unread: true }), { activeId: "a1" }).unread).toBe(false);
    expect(rosterStatus(row({ unread: true }), { activeId: "other" }).unread).toBe(true);
  });

  it("a running turn with no tool in flight is thinking, not typing", () => {
    const s = rosterStatus(row({ status: "running" }));
    expect(s.work).toBe("thinking");
    expect(s.typing).toBe("thinking…");
    expect(s.label).toBe("thinking…");
  });

  it("a running turn on a tool names the tool", () => {
    const s = rosterStatus(row({ status: "queued", current_action: "query_range" }));
    expect(s.work).toBe("tool");
    expect(s.typing).toBe("running query_range…");
    expect(s.label).toBe("running query_range…");
    expect(rosterStatus(row({ status: "running", current_action: "read_file" })).typing).toBe("reading file…");
    expect(rosterStatus(row({ status: "running", current_action: "mcp__wick__wick_search" })).typing).toBe("searching…");
  });

  it("a remote agent waits for the other side, never types or thinks", () => {
    const s = rosterStatus(row({ status: "running", kind: "a2a-remote", handle: "halodev", current_action: "Bash" }));
    expect(s.work).toBe("waiting");
    expect(s.typing).toBe("Waiting for @halodev's reply…");
  });

  it("current_action is ignored when the turn is not running", () => {
    expect(rosterStatus(row({ current_action: "Bash" })).typing).toBeNull();
  });

  it("attention beats typing and unread", () => {
    const s = rosterStatus(row({ status: "running", needs_attention: true, unread: true }));
    expect(s.label).toBe("needs your attention");
    expect(s.attention).toBe(true);
    expect(s.typing).toBe("thinking…");
  });

  it("hatching beats everything but disabled", () => {
    expect(rosterStatus(row({ unread: true }), { hatching: true }).label).toBe("just hatched");
  });

  it("a disabled agent shows no live cue", () => {
    const s = rosterStatus(row({ disabled: true, status: "running", unread: true, needs_attention: true }), { hatching: true });
    expect(s).toEqual({ unread: false, attention: false, work: "idle", typing: null, presence: "disabled", label: "disabled" });
  });

  it("the status dot: amber needs you, spinner working, grey disabled, green otherwise", () => {
    expect(rosterStatus(row()).presence).toBe("online");
    expect(rosterStatus(row({ unread: true })).presence).toBe("online");
    expect(rosterStatus(row({ status: "running" })).presence).toBe("working");
    expect(rosterStatus(row({ status: "running", kind: "slack-remote", handle: "h" })).presence).toBe("working");
    expect(rosterStatus(row({ subagents_working: ["x"] })).presence).toBe("working");
    expect(rosterStatus(row({ status: "running", needs_attention: true })).presence).toBe("attention");
    expect(rosterStatus(row({ disabled: true, needs_attention: true, status: "running" })).presence).toBe("disabled");
  });

  it("rows from an older server (no P13 fields) read as idle", () => {
    expect(rosterStatus({ id: "x", status: "idle", disabled: false }).label).toBe("online, idle");
  });
});

describe("withTurn", () => {
  const list = [
    { id: "a1", status: "running", current_action: "query_range" },
    { id: "a2", status: "running", current_action: "search" },
  ];

  it("a finished turn reads idle at once and drops its tool", () => {
    const out = withTurn(list, "a1", false);
    expect(out[0]).toEqual({ id: "a1", status: "idle", current_action: "" });
    expect(out[1]).toBe(list[1]);
    expect(rosterStatus({ ...out[0], disabled: false }).typing).toBeNull();
  });

  it("a started turn reads running and keeps the tool it knows", () => {
    const out = withTurn([{ id: "a1", status: "idle", current_action: "x" }], "a1", true);
    expect(out[0]).toEqual({ id: "a1", status: "running", current_action: "x" });
  });
});

describe("withActivity (the /stream/sessions activity event)", () => {
  const list: { id: string; status: string; disabled: boolean; main_session_id: string; current_action: string; needs_attention?: boolean; tool_error?: boolean }[] = [
    { id: "a1", status: "idle", disabled: false, main_session_id: "s1", current_action: "" },
    { id: "a2", status: "idle", disabled: false, main_session_id: "s2", current_action: "" },
  ];
  const ev = (p: Partial<SessionActivity>): SessionActivity => ({ session_id: "s1", work: "", action: "", ...p });

  it("thinking → tool → thinking → idle, from stream events alone", () => {
    let r = withActivity(list, ev({ work: "thinking" }));
    expect(rosterStatus(r.agents[0]).work).toBe("thinking");
    expect(r.agents[1]).toBe(list[1]);
    r = withActivity(r.agents, ev({ work: "tool", action: "Bash" }));
    expect(rosterStatus(r.agents[0]).typing).toBe("running Bash…");
    r = withActivity(r.agents, ev({ work: "thinking", tool_error: true }));
    expect(rosterStatus(r.agents[0]).work).toBe("thinking");
    expect(r.agents[0].tool_error).toBe(true);
    expect(r.finished).toBe(false);
    r = withActivity(r.agents, ev({}));
    expect(rosterStatus(r.agents[0]).work).toBe("idle");
    expect(r.agents[0].tool_error).toBe(false);
    expect(r.finished).toBe(true);
  });

  it("a session that is no agent's main chat, or no change, returns the same list", () => {
    expect(withActivity(list, ev({ session_id: "other", work: "tool", action: "Bash" })).agents).toBe(list);
    expect(withActivity(list, ev({ session_id: "" , work: "thinking" })).agents).toBe(list);
    const running = withActivity(list, ev({ work: "thinking" })).agents;
    expect(withActivity(running, ev({ work: "thinking" })).agents).toBe(running);
  });

  it("needs_attention follows the event; a queued row starts running", () => {
    const r = withActivity([{ ...list[0], status: "queued" }], ev({ work: "thinking", needs_attention: true }));
    expect(r.agents[0].status).toBe("running");
    expect(rosterStatus(r.agents[0]).attention).toBe(true);
    expect(withTurn([{ id: "a1", status: "running", tool_error: true }], "a1", false)[0].tool_error).toBe(false);
  });
});

/* The roster no longer polls its live state: the timers AgentsApp keeps are
   the 30s full refresh and a local 1s clock (no request), and the 2s live
   read is gone with its endpoint. */
describe("no quick poll", () => {
  it("AgentsApp has no 2s interval and no /agents/live read", () => {
    const src = readFileSync(resolve(__dirname, "../../AgentsApp.svelte"), "utf8");
    const intervals = [...src.matchAll(/setInterval\([\s\S]*?,\s*(\w+)\s*\)/g)].map((m) => m[1]);
    // The roster refreshes on agent_changed over the sessions stream (no 30s
    // poll since d979f8fc); the timers left are clocks that fetch nothing:
    // the 1s wait clock and the roster's minute clock (stamps, dozing).
    expect(intervals.filter((ms) => ms !== "1000" && ms !== "60_000")).toEqual([]);
    expect(src).toMatch(/setInterval\(\(\) => \(waitNow = Date\.now\(\)\), 1000\)/);
    expect(src).toMatch(/setInterval\(\(\) => \(rosterNow = Date\.now\(\)\), 60_000\)/);
    expect(src).toMatch(/rosterTime\(a\.last_active, rosterNow\)/);
    expect(src).toMatch(/dozing=\{dozing\(a, st, rosterNow\)\}/);
    expect(src).not.toMatch(/agents\/live|listAgentsLive|createLivePoll/);
    // The stream itself lives in rosterLive.ts since d979f8fc.
    expect(src).toMatch(/liveRoster\(/);
  });
});

describe("sub-agents working after the agent's own turn", () => {
  it("one background sub-agent: working, named, with the 🤖 cue", () => {
    const s = rosterStatus(row({ subagents_working: ["wick-fixer"] }));
    expect(s.work).toBe("subagent");
    expect(s.typing).toBe("🤖 wick-fixer bekerja…");
    expect(s.label).toBe("🤖 wick-fixer bekerja…");
  });

  it("more than one is counted", () => {
    expect(rosterStatus(row({ subagents_working: ["a", "b"] })).typing).toBe("🤖 2 sub-agent bekerja…");
  });

  it("the agent's own turn outranks its sub-agents", () => {
    const thinking = rosterStatus(row({ status: "running", subagents_working: ["wick-fixer"] }));
    expect(thinking.work).toBe("thinking");
    const tool = rosterStatus(row({ status: "running", current_action: "Bash", subagents_working: ["wick-fixer"] }));
    expect(tool.work).toBe("tool");
  });

  it("attention and hatching beat it, it beats unread, disabled shows nothing", () => {
    expect(rosterStatus(row({ needs_attention: true, subagents_working: ["x"] })).label).toBe("needs your attention");
    expect(rosterStatus(row({ subagents_working: ["x"] }), { hatching: true }).label).toBe("just hatched");
    const unread = rosterStatus(row({ unread: true, subagents_working: ["x"] }));
    expect(unread.label).toBe("🤖 x bekerja…");
    expect(unread.unread).toBe(true);
    const off = rosterStatus(row({ disabled: true, subagents_working: ["x"] }));
    expect(off.work).toBe("idle");
    expect(off.typing).toBeNull();
  });

  it("an empty list reads idle", () => {
    expect(rosterStatus(row({ subagents_working: [] })).work).toBe("idle");
  });

  it("a turn ending on the stream keeps the sub-agents, so the card stays working", () => {
    const agents = [{ id: "a1", status: "running", disabled: false, main_session_id: "s1", current_action: "", subagents_working: ["wick-fixer"] }];
    const ev: SessionActivity = { session_id: "s1", work: "", action: "" };
    const next = withActivity(agents, ev);
    expect(next.finished).toBe(true);
    expect(next.agents[0].subagents_working).toEqual(["wick-fixer"]);
    expect(rosterStatus(next.agents[0]).work).toBe("subagent");
    const ended = withTurn(agents, "a1", false);
    expect(ended[0].subagents_working).toEqual(["wick-fixer"]);
    expect(rosterStatus(ended[0]).work).toBe("subagent");
  });
});

/* P44: the roster row says presence with a dot (label read out, no hover
   tip), a remote with its source icon, and never "shared" — the header
   tells that once. The old testids are gone on purpose. */
describe("roster row and header markup", () => {
  const src = readFileSync(resolve(__dirname, "../../AgentsApp.svelte"), "utf8");
  it("no hover tip and no Shared / remote pills", () => {
    expect(src).not.toMatch(/roster-tip|roster-shared-badge|roster-remote-badge/);
    expect(src).not.toMatch(/st\.tip|\.tip\}/);
  });
  it("the dot and the source icon sit on the row and the header", () => {
    expect(src.match(/<PresenceDot /g)?.length).toBe(2);
    expect(src.match(/<RemoteKindIcon /g)?.length).toBe(2);
    expect(src).toMatch(/<AgentShareLine agent=\{selected\} onOpenSharing=\{\(\) => openPanel\(\{ kind: "settings", tab: "sharing" \}\)\}/);
  });
  it("line 2 never says shared by, and the header drops the online text", () => {
    const preview = src.slice(src.indexOf("function rowPreview"), src.indexOf("const mutedBell"));
    expect(preview).not.toMatch(/sharedLabel/);
    expect(src).not.toMatch(/<\/span>online\b/);
  });
});

describe("Team tasks (team_message / @mention)", () => {
  it("the sender waits on its teammates: working, one handle or how many", () => {
    const one = rosterStatus(row({ team_waiting: ["anton"] }));
    expect(one.work).toBe("team");
    expect(one.presence).toBe("working");
    expect(one.typing).toBe("waiting on @anton…");
    expect(rosterStatus(row({ team_waiting: ["a", "b", "c"] })).typing).toBe("waiting on 3 teammates…");
  });

  it("outranks its own turn and its background sub-agents, which keep their own label", () => {
    expect(rosterStatus(row({ status: "running", current_action: "team_message", team_waiting: ["a"] })).work).toBe("team");
    expect(rosterStatus(row({ team_waiting: ["a"], subagents_working: ["x"] })).typing).toBe("waiting on @a…");
    expect(rosterStatus(row({ subagents_working: ["x"] })).typing).toBe("🤖 x bekerja…");
  });

  it("the teammate working on it replies to its sender, ahead of everything else it does", () => {
    const s = rosterStatus(row({ replying_to: "Captain" }));
    expect(s.work).toBe("replying");
    expect(s.presence).toBe("working");
    expect(s.typing).toBe("replying to Captain…");
    expect(s.label).toBe("replying to Captain…");
    expect(rosterStatus(row({ status: "running", current_action: "Bash", replying_to: "Captain", team_waiting: ["a"] })).work).toBe("replying");
  });

  it("a teammate asking its sender needs attention", () => {
    const s = rosterStatus(row({ team_asks: { to: "Captain", question: "which deploy?" } }));
    expect(s.attention).toBe(true);
    expect(s.presence).toBe("attention");
  });

  it("disabled shows none of it", () => {
    const off = rosterStatus(row({ disabled: true, team_waiting: ["a"], replying_to: "Captain", team_asks: { to: "Captain", question: "q" } }));
    expect(off.work).toBe("idle");
    expect(off.attention).toBe(false);
    expect(off.typing).toBeNull();
  });
});

describe("Team task work in the avatar and the header", () => {
  it("busyWork covers sub-agents, Team waits and replies, not the agent's own turn", () => {
    expect(["subagent", "team", "replying"].every((w) => busyWork(w as WorkState))).toBe(true);
    expect(["idle", "thinking", "tool", "waiting"].some((w) => busyWork(w as WorkState))).toBe(false);
  });

  it("avatarWorking: its own turn and busyWork animate the avatar, a Team task it replies to does not", () => {
    expect(avatarWorking("running", "thinking")).toBe(true);
    expect(avatarWorking("idle", "team")).toBe(true);
    expect(avatarWorking("idle", "subagent")).toBe(true);
    expect(avatarWorking("idle", "replying")).toBe(false);
    expect(avatarWorking("running", "replying")).toBe(false);
    expect(avatarWorking("idle", "idle")).toBe(false);
  });

  it("the row avatar, the header avatar and header line 2 all read busyWork", () => {
    const app = readFileSync(resolve(__dirname, "../../AgentsApp.svelte"), "utf8");
    const avatar = readFileSync(resolve(__dirname, "../components/team/RosterAvatar.svelte"), "utf8");
    expect(avatar).toMatch(/working=\{!ring && avatarWorking\(a\.status, st\.work\)\} tool=\{st\.work === "tool" \|\| st\.work === "subagent"\}/);
    expect(app).toMatch(/working=\{!!headerStatus && avatarWorking\(selected\.status, headerStatus\.work\)\} tool=\{headerStatus\?\.work === "tool" \|\| headerStatus\?\.work === "subagent"\}/);
    expect(app).toMatch(/\{:else if isWorking\(selected\.status\) \|\| \(headerStatus && busyWork\(headerStatus\.work\)\)\}/);
  });

  it("the row's working line ends in the bouncing dots, and its avatar takes the Team-task mark", () => {
    const app = readFileSync(resolve(__dirname, "../../AgentsApp.svelte"), "utf8");
    expect(app).toMatch(/data-testid="roster-working" data-work=\{st\.work\}>\{st\.typing\.replace\(\/…\$\/, ""\)\}<span class="dots"/);
    expect(app).toMatch(/<PresenceDot [^>]*mark=\{teamMark\(a, st\)\} \/>/);
    expect(app).toMatch(/if \(a\.team_asks && st\.attention\) return "asks";/);
    expect(app).toMatch(/return st\.work === "replying" \? "task" : null;/);
  });

  it("a teammate asking its sender gets the amber count badge on its row", () => {
    const app = readFileSync(resolve(__dirname, "../../AgentsApp.svelte"), "utf8");
    expect(app).toMatch(/\{#if a\.team_asks && !a\.disabled\}<span class="roster-badge[^"]*bg-amber-500[^"]*"[^>]*data-testid="roster-asks">1<\/span>\{\/if\}/);
  });
});
