import { describe, test, expect, vi, beforeEach } from "vitest";
import { renderMarkdown } from "../markdown.js";
import { enrich } from "../richRender.js";

/* End-to-end of the image-card render path: markdown emits the
   [data-imagecard] placeholder (common-md), and enrich() (richRender) turns it
   into a clickable thumbnail grid. We drive enrich directly on a detached node
   — its Svelte-action shape (run on mount + observe) works the same. */
function enriched(text: string): HTMLElement {
  const node = document.createElement("div");
  node.innerHTML = renderMarkdown(text);
  enrich(node, text);
  return node;
}

const FENCE =
  "```imagecard\n" +
  "https://abc.com/a.jpg | Alpha\n" +
  "https://abc.net/b.png | Beta\n" +
  "https://abc.org/c.webp\n" +
  "```";

describe("renderImageCards", () => {
  beforeEach(() => {
    /* jsdom has no layout; mermaid/katex are lazy and never load here. */
    vi.useRealTimers();
  });

  test("turns an imagecard fence into one card per valid url", () => {
    const node = enriched(FENCE);
    const cards = node.querySelectorAll("[data-imagecard-item]");
    expect(cards.length).toBe(3);
    const imgs = node.querySelectorAll("[data-imagecard-item] img");
    /* first img of each card is the thumbnail; favicon is a nested img too,
       so assert the thumbnail src specifically via the card's first img */
    expect((cards[0].querySelector("img") as HTMLImageElement).src).toBe("https://abc.com/a.jpg");
    expect((cards[2].querySelector("img") as HTMLImageElement).src).toBe("https://abc.org/c.webp");
    expect(imgs.length).toBeGreaterThanOrEqual(3);
  });

  test("shows the source domain in the pill; caption rides on title (hover)", () => {
    const node = enriched(FENCE);
    const cards = node.querySelectorAll("[data-imagecard-item]");
    /* the visible pill shows the host, not the caption */
    expect(cards[0].textContent).toContain("abc.com");
    expect(cards[2].textContent).toContain("abc.org");
    /* the caption is on title so it surfaces on hover without covering the image */
    expect((cards[0] as HTMLElement).title).toBe("Alpha");
    /* no caption → title falls back to host */
    expect((cards[2] as HTMLElement).title).toBe("abc.org");
  });

  test("lays out as a masonry: image keeps natural height (no fixed crop height)", () => {
    const node = enriched(FENCE);
    const img = node.querySelector("[data-imagecard-item] img") as HTMLImageElement;
    /* natural ratio: h-auto, never a fixed crop height like h-32 */
    expect(img.className).toContain("h-auto");
    expect(img.className).not.toContain("h-32");
    /* cards avoid splitting across a CSS column */
    const card = node.querySelector("[data-imagecard-item]") as HTMLElement;
    expect(card.style.breakInside).toBe("avoid");
  });

  test("any number of images all render into the single gallery (no cap)", () => {
    const many = Array.from({ length: 12 }, (_, i) => `https://abc.com/${i}.jpg | Pic ${i}`).join("\n");
    const node = enriched("```imagecard\n" + many + "\n```");
    expect(node.querySelectorAll("[data-imagecard-item]").length).toBe(12);
  });

  test("parses optional ratio + focus fields and forwards them on click", () => {
    const node = enriched("```imagecard\nhttps://abc.com/a.jpg | Hero | 16:9 | top\n```");
    const detail = vi.fn();
    node.addEventListener("wick-imagecard-open", (e) => detail((e as CustomEvent).detail));
    (node.querySelector("[data-imagecard-item]") as HTMLElement).click();
    const d = detail.mock.calls[0][0];
    expect(d.items[0]).toMatchObject({ url: "https://abc.com/a.jpg", kind: "image" });
  });

  test("skips lines without an http(s) url (e.g. a half-streamed last line)", () => {
    const node = enriched("```imagecard\nhttps://abc.com/a.jpg | ok\nnot-a-url\n```");
    expect(node.querySelectorAll("[data-imagecard-item]").length).toBe(1);
  });

  test("clicking a card dispatches wick-imagecard-open with items + index", () => {
    const node = enriched(FENCE);
    const detail = vi.fn();
    node.addEventListener("wick-imagecard-open", (e) => detail((e as CustomEvent).detail));
    (node.querySelectorAll("[data-imagecard-item]")[1] as HTMLElement).click();
    expect(detail).toHaveBeenCalledOnce();
    const d = detail.mock.calls[0][0];
    expect(d.index).toBe(1);
    expect(d.items).toHaveLength(3);
    expect(d.items[1]).toMatchObject({ url: "https://abc.net/b.png", kind: "image" });
  });

  test("a broken image degrades to a domain chip instead of a broken icon", () => {
    const node = enriched("```imagecard\nhttps://abc.com/x.jpg | Pic\n```");
    const card = node.querySelector("[data-imagecard-item]") as HTMLElement;
    const img = card.querySelector("img") as HTMLImageElement;
    /* simulate the hotlink-blocked / 404 image load failure */
    img.onerror?.(new Event("error"));
    expect(card.textContent).toContain("abc.com");
    expect(card.className).not.toContain("cursor-zoom-in");
  });
});
