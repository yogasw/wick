import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import McpGrouping from "../McpGrouping.svelte";
import * as api from "$lib/api.js";

vi.mock("$lib/api.js");

const tools = [
  { name: "list-widgets-tool", description: "Requires widget.view." },
  { name: "get-widget-tool", description: "Requires widget.view." },
  { name: "sync-all-tool", description: "No permission line." },
];

const result = {
  rules: '{"strip_suffix":"-tool","other":"Other"}',
  preview: {
    groups: [
      { title: "Widget", tools: ["list-widgets-tool", "get-widget-tool"] },
      { title: "Other", tools: ["sync-all-tool"] },
    ],
    other: ["sync-all-tool"],
    orphan_pins: ["gone-tool"],
    placements: [
      { tool: "list-widgets-tool", group: "Widget", by: "template[0]" },
      { tool: "get-widget-tool", group: "Widget", by: "template[0]" },
      { tool: "sync-all-tool", group: "Other", by: "other" },
    ],
    total: 3,
  },
};

describe("McpGrouping", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.previewMcpGrouping).mockResolvedValue(result);
  });

  it("suggests a recipe, hands it to the parent and shows the groups", async () => {
    const onChange = vi.fn();
    render(McpGrouping, { props: { tools, rules: "", onChange } });
    await fireEvent.click(screen.getByText("Suggest from tools"));
    expect(api.previewMcpGrouping).toHaveBeenCalledWith(tools, "", true);
    expect(onChange).toHaveBeenCalledWith(result.rules);
    expect(await screen.findByText("Widget")).toBeTruthy();
    expect(screen.getByText(/1 in Other/)).toBeTruthy();
    expect(screen.getByText(/gone-tool/)).toBeTruthy();
  });

  it("disables the buttons until tools are loaded", () => {
    render(McpGrouping, { props: { tools: [], rules: "", onChange: vi.fn() } });
    expect((screen.getByText("Preview") as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText(/Run Test to load the tools first/)).toBeTruthy();
  });

  it("previews the stored recipe on load", async () => {
    render(McpGrouping, { props: { tools, rules: result.rules, onChange: vi.fn() } });
    expect(await screen.findByText("Widget")).toBeTruthy();
    expect(api.previewMcpGrouping).toHaveBeenCalledWith(tools, result.rules, false);
  });

  it("shows a server error", async () => {
    vi.mocked(api.previewMcpGrouping).mockRejectedValue(new Error("rules[0]: invalid pattern"));
    render(McpGrouping, { props: { tools, rules: '{"rules":[]}', onChange: vi.fn() } });
    await fireEvent.click(screen.getByText("Preview"));
    expect(await screen.findByText(/invalid pattern/)).toBeTruthy();
  });
});
