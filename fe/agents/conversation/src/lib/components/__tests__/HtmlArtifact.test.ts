import { describe, test, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import HtmlArtifact from "../HtmlArtifact.svelte";
import { setWidgetPolicy, BLOCKED_WIDGET_POLICY } from "../../richRender.js";
import { _resetHeightMemo } from "../../artifactHeight.js";

afterEach(() => {
  vi.unstubAllGlobals();
  _resetHeightMemo();
});

function postHeight(id: string, height: number) {
  window.dispatchEvent(new MessageEvent("message", { data: { type: "wick-artifact-height", id, height } }));
}

/* Pull the reporter id out of the rendered iframe srcdoc so the height test
   can post a matching message (the id is generated per-mount). */
function idFromIframe(iframe: HTMLIFrameElement): string {
  const m = /wick-artifact-height",id:"([^"]+)"/.exec(iframe.srcdoc);
  return m?.[1] ?? "";
}

describe("HtmlArtifact", () => {
  test("renders an auto-height iframe from inline src", () => {
    const { container } = render(HtmlArtifact, { props: { src: "<p>hi</p>", name: "x.html" } });
    const iframe = container.querySelector("iframe") as HTMLIFrameElement;
    expect(iframe).not.toBeNull();
    expect(iframe.style.height).toBe("320px"); // default before any report
  });

  test("fetches html from url", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ text: () => Promise.resolve("<p>fetched</p>") }));
    const { container } = render(HtmlArtifact, { props: { url: "/raw?path=a.html", name: "a.html" } });
    await waitFor(() => expect(container.querySelector("iframe")).not.toBeNull());
    expect(fetch).toHaveBeenCalledWith("/raw?path=a.html");
  });

  test("grows to the height reported by its inline reporter", async () => {
    vi.stubGlobal("innerHeight", 3000);
    const { container } = render(HtmlArtifact, { props: { src: "<p>hi</p>", name: "x.html" } });
    const iframe = container.querySelector("iframe") as HTMLIFrameElement;
    const id = idFromIframe(iframe);
    expect(id).not.toBe("");
    window.dispatchEvent(new MessageEvent("message", { data: { type: "wick-artifact-height", id, height: 742 } }));
    await waitFor(() => expect(iframe.style.height).toBe("742px"));
  });

  test("caps absurd heights", async () => {
    vi.stubGlobal("innerHeight", 5000);
    const { container } = render(HtmlArtifact, { props: { src: "<p>hi</p>", name: "x.html" } });
    const iframe = container.querySelector("iframe") as HTMLIFrameElement;
    const id = idFromIframe(iframe);
    window.dispatchEvent(new MessageEvent("message", { data: { type: "wick-artifact-height", id, height: 99999 } }));
    await waitFor(() => expect(iframe.style.height).toBe("2400px"));
  });

  test("a document taller than the chat-height cap scrolls inside the frame, only then", async () => {
    vi.stubGlobal("innerHeight", 1000);
    const { container } = render(HtmlArtifact, { props: { src: "<p>hi</p>", name: "x.html" } });
    const iframe = container.querySelector("iframe") as HTMLIFrameElement;
    expect(iframe.hasAttribute("scrolling")).toBe(false);
    const sent: unknown[] = [];
    vi.spyOn(iframe.contentWindow as Window, "postMessage").mockImplementation(((m: unknown) => { sent.push(m); }) as typeof window.postMessage);
    const id = idFromIframe(iframe);
    postHeight(id, 4000);
    await waitFor(() => expect(iframe.style.height).toBe("800px"));
    expect(sent.at(-1)).toEqual({ type: "wick-artifact-overflow", id, on: true });
    postHeight(id, 500);
    await waitFor(() => expect(iframe.style.height).toBe("500px"));
    expect(sent.at(-1)).toEqual({ type: "wick-artifact-overflow", id, on: false });
  });

  test("the overflow answer is only sent when it changes", async () => {
    vi.stubGlobal("innerHeight", 1000);
    const { container } = render(HtmlArtifact, { props: { src: "<p>hi</p>", name: "x.html" } });
    const iframe = container.querySelector("iframe") as HTMLIFrameElement;
    const sent: unknown[] = [];
    vi.spyOn(iframe.contentWindow as Window, "postMessage").mockImplementation(((m: unknown) => { sent.push(m); }) as typeof window.postMessage);
    const id = idFromIframe(iframe);
    postHeight(id, 4000);
    await waitFor(() => expect(iframe.style.height).toBe("800px"));
    postHeight(id, 4100);
    // narrowed by its own scrollbar, just under the cap: still scrolling
    postHeight(id, 790);
    await new Promise((r) => setTimeout(r, 20));
    expect(iframe.style.height).toBe("800px");
    expect(sent.filter((m) => (m as { type?: string }).type === "wick-artifact-overflow")).toHaveLength(1);
  });

  test("a window resize re-fits the frame to the new cap", async () => {
    vi.stubGlobal("innerHeight", 1000);
    const { container } = render(HtmlArtifact, { props: { src: "<p>hi</p>", name: "x.html" } });
    const iframe = container.querySelector("iframe") as HTMLIFrameElement;
    postHeight(idFromIframe(iframe), 4000);
    await waitFor(() => expect(iframe.style.height).toBe("800px"));
    vi.stubGlobal("innerHeight", 2000);
    window.dispatchEvent(new Event("resize"));
    await waitFor(() => expect(iframe.style.height).toBe("1600px"));
  });

  test("a remount of the same artifact starts at its last height, not the default", async () => {
    vi.stubGlobal("innerHeight", 3000);
    const first = render(HtmlArtifact, { props: { src: "<p>same</p>", name: "x.html" } });
    const iframe = first.container.querySelector("iframe") as HTMLIFrameElement;
    postHeight(idFromIframe(iframe), 1500);
    await waitFor(() => expect(iframe.style.height).toBe("1500px"));
    first.unmount();
    const second = render(HtmlArtifact, { props: { src: "<p>same</p>", name: "x.html" } });
    expect((second.container.querySelector("iframe") as HTMLIFrameElement).style.height).toBe("1500px");
    // a different artifact still starts at the default
    const other = render(HtmlArtifact, { props: { src: "<p>other</p>", name: "y.html" } });
    expect((other.container.querySelector("iframe") as HTMLIFrameElement).style.height).toBe("320px");
  });

  // Review #3: read and write use the same key — the source currently
  // shown — so a streamed inline artifact remounted with its final source
  // starts at the height it settled on.
  test("memo key follows the streamed source, so the final remount recalls it", async () => {
    vi.stubGlobal("innerHeight", 3000);
    const host = document.createElement("div");
    host.setAttribute("data-html-src", "<p>a</p>");
    document.body.appendChild(host);
    const live = render(HtmlArtifact, { target: host, props: { src: "<p>a</p>", srcHost: host, name: "x.html" } });
    host.setAttribute("data-html-src", "<p>a</p><p>b</p>");
    await waitFor(() => expect((host.querySelector("iframe") as HTMLIFrameElement).srcdoc).toContain("<p>b</p>"));
    const iframe = host.querySelector("iframe") as HTMLIFrameElement;
    postHeight(idFromIframe(iframe), 1400);
    await waitFor(() => expect(iframe.style.height).toBe("1400px"));
    live.unmount();
    host.remove();
    const final = render(HtmlArtifact, { props: { src: "<p>a</p><p>b</p>", name: "x.html" } });
    expect((final.container.querySelector("iframe") as HTMLIFrameElement).style.height).toBe("1400px");
  });

  // Review #4: a hidden chat panel (clientHeight 0) must not shrink the
  // preview; the cap re-fits when the PANEL resizes.
  test("a hidden panel keeps the height; a panel resize re-fits the cap", async () => {
    const cbs: Array<() => void> = [];
    vi.stubGlobal("ResizeObserver", class { constructor(cb: () => void) { cbs.push(cb); } observe() {} disconnect() {} });
    const panel = document.createElement("div");
    panel.setAttribute("data-chat-panel", "");
    let ch = 0;
    Object.defineProperty(panel, "clientHeight", { configurable: true, get: () => ch });
    document.body.appendChild(panel);
    const host = document.createElement("div");
    panel.appendChild(host);
    render(HtmlArtifact, { target: host, props: { src: "<p>p</p>", name: "x.html" } });
    const iframe = host.querySelector("iframe") as HTMLIFrameElement;
    postHeight(idFromIframe(iframe), 4000);
    await new Promise((r) => setTimeout(r, 20));
    expect(iframe.style.height).toBe("320px");
    ch = 1000;
    cbs.forEach((cb) => cb());
    await waitFor(() => expect(iframe.style.height).toBe("800px"));
    ch = 500;
    cbs.forEach((cb) => cb());
    await waitFor(() => expect(iframe.style.height).toBe("400px"));
    panel.remove();
  });

  test("resizing while entirely above the visible thread keeps the reader's place", async () => {
    vi.stubGlobal("innerHeight", 3000);
    const panel = document.createElement("div");
    panel.setAttribute("data-chat-panel", "");
    document.body.appendChild(panel);
    // reader parked mid-thread, far from the bottom
    Object.defineProperty(panel, "scrollHeight", { configurable: true, value: 10000 });
    Object.defineProperty(panel, "clientHeight", { configurable: true, value: 2000 });
    panel.getBoundingClientRect = () => ({ top: 100, bottom: 2100 }) as DOMRect;
    let top = 5000;
    Object.defineProperty(panel, "scrollTop", { configurable: true, get: () => top, set: (v: number) => { top = v; } });
    const host = document.createElement("div");
    panel.appendChild(host);
    render(HtmlArtifact, { target: host, props: { src: "<p>above</p>", name: "x.html" } });
    const iframe = host.querySelector("iframe") as HTMLIFrameElement;
    iframe.getBoundingClientRect = () => ({ top: -500, bottom: 50 }) as DOMRect;
    postHeight(idFromIframe(iframe), 1000);
    await waitFor(() => expect(iframe.style.height).toBe("1000px"));
    await waitFor(() => expect(panel.scrollTop).toBe(5000 + (1000 - 320)));
    panel.remove();
  });

  test("Show code toggles raw source then back to preview", async () => {
    const { container } = render(HtmlArtifact, { props: { src: "<p>RAWMARK</p>", name: "x.html" } });
    await fireEvent.click(screen.getByRole("button", { name: "Actions for x.html" }));
    await fireEvent.click(screen.getByRole("menuitem", { name: "Show code" }));
    expect(container.querySelector("pre")?.textContent).toContain("RAWMARK");
    await fireEvent.click(screen.getByRole("button", { name: "Actions for x.html" }));
    await fireEvent.click(screen.getByRole("menuitem", { name: "Show preview" }));
    expect(container.querySelector("pre")).toBeNull();
    expect(container.querySelector("iframe")).not.toBeNull();
  });

  test("updates the preview when the streaming host's data-html-src grows", async () => {
    const host = document.createElement("div");
    host.setAttribute("data-html-src", "<p>one</p>");
    document.body.appendChild(host);
    render(HtmlArtifact, { target: host, props: { src: "<p>one</p>", srcHost: host, name: "x.html" } });
    expect((host.querySelector("iframe") as HTMLIFrameElement).srcdoc).toContain("one");
    // Simulate a streaming token growing the source on the host attribute.
    host.setAttribute("data-html-src", "<p>one</p><p>two</p>");
    await waitFor(() => expect((host.querySelector("iframe") as HTMLIFrameElement).srcdoc).toContain("two"));
    host.remove();
  });

  test("Reload menu item re-fetches a url-backed preview with fresh bytes", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce({ text: () => Promise.resolve("<p>old</p>") })
      .mockResolvedValueOnce({ text: () => Promise.resolve("<p>new</p>") });
    vi.stubGlobal("fetch", fetchMock);
    const { container } = render(HtmlArtifact, { props: { url: "/raw?path=a.html", name: "a.html" } });
    await waitFor(() => expect((container.querySelector("iframe") as HTMLIFrameElement).srcdoc).toContain("old"));
    await fireEvent.click(screen.getByRole("button", { name: "Actions for a.html" }));
    await fireEvent.click(screen.getByRole("menuitem", { name: "Reload" }));
    await waitFor(() => expect((container.querySelector("iframe") as HTMLIFrameElement).srcdoc).toContain("new"));
    // second call bypasses the HTTP cache
    expect(fetchMock).toHaveBeenLastCalledWith("/raw?path=a.html", { cache: "no-store" });
  });

  test("Reload remounts the iframe even when refetched bytes are identical", async () => {
    // Same bytes both times: a plain srcdoc reassign wouldn't re-run the
    // artifact's scripts, so reload must remount the iframe (fresh element).
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ text: () => Promise.resolve("<p>same</p>") }));
    const { container } = render(HtmlArtifact, { props: { url: "/raw?path=a.html", name: "a.html" } });
    await waitFor(() => expect(container.querySelector("iframe")).not.toBeNull());
    const before = container.querySelector("iframe");
    await fireEvent.click(screen.getByRole("button", { name: "Actions for a.html" }));
    await fireEvent.click(screen.getByRole("menuitem", { name: "Reload" }));
    await waitFor(() => expect(container.querySelector("iframe")).not.toBe(before));
  });

  test("Reload is absent for inline (src) artifacts — nothing to re-fetch", async () => {
    render(HtmlArtifact, { props: { src: "<p>hi</p>", name: "x.html" } });
    await fireEvent.click(screen.getByRole("button", { name: "Actions for x.html" }));
    expect(screen.queryByRole("menuitem", { name: "Reload" })).toBeNull();
  });

  test("Full screen opens an overlay with a larger iframe; Escape closes", async () => {
    render(HtmlArtifact, { props: { src: "<p>hi</p>", name: "x.html" } });
    await fireEvent.click(screen.getByRole("button", { name: "Actions for x.html" }));
    await fireEvent.click(screen.getByRole("menuitem", { name: "Full screen" }));
    expect(screen.getByRole("button", { name: "Close preview" })).toBeTruthy();
    await fireEvent.keyDown(window, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("button", { name: "Close preview" })).toBeNull());
  });
});

/* The widget CSP arrives with session meta, which can land after artifacts
   have already mounted. These pin that the late policy is actually picked up
   rather than leaving the widget stuck on the blocked fallback. */
describe("HtmlArtifact — widget CSP policy", () => {
  afterEach(() => setWidgetPolicy(BLOCKED_WIDGET_POLICY));

  const iframeOf = (c: HTMLElement) => c.querySelector("iframe") as HTMLIFrameElement;

  test("sandbox is allow-scripts only under the default blocked policy", () => {
    const { container } = render(HtmlArtifact, { props: { src: "<p>hi</p>", name: "x.html" } });
    expect(iframeOf(container).getAttribute("sandbox")).toBe("allow-scripts");
  });

  test("sandbox gains allow-popups when the policy permits popups", () => {
    setWidgetPolicy({ allow_popups: true });
    const { container } = render(HtmlArtifact, { props: { src: "<p>hi</p>", name: "x.html" } });
    expect(iframeOf(container).getAttribute("sandbox")).toBe("allow-scripts allow-popups");
  });

  test("srcdoc carries the CSP of the policy in effect at mount", () => {
    setWidgetPolicy({ frame_src: "list", allowlist: ["https://maps.google.com"] });
    const { container } = render(HtmlArtifact, { props: { src: "<p>hi</p>", name: "x.html" } });
    expect(iframeOf(container).srcdoc).toContain("frame-src https://maps.google.com");
  });

  test("a policy arriving after mount rebuilds the srcdoc and remounts", async () => {
    const { container } = render(HtmlArtifact, { props: { src: "<p>hi</p>", name: "x.html" } });
    expect(iframeOf(container).srcdoc).toContain("frame-src 'none'");
    const before = iframeOf(container);

    setWidgetPolicy({ frame_src: "list", allow_popups: true, allowlist: ["https://maps.google.com"] });

    await waitFor(() => {
      const el = iframeOf(container);
      expect(el.srcdoc).toContain("frame-src https://maps.google.com");
      // remounted, so the artifact's inline scripts re-run under the new CSP
      expect(el).not.toBe(before);
      expect(el.getAttribute("sandbox")).toBe("allow-scripts allow-popups");
    });
  });

  test("an identical policy does not remount the iframe", async () => {
    const { container } = render(HtmlArtifact, { props: { src: "<p>hi</p>", name: "x.html" } });
    const before = iframeOf(container);
    setWidgetPolicy({ ...BLOCKED_WIDGET_POLICY });
    await Promise.resolve();
    expect(iframeOf(container)).toBe(before);
  });
});
