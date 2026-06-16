import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import ConfigsForm from "../ConfigsForm.svelte";
import * as api from "$lib/api.js";
import type { ConfigField } from "$lib/types.js";

vi.mock("$lib/api.js");

function field(over: Partial<ConfigField>): ConfigField {
  return {
    key: "k",
    type: "text",
    value: "",
    options: "",
    required: false,
    is_secret: false,
    has_value: false,
    description: "",
    visible_when: "",
    env_override: "",
    ...over,
  };
}

beforeEach(() => {
  vi.mocked(api.setConnectorConfig).mockResolvedValue(undefined);
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("ConfigsForm", () => {
  it("shows the empty state when no fields", () => {
    render(ConfigsForm, { connectorKey: "slack", connectorId: "1", fields: [], canConfigure: true });
    expect(screen.getByText("No configuration fields.")).toBeTruthy();
  });

  it("renders a missing badge for an unset required field", () => {
    render(ConfigsForm, {
      connectorKey: "slack",
      connectorId: "1",
      fields: [field({ key: "api_url", required: true })],
      canConfigure: true,
    });
    expect(screen.getByText("missing")).toBeTruthy();
  });

  it("renders a stored badge for a secret with a saved value", () => {
    render(ConfigsForm, {
      connectorKey: "slack",
      connectorId: "1",
      fields: [field({ key: "token", is_secret: true, has_value: true })],
      canConfigure: true,
    });
    expect(screen.getByText("stored")).toBeTruthy();
  });

  it("hides a field whose visible_when predicate is unmet", () => {
    render(ConfigsForm, {
      connectorKey: "slack",
      connectorId: "1",
      fields: [
        field({ key: "mode", type: "dropdown", options: "Basic::basic|Advanced::advanced", value: "basic" }),
        field({ key: "secret_key", visible_when: "mode:advanced" }),
      ],
      canConfigure: true,
    });
    expect(screen.queryByText("secret_key")).toBeNull();
    expect(screen.getByText("mode")).toBeTruthy();
  });

  it("reveals a visible_when field when its dependency flips", async () => {
    render(ConfigsForm, {
      connectorKey: "slack",
      connectorId: "1",
      fields: [
        field({ key: "mode", type: "dropdown", options: "Basic::basic|Advanced::advanced", value: "basic" }),
        field({ key: "secret_key", visible_when: "mode:advanced" }),
      ],
      canConfigure: true,
    });
    const select = screen.getByRole("combobox");
    await fireEvent.change(select, { target: { value: "advanced" } });
    expect(screen.getByText("secret_key")).toBeTruthy();
  });

  it("auto-saves a dropdown change immediately", async () => {
    render(ConfigsForm, {
      connectorKey: "slack",
      connectorId: "row1",
      fields: [field({ key: "mode", type: "dropdown", options: "A::a|B::b", value: "a" })],
      canConfigure: true,
    });
    const select = screen.getByRole("combobox");
    await fireEvent.change(select, { target: { value: "b" } });
    expect(api.setConnectorConfig).toHaveBeenCalledWith("slack", "row1", "mode", "b");
  });

  it("debounces a free-text save", async () => {
    vi.useFakeTimers();
    render(ConfigsForm, {
      connectorKey: "slack",
      connectorId: "row1",
      fields: [field({ key: "api_url", type: "text" })],
      canConfigure: true,
    });
    const input = screen.getByRole("textbox");
    await fireEvent.input(input, { target: { value: "https://x.test" } });
    expect(api.setConnectorConfig).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(900);
    expect(api.setConnectorConfig).toHaveBeenCalledWith("slack", "row1", "api_url", "https://x.test");
    vi.useRealTimers();
  });

  it("disables inputs when canConfigure is false", () => {
    render(ConfigsForm, {
      connectorKey: "slack",
      connectorId: "1",
      fields: [field({ key: "api_url", type: "text" })],
      canConfigure: false,
    });
    expect((screen.getByRole("textbox") as HTMLInputElement).disabled).toBe(true);
  });
});
