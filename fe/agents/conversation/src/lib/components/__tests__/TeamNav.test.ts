import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import TeamNav from "../TeamNav.svelte";

describe("TeamNav", () => {
  test("Agents goes to the exit target, Mini Tools to /mini-tools", () => {
    render(TeamNav, { props: { exitHref: "/tools/agents/sessions", onSettings: vi.fn() } });
    expect(screen.getByRole("link", { name: "Agents" }).getAttribute("href")).toBe("/tools/agents/sessions");
    expect(screen.getByRole("link", { name: "Mini Tools" }).getAttribute("href")).toBe("/mini-tools");
  });

  test("Team settings opens the drawer and shows as current while open", async () => {
    const onSettings = vi.fn();
    const { rerender } = render(TeamNav, { props: { exitHref: "/x", onSettings } });
    const btn = screen.getByRole("button", { name: "Team settings" });
    expect(btn.getAttribute("aria-current")).toBeNull();
    await fireEvent.click(btn);
    expect(onSettings).toHaveBeenCalledOnce();
    await rerender({ exitHref: "/x", onSettings, settingsActive: true });
    expect(screen.getByRole("button", { name: "Team settings" }).getAttribute("aria-current")).toBe("page");
  });
});
