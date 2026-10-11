import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";

const saveTeamSettings = vi.fn();
vi.mock("../../../api/team.js", async (orig) => ({
  ...(await orig<typeof import("../../../api/team.js")>()),
  saveTeamSettings: (...a: unknown[]) => saveTeamSettings(...a),
  runApi: <T,>(p: Promise<T>) => p,
}));
const getProjectOptions = vi.fn();
vi.mock("../../../api/options.js", async (orig) => ({
  ...(await orig<typeof import("../../../api/options.js")>()),
  getProjectOptions: (...a: unknown[]) => getProjectOptions(...a),
}));

import TicketsPage from "../TicketsPage.svelte";

const options = () => [
  { id: "p2", name: "Beta", ticketEnabled: true },
  { id: "p1", name: "Alpha", ticketEnabled: true },
  { id: "p3", name: "No board", ticketEnabled: false },
];

function mount(selected: string[] | null, route = {}) {
  const onPicked = vi.fn();
  const onRoute = vi.fn();
  render(TicketsPage, { props: { base: "/tools/agents", selected, route, onPicked, onRoute } });
  return { onPicked, onRoute };
}

describe("TicketsPage", () => {
  beforeEach(() => {
    saveTeamSettings.mockReset();
    getProjectOptions.mockReset();
    getProjectOptions.mockResolvedValue(options());
  });

  test("nothing picked: only projects with ticket mode on, Done waits for a tick", async () => {
    mount([]);
    expect(await screen.findByText("Select projects first")).toBeTruthy();
    expect(await screen.findByLabelText(/Alpha/)).toBeTruthy();
    expect(screen.getByLabelText(/Beta/)).toBeTruthy();
    expect(screen.queryByLabelText(/No board/)).toBeNull();
    expect((screen.getByRole("button", { name: "Done" }) as HTMLButtonElement).disabled).toBe(true);
  });

  test("Done saves ticket_projects with the ticked ids, in the order picked", async () => {
    saveTeamSettings.mockImplementation((_b: string, body: Record<string, unknown>) => Promise.resolve({ prompt: "", open_team: false, idle_animations: true, max_prompt_bytes: 1, ...body }));
    const { onPicked } = mount([]);
    await fireEvent.click(await screen.findByLabelText(/Beta/));
    await fireEvent.click(screen.getByLabelText(/Alpha/));
    await fireEvent.click(screen.getByRole("button", { name: "Done · 2 selected" }));
    await waitFor(() => expect(saveTeamSettings).toHaveBeenCalledWith("/tools/agents", { ticket_projects: ["p2", "p1"] }));
    await waitFor(() => expect(onPicked).toHaveBeenCalledWith(expect.objectContaining({ ticket_projects: ["p2", "p1"] })));
  });

  test("a refused save stays on the picker with the reason", async () => {
    saveTeamSettings.mockRejectedValue(new Error("ticket_projects: no such project"));
    const { onPicked } = mount([]);
    await fireEvent.click(await screen.findByLabelText(/Alpha/));
    await fireEvent.click(screen.getByRole("button", { name: "Done · 1 selected" }));
    expect(await screen.findByText("ticket_projects: no such project")).toBeTruthy();
    expect(onPicked).not.toHaveBeenCalled();
  });

  test("with a selection: the board names the picked projects, no picker", async () => {
    mount(["p1", "p2"]);
    const board = await screen.findByTestId("tickets-board");
    await waitFor(() => expect(board.textContent).toContain("Alpha"));
    expect(board.textContent).toContain("Beta");
    expect(screen.queryByTestId("tickets-picker")).toBeNull();
  });

  test("a ticket route shows its page; ← Tickets goes back to the board", async () => {
    const { onRoute } = mount(["p1"], { project: "p1", ticket: "T-7" });
    const page = await screen.findByTestId("tickets-ticket");
    expect(page.dataset.ticket).toBe("T-7");
    await fireEvent.click(screen.getByRole("button", { name: "← Tickets" }));
    expect(onRoute).toHaveBeenCalledWith({});
  });

  test("settings failed to load: the reason and Retry, never the picker", async () => {
    const onRetry = vi.fn();
    render(TicketsPage, { props: { base: "/tools/agents", selected: null, route: {}, loadError: "boom", onRetry, onPicked: vi.fn(), onRoute: vi.fn() } });
    expect((await screen.findByTestId("tickets-load-error")).textContent).toContain("boom");
    expect(screen.queryByTestId("tickets-picker")).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(onRetry).toHaveBeenCalled();
  });

  test("a ticket link with nothing picked opens the ticket, not the picker", async () => {
    mount([], { project: "p1", ticket: "T-7" });
    expect((await screen.findByTestId("tickets-ticket")).dataset.ticket).toBe("T-7");
    expect(screen.queryByTestId("tickets-picker")).toBeNull();
  });
});
