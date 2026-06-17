import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import ArtifactGallery from "../ArtifactGallery.svelte";
import type { Artifact } from "../../types/agents.js";

function art(over: Partial<Artifact> = {}): Artifact {
  return { name: "a.png", path: "a.png", url: "/raw?path=a.png", download_url: "/dl?path=a.png", kind: "image", ...over };
}

describe("ArtifactGallery", () => {
  test("grid layout when <= 4 items", () => {
    const items = [art(), art({ name: "b.png", path: "b.png" })];
    const { container } = render(ArtifactGallery, { props: { artifacts: items, onOpen: () => {} } });
    expect(container.querySelector("[data-gallery-grid]")).not.toBeNull();
    expect(container.querySelector("[data-gallery-carousel]")).toBeNull();
  });

  test("carousel layout when > 4 items", () => {
    const items = Array.from({ length: 5 }, (_, i) => art({ name: `n${i}.png`, path: `n${i}.png` }));
    const { container } = render(ArtifactGallery, { props: { artifacts: items, onOpen: () => {} } });
    expect(container.querySelector("[data-gallery-carousel]")).not.toBeNull();
  });

  test("clicking an image artifact calls onOpen with the item", async () => {
    const onOpen = vi.fn();
    render(ArtifactGallery, { props: { artifacts: [art()], onOpen } });
    await fireEvent.click(screen.getByRole("button", { name: /a\.png/ }));
    expect(onOpen).toHaveBeenCalledWith(expect.objectContaining({ kind: "image", url: "/raw?path=a.png" }));
  });

  test("file artifact renders a download link", () => {
    const { container } = render(ArtifactGallery, {
      props: { artifacts: [art({ name: "x.zip", path: "x.zip", kind: "file", download_url: "/dl?path=x.zip" })], onOpen: () => {} },
    });
    const a = container.querySelector('a[href="/dl?path=x.zip"]') as HTMLAnchorElement;
    expect(a).not.toBeNull();
  });
});
