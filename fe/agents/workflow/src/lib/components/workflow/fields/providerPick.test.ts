import { describe, expect, it } from "vitest";
import { buildProviderOptions } from "@wick-fe/common-ui";
import { catalogProviderList, fromPickValue, toPickValue } from "./providerPick";

const catalog = [
  { name: "claude", type: "claude", is_default: true },
  { name: "claude_support_ent", type: "claude" },
  { name: "team", type: "codex" },
];

describe("toPickValue", () => {
  it("is empty for the engine default", () => {
    expect(toPickValue(catalog, "", "")).toBe("");
    expect(toPickValue(catalog, undefined)).toBe("");
  });
  it("maps a stored instance name to its type/name key", () => {
    expect(toPickValue(catalog, "claude_support_ent")).toBe("claude/claude_support_ent");
    expect(toPickValue(catalog, "team", "gpt-5")).toBe("codex/team::gpt-5");
  });
  it("keeps a legacy name the catalog no longer offers visible", () => {
    const v = toPickValue(catalog, "gone");
    const opts = buildProviderOptions(catalogProviderList(catalog), v);
    const extra = opts.find((o) => o.label.includes("(unavailable)"));
    expect(extra).toBeTruthy();
    expect(fromPickValue(extra!.value).provider).toBe("gone");
  });
});

describe("fromPickValue", () => {
  it("splits type/name::model into the node's provider + model", () => {
    expect(fromPickValue("claude/claude_support_ent::sonnet")).toEqual({ provider: "claude_support_ent", model: "sonnet" });
    expect(fromPickValue("codex/team")).toEqual({ provider: "team", model: "" });
    expect(fromPickValue("")).toEqual({ provider: "", model: "" });
  });
  it("round-trips through toPickValue", () => {
    const v = toPickValue(catalog, "team", "gpt-5");
    const back = fromPickValue(v);
    expect(toPickValue(catalog, back.provider, back.model)).toBe(v);
  });
});

describe("catalogProviderList", () => {
  it("falls back to the name when an older server sends no type", () => {
    expect(catalogProviderList([{ name: "claude" }])).toEqual([{ type: "claude", name: "claude" }]);
  });
});
