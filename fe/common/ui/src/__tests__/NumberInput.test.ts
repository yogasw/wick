import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import NumberInput from "../NumberInput.svelte";

describe("NumberInput", () => {
  test("parses input to a number", async () => {
    const onChange = vi.fn();
    render(NumberInput, { props: { value: 0, onChange, ariaLabel: "n" } });
    await fireEvent.input(screen.getByLabelText("n"), { target: { value: "42" } });
    expect(onChange).toHaveBeenCalledWith(42);
  });

  test("empty value becomes 0", async () => {
    const onChange = vi.fn();
    render(NumberInput, { props: { value: 5, onChange, ariaLabel: "n" } });
    await fireEvent.input(screen.getByLabelText("n"), { target: { value: "" } });
    expect(onChange).toHaveBeenCalledWith(0);
  });

  test("min/max/step attributes are set", () => {
    render(NumberInput, { props: { value: 1, onChange: vi.fn(), min: 0, max: 10, step: 2, ariaLabel: "n" } });
    const input = screen.getByLabelText("n");
    expect(input.getAttribute("min")).toBe("0");
    expect(input.getAttribute("max")).toBe("10");
    expect(input.getAttribute("step")).toBe("2");
  });
});
