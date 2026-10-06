import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import { describe, it, expect, vi } from "vitest";
import SessionFieldChips from "./SessionFieldChips.svelte";
import DraftChat from "./DraftChat.svelte";
import type { AgentItem, SessionField } from "../api/team.js";

const fields: SessionField[] = [
  { key: "source", label: "Repository", placeholder: "owner/repo", default: "acme/app" },
  { key: "branch", label: "Branch", placeholder: "repository default" },
];

describe("SessionFieldChips", () => {
  it("shows each default, then saves an inline override", async () => {
    const onChange = vi.fn();
    render(SessionFieldChips, { fields, values: {}, onChange });
    expect(screen.getByRole("button", { name: "Repository" }).textContent).toContain("acme/app");
    // No default: the plugin's placeholder, muted.
    expect(screen.getByRole("button", { name: "Branch" }).textContent).toContain("repository default");

    await fireEvent.click(screen.getByRole("button", { name: "Branch" }));
    const input = screen.getByRole("textbox", { name: "Branch" });
    await fireEvent.input(input, { target: { value: " release " } });
    await fireEvent.keyDown(input, { key: "Enter" });
    expect(onChange).toHaveBeenLastCalledWith({ branch: "release" });
  });

  it("resets a chip to its default with ×", async () => {
    const onChange = vi.fn();
    render(SessionFieldChips, { fields, values: { source: "o/other", branch: "dev" }, onChange });
    await fireEvent.click(screen.getByRole("button", { name: "Reset Repository" }));
    expect(onChange).toHaveBeenLastCalledWith({ branch: "dev" });
  });

  it("is read-only once the session started", async () => {
    const onChange = vi.fn();
    const { container } = render(SessionFieldChips, { fields, values: { source: "o/app", branch: "release" }, locked: true, onChange });
    expect(container.querySelector("[data-session-fields]")?.getAttribute("data-locked")).toBe("true");
    const repo = screen.getByRole("button", { name: "Repository" }) as HTMLButtonElement;
    expect(repo.disabled).toBe(true);
    expect(repo.textContent).toContain("o/app");
    expect(screen.queryByRole("button", { name: "Reset Repository" })).toBeNull();
    await fireEvent.click(repo);
    expect(screen.queryByRole("textbox")).toBeNull();
    expect(onChange).not.toHaveBeenCalled();
  });
});

describe("DraftChat session fields", () => {
  const agent = { id: "a1", name: "Jules", kind: "plugin-remote" } as unknown as AgentItem;

  it("fills the chips with the plugin's defaults", async () => {
    render(DraftChat, { agent, onSend: vi.fn(async () => {}), loadFields: async () => fields });
    await waitFor(() => expect(screen.getByRole("button", { name: "Repository" }).textContent).toContain("acme/app"));
  });

  it("shows no chips for an agent without fields", () => {
    const { container } = render(DraftChat, { agent, onSend: vi.fn(async () => {}) });
    expect(container.querySelector("[data-session-fields]")).toBeNull();
  });
});
