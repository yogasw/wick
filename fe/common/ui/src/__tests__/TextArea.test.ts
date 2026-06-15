import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import TextArea from "../TextArea.svelte";

describe("TextArea", () => {
  test("renders value and fires onChange on input", async () => {
    const onChange = vi.fn();
    render(TextArea, { props: { value: "line", onChange, ariaLabel: "ta" } });
    const ta = screen.getByLabelText("ta") as HTMLTextAreaElement;
    expect(ta.value).toBe("line");
    await fireEvent.input(ta, { target: { value: "new" } });
    expect(onChange).toHaveBeenCalledWith("new");
  });

  test("rows attribute is set", () => {
    render(TextArea, { props: { value: "", onChange: vi.fn(), rows: 6, ariaLabel: "ta" } });
    expect((screen.getByLabelText("ta") as HTMLTextAreaElement).rows).toBe(6);
  });
});
