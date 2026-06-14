import { describe, test, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import ConversationHeader from "../ConversationHeader.svelte";
import type { LifecycleState } from "../../stores/thread.js";

function lc(state: LifecycleState["state"], opts: Partial<LifecycleState> = {}): LifecycleState {
  return { state, pid: opts.pid ?? 0, substate: opts.substate ?? "", at: opts.at ?? 0 };
}

const baseProps = {
  title: "Test Session",
  sseStatus: "connected" as const,
  onKill: () => {},
  onDelete: () => {},
  activeView: "conversation" as const,
  onTabChange: () => {},
};

describe("ConversationHeader", () => {
  test("renders session title", () => {
    render(ConversationHeader, { props: baseProps });
    expect(screen.getByText("Test Session")).toBeDefined();
  });

  test("shows SSE connected status", () => {
    const { container } = render(ConversationHeader, { props: baseProps });
    expect(container.innerHTML).toContain("live");
  });

  test("shows connecting status when SSE is connecting", () => {
    const { container } = render(ConversationHeader, {
      props: { ...baseProps, sseStatus: "connecting" },
    });
    expect(container.innerHTML).toContain("connecting");
  });

  test("shows error status when SSE has error", () => {
    const { container } = render(ConversationHeader, {
      props: { ...baseProps, sseStatus: "error" },
    });
    expect(container.innerHTML).toContain("error");
  });

  test("no lifecycle badge when lifecycle state is empty", () => {
    const { container } = render(ConversationHeader, {
      props: { ...baseProps, lifecycle: lc("") },
    });
    expect(container.querySelector("[data-lifecycle-badge]")).toBeNull();
  });

  test("no lifecycle badge when lifecycle is not provided", () => {
    const { container } = render(ConversationHeader, { props: baseProps });
    expect(container.querySelector("[data-lifecycle-badge]")).toBeNull();
  });

  test("lifecycle:working shows badge with working styling", () => {
    const { container } = render(ConversationHeader, {
      props: { ...baseProps, lifecycle: lc("working", { substate: "bash" }) },
    });
    const badge = container.querySelector("[data-lifecycle-badge]");
    expect(badge).not.toBeNull();
    expect(badge!.className).toContain("green");
    expect(container.innerHTML).toContain("bash");
  });

  test("lifecycle:spawning shows badge with amber styling", () => {
    const { container } = render(ConversationHeader, {
      props: { ...baseProps, lifecycle: lc("spawning") },
    });
    const badge = container.querySelector("[data-lifecycle-badge]");
    expect(badge).not.toBeNull();
    expect(badge!.className).toContain("amber");
    expect(container.innerHTML).toContain("spawning");
  });

  test("lifecycle:idle shows badge with blue styling", () => {
    const { container } = render(ConversationHeader, {
      props: { ...baseProps, lifecycle: lc("idle") },
    });
    const badge = container.querySelector("[data-lifecycle-badge]");
    expect(badge).not.toBeNull();
    expect(badge!.className).toContain("blue");
    expect(container.innerHTML).toContain("idle");
  });

  test("lifecycle:killed does not show badge (treated as neutral)", () => {
    const { container } = render(ConversationHeader, {
      props: { ...baseProps, lifecycle: lc("killed") },
    });
    expect(container.querySelector("[data-lifecycle-badge]")).toBeNull();
  });

  test("lifecycle:working without substate shows 'working' label", () => {
    const { container } = render(ConversationHeader, {
      props: { ...baseProps, lifecycle: lc("working") },
    });
    expect(container.innerHTML).toContain("working");
  });

  describe("idle countdown", () => {
    afterEach(() => {
      vi.useRealTimers();
    });

    test("idle state renders 'kill in' countdown text", () => {
      vi.useFakeTimers();
      const now = 1_000_000;
      vi.setSystemTime(now);
      /* 60 s remaining: at=now-60000, timeout=120000 → kill in 60s */
      const at = now - 60_000;
      const { container } = render(ConversationHeader, {
        props: {
          ...baseProps,
          lifecycle: lc("idle", { at }),
          idleTimeoutMs: 120_000,
        },
      });
      expect(container.innerHTML).toContain("kill in 60s");
    });

    test("idle state with at=0 falls back to entry time and shows countdown", () => {
      vi.useFakeTimers();
      const now = 2_000_000;
      vi.setSystemTime(now);
      const { container } = render(ConversationHeader, {
        props: {
          ...baseProps,
          lifecycle: lc("idle", { at: 0 }),
          idleTimeoutMs: 120_000,
        },
      });
      /* at=0 → falls back to Date.now() at entry → ~120s remaining */
      expect(container.innerHTML).toContain("kill in 120s");
    });

    test("working state does not show 'kill in' countdown", () => {
      const { container } = render(ConversationHeader, {
        props: {
          ...baseProps,
          lifecycle: lc("working"),
          idleTimeoutMs: 120_000,
        },
      });
      expect(container.innerHTML).not.toContain("kill in");
    });

    test("spawning state does not show 'kill in' countdown", () => {
      const { container } = render(ConversationHeader, {
        props: {
          ...baseProps,
          lifecycle: lc("spawning"),
          idleTimeoutMs: 120_000,
        },
      });
      expect(container.innerHTML).not.toContain("kill in");
    });
  });

  describe("tab dropdown", () => {
    test("renders the current tab label in the burger button", () => {
      render(ConversationHeader, { props: { ...baseProps, activeView: "conversation" } });
      const tabBtn = screen.getByRole("button", { name: /tab menu/i });
      expect(tabBtn.textContent).toContain("Conversation");
    });

    test("dropdown is hidden by default", () => {
      const { container } = render(ConversationHeader, { props: baseProps });
      expect(container.querySelector("[data-tab-dropdown]")).toBeNull();
    });

    test("clicking the burger button shows the 4 tab options", async () => {
      const { container } = render(ConversationHeader, { props: baseProps });
      const tabBtn = screen.getByRole("button", { name: /tab menu/i });
      await fireEvent.click(tabBtn);
      const dropdown = container.querySelector("[data-tab-dropdown]");
      expect(dropdown).not.toBeNull();
      expect(dropdown!.textContent).toContain("Conversation");
      expect(dropdown!.textContent).toContain("Commands");
      expect(dropdown!.textContent).toContain("Approvals");
      expect(dropdown!.textContent).toContain("Raw");
    });

    test("clicking a tab option calls onTabChange with the correct value", async () => {
      const onTabChange = vi.fn();
      render(ConversationHeader, { props: { ...baseProps, onTabChange } });
      await fireEvent.click(screen.getByRole("button", { name: /tab menu/i }));
      const approvalsBtn = screen.getByRole("button", { name: /^Approvals$/i });
      await fireEvent.click(approvalsBtn);
      expect(onTabChange).toHaveBeenCalledWith("approvals");
    });

    test("clicking the active tab still calls onTabChange", async () => {
      const onTabChange = vi.fn();
      render(ConversationHeader, {
        props: { ...baseProps, activeView: "conversation", onTabChange },
      });
      await fireEvent.click(screen.getByRole("button", { name: /tab menu/i }));
      await fireEvent.click(screen.getByRole("button", { name: /^Conversation$/i }));
      expect(onTabChange).toHaveBeenCalledWith("conversation");
    });

    test("header root element has relative positioning class to establish stacking context", () => {
      const { container } = render(ConversationHeader, { props: baseProps });
      const root = container.firstElementChild as HTMLElement;
      expect(root.className).toContain("relative");
    });

    test("dropdown panel has z-50 class so it paints above zone-2 content", async () => {
      const { container } = render(ConversationHeader, { props: baseProps });
      await fireEvent.click(screen.getByRole("button", { name: /tab menu/i }));
      const dropdown = container.querySelector("[data-tab-dropdown]") as HTMLElement;
      expect(dropdown).not.toBeNull();
      expect(dropdown.className).toContain("z-50");
    });
  });
});
