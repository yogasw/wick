import { describe, test, expect, vi, beforeEach, afterEach } from "vitest";
import { attachThreadScroll } from "../threadStick.js";

/* A fake scroller: scrollHeight is settable, scrollTop clamps like a browser,
   ResizeObserver callbacks are fired by hand. */
let roCallbacks: Array<() => void> = [];
class FakeRO {
  cb: () => void;
  constructor(cb: () => void) { this.cb = cb; roCallbacks.push(cb); }
  observe() {}
  disconnect() {}
}
function makeScroller(height = 3000, client = 500) {
  const el = document.createElement("div");
  el.appendChild(document.createElement("div"));
  let sh = height;
  let top = 0;
  Object.defineProperty(el, "clientHeight", { configurable: true, get: () => client });
  Object.defineProperty(el, "scrollHeight", { configurable: true, get: () => sh });
  Object.defineProperty(el, "scrollTop", {
    configurable: true,
    get: () => top,
    set: (v: number) => { top = Math.max(0, Math.min(v, sh - client)); },
  });
  document.body.appendChild(el);
  return {
    el,
    // content resize: the browser clamps scrollTop (firing a scroll event if
    // it moved), then ResizeObserver runs
    resize(next: number) {
      sh = next;
      const before = top;
      top = Math.max(0, Math.min(top, sh - client));
      if (top !== before) el.dispatchEvent(new Event("scroll"));
      roCallbacks.forEach((cb) => cb());
    },
    max: () => sh - client,
  };
}
// scrollToBottom ignores its own scroll event for one frame; let it pass.
const frame = () => new Promise((r) => setTimeout(r, 40));
const wheel = (deltaY: number) => Object.assign(new Event("wheel"), { deltaY });

beforeEach(() => {
  roCallbacks = [];
  vi.stubGlobal("ResizeObserver", FakeRO);
});
afterEach(() => {
  vi.unstubAllGlobals();
  document.body.innerHTML = "";
});

describe("thread bottom-pin", () => {
  test("send → bottom: scrollToBottom pins and re-sticks after a release", () => {
    const s = makeScroller();
    const ctl = attachThreadScroll(s.el);
    s.el.dispatchEvent(wheel(-40));
    expect(ctl.isStuck()).toBe(false);
    ctl.scrollToBottom();
    expect(s.el.scrollTop).toBe(s.max());
    expect(ctl.isStuck()).toBe(true);
    expect(s.el.hasAttribute("data-stick-bottom")).toBe(true);
  });

  test("follows growth while stuck", () => {
    const s = makeScroller();
    const ctl = attachThreadScroll(s.el);
    ctl.scrollToBottom();
    s.resize(4000);
    expect(s.el.scrollTop).toBe(3500);
  });

  // The original bug: an artifact collapses on remount (2400 → 320), the
  // browser clamps, then it grows back. A stuck reader must end at the bottom.
  test("shrink then grow while stuck → still at the bottom", () => {
    const s = makeScroller(5000);
    const ctl = attachThreadScroll(s.el);
    ctl.scrollToBottom();
    s.resize(2920);
    expect(ctl.isStuck()).toBe(true);
    s.resize(5000);
    expect(s.el.scrollTop).toBe(4500);
  });

  test("a programmatic scroll (scrollIntoView, clamp) never releases the pin", async () => {
    const s = makeScroller();
    const onJump = vi.fn();
    const ctl = attachThreadScroll(s.el, { onJump });
    ctl.scrollToBottom();
    await frame();
    s.el.scrollTop = 1000; // e.g. scrollIntoView carried out of an iframe
    s.el.dispatchEvent(new Event("scroll"));
    expect(ctl.isStuck()).toBe(true);
    // still stuck → no Jump button flashing in the meantime
    expect(onJump).not.toHaveBeenCalledWith(true);
    // the next content change pins back down
    s.resize(3200);
    expect(s.el.scrollTop).toBe(2700);
  });

  test("wheel up releases instantly; growth then leaves the reader alone", () => {
    const s = makeScroller();
    const onJump = vi.fn();
    const ctl = attachThreadScroll(s.el, { onJump });
    ctl.scrollToBottom();
    s.el.dispatchEvent(wheel(-40));
    expect(ctl.isStuck()).toBe(false);
    expect(s.el.hasAttribute("data-stick-bottom")).toBe(false);
    expect(onJump).toHaveBeenLastCalledWith(true);
    s.el.scrollTop = 2000;
    s.resize(4000);
    expect(s.el.scrollTop).toBe(2000);
  });

  test("wheel down does not release", () => {
    const s = makeScroller();
    const ctl = attachThreadScroll(s.el);
    s.el.dispatchEvent(wheel(40));
    expect(ctl.isStuck()).toBe(true);
  });

  test("up-keys release; returning to the bottom (≤4px) re-pins", () => {
    const s = makeScroller();
    const ctl = attachThreadScroll(s.el);
    s.el.dispatchEvent(new KeyboardEvent("keydown", { key: "PageUp" }));
    expect(ctl.isStuck()).toBe(false);
    s.el.scrollTop = s.max() - 3;
    s.el.dispatchEvent(new Event("scroll"));
    expect(ctl.isStuck()).toBe(true);
  });

  test("a small scroll up (≤80px) shows no Jump button and does not re-pin early", async () => {
    const s = makeScroller();
    const onJump = vi.fn();
    const ctl = attachThreadScroll(s.el, { onJump });
    ctl.scrollToBottom();
    await frame();
    s.el.dispatchEvent(wheel(-40));
    s.el.scrollTop = s.max() - 40;
    s.el.dispatchEvent(new Event("scroll"));
    expect(onJump).toHaveBeenLastCalledWith(false);
    expect(ctl.isStuck()).toBe(false);
  });

  test("followIfStuck is coalesced to one pin per frame (cheap on every token)", async () => {
    const s = makeScroller();
    const ctl = attachThreadScroll(s.el);
    await frame();
    let writes = 0;
    const desc = Object.getOwnPropertyDescriptor(s.el, "scrollTop")!;
    Object.defineProperty(s.el, "scrollTop", {
      configurable: true,
      get: desc.get,
      set: (v: number) => { writes++; desc.set!.call(s.el, v); },
    });
    for (let i = 0; i < 100; i++) ctl.followIfStuck();
    expect(writes).toBe(0);
    await frame();
    expect(writes).toBe(1);
  });

  test("smooth scrollToBottom glides, retargets on growth, and does not blink the Jump button", () => {
    const s = makeScroller();
    const calls: ScrollToOptions[] = [];
    (s.el as unknown as { scrollTo: unknown }).scrollTo = (o: ScrollToOptions) => { calls.push(o); };
    const onJump = vi.fn();
    const ctl = attachThreadScroll(s.el, { onJump });
    s.el.dispatchEvent(wheel(-40));
    onJump.mockClear();
    ctl.scrollToBottom({ smooth: true });
    expect(calls.at(-1)).toEqual({ top: 3000, behavior: "smooth" });
    // mid-glide scroll event far from the bottom: no Jump toggle
    s.el.scrollTop = 1000;
    s.el.dispatchEvent(new Event("scroll"));
    expect(onJump).not.toHaveBeenCalledWith(true);
    // growth during the glide retargets it smoothly instead of snapping
    s.resize(3600);
    expect(calls.at(-1)).toEqual({ top: 3600, behavior: "smooth" });
  });

  test("prefers-reduced-motion → instant scroll, no smooth", () => {
    vi.stubGlobal("matchMedia", (q: string) => ({ matches: q.includes("reduce") }));
    const s = makeScroller();
    const scrollTo = vi.fn();
    (s.el as unknown as { scrollTo: unknown }).scrollTo = scrollTo;
    const ctl = attachThreadScroll(s.el);
    ctl.scrollToBottom({ smooth: true });
    expect(scrollTo).not.toHaveBeenCalled();
    expect(s.el.scrollTop).toBe(s.max());
  });

  test("near the top asks for older history", () => {
    const s = makeScroller();
    const onNearTop = vi.fn();
    attachThreadScroll(s.el, { onNearTop });
    s.el.scrollTop = 10;
    s.el.dispatchEvent(new Event("scroll"));
    expect(onNearTop).toHaveBeenCalled();
  });
});
