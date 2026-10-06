import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import { describe, it, expect, vi } from "vitest";
import SessionFieldChips from "./SessionFieldChips.svelte";
import DraftChat from "./DraftChat.svelte";
import type { AgentItem, SessionField } from "../api/team.js";

const fields: SessionField[] = [
  { key: "source", label: "Repository", placeholder: "owner/repo", default: "acme/app" },
  { key: "branch", label: "Branch", placeholder: "repository default" },
];

const button = () => screen.getByRole("button", { name: "Repository and branch" });

describe("SessionFieldChips", () => {
  it("shows the defaults on the toolbar button, then saves an override", async () => {
    const onChange = vi.fn();
    render(SessionFieldChips, { fields, values: {}, onChange });
    expect(button().textContent).toContain("acme/app");

    await fireEvent.click(button());
    const input = screen.getByRole("textbox", { name: "Branch" });
    expect((input as HTMLInputElement).placeholder).toBe("repository default");
    await fireEvent.input(input, { target: { value: " release " } });
    await fireEvent.keyDown(input, { key: "Enter" });
    expect(onChange).toHaveBeenLastCalledWith({ branch: "release" });
  });

  it("joins every value on the button", () => {
    render(SessionFieldChips, { fields, values: { source: "o/other", branch: "dev" } });
    expect(button().textContent).toContain("o/other · dev");
  });

  it("resets the overrides to the defaults", async () => {
    const onChange = vi.fn();
    render(SessionFieldChips, { fields, values: { source: "o/other", branch: "dev" }, onChange });
    await fireEvent.click(button());
    await fireEvent.click(screen.getByRole("button", { name: "Reset to defaults" }));
    expect(onChange).toHaveBeenLastCalledWith({});
  });

  it("is read-only once the session started", async () => {
    const onChange = vi.fn();
    const { container } = render(SessionFieldChips, { fields, values: { source: "o/app", branch: "release" }, locked: true, onChange });
    expect(container.querySelector("[data-session-fields]")?.getAttribute("data-locked")).toBe("true");
    expect(button().textContent).toContain("o/app · release");
    await fireEvent.click(button());
    expect(screen.queryByRole("textbox")).toBeNull();
    expect(container.querySelector('[data-session-field="branch"]')?.textContent).toContain("release");
    expect(screen.queryByRole("button", { name: "Reset to defaults" })).toBeNull();
    expect(onChange).not.toHaveBeenCalled();
  });

  it("closes the popover on Escape", async () => {
    const { container } = render(SessionFieldChips, { fields, values: {} });
    await fireEvent.click(button());
    expect(container.querySelector("[data-session-fields-popover]")).not.toBeNull();
    await fireEvent.keyDown(window, { key: "Escape" });
    expect(container.querySelector("[data-session-fields-popover]")).toBeNull();
  });
});

describe("DraftChat session fields", () => {
  const agent = { id: "a1", name: "Jules", kind: "plugin-remote" } as unknown as AgentItem;

  it("puts the fields' button in the composer toolbar", async () => {
    render(DraftChat, { agent, onSend: vi.fn(async () => {}), loadFields: async () => fields });
    await waitFor(() => expect(button().textContent).toContain("acme/app"));
  });

  it("shows no button for an agent without fields", () => {
    const { container } = render(DraftChat, { agent, onSend: vi.fn(async () => {}) });
    expect(container.querySelector("[data-session-fields]")).toBeNull();
  });
});
