import { describe, it, test, expect } from "vitest";
import { parseAgentsRoute, formatAgentsRoute, isReservedHandle, type AgentsRoute } from "../agentsRouter.js";
import { hiddenTabsFor } from "../agentMode.js";

const B = "/tools/agents";

describe("parseAgentsRoute", () => {
  test("roster root", () => {
    expect(parseAgentsRoute("/tools/agents/team", "", B)).toEqual({ handle: null, session: null, panel: null });
    expect(parseAgentsRoute("/tools/agents/team/", "", B).handle).toBeNull();
  });

  test("agent handle", () => {
    expect(parseAgentsRoute("/tools/agents/team/log-hunter", "", B).handle).toBe("log-hunter");
  });

  test("extra path segments are ignored, not mistaken for the handle", () => {
    expect(parseAgentsRoute("/tools/agents/team/support/x/y", "", B).handle).toBe("support");
  });

  test("settings panel with tab; unknown tab falls back to persona", () => {
    expect(parseAgentsRoute("/tools/agents/team/a1", "?panel=settings&tab=access", B).panel).toEqual({
      kind: "settings",
      tab: "access",
    });
    expect(parseAgentsRoute("/tools/agents/team/a1", "?panel=settings&tab=nope", B).panel).toEqual({
      kind: "settings",
      tab: "persona",
    });
  });

  test("every Settings tab parses; the old features tab opens Tools & fitur", () => {
    for (const tab of ["persona", "access", "tools", "skills", "mention", "captain", "session", "avatar", "advanced"]) {
      expect(parseAgentsRoute("/tools/agents/team/a1", `?panel=settings&tab=${tab}`, B).panel).toEqual({
        kind: "settings",
        tab,
      });
    }
    expect(parseAgentsRoute("/tools/agents/team/a1", "?panel=settings&tab=features", B).panel).toEqual({
      kind: "settings",
      tab: "tools",
    });
    expect(parseAgentsRoute("/tools/agents/team/a1", "?panel=settings", B).panel).toEqual({
      kind: "settings",
      tab: "persona",
    });
  });

  test("new + sessions panels; unknown panel is none", () => {
    expect(parseAgentsRoute("/tools/agents/team", "?panel=new", B).panel).toEqual({ kind: "new" });
    expect(parseAgentsRoute("/tools/agents/team/a1", "?panel=sessions", B).panel).toEqual({ kind: "sessions" });
    expect(parseAgentsRoute("/tools/agents/team/a1", "?panel=scheduled", B).panel).toEqual({ kind: "scheduled" });
    expect(parseAgentsRoute("/tools/agents/team/a1", "?panel=bogus", B).panel).toBeNull();
    expect(parseAgentsRoute("/tools/agents/team/a1", "?panel=connections&conn=a2a", B).panel).toEqual({ kind: "connections", conn: "a2a" });
    expect(parseAgentsRoute("/tools/agents/team/a1", "?panel=connections&conn=fax", B).panel).toEqual({ kind: "connections" });
  });

  test("session only counts under a handle", () => {
    expect(parseAgentsRoute("/tools/agents/team/a1", "?session=s9", B).session).toBe("s9");
    expect(parseAgentsRoute("/tools/agents/team", "?session=s9", B).session).toBeNull();
  });

  test("team settings panel, with or without an agent; unknown tab is general", () => {
    expect(parseAgentsRoute("/tools/agents/team", "?panel=team-settings", B).panel).toEqual({ kind: "team-settings", tab: "general" });
    expect(parseAgentsRoute("/tools/agents/team/a1", "?panel=team-settings&tab=nope", B)).toEqual({
      handle: "a1",
      session: null,
      panel: { kind: "team-settings", tab: "general" },
    });
  });

  test("a path outside /team is the roster root", () => {
    expect(parseAgentsRoute("/tools/agents/sessions/x", "", B)).toEqual({ handle: null, session: null, panel: null });
  });
});

describe("formatAgentsRoute", () => {
  const cases: [AgentsRoute, string][] = [
    [{ handle: null, session: null, panel: null }, "/tools/agents/team"],
    [{ handle: "captain", session: null, panel: null }, "/tools/agents/team/captain"],
    [{ handle: null, session: null, panel: { kind: "new" } }, "/tools/agents/team?panel=new"],
    [
      { handle: "a1", session: null, panel: { kind: "settings", tab: "tools" } },
      "/tools/agents/team/a1?panel=settings&tab=tools",
    ],
    [
      { handle: "a1", session: null, panel: { kind: "settings", tab: "advanced" } },
      "/tools/agents/team/a1?panel=settings&tab=advanced",
    ],
    [{ handle: "a1", session: "s2", panel: { kind: "sessions" } }, "/tools/agents/team/a1?session=s2&panel=sessions"],
    [{ handle: "a1", session: null, panel: { kind: "scheduled" } }, "/tools/agents/team/a1?panel=scheduled"],
    [{ handle: "a1", session: null, panel: { kind: "connections", conn: "telegram" } }, "/tools/agents/team/a1?panel=connections&conn=telegram"],
    [
      { handle: "captain", session: null, panel: { kind: "team-settings", tab: "general" } },
      "/tools/agents/team/captain?panel=team-settings&tab=general",
    ],
  ];
  test.each(cases)("%j → %s", (r, url) => {
    expect(formatAgentsRoute(r, B)).toBe(url);
  });

  test("round-trips", () => {
    for (const [r] of cases) {
      const url = new URL("http://x" + formatAgentsRoute(r, B));
      expect(parseAgentsRoute(url.pathname, url.search, B)).toEqual(r);
    }
  });
});

describe("hiddenTabsFor", () => {
  test("maps off features to their rail tabs", () => {
    expect(hiddenTabsFor({ schedule: false, tickets: false, source: true })).toEqual(["scheduled", "ticket"]);
  });
  test("no features hides nothing", () => {
    expect(hiddenTabsFor(null)).toEqual([]);
  });
});

import { HANDLE_RE, slugHandle, splitPick, joinPick, destructiveAllowed } from "../agentForm.js";

describe("agentForm", () => {
  test("slugHandle produces a valid handle", () => {
    expect(slugHandle("Log Hunter!")).toBe("log-hunter");
    expect(HANDLE_RE.test(slugHandle("Support Bot 2"))).toBe(true);
    expect(HANDLE_RE.test("-bad")).toBe(false);
  });
  test("pick split/join round-trip", () => {
    expect(splitPick("claude/claude::opus")).toEqual({ provider: "claude/claude", model: "opus" });
    expect(splitPick("codex/codex")).toEqual({ provider: "codex/codex", model: "" });
    expect(joinPick("claude/claude", "opus")).toBe("claude/claude::opus");
    expect(joinPick("", "opus")).toBe("");
  });
  test("destructiveAllowed honours level", () => {
    const cat = [
      {
        id: "c1", key: "slack", label: "Slack", description: "", accounts: [],
        ops: [
          { key: "read_thread", name: "Read", destructive: false },
          { key: "delete_message", name: "Delete", destructive: true },
        ],
      },
    ];
    const g = (level: "all" | "read" | "pick", ops: string[] = []) => [{ connector_id: "c1", accounts: [], level, ops }];
    expect(destructiveAllowed(g("all"), cat)).toEqual(["Slack · Delete"]);
    expect(destructiveAllowed(g("read"), cat)).toEqual([]);
    expect(destructiveAllowed(g("pick", ["read_thread"]), cat)).toEqual([]);
    expect(destructiveAllowed(g("pick", ["delete_message"]), cat)).toEqual(["Slack · Delete"]);
  });
});

describe("convert route", () => {
  it("round-trips the wizard's project to convert", () => {
    const r = parseAgentsRoute("/tools/agents/team", "?panel=new&project=p%201", "/tools/agents");
    expect(r.panel).toEqual({ kind: "new", project: "p 1" });
    expect(formatAgentsRoute(r, "/tools/agents")).toBe("/tools/agents/team?panel=new&project=p+1");
    expect(parseAgentsRoute("/tools/agents/team", "?panel=new", "/tools/agents").panel).toEqual({ kind: "new" });
  });
});

describe("group routes", () => {
  test("/team/group/<id> opens a group, never an agent", () => {
    const r = parseAgentsRoute("/tools/agents/team/group/abc-123", "", B);
    expect(r).toEqual({ handle: null, session: null, panel: null, group: "abc-123" });
  });
  test("group settings panel only on a group", () => {
    expect(parseAgentsRoute("/tools/agents/team/group/abc", "?panel=group-settings", B).panel).toEqual({ kind: "group-settings" });
    expect(parseAgentsRoute("/tools/agents/team/anton", "?panel=group-settings", B).panel).toBeNull();
  });
  test("round trip", () => {
    const r: AgentsRoute = { handle: null, session: null, panel: { kind: "group-settings" }, group: "abc" };
    const url = formatAgentsRoute(r, B);
    expect(url).toBe("/tools/agents/team/group/abc?panel=group-settings");
    const [path, qs] = url.split("?");
    expect(parseAgentsRoute(path, "?" + qs, B)).toEqual(r);
  });
  test("a bare /team/group is not a group", () => {
    expect(parseAgentsRoute("/tools/agents/team/group", "", B).group).toBeUndefined();
  });
  test("the old /team/g/<id> is gone: no group, no alias", () => {
    const r = parseAgentsRoute("/tools/agents/team/g/abc", "", B);
    expect(r.group).toBeUndefined();
    expect(r.handle).toBe("g");
  });
});

describe("tickets routes", () => {
  test("/team/tickets is the board; no agent, no group", () => {
    expect(parseAgentsRoute("/tools/agents/team/tickets", "", B)).toEqual({ handle: null, session: null, panel: null, tickets: {} });
    expect(parseAgentsRoute("/tools/agents/team/tickets/", "", B).tickets).toEqual({});
  });
  test("/team/tickets/<project>/<ticket> names one ticket; half of one is the board", () => {
    expect(parseAgentsRoute("/tools/agents/team/tickets/p%201/T-12", "", B).tickets).toEqual({ project: "p 1", ticket: "T-12" });
    expect(parseAgentsRoute("/tools/agents/team/tickets/p1", "", B).tickets).toEqual({});
    expect(parseAgentsRoute("/tools/agents/team/tickets/%E0%A4/T-1", "", B).tickets).toEqual({});
  });
  test("/team/t is not Tickets", () => {
    expect(parseAgentsRoute("/tools/agents/team/t", "", B).tickets).toBeUndefined();
    expect(parseAgentsRoute("/tools/agents/team/t/p1/T-1", "", B).tickets).toBeUndefined();
  });
  test("tickets is a reserved handle", () => {
    expect(isReservedHandle("tickets")).toBe(true);
    expect(isReservedHandle("ticket")).toBe(false);
    expect(isReservedHandle("group")).toBe(true);
    expect(isReservedHandle("groups")).toBe(false);
  });
  test("a two-letter handle is still an agent", () => {
    const r = parseAgentsRoute("/tools/agents/team/tt", "", B);
    expect(r.handle).toBe("tt");
    expect(r.tickets).toBeUndefined();
  });
  test("the Team settings Tickets tab opens over the page; session is ignored", () => {
    const r = parseAgentsRoute("/tools/agents/team/tickets", "?panel=team-settings&tab=tickets&session=s1", B);
    expect(r).toEqual({ handle: null, session: null, panel: { kind: "team-settings", tab: "tickets" }, tickets: {} });
  });
  test("format is the inverse", () => {
    const cases: AgentsRoute[] = [
      { handle: null, session: null, panel: null, tickets: {} },
      { handle: null, session: null, panel: null, tickets: { project: "p 1", ticket: "T-12" } },
      { handle: null, session: null, panel: { kind: "team-settings", tab: "tickets" }, tickets: {} },
    ];
    for (const r of cases) {
      const url = formatAgentsRoute(r, B);
      const [path, qs = ""] = url.split("?");
      expect(parseAgentsRoute(path, qs ? "?" + qs : "", B)).toEqual(r);
    }
    expect(formatAgentsRoute({ handle: null, session: null, panel: null, tickets: { project: "p 1", ticket: "T-12" } }, B)).toBe("/tools/agents/team/tickets/p%201/T-12");
    expect(formatAgentsRoute({ handle: null, session: null, panel: { kind: "team-settings", tab: "tickets" }, tickets: {} }, B)).toBe(
      "/tools/agents/team/tickets?panel=team-settings&tab=tickets",
    );
  });
});
