import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import IconPicker from "../IconPicker.svelte";

/* The lazy emoji loader is mocked so the test never triggers the real
   ~400KB vendor import — we assert the open/close + custom-paste paths only.
   mountEmojiPicker is a no-op stub that resolves to a dummy element. */
vi.mock("../emojiPicker.js", () => ({
  mountEmojiPicker: vi.fn(async () => document.createElement("div")),
}));

beforeEach(() => {
  vi.clearAllMocks();
});

describe("IconPicker", () => {
  it("renders the placeholder glyph when empty", () => {
    render(IconPicker, { value: "", onChange: vi.fn() });
    expect(screen.getByText("🔌")).toBeTruthy();
  });

  it("renders an emoji value verbatim as text", () => {
    render(IconPicker, { value: "🚀", onChange: vi.fn() });
    expect(screen.getByText("🚀")).toBeTruthy();
  });

  it("renders an inline <svg> / data:image value as an <img> preview", () => {
    const { container } = render(IconPicker, { value: "data:image/png;base64,AAAA", onChange: vi.fn() });
    const img = container.querySelector("img");
    expect(img).toBeTruthy();
    expect(img?.getAttribute("src")).toBe("data:image/png;base64,AAAA");
  });

  it("opens and closes the panel on toggle", async () => {
    render(IconPicker, { value: "", onChange: vi.fn() });
    const toggle = screen.getByRole("button", { name: "Pick an icon" });
    expect(screen.queryByLabelText(/Custom — any emoji/)).toBeNull();
    await fireEvent.click(toggle);
    expect(screen.getByLabelText(/Custom — any emoji/)).toBeTruthy();
    await fireEvent.click(toggle);
    expect(screen.queryByLabelText(/Custom — any emoji/)).toBeNull();
  });

  it("applies a valid custom inline-svg paste", async () => {
    const onChange = vi.fn();
    render(IconPicker, { value: "", onChange });
    await fireEvent.click(screen.getByRole("button", { name: "Pick an icon" }));
    await fireEvent.input(screen.getByLabelText(/Custom — any emoji/), {
      target: { value: "<svg viewBox=\"0 0 1 1\"></svg>" },
    });
    await fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    expect(onChange).toHaveBeenCalledWith("<svg viewBox=\"0 0 1 1\"></svg>");
  });

  it("rejects a non-image data: payload", async () => {
    const onChange = vi.fn();
    render(IconPicker, { value: "", onChange });
    await fireEvent.click(screen.getByRole("button", { name: "Pick an icon" }));
    await fireEvent.input(screen.getByLabelText(/Custom — any emoji/), {
      target: { value: "data:text/html,<b>x</b>" },
    });
    await fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByText("Only data:image/… payloads are allowed.")).toBeTruthy();
  });

  it("rejects non-svg markup", async () => {
    const onChange = vi.fn();
    render(IconPicker, { value: "", onChange });
    await fireEvent.click(screen.getByRole("button", { name: "Pick an icon" }));
    await fireEvent.input(screen.getByLabelText(/Custom — any emoji/), {
      target: { value: "<div>nope</div>" },
    });
    await fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByText("Only inline <svg> markup is allowed.")).toBeTruthy();
  });

  it("rejects a payload over the 32KB cap", async () => {
    const onChange = vi.fn();
    render(IconPicker, { value: "", onChange });
    await fireEvent.click(screen.getByRole("button", { name: "Pick an icon" }));
    const big = "<svg>" + "a".repeat(33 * 1024) + "</svg>";
    await fireEvent.input(screen.getByLabelText(/Custom — any emoji/), { target: { value: big } });
    await fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByText("Too large — max 32KB.")).toBeTruthy();
  });
});
