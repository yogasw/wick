import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import type { SlackDirectory } from "../../../api/team.js";

let reply: SlackDirectory = { entries: [] };
const search = vi.fn((_b: string, _p: Record<string, string>) => Promise.resolve(reply));
vi.mock("../../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../../api/team.js")>()),
  searchSlackDirectory: (b: string, p: Record<string, string>) => search(b, p),
  runApi: <T,>(p: Promise<T>) => p,
}));

import SlackDirectoryPicker from "../SlackDirectoryPicker.svelte";

const base = { base: "/tools/agents", connectorId: "slk1", identity: "bot" as const, accountId: "", inputId: "t-user", idLabel: "User or bot ID", idPlaceholder: "U0123ABCD" };

describe("SlackDirectoryPicker", () => {
  beforeEach(() => { search.mockClear(); reply = { entries: [] }; });

  test("typing searches the workspace; picking a bot fills id and display name", async () => {
    reply = { entries: [
      { id: "U1", name: "alpha.tester", real_name: "Alpha Tester", display_name: "alpha" },
      { id: "B1", name: "helper-bot", real_name: "Helper Bot", is_bot: true },
    ] };
    const r = render(SlackDirectoryPicker, { ...base, kind: "users", value: "", name: "" });
    await fireEvent.input(screen.getByRole("combobox"), { target: { value: "help" } });
    await waitFor(() => expect(search).toHaveBeenCalledWith("/tools/agents", expect.objectContaining({ connectorId: "slk1", kind: "users", q: "help" })));
    const opt = await screen.findByRole("option", { name: /Helper Bot/ });
    expect(opt.textContent).toContain("BOT");
    expect(screen.getByRole("option", { name: /Alpha Tester/ }).textContent).not.toContain("BOT");
    await fireEvent.mouseDown(opt);
    expect((screen.getByLabelText("User or bot ID") as HTMLInputElement).value).toBe("B1");
    expect(r.container.textContent).toContain("Picked B1 · @helper-bot");
  });

  test("channels: picking fills the channel id and #name", async () => {
    reply = { entries: [{ id: "C1", name: "ops-alerts" }, { id: "C2", name: "ops-secret", is_private: true }] };
    render(SlackDirectoryPicker, { ...base, kind: "channels", inputId: "t-ch", idLabel: "Channel ID", value: "", name: "" });
    await fireEvent.input(screen.getByRole("combobox"), { target: { value: "ops" } });
    await fireEvent.mouseDown(await screen.findByRole("option", { name: /ops-alerts/ }));
    expect((screen.getByLabelText("Channel ID") as HTMLInputElement).value).toBe("C1");
  });

  test("a missing scope shows the reason and opens the manual id field", async () => {
    reply = { entries: [], error: "this Slack token cannot list users and bots: add the users:read scope, or enter the ID manually", missing_scope: true };
    const r = render(SlackDirectoryPicker, { ...base, kind: "users", value: "", name: "" });
    await fireEvent.input(screen.getByRole("combobox"), { target: { value: "a" } });
    expect((await screen.findByRole("alert")).textContent).toContain("users:read");
    await waitFor(() => expect(r.container.querySelector("details")?.open).toBe(true));
    await fireEvent.input(screen.getByLabelText("User or bot ID"), { target: { value: "U9" } });
    expect((screen.getByLabelText("User or bot ID") as HTMLInputElement).value).toBe("U9");
  });

  test("without a workspace there is no search, only the id field", () => {
    render(SlackDirectoryPicker, { ...base, connectorId: "", kind: "users", value: "", name: "" });
    expect(screen.queryByRole("combobox")).toBeNull();
    expect(screen.getByLabelText("User or bot ID")).toBeTruthy();
  });
});
