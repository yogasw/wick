import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/svelte";
import ToolDetail from "../ToolDetail.svelte";
import * as api from "$lib/api.js";
import type { ToolDetail as ToolDetailType } from "$lib/types.js";

vi.mock("$lib/api.js");

function makeTool(over: Partial<ToolDetailType> = {}): ToolDetailType {
  return {
    key: "echo",
    name: "Echo",
    description: "Echoes input.",
    icon: "🔁",
    can_configure: true,
    fields: [{ key: "prefix", type: "text", value: ">>", options: "", required: false, is_secret: false, has_value: true, description: "", visible_when: "", env_override: "" }],
    ...over,
  };
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(api.getTool).mockResolvedValue(makeTool());
});

describe("ToolDetail", () => {
  it("renders the tool header + config field", async () => {
    render(ToolDetail, { toolKey: "echo" });
    expect(await screen.findByText("Echo")).toBeTruthy();
    expect(screen.getByText("prefix")).toBeTruthy();
  });

  it("surfaces a load error", async () => {
    vi.mocked(api.getTool).mockRejectedValue(new Error("boom"));
    render(ToolDetail, { toolKey: "echo" });
    expect(await screen.findByText("boom")).toBeTruthy();
  });
});
