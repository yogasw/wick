import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import SwitchModal from "../SwitchModal.svelte";

const ITEMS = [
  { id: null, label: "— no project —", current: true },
  { id: "a", label: "Alpha" },
  { id: "b", label: "Beta", hint: "pinned" },
];

describe("SwitchModal", () => {
  test("renders nothing when closed", () => {
    const { container } = render(SwitchModal, {
      props: { open: false, title: "Switch project", items: ITEMS, onSelect: vi.fn(), onClose: vi.fn() },
    });
    expect(container.querySelector("[data-switch-popup]")).toBeNull();
  });

  test("renders the title and items when open", () => {
    render(SwitchModal, {
      props: { open: true, title: "Switch project", items: ITEMS, onSelect: vi.fn(), onClose: vi.fn() },
    });
    expect(screen.getByText("Switch project")).toBeDefined();
    expect(screen.getByText("Alpha")).toBeDefined();
    expect(screen.getByText("Beta")).toBeDefined();
  });

  test("clicking an item calls onSelect with its id and closes", async () => {
    const onSelect = vi.fn();
    const onClose = vi.fn();
    render(SwitchModal, {
      props: { open: true, title: "Switch project", items: ITEMS, onSelect, onClose },
    });
    await fireEvent.click(screen.getByText("Alpha"));
    expect(onSelect).toHaveBeenCalledWith("a");
    expect(onClose).toHaveBeenCalledOnce();
  });

  test("the current item is disabled — clicking it does nothing", async () => {
    const onSelect = vi.fn();
    const onClose = vi.fn();
    render(SwitchModal, {
      props: { open: true, title: "Switch project", items: ITEMS, onSelect, onClose },
    });
    await fireEvent.click(screen.getByText("— no project —"));
    expect(onSelect).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled(); // disabled button — no-op
  });

  test("arrow keys + Enter select an item (skipping the current one)", async () => {
    const onSelect = vi.fn();
    const onClose = vi.fn();
    render(SwitchModal, {
      props: { open: true, title: "Switch project", items: ITEMS, onSelect, onClose },
    });
    // Opens highlighted on "Alpha" (first non-current). Enter picks it.
    await fireEvent.keyDown(window, { key: "Enter" });
    expect(onSelect).toHaveBeenCalledWith("a");
    expect(onClose).toHaveBeenCalledOnce();
  });

  test("ArrowDown moves the highlight before selecting", async () => {
    const onSelect = vi.fn();
    render(SwitchModal, {
      props: { open: true, title: "Switch project", items: ITEMS, onSelect, onClose: vi.fn() },
    });
    await fireEvent.keyDown(window, { key: "ArrowDown" }); // Alpha → Beta
    await fireEvent.keyDown(window, { key: "Enter" });
    expect(onSelect).toHaveBeenCalledWith("b");
  });

  test("Escape closes the modal", async () => {
    const onClose = vi.fn();
    render(SwitchModal, {
      props: { open: true, title: "Switch provider", items: ITEMS, onSelect: vi.fn(), onClose },
    });
    await fireEvent.keyDown(window, { key: "Escape" });
    expect(onClose).toHaveBeenCalledOnce();
  });

  test("clicking outside the popup closes it", async () => {
    const onClose = vi.fn();
    render(SwitchModal, {
      props: { open: true, title: "Switch provider", items: ITEMS, onSelect: vi.fn(), onClose },
    });
    await fireEvent.mouseDown(document.body);
    expect(onClose).toHaveBeenCalledOnce();
  });
});
