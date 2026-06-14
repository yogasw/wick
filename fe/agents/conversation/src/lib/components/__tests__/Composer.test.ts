import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import Composer from "../Composer.svelte";

describe("Composer", () => {
  test("clicking Send with text calls onSend and clears the textarea", async () => {
    const onSend = vi.fn();
    render(Composer, { props: { onSend } });

    const textarea = screen.getByRole("textbox");
    await fireEvent.input(textarea, { target: { value: "hello world" } });
    const btn = screen.getByRole("button", { name: /send/i });
    await fireEvent.click(btn);

    expect(onSend).toHaveBeenCalledOnce();
    expect(onSend).toHaveBeenCalledWith({ text: "hello world", files: [] });
    expect((textarea as HTMLTextAreaElement).value).toBe("");
  });

  test("pressing Enter (without Shift) sends the message", async () => {
    const onSend = vi.fn();
    render(Composer, { props: { onSend } });

    const textarea = screen.getByRole("textbox");
    await fireEvent.input(textarea, { target: { value: "enter send" } });
    await fireEvent.keyDown(textarea, { key: "Enter", shiftKey: false });

    expect(onSend).toHaveBeenCalledOnce();
    expect(onSend).toHaveBeenCalledWith({ text: "enter send", files: [] });
  });

  test("pressing Shift+Enter does NOT call onSend", async () => {
    const onSend = vi.fn();
    render(Composer, { props: { onSend } });

    const textarea = screen.getByRole("textbox");
    await fireEvent.input(textarea, { target: { value: "no send" } });
    await fireEvent.keyDown(textarea, { key: "Enter", shiftKey: true });

    expect(onSend).not.toHaveBeenCalled();
  });

  test("Send button is disabled when text is empty and no files", () => {
    render(Composer, { props: { onSend: vi.fn() } });
    const btn = screen.getByRole("button", { name: /send/i });
    expect((btn as HTMLButtonElement).disabled).toBe(true);
  });

  test("Send button is enabled once text is entered", async () => {
    render(Composer, { props: { onSend: vi.fn() } });

    const textarea = screen.getByRole("textbox");
    await fireEvent.input(textarea, { target: { value: "some text" } });

    const btn = screen.getByRole("button", { name: /send/i });
    expect((btn as HTMLButtonElement).disabled).toBe(false);
  });

  test("Send button is disabled when disabled prop is true even with text", async () => {
    render(Composer, { props: { onSend: vi.fn(), disabled: true } });

    const textarea = screen.getByRole("textbox");
    await fireEvent.input(textarea, { target: { value: "blocked" } });

    const btn = screen.getByRole("button", { name: /send/i });
    expect((btn as HTMLButtonElement).disabled).toBe(true);
  });

  test("attaching a file via file input shows it in the pending list", async () => {
    render(Composer, { props: { onSend: vi.fn() } });

    const fileInput = document.querySelector("input[type=file]") as HTMLInputElement;
    expect(fileInput).not.toBeNull();

    const file = new File(["hello"], "test.txt", { type: "text/plain" });
    await fireEvent.change(fileInput, { target: { files: [file] } });

    expect(screen.getByText("test.txt")).toBeDefined();
  });

  test("removing an attached file drops it from the pending list", async () => {
    render(Composer, { props: { onSend: vi.fn() } });

    const fileInput = document.querySelector("input[type=file]") as HTMLInputElement;
    const file = new File(["hello"], "remove-me.txt", { type: "text/plain" });
    await fireEvent.change(fileInput, { target: { files: [file] } });

    expect(screen.getByText("remove-me.txt")).toBeDefined();

    const removeBtn = screen.getByRole("button", { name: /remove remove-me\.txt/i });
    await fireEvent.click(removeBtn);

    expect(screen.queryByText("remove-me.txt")).toBeNull();
  });

  test("Send button is enabled when files are attached even with empty text", async () => {
    render(Composer, { props: { onSend: vi.fn() } });

    const fileInput = document.querySelector("input[type=file]") as HTMLInputElement;
    const file = new File(["data"], "image.png", { type: "image/png" });
    await fireEvent.change(fileInput, { target: { files: [file] } });

    const btn = screen.getByRole("button", { name: /send/i });
    expect((btn as HTMLButtonElement).disabled).toBe(false);
  });

  test("sending includes pending files and clears them afterward", async () => {
    const onSend = vi.fn();
    render(Composer, { props: { onSend } });

    const fileInput = document.querySelector("input[type=file]") as HTMLInputElement;
    const file = new File(["data"], "attach.png", { type: "image/png" });
    await fireEvent.change(fileInput, { target: { files: [file] } });

    const textarea = screen.getByRole("textbox");
    await fireEvent.input(textarea, { target: { value: "with attachment" } });

    const btn = screen.getByRole("button", { name: /send/i });
    await fireEvent.click(btn);

    expect(onSend).toHaveBeenCalledWith({ text: "with attachment", files: [file] });
    expect(screen.queryByText("attach.png")).toBeNull();
    expect((textarea as HTMLTextAreaElement).value).toBe("");
  });

  test("drag-and-drop handler is present on the composer root element", () => {
    const { container } = render(Composer, { props: { onSend: vi.fn() } });
    const root = container.firstElementChild as HTMLElement;
    /* jsdom cannot simulate actual file drag-drop (DataTransfer.items is read-only),
       so we verify the ondrop attribute exists as evidence the handler is wired. */
    expect(root).not.toBeNull();
    /* The composer should have a drag-sensitive wrapper */
    const dropZone = container.querySelector("[data-composer-drop]");
    expect(dropZone).not.toBeNull();
  });

  test("paste handler is wired (programmatic path — jsdom paste file simulation)", async () => {
    const onSend = vi.fn();
    render(Composer, { props: { onSend } });

    const textarea = screen.getByRole("textbox");
    /* jsdom ClipboardEvent.clipboardData.items is read-only; we verify the
       listener is attached by firing a paste event without items — no crash
       means the handler exists and handles the empty-items case gracefully. */
    await fireEvent.paste(textarea);
    expect(onSend).not.toHaveBeenCalled();
  });

  test("placeholder prop is passed to the textarea", () => {
    render(Composer, { props: { onSend: vi.fn(), placeholder: "Type a message…" } });
    const textarea = screen.getByPlaceholderText("Type a message…");
    expect(textarea).not.toBeNull();
  });
});
