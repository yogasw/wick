import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import ConnectorsIndex from "../ConnectorsIndex.svelte";
import * as api from "$lib/api.js";
import * as router from "$lib/router.js";
import type { ConnectorDef } from "$lib/types.js";

vi.mock("$lib/api.js");
vi.mock("$lib/router.js", async () => {
  const actual = await vi.importActual<typeof import("$lib/router.js")>("$lib/router.js");
  return { ...actual, push: vi.fn() };
});

function makeData(): ConnectorDef[] {
  return [
    { key: "slack", name: "Slack", category: "Communication", icon: "💬", custom: false, disabled: false },
    { key: "github", name: "GitHub", category: "Development", icon: "🐙", custom: false, disabled: false },
    { key: "my-curl", name: "My cURL", category: "Development", icon: "🔧", custom: true, disabled: true },
  ];
}

beforeEach(() => {
  vi.mocked(api.listConnectors).mockResolvedValue(makeData());
});

describe("ConnectorsIndex", () => {
  it("renders connector cards after load", async () => {
    render(ConnectorsIndex);
    expect(await screen.findByText("Slack")).toBeTruthy();
    expect(screen.getByText("GitHub")).toBeTruthy();
    expect(screen.getByText("My cURL")).toBeTruthy();
  });

  it("renders category filter chips from the loaded data", async () => {
    render(ConnectorsIndex);
    await screen.findByText("Slack");
    expect(screen.getByRole("button", { name: "Communication" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Development" })).toBeTruthy();
  });

  it("filters the grid by search query", async () => {
    render(ConnectorsIndex);
    await screen.findByText("Slack");
    const input = screen.getByLabelText("Search connectors");
    await fireEvent.input(input, { target: { value: "git" } });
    expect(screen.getByText("GitHub")).toBeTruthy();
    expect(screen.queryByText("Slack")).toBeNull();
  });

  it("filters the grid by category chip", async () => {
    render(ConnectorsIndex);
    await screen.findByText("Slack");
    await fireEvent.click(screen.getByRole("button", { name: "Communication" }));
    expect(screen.getByText("Slack")).toBeTruthy();
    expect(screen.queryByText("GitHub")).toBeNull();
  });

  it("shows an empty state when nothing matches", async () => {
    render(ConnectorsIndex);
    await screen.findByText("Slack");
    const input = screen.getByLabelText("Search connectors");
    await fireEvent.input(input, { target: { value: "zzz-nope" } });
    expect(screen.getByText("No connectors match your filters.")).toBeTruthy();
  });

  it("renders an error state on failure", async () => {
    vi.mocked(api.listConnectors).mockRejectedValueOnce(new Error("boom"));
    render(ConnectorsIndex);
    expect(await screen.findByText("boom")).toBeTruthy();
  });

  it("opens the New connector menu and navigates to the paste flow", async () => {
    vi.mocked(router.push).mockClear();
    render(ConnectorsIndex);
    await screen.findByText("Slack");
    await fireEvent.click(screen.getByRole("button", { name: "＋ New connector" }));
    await fireEvent.click(await screen.findByText("From paste"));
    expect(router.push).toHaveBeenCalledWith("/custom/paste");
  });

  it("navigates to the manual flow from the New connector menu", async () => {
    vi.mocked(router.push).mockClear();
    render(ConnectorsIndex);
    await screen.findByText("Slack");
    await fireEvent.click(screen.getByRole("button", { name: "＋ New connector" }));
    await fireEvent.click(await screen.findByText("Blank / manual"));
    expect(router.push).toHaveBeenCalledWith("/custom/manual");
  });
});
