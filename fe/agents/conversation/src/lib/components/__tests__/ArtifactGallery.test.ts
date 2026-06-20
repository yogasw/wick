import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
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

  test("file artifact renders a doc card with an actions menu", () => {
    render(ArtifactGallery, {
      props: { artifacts: [art({ name: "x.zip", path: "x.zip", kind: "file", download_url: "/dl?path=x.zip" })], onOpen: () => {} },
    });
    expect(screen.getByText("x.zip")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Actions for x.zip" })).toBeTruthy();
  });

  test("markdown artifact renders a doc card with a Full screen action in its menu", async () => {
    render(ArtifactGallery, {
      props: { artifacts: [art({ name: "PLAN.md", path: "PLAN.md", kind: "markdown", download_url: "/dl?path=PLAN.md" })], onOpen: () => {} },
    });
    expect(screen.getByText("Document · MD")).toBeTruthy();
    await fireEvent.click(screen.getByRole("button", { name: "Actions for PLAN.md" }));
    expect(screen.getByRole("menuitem", { name: "Full screen" })).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: "Download" })).toBeTruthy();
  });

  test("Full screen on a markdown artifact calls onOpen with kind markdown", async () => {
    const onOpen = vi.fn();
    render(ArtifactGallery, {
      props: { artifacts: [art({ name: "PLAN.md", path: "PLAN.md", kind: "markdown", url: "/raw?path=PLAN.md" })], onOpen },
    });
    await fireEvent.click(screen.getByRole("button", { name: "Actions for PLAN.md" }));
    await fireEvent.click(screen.getByRole("menuitem", { name: "Full screen" }));
    expect(onOpen).toHaveBeenCalledWith(expect.objectContaining({ kind: "markdown", url: "/raw?path=PLAN.md" }));
  });

  test("html artifact delegates to the shared HtmlArtifact preview (own ⋮ menu)", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({ text: () => Promise.resolve("<p>hi</p>") }));
    const { container } = render(ArtifactGallery, {
      props: { artifacts: [art({ name: "p.html", path: "p.html", kind: "html", url: "/raw?path=p.html" })], onOpen: () => {} },
    });
    expect(container.querySelector("[data-html-artifact]")).not.toBeNull();
    // The HtmlArtifact component owns the actions menu, keyed by the artifact name.
    expect(screen.getByRole("button", { name: "Actions for p.html" })).toBeTruthy();
    vi.unstubAllGlobals();
  });
});
