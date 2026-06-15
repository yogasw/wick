import { describe, it, expect } from "vitest";
import { parseColOpts, kvColumns, parseRows, isVisible } from "../options.js";
import type { ConfigField } from "$lib/types.js";

function field(over: Partial<ConfigField>): ConfigField {
  return {
    key: "k",
    type: "text",
    value: "",
    options: "",
    required: false,
    is_secret: false,
    has_value: false,
    description: "",
    visible_when: "",
    env_override: "",
    ...over,
  };
}

describe("parseColOpts", () => {
  it("splits label::value pairs", () => {
    expect(parseColOpts("Public::pub|Private::priv")).toEqual([
      { label: "Public", value: "pub" },
      { label: "Private", value: "priv" },
    ]);
  });

  it("uses the same string when no separator", () => {
    expect(parseColOpts("a|b")).toEqual([
      { label: "a", value: "a" },
      { label: "b", value: "b" },
    ]);
  });

  it("returns [] for empty input", () => {
    expect(parseColOpts("")).toEqual([]);
  });
});

describe("kvColumns", () => {
  it("splits the pipe-separated options", () => {
    expect(kvColumns(field({ options: "id|name" }))).toEqual(["id", "name"]);
  });

  it("falls back to ['value'] when options empty", () => {
    expect(kvColumns(field({ options: "" }))).toEqual(["value"]);
  });
});

describe("parseRows", () => {
  it("parses a JSON array of objects", () => {
    expect(parseRows('[{"id":"1"}]')).toEqual([{ id: "1" }]);
  });

  it("returns [] for empty or malformed input", () => {
    expect(parseRows("")).toEqual([]);
    expect(parseRows("not json")).toEqual([]);
    expect(parseRows('{"id":"1"}')).toEqual([]);
  });
});

describe("isVisible", () => {
  it("is visible with no rule", () => {
    expect(isVisible("", { mode: "x" })).toBe(true);
  });

  it("matches a single dependency value", () => {
    expect(isVisible("mode:advanced", { mode: "advanced" })).toBe(true);
    expect(isVisible("mode:advanced", { mode: "basic" })).toBe(false);
  });

  it("matches any of a pipe-separated value set", () => {
    expect(isVisible("mode:a|b", { mode: "b" })).toBe(true);
    expect(isVisible("mode:a|b", { mode: "c" })).toBe(false);
  });

  it("treats a missing dependency as empty", () => {
    expect(isVisible("mode:", {})).toBe(true);
    expect(isVisible("mode:x", {})).toBe(false);
  });
});
