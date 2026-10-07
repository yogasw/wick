import { describe, test, expect, vi, afterEach } from "vitest";
import { artifactScrollGuard, artifactHeightReporter, buildArtifactSrcdoc } from "../richRender.js";

/* Run an injected <script> body in the jsdom window, as the iframe would. */
function runScript(tag: string) {
  const body = tag.replace(/^<script>/, "").replace(/<\/script>$/, "");
  new Function(body)();
}

describe("artifact scroll guard", () => {
  const origSIV = Element.prototype.scrollIntoView;
  const origFocus = HTMLElement.prototype.focus;
  afterEach(() => {
    Element.prototype.scrollIntoView = origSIV;
    HTMLElement.prototype.focus = origFocus;
    document.body.innerHTML = "";
    vi.restoreAllMocks();
  });

  test("is injected into every artifact's <head>, before the artifact's scripts", () => {
    const doc = buildArtifactSrcdoc("<script>go()</script>");
    expect(doc.indexOf("EP.scrollIntoView=")).toBeGreaterThan(-1);
    expect(doc.indexOf("EP.scrollIntoView=")).toBeLessThan(doc.indexOf("go()"));
  });

  // The Modena widget calls row.scrollIntoView({block:"nearest",behavior:"smooth"}).
  test("scrollIntoView scrolls the container inside the document, never escapes it", () => {
    runScript(artifactScrollGuard());
    const box = document.createElement("div");
    box.style.overflowY = "auto";
    const row = document.createElement("div");
    box.appendChild(row);
    document.body.appendChild(box);
    Object.defineProperty(box, "scrollHeight", { value: 1000 });
    Object.defineProperty(box, "clientHeight", { value: 200 });
    box.getBoundingClientRect = () => ({ top: 0, bottom: 200, left: 0, right: 300 }) as DOMRect;
    row.getBoundingClientRect = () => ({ top: 500, bottom: 540, left: 0, right: 300 }) as DOMRect;
    const boxScroll = vi.fn();
    (box as unknown as { scrollBy: unknown }).scrollBy = boxScroll;
    const winScroll = vi.spyOn(window, "scrollBy").mockImplementation(() => {});
    row.scrollIntoView({ block: "nearest", behavior: "smooth" });
    expect(boxScroll).toHaveBeenCalledWith({ top: 340, left: 0, behavior: "smooth" });
    // the document root is visited at most once and nothing above it exists
    // for the shim to reach; the parent page is never addressed
    expect(winScroll.mock.calls.every((c) => typeof c[0] === "object")).toBe(true);
  });

  test("focus() never scrolls by itself; it goes through the guarded scrollIntoView", () => {
    const nativeFocus = vi.fn();
    HTMLElement.prototype.focus = nativeFocus as unknown as typeof HTMLElement.prototype.focus;
    runScript(artifactScrollGuard());
    const input = document.createElement("input");
    document.body.appendChild(input);
    const siv = vi.spyOn(Element.prototype, "scrollIntoView").mockImplementation(() => {});
    input.focus();
    expect(nativeFocus).toHaveBeenCalledWith({ preventScroll: true });
    expect(siv).toHaveBeenCalledWith({ block: "nearest" });
    siv.mockClear();
    input.focus({ preventScroll: true });
    expect(siv).not.toHaveBeenCalled();
  });
});

/* The 100vh ratchet: a child sized calc(100vh - x) grows every time the
   parent grows the frame to our report. Simulated by tying the measured
   height to innerHeight. */
describe("artifact height reporter — viewport echo", () => {
  afterEach(() => vi.useRealTimers());

  test("a taller reading right after the frame resized is not reported; real growth later is", () => {
    vi.useFakeTimers();
    const posted: number[] = [];
    vi.spyOn(window, "postMessage").mockImplementation(((m: { height: number }) => { posted.push(m.height); }) as typeof window.postMessage);
    let natural = 0; // extra height from real content
    Object.defineProperty(document.documentElement, "scrollHeight", {
      configurable: true,
      get: () => window.innerHeight + 200 + natural, // vh-relative doc
    });
    vi.stubGlobal("innerHeight", 320);
    runScript(artifactHeightReporter("vh"));
    window.dispatchEvent(new Event("load"));
    expect(posted).toEqual([520]);
    // parent applies 520 → frame resizes → doc re-measures 720: echo, dropped
    vi.stubGlobal("innerHeight", 520);
    window.dispatchEvent(new Event("resize"));
    expect(posted).toEqual([520]);
    // once the window has passed, a real change (mutation-driven send) grows
    vi.advanceTimersByTime(2000);
    const afterTimers = posted.length;
    natural = 300;
    window.dispatchEvent(new Event("resize"));
    expect(posted.length).toBeGreaterThanOrEqual(afterTimers);
    expect(Math.max(...posted)).toBeLessThan(2400);
    vi.unstubAllGlobals();
    delete (document.documentElement as unknown as { scrollHeight?: number }).scrollHeight;
  });
});

describe("artifact height reporter — inner scroll switch", () => {
  afterEach(() => { document.documentElement.style.overflow = ""; });

  test("inline frames start clipped and follow the host's overflow message", () => {
    runScript(artifactHeightReporter("sw"));
    expect(document.documentElement.style.overflow).toBe("hidden");
    window.dispatchEvent(new MessageEvent("message", { data: { type: "wick-artifact-overflow", id: "sw", on: true } }));
    expect(document.documentElement.style.overflow).toBe("auto");
    window.dispatchEvent(new MessageEvent("message", { data: { type: "wick-artifact-overflow", id: "other", on: false } }));
    expect(document.documentElement.style.overflow).toBe("auto");
    window.dispatchEvent(new MessageEvent("message", { data: { type: "wick-artifact-overflow", id: "sw", on: false } }));
    expect(document.documentElement.style.overflow).toBe("hidden");
  });

  test("the Full screen frame (clip=false) keeps its own scrollbar", () => {
    runScript(artifactHeightReporter("fs", false));
    expect(document.documentElement.style.overflow).toBe("");
  });
});

// Review #5: a taller reading dropped as an echo is re-measured once the
// echo window closes, so real growth landing inside it is not lost.
describe("artifact height reporter — dropped echo is resent", () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    delete (document.documentElement as unknown as { scrollHeight?: number }).scrollHeight;
  });

  test("re-sends after the window", () => {
    vi.useFakeTimers();
    const posted: number[] = [];
    vi.spyOn(window, "postMessage").mockImplementation(((m: { height: number }) => { posted.push(m.height); }) as typeof window.postMessage);
    let content = 500;
    Object.defineProperty(document.documentElement, "scrollHeight", { configurable: true, get: () => content });
    vi.stubGlobal("innerHeight", 320);
    runScript(artifactHeightReporter("rs"));
    window.dispatchEvent(new Event("load"));
    // (reporters from earlier tests share this window, so compare values)
    expect(new Set(posted)).toEqual(new Set([500]));
    vi.stubGlobal("innerHeight", 500);
    content = 650; // an image finished right as the frame resized
    window.dispatchEvent(new Event("resize"));
    expect(posted).not.toContain(650);
    vi.advanceTimersByTime(30); // the 50ms timer is still inside the window
    vi.advanceTimersByTime(300);
    expect(posted).toContain(650);
  });
});

// Review #6: an element inside a shadow root still scrolls the light-DOM
// container around its host, and still never escapes the document.
describe("artifact scroll guard — shadow DOM", () => {
  const origSIV = Element.prototype.scrollIntoView;
  afterEach(() => {
    Element.prototype.scrollIntoView = origSIV;
    document.body.innerHTML = "";
  });

  test("crosses the shadow boundary to the scrolling container", () => {
    runScript(artifactScrollGuard());
    const box = document.createElement("div");
    box.style.overflowY = "auto";
    Object.defineProperty(box, "scrollHeight", { value: 1000 });
    Object.defineProperty(box, "clientHeight", { value: 200 });
    box.getBoundingClientRect = () => ({ top: 0, bottom: 200, left: 0, right: 300 }) as DOMRect;
    const host = document.createElement("div");
    box.appendChild(host);
    document.body.appendChild(box);
    const root = host.attachShadow({ mode: "open" });
    const row = document.createElement("div");
    root.appendChild(row);
    row.getBoundingClientRect = () => ({ top: 500, bottom: 540, left: 0, right: 300 }) as DOMRect;
    const boxScroll = vi.fn();
    (box as unknown as { scrollBy: unknown }).scrollBy = boxScroll;
    vi.spyOn(window, "scrollBy").mockImplementation(() => {});
    row.scrollIntoView({ block: "nearest" });
    expect(boxScroll).toHaveBeenCalledWith({ top: 340, left: 0, behavior: "auto" });
  });
});
