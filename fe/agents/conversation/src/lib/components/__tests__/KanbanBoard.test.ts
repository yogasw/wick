import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import KanbanBoard from "../KanbanBoard.svelte";
import type { TicketBoard } from "../../types/agents.js";

const board: TicketBoard = {
  config: {
    enabled: true,
    fields: [{ key: "priority", label: "Priority", type: "select", options: ["low", "high"] }],
  },
  statuses: ["open", "in_progress", "waiting", "done"],
  me: "u-me",
  users: { "u-me": "Me Myself", "u-2": "Other Person" },
  tickets: [
    {
      session_id: "sess-open-1",
      title: "Fix the webhook",
      status: "open",
      assignee: "u-me",
      fields: { priority: "high" },
      updated_at: "2026-08-22T00:00:00Z",
      last_active: "2026-08-22T00:00:00Z",
      stale: true,
      owner_id: "u-me",
    },
    {
      session_id: "sess-done-1",
      title: "Old finished thing",
      status: "done",
      assignee: "u-2",
      last_active: "2026-08-01T00:00:00Z",
      stale: false,
      owner_id: "u-2",
    },
  ],
};

function renderBoard(overrides: Partial<Parameters<typeof render>[1]["props"]> = {}) {
  const onFilter = vi.fn();
  const onSelect = vi.fn();
  const utils = render(KanbanBoard, {
    props: { base: "/tools/agents", board, filter: {}, onFilter, onSelect, ...overrides },
  });
  return { ...utils, onFilter, onSelect };
}

describe("KanbanBoard", () => {
  test("renders one column per status with cards in place", () => {
    renderBoard();
    expect(screen.getByText("Fix the webhook")).toBeTruthy();
    expect(screen.getByText("Old finished thing")).toBeTruthy();
    // 4 column headers
    for (const label of ["Open", "In Progress", "Waiting", "Done"]) {
      expect(screen.getAllByText(label).length).toBeGreaterThan(0);
    }
    // custom field + stale badge visible on the card
    expect(screen.getByText("Priority: high")).toBeTruthy();
    expect(screen.getByText("stale")).toBeTruthy();
  });

  test("clicking a card selects its session", async () => {
    const { onSelect } = renderBoard();
    await fireEvent.click(screen.getByTestId("ticket-card-sess-open-1"));
    expect(onSelect).toHaveBeenCalledWith("sess-open-1");
  });

  test("toggling a status chip emits the reduced filter", async () => {
    const { onFilter } = renderBoard();
    // Chips are the buttons with aria-pressed; click "Done" to hide it.
    const chips = screen.getAllByRole("button", { pressed: true });
    const done = chips.find((c) => c.textContent?.trim() === "Done")!;
    await fireEvent.click(done);
    expect(onFilter).toHaveBeenCalledWith({
      statuses: ["open", "in_progress", "waiting"],
    });
  });

  test("assignee 'me' filter hides other people's tickets", () => {
    renderBoard({ filter: { assignee: "me" } });
    expect(screen.getByText("Fix the webhook")).toBeTruthy();
    expect(screen.queryByText("Old finished thing")).toBeNull();
  });
});
