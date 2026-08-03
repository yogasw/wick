import { render, screen, fireEvent } from "@testing-library/svelte";
import { describe, it, expect, vi } from "vitest";
import MessageThread from "./MessageThread.svelte";
import type { AgentMessageItem } from "../types/agents.js";

const msgs: AgentMessageItem[] = [
  {
    id: "1",
    from_handle: "main",
    to_handle: "reviewer",
    body: "start please",
    kind: "tell",
    created_at: "",
  },
  {
    id: "2",
    from_handle: "reviewer",
    to_handle: "main",
    body: "on it",
    kind: "reply",
    created_at: "",
  },
];

describe("MessageThread", () => {
  // Ordering alone cannot tell a live exchange from a stalled one.
  it("dates each message, with the exact time in a tooltip", () => {
    const sent = new Date(Date.now() - 7 * 60_000).toISOString();
    render(MessageThread, {
      messages: [{ ...msgs[0], created_at: sent }],
      onBumpHops: vi.fn(),
      hopsLeft: 4,
    });
    const stamp = screen.getByText("7m ago");
    expect(stamp.getAttribute("title")).toContain(String(new Date(sent).getFullYear()));
  });

  // Rows written before timestamps existed carry an empty created_at;
  // an empty tooltip on a blank label is worse than no label.
  it("renders no stamp when the message has no timestamp", () => {
    render(MessageThread, { messages: msgs, onBumpHops: vi.fn(), hopsLeft: 4 });
    expect(screen.queryByText(/ago$/)).toBeNull();
  });

  it("shows who spoke to whom", () => {
    render(MessageThread, { messages: msgs, onBumpHops: vi.fn(), hopsLeft: 4 });
    expect(screen.getByText("start please")).toBeTruthy();
    expect(screen.getAllByText("@main").length).toBeGreaterThan(0);
    expect(screen.getAllByText("@reviewer").length).toBeGreaterThan(0);
  });

  // The refill is a human act; offering it while hops remain invites a
  // click that does nothing and hides the limit's purpose.
  it("offers more hops only once they have run out", () => {
    const { unmount } = render(MessageThread, {
      messages: msgs,
      onBumpHops: vi.fn(),
      hopsLeft: 3,
    });
    expect(screen.queryByRole("button", { name: /allow 10 more/i })).toBeNull();
    unmount();

    render(MessageThread, { messages: msgs, onBumpHops: vi.fn(), hopsLeft: 0 });
    expect(screen.getByRole("button", { name: /allow 10 more/i })).toBeTruthy();
  });

  it("calls onBumpHops when the refill is clicked", async () => {
    const onBumpHops = vi.fn();
    render(MessageThread, { messages: msgs, onBumpHops, hopsLeft: 0 });
    await fireEvent.click(screen.getByRole("button", { name: /allow 10 more/i }));
    expect(onBumpHops).toHaveBeenCalledOnce();
  });

  // A salvaged closing turn often does not answer the question, so the
  // reader has to be able to tell it from a real reply.
  it("marks a reply that was promoted automatically", () => {
    render(MessageThread, {
      messages: [{ ...msgs[1]!, auto_reply: true }],
      onBumpHops: vi.fn(),
      hopsLeft: 5,
    });
    expect(screen.getByText(/sent as the answer automatically/i)).toBeTruthy();
  });

  it("says so when no agent has spoken yet", () => {
    render(MessageThread, { messages: [], onBumpHops: vi.fn(), hopsLeft: 5 });
    expect(screen.getByText(/no messages between agents yet/i)).toBeTruthy();
  });
});
