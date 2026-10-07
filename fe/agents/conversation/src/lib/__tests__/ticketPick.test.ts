import { describe, test, expect } from "vitest";
import { rankTickets, shortTicketId } from "../ticketPick.js";
import type { TicketCard } from "../types/agents.js";

const tk = (id: string, title: string, updated_at = "2026-09-30T00:00:00Z") =>
  ({ id, title, updated_at }) as TicketCard;

describe("rankTickets", () => {
  const list = [
    tk("3eb1f07f4ae081c38ec0edde28a0604e", "Paragon webhook 3eb1", "2026-09-30T10:00:00Z"),
    tk("3eb1f07f4ae08125bf4bff60a77bce2f", "MTI changes", "2026-09-30T09:00:00Z"),
    tk("T-9", "Something about 3eb1f07f4ae08125", "2026-09-30T11:00:00Z"),
  ];

  test("no query = most recently updated first", () => {
    expect(rankTickets(list, "").map((t) => t.id)).toEqual([
      "T-9",
      "3eb1f07f4ae081c38ec0edde28a0604e",
      "3eb1f07f4ae08125bf4bff60a77bce2f",
    ]);
  });

  test("an id hit beats a title hit, even an older one", () => {
    expect(rankTickets(list, "3eb1f07f4ae08125").map((t) => t.id)).toEqual([
      "3eb1f07f4ae08125bf4bff60a77bce2f",
      "T-9",
    ]);
  });

  test("a pasted Notion URL finds its ticket first", () => {
    const url = "https://app.notion.com/p/Moratel-Request-3eb1f07f4ae08125bf4bff60a77bce2f";
    expect(rankTickets(list, url)[0].id).toBe("3eb1f07f4ae08125bf4bff60a77bce2f");
  });

  test("title search is case-insensitive; misses drop out", () => {
    expect(rankTickets(list, "mti").map((t) => t.id)).toEqual(["3eb1f07f4ae08125bf4bff60a77bce2f"]);
    expect(rankTickets(list, "zzz")).toEqual([]);
  });
});

describe("shortTicketId", () => {
  test("short codes pass through; long ids keep head and distinguishing tail", () => {
    expect(shortTicketId("T-4F2A")).toBe("T-4F2A");
    expect(shortTicketId("3eb1f07f4ae081c38ec0edde28a0604e")).toBe("3eb1…a0604e");
  });
});
