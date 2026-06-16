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
    {
      key: "slack",
      name: "Slack",
      description: "Send messages and manage channels",
      category: "Communication",
      category_desc: "Chat + messaging platforms",
      icon: "💬",
      op_count: 5,
      active_count: 2,
      needs_setup_count: 0,
      disabled_count: 0,
      system: false,
      custom: false,
      custom_source: "",
      needs_reload: false,
      disabled: false,
    },
    {
      key: "github",
      name: "GitHub",
      description: "Repos, issues, and pull requests",
      category: "Development",
      category_desc: "Dev tooling + source control",
      icon: "🐙",
      op_count: 8,
      active_count: 1,
      needs_setup_count: 0,
      disabled_count: 0,
      system: false,
      custom: false,
      custom_source: "",
      needs_reload: false,
      disabled: false,
    },
    {
      key: "my-curl",
      name: "My cURL",
      description: "Imported from a cURL command",
      category: "Development",
      category_desc: "Dev tooling + source control",
      icon: "🔧",
      op_count: 3,
      active_count: 0,
      needs_setup_count: 0,
      disabled_count: 1,
      system: false,
      custom: true,
      custom_source: "cURL",
      needs_reload: false,
      disabled: true,
    },
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

  it("groups connectors into category sections with subtitles", async () => {
    const { container } = render(ConnectorsIndex);
    await screen.findByText("Slack");
    const sections = container.querySelectorAll("[data-group]");
    expect(sections.length).toBe(2);
    const names = Array.from(sections).map((s) => s.getAttribute("data-group-name"));
    expect(names).toContain("Communication");
    expect(names).toContain("Development");
    expect(screen.getByText("Chat + messaging platforms")).toBeTruthy();
    expect(screen.getByText("Dev tooling + source control")).toBeTruthy();
  });

  it("renders each connector description and op-count stats", async () => {
    render(ConnectorsIndex);
    await screen.findByText("Slack");
    expect(screen.getByText("Send messages and manage channels")).toBeTruthy();
    expect(screen.getByText("5 operation(s) · 2 active")).toBeTruthy();
    expect(screen.getByText("8 operation(s) · 1 active")).toBeTruthy();
  });

  it("renders the Custom badge with its source for custom connectors", async () => {
    render(ConnectorsIndex);
    await screen.findByText("My cURL");
    expect(screen.getByText("Custom · cURL")).toBeTruthy();
    expect(screen.getByText(/1 disabled/)).toBeTruthy();
  });

  it("renders the page subtitle", async () => {
    render(ConnectorsIndex);
    await screen.findByText("Slack");
    expect(screen.getByText(/LLM-callable connectors that wrap external APIs/)).toBeTruthy();
  });

  it("renders the '/' keyboard hint next to search", async () => {
    render(ConnectorsIndex);
    await screen.findByText("Slack");
    expect(screen.getByText("/")).toBeTruthy();
  });

  it("focuses the search box when '/' is pressed", async () => {
    render(ConnectorsIndex);
    await screen.findByText("Slack");
    await fireEvent.keyDown(window, { key: "/" });
    expect(document.activeElement).toBe(screen.getByLabelText("Search connectors"));
  });
});
