import { describe, it, expect } from "vitest";
import { composerPlaceholder, connectorCaption, hiddenTabNote } from "../agentMode.js";

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
