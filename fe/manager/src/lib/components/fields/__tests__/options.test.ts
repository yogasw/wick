import { describe, it, expect } from "vitest";
import {
  parseColOpts,
  kvColumns,
  parseRows,
  isVisible,
  parseGroup,
  groupFields,
  isFieldVisible,
  DEFAULT_GROUP_TITLE,
} from "../options.js";
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

describe("parseGroup", () => {
  it("defaults empty to the default title with no desc", () => {
    expect(parseGroup("")).toEqual({ title: DEFAULT_GROUP_TITLE, desc: "", collapsed: false });
    expect(parseGroup(undefined)).toEqual({ title: DEFAULT_GROUP_TITLE, desc: "", collapsed: false });
  });

  it("uses the whole value as title when no pipe", () => {
    expect(parseGroup("Access Control")).toEqual({
      title: "Access Control",
      desc: "",
      collapsed: false,
    });
  });

  it("splits title|description and trims both", () => {
    expect(parseGroup("Connection | Transport creds")).toEqual({
      title: "Connection",
      desc: "Transport creds",
      collapsed: false,
    });
  });

  it("marks the card collapsed on a 3rd 'collapsed' segment", () => {
    expect(parseGroup("Advanced|Extra knobs|collapsed")).toEqual({
      title: "Advanced",
      desc: "Extra knobs",
      collapsed: true,
    });
  });

  it("supports collapsed with no description (Title||collapsed)", () => {
    expect(parseGroup("Advanced||collapsed")).toEqual({
      title: "Advanced",
      desc: "",
      collapsed: true,
    });
  });

  it("ignores a non-collapsed 3rd segment", () => {
    expect(parseGroup("A|b|whatever").collapsed).toBe(false);
  });
});

describe("groupFields", () => {
  it("collapses ungrouped fields into the default card", () => {
    const groups = groupFields([field({ key: "a" }), field({ key: "b" })]);
    expect(groups).toHaveLength(1);
    expect(groups[0].title).toBe(DEFAULT_GROUP_TITLE);
    expect(groups[0].simple.map((f) => f.key)).toEqual(["a", "b"]);
  });

  it("partitions by group title in first-seen order", () => {
    const groups = groupFields([
      field({ key: "a", group: "X" }),
      field({ key: "b", group: "Y" }),
      field({ key: "c", group: "X" }),
    ]);
    expect(groups.map((g) => g.title)).toEqual(["X", "Y"]);
    expect(groups[0].simple.map((f) => f.key)).toEqual(["a", "c"]);
    expect(groups[1].simple.map((f) => f.key)).toEqual(["b"]);
  });

  it("keeps the first non-empty description for a group", () => {
    const groups = groupFields([
      field({ key: "a", group: "X" }),
      field({ key: "b", group: "X|the desc" }),
    ]);
    expect(groups[0].desc).toBe("the desc");
  });

  it("splits kvlist and picker fields into their own slots within a group", () => {
    const groups = groupFields([
      field({ key: "s", group: "X" }),
      field({ key: "k", type: "kvlist", group: "X" }),
      field({ key: "p", type: "picker", group: "X" }),
    ]);
    expect(groups).toHaveLength(1);
    expect(groups[0].simple.map((f) => f.key)).toEqual(["s"]);
    expect(groups[0].kvlists.map((f) => f.key)).toEqual(["k"]);
    expect(groups[0].pickers.map((f) => f.key)).toEqual(["p"]);
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

describe("isFieldVisible (cascade)", () => {
  // trigger (bool) → mode (visible_when trigger:true) → channels (visible_when mode:whitelist)
  const trigger = field({ key: "trigger", type: "bool" });
  const mode = field({ key: "mode", visible_when: "trigger:true" });
  const channels = field({ key: "channels", type: "picker", visible_when: "mode:whitelist" });
  const byKey = new Map([trigger, mode, channels].map((f) => [f.key, f]));

  it("hides a grandchild when the grandparent is off, even if the child's own value matches", () => {
    // trigger off, but mode still 'whitelist' → channels must hide because mode is hidden
    const values = { trigger: "false", mode: "whitelist" };
    expect(isFieldVisible(channels, byKey, values)).toBe(false);
    expect(isFieldVisible(mode, byKey, values)).toBe(false);
  });

  it("shows the whole chain when grandparent on and parent value matches", () => {
    const values = { trigger: "true", mode: "whitelist" };
    expect(isFieldVisible(mode, byKey, values)).toBe(true);
    expect(isFieldVisible(channels, byKey, values)).toBe(true);
  });

  it("hides only the leaf when the parent value does not match but grandparent is on", () => {
    const values = { trigger: "true", mode: "all" };
    expect(isFieldVisible(mode, byKey, values)).toBe(true);
    expect(isFieldVisible(channels, byKey, values)).toBe(false);
  });

  it("fails open when a dependency field is missing", () => {
    const orphan = field({ key: "orphan", visible_when: "ghost:true" });
    expect(isFieldVisible(orphan, new Map([[orphan.key, orphan]]), {})).toBe(false); // rule itself fails
    expect(isFieldVisible(orphan, new Map([[orphan.key, orphan]]), { ghost: "true" })).toBe(true);
  });
});
