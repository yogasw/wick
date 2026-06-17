import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import MediaLightbox from "../MediaLightbox.svelte";

const img = { url: "/raw?path=a.png", name: "a.png", kind: "image" as const };

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
});
