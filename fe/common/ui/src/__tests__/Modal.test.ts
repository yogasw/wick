import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import { createRawSnippet } from "svelte";
import Modal from "../Modal.svelte";

const body = createRawSnippet(() => ({ render: () => `<p>body content</p>` }));

describe("Modal", () => {
  test("not rendered when open=false", () => {
    render(Modal, { props: { open: false, onClose: vi.fn(), children: body } });
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  test("renders title + children when open", () => {
    render(Modal, { props: { open: true, title: "Hello", onClose: vi.fn(), children: body } });
    expect(screen.getByRole("dialog")).toBeTruthy();
    expect(screen.getByText("Hello")).toBeTruthy();
    expect(screen.getByText("body content")).toBeTruthy();
  });

  test("Escape calls onClose", async () => {
    const onClose = vi.fn();
    render(Modal, { props: { open: true, onClose, children: body } });
    await fireEvent.keyDown(window, { key: "Escape" });
    expect(onClose).toHaveBeenCalled();
  });

  test("close button calls onClose", async () => {
    const onClose = vi.fn();
    render(Modal, { props: { open: true, onClose, children: body } });
    await fireEvent.click(screen.getByLabelText("Close"));
    expect(onClose).toHaveBeenCalled();
  });

  test("backdrop click closes; panel click does not", async () => {
    const onClose = vi.fn();
    const { container } = render(Modal, { props: { open: true, onClose, children: body } });
    await fireEvent.click(screen.getByRole("dialog"));
    expect(onClose).not.toHaveBeenCalled();
    const backdrop = container.querySelector("[role='presentation']") as HTMLElement;
    await fireEvent.click(backdrop);
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  test("footer snippet renders", () => {
    const footer = createRawSnippet(() => ({ render: () => `<button>OK</button>` }));
    render(Modal, { props: { open: true, onClose: vi.fn(), children: body, footer } });
    expect(screen.getByText("OK")).toBeTruthy();
  });

  /* What makes it a modal rather than a div that looks like one: it takes
     focus, gives it back, holds the page still, and — when dialogs nest —
     lets only the innermost one answer Escape. */

  test("opening takes focus and closing hands it back", async () => {
    const opener = document.createElement("button");
    document.body.appendChild(opener);
    opener.focus();
    expect(document.activeElement).toBe(opener);

    const { rerender } = render(Modal, { props: { open: false, onClose: vi.fn(), children: body } });
    await rerender({ open: true, onClose: vi.fn(), children: body });
    expect(screen.getByRole("dialog").contains(document.activeElement)).toBe(true);

    await rerender({ open: false, onClose: vi.fn(), children: body });
    expect(document.activeElement).toBe(opener);
    opener.remove();
  });

  test("the page behind does not scroll while it is open", async () => {
    const { rerender } = render(Modal, { props: { open: false, onClose: vi.fn(), children: body } });
    expect(document.body.style.overflow).toBe("");
    await rerender({ open: true, onClose: vi.fn(), children: body });
    expect(document.body.style.overflow).toBe("hidden");
    await rerender({ open: false, onClose: vi.fn(), children: body });
    expect(document.body.style.overflow).toBe("");
  });

  // Every open modal puts a keydown handler on the window, so before this one
  // keypress reached all of them: an outer dialog would open a confirmation
  // and the confirmation would close itself in the same dispatch.
  test("Escape closes only the innermost dialog", async () => {
    const outer = vi.fn();
    const inner = vi.fn();
    render(Modal, { props: { open: true, onClose: outer, children: body } });
    const nested = render(Modal, { props: { open: true, onClose: inner, children: body } });
    await Promise.resolve();

    await fireEvent.keyDown(window, { key: "Escape" });
    expect(inner).toHaveBeenCalledTimes(1);
    expect(outer).not.toHaveBeenCalled();

    // With the inner one gone, the outer answers again.
    nested.unmount();
    await fireEvent.keyDown(window, { key: "Escape" });
    expect(outer).toHaveBeenCalledTimes(1);
  });

  test("a closed modal leaves the page scrollable even if torn down while open", () => {
    const { unmount } = render(Modal, { props: { open: true, onClose: vi.fn(), children: body } });
    unmount();
    expect(document.body.style.overflow).toBe("");
  });

  // 2xl is for reading a page of prose, which 4xl left as a strip on a wide
  // screen (Yoga, 2026-09-26: "kecil bet gini").
  test("2xl is the widest size, for reading rather than for a form", () => {
    render(Modal, { props: { open: true, onClose: vi.fn(), children: body, size: "2xl" as const } });
    expect(screen.getByRole("dialog").className).toContain("max-w-6xl");
  });
});
