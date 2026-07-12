import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import Composer from "../Composer.svelte";

describe("Composer — send + attachments", () => {
  test("clicking Send with text calls onSend and clears the textarea", async () => {
    const onSend = vi.fn();
    render(Composer, { props: { onSend } });
    const textarea = screen.getByRole("textbox");
    await fireEvent.input(textarea, { target: { value: "hello world" } });
    await fireEvent.click(screen.getByRole("button", { name: /send/i }));
    expect(onSend).toHaveBeenCalledWith({ text: "hello world", files: [] });
    expect((textarea as HTMLTextAreaElement).value).toBe("");
  });

  test("Enter sends, Shift+Enter does not", async () => {
    const onSend = vi.fn();
    render(Composer, { props: { onSend } });
    const textarea = screen.getByRole("textbox");
    await fireEvent.input(textarea, { target: { value: "x" } });
    await fireEvent.keyDown(textarea, { key: "Enter", shiftKey: true });
    expect(onSend).not.toHaveBeenCalled();
    await fireEvent.keyDown(textarea, { key: "Enter", shiftKey: false });
    expect(onSend).toHaveBeenCalledOnce();
  });

  test("Send disabled with no text/files, enabled once text entered", async () => {
    render(Composer, { props: { onSend: vi.fn() } });
    const btn = screen.getByRole("button", { name: /send/i }) as HTMLButtonElement;
    expect(btn.disabled).toBe(true);
    await fireEvent.input(screen.getByRole("textbox"), { target: { value: "hi" } });
    expect(btn.disabled).toBe(false);
  });

  test("attaching a file shows it and includes it on send", async () => {
    const onSend = vi.fn();
    render(Composer, { props: { onSend } });
    const fileInput = document.querySelector("input[type=file]") as HTMLInputElement;
    const file = new File(["x"], "a.png", { type: "image/png" });
    await fireEvent.change(fileInput, { target: { files: [file] } });
    expect(screen.getByText("a.png")).toBeDefined();
    await fireEvent.click(screen.getByRole("button", { name: /send/i }));
    expect(onSend).toHaveBeenCalledWith({ text: "", files: [file] });
  });

  test("submitLabel renders the text beside the send arrow", () => {
    render(Composer, { props: { onSend: vi.fn(), submitLabel: "Send" } });
    expect(screen.getByRole("button", { name: /send/i }).textContent).toContain("Send");
  });
});

describe("Composer — toolbar dropdowns + bell", () => {
  test("no native selects; project + provider are toolbar chips; preset lives in the + menu", async () => {
    render(Composer, {
      props: {
        onSend: vi.fn(),
        provider: { options: [{ label: "claude", value: "claude/claude" }], value: "claude/claude", onChange: vi.fn() },
        project: { options: [{ label: "📁 P", value: "p" }], value: "p", onChange: vi.fn() },
        preset: { options: [{ label: "default", value: "" }], value: "", onChange: vi.fn() },
      },
    });
    expect(document.querySelectorAll("select").length).toBe(0);
    // toolbar chips (menu closed → one match each)
    expect(screen.getByRole("button", { name: /project/i })).toBeDefined();
    expect(screen.getByRole("button", { name: /provider/i })).toBeDefined();
    // preset has no chip — only inside the + menu
    expect(screen.queryByRole("button", { name: /preset/i })).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: /^add$/i }));
    expect(screen.getByRole("button", { name: /preset/i })).toBeDefined();
  });

  test("project chip is hidden when no project is selected (empty value)", () => {
    render(Composer, {
      props: {
        onSend: vi.fn(),
        project: { options: [{ label: "— no project —", value: "" }, { label: "📁 P", value: "p" }], value: "", onChange: vi.fn() },
      },
    });
    // no chip in the toolbar; project is still reachable via the + menu
    expect(screen.queryByRole("button", { name: /project/i })).toBeNull();
  });

  test("clicking the project chip opens its drill-in; picking an option fires onChange", async () => {
    const onChange = vi.fn();
    render(Composer, {
      props: {
        onSend: vi.fn(),
        project: { options: [{ label: "P one", value: "1" }, { label: "P two", value: "2" }], value: "1", onChange },
      },
    });
    await fireEvent.click(screen.getByRole("button", { name: /project/i })); // the chip
    await fireEvent.click(screen.getByText("P two"));
    expect(onChange).toHaveBeenCalledWith("2");
  });

  test("the + menu holds Attach file; the bell is a standalone icon shown only with notifyKey", async () => {
    const { unmount } = render(Composer, { props: { onSend: vi.fn() } });
    expect(screen.queryByRole("button", { name: /notifications/i })).toBeNull(); // no bell without key
    await fireEvent.click(screen.getByRole("button", { name: /^add$/i }));
    expect(screen.getByRole("button", { name: /attach file/i })).toBeDefined();
    unmount();

    render(Composer, { props: { onSend: vi.fn(), notifyKey: "k" } });
    expect(screen.getByRole("button", { name: /notifications/i })).toBeDefined(); // standalone bell
  });

  test("+ menu → Attach file opens the file picker", async () => {
    render(Composer, { props: { onSend: vi.fn() } });
    const fileInput = document.querySelector("input[type=file]") as HTMLInputElement;
    const clicked = vi.spyOn(fileInput, "click").mockImplementation(() => {});
    await fireEvent.click(screen.getByRole("button", { name: /^add$/i }));
    await fireEvent.click(screen.getByRole("button", { name: /attach file/i }));
    expect(clicked).toHaveBeenCalledOnce();
  });
});

describe("Composer — @ mention", () => {
  test("typing @ opens the file menu filtered by query (client fallback)", async () => {
    render(Composer, { props: { onSend: vi.fn(), mentionFiles: ["src/main.go", "README.md"] } });
    await fireEvent.input(screen.getByRole("textbox"), { target: { value: "@src" } });
    expect(screen.getByText("src/main.go")).toBeDefined();
    expect(screen.queryByText("README.md")).toBeNull();
  });

  test("@ uses the onSearchFiles backend provider (spaces allowed)", async () => {
    const onSearchFiles = vi.fn().mockResolvedValue(["src/main.go"]);
    render(Composer, { props: { onSend: vi.fn(), onSearchFiles } });
    await fireEvent.input(screen.getByRole("textbox"), { target: { value: "@src main" } });
    await waitFor(() => expect(onSearchFiles).toHaveBeenLastCalledWith("src main"));
    expect(await screen.findByText("src/main.go")).toBeDefined();
  });

  test("selecting a file inserts @path", async () => {
    render(Composer, { props: { onSend: vi.fn(), mentionFiles: ["src/main.go"] } });
    const textarea = screen.getByRole("textbox") as HTMLTextAreaElement;
    await fireEvent.input(textarea, { target: { value: "read @ma" } });
    await fireEvent.mouseDown(screen.getByText("src/main.go"));
    expect(textarea.value).toBe("read @src/main.go ");
  });

  test("@ does not trigger mid-word (email)", async () => {
    render(Composer, { props: { onSend: vi.fn(), mentionFiles: ["a.txt"] } });
    await fireEvent.input(screen.getByRole("textbox"), { target: { value: "foo@bar" } });
    expect(screen.queryByRole("listbox")).toBeNull();
  });
});

describe("Composer — / commands", () => {
  const CMDS = [
    { value: "provider", label: "/provider", category: "Switch", run: vi.fn() },
    { value: "processes", label: "/processes", category: "Panels", run: vi.fn() },
  ];

  test("typing / opens the command menu with category headers", async () => {
    render(Composer, { props: { onSend: vi.fn(), commands: CMDS } });
    await fireEvent.input(screen.getByRole("textbox"), { target: { value: "/" } });
    expect(screen.getByText("Switch")).toBeDefined();
    expect(screen.getByText("Panels")).toBeDefined();
  });

  test("selecting a command with run() fires it and clears the / token", async () => {
    const run = vi.fn();
    render(Composer, { props: { onSend: vi.fn(), commands: [{ value: "processes", label: "/processes", run }] } });
    const textarea = screen.getByRole("textbox") as HTMLTextAreaElement;
    await fireEvent.input(textarea, { target: { value: "/proc" } });
    await fireEvent.mouseDown(screen.getByText("/processes"));
    expect(run).toHaveBeenCalledOnce();
    expect(textarea.value).toBe("");
  });

  test("/ triggers mid-message after whitespace, not just as a prefix", async () => {
    render(Composer, { props: { onSend: vi.fn(), commands: CMDS } });
    await fireEvent.input(screen.getByRole("textbox"), { target: { value: "hi /prov" } });
    expect(screen.getByText("Switch")).toBeDefined();
  });

  test("/ does not trigger mid-word (path segment)", async () => {
    render(Composer, { props: { onSend: vi.fn(), commands: CMDS } });
    await fireEvent.input(screen.getByRole("textbox"), { target: { value: "open src/provider" } });
    expect(screen.queryByRole("listbox")).toBeNull();
  });

  test("selecting a mid-message command inserts the token after the existing text", async () => {
    const cmds = [{ value: "model gpt-5", label: "/model", category: "Set" }];
    render(Composer, { props: { onSend: vi.fn(), commands: cmds } });
    const textarea = screen.getByRole("textbox") as HTMLTextAreaElement;
    await fireEvent.input(textarea, { target: { value: "use /mod" } });
    await fireEvent.mouseDown(screen.getByText("/model"));
    expect(textarea.value).toBe("use /model gpt-5 ");
  });
});

describe("Composer — provider badge", () => {
  const props = {
    onSend: vi.fn(),
    provider: {
      options: [
        { label: "claude", value: "claude/claude" },
        { label: "codex", value: "codex/codex", badge: "AI Router" },
      ],
      value: "codex/codex",
      onChange: vi.fn(),
    },
  };

  test("selected badged provider marks the chip title with the badge", async () => {
    render(Composer, { props });
    expect(screen.getByRole("button", { name: "Provider" }).getAttribute("title")).toBe("codex · via AI Router");
  });

  test("a non-badged selection leaves the chip title plain", async () => {
    render(Composer, { props: { ...props, provider: { ...props.provider, value: "claude/claude" } } });
    expect(screen.getByRole("button", { name: "Provider" }).getAttribute("title")).toBe("claude");
  });

  test("the picker list renders the badge pill on badged options", async () => {
    render(Composer, { props });
    await fireEvent.click(screen.getByRole("button", { name: "Provider" }));
    expect(screen.getAllByText("AI Router").length).toBeGreaterThan(0);
  });
});

describe("Composer — menu behavior", () => {
  test("no menu when neither files nor commands are provided", async () => {
    render(Composer, { props: { onSend: vi.fn() } });
    await fireEvent.input(screen.getByRole("textbox"), { target: { value: "@x" } });
    expect(screen.queryByRole("listbox")).toBeNull();
  });

  test("Enter picks the highlighted item instead of sending while menu is open", async () => {
    const onSend = vi.fn();
    render(Composer, { props: { onSend, mentionFiles: ["a.txt"] } });
    const textarea = screen.getByRole("textbox") as HTMLTextAreaElement;
    await fireEvent.input(textarea, { target: { value: "@" } });
    await fireEvent.keyDown(textarea, { key: "Enter" });
    expect(onSend).not.toHaveBeenCalled();
    expect(textarea.value).toBe("@a.txt ");
  });

  test("clicking outside closes the menu", async () => {
    render(Composer, { props: { onSend: vi.fn(), mentionFiles: ["a.txt"] } });
    await fireEvent.input(screen.getByRole("textbox"), { target: { value: "@" } });
    expect(screen.getByRole("textbox", { name: /search files/i })).not.toBeNull();
    await fireEvent.mouseDown(document.body);
    expect(screen.queryByRole("textbox", { name: /search files/i })).toBeNull();
  });
});
