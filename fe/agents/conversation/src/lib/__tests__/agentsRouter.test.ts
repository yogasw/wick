import { describe, it, test, expect } from "vitest";
import { parseAgentsRoute, formatAgentsRoute, type AgentsRoute } from "../agentsRouter.js";
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
    for (const tab of ["persona", "access", "tools", "avatar", "advanced"]) {
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
    expect(parseAgentsRoute("/tools/agents/team/a1", "?panel=bogus", B).panel).toBeNull();
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
