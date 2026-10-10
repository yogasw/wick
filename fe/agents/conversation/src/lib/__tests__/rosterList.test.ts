import { describe, test, expect } from "vitest";
import { rosterEntries, mobilePins, canMakeGroup, unreadLabel, rowTone, silentPreview, silentPreviewClass, dozing } from "../rosterList.js";
import type { AgentItem, GroupItem } from "../api/team.js";

const agent = (id: string, last: string | null, over: Partial<AgentItem> = {}) =>
  ({ id, name: id[0].toUpperCase() + id.slice(1), handle: id, is_captain: false, disabled: false, last_active: last, ...over }) as AgentItem;
const group = (id: string, last: string | null, members: string[] = []) =>
  ({ id, name: `Group ${id}`, last_active: last, members: members.map((m) => ({ id: m, handle: m, name: m })) }) as unknown as GroupItem;

describe("rosterEntries", () => {
  test("the Captain is pinned on top; the rest mixed, newest first", () => {
    const agents = [agent("captain", "2026-10-01T08:00:00Z", { is_captain: true }), agent("log", "2026-10-03T08:00:00Z"), agent("idle", null)];
    const groups = [group("inc", "2026-10-02T08:00:00Z")];
    expect(rosterEntries(agents, groups, "").map((e) => `${e.kind}:${e.id}`)).toEqual([
      "agent:captain",
      "agent:log",
      "group:inc",
      "agent:idle",
    ]);
  });

  test("a search keeps the Captain on top when it matches", () => {
    const agents = [agent("luna", "2026-10-03T08:00:00Z"), agent("captain", null, { is_captain: true, name: "Lucas" })];
    expect(rosterEntries(agents, [], "lu").map((e) => e.id)).toEqual(["captain", "luna"]);
    expect(rosterEntries(agents, [], "luna").map((e) => e.id)).toEqual(["luna"]);
  });

  test("ties fall back to name", () => {
    const t = "2026-10-03T08:00:00Z";
    expect(rosterEntries([agent("zed", t), agent("abe", t)], [], "").map((e) => e.id)).toEqual(["abe", "zed"]);
  });

  test("search matches agent name/handle and a group's name or members", () => {
    const agents = [agent("vera", null), agent("rekap", null)];
    const groups = [group("a", null, ["vera"]), group("b", null, ["rekap"])];
    expect(rosterEntries(agents, groups, " VER ").map((e) => `${e.kind}:${e.id}`).sort()).toEqual(["agent:vera", "group:a"]);
    expect(rosterEntries(agents, groups, "group b").map((e) => e.id)).toEqual(["b"]);
  });
});

describe("mobilePins", () => {
  test("Captain, then the two agents used last", () => {
    const agents = [
      agent("old", "2026-09-01T08:00:00Z"),
      agent("captain", null, { is_captain: true }),
      agent("new", "2026-10-03T08:00:00Z"),
      agent("mid", "2026-10-02T08:00:00Z"),
    ];
    expect(mobilePins(agents).map((a) => a.id)).toEqual(["captain", "new", "mid"]);
  });

  test("explicit pins come first; disabled agents never pinned; at most three", () => {
    const agents = [
      agent("captain", null, { is_captain: true }),
      agent("off", "2026-10-03T09:00:00Z", { disabled: true }),
      agent("a", "2026-10-03T08:00:00Z"),
      agent("b", "2026-10-02T08:00:00Z"),
      agent("c", "2026-10-01T08:00:00Z"),
    ];
    expect(mobilePins(agents, ["c", "off"]).map((a) => a.id)).toEqual(["c", "captain", "a"]);
  });

  test("fewer agents than pins: all of them, no repeats", () => {
    expect(mobilePins([agent("solo", null, { is_captain: true })]).map((a) => a.id)).toEqual(["solo"]);
    expect(mobilePins([])).toEqual([]);
  });
});

test("canMakeGroup needs two agents", () => {
  expect(canMakeGroup([])).toBe(false);
  expect(canMakeGroup([{ id: "a" }])).toBe(false);
  expect(canMakeGroup([{ id: "a" }, { id: "b" }])).toBe(true);
});

test("unreadLabel: count, capped at 9+; empty when the server sends none", () => {
  expect(unreadLabel(undefined)).toBe("");
  expect(unreadLabel(0)).toBe("");
  expect(unreadLabel(1)).toBe("1");
  expect(unreadLabel(9)).toBe("9");
  expect(unreadLabel(12)).toBe("9+");
});

test("rowTone: unread only accents the time (the badge counts), name and preview stay at normal weight", () => {
  const on = rowTone(true);
  const off = rowTone(false);
  expect(on.name).toBe(off.name);
  expect(on.preview).toBe(off.preview);
  expect(on.time).toContain("font-semibold");
  expect(on.time).toContain("text-green-600");
  expect(off.name).not.toContain("font-bold");
  expect(off.preview).toContain("text-black-800");
  expect(off.time).toBe("text-black-700");
  // Waiting on someone accents the time too, the rest stays as read.
  const waits = rowTone(false, true);
  expect(waits.time).toBe(on.time);
  expect(waits.name).toBe(off.name);
  expect(waits.preview).toBe(off.preview);
});

describe("silentPreview", () => {
  test("the server flag dims the preview; the text is already clean", () => {
    expect(silentPreview("run 3/5: 200 OK", true)).toEqual({ text: "run 3/5: 200 OK", silent: true });
    expect(silentPreview("Sudah dicek, aman.", false)).toEqual({ text: "Sudah dicek, aman.", silent: false });
    expect(silentPreview("Sudah dicek, aman.")).toEqual({ text: "Sudah dicek, aman.", silent: false });
  });
  test("a raw marker never shows, whatever the flag says", () => {
    expect(silentPreview("  [SILENT] nothing new")).toEqual({ text: "nothing new", silent: true });
    expect(silentPreview("[silent]", false)).toEqual({ text: "", silent: true });
  });
  test("a marker mid-text is not a silent reply", () => {
    expect(silentPreview("done. [silent]")).toEqual({ text: "done. [silent]", silent: false });
  });
  test("silent previews render dimmed and italic", () => {
    expect(silentPreviewClass).toContain("italic");
  });
});

describe("Team task rows", () => {
  const ag = (id: string, extra: Record<string, unknown> = {}) =>
    ({ id, name: id, handle: id, status: "idle", main_session_id: "", last_active: "2026-10-10T10:00:00Z", last_preview: "", ...extra }) as unknown as AgentItem;

  test("an agent waiting on someone sits right under the Captain, ahead of newer rows", () => {
    const list = rosterEntries(
      [
        ag("busy", { last_active: "2026-10-10T10:14:00Z" }),
        ag("asks", { last_active: "2026-10-10T10:12:00Z", team_asks: { to: "Captain", question: "q" } }),
        ag("cap", { is_captain: true, last_active: "2026-10-10T10:13:00Z" }),
        ag("off", { disabled: true, needs_attention: true, last_active: "2026-10-10T10:15:00Z" }),
      ],
      [],
      "",
    ).map((e) => e.id);
    expect(list).toEqual(["cap", "asks", "off", "busy"]);
  });

  test("dozing: idle since before today, never while it works, waits or has unread", () => {
    const now = new Date(2026, 9, 10, 14, 0).getTime();
    const yesterday = new Date(2026, 9, 9, 18, 0).toISOString();
    const today = new Date(2026, 9, 10, 9, 0).toISOString();
    const idle = { work: "idle", attention: false, unread: false };
    expect(dozing({ last_active: yesterday }, idle, now)).toBe(true);
    expect(dozing({ last_active: today }, idle, now)).toBe(false);
    expect(dozing({ last_active: yesterday }, { ...idle, work: "replying" }, now)).toBe(false);
    expect(dozing({ last_active: yesterday }, { ...idle, attention: true }, now)).toBe(false);
    expect(dozing({ last_active: yesterday }, { ...idle, unread: true }, now)).toBe(false);
    expect(dozing({ last_active: yesterday, disabled: true }, idle, now)).toBe(false);
    expect(dozing({ last_active: null }, idle, now)).toBe(false);
  });
});
