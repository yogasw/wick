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

  test("provider with a loader drills straight to its model list + fetches live models", async () => {
    const loadModels = vi.fn().mockResolvedValue([
      { id: "m-a", label: "Model A", default: true },
      { id: "m-b", label: "Model B", default: false },
    ]);
    const onChange = vi.fn();
    render(Composer, {
      props: {
        onSend: vi.fn(),
        // Single instance, NO static models — the loader must still enable the drill.
        provider: { options: [{ label: "wick", value: "wick/wick" }], value: "wick/wick", onChange, loadModels },
      },
    });
    // The chip opens the CURRENT provider straight into its model list (a
    // provider is already selected), fetching live models — no relisting.
    await fireEvent.click(screen.getByRole("button", { name: /provider/i }));
    expect(loadModels).toHaveBeenCalledWith("wick/wick");
    await waitFor(() => expect(screen.getByText("Model A")).toBeDefined());
    expect(screen.getByText("Model B")).toBeDefined();
    // Picking a model pins it as "value::modelID".
    await fireEvent.click(screen.getByText("Model B"));
    expect(onChange).toHaveBeenCalledWith("wick/wick::m-b");
  });

  test("drilled provider offers a 'Use default' row that pins the provider without a model", async () => {
    const onChange = vi.fn();
    render(Composer, {
      props: {
        onSend: vi.fn(),
        provider: { options: [{ label: "wick", value: "wick/wick" }], value: "wick/wick", onChange, loadModels: vi.fn().mockResolvedValue([]) },
      },
    });
    await fireEvent.click(screen.getByRole("button", { name: /provider/i }));
    await fireEvent.click(screen.getByText(/Use wick/i));
    expect(onChange).toHaveBeenCalledWith("wick/wick");
  });

  test("arrow keys + Enter navigate the model list", async () => {
    const loadModels = vi.fn().mockResolvedValue([
      { id: "m-a", label: "Model A", default: false },
      { id: "m-b", label: "Model B", default: false },
    ]);
    const onChange = vi.fn();
    render(Composer, {
      props: {
        onSend: vi.fn(),
        provider: { options: [{ label: "wick", value: "wick/wick" }], value: "wick/wick", onChange, loadModels },
      },
    });
    await fireEvent.click(screen.getByRole("button", { name: /provider/i }));
    await waitFor(() => expect(screen.getByText("Model A")).toBeDefined());
    const search = screen.getByPlaceholderText(/Search wick models/i);
    // Rows: [0]=Use default, [1]=Model A, [2]=Model B. One down → Model A (idx 1),
    // Enter selects it.
    await fireEvent.keyDown(search, { key: "ArrowDown" });
    await fireEvent.keyDown(search, { key: "Enter" });
    expect(onChange).toHaveBeenCalledWith("wick/wick::m-a");
  });

  test("a live-set model row drills into its expansion (level 4)", async () => {
    const loadModels = vi.fn(async (_v: string, opts?: { entry?: string }) => {
      // Level 3 → the provider's rows (one plain model + one live set).
      if (!opts) return [
        { id: "m-plain", label: "Plain model", default: false },
        // The live set is ALSO the default — clicking it must still DRILL
        // (level 4), never auto-select, even though it's the default row.
        { id: "m_set", label: "claude code", default: true, live: true },
      ];
      // Level 4 → the live set's expanded vendor models.
      return [
        { id: "cc/claude-opus", label: "cc/claude-opus", default: false },
        { id: "cc/claude-sonnet", label: "cc/claude-sonnet", default: false },
      ];
    });
    const onChange = vi.fn();
    render(Composer, {
      props: {
        onSend: vi.fn(),
        provider: { options: [{ label: "wick", value: "wick/wick" }], value: "wick/wick", onChange, loadModels },
      },
    });
    await fireEvent.click(screen.getByRole("button", { name: /provider/i }));
    await waitFor(() => expect(screen.getByText("claude code")).toBeDefined());
    // Clicking the live set expands it (level 4), calling loadModels with the set opts.
    await fireEvent.click(screen.getByText("claude code"));
    await waitFor(() =>
      expect(loadModels).toHaveBeenCalledWith("wick/wick", expect.objectContaining({ entry: "m_set" })),
    );
    await waitFor(() => expect(screen.getByText("cc/claude-opus")).toBeDefined());
    // Picking an expanded model pins it as "<value>::<entryID>@<vendorID>" so
    // the backend resolves the live-set entry then overrides the model.
    await fireEvent.click(screen.getByText("cc/claude-sonnet"));
    expect(onChange).toHaveBeenCalledWith("wick/wick::m_set@cc/claude-sonnet");
  });

  test("live-set drills even when it's default AND already the pinned value", async () => {
    const loadModels = vi.fn(async (_v: string, opts?: { entry?: string }) => {
      if (!opts) return [{ id: "m_set", label: "claude code", default: true, live: true }];
      return [{ id: "cc/claude-fable-5", label: "cc/claude-fable-5", default: false }];
    });
    const onChange = vi.fn();
    render(Composer, {
      props: {
        onSend: vi.fn(),
        // Exactly the user's state: the live set is default AND the current pin.
        provider: {
          options: [{ label: "wick", value: "wick/wick", models: [{ id: "m_set", label: "claude code", default: true, live: true }] }],
          value: "wick/wick::m_set@cc/claude-haiku",
          onChange,
          loadModels,
        },
      },
    });
    await fireEvent.click(screen.getByRole("button", { name: /provider/i }));
    await fireEvent.click(await screen.findByText("claude code"));
    // Must drill to level 4, not select.
    await waitFor(() => expect(screen.getByText("cc/claude-fable-5")).toBeDefined());
    expect(onChange).not.toHaveBeenCalled();
  });

  test("arrow keys navigate the model drill and Enter selects the highlighted row", async () => {
    const loadModels = vi.fn().mockResolvedValue([
      { id: "m-a", label: "model a" },
      { id: "m-b", label: "model b" },
      { id: "m-c", label: "model c" },
    ]);
    const onChange = vi.fn();
    render(Composer, {
      props: {
        onSend: vi.fn(),
        provider: { options: [{ label: "wick", value: "wick/wick" }], value: "wick/wick", onChange, loadModels },
      },
    });
    await fireEvent.click(screen.getByRole("button", { name: /provider/i }));
    const search = await screen.findByPlaceholderText(/Search wick models/i);
    await waitFor(() => expect(screen.getByText("model a")).toBeDefined());
    // Rows: [0]=Use default, [1]=model a, [2]=model b, [3]=model c.
    // Two ArrowDowns from 0 → land on "model b", Enter selects it.
    await fireEvent.keyDown(search, { key: "ArrowDown" });
    await fireEvent.keyDown(search, { key: "ArrowDown" });
    await fireEvent.keyDown(search, { key: "Enter" });
    expect(onChange).toHaveBeenCalledWith("wick/wick::m-b");
  });

  test("arrow keys still work in the provider type list (menu-focused, no search box)", async () => {
    const onChange = vi.fn();
    render(Composer, {
      props: {
        onSend: vi.fn(),
        // Two distinct types → level-1 list, no auto-drill, no search input.
        provider: {
          options: [
            { label: "claude", value: "claude/a" },
            { label: "codex", value: "codex/b" },
          ],
          value: "claude/a",
          onChange,
        },
      },
    });
    await fireEvent.click(screen.getByRole("button", { name: /provider/i }));
    const menu = await screen.findByRole("menu");
    // Rows: [0]=claude, [1]=codex. ArrowDown → codex, Enter applies it.
    await fireEvent.keyDown(menu, { key: "ArrowDown" });
    await fireEvent.keyDown(menu, { key: "Enter" });
    expect(onChange).toHaveBeenCalledWith("codex/b");
  });

  test("opening the provider view auto-drills into the selected live set (level 4)", async () => {
    const loadModels = vi.fn(async (_v: string, opts?: { entry?: string }) => {
      if (!opts) return [{ id: "m_set", label: "claude code", default: true, live: true }];
      return [
        { id: "cc/claude-fable-5", label: "cc/claude-fable-5", default: false },
        { id: "cc/claude-haiku", label: "cc/claude-haiku", default: false },
      ];
    });
    const onChange = vi.fn();
    render(Composer, {
      props: {
        onSend: vi.fn(),
        // A live-set vendor model is already the selection.
        provider: {
          options: [{ label: "wick", value: "wick/wick" }],
          value: "wick/wick::m_set@cc/claude-haiku",
          onChange,
          loadModels,
        },
      },
    });
    // Just OPEN the provider view — no manual click on the set row.
    await fireEvent.click(screen.getByRole("button", { name: /provider/i }));
    // It must land on level 4 directly: the set's expanded vendor list is shown
    // and the header search is scoped to the set, without any extra interaction.
    await waitFor(() => expect(screen.getByText("cc/claude-fable-5")).toBeDefined());
    expect(screen.getByPlaceholderText(/Search claude code/i)).toBeDefined();
    expect(loadModels).toHaveBeenCalledWith("wick/wick", expect.objectContaining({ entry: "m_set" }));
    expect(onChange).not.toHaveBeenCalled();
  });

  test("second click on an open live set collapses it", async () => {
    const loadModels = vi.fn(async (_v: string, opts?: { entry?: string }) => {
      if (!opts) return [{ id: "m_set", label: "claude code", default: false, live: true }];
      return [{ id: "cc/opus", label: "cc/opus", default: false }];
    });
    render(Composer, {
      props: {
        onSend: vi.fn(),
        provider: { options: [{ label: "wick", value: "wick/wick" }], value: "wick/wick", onChange: vi.fn(), loadModels },
      },
    });
    await fireEvent.click(screen.getByRole("button", { name: /provider/i }));
    await fireEvent.click(await screen.findByText("claude code"));
    await waitFor(() => expect(screen.getByText("cc/opus")).toBeDefined()); // level 4 open
    // Click the live set row again (it's back in the level-3 list after collapse)…
    // Collapse is via the header back OR re-click; assert the header shows level 4 first.
    expect(screen.getByPlaceholderText(/Search claude code/i)).toBeDefined();
  });

  test("live-set drill survives a reactive provider prop change (re-derive/SSE)", async () => {
    const loadModels = vi.fn(async (_v: string, opts?: { entry?: string }) => {
      if (!opts) return [{ id: "m_set", label: "claude code", default: true, live: true }];
      return [{ id: "cc/opus", label: "cc/opus", default: false }];
    });
    // Simulate the conversation SPA: provider is a $derived that gets a NEW
    // object identity on every re-render (options rebuilt, same data).
    const mkProvider = () => ({
      options: [{ label: "wick", value: "wick/wick" }],
      value: "wick/wick",
      onChange: vi.fn(),
      loadModels,
    });
    const { rerender } = render(Composer, { props: { onSend: vi.fn(), provider: mkProvider() } });
    await fireEvent.click(screen.getByRole("button", { name: /provider/i }));
    await fireEvent.click(await screen.findByText("claude code"));
    await waitFor(() => expect(screen.getByText("cc/opus")).toBeDefined()); // level 4 shown
    // A re-render arrives (new provider object) — the drill must NOT reset.
    await rerender({ onSend: vi.fn(), provider: mkProvider() });
    expect(screen.getByText("cc/opus")).toBeDefined(); // still in level 4
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

  test("/provider opens the picker focused so arrow keys work without a click", async () => {
    const onChange = vi.fn();
    // The /provider command's run() opens the shared provider picker (mirrors
    // the app: the command routes to the component's openProvider()).
    let openProvider: (() => void) | undefined;
    const cmds = [{ value: "provider", label: "/provider", run: () => openProvider?.() }];
    const { component } = render(Composer, {
      props: {
        onSend: vi.fn(),
        commands: cmds,
        provider: {
          options: [
            { label: "claude", value: "claude/a" },
            { label: "codex", value: "codex/b" },
          ],
          value: "claude/a",
          onChange,
        },
      },
    });
    openProvider = (component as unknown as { openProvider: () => void }).openProvider;
    // Run /provider via the slash menu (Enter), exactly like the user.
    const textarea = screen.getByRole("textbox");
    await fireEvent.input(textarea, { target: { value: "/prov" } });
    await fireEvent.keyDown(textarea, { key: "Enter" });
    // The provider list is up and focused — arrow down + Enter must apply
    // WITHOUT any intervening click.
    const menu = await screen.findByRole("menu");
    await fireEvent.keyDown(menu, { key: "ArrowDown" });
    await fireEvent.keyDown(menu, { key: "Enter" });
    expect(onChange).toHaveBeenCalledWith("codex/b");
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

/* The menu reopening on the next keystroke made Esc worthless: it sat over
   the composer saying "No matches" while you finished typing a mention it
   was never going to complete. */
describe("Composer — a dismissed menu stays dismissed", () => {
  const menu = () => screen.queryByRole("textbox", { name: /search files/i });

  async function openThenEscape() {
    render(Composer, { props: { onSend: vi.fn(), mentionFiles: ["a.txt"] } });
    const textarea = screen.getByRole("textbox", { name: "" }) as HTMLTextAreaElement;
    await fireEvent.input(textarea, { target: { value: "@a" } });
    expect(menu()).not.toBeNull();
    await fireEvent.keyDown(textarea, { key: "Escape" });
    expect(menu()).toBeNull();
    return textarea;
  }

  test("typing more of the same mention does not reopen it", async () => {
    const textarea = await openThenEscape();
    await fireEvent.input(textarea, { target: { value: "@ap" } });
    expect(menu()).toBeNull();
  });

  test("clicking away also counts as a refusal", async () => {
    render(Composer, { props: { onSend: vi.fn(), mentionFiles: ["a.txt"] } });
    const textarea = screen.getByRole("textbox", { name: "" }) as HTMLTextAreaElement;
    await fireEvent.input(textarea, { target: { value: "@a" } });
    await fireEvent.mouseDown(document.body);
    await fireEvent.input(textarea, { target: { value: "@ap" } });
    expect(menu()).toBeNull();
  });

  // A `@` mention deliberately swallows spaces so a file search can be
  // "src main", which means everything after it on the LINE is still the
  // same token. A newline is what ends it.
  test("a mention on the next line is a new token and opens again", async () => {
    const textarea = await openThenEscape();
    await fireEvent.input(textarea, { target: { value: "@ap and more" } });
    expect(menu()).toBeNull();
    await fireEvent.input(textarea, { target: { value: "@ap and more\n@" } });
    expect(menu()).not.toBeNull();
  });

  test("deleting the mention re-arms it, so retyping @ works", async () => {
    const textarea = await openThenEscape();
    await fireEvent.input(textarea, { target: { value: "" } });
    expect(menu()).toBeNull();
    await fireEvent.input(textarea, { target: { value: "@" } });
    expect(menu()).not.toBeNull();
  });

  test("dismissing a mention does not silence the / command menu", async () => {
    render(Composer, {
      props: {
        onSend: vi.fn(),
        mentionFiles: ["a.txt"],
        commands: [{ value: "processes", label: "/processes" }],
      },
    });
    const textarea = screen.getByRole("textbox", { name: "" }) as HTMLTextAreaElement;
    await fireEvent.input(textarea, { target: { value: "@a" } });
    await fireEvent.keyDown(textarea, { key: "Escape" });
    await fireEvent.input(textarea, { target: { value: "@a /" } });
    expect(screen.queryByRole("textbox", { name: /search commands/i })).not.toBeNull();
  });
});
