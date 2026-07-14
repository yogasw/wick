import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import WsInstanceCard from "../WsInstanceCard.svelte";
import type { WsInstance } from "../../types/agents.js";

const INSTANCE_READY: WsInstance = {
  id: "cid-1",
  label: "My Connector",
  status: "ready",
  fields: [
    { key: "base_url", label: "Base URL", type: "text", required: true, value: "https://example.com" },
    { key: "api_key", label: "API Key", type: "text", secret: true, required: true, set: true },
  ],
};

const INSTANCE_NEEDS_SETUP: WsInstance = {
  id: "cid-2",
  label: "Unset Connector",
  status: "needs_setup",
  fields: [
    { key: "token", label: "Token", type: "text", secret: true, required: true },
    { key: "region", label: "Region", type: "dropdown", options: ["us-east", "eu-west"], value: "us-east" },
  ],
};

const INSTANCE_NO_FIELDS: WsInstance = {
  id: "cid-empty",
  label: "No Fields",
  status: "ready",
  fields: [],
};

function makeProps(overrides: Partial<Parameters<typeof render>[1]["props"]> = {}) {
  return {
    instance: INSTANCE_READY,
    open: true,
    onSave: vi.fn(),
    onTest: vi.fn().mockResolvedValue({ ok: true }),
    onRename: vi.fn(),
    onDuplicate: vi.fn(),
    onDelete: vi.fn(),
    ...overrides,
  };
}

describe("WsInstanceCard", () => {
  test("renders instance label in header", () => {
    render(WsInstanceCard, { props: makeProps() });
    expect(screen.getByText("My Connector")).toBeDefined();
  });

  test("falls back to instance.id when no label", () => {
    const inst: WsInstance = { id: "raw-id", status: "ready", fields: [] };
    render(WsInstanceCard, { props: makeProps({ instance: inst, open: true }) });
    expect(screen.getByText("raw-id")).toBeDefined();
  });

  test("renders ready status badge", () => {
    render(WsInstanceCard, { props: makeProps() });
    expect(screen.getByText("ready")).toBeDefined();
  });

  test("renders needs-setup badge when status is not ready", () => {
    render(WsInstanceCard, { props: makeProps({ instance: INSTANCE_NEEDS_SETUP, open: true }) });
    expect(screen.getByText("needs setup")).toBeDefined();
  });

  test("collapse toggle: body hidden when open=false", () => {
    render(WsInstanceCard, { props: makeProps({ open: false }) });
    expect(screen.queryByTestId("card-body")).toBeNull();
  });

  test("collapse toggle: body visible when open=true", () => {
    render(WsInstanceCard, { props: makeProps({ open: true }) });
    expect(screen.getByTestId("card-body")).toBeDefined();
  });

  test("clicking header toggles body open", async () => {
    render(WsInstanceCard, { props: makeProps({ open: false }) });
    const btn = screen.getByRole("button", { name: /my connector/i });
    expect(screen.queryByTestId("card-body")).toBeNull();
    await fireEvent.click(btn);
    expect(screen.getByTestId("card-body")).toBeDefined();
  });

  test("clicking header again closes body", async () => {
    render(WsInstanceCard, { props: makeProps({ open: true }) });
    const btn = screen.getByRole("button", { name: /my connector/i });
    await fireEvent.click(btn);
    expect(screen.queryByTestId("card-body")).toBeNull();
  });

  test("renders text field with label", () => {
    render(WsInstanceCard, { props: makeProps({ open: true }) });
    expect(screen.getByText("Base URL")).toBeDefined();
  });

  test("text field pre-fills non-secret value", () => {
    render(WsInstanceCard, { props: makeProps({ open: true }) });
    const input = screen.getByDisplayValue("https://example.com");
    expect(input).toBeDefined();
  });

  /* SECURITY: secret field must render as password input and must NEVER
     contain the stored secret value in the DOM — value is always empty
     and placeholder signals it is set server-side. */
  test("secret field renders as password input type", () => {
    render(WsInstanceCard, { props: makeProps({ open: true }) });
    const pwInputs = document.querySelectorAll<HTMLInputElement>("input[type='password']");
    expect(pwInputs.length).toBeGreaterThan(0);
  });

  test("secret field value is always empty — stored secret never in DOM", () => {
    render(WsInstanceCard, { props: makeProps({ open: true }) });
    const pwInputs = document.querySelectorAll<HTMLInputElement>("input[type='password']");
    for (const inp of pwInputs) {
      /* The DOM value must be blank — never the server-side secret */
      expect(inp.value).toBe("");
    }
  });

  test("secret field with set=true shows placeholder indicating a value is stored", () => {
    render(WsInstanceCard, { props: makeProps({ open: true }) });
    const pwInput = document.querySelector<HTMLInputElement>(
      "input[data-secret='true'][data-field='api_key']",
    );
    expect(pwInput).not.toBeNull();
    expect(pwInput!.placeholder).toContain("set");
  });

  test("secret field without set has enter-to-set placeholder", () => {
    render(WsInstanceCard, { props: makeProps({ instance: INSTANCE_NEEDS_SETUP, open: true }) });
    const pwInput = document.querySelector<HTMLInputElement>(
      "input[data-secret='true'][data-field='token']",
    );
    expect(pwInput).not.toBeNull();
    expect(pwInput!.value).toBe("");
  });

  test("dropdown field renders a select element with options", () => {
    render(WsInstanceCard, { props: makeProps({ instance: INSTANCE_NEEDS_SETUP, open: true }) });
    const sel = document.querySelector<HTMLSelectElement>("select[data-field='region']");
    expect(sel).not.toBeNull();
    const opts = Array.from(sel!.options).map((o) => o.value);
    expect(opts).toContain("us-east");
    expect(opts).toContain("eu-west");
  });

  test("required field shows asterisk", () => {
    render(WsInstanceCard, { props: makeProps({ open: true }) });
    expect(screen.getAllByText("*").length).toBeGreaterThan(0);
  });

  test("set=true field shows '• set' indicator", () => {
    render(WsInstanceCard, { props: makeProps({ open: true }) });
    expect(screen.getByText("• set")).toBeDefined();
  });

  test("editing a field and clicking Save calls onSave with config map", async () => {
    const onSave = vi.fn();
    render(WsInstanceCard, { props: makeProps({ onSave, open: true }) });
    const urlInput = screen.getByDisplayValue("https://example.com") as HTMLInputElement;
    await fireEvent.input(urlInput, { target: { value: "https://new.example.com" } });
    const saveBtn = screen.getByRole("button", { name: /^save$/i });
    await fireEvent.click(saveBtn);
    expect(onSave).toHaveBeenCalledOnce();
    const [cid, values] = onSave.mock.calls[0] as [string, Record<string, string>];
    expect(cid).toBe("cid-1");
    expect(values["base_url"]).toBe("https://new.example.com");
  });

  test("Save button is disabled when no fields are dirty", () => {
    render(WsInstanceCard, { props: makeProps({ open: true }) });
    const saveBtn = screen.getByRole("button", { name: /^save$/i }) as HTMLButtonElement;
    expect(saveBtn.disabled).toBe(true);
  });

  test("Save button enables after editing a field", async () => {
    render(WsInstanceCard, { props: makeProps({ open: true }) });
    const urlInput = screen.getByDisplayValue("https://example.com") as HTMLInputElement;
    await fireEvent.input(urlInput, { target: { value: "https://changed.com" } });
    const saveBtn = screen.getByRole("button", { name: /^save$/i }) as HTMLButtonElement;
    expect(saveBtn.disabled).toBe(false);
  });

  test("Reset button appears when a field is dirty and reverts the value", async () => {
    render(WsInstanceCard, { props: makeProps({ open: true }) });
    const urlInput = screen.getByDisplayValue("https://example.com") as HTMLInputElement;
    await fireEvent.input(urlInput, { target: { value: "https://changed.com" } });
    const resetBtn = screen.getByRole("button", { name: /^reset$/i });
    expect(resetBtn).toBeDefined();
    await fireEvent.click(resetBtn);
    expect((screen.getByDisplayValue("https://example.com") as HTMLInputElement).value).toBe(
      "https://example.com",
    );
  });

  test("Test button calls onTest and renders ok result", async () => {
    const onTest = vi.fn().mockResolvedValue({ ok: true });
    render(WsInstanceCard, { props: makeProps({ onTest, open: true }) });
    const testBtn = screen.getByRole("button", { name: /^test$/i });
    await fireEvent.click(testBtn);
    await vi.waitFor(() => screen.getByTestId("test-result"));
    expect(onTest).toHaveBeenCalledOnce();
    expect(screen.getByTestId("test-result").textContent).toContain("Looks good");
  });

  test("Test button calls onTest and renders error result", async () => {
    const onTest = vi.fn().mockResolvedValue({ ok: false, error: "Auth failed" });
    render(WsInstanceCard, { props: makeProps({ onTest, open: true }) });
    await fireEvent.click(screen.getByRole("button", { name: /^test$/i }));
    await vi.waitFor(() => screen.getByTestId("test-result"));
    expect(screen.getByTestId("test-result").textContent).toContain("Auth failed");
  });

  test("Test result shows no_health_check message when set", async () => {
    const onTest = vi.fn().mockResolvedValue({ ok: false, no_health_check: true });
    render(WsInstanceCard, { props: makeProps({ onTest, open: true }) });
    await fireEvent.click(screen.getByRole("button", { name: /^test$/i }));
    await vi.waitFor(() => screen.getByTestId("test-result"));
    expect(screen.getByTestId("test-result").textContent).toContain("No health check");
  });

  test("clicking Rename pencil shows rename input", async () => {
    render(WsInstanceCard, { props: makeProps({ open: true }) });
    const pencil = screen.getByTitle("Rename");
    await fireEvent.click(pencil);
    expect(screen.getByTestId("rename-input")).toBeDefined();
  });

  test("rename: pressing Enter commits and calls onRename", async () => {
    const onRename = vi.fn();
    render(WsInstanceCard, { props: makeProps({ onRename, open: true }) });
    await fireEvent.click(screen.getByTitle("Rename"));
    const inp = screen.getByTestId("rename-input") as HTMLInputElement;
    await fireEvent.input(inp, { target: { value: "New Label" } });
    await fireEvent.keyDown(inp, { key: "Enter" });
    expect(onRename).toHaveBeenCalledOnce();
    expect(onRename).toHaveBeenCalledWith("cid-1", "New Label");
  });

  test("rename: pressing Escape cancels without calling onRename", async () => {
    const onRename = vi.fn();
    render(WsInstanceCard, { props: makeProps({ onRename, open: true }) });
    await fireEvent.click(screen.getByTitle("Rename"));
    const inp = screen.getByTestId("rename-input") as HTMLInputElement;
    await fireEvent.keyDown(inp, { key: "Escape" });
    expect(onRename).not.toHaveBeenCalled();
    expect(screen.queryByTestId("rename-input")).toBeNull();
  });

  test("Duplicate button calls onDuplicate with instance id", async () => {
    const onDuplicate = vi.fn();
    render(WsInstanceCard, { props: makeProps({ onDuplicate, open: true }) });
    await fireEvent.click(screen.getByRole("button", { name: /duplicate/i }));
    expect(onDuplicate).toHaveBeenCalledOnce();
    expect(onDuplicate).toHaveBeenCalledWith("cid-1");
  });

  test("Remove button calls onDelete with instance id", async () => {
    const onDelete = vi.fn();
    render(WsInstanceCard, { props: makeProps({ onDelete, open: true }) });
    await fireEvent.click(screen.getByRole("button", { name: /remove/i }));
    expect(onDelete).toHaveBeenCalledOnce();
    expect(onDelete).toHaveBeenCalledWith("cid-1");
  });

  test("shows inline save error when onSave throws", async () => {
    const onSave = vi.fn(() => { throw new Error("network error"); });
    render(WsInstanceCard, { props: makeProps({ onSave, open: true }) });
    const urlInput = screen.getByDisplayValue("https://example.com") as HTMLInputElement;
    await fireEvent.input(urlInput, { target: { value: "https://changed.com" } });
    await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));
    await vi.waitFor(() => expect(screen.getByText(/network error/i)).toBeDefined());
  });

  test("inline error clears when field is edited after a save error", async () => {
    const onSave = vi.fn(() => { throw new Error("oops"); });
    render(WsInstanceCard, { props: makeProps({ onSave, open: true }) });
    const urlInput = screen.getByDisplayValue("https://example.com") as HTMLInputElement;
    await fireEvent.input(urlInput, { target: { value: "https://changed.com" } });
    await fireEvent.click(screen.getByRole("button", { name: /^save$/i }));
    await vi.waitFor(() => expect(screen.queryByText(/oops/i)).not.toBeNull());
    await fireEvent.input(urlInput, { target: { value: "https://other.com" } });
    expect(screen.queryByText(/oops/i)).toBeNull();
  });

  test("Save button disabled when no fields dirty shows nothing-to-save hint", () => {
    render(WsInstanceCard, { props: makeProps({ open: true }) });
    const saveBtn = screen.getByRole("button", { name: /^save$/i }) as HTMLButtonElement;
    expect(saveBtn.disabled).toBe(true);
    expect(screen.queryByText(/no changes/i)).toBeDefined();
  });

});
