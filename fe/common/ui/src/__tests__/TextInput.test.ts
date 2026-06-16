import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import TextInput from "../TextInput.svelte";

describe("TextInput", () => {
  test("renders value and fires onChange on input", async () => {
    const onChange = vi.fn();
    render(TextInput, { props: { value: "hi", onChange, ariaLabel: "name" } });
    const input = screen.getByLabelText("name") as HTMLInputElement;
    expect(input.value).toBe("hi");
    await fireEvent.input(input, { target: { value: "bye" } });
    expect(onChange).toHaveBeenCalledWith("bye");
  });

  test("fires onBlur", async () => {
    const onBlur = vi.fn();
    render(TextInput, { props: { value: "", onChange: vi.fn(), onBlur, ariaLabel: "x" } });
    await fireEvent.blur(screen.getByLabelText("x"));
    expect(onBlur).toHaveBeenCalled();
  });

  test("type search is honored", () => {
    render(TextInput, { props: { value: "", onChange: vi.fn(), type: "search", ariaLabel: "s" } });
    expect((screen.getByLabelText("s") as HTMLInputElement).type).toBe("search");
  });

  test("base uses rounded-lg and a green focus ring", () => {
    render(TextInput, { props: { value: "", onChange: vi.fn(), ariaLabel: "r" } });
    const input = screen.getByLabelText("r") as HTMLInputElement;
    expect(input.className).toContain("rounded-lg");
    expect(input.className).toContain("focus:ring-green-200");
  });
});
