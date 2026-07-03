import { describe, test, expect, vi } from "vitest";
import { render, fireEvent } from "@testing-library/svelte";
import CodeEditor from "../CodeEditor.svelte";

/* Ace can't init under jsdom, so the component renders its textarea fallback.
   These tests pin the fallback contract (value shown, edits emitted). */

describe("CodeEditor fallback", () => {
  test("fill-height mode renders a textarea with the value", () => {
    const { container } = render(CodeEditor, {
      props: { value: "hello world", onChange: vi.fn() },
    });
    const ta = container.querySelector("textarea");
    expect(ta).not.toBeNull();
    expect(ta!.value).toBe("hello world");
  });

  test("rows mode renders a sized textarea", () => {
    const { container } = render(CodeEditor, {
      props: { value: "x", onChange: vi.fn(), rows: 14, language: "go" },
    });
    const ta = container.querySelector("textarea");
    expect(ta).not.toBeNull();
    expect(ta!.getAttribute("rows")).toBe("14");
  });

  test("typing emits onChange", async () => {
    const onChange = vi.fn();
    const { container } = render(CodeEditor, { props: { value: "", onChange } });
    const ta = container.querySelector("textarea")!;
    await fireEvent.input(ta, { target: { value: "edited" } });
    expect(onChange).toHaveBeenCalledWith("edited");
  });

  test("readonly textarea does not accept edits path (attribute set)", () => {
    const { container } = render(CodeEditor, {
      props: { value: "ro", onChange: vi.fn(), readonly: true },
    });
    const ta = container.querySelector("textarea")!;
    expect(ta.hasAttribute("readonly")).toBe(true);
  });
});
