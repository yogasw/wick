import { describe, test, expect } from "vitest";
import { render, screen } from "@testing-library/svelte";
import { createRawSnippet } from "svelte";
import LabeledInput from "../LabeledInput.svelte";

const control = createRawSnippet(() => ({
  render: () => `<input aria-label="field" />`,
}));

describe("LabeledInput", () => {
  test("renders label, helper, and children", () => {
    render(LabeledInput, { props: { label: "Name", helper: "your name", children: control } });
    expect(screen.getByText("Name")).toBeTruthy();
    expect(screen.getByText("your name")).toBeTruthy();
    expect(screen.getByLabelText("field")).toBeTruthy();
  });

  test("error replaces helper", () => {
    render(LabeledInput, { props: { label: "X", helper: "h", error: "bad", children: control } });
    expect(screen.getByText("bad")).toBeTruthy();
    expect(screen.queryByText("h")).toBeNull();
  });

  test("required shows an asterisk", () => {
    render(LabeledInput, { props: { label: "X", required: true, children: control } });
    expect(screen.getByText("*")).toBeTruthy();
  });
});
