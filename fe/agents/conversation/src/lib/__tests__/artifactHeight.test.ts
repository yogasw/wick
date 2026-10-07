import { describe, test, expect, afterEach } from "vitest";
import {
  DEFAULT_HEIGHT,
  MAX_HEIGHT,
  inlineCap,
  fitHeight,
  SCROLL_HYSTERESIS,
  anchorShift,
  artifactKey,
  rememberHeight,
  recallHeight,
  _resetHeightMemo,
} from "../artifactHeight.js";

afterEach(() => _resetHeightMemo());

describe("inlineCap", () => {
  test("is a fraction of the visible chat height", () => {
    expect(inlineCap(1000)).toBe(800);
  });
  test("never below the default nor above the hard max", () => {
    expect(inlineCap(100)).toBe(DEFAULT_HEIGHT);
    expect(inlineCap(10000)).toBe(MAX_HEIGHT);
  });
  test("an unknown viewport falls back to the hard max", () => {
    expect(inlineCap(0)).toBe(MAX_HEIGHT);
    expect(inlineCap(Number.NaN)).toBe(MAX_HEIGHT);
  });
});

describe("fitHeight", () => {
  // A scrollbar narrows the document; content whose height follows its width
  // then drops just under the cap, the scrollbar goes, and it is over again.
  test("a scrolling document stays scrolling until it is clearly under the cap", () => {
    expect(fitHeight(790, 1000, true)).toEqual({ height: 800, scroll: true });
    expect(fitHeight(800 - SCROLL_HYSTERESIS, 1000, true)).toEqual({ height: 800 - SCROLL_HYSTERESIS, scroll: false });
    // not scrolling yet: the same reading just fits
    expect(fitHeight(790, 1000, false)).toEqual({ height: 790, scroll: false });
  });
  test("the scrollbar-width flip settles instead of alternating", () => {
    // 810 wide, 795 once the scrollbar takes its width
    let scrolling = false;
    const seen: boolean[] = [];
    for (let i = 0; i < 6; i++) {
      const fit = fitHeight(scrolling ? 795 : 810, 1000, scrolling);
      scrolling = fit.scroll;
      seen.push(scrolling);
    }
    expect(seen).toEqual([true, true, true, true, true, true]);
  });
  test("content that fits keeps its own height, no inner scroll", () => {
    expect(fitHeight(600.2, 1000)).toEqual({ height: 601, scroll: false });
    expect(fitHeight(800, 1000)).toEqual({ height: 800, scroll: false });
  });
  test("content past the cap is capped and scrolls internally", () => {
    expect(fitHeight(801, 1000)).toEqual({ height: 800, scroll: true });
  });
  // A 100vh doc reports taller each time the frame grows. The cap does not
  // depend on the frame, so it settles there instead of climbing.
  test("a viewport-relative doc converges at the cap", () => {
    let h = DEFAULT_HEIGHT;
    for (let i = 0; i < 20; i++) h = fitHeight(h + 200, 1000).height;
    expect(h).toBe(800);
  });
});

describe("anchorShift", () => {
  test("artifact fully above the view: shift by the height change", () => {
    expect(anchorShift(320, 2400, 50, 100)).toBe(2080);
    expect(anchorShift(2400, 320, 50, 100)).toBe(-2080);
  });
  test("artifact on screen: no shift (growth happens below what is read)", () => {
    expect(anchorShift(320, 2400, 400, 100)).toBe(0);
  });
});

describe("height memo", () => {
  test("keys by url first, then by inline source", () => {
    expect(artifactKey("/f?a", "<p>x</p>")).toBe("u:/f?a");
    expect(artifactKey(undefined, "<p>x</p>")).toBe(artifactKey(undefined, "<p>x</p>"));
    expect(artifactKey(undefined, "<p>x</p>")).not.toBe(artifactKey(undefined, "<p>y</p>"));
    expect(artifactKey(undefined, null)).toBeNull();
  });
  test("remembers the last height per key", () => {
    rememberHeight("u:/a", 900);
    rememberHeight("u:/a", 1200);
    expect(recallHeight("u:/a")).toBe(1200);
    expect(recallHeight("u:/b")).toBeUndefined();
    rememberHeight(null, 500);
    rememberHeight("u:/c", 0);
    expect(recallHeight("u:/c")).toBeUndefined();
  });
  test("is bounded", () => {
    for (let i = 0; i < 250; i++) rememberHeight(`u:/${i}`, 400);
    expect(recallHeight("u:/0")).toBeUndefined();
    expect(recallHeight("u:/249")).toBe(400);
  });
});
