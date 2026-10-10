import { describe, test, expect } from "vitest";
import { replyPreview, replyable, REPLY_PREVIEW_MAX } from "../replyTo.js";
import type { ConversationTurn } from "../types/agents.js";

const turn = (o: Partial<ConversationTurn>) => ({ turn_id: "1", role: "user", text: "hi", ...o }) as ConversationTurn;

describe("replyPreview", () => {
  test("one line, clipped", () => {
    expect(replyPreview("a\n\n b")).toBe("a b");
    const long = replyPreview("y".repeat(500));
    expect(Array.from(long).length).toBe(REPLY_PREVIEW_MAX);
    expect(long.endsWith("…")).toBe(true);
  });
});

describe("replyable", () => {
  test("needs a stored id and something to quote", () => {
    expect(replyable(turn({}))).toBe(true);
    expect(replyable(turn({ turn_id: "local-user-3" }))).toBe(false);
    expect(replyable(turn({ turn_id: "" }))).toBe(false);
    expect(replyable(turn({ role: "system" }))).toBe(false);
    expect(replyable(turn({ text: "  " }))).toBe(false);
  });
  test("only server ids: streaming, remote and postback bubbles are not replyable", () => {
    expect(replyable(turn({ turn_id: "1791617791942000000" }))).toBe(true);
    expect(replyable(turn({ turn_id: "turn-12" }))).toBe(true);
    for (const id of ["live-1700", "remote-user-1700", "postback-3", "sys-1", "error-2", "turn-x", "12a"]) {
      expect(replyable(turn({ turn_id: id }))).toBe(false);
    }
  });
});
