import { describe, it, expect } from "vitest";
import { FEATURE_TABS, composerPlaceholder, connectorCaption, hiddenTabNote, hiddenTabsFor, railShownNote } from "../agentMode.js";

describe("hiddenTabNote", () => {
  it("is empty when nothing is hidden", () => {
    expect(hiddenTabNote([])).toBe("");
    expect(hiddenTabNote(undefined)).toBe("");
  });
  it("counts each hidden tab once", () => {
    expect(hiddenTabNote(["source", "browser", "source"])).toBe("2 tab disembunyikan — fiturnya tidak diizinkan");
  });
});

describe("composerPlaceholder", () => {
  it("addresses the agent by name", () => {
    expect(composerPlaceholder("Ops Bot")).toBe("Message Ops Bot…");
  });
  it("falls back when the name is missing", () => {
    expect(composerPlaceholder("  ")).toBe("Message agent…");
    expect(composerPlaceholder(null)).toBe("Message agent…");
  });
});

describe("connectorCaption", () => {
  it("counts connectors, not grants per account", () => {
    expect(connectorCaption([{ connector_id: "a" }, { connector_id: "b" }, { connector_id: "a" }])).toBe("2 connector");
  });
  it("reads zero for no grants", () => {
    expect(connectorCaption(null)).toBe("0 connector");
  });
});

describe("feature flags", () => {
  it("never gates Todos or Workspace, even with an old flag set to false", () => {
    expect(hiddenTabsFor({ todos: false, workspace: false } as never)).toEqual([]);
    expect(FEATURE_TABS.map((f) => f.tab)).not.toContain("todos");
    expect(FEATURE_TABS.map((f) => f.tab)).not.toContain("workspace");
  });

  it("notes that Browser needs Playwright", () => {
    expect(FEATURE_TABS.find((f) => f.feature === "browser")?.hint).toMatch(/Playwright/);
  });

  it("lists the rail tabs the flags leave", () => {
    expect(railShownNote({ source: false, schedule: true, files: false, process: false, browser: false, subagents: false, notes: false, tickets: false })).toBe(
      "rail tampil: Routines, Workspace, Todos",
    );
    expect(railShownNote(null)).toBe(
      "rail tampil: Source, Routines, Files, Process, Browser, Sub-agents, Notes, Ticket, Workspace, Todos",
    );
  });
});
