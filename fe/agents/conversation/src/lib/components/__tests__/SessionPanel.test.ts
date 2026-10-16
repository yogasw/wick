import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import SessionPanel from "../SessionPanel.svelte";
import type { NotesResponse } from "../../types/agents.js";

const info = (over: Partial<NotesResponse> = {}): NotesResponse => ({
  notes: [],
  users: { u1: "Yoga Setiawan" },
  session_info: { started_by: "u1", project_id: "p1", project_name: "Franky", created_at: new Date().toISOString() },
  session_people: [
    { id: "u1", name: "Yoga Setiawan", messages: 12, owner: true },
    { id: "slack:U1", name: "Hana", messages: 2 },
  ],
  ...over,
});

describe("SessionPanel", () => {
  test("Info, home agent first, the agents that replied and the people", () => {
    render(SessionPanel, {
      info: info({ ticket: { id: "T-R67R", title: "Cek badge Loki", status: "doing" } }),
      homeName: "Captain",
      homeOwner: "@yoga",
      agents: [{ key: "a", name: "Log Investigator", owner: "@hana", joined: "via @mention", replies: 3 }],
      onOpenTicket: () => {},
      onDiscuss: () => {},
    });
    expect(screen.getByTestId("session-started-by").textContent).toBe("Yoga Setiawan");
    expect(screen.getByText("Franky")).toBeTruthy();
    expect(screen.getByTestId("session-agent-home").textContent).toContain("home");
    expect(screen.getByTestId("session-agent-home").textContent).toContain("Answers messages without @");
    expect(screen.getByTestId("session-agent").textContent).toContain("via @mention · 3 replies");
    const people = screen.getAllByTestId("session-person").map((e) => e.textContent ?? "");
    expect(people[0]).toContain("owner · 12 messages");
    expect(people[1]).toContain("2 messages");
  });

  // An internal "slack:U1" key is no name for a person.
  test("a writer with no name or handle is named by their channel", () => {
    render(SessionPanel, {
      info: info({ session_people: [{ id: "slack:U9", channel: "slack", messages: 1 }] }),
      homeName: "Captain",
      agents: [],
    });
    const [p] = screen.getAllByTestId("session-person").map((e) => e.textContent ?? "");
    expect(p).toContain("Unknown slack user");
    expect(p).not.toContain("slack:U9");
  });

  test("ticket link opens the ticket; Discuss in a group only on a ticket", async () => {
    const onOpenTicket = vi.fn();
    const onDiscuss = vi.fn();
    const { unmount } = render(SessionPanel, { info: info({ ticket: { id: "T-1", title: "One", status: "todo" } }), homeName: "Captain", agents: [], onOpenTicket, onDiscuss });
    await fireEvent.click(screen.getByRole("button", { name: /One/ }));
    expect(onOpenTicket).toHaveBeenCalledWith("T-1");
    await fireEvent.click(screen.getByTestId("session-discuss-in-group"));
    expect(onDiscuss).toHaveBeenCalled();
    unmount();
    render(SessionPanel, { info: info(), homeName: "Project agent", agents: [], onDiscuss });
    expect(screen.queryByTestId("session-discuss-in-group")).toBeNull();
    expect(screen.getByTestId("session-ticket").textContent).toContain("—");
  });
});
