import { describe, test, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import HtmlArtifact from "../HtmlArtifact.svelte";

afterEach(() => vi.unstubAllGlobals());

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
    const { container } = render(HtmlArtifact, { props: { src: "<p>hi</p>", name: "x.html" } });
    const iframe = container.querySelector("iframe") as HTMLIFrameElement;
    const id = idFromIframe(iframe);
    expect(id).not.toBe("");
    window.dispatchEvent(new MessageEvent("message", { data: { type: "wick-artifact-height", id, height: 742 } }));
    await waitFor(() => expect(iframe.style.height).toBe("742px"));
  });

  test("caps absurd heights", async () => {
    const { container } = render(HtmlArtifact, { props: { src: "<p>hi</p>", name: "x.html" } });
    const iframe = container.querySelector("iframe") as HTMLIFrameElement;
    const id = idFromIframe(iframe);
    window.dispatchEvent(new MessageEvent("message", { data: { type: "wick-artifact-height", id, height: 99999 } }));
    await waitFor(() => expect(iframe.style.height).toBe("2400px"));
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

  test("Full screen opens an overlay with a larger iframe; Escape closes", async () => {
    render(HtmlArtifact, { props: { src: "<p>hi</p>", name: "x.html" } });
    await fireEvent.click(screen.getByRole("button", { name: "Actions for x.html" }));
    await fireEvent.click(screen.getByRole("menuitem", { name: "Full screen" }));
    expect(screen.getByRole("button", { name: "Close preview" })).toBeTruthy();
    await fireEvent.keyDown(window, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("button", { name: "Close preview" })).toBeNull());
  });
});
