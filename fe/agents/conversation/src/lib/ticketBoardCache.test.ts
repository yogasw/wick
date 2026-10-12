import { describe, test, expect, beforeEach, afterEach } from "vitest";
import {
  BOARD_CACHE_MAX,
  BOARD_CACHE_TTL_MS,
  boardGen,
  cachedBoard,
  cachedFilter,
  forgetBoards,
  rememberBoard,
  rememberFilter,
  resetBoardCache,
} from "./ticketBoardCache.js";
import { setViewerId } from "./viewer.js";
import type { TicketBoard } from "./types/agents.js";

const board = (title: string) => ({ tickets: [{ id: "T-1", title }], untracked: [] }) as unknown as TicketBoard;
const titleOf = (b?: TicketBoard) => b?.tickets[0]?.title;

describe("ticketBoardCache", () => {
  beforeEach(() => {
    sessionStorage.clear();
    resetBoardCache();
    setViewerId("u1");
  });
  afterEach(() => setViewerId(""));

  test("answers the last board of the same request only", () => {
    rememberBoard("p1", "rows=3", board("a"), boardGen("p1"));
    expect(titleOf(cachedBoard("p1", "rows=3"))).toBe("a");
    expect(cachedBoard("p1", "rows=0")).toBeUndefined();
    expect(cachedBoard("p2", "rows=3")).toBeUndefined();
  });

  test("survives a reload through sessionStorage", () => {
    rememberBoard("p1", "q", board("a"), boardGen("p1"));
    resetBoardCache();
    expect(titleOf(cachedBoard("p1", "q"))).toBe("a");
  });

  test("never serves another user's boards, and drops them", () => {
    rememberBoard("p1", "q", board("a"), boardGen("p1"));
    rememberFilter("p1", { assignee: "me" });
    resetBoardCache();
    setViewerId("u2");
    expect(cachedBoard("p1", "q")).toBeUndefined();
    expect(cachedFilter("p1")).toBeUndefined();
    expect(sessionStorage.getItem("wick.ticketBoards.u1")).toBeNull();
  });

  test("stores nothing without a known user", () => {
    setViewerId("");
    resetBoardCache();
    rememberBoard("p1", "q", board("a"), boardGen("p1"));
    expect(cachedBoard("p1", "q")).toBeUndefined();
    expect(sessionStorage.length).toBe(0);
  });

  test("an entry older than the TTL is not served", () => {
    rememberBoard("p1", "q", board("a"), boardGen("p1"), 1_000);
    expect(cachedBoard("p1", "q", 1_000 + BOARD_CACHE_TTL_MS)).toBeDefined();
    expect(cachedBoard("p1", "q", 1_001 + BOARD_CACHE_TTL_MS)).toBeUndefined();
  });

  test("keeps the newest BOARD_CACHE_MAX boards", () => {
    for (let i = 0; i <= BOARD_CACHE_MAX; i++) rememberBoard("p" + i, "q", board("b" + i), boardGen("p" + i), 1_000 + i);
    const now = 1_000 + BOARD_CACHE_MAX;
    expect(cachedBoard("p0", "q", now)).toBeUndefined();
    expect(titleOf(cachedBoard("p1", "q", now))).toBe("b1");
    expect(titleOf(cachedBoard("p" + BOARD_CACHE_MAX, "q", now))).toBe("b" + BOARD_CACHE_MAX);
  });

  test("a local write drops the project's boards and the answers already in flight", () => {
    rememberBoard("p1", "q", board("a"), boardGen("p1"));
    rememberBoard("p2", "q", board("b"), boardGen("p2"));
    const inFlight = boardGen("p1");
    forgetBoards("p1");
    expect(cachedBoard("p1", "q")).toBeUndefined();
    expect(titleOf(cachedBoard("p2", "q"))).toBe("b");
    rememberBoard("p1", "q", board("stale"), inFlight);
    expect(cachedBoard("p1", "q")).toBeUndefined();
    rememberBoard("p1", "q", board("fresh"), boardGen("p1"));
    expect(titleOf(cachedBoard("p1", "q"))).toBe("fresh");
  });

  test("a write with no project drops every board", () => {
    rememberBoard("p1", "q", board("a"), boardGen("p1"));
    const inFlight = boardGen("p2");
    forgetBoards();
    expect(cachedBoard("p1", "q")).toBeUndefined();
    rememberBoard("p2", "q", board("stale"), inFlight);
    expect(cachedBoard("p2", "q")).toBeUndefined();
  });
});
