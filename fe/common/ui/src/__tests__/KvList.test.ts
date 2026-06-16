import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import { createRawSnippet } from "svelte";
import KvList from "../KvList.svelte";

describe("KvList", () => {
  test("renders a default text input per cell, edit calls onChange", async () => {
    const onChange = vi.fn();
    render(KvList, {
      props: {
        columns: ["key", "value"],
        rows: [{ key: "FOO", value: "1" }],
        onChange,
      },
    });
    const keyInput = screen.getByLabelText("key") as HTMLInputElement;
    expect(keyInput.value).toBe("FOO");
    await fireEvent.input(keyInput, { target: { value: "BAR" } });
    expect(onChange).toHaveBeenCalledWith([{ key: "BAR", value: "1" }]);
  });

  test("add row appends a blank row with all columns", async () => {
    const onChange = vi.fn();
    render(KvList, {
      props: {
        columns: ["key", "value"],
        rows: [{ key: "FOO", value: "1" }],
        onChange,
        addLabel: "+ Add row",
      },
    });
    await fireEvent.click(screen.getByText("+ Add row"));
    expect(onChange).toHaveBeenCalledWith([
      { key: "FOO", value: "1" },
      { key: "", value: "" },
    ]);
  });

  test("remove row drops it by index", async () => {
    const onChange = vi.fn();
    render(KvList, {
      props: {
        columns: ["value"],
        rows: [{ value: "a" }, { value: "b" }],
        onChange,
      },
    });
    const removeButtons = screen.getAllByRole("button", { name: "Remove row" });
    await fireEvent.click(removeButtons[0]);
    expect(onChange).toHaveBeenCalledWith([{ value: "b" }]);
  });

  test("cell snippet override replaces the default input", () => {
    const onChange = vi.fn();
    render(KvList, {
      props: {
        columns: ["value"],
        rows: [{ value: "x" }],
        onChange,
        cell: createRawSnippet(() => ({
          render: () => `<span data-testid="custom-cell">custom</span>`,
        })),
      },
    });
    expect(screen.getByTestId("custom-cell")).toBeTruthy();
    expect(screen.queryByLabelText("value")).toBeNull();
  });

  test("row container uses rounded-lg", () => {
    const { container } = render(KvList, {
      props: { columns: ["key", "value"], rows: [{ key: "a", value: "b" }], onChange: vi.fn() },
    });
    const rowDiv = container.querySelector(".border.border-white-300") as HTMLElement;
    expect(rowDiv.className).toContain("rounded-lg");
  });
});
