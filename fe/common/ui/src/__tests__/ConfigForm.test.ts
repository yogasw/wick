import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import ConfigForm from "../ConfigForm.svelte";
import { isVisible, isToggle, dropdownOptions, type ConfigField } from "../config-types.js";

const thinkingSchema: ConfigField[] = [
  { Key: "thinking_on", Type: "checkbox", Description: "Extended reasoning", Group: "Thinking|Reasoning for this session." },
  { Key: "reasoning_effort", Type: "dropdown", Options: "low|medium|high", Description: "Effort", visible_when: "thinking_on:true", Group: "Thinking" },
];

describe("config-types helpers", () => {
  test("isToggle treats bool/boolean/checkbox alike", () => {
    expect(isToggle({ Key: "a", Type: "checkbox" })).toBe(true);
    expect(isToggle({ Key: "a", Type: "bool" })).toBe(true);
    expect(isToggle({ Key: "a", Type: "boolean" })).toBe(true);
    expect(isToggle({ Key: "a", Type: "dropdown" })).toBe(false);
  });

  test("dropdownOptions splits + trims + drops empties", () => {
    expect(dropdownOptions({ Key: "x", Options: "low | medium |high|" })).toEqual(["low", "medium", "high"]);
    expect(dropdownOptions({ Key: "x" })).toEqual([]);
  });

  test("isVisible honours visible_when + hidden", () => {
    const f = thinkingSchema[1];
    expect(isVisible(f, { thinking_on: "true" })).toBe(true);
    expect(isVisible(f, { thinking_on: "false" })).toBe(false);
    expect(isVisible(f, {})).toBe(false);
    expect(isVisible({ Key: "h", hidden: true }, {})).toBe(false);
  });
});

describe("ConfigForm", () => {
  test("toggle emits inverted string value", async () => {
    const onChange = vi.fn();
    render(ConfigForm, { props: { schema: thinkingSchema, values: { thinking_on: "false" }, onChange } });
    const sw = screen.getByRole("switch", { name: "Extended reasoning" });
    await fireEvent.click(sw);
    expect(onChange).toHaveBeenCalledWith("thinking_on", "true");
  });

  test("dropdown hidden while gate is off, shown when on", () => {
    const { rerender } = render(ConfigForm, {
      props: { schema: thinkingSchema, values: { thinking_on: "false" }, onChange: vi.fn() },
    });
    // effort dropdown gated on thinking_on:true → not rendered yet
    expect(screen.queryByText("Effort")).toBeNull();
    rerender({ schema: thinkingSchema, values: { thinking_on: "true" }, onChange: vi.fn() });
    expect(screen.getByText("Effort")).toBeTruthy();
  });

  test("group heading renders once", () => {
    render(ConfigForm, { props: { schema: thinkingSchema, values: { thinking_on: "true" }, onChange: vi.fn() } });
    expect(screen.getAllByText("Thinking")).toHaveLength(1);
  });
});
