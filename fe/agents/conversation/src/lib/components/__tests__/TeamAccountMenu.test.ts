import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import TeamAccountMenu from "../TeamAccountMenu.svelte";

const theme = { mode: "light" as const, light: "github-light", dark: "github-dark" };

function mount(over: Record<string, unknown> = {}) {
  const onSettings = vi.fn();
  render(TeamAccountMenu, {
    props: { viewerName: "Yoga Setiawan", exitHref: "/tools/agents/sessions", theme, onSettings, ...over },
  });
  return { onSettings, button: screen.getByRole("button", { name: "Account menu" }) };
}

describe("TeamAccountMenu", () => {
  test("closed by default; the row shows initial and name and announces a menu", () => {
    const { button } = mount();
    expect(button.getAttribute("aria-haspopup")).toBe("menu");
    expect(button.getAttribute("aria-expanded")).toBe("false");
    expect(button.textContent).toContain("Y");
    expect(button.textContent).toContain("Yoga Setiawan");
    expect(screen.queryByRole("menu")).toBeNull();
  });

  test("the ↩ beside the name goes back to Agents in one click", () => {
    mount();
    const back = screen.getByRole("link", { name: "Back to Agents" });
    expect(back.getAttribute("href")).toBe("/tools/agents/sessions");
    expect(back.getAttribute("title")).toBe("Back to Agents");
  });

  test("the menu rises above the row (it sits at the sidebar's foot)", async () => {
    const { button } = mount();
    await fireEvent.click(button);
    expect(screen.getByRole("menu").className).toContain("bottom-full");
  });

  test("opens with the name, then items in order", async () => {
    const { button } = mount();
    await fireEvent.click(button);
    expect(button.getAttribute("aria-expanded")).toBe("true");
    const menu = screen.getByRole("menu");
    expect(menu.textContent).toContain("Yoga Setiawan");
    const labels = screen.getAllByRole("menuitem").map((el) => el.textContent?.trim());
    // "Switch to Agents" is the ↩ icon now, not a menu item.
    expect(labels).toEqual(["Team settings", "Mini Tools"]);
    expect(screen.getByRole("menuitem", { name: "Mini Tools" }).getAttribute("href")).toBe("/mini-tools");
    // Theme sits between Team settings and Mini Tools.
    const text = menu.textContent ?? "";
    expect(text.indexOf("Team settings")).toBeLessThan(text.indexOf("Theme"));
    expect(text.indexOf("Theme")).toBeLessThan(text.indexOf("Mini Tools"));
  });

  test("Theme posts the user's paired theme to /theme, current mode checked", async () => {
    mount();
    await fireEvent.click(screen.getByRole("button", { name: "Account menu" }));
    const light = screen.getByRole("menuitemradio", { name: "Light" });
    const dark = screen.getByRole("menuitemradio", { name: "Dark" });
    expect(light.getAttribute("aria-checked")).toBe("true");
    expect(dark.getAttribute("aria-checked")).toBe("false");
    const form = dark.closest("form")!;
    expect(form.getAttribute("action")).toBe("/theme");
    expect(form.getAttribute("method")).toBe("POST");
    expect((form.querySelector('input[name="theme"]') as HTMLInputElement).value).toBe("github-dark");
    expect((form.querySelector('input[name="redirect"]') as HTMLInputElement).value).toBe(location.pathname + location.search);
  });

  test("no Theme row without theme data", async () => {
    mount({ theme: null });
    await fireEvent.click(screen.getByRole("button", { name: "Account menu" }));
    expect(screen.queryByRole("menuitemradio")).toBeNull();
  });

  test("Team settings calls back and closes", async () => {
    const { onSettings, button } = mount();
    await fireEvent.click(button);
    await fireEvent.click(screen.getByRole("menuitem", { name: "Team settings" }));
    expect(onSettings).toHaveBeenCalledOnce();
    expect(screen.queryByRole("menu")).toBeNull();
  });

  test("Esc closes and gives focus back to the button; a press outside closes", async () => {
    const { button } = mount();
    await fireEvent.click(button);
    await fireEvent.keyDown(window, { key: "Escape" });
    expect(screen.queryByRole("menu")).toBeNull();
    await vi.waitFor(() => expect(document.activeElement).toBe(button));

    await fireEvent.click(button);
    await fireEvent.pointerDown(document.body);
    expect(screen.queryByRole("menu")).toBeNull();
  });
});
