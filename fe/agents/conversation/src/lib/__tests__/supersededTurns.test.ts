import { describe, test, expect } from "vitest";

/* The rule ConversationThread applies, stated once so it can be tested
   without mounting the component: an interrupted assistant turn followed
   immediately by another assistant turn was superseded by it. */
type T = { role: string; interrupted?: boolean; text: string };

function shown(turns: T[]): T[] {
  return turns.filter((t, i) => {
    if (!t.interrupted || t.role !== "assistant") return true;
    const next = turns[i + 1];
    return !next || next.role !== "assistant";
  });
}

describe("superseded turns", () => {
  test("a cut-off answer finished by the next turn is shown once", () => {
    const out = shown([
      { role: "user", text: "docs perlu diupdate" },
      { role: "assistant", interrupted: true, text: "Betul — kucek sisanya" },
      { role: "assistant", text: "Betul, masih ada sisa — dua tempat…" },
    ]);
    expect(out.map((t) => t.text)).toEqual(["docs perlu diupdate", "Betul, masih ada sisa — dua tempat…"]);
  });

  test("it works when the resume was reworded", () => {
    // The server-side trim only catches an exact prefix; this is the case
    // that slips past it, and the one the user actually noticed.
    const out = shown([
      { role: "assistant", interrupted: true, text: "Ketemu penyebabnya" },
      { role: "assistant", text: "Penyebabnya ketemu, begini ceritanya" },
    ]);
    expect(out).toHaveLength(1);
    expect(out[0].text).toBe("Penyebabnya ketemu, begini ceritanya");
  });

  test("an interruption the agent never came back from is kept", () => {
    // Hiding it would erase the only evidence that anything was said.
    const out = shown([
      { role: "assistant", interrupted: true, text: "half an answer" },
      { role: "user", text: "halo?" },
    ]);
    expect(out.map((t) => t.text)).toEqual(["half an answer", "halo?"]);
  });

  test("the last turn is kept even when interrupted", () => {
    const out = shown([{ role: "assistant", interrupted: true, text: "cut off, nothing after" }]);
    expect(out).toHaveLength(1);
  });

  test("a completed turn is never dropped", () => {
    const out = shown([
      { role: "assistant", text: "first answer" },
      { role: "assistant", text: "second answer" },
    ]);
    expect(out).toHaveLength(2);
  });
});
