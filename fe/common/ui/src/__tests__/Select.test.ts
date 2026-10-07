import { describe, test, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/svelte";
import Select from "../Select.svelte";

afterEach(() => cleanup());

const opts = [
  { label: "Claude", value: "claude" },
  { label: "Gemini", value: "gemini", badge: "experimental", disabled: true },
  { label: "oh-my-pi (omp)", value: "omp", description: "Login ChatGPT atau Claude" },
  { label: "opencode", value: "opencode" },
];

function trigger(): HTMLButtonElement {
  return screen.getByTestId("wick-select-trigger") as HTMLButtonElement;
}

describe("Select", () => {
  test("trigger shows the selected label with listbox ARIA", () => {
    render(Select, { props: { value: "omp", options: opts, onChange: vi.fn() } });
    const t = trigger();
    expect(t.textContent).toContain("oh-my-pi (omp)");
    expect(t.getAttribute("aria-haspopup")).toBe("listbox");
    expect(t.getAttribute("aria-expanded")).toBe("false");
    expect(screen.queryByRole("listbox")).toBeNull();
  });

  test("click opens, click an option selects and closes", async () => {
    const onChange = vi.fn();
    render(Select, { props: { value: "claude", options: opts, onChange } });
    await fireEvent.click(trigger());
    expect(trigger().getAttribute("aria-expanded")).toBe("true");
    const list = screen.getByRole("listbox");
    expect(list.parentElement?.parentElement).toBe(document.body); // portalled
    const omp = screen.getByRole("option", { name: /oh-my-pi/ });
    expect(omp.textContent).toContain("Login ChatGPT atau Claude");
    expect(screen.getByRole("option", { name: /^Claude$/ }).getAttribute("aria-selected")).toBe("true");
    await fireEvent.click(omp);
    expect(onChange).toHaveBeenCalledWith("omp");
    expect(screen.queryByRole("listbox")).toBeNull();
  });

  test("keyboard: ArrowDown opens, arrows skip disabled, Enter selects", async () => {
    const onChange = vi.fn();
    render(Select, { props: { value: "claude", options: opts, onChange } });
    const t = trigger();
    await fireEvent.keyDown(t, { key: "ArrowDown" });
    expect(screen.getByRole("listbox")).toBeTruthy();
    expect(t.getAttribute("aria-activedescendant")).toMatch(/-opt-0$/);
    await fireEvent.keyDown(t, { key: "ArrowDown" }); // skips disabled Gemini
    expect(t.getAttribute("aria-activedescendant")).toMatch(/-opt-2$/);
    await fireEvent.keyDown(t, { key: "End" });
    expect(t.getAttribute("aria-activedescendant")).toMatch(/-opt-3$/);
    await fireEvent.keyDown(t, { key: "Home" });
    expect(t.getAttribute("aria-activedescendant")).toMatch(/-opt-0$/);
    await fireEvent.keyDown(t, { key: "o" }); // type-ahead
    expect(t.getAttribute("aria-activedescendant")).toMatch(/-opt-2$/);
    await fireEvent.keyDown(t, { key: "Enter" });
    expect(onChange).toHaveBeenCalledWith("omp");
  });

  test("Space opens; Escape closes and keeps focus on the trigger", async () => {
    render(Select, { props: { value: "claude", options: opts, onChange: vi.fn() } });
    const t = trigger();
    t.focus();
    await fireEvent.keyDown(t, { key: " " });
    expect(screen.getByRole("listbox")).toBeTruthy();
    await fireEvent.keyDown(t, { key: "Escape" });
    expect(screen.queryByRole("listbox")).toBeNull();
    expect(document.activeElement).toBe(t);
  });

  test("click outside and Tab close", async () => {
    render(Select, { props: { value: "claude", options: opts, onChange: vi.fn() } });
    await fireEvent.click(trigger());
    await fireEvent.mouseDown(document.body);
    expect(screen.queryByRole("listbox")).toBeNull();
    await fireEvent.click(trigger());
    await fireEvent.keyDown(trigger(), { key: "Tab" });
    expect(screen.queryByRole("listbox")).toBeNull();
  });

  test("disabled option is not selectable; disabled Select never opens", async () => {
    const onChange = vi.fn();
    const { unmount } = render(Select, { props: { value: "claude", options: opts, onChange } });
    await fireEvent.click(trigger());
    await fireEvent.click(screen.getByRole("option", { name: /Gemini/ }));
    expect(onChange).not.toHaveBeenCalled();
    unmount();
    render(Select, { props: { value: "claude", options: opts, onChange, disabled: true } });
    expect(trigger().disabled).toBe(true);
    await fireEvent.click(trigger());
    expect(screen.queryByRole("listbox")).toBeNull();
  });

  test("searchable filters by label and description", async () => {
    const onChange = vi.fn();
    render(Select, { props: { value: "", options: opts, onChange, searchable: true, placeholder: "Pick…" } });
    expect(trigger().textContent).toContain("Pick…");
    await fireEvent.click(trigger());
    const search = screen.getByTestId("wick-select-search") as HTMLInputElement;
    await fireEvent.input(search, { target: { value: "chatgpt" } });
    const shown = screen.getAllByRole("option");
    expect(shown).toHaveLength(1);
    await fireEvent.keyDown(search, { key: "Enter" });
    expect(onChange).toHaveBeenCalledWith("omp");
  });

  test("auto-searchable above 8 options; string options still work", async () => {
    const many = Array.from({ length: 10 }, (_, i) => `opt${i}`);
    const onChange = vi.fn();
    render(Select, { props: { value: "opt1", options: many, onChange } });
    await fireEvent.click(trigger());
    expect(screen.getByTestId("wick-select-search")).toBeTruthy();
    await fireEvent.click(screen.getByRole("option", { name: "opt5" }));
    expect(onChange).toHaveBeenCalledWith("opt5");
  });

  test("id labels the trigger, name posts a hidden input, boxed style kept", () => {
    const { container } = render(Select, { props: { value: "omp", options: opts, onChange: vi.fn(), id: "t", name: "type" } });
    const t = trigger();
    expect(t.id).toBe("t");
    expect(t.className).toContain("rounded-lg");
    expect(t.className).toContain("focus:ring-green-200");
    const hidden = container.querySelector('input[type="hidden"][name="type"]') as HTMLInputElement;
    expect(hidden.value).toBe("omp");
  });
});
