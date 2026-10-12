import { describe, test, expect } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import ThreadMessage from "../ThreadMessage.svelte";
import type { ConversationTurn } from "../../types/agents.js";

function systemTurn(kind: string, extras: Record<string, string>): ConversationTurn {
  return {
    turn_id: "t-fo",
    role: "system",
    agent: "main",
    provider: "",
    text: "Provider failed over → codex/waba",
    timestamp: 0,
    truncated: false,
    interrupted: false,
    has_trace: false,
    events: [],
    attachments: [],
    kind,
    extras,
  };
}

describe("ThreadMessage - provider failover notices", () => {
  test("failover card: from → to · reason, continue text folded", async () => {
    render(ThreadMessage, {
      props: {
        turn: systemTurn("provider_failover", {
          from: "claude/mine",
          to: "codex/waba",
          home: "claude/mine",
          reason: "usage limit",
          note: "History stays on claude/mine.",
          continue: "Continue from where the previous provider stopped.",
        }),
      },
    });
    const card = screen.getByTestId("provider-failover-card");
    expect(card.getAttribute("data-kind")).toBe("provider_failover");
    expect(screen.getByTestId("provider-failover-label").textContent).toBe("Auto-failover: claude/mine → codex/waba · usage limit");
    expect(screen.getByTestId("provider-failover-note").textContent).toBe("History stays on claude/mine.");
    const det = screen.getByTestId("provider-failover-continue") as HTMLDetailsElement;
    expect(det.open).toBe(false);
    expect(det.textContent).toContain("Auto-continued on codex/waba");
    await fireEvent.click(screen.getByText("Auto-continued on codex/waba"));
    expect(det.textContent).toContain("Continue from where the previous provider stopped.");
  });

  test("failover card without continue text has no fold", () => {
    render(ThreadMessage, { props: { turn: systemTurn("provider_failover", { from: "a/a", to: "b/b" }) } });
    expect(screen.getByTestId("provider-failover-label").textContent).toBe("Auto-failover: a/a → b/b");
    expect(screen.queryByTestId("provider-failover-continue")).toBeNull();
  });

  test("return card: Back to <home>", () => {
    render(ThreadMessage, {
      props: { turn: systemTurn("provider_return", { from: "codex/waba", to: "claude/mine", home: "claude/mine", continue: "x" }) },
    });
    expect(screen.getByTestId("provider-failover-card").getAttribute("data-kind")).toBe("provider_return");
    expect(screen.getByTestId("provider-failover-label").textContent).toBe("Back to claude/mine");
    expect(screen.queryByTestId("provider-failover-continue")).toBeNull();
  });

  test("empty extras fall back to generic labels", () => {
    const { unmount } = render(ThreadMessage, { props: { turn: systemTurn("provider_failover", {}) } });
    expect(screen.getByTestId("provider-failover-label").textContent).toBe("Auto-failover: provider → next provider");
    expect(screen.queryByTestId("provider-failover-note")).toBeNull();
    expect(screen.queryByTestId("provider-failover-continue")).toBeNull();
    unmount();
    render(ThreadMessage, { props: { turn: systemTurn("provider_return", {}) } });
    expect(screen.getByTestId("provider-failover-label").textContent).toBe("Back to the home provider");
  });

  test("markup in extras renders as text, never as elements", () => {
    render(ThreadMessage, {
      props: {
        turn: systemTurn("provider_failover", {
          from: "<b>a</b>",
          to: "b/b",
          reason: "<img src=x onerror=alert(1)>",
          note: "<i>n</i>",
          continue: "<b>x</b><script>alert(1)</script>",
        }),
      },
    });
    const card = screen.getByTestId("provider-failover-card");
    expect(screen.getByTestId("provider-failover-label").textContent).toContain("<b>a</b>");
    expect(screen.getByTestId("provider-failover-label").textContent).toContain("<img src=x onerror=alert(1)>");
    expect(screen.getByTestId("provider-failover-continue").textContent).toContain("<b>x</b><script>alert(1)</script>");
    expect(card.querySelector("b, i, img, script")).toBeNull();
  });
});

