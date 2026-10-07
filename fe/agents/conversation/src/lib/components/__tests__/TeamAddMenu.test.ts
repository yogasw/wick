import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import TeamAddMenu from "../TeamAddMenu.svelte";

function mount(canGroup = true) {
  const onAgent = vi.fn();
  const onGroup = vi.fn();
  render(TeamAddMenu, { props: { canGroup, onAgent, onGroup } });
  return { onAgent, onGroup, button: screen.getByRole("button", { name: "New agent or group" }) };
}

describe("TeamAddMenu", () => {
  test("a ghost + with a tooltip, announcing a closed menu", () => {
    const { button } = mount();
    expect(button.getAttribute("title")).toBe("New agent or group");
    expect(button.getAttribute("aria-haspopup")).toBe("menu");
    expect(button.getAttribute("aria-expanded")).toBe("false");
    expect(screen.queryByRole("menu")).toBeNull();
  });

  test("opens New agent then New group; each calls back and closes", async () => {
    const { onAgent, onGroup, button } = mount();
    await fireEvent.click(button);
    expect(button.getAttribute("aria-expanded")).toBe("true");
    expect(screen.getAllByRole("menuitem").map((el) => el.textContent?.trim())).toEqual(["New agent", "New group"]);
    await fireEvent.click(screen.getByRole("menuitem", { name: "New agent" }));
    expect(onAgent).toHaveBeenCalledOnce();
    expect(screen.queryByRole("menu")).toBeNull();
    await fireEvent.click(button);
    await fireEvent.click(screen.getByRole("menuitem", { name: "New group" }));
    expect(onGroup).toHaveBeenCalledOnce();
  });

  test("under two agents New group is off and says why", async () => {
    const { onGroup, button } = mount(false);
    await fireEvent.click(button);
    const g = screen.getByRole("menuitem", { name: /New group/ }) as HTMLButtonElement;
    expect(g.disabled).toBe(true);
    expect(g.getAttribute("aria-disabled")).toBe("true");
    expect(g.textContent).toContain("Needs 2+ agents");
    await fireEvent.click(g);
    expect(onGroup).not.toHaveBeenCalled();
  });

  test("Esc closes and refocuses the +; a press outside closes", async () => {
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
