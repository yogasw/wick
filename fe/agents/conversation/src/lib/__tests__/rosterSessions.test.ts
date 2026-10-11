import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import {
  elapsedLabel,
  lastSession,
  lastSessionKey,
  loadSeen,
  markSeen,
  rowTarget,
  runningSessions,
  SEEN_KEY,
  SEEN_MAX,
  sessionSignal,
  sessionTitle,
  triggerLabel,
} from "../rosterSessions.js";

const ws = (id: string, trigger = "you", since = "2026-10-11T10:00:00Z") => ({ id, label: `chat ${id}`, trigger, since });

describe("rowTarget", () => {
  it("opens the oldest running chat, else the main chat", () => {
    expect(rowTarget({ disabled: false, working_sessions: [ws("old"), ws("new")] })).toBe("old");
    expect(rowTarget({ disabled: false })).toBeNull();
    expect(rowTarget({ disabled: true, working_sessions: [ws("old")] })).toBeNull();
    expect(runningSessions({ disabled: true, working_sessions: [ws("a")] })).toEqual([]);
  });
});

describe("labels", () => {
  it("names chats and who they work for", () => {
    expect(sessionTitle({ label: "  " })).toBe("Untitled chat");
    expect(sessionTitle({ label: "Debug 401" })).toBe("Debug 401");
    expect(triggerLabel({ trigger: "you" })).toBe("You");
    expect(triggerLabel({ trigger: "Captain" })).toBe("Captain");
    expect(triggerLabel({ trigger: "", source: "A2A" })).toBe("A2A");
    expect(triggerLabel({ trigger: "" })).toBe("");
  });

  it("elapsedLabel counts minutes and hours", () => {
    const at = Date.parse("2026-10-11T10:00:00Z");
    expect(elapsedLabel("2026-10-11T10:00:00Z", at + 20_000)).toBe("<1m");
    expect(elapsedLabel("2026-10-11T10:00:00Z", at + 4 * 60_000)).toBe("4m");
    expect(elapsedLabel("2026-10-11T10:00:00Z", at + 65 * 60_000)).toBe("1h 5m");
    expect(elapsedLabel(undefined, at)).toBe("");
  });
});

describe("lastSession", () => {
  const done = ws("done", "Captain");
  it("shows the finished chat while nothing else runs, until it is opened", () => {
    expect(lastSession({ disabled: false, last_session: done }, new Set(), null)).toEqual(done);
    expect(lastSession({ disabled: false, last_session: done, working_sessions: [ws("x")] }, new Set(), null)).toBeNull();
    expect(lastSession({ disabled: false, last_session: done }, new Set([lastSessionKey(done)]), null)).toBeNull();
    expect(lastSession({ disabled: false, last_session: done }, new Set(), "done")).toBeNull();
    expect(lastSession({ disabled: true, last_session: done }, new Set(), null)).toBeNull();
    // The same chat finishing again later shows again.
    expect(lastSession({ disabled: false, last_session: { ...done, since: "2026-10-11T11:00:00Z" } }, new Set([lastSessionKey(done)]), null)).not.toBeNull();
  });

  it("seen keys persist, newest SEEN_MAX kept", () => {
    const mem = new Map<string, string>();
    const store = { getItem: (k: string) => mem.get(k) ?? null, setItem: (k: string, v: string) => void mem.set(k, v) };
    let seen = loadSeen(store);
    for (let i = 0; i < SEEN_MAX + 5; i++) seen = markSeen(seen, `k${i}`, store);
    expect(seen.size).toBe(SEEN_MAX);
    expect(seen.has("k0")).toBe(false);
    expect(loadSeen(store).has(`k${SEEN_MAX + 4}`)).toBe(true);
    mem.set(SEEN_KEY, "not json");
    expect(loadSeen(store).size).toBe(0);
  });
});

describe("sessionSignal", () => {
  const ev = (session_id: string, work: "" | "thinking", agent_id?: string) => ({ session_id, work, action: "", agent_id });
  const roster = (working: ReturnType<typeof ws>[] = []) => [{ id: "a1", main_session_id: "main", working_sessions: working }];

  it("every turn start and end of a roster agent's non-main chat reads the roster, steps inside a turn do not", () => {
    const active = new Set<string>();
    expect(sessionSignal(roster(), ev("new", "thinking", "a1"), active)).toBe(true);
    expect(sessionSignal(roster(), ev("new", "thinking", "a1"), active)).toBe(false);
    // The roster read came back before the turn showed: its end still reads again.
    expect(sessionSignal(roster(), ev("new", "", "a1"), active)).toBe(true);
    expect(sessionSignal(roster(), ev("new", "", "a1"), active)).toBe(false);
    // Its next turn starts afresh.
    expect(sessionSignal(roster(), ev("new", "thinking", "a1"), active)).toBe(true);
  });

  it("conversations the server names no roster agent for never read the roster", () => {
    const active = new Set<string>();
    // A channel thread, another person's chat, a main chat: no agent_id.
    expect(sessionSignal(roster(), ev("slack-thread", "thinking"), active)).toBe(false);
    expect(sessionSignal(roster(), ev("slack-thread", ""), active)).toBe(false);
    // An agent not on this roster.
    expect(sessionSignal(roster(), ev("x", "thinking", "other"), active)).toBe(false);
    // The agent's main chat is withActivity's.
    expect(sessionSignal(roster(), ev("main", "thinking", "a1"), active)).toBe(false);
    expect(active.size).toBe(0);
  });

  it("a chat already on a row as working that ends reads the roster (page loaded mid-turn)", () => {
    const active = new Set<string>();
    expect(sessionSignal(roster([ws("run")]), ev("run", "", "a1"), active)).toBe(true);
    expect(sessionSignal(roster(), ev("idle-other", "", "a1"), active)).toBe(false);
  });
});

describe("roster wiring", () => {
  it("the row click opens rowTarget; the count menu and Last line sit beside the row button", () => {
    const app = readFileSync(resolve(__dirname, "../../AgentsApp.svelte"), "utf8");
    expect(app).toMatch(/openRowChat\(a, rowTarget\(a\)\);/);
    expect(app).toMatch(/\{#if running\.length > 1\}<RosterSessionsMenu sessions=\{running\}/);
    expect(app).toMatch(/data-testid="roster-last"/);
    expect(app).toMatch(/else if \(sessionSignal\(agents, ev, activeSessions\)\) sessionReload\.call\(\);/);
    expect(app).toMatch(/boundedDebounce\(\(\) => load\(\), ROSTER_REFETCH_DEBOUNCE_MS, ROSTER_REFETCH_MAX_WAIT_MS\)/);
    expect(app).toMatch(/onReconnect: \(\) => activeSessions\.clear\(\),/);
    expect(app).toMatch(/const load = singleFlight\(async \(\) => \{/);
  });
});
