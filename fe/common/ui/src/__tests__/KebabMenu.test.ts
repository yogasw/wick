import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import KebabMenu from "../KebabMenu.svelte";

describe("KebabMenu", () => {
  test("opens on trigger click and renders items", async () => {
    const onclick = vi.fn();
    render(KebabMenu, { props: { items: [{ label: "Edit", onclick }] } });
    expect(screen.queryByRole("menuitem", { name: "Edit" })).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: "Actions" }));
    expect(screen.getByRole("menuitem", { name: "Edit" })).toBeTruthy();
  });

  test("running an item fires its callback and closes the menu", async () => {
    const onclick = vi.fn();
    render(KebabMenu, { props: { items: [{ label: "Edit", onclick }] } });
    await fireEvent.click(screen.getByRole("button", { name: "Actions" }));
    await fireEvent.click(screen.getByRole("menuitem", { name: "Edit" }));
    expect(onclick).toHaveBeenCalledOnce();
    expect(screen.queryByRole("menuitem", { name: "Edit" })).toBeNull();
  });

  test("a disabled item does not fire", async () => {
    const onclick = vi.fn();
    render(KebabMenu, { props: { items: [{ label: "Edit", onclick, disabled: true }] } });
    await fireEvent.click(screen.getByRole("button", { name: "Actions" }));
    await fireEvent.click(screen.getByRole("menuitem", { name: "Edit" }));
    expect(onclick).not.toHaveBeenCalled();
  });

  test("a custom ariaLabel labels the trigger", async () => {
    render(KebabMenu, { props: { items: [{ label: "X", onclick: vi.fn() }], ariaLabel: "Actions for Prod" } });
    expect(screen.getByRole("button", { name: "Actions for Prod" })).toBeTruthy();
  });

  test("opening a second menu closes the first (one open at a time)", async () => {
    render(KebabMenu, { props: { items: [{ label: "A1", onclick: vi.fn() }], ariaLabel: "menu-a" } });
    render(KebabMenu, { props: { items: [{ label: "B1", onclick: vi.fn() }], ariaLabel: "menu-b" } });
    await fireEvent.click(screen.getByRole("button", { name: "menu-a" }));
    expect(screen.getByRole("menuitem", { name: "A1" })).toBeTruthy();
    await fireEvent.click(screen.getByRole("button", { name: "menu-b" }));
    expect(screen.getByRole("menuitem", { name: "B1" })).toBeTruthy();
    expect(screen.queryByRole("menuitem", { name: "A1" })).toBeNull();
  });
});
