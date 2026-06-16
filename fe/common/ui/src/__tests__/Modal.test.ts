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
});
