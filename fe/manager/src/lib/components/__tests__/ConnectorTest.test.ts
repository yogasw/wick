import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import ConnectorTest from "../ConnectorTest.svelte";
import * as api from "$lib/api.js";
import * as router from "$lib/router.js";
import type { TestMeta } from "$lib/types.js";

vi.mock("$lib/api.js");
vi.mock("$lib/router.js", () => ({ push: vi.fn() }));

function makeMeta(over: Partial<TestMeta> = {}): TestMeta {
  return {
    key: "slack",
    name: "Slack",
    icon: "💬",
    id: "row-a",
    label: "Prod",
    ops: [
      {
        key: "send",
        name: "Send",
        description: "Send a message",
        destructive: false,
        input: [
          { key: "channel", type: "text", required: true, description: "target channel" },
          { key: "text", type: "textarea", required: false, description: "" },
        ],
      },
      { key: "del", name: "Delete", description: "Delete a message", destructive: true, input: [] },
    ],
    accounts: [],
    ...over,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  history.replaceState({}, "", "/modules/manager/app/connectors/slack/row-a/test");
  vi.mocked(api.getTestMeta).mockResolvedValue(makeMeta());
});

describe("ConnectorTest", () => {
  it("renders the op picker and the first op's input form", async () => {
    render(ConnectorTest, { connectorKey: "slack", connectorId: "row-a" });
    expect(await screen.findByText("Test runner")).toBeTruthy();
    expect(screen.getByLabelText("channel")).toBeTruthy();
    expect(screen.getByLabelText("text")).toBeTruthy();
  });

  it("syncs the selected op to the URL via ?op=", async () => {
    render(ConnectorTest, { connectorKey: "slack", connectorId: "row-a" });
    await screen.findByText("Test runner");
    expect(window.location.search).toBe("?op=send");
  });

  it("preselects the op from the URL ?op= param", async () => {
    history.replaceState({}, "", "/modules/manager/app/connectors/slack/row-a/test?op=del");
    render(ConnectorTest, { connectorKey: "slack", connectorId: "row-a" });
    await screen.findByText("This operation takes no input.");
    expect(window.location.search).toBe("?op=del");
  });

  it("runs the active op and renders status + latency + response", async () => {
    vi.mocked(api.runConnectorTest).mockResolvedValue({
      operation: "send",
      status: "success",
      latency_ms: 42,
      response: { ok: "yes" },
    });
    render(ConnectorTest, { connectorKey: "slack", connectorId: "row-a" });
    await screen.findByText("Test runner");
    await fireEvent.input(screen.getByLabelText("channel"), { target: { value: "#general" } });
    await fireEvent.click(screen.getByRole("button", { name: "Run" }));
    expect(await screen.findByText("success")).toBeTruthy();
    expect(screen.getByText("42 ms")).toBeTruthy();
    expect(api.runConnectorTest).toHaveBeenCalledWith(
      "slack",
      "row-a",
      "send",
      { channel: "#general", text: "" },
      "",
    );
  });

  it("renders an error result when the run fails", async () => {
    vi.mocked(api.runConnectorTest).mockResolvedValue({
      operation: "send",
      status: "error",
      error: "boom",
    });
    render(ConnectorTest, { connectorKey: "slack", connectorId: "row-a" });
    await screen.findByText("Test runner");
    await fireEvent.click(screen.getByRole("button", { name: "Run" }));
    expect(await screen.findByText("error")).toBeTruthy();
    expect(screen.getByText("boom")).toBeTruthy();
  });

  it("navigates to history via the View history button", async () => {
    render(ConnectorTest, { connectorKey: "slack", connectorId: "row-a" });
    await screen.findByText("Test runner");
    await fireEvent.click(screen.getByRole("button", { name: "View history" }));
    expect(router.push).toHaveBeenCalledWith("/connectors/slack/row-a/history");
  });

  it("shows an empty state when the connector has no operations", async () => {
    vi.mocked(api.getTestMeta).mockResolvedValue(makeMeta({ ops: [] }));
    render(ConnectorTest, { connectorKey: "slack", connectorId: "row-a" });
    expect(await screen.findByText("This connector exposes no operations.")).toBeTruthy();
  });

  it("renders the H1 at the legacy 1.375rem size", async () => {
    render(ConnectorTest, { connectorKey: "slack", connectorId: "row-a" });
    const h1 = await screen.findByRole("heading", { level: 1, name: "Test runner" });
    expect(h1.className).toContain("text-[1.375rem]");
  });
});
