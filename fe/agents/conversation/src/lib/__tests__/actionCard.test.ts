import { describe, expect, test } from "vitest";
import { splitActionCards, parseActionCard, cardMode, lockCard } from "../actionCard.js";

const card = { id: "cap-1", icon: "shield", title: "Create agent", subtitle: "Rekap", status: "waiting", rows: [["Access", "Notion (read)"]], actions: [{ label: "Approve", value: "approve", style: "primary" }, { label: "Cancel", value: "cancel", style: "ghost" }] };
const fence = (body: string) => "```actioncard\n" + body + "\n```";

describe("splitActionCards", () => {
  test("text around a card stays markdown; the card is parsed", () => {
    const segs = splitActionCards(`Before\n\n${fence(JSON.stringify(card))}\n\nAfter`);
    expect(segs.map((s) => s.kind)).toEqual(["md", "card", "md"]);
    const c = segs[1];
    expect(c.kind === "card" && c.card).toMatchObject({ id: "cap-1", title: "Create agent", rows: [["Access", "Notion (read)"]] });
    expect(c.kind === "card" && c.card.actions?.[0]).toEqual({ label: "Approve", value: "approve", style: "primary" });
  });
  test("invalid JSON stays inside the markdown (rendered as a code block)", () => {
    const text = `x\n${fence("{not json")}\ny`;
    expect(splitActionCards(text)).toEqual([{ kind: "md", text }]);
  });
  test("a card without an id is not a card", () => {
    expect(parseActionCard(JSON.stringify({ title: "no id" }))).toBeNull();
    expect(parseActionCard("[1,2]")).toBeNull();
  });
  test("an unclosed fence is not a card", () => {
    expect(splitActionCards("```actioncard\n" + JSON.stringify(card)).every((s) => s.kind === "md")).toBe(true);
  });
});

describe("cardMode / lockCard", () => {
  const cards = { "cap-1": { turn_id: "t2" }, "done-1": { turn_id: "t3", locked: true } };
  test("newest version is live, older ones superseded, unknown = not a card", () => {
    expect(cardMode("cap-1", "t2", cards)).toBe("active");
    expect(cardMode("cap-1", "t1", cards)).toBe("superseded");
    expect(cardMode("done-1", "t3", cards)).toBe("locked");
    expect(cardMode("nope", "t1", cards)).toBe("none");
  });
  test("a click locks the card and remembers it", () => {
    const pb = { card_id: "cap-1", value: "approve", label: "Approve" };
    const next = lockCard(cards, pb);
    expect(next["cap-1"]).toMatchObject({ locked: true, postback: pb });
    expect(lockCard(cards, { ...pb, card_id: "ghost" })).toBe(cards);
  });
});
