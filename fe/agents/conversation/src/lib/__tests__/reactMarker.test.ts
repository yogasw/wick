import { describe, expect, it } from "vitest";
import { splitReactMarker, emojiGlyph, reactionsByTurn } from "../reactMarker.js";
import type { ConversationTurn } from "../types/agents.js";

const turn = (role: string, text: string) => ({ turn_id: "", role, text, events: [], attachments: [] }) as unknown as ConversationTurn;

describe("splitReactMarker", () => {
  it("splits an opening marker line from the rest", () => {
    expect(splitReactMarker("[react :ok_hand:]\nNoted, next step is mine.")).toEqual({ name: "ok_hand", ts: "", rest: "Noted, next step is mine." });
    expect(splitReactMarker("[react :+1: 1700000000.000100]")).toEqual({ name: "+1", ts: "1700000000.000100", rest: "" });
    expect(splitReactMarker(" [REACT:pray] ")?.name).toBe("pray");
  });
  it("ignores a marker that is not its own first line", () => {
    for (const t of ["Done.\n[react :pray:]", "[react :pray:] thanks", "[react :pray: abc]", "", undefined]) {
      expect(splitReactMarker(t)).toBeNull();
    }
  });
});

describe("emojiGlyph", () => {
  it("maps common names and keeps custom ones as :name:", () => {
    expect(emojiGlyph("pray")).toBe("🙏");
    expect(emojiGlyph("raised_hands::skin-tone-3")).toBe("🙌");
    expect(emojiGlyph("acme_custom")).toBe(":acme_custom:");
  });
});

describe("reactionsByTurn", () => {
  it("puts the reaction on the nearest user turn before the reply", () => {
    const turns = [turn("user", "hi"), turn("user", "thanks"), turn("assistant", "[react :pray:]"), turn("assistant", "[react :+1: 1700000000.000100]\nok")];
    expect(reactionsByTurn(turns)).toEqual([[], ["pray"], [], []]);
  });
});
