import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import TerminalPanel from "../TerminalPanel.svelte";
import * as term from "$lib/terminal.js";

vi.mock("$lib/terminal.js", () => ({
  apiTerminalStatus: vi.fn(),
  apiTerminalStart: vi.fn(),
  apiTerminalClose: vi.fn(),
}));
vi.mock("$lib/managedbin.js", async (importOriginal) => {
  const orig = await importOriginal<typeof import("$lib/managedbin.js")>();
  return { ...orig, apiManagedList: vi.fn(async () => ({ types: [], isAdmin: true })) };
});
vi.mock("@wick-fe/common-stores", () => ({
  toastOk: vi.fn(),
  toastWarn: vi.fn(),
  toastError: vi.fn(),
  toasts: { subscribe: vi.fn(() => vi.fn()) },
}));

const commands = [
  { key: "tui", label: "opencode" },
  { key: "auth-login", label: "opencode auth login" },
  { key: "auth-list", label: "opencode auth list" },
];

async function openSection() {
  render(TerminalPanel, { base: "/tools/agents", type: "opencode", name: "scratch" });
  await fireEvent.click(await screen.findByText("Terminal"));
  return screen.findByTestId("open-terminal");
}

beforeEach(() => {
  localStorage.clear();
  vi.mocked(term.apiTerminalStatus).mockReset();
  vi.mocked(term.apiTerminalStart).mockReset();
  vi.mocked(term.apiTerminalClose).mockReset().mockResolvedValue(undefined);
});

describe("TerminalPanel", () => {
  it("opens the picked allowlisted command in a modal iframe and kills it on close", async () => {
    vi.mocked(term.apiTerminalStatus).mockResolvedValue({ commands, gottyInstalled: true });
    vi.mocked(term.apiTerminalStart).mockResolvedValue({ id: "abc", url: "/tools/agents/api/providers/opencode/scratch/terminal/abc/", command: "opencode auth list" });
    const btn = await openSection();
    await waitFor(() => expect((btn as HTMLButtonElement).disabled).toBe(false));

    const sel = screen.getByTestId("terminal-command") as HTMLSelectElement;
    expect([...sel.options].map((o) => o.textContent)).toEqual(commands.map((c) => c.label));
    await fireEvent.change(sel, { target: { value: "auth-list" } });
    await fireEvent.click(btn);

    expect(term.apiTerminalStart).toHaveBeenCalledWith("/tools/agents", "opencode", "scratch", "auth-list");
    const modal = await screen.findByTestId("terminal-modal");
    expect(modal.querySelector("iframe")?.getAttribute("src")).toBe("/tools/agents/api/providers/opencode/scratch/terminal/abc/");

    await fireEvent.keyDown(window, { key: "Escape" });
    await waitFor(() => expect(screen.queryByTestId("terminal-modal")).toBeNull());
    expect(term.apiTerminalClose).toHaveBeenCalledWith("/tools/agents", "opencode", "scratch", "abc");
  });

  it("keeps Open terminal disabled until gotty is installed", async () => {
    vi.mocked(term.apiTerminalStatus).mockResolvedValue({ commands, gottyInstalled: false });
    const btn = await openSection();
    await waitFor(() => expect(term.apiTerminalStatus).toHaveBeenCalled());
    expect((btn as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText("gotty not installed")).toBeTruthy();
  });
});
