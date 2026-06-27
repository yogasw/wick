import { describe, test, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import MediaLightbox from "../MediaLightbox.svelte";

const img = { url: "/raw?path=a.png", name: "a.png", kind: "image" as const };
/* single-item helpers mirror the old `item`-based call sites: a one-element
   gallery opened at index 0. */
const one = (it: typeof img | { url: string; name: string; kind: "pdf" | "html" | "markdown" | "text" | "file" }) => ({
  items: [it],
  index: 0,
});

function mockFetchText(body: string) {
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ text: () => Promise.resolve(body) }));
}
afterEach(() => vi.unstubAllGlobals());

describe("MediaLightbox", () => {
  test("renders an img for image kind", () => {
    const { container } = render(MediaLightbox, { props: { ...one(img), onClose: () => {} } });
    expect(container.querySelector("img")).not.toBeNull();
  });

  test("renders an iframe for pdf kind", () => {
    const { container } = render(MediaLightbox, {
      props: { ...one({ url: "/raw?path=a.pdf", name: "a.pdf", kind: "pdf" }), onClose: () => {} },
    });
    expect(container.querySelector("iframe")).not.toBeNull();
  });

  test("zoom in increases scale, reset returns to 1", async () => {
    const { container } = render(MediaLightbox, { props: { ...one(img), onClose: () => {} } });
    const media = container.querySelector("[data-lightbox-media]") as HTMLElement;
    const start = media.style.transform;
    await fireEvent.click(screen.getByLabelText("Zoom in"));
    expect(media.style.transform).not.toBe(start);
    await fireEvent.click(screen.getByLabelText("Reset zoom"));
    expect(media.style.transform).toContain("scale(1)");
  });

  test("Escape calls onClose", async () => {
    const onClose = vi.fn();
    render(MediaLightbox, { props: { ...one(img), onClose } });
    await fireEvent.keyDown(window, { key: "Escape" });
    expect(onClose).toHaveBeenCalled();
  });

  test("clicking the empty area around the image closes the viewer", async () => {
    const onClose = vi.fn();
    const { container } = render(MediaLightbox, { props: { ...one(img), onClose } });
    /* the media stage is the flex container that holds the <img>; clicking it
       directly (not the image) is a bare-backdrop click → close */
    const stage = container.querySelector("[data-lightbox-media]")!.parentElement as HTMLElement;
    await fireEvent.click(stage);
    expect(onClose).toHaveBeenCalled();
  });

  test("clicking the image itself does NOT close the viewer", async () => {
    const onClose = vi.fn();
    const { container } = render(MediaLightbox, { props: { ...one(img), onClose } });
    await fireEvent.click(container.querySelector("[data-lightbox-media]") as HTMLElement);
    expect(onClose).not.toHaveBeenCalled();
  });

  test("renders nothing when items is null", () => {
    const { container } = render(MediaLightbox, { props: { items: null, index: 0, onClose: () => {} } });
    expect(container.querySelector("[data-lightbox-media]")).toBeNull();
  });

  test("markdown kind fetches content and renders a doc panel (no zoom controls)", async () => {
    mockFetchText("# Title\n\nbody");
    const { container } = render(MediaLightbox, {
      props: { ...one({ url: "/raw?path=p.md", name: "p.md", kind: "markdown" }), onClose: () => {} },
    });
    await waitFor(() => expect(container.querySelector("[data-lightbox-doc]")).not.toBeNull());
    expect(container.querySelector("[data-lightbox-doc]")?.textContent).toContain("Title");
    expect(screen.queryByLabelText("Zoom in")).toBeNull();
  });

  test("text kind renders raw content in a pre block", async () => {
    mockFetchText("plain log line");
    const { container } = render(MediaLightbox, {
      props: { ...one({ url: "/raw?path=a.log", name: "a.log", kind: "text" }), onClose: () => {} },
    });
    await waitFor(() => expect(container.querySelector("[data-lightbox-doc] pre")?.textContent).toContain("plain log line"));
  });

  test("html kind renders an iframe with the fetched srcdoc", async () => {
    mockFetchText("<p>hi</p>");
    const { container } = render(MediaLightbox, {
      props: { ...one({ url: "/raw?path=p.html", name: "p.html", kind: "html" }), onClose: () => {} },
    });
    await waitFor(() => expect(container.querySelector("iframe[srcdoc]")).not.toBeNull());
  });

  describe("carousel", () => {
    const gallery = [
      { url: "https://abc.com/1.jpg", name: "First", kind: "image" as const },
      { url: "https://abc.net/2.jpg", name: "Second", kind: "image" as const },
      { url: "https://abc.org/3.jpg", name: "Third", kind: "image" as const },
    ];

    test("single item shows no prev/next controls", () => {
      render(MediaLightbox, { props: { ...one(img), onClose: () => {} } });
      expect(screen.queryByLabelText("Previous image")).toBeNull();
      expect(screen.queryByLabelText("Next image")).toBeNull();
    });

    test("multi item shows a position counter and prev/next", () => {
      const { container } = render(MediaLightbox, { props: { items: gallery, index: 0, onClose: () => {} } });
      expect(screen.getByLabelText("Next image")).not.toBeNull();
      expect(screen.getByLabelText("Previous image")).not.toBeNull();
      expect(container.querySelector("[data-lightbox-counter]")?.textContent).toContain("1 / 3");
      expect(container.querySelector("img")?.getAttribute("src")).toBe("https://abc.com/1.jpg");
    });

    test("opens at the clicked index", () => {
      const { container } = render(MediaLightbox, { props: { items: gallery, index: 2, onClose: () => {} } });
      expect(container.querySelector("[data-lightbox-counter]")?.textContent).toContain("3 / 3");
      expect(container.querySelector("img")?.getAttribute("src")).toBe("https://abc.org/3.jpg");
    });

    test("Next advances, Previous goes back, both wrap around", async () => {
      const { container } = render(MediaLightbox, { props: { items: gallery, index: 0, onClose: () => {} } });
      const counter = () => container.querySelector("[data-lightbox-counter]")?.textContent;
      await fireEvent.click(screen.getByLabelText("Next image"));
      expect(counter()).toContain("2 / 3");
      expect(container.querySelector("img")?.getAttribute("src")).toBe("https://abc.net/2.jpg");
      /* wrap forward: 3 → 1 */
      await fireEvent.click(screen.getByLabelText("Next image"));
      await fireEvent.click(screen.getByLabelText("Next image"));
      expect(counter()).toContain("1 / 3");
      /* wrap backward: 1 → 3 */
      await fireEvent.click(screen.getByLabelText("Previous image"));
      expect(counter()).toContain("3 / 3");
    });

    test("ArrowRight / ArrowLeft navigate", async () => {
      const { container } = render(MediaLightbox, { props: { items: gallery, index: 0, onClose: () => {} } });
      await fireEvent.keyDown(window, { key: "ArrowRight" });
      expect(container.querySelector("[data-lightbox-counter]")?.textContent).toContain("2 / 3");
      await fireEvent.keyDown(window, { key: "ArrowLeft" });
      expect(container.querySelector("[data-lightbox-counter]")?.textContent).toContain("1 / 3");
    });

    test("shows the source domain caption when an item carries a sourceUrl", () => {
      const items = [{ url: "https://img.cdn/x.jpg", name: "pic", kind: "image" as const, sourceUrl: "https://pinterest.com/pin/1" }];
      const { container } = render(MediaLightbox, { props: { items, index: 0, onClose: () => {} } });
      expect(container.querySelector("[data-lightbox-source]")?.textContent).toContain("pinterest.com");
    });
  });
});
