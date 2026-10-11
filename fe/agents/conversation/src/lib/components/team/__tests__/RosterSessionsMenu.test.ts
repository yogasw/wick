import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import { tick } from "svelte";
import RosterSessionsMenu from "../RosterSessionsMenu.svelte";

const sessions = [
  { id: "s1", label: "Template rejection", trigger: "Captain", since: new Date(Date.now() - 12 * 60000).toISOString() },
  { id: "s2", label: "Quality drop", trigger: "you" },
  { id: "s3", label: "", trigger: "", source: "Slack" },
];

async function openMenu() {
  const onopen = vi.fn();
  render(RosterSessionsMenu, { sessions, now: Date.now(), onopen });
  const badge = screen.getByTestId("roster-sessions-count");
  await fireEvent.click(badge);
  await tick();
  return { onopen, badge, items: screen.getAllByTestId("roster-sessions-item") };
}

describe("RosterSessionsMenu", () => {
  test("opening focuses the first chat; each row names who or where it came from", async () => {
    const { items } = await openMenu();
    expect(document.activeElement).toBe(items[0]);
    expect(items.map((i) => i.textContent?.replace(/\s+/g, " ").trim())).toEqual([
      "Template rejection Captain · 12m",
      "Quality drop You",
      "Untitled chat Slack",
    ]);
  });

  test("arrows walk the chats and wrap", async () => {
    const { items } = await openMenu();
    await fireEvent.keyDown(items[0], { key: "ArrowDown" });
    expect(document.activeElement).toBe(items[1]);
    await fireEvent.keyDown(items[1], { key: "ArrowUp" });
    await fireEvent.keyDown(items[0], { key: "ArrowUp" });
    expect(document.activeElement).toBe(items[2]);
    await fireEvent.keyDown(items[2], { key: "Home" });
    expect(document.activeElement).toBe(items[0]);
  });

  test("Esc closes only the popover: focus back on the badge, the page's own Esc never sees it", async () => {
    const pageEsc = vi.fn();
    window.addEventListener("keydown", pageEsc);
    const { badge, items } = await openMenu();
    await fireEvent.keyDown(items[1], { key: "Escape" });
    await tick();
    window.removeEventListener("keydown", pageEsc);
    expect(screen.queryByTestId("roster-sessions-menu")).toBeNull();
    expect(document.activeElement).toBe(badge);
    expect(pageEsc).not.toHaveBeenCalled();
  });

  test("a chat opens it and closes the popover", async () => {
    const { onopen, items } = await openMenu();
    await fireEvent.click(items[2]);
    await tick();
    expect(onopen).toHaveBeenCalledWith("s3");
    expect(screen.queryByTestId("roster-sessions-menu")).toBeNull();
  });
});
