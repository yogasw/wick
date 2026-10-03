import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";

const getTeamSettings = vi.fn();
const saveTeamSettings = vi.fn();
vi.mock("../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../api/team.js")>()),
  getTeamSettings: (...a: unknown[]) => getTeamSettings(...a),
  saveTeamSettings: (...a: unknown[]) => saveTeamSettings(...a),
  runApi: <T,>(p: Promise<T>) => p,
}));

import TeamSettings from "../TeamSettings.svelte";

const settings = (over: Record<string, unknown> = {}) => ({ prompt: "Be brief.", open_team: true, max_prompt_bytes: 16384, ...over });

function mount() {
  return render(TeamSettings, { props: { base: "/tools/agents", tab: "general", onTab: vi.fn(), onClose: vi.fn() } });
}

describe("TeamSettings", () => {
  beforeEach(() => {
    getTeamSettings.mockReset();
    saveTeamSettings.mockReset();
  });

  test("loads the caller's settings; no operator link for a non-admin", async () => {
    getTeamSettings.mockResolvedValue(settings());
    mount();
    expect(await screen.findByLabelText("Team prompt")).toHaveProperty("value", "Be brief.");
    expect(screen.getByRole("tab", { name: "General" }).getAttribute("aria-selected")).toBe("true");
    expect(screen.queryByTestId("operator-prompt-link")).toBeNull();
  });

  test("the toggle saves at once, sending only that field", async () => {
    getTeamSettings.mockResolvedValue(settings());
    saveTeamSettings.mockImplementation((_b: string, body: Record<string, unknown>) => Promise.resolve(settings(body)));
    mount();
    await screen.findByLabelText("Team prompt");
    await fireEvent.click(screen.getByRole("switch", { name: "Open Team when I open Agents" }));
    await waitFor(() => expect(saveTeamSettings).toHaveBeenCalledWith("/tools/agents", { open_team: false }));
    await waitFor(() => expect(screen.getByTestId("autosave-status").textContent).toContain("Saved"));
  });

  test("a prompt over the limit is not sent", async () => {
    getTeamSettings.mockResolvedValue(settings({ max_prompt_bytes: 10 }));
    mount();
    const ta = await screen.findByLabelText("Team prompt");
    await fireEvent.input(ta, { target: { value: "x".repeat(11) } });
    await fireEvent.focusOut(ta);
    expect(screen.getByTestId("autosave-status").textContent).toContain("Not saved");
    expect(saveTeamSettings).not.toHaveBeenCalled();
  });

  test("admins get the operator prompt link", async () => {
    getTeamSettings.mockResolvedValue(settings({ operator_prompt_href: "/tools/agents/settings" }));
    mount();
    const link = await screen.findByTestId("operator-prompt-link");
    expect(link.getAttribute("href")).toBe("/tools/agents/settings");
  });
});
