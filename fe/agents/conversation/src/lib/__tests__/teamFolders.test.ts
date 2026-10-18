import { describe, test, expect } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import {
  agentTier,
  byTier,
  canUnfile,
  createFolder,
  deleteFolder,
  folderOpen,
  folderView,
  foldersOf,
  groupTier,
  INHERITED_PREFIX,
  materialize,
  moveAgent,
  parseFoldersCache,
  readApplies,
  saveApplies,
  renameError,
  renameFolder,
  reorderFolder,
  resolveFolders,
  ROSTER_MOTION_MS,
  rosterMotionMs,
  teamList,
  toggleOpen,
  whoLine,
} from "../teamFolders.js";
import type { RosterEntry } from "../rosterList.js";
import type { AgentItem, GroupItem, TeamFolders } from "../api/team.js";

// Noon local time, so "today" and "yesterday" never straddle midnight.
const NOW = new Date(2026, 9, 17, 12, 0, 0).getTime();
const TODAY = new Date(2026, 9, 17, 9, 0, 0).toISOString();
const TODAY_LATER = new Date(2026, 9, 17, 11, 0, 0).toISOString();
const OLD = new Date(2026, 9, 10, 9, 0, 0).toISOString();

const agent = (id: string, last: string | null, over: Partial<AgentItem> = {}) =>
  ({ id, name: id[0].toUpperCase() + id.slice(1), handle: id, is_captain: false, disabled: false, last_active: last, ...over }) as AgentItem;
const group = (id: string, last: string | null, unread = false) =>
  ({ id, name: `Group ${id}`, last_active: last, unread, members: [] }) as unknown as GroupItem;
const ae = (a: AgentItem): RosterEntry => ({ kind: "agent", id: a.id, agent: a });

type St = { work: "idle" | "thinking" | "tool" | "team" | "replying"; attention: boolean; unread: boolean };
const idle: St = { work: "idle", attention: false, unread: false };
/** statusOf from a table of overrides by agent id. */
const statuses = (m: Record<string, Partial<St>>) => (a: AgentItem) => ({ ...idle, ...(m[a.id] ?? {}) }) as never;

const state = (over: Partial<TeamFolders> = {}): TeamFolders => ({ folders: [], place: {}, inherited: {}, ...over });

describe("agentTier", () => {
  test("working, then active, then dozing", () => {
    expect(agentTier(agent("a", OLD), { ...idle, work: "thinking" }, NOW)).toBe(0);
    expect(agentTier(agent("a", OLD), { ...idle, work: "team" }, NOW)).toBe(0);
    expect(agentTier(agent("a", TODAY), idle, NOW)).toBe(1);
    expect(agentTier(agent("a", OLD), { ...idle, unread: true }, NOW)).toBe(1);
    expect(agentTier(agent("a", OLD), { ...idle, attention: true }, NOW)).toBe(1);
    expect(agentTier(agent("a", OLD), idle, NOW)).toBe(2);
  });
  test("a group chat is active when unread or used today", () => {
    expect(groupTier(group("g", OLD, true), NOW)).toBe(1);
    expect(groupTier(group("g", TODAY), NOW)).toBe(1);
    expect(groupTier(group("g", OLD), NOW)).toBe(2);
  });
});

describe("byTier", () => {
  test("Captain on top, then working, active, idle; newest first inside a tier", () => {
    const list = [
      agent("old", OLD),
      agent("captain", OLD, { is_captain: true }),
      agent("fresh", TODAY),
      agent("fresher", TODAY_LATER),
      agent("busy", OLD),
    ].map(ae);
    const out = byTier([...list, { kind: "group", id: "g", group: group("g", null) }], statuses({ busy: { work: "thinking" } }), NOW);
    expect(out.map((e) => e.id)).toEqual(["captain", "busy", "fresher", "fresh", "old", "g"]);
  });
});

describe("resolveFolders", () => {
  test("own placement wins; a placement into a gone folder is loose", () => {
    const s = state({ folders: [{ id: "f1", name: "Client" }], place: { a: "f1", b: "gone" }, inherited: { a: "Ops" } });
    expect(resolveFolders(s, ["a", "b"])).toEqual({ folders: [{ id: "f1", name: "Client" }], placeOf: { a: "f1" } });
  });
  test("a shared agent joins the viewer's folder of the same name, any case", () => {
    const s = state({ folders: [{ id: "f1", name: "Client" }], inherited: { x: "client" } });
    expect(resolveFolders(s, ["x"]).placeOf).toEqual({ x: "f1" });
  });
  test("an owner's folder the viewer lacks is made up after theirs", () => {
    const s = state({ folders: [{ id: "f1", name: "Mine" }], inherited: { x: "Ops", y: "Ops", z: "Billing" } });
    const r = resolveFolders(s, ["x", "y", "z"]);
    expect(r.folders.map((f) => f.name)).toEqual(["Mine", "Billing", "Ops"]);
    expect(r.placeOf.x).toBe(`${INHERITED_PREFIX}ops`);
    expect(r.placeOf.y).toBe(r.placeOf.x);
  });
  test("no state yet is all loose", () => {
    expect(resolveFolders(null, ["a"])).toEqual({ folders: [], placeOf: {} });
  });
});

describe("materialize", () => {
  const ids = () => {
    let n = 0;
    return () => `new${++n}`;
  };
  test("an inherited folder with agents becomes real; its agents still follow by name", () => {
    const s = state({ folders: [{ id: "f1", name: "Mine" }], place: { a: "f1" }, inherited: { x: "Ops" } });
    expect(materialize(s, ["a", "x"], ids())).toEqual({
      folders: [{ id: "f1", name: "Mine" }, { id: "new1", name: "Ops" }],
      place: { a: "f1" },
    });
  });
  test("a move into an inherited folder points at its new id", () => {
    const s = moveAgent(state({ inherited: { x: "Ops" } }), ["x", "a"], "a", `${INHERITED_PREFIX}ops`);
    const body = materialize(s, ["x", "a"], ids());
    expect(body.folders).toEqual([{ id: "new1", name: "Ops" }]);
    expect(body.place).toEqual({ a: "new1" });
  });
  test("an inherited folder emptied by moves is dropped", () => {
    const s = moveAgent(state({ folders: [{ id: "f1", name: "Mine" }], inherited: { x: "Ops" } }), ["x"], "x", "f1");
    expect(materialize(s, ["x"], ids())).toEqual({ folders: [{ id: "f1", name: "Mine" }], place: { x: "f1" } });
  });
  test("a shared agent can't be made loose against its owner's folder", () => {
    const s = state({ inherited: { x: "Ops" } });
    expect(canUnfile(s, "x")).toBe(false);
    expect(canUnfile(state({ place: { x: "f1" } }), "x")).toBe(true);
    expect(canUnfile(s, "y")).toBe(true);
  });
  test("folders start folded, whatever the saved flag says, and open only on a toggle", () => {
    const none = new Set<string>();
    expect(folderOpen(none, "f1", false)).toBe(false);
    expect(folderOpen(none, "f1", true)).toBe(true);
    const opened = toggleOpen(none, "f1");
    expect(folderOpen(opened, "f1", false)).toBe(true);
    expect(folderOpen(opened, "f2", false)).toBe(false);
    expect(none.size).toBe(0);
    expect(folderOpen(toggleOpen(opened, "f1"), "f1", false)).toBe(false);
  });
  test("AgentsApp keeps the open set in memory and never reads the saved flag", () => {
    const app = readFileSync(resolve(__dirname, "../../AgentsApp.svelte"), "utf8");
    expect(app).toMatch(/let openFolders = \$state<ReadonlySet<string>>\(new Set\(\)\)/);
    expect(app).toMatch(/\{@const open = folderOpen\(openFolders, f\.id, searching\)\}/);
    expect(app).not.toMatch(/\.collapsed\b|setCollapsed/);
  });
});

describe("edits", () => {
  const base = state({ folders: [{ id: "f1", name: "A" }, { id: "f2", name: "B" }, { id: "f3", name: "C" }], place: { a: "f1", b: "f2" } });
  test("reorder before and after a target", () => {
    expect(reorderFolder(base, [], "f3", "f1", false).folders.map((f) => f.id)).toEqual(["f3", "f1", "f2"]);
    expect(reorderFolder(base, [], "f1", "f2", true).folders.map((f) => f.id)).toEqual(["f2", "f1", "f3"]);
    expect(reorderFolder(base, [], "f1", "f1", true)).toBe(base);
  });
  test("delete makes its agents loose", () => {
    const s = deleteFolder(base, "f1");
    expect(s.folders.map((f) => f.id)).toEqual(["f2", "f3"]);
    expect(s.place).toEqual({ b: "f2" });
  });
  test("create adds at the end", () => {
    const { state: s, id } = createFolder(base, "  New  ", () => "fx");
    expect(id).toBe("fx");
    expect(s.folders.at(-1)).toEqual({ id: "fx", name: "New" });
  });
  test("move to No folder drops the placement", () => {
    expect(moveAgent(base, [], "a", "").place).toEqual({ b: "f2" });
    expect(moveAgent(base, [], "a", "f3").place).toEqual({ a: "f3", b: "f2" });
  });
  test("rename checks the name and pins shared agents that sat in it by name", () => {
    expect(renameError(base, [], "f1", " ")).not.toBe("");
    expect(renameError(base, [], "f1", "b")).toMatch(/already exists/);
    expect(renameError(base, [], "f1", "A")).toBe("");
    expect(renameError(base, [], "f1", "x".repeat(41))).not.toBe("");
    const s = renameFolder(state({ folders: [{ id: "f1", name: "Ops" }], inherited: { x: "Ops" } }), ["x"], "f1", "Operations");
    expect(s.folders).toEqual([{ id: "f1", name: "Operations" }]);
    expect(resolveFolders(s, ["x"]).placeOf).toEqual({ x: "f1" });
  });
});

describe("folderView", () => {
  const f = { id: "f1", name: "Client" };
  test("thinking beats waiting; summary names who", () => {
    const m = [agent("paragon", TODAY), agent("kpj", TODAY_LATER), agent("cosmix", OLD)];
    const v = folderView(f, m, statuses({ paragon: { work: "thinking" }, kpj: { work: "tool" }, cosmix: { work: "team" } }), NOW);
    expect(v.busy).toBe("thinking");
    expect(v.busyAgents.map((a) => a.id)).toEqual(["kpj", "paragon"]);
    expect(v.summary).toBe("Kpj, Paragon thinking…");
  });
  test("only waiting on teammates is waiting", () => {
    const v = folderView(f, [agent("cosmix", OLD), agent("olx", TODAY)], statuses({ cosmix: { work: "team" } }), NOW);
    expect(v.busy).toBe("waiting");
    expect(v.summary).toBe("Cosmix waiting…");
  });
  test("idle: count of agents and the summed unread", () => {
    const v = folderView(f, [agent("a", OLD, { unread_count: 3 }), agent("b", OLD), agent("c", OLD, { unread_count: 0 })], statuses({ a: { unread: true }, c: { unread: true } }), NOW);
    expect(v.busy).toBe("");
    expect(v.summary).toBe("3 agents");
    expect(v.unread).toBe(4);
    expect(v.sleepy).toBe(false);
  });
  test("all dozing fades the folder; newest member gives its time", () => {
    const v = folderView(f, [agent("a", OLD), agent("b", new Date(2026, 9, 12).toISOString())], statuses({}), NOW);
    expect(v.sleepy).toBe(true);
    expect(v.last_active).toBe(new Date(2026, 9, 12).toISOString());
    expect(v.agents.map((a) => a.id)).toEqual(["b", "a"]);
  });
  test("whoLine caps at two names", () => {
    expect(whoLine([{ name: "A" }, { name: "B" }, { name: "C" }, { name: "D" }], "thinking")).toBe("A, B +2 thinking…");
  });
});

describe("teamList", () => {
  test("filed agents leave the loose list; folders keep their order", () => {
    const agents = [agent("captain", OLD, { is_captain: true }), agent("a", TODAY), agent("b", TODAY_LATER), agent("c", OLD)];
    const s = state({ folders: [{ id: "f2", name: "Z" }, { id: "f1", name: "A" }], place: { a: "f1", b: "f2" } });
    const out = teamList([...agents.map(ae), { kind: "group", id: "g", group: group("g", TODAY) }], resolveFolders(s, agents.map((a) => a.id)), statuses({ b: { work: "thinking" } }), false, NOW);
    expect(out.loose.map((e) => e.id)).toEqual(["captain", "g", "c"]);
    expect(out.folders.map((f) => [f.folder.id, f.agents.map((a) => a.id)])).toEqual([["f2", ["b"]], ["f1", ["a"]]]);
  });
  test("a search leaves out folders without a match", () => {
    const s = state({ folders: [{ id: "f1", name: "A" }, { id: "f2", name: "B" }], place: { a: "f1" } });
    const out = teamList([ae(agent("a", TODAY))], resolveFolders(s, ["a"]), statuses({}), true, NOW);
    expect(out.folders.map((f) => f.folder.id)).toEqual(["f1"]);
    expect(teamList([], resolveFolders(s, []), statuses({}), false, NOW).folders.map((f) => f.summary)).toEqual(["Empty", "Empty"]);
  });
});

describe("folders cache", () => {
  test("a stored answer reads back whole, missing fields filled", () => {
    const f: TeamFolders = { folders: [{ id: "f1", name: "Ops" }], place: { a1: "f1" }, inherited: { a2: "Partners" } };
    expect(parseFoldersCache(JSON.stringify(f))).toEqual(f);
    expect(parseFoldersCache(JSON.stringify({ folders: [] }))).toEqual({ folders: [], place: {}, inherited: {} });
  });

  test("nothing stored, junk or not folders is no cache", () => {
    expect(parseFoldersCache(null)).toBeNull();
    expect(parseFoldersCache("{oops")).toBeNull();
    expect(parseFoldersCache("null")).toBeNull();
    expect(parseFoldersCache(JSON.stringify({ agents: [] }))).toBeNull();
    expect(parseFoldersCache("[]")).toBeNull();
  });

  test("foldersOf fills what an older server leaves out", () => {
    expect(foldersOf({})).toEqual({ folders: [], place: {}, inherited: {} });
  });
});

describe("rosterMotionMs", () => {
  test("rows move, except under reduced motion or a search", () => {
    expect(rosterMotionMs(false, false)).toBe(ROSTER_MOTION_MS);
    expect(rosterMotionMs(true, false)).toBe(0);
    expect(rosterMotionMs(false, true)).toBe(0);
  });
});

describe("folders sync", () => {
  const clean = { rev: 3, acked: 3, busy: false };
  test("a read applies when every edit was saved before and after it", () => {
    expect(readApplies(clean, clean)).toBe(true);
  });
  test("a read never undoes an edit still waiting, in flight or made meanwhile", () => {
    // Collapsed while the read was in flight: the debounce is pending.
    expect(readApplies(clean, { rev: 4, acked: 3, busy: true })).toBe(false);
    // ...and saved before the read landed: the read still predates it.
    expect(readApplies(clean, { rev: 4, acked: 4, busy: false })).toBe(false);
    // Started while a PUT was in flight, landed after it.
    expect(readApplies({ rev: 4, acked: 3, busy: true }, { rev: 4, acked: 4, busy: false })).toBe(false);
    // A PUT answered but a newer one is still queued.
    expect(readApplies({ rev: 5, acked: 4, busy: false }, { rev: 5, acked: 4, busy: false })).toBe(false);
  });
  test("a save's answer applies unless an edit came after it was sent", () => {
    expect(saveApplies(4, { rev: 4, acked: 4, busy: false })).toBe(true);
    expect(saveApplies(4, { rev: 5, acked: 4, busy: true })).toBe(false);
  });
});
