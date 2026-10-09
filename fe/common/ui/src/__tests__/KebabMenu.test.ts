import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import KebabMenu from "../KebabMenu.svelte";

/* These exercise the menu directly rather than through FileBrowserNode, so
   the menu's own behaviour is pinned down independently of any row that
   hosts it. */

async function open() {
  await fireEvent.click(screen.getByRole("button", { name: "Actions" }));
}

describe("KebabMenu", () => {
  test("runs a plain item and closes", async () => {
    const onclick = vi.fn();
    render(KebabMenu, { props: { items: [{ label: "Delete", onclick }] } });
    await open();
    await fireEvent.click(screen.getByRole("menuitem", { name: "Delete" }));
    expect(onclick).toHaveBeenCalled();
    expect(screen.queryByRole("menuitem")).toBeNull();
  });

  test("a detail line shows the value a row would act on", async () => {
    render(KebabMenu, { props: { items: [{ label: "Full path", detail: "/abs/x.ts", onclick: vi.fn() }] } });
    await open();
    expect(screen.getByTitle("/abs/x.ts")).toBeDefined();
  });

  /* The submenu replaces the popup instead of flying out beside it: the rail
     it lives in is ~300px wide, so a flyout would open off-screen. */
  test("a submenu swaps the rows in place and Back returns", async () => {
    const copy = vi.fn();
    render(KebabMenu, {
      props: {
        items: [
          { label: "Download", onclick: vi.fn() },
          { label: "Copy path", submenu: [{ label: "Full path", detail: "/abs/x.ts", onclick: copy }] },
        ],
      },
    });
    await open();
    await fireEvent.click(screen.getByRole("menuitem", { name: /Copy path/ }));
    // Swapped: the parent rows are gone, the child's are here.
    expect(screen.queryByRole("menuitem", { name: "Download" })).toBeNull();
    expect(screen.getByRole("menuitem", { name: /Full path/ })).toBeDefined();
    // Back is the header row, labelled with where you are.
    await fireEvent.click(screen.getByRole("button", { name: /Copy path/ }));
    expect(screen.getByRole("menuitem", { name: "Download" })).toBeDefined();
  });

  /* keepOpen is what makes "copy one flavour, then another" one click instead
     of four. */
  test("a keepOpen row runs without closing and reports in place", async () => {
    const copy = vi.fn();
    render(KebabMenu, {
      props: {
        items: [
          {
            label: "Copy path",
            submenu: [
              { label: "Full path", detail: "/abs/x.ts", keepOpen: true, doneLabel: "Copied", onclick: copy },
              { label: "Session path", detail: "x.ts", keepOpen: true, doneLabel: "Copied", onclick: vi.fn() },
            ],
          },
        ],
      },
    });
    await open();
    await fireEvent.click(screen.getByRole("menuitem", { name: /Copy path/ }));
    await fireEvent.click(screen.getByRole("menuitem", { name: /Full path/ }));
    expect(copy).toHaveBeenCalled();
    expect(screen.getByText("Copied")).toBeDefined();
    // Still open, so the second flavour is one click away.
    expect(screen.getByRole("menuitem", { name: /Session path/ })).toBeDefined();
  });

  test("a disabled row does nothing", async () => {
    const onclick = vi.fn();
    render(KebabMenu, { props: { items: [{ label: "Nope", disabled: true, onclick }] } });
    await open();
    await fireEvent.click(screen.getByRole("menuitem", { name: "Nope" }));
    expect(onclick).not.toHaveBeenCalled();
  });
});
