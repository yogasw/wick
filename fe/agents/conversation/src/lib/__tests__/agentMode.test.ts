import { describe, it, expect } from "vitest";
import { FEATURE_TABS, composerPlaceholder, providerSwitchWarns, providerLabel, providerSwitchBody, connectorCaption, hiddenTabNote, hiddenTabsFor } from "../agentMode.js";

describe("hiddenTabNote", () => {
  it("is empty when nothing is hidden", () => {
    expect(hiddenTabNote([])).toBe("");
    expect(hiddenTabNote(undefined)).toBe("");
  });
  it("counts each hidden tab once", () => {
    expect(hiddenTabNote(["source", "browser", "source"])).toBe("2 tabs hidden — feature not allowed");
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
    expect(connectorCaption([{ connector_id: "a" }, { connector_id: "b" }, { connector_id: "a" }])).toBe("2 connectors");
  });
  it("reads zero for no grants", () => {
    expect(connectorCaption(null)).toBe("0 connectors");
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

  it("shows Files and Process from the native tools, not from flags", () => {
    expect(hiddenTabsFor({ files: false, process: false } as never, ["Read", "Bash"])).toEqual([]);
    expect(hiddenTabsFor({}, ["Grep", "WebFetch"])).toEqual(["files", "process"]);
    expect(hiddenTabsFor(null, ["Write"])).toEqual(["process"]);
    expect(hiddenTabsFor(null, null)).toEqual([]);
    expect(FEATURE_TABS.find((f) => f.feature === "schedule")?.label).toBe("Scheduled");
  });
});

describe("providerSwitchWarns", () => {
  it("anything goes before the first message", () => {
    expect(providerSwitchWarns(false, "claude/claude", "codex/codex")).toBe(false);
  });
  it("another provider asks first once the chat started", () => {
    expect(providerSwitchWarns(true, "claude/claude", "codex/codex::gpt-5")).toBe(true);
    expect(providerSwitchWarns(true, "claude", "codex/codex")).toBe(true);
    expect(providerSwitchWarns(true, "codex/codex", "codex/gemini_flash")).toBe(true);
  });
  it("another model of the same provider is silent", () => {
    expect(providerSwitchWarns(true, "claude/claude", "claude/claude::opus")).toBe(false);
    expect(providerSwitchWarns(true, "claude", "claude/claude::sonnet")).toBe(false);
  });
  it("a started chat on the wick default asks too", () => {
    expect(providerSwitchWarns(true, "", "codex/codex")).toBe(true);
  });
});

describe("provider switch warning text", () => {
  it("names providers as the picker does", () => {
    expect(providerLabel("claude/claude::opus")).toBe("claude");
    expect(providerLabel("codex")).toBe("codex");
    expect(providerLabel("codex/gemini_flash")).toBe("codex · gemini_flash");
    expect(providerLabel("")).toBe("wick default");
  });
  it("says the new provider may not resume", () => {
    expect(providerSwitchBody("claude/claude", "codex/codex::gpt-5")).toBe(
      "This chat started on claude. codex may not resume this conversation, and earlier context may not carry over.",
    );
  });
});
