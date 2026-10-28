import { describe, test, expect } from "vitest";
import { splitWickPreamble, CATCHUP_HEADER } from "../wickPreamble.js";

describe("splitWickPreamble", () => {
  test("a plain message is left alone", () => {
    const p = splitWickPreamble("halo\n\napa kabar");
    expect(p).toMatchObject({ listen: "", catchUp: "", body: "halo\n\napa kabar" });
  });

  test("the short listen note and the catch-up block come off the person's words", () => {
    const text = `[listen mode · held 15s · someone else replied meanwhile] Not a mention: …\nFor this thread: only WABA\n\n${CATCHUP_HEADER}\n- Budi (1.2): sudah dicek\n- bot (1.3): ok\n\nTapi kenapa?\n\nTolong cek`;
    const p = splitWickPreamble(text);
    expect(p.listenLabel).toBe("held 15s · someone else replied meanwhile");
    expect(p.listen).toContain("For this thread: only WABA");
    expect(p.catchUpCount).toBe(2);
    expect(p.body).toBe("Tapi kenapa?\n\nTolong cek");
  });

  test("the long note of 0.1.543–0.1.545 still folds away", () => {
    const text = "[listen mode] This reply … wick held it 15s before passing it on; …\nListen instructions (LISTEN.md):\nAnswer only…\n\nAgent rules:\n- a\n\nIs the endpoint for this available?";
    const p = splitWickPreamble(text);
    expect(p.listenLabel).toBe("held 15s");
    expect(p.body).toBe("Is the endpoint for this available?");
  });

  test("a mention turn with only a catch-up block", () => {
    const p = splitWickPreamble(`${CATCHUP_HEADER}\n- A (1): x\n\n<@U1> cek ini`);
    expect(p.listen).toBe("");
    expect(p.catchUpCount).toBe(1);
    expect(p.body).toBe("<@U1> cek ini");
  });
});

describe("splitWickPreamble auto-reply", () => {
  test("an auto-reply note becomes the chip, marked auto", () => {
    const p = splitWickPreamble("[auto-reply · held 3s] Not a mention, but this thread auto-replies: answer it as you would a mention, never [silent].\n\ncek lagi dong");
    expect(p.listenMode).toBe("auto");
    expect(p.listenLabel).toBe("held 3s");
    expect(p.body).toBe("cek lagi dong");
  });
  test("a listen note stays listen", () => {
    expect(splitWickPreamble("[listen mode · held 15s] Not a mention.\n\nhalo").listenMode).toBe("listen");
  });
});
