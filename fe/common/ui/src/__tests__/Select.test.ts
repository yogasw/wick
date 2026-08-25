import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import Select from "../Select.svelte";

describe("Select", () => {
  test("renders options and fires onChange", async () => {
    const onChange = vi.fn();
    render(Select, { props: { value: "a", options: ["a", "b"], onChange } });
    const sel = screen.getByRole("combobox") as HTMLSelectElement;
    await fireEvent.change(sel, { target: { value: "b" } });
    expect(onChange).toHaveBeenCalledWith("b");
  });
  test("base uses rounded-lg and a green focus ring", () => {
    const { container } = render(Select, { props: { value: "a", options: ["a"], onChange: vi.fn() } });
    const sel = container.querySelector("select") as HTMLSelectElement;
    expect(sel.className).toContain("rounded-lg");
    expect(sel.className).toContain("focus:ring-green-200");
  });
});
