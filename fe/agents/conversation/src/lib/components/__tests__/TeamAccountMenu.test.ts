import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import TeamAccountMenu from "../TeamAccountMenu.svelte";

const theme = { mode: "light" as const, light: "github-light", dark: "github-dark" };

function mount(over: Record<string, unknown> = {}) {
  const onSettings = vi.fn();
  render(TeamAccountMenu, {
    props: { viewerName: "Yoga Setiawan", viewerEmail: "yoga@example.com", theme, onSettings, ...over },
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

  test("no ↩ beside the name: the Team | Agents switch is the way back", () => {
    mount();
    expect(screen.queryByRole("link", { name: "Back to Agents" })).toBeNull();
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
    expect(menu.textContent).toContain("yoga@example.com");
    // Same items as wick's account menu; no Agents item (the switch covers it).
    expect(labels).toEqual(["Profile", "Access Tokens", "Connected Apps", "MCP", "Team settings", "Mini Tools", "Sign out"]);
    expect(screen.getByRole("menuitem", { name: "Mini Tools" }).getAttribute("href")).toBe("/mini-tools");
    expect(screen.getByRole("menuitem", { name: "Access Tokens" }).getAttribute("href")).toBe("/profile/tokens");
    expect(screen.getByRole("menuitem", { name: "Sign out" }).closest("form")!.getAttribute("action")).toBe("/auth/logout");
    const text = menu.textContent ?? "";
    expect(text.indexOf("Mini Tools")).toBeLessThan(text.indexOf("Theme"));
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

  test("Admin panel only for admins", async () => {
    mount({ isAdmin: true });
    await fireEvent.click(screen.getByRole("button", { name: "Account menu" }));
    expect(screen.getByRole("menuitem", { name: "Admin panel" }).getAttribute("href")).toBe("/admin");
  });

  test("non-admins get no Admin panel", async () => {
    mount();
    await fireEvent.click(screen.getByRole("button", { name: "Account menu" }));
    expect(screen.queryByRole("menuitem", { name: "Admin panel" })).toBeNull();
  });

  test("Viewing as: a dot on the row, and the banner posts Back to my account", async () => {
    mount({ viewingAs: "Member" });
    expect(screen.getByTestId("viewing-as-dot")).toBeTruthy();
    await fireEvent.click(screen.getByRole("button", { name: "Account menu" }));
    expect(screen.getByTestId("viewing-as").textContent).toContain("Viewing as Member");
    const form = screen.getByRole("button", { name: "Back to my account" }).closest("form")!;
    expect(form.getAttribute("action")).toBe("/admin/impersonate/stop");
    expect(form.getAttribute("method")).toBe("POST");
  });

  test("no banner and no dot when not viewing as someone", async () => {
    mount();
    expect(screen.queryByTestId("viewing-as-dot")).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: "Account menu" }));
    expect(screen.queryByTestId("viewing-as")).toBeNull();
  });

  test("versions show when known", async () => {
    mount({ appVersion: "1.2.3", wickVersion: "0.9.0" });
    await fireEvent.click(screen.getByRole("button", { name: "Account menu" }));
    expect(screen.getByTestId("versions").textContent).toContain("1.2.3");
  });
});
