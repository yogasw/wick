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
  test("renders tab label (title is no longer displayed directly in header)", () => {
    const { container } = render(ConversationHeader, { props: baseProps });
    // header now shows tab dropdown, not title text
    expect(container.innerHTML).toContain("Conversation");
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

  test("lifecycle:working shows badge with working styling and 'working' label", () => {
    const { container } = render(ConversationHeader, {
      props: { ...baseProps, lifecycle: lc("working", { substate: "bash" }) },
    });
    const badge = container.querySelector("[data-lifecycle-badge]");
    expect(badge).not.toBeNull();
    expect(badge!.className).toContain("green");
    /* label reflects the lifecycle state, not the substate (which can leak the provider name) */
    expect(badge!.textContent).toContain("working");
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

  test("lifecycle:killed shows a 'killed' badge", () => {
    const { container } = render(ConversationHeader, {
      props: { ...baseProps, lifecycle: lc("killed") },
    });
    const badge = container.querySelector("[data-lifecycle-badge]");
    expect(badge).not.toBeNull();
    expect(badge!.textContent).toContain("killed");
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

    test("idle countdown expired shows 'killed' instead of 'idle · 0s'", () => {
      vi.useFakeTimers();
      const now = 5_000_000;
      vi.setSystemTime(now);
      /* at far in the past → remaining <= 0 → spawn auto-killed */
      const at = now - 200_000;
      const { container } = render(ConversationHeader, {
        props: {
          ...baseProps,
          lifecycle: lc("idle", { at }),
          idleTimeoutMs: 120_000,
        },
      });
      const badge = container.querySelector("[data-lifecycle-badge]");
      expect(badge!.textContent).toContain("killed");
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
      render(ConversationHeader, { props: baseProps });
      expect(document.querySelector("[data-tab-dropdown]")).toBeNull();
    });

    test("clicking the burger button shows the 4 tab options", async () => {
      render(ConversationHeader, { props: baseProps });
      const tabBtn = screen.getByRole("button", { name: /tab menu/i });
      await fireEvent.click(tabBtn);
      const dropdown = document.querySelector("[data-tab-dropdown]");
      expect(dropdown).not.toBeNull();
      expect(dropdown!.textContent).toContain("Conversation");
      expect(dropdown!.textContent).toContain("Commands");
      expect(dropdown!.textContent).toContain("Approvals");
      expect(dropdown!.textContent).toContain("Raw");
    });

    test("dropdown panel has fixed positioning class to escape ancestor overflow clipping", async () => {
      render(ConversationHeader, { props: baseProps });
      await fireEvent.click(screen.getByRole("button", { name: /tab menu/i }));
      const dropdown = document.querySelector("[data-tab-dropdown]") as HTMLElement;
      expect(dropdown).not.toBeNull();
      expect(dropdown.className).toContain("fixed");
    });

    test("dropdown panel has z-[1000] to render above all stacking contexts", async () => {
      render(ConversationHeader, { props: baseProps });
      await fireEvent.click(screen.getByRole("button", { name: /tab menu/i }));
      const dropdown = document.querySelector("[data-tab-dropdown]") as HTMLElement;
      expect(dropdown).not.toBeNull();
      expect(dropdown.className).toContain("z-[1000]");
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

    test("clicking a tab item closes the dropdown", async () => {
      render(ConversationHeader, { props: baseProps });
      await fireEvent.click(screen.getByRole("button", { name: /tab menu/i }));
      expect(document.querySelector("[data-tab-dropdown]")).not.toBeNull();
      await fireEvent.click(screen.getByRole("button", { name: /^Commands$/i }));
      expect(document.querySelector("[data-tab-dropdown]")).toBeNull();
    });

    test("Escape key closes the dropdown", async () => {
      render(ConversationHeader, { props: baseProps });
      await fireEvent.click(screen.getByRole("button", { name: /tab menu/i }));
      expect(document.querySelector("[data-tab-dropdown]")).not.toBeNull();
      await fireEvent.keyDown(window, { key: "Escape" });
      expect(document.querySelector("[data-tab-dropdown]")).toBeNull();
    });

    test("clicking outside the dropdown closes it", async () => {
      render(ConversationHeader, { props: baseProps });
      await fireEvent.click(screen.getByRole("button", { name: /tab menu/i }));
      expect(document.querySelector("[data-tab-dropdown]")).not.toBeNull();
      await fireEvent.click(document.body);
      expect(document.querySelector("[data-tab-dropdown]")).toBeNull();
    });

    test("header root element has positioning class to establish stacking context", () => {
      const { container } = render(ConversationHeader, { props: baseProps });
      const root = container.firstElementChild as HTMLElement;
      // header is now absolute floating (transparent bar), not relative
      expect(root.className).toMatch(/absolute|relative/);
    });
  });

  describe("agent label", () => {
    test("bare type shows just the type", () => {
      const { container } = render(ConversationHeader, {
        props: { ...baseProps, agentLabel: "codex" },
      });
      expect(container.textContent).toContain("codex");
      expect(container.textContent).not.toContain("codex/");
    });

    test("type/type collapses to the type only", () => {
      const { container } = render(ConversationHeader, {
        props: { ...baseProps, agentLabel: "codex/codex" },
      });
      expect(container.textContent).toContain("codex");
      // instance half is suppressed when name === type
      expect(container.textContent).not.toContain("codex/");
    });

    test("named instance shows type and instance name", () => {
      const { container } = render(ConversationHeader, {
        props: { ...baseProps, agentLabel: "codex/gemini_flash" },
      });
      expect(container.textContent).toContain("codex");
      expect(container.textContent).toContain("gemini_flash");
    });
  });

  /* The header used to say nothing about delegated work: with the leader
     idle and a sub-agent grinding away, the only green thing on screen was
     the SSE "live" pill, which means "connected", not "busy". */
  describe("sub-agent badge", () => {
    test("absent when no sub-agent is working", () => {
      const { container } = render(ConversationHeader, {
        props: { ...baseProps, lifecycle: lc("idle"), subAgentsBusy: 0 },
      });
      expect(container.querySelector("[data-subagent-badge]")).toBeNull();
    });

    test("shows a count while sub-agents work", () => {
      const { container } = render(ConversationHeader, {
        props: { ...baseProps, lifecycle: lc("idle"), subAgentsBusy: 2 },
      });
      const badge = container.querySelector("[data-subagent-badge]");
      expect(badge).not.toBeNull();
      expect(badge?.textContent).toContain("2");
      expect(badge?.textContent).toContain("sub-agents");
    });

    test("singular for one", () => {
      const { container } = render(ConversationHeader, {
        props: { ...baseProps, lifecycle: lc("idle"), subAgentsBusy: 1 },
      });
      const badge = container.querySelector("[data-subagent-badge]");
      expect(badge?.textContent).toContain("1 sub-agent");
      expect(badge?.textContent).not.toContain("sub-agents");
    });

    /* The leader's own badge is not rewritten: claiming "working" while its
       process is idle would be a lie, and the point of a separate badge is
       that both facts stay readable. */
    test("does not alter the leader's lifecycle badge", () => {
      const { container } = render(ConversationHeader, {
        props: { ...baseProps, lifecycle: lc("idle"), subAgentsBusy: 3 },
      });
      expect(container.querySelector("[data-lifecycle-label]")?.textContent).toBe("idle");
      expect(container.querySelector("[data-subagent-badge]")).not.toBeNull();
    });

    /* Delegated work outlives the leader's process — killing the leader is
       one way to end up with only sub-agents running — so the badge must
       not depend on a lifecycle badge being present. */
    test("shows even when the leader has no lifecycle at all", () => {
      const { container } = render(ConversationHeader, {
        props: { ...baseProps, lifecycle: lc(""), subAgentsBusy: 1 },
      });
      expect(container.querySelector("[data-lifecycle-badge]")).toBeNull();
      expect(container.querySelector("[data-subagent-badge]")).not.toBeNull();
    });
  });
});
