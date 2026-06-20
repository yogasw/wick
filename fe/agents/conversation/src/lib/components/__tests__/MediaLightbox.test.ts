import { describe, test, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import MediaLightbox from "../MediaLightbox.svelte";

const img = { url: "/raw?path=a.png", name: "a.png", kind: "image" as const };

function mockFetchText(body: string) {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ text: () => Promise.resolve(body) }));
}
afterEach(() => vi.unstubAllGlobals());

describe("MediaLightbox", () => {
  test("renders an img for image kind", () => {
    const { container } = render(MediaLightbox, { props: { item: img, onClose: () => {} } });
    expect(container.querySelector("img")).not.toBeNull();
  });

  test("renders an iframe for pdf kind", () => {
    const { container } = render(MediaLightbox, {
      props: { item: { url: "/raw?path=a.pdf", name: "a.pdf", kind: "pdf" }, onClose: () => {} },
    });
    expect(container.querySelector("iframe")).not.toBeNull();
  });

  test("zoom in increases scale, reset returns to 1", async () => {
    const { container } = render(MediaLightbox, { props: { item: img, onClose: () => {} } });
    const media = container.querySelector("[data-lightbox-media]") as HTMLElement;
    const start = media.style.transform;
    await fireEvent.click(screen.getByLabelText("Zoom in"));
    expect(media.style.transform).not.toBe(start);
    await fireEvent.click(screen.getByLabelText("Reset zoom"));
    expect(media.style.transform).toContain("scale(1)");
  });

  test("Escape calls onClose", async () => {
    const onClose = vi.fn();
    render(MediaLightbox, { props: { item: img, onClose } });
    await fireEvent.keyDown(window, { key: "Escape" });
    expect(onClose).toHaveBeenCalled();
  });

  test("renders nothing when item is null", () => {
    const { container } = render(MediaLightbox, { props: { item: null, onClose: () => {} } });
    expect(container.querySelector("[data-lightbox-media]")).toBeNull();
  });

  test("markdown kind fetches content and renders a doc panel (no zoom controls)", async () => {
    mockFetchText("# Title\n\nbody");
    const { container } = render(MediaLightbox, {
      props: { item: { url: "/raw?path=p.md", name: "p.md", kind: "markdown" }, onClose: () => {} },
    });
    await waitFor(() => expect(container.querySelector("[data-lightbox-doc]")).not.toBeNull());
    expect(container.querySelector("[data-lightbox-doc]")?.textContent).toContain("Title");
    expect(screen.queryByLabelText("Zoom in")).toBeNull();
  });

  test("text kind renders raw content in a pre block", async () => {
    mockFetchText("plain log line");
    const { container } = render(MediaLightbox, {
      props: { item: { url: "/raw?path=a.log", name: "a.log", kind: "text" }, onClose: () => {} },
    });
    await waitFor(() => expect(container.querySelector("[data-lightbox-doc] pre")?.textContent).toContain("plain log line"));
  });

  test("html kind renders an iframe with the fetched srcdoc", async () => {
    mockFetchText("<p>hi</p>");
    const { container } = render(MediaLightbox, {
      props: { item: { url: "/raw?path=p.html", name: "p.html", kind: "html" }, onClose: () => {} },
    });
    await waitFor(() => expect(container.querySelector("iframe[srcdoc]")).not.toBeNull());
  });
});
