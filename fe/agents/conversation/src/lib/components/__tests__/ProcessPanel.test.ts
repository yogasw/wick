import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import ProcessPanel from "../ProcessPanel.svelte";
import type { ProcessInfo } from "../../types/agents.js";

const PROC_ACTIVE: ProcessInfo = {
  session_id: "sess-abc123",
  agent_name: "claude-sonnet",
  provider: "anthropic",
  pid: 4242,
  queued: 0,
  lifecycle: "working",
  alive: true,
};

const PROC_QUEUED: ProcessInfo = {
  session_id: "sess-def456",
  agent_name: "gpt-4o",
  provider: "openai",
  pid: 0,
  queued: 2,
  lifecycle: "queued",
  alive: false,
};

const PROC_WITH_SUBSTATE: ProcessInfo = {
  session_id: "sess-ghi789",
  agent_name: "claude-opus",
  provider: "anthropic",
  pid: 5050,
  queued: 0,
  lifecycle: "idle",
  substate: "waiting_tool",
  alive: true,
};

describe("ProcessPanel", () => {
  test("shows empty state when no processes", () => {
    render(ProcessPanel, {
      props: { processes: [], onKill: vi.fn(), onDequeue: vi.fn() },
    });
    expect(screen.getByText(/no active processes/i)).toBeDefined();
  });

  test("renders agent name and provider for a row", () => {
    render(ProcessPanel, {
      props: { processes: [PROC_ACTIVE], onKill: vi.fn(), onDequeue: vi.fn() },
    });
    expect(screen.getByText("claude-sonnet")).toBeDefined();
    expect(screen.getByText("anthropic")).toBeDefined();
  });

  test("renders lifecycle badge", () => {
    render(ProcessPanel, {
      props: { processes: [PROC_ACTIVE], onKill: vi.fn(), onDequeue: vi.fn() },
    });
    expect(screen.getByText("working")).toBeDefined();
  });

  test("renders pid value", () => {
    render(ProcessPanel, {
      props: { processes: [PROC_ACTIVE], onKill: vi.fn(), onDequeue: vi.fn() },
    });
    expect(screen.getByText("4242")).toBeDefined();
  });

  test("renders queued count when queued > 0", () => {
    render(ProcessPanel, {
      props: { processes: [PROC_QUEUED], onKill: vi.fn(), onDequeue: vi.fn() },
    });
    expect(screen.getByText(/2 waiting/)).toBeDefined();
  });

  test("renders substate when present", () => {
    render(ProcessPanel, {
      props: { processes: [PROC_WITH_SUBSTATE], onKill: vi.fn(), onDequeue: vi.fn() },
    });
    expect(screen.getByText("waiting_tool")).toBeDefined();
  });

  test("non-queued row shows Kill button and calls onKill with session_id", async () => {
    const onKill = vi.fn();
    render(ProcessPanel, {
      props: { processes: [PROC_ACTIVE], onKill, onDequeue: vi.fn() },
    });
    const btn = screen.getByRole("button", { name: /kill/i });
    await fireEvent.click(btn);
    expect(onKill).toHaveBeenCalledOnce();
    expect(onKill).toHaveBeenCalledWith("sess-abc123");
  });

  test("queued row shows Cancel button and calls onDequeue with session_id", async () => {
    const onDequeue = vi.fn();
    render(ProcessPanel, {
      props: { processes: [PROC_QUEUED], onKill: vi.fn(), onDequeue },
    });
    const btn = screen.getByRole("button", { name: /cancel/i });
    await fireEvent.click(btn);
    expect(onDequeue).toHaveBeenCalledOnce();
    expect(onDequeue).toHaveBeenCalledWith("sess-def456");
  });

  test("multiple rows are all rendered", () => {
    render(ProcessPanel, {
      props: {
        processes: [PROC_ACTIVE, PROC_QUEUED],
        onKill: vi.fn(),
        onDequeue: vi.fn(),
      },
    });
    expect(screen.getByText("claude-sonnet")).toBeDefined();
    expect(screen.getByText("gpt-4o")).toBeDefined();
  });

  test("dead process (alive=false, not queued) shows dead lifecycle badge", () => {
    const deadProc: ProcessInfo = { ...PROC_ACTIVE, alive: false, lifecycle: "working" };
    render(ProcessPanel, {
      props: { processes: [deadProc], onKill: vi.fn(), onDequeue: vi.fn() },
    });
    expect(screen.getByText("dead")).toBeDefined();
  });

  test("pid=0 renders em-dash placeholder", () => {
    render(ProcessPanel, {
      props: { processes: [PROC_QUEUED], onKill: vi.fn(), onDequeue: vi.fn() },
    });
    expect(screen.getByText("—")).toBeDefined();
  });

  test("renders both rows when two processes share the same session_id without crashing", () => {
    const procA: ProcessInfo = {
      session_id: "shared-sess",
      agent_name: "agent-alpha",
      provider: "anthropic",
      pid: 1111,
      queued: 0,
      lifecycle: "working",
      alive: true,
    };
    const procB: ProcessInfo = {
      session_id: "shared-sess",
      agent_name: "agent-beta",
      provider: "openai",
      pid: 2222,
      queued: 0,
      lifecycle: "idle",
      alive: true,
    };

    render(ProcessPanel, {
      props: { processes: [procA, procB], onKill: vi.fn(), onDequeue: vi.fn() },
    });

    expect(screen.getByText("agent-alpha")).toBeDefined();
    expect(screen.getByText("agent-beta")).toBeDefined();
  });

  describe("#24 lifecycle badge tokens", () => {
    const cases: Array<{ lc: string; badge: string; alive?: boolean }> = [
      { lc: "working",  badge: "bg-pos-100 text-pos-400",   alive: true },
      { lc: "idle",     badge: "bg-prog-100 text-prog-400", alive: true },
      { lc: "spawning", badge: "bg-cau-100 text-cau-400",   alive: true },
      { lc: "queued",   badge: "bg-cau-100 text-cau-400",   alive: false },
      { lc: "killed",   badge: "bg-neg-100 text-neg-400",   alive: true },
    ];

    for (const { lc, badge, alive } of cases) {
      test(`lifecycle="${lc}" badge has classes ${badge}`, () => {
        const proc: ProcessInfo = {
          session_id: "badge-test",
          agent_name: "test-agent",
          provider: "anthropic",
          pid: lc === "queued" ? 0 : 9999,
          queued: 0,
          lifecycle: lc,
          alive: alive ?? true,
        };
        render(ProcessPanel, {
          props: { processes: [proc], onKill: vi.fn(), onDequeue: vi.fn() },
        });
        const badgeEl = screen.getByText(lc);
        for (const cls of badge.split(" ")) {
          expect(badgeEl.classList.contains(cls)).toBe(true);
        }
      });
    }
  });

  describe("#32 lifecycle dot in ProcessPanel", () => {
    test("working row has a dot with class bg-pos-400", () => {
      render(ProcessPanel, {
        props: { processes: [PROC_ACTIVE], onKill: vi.fn(), onDequeue: vi.fn() },
      });
      const badge = screen.getByText("working");
      const dot = badge.previousElementSibling;
      expect(dot).not.toBeNull();
      expect(dot!.classList.contains("bg-pos-400")).toBe(true);
    });

    test("idle row has a dot with class bg-prog-400", () => {
      render(ProcessPanel, {
        props: { processes: [PROC_WITH_SUBSTATE], onKill: vi.fn(), onDequeue: vi.fn() },
      });
      const badge = screen.getByText("idle");
      const dot = badge.previousElementSibling;
      expect(dot).not.toBeNull();
      expect(dot!.classList.contains("bg-prog-400")).toBe(true);
    });

    test("queued row has a dot with class bg-orange-500 and animate-pulse", () => {
      render(ProcessPanel, {
        props: { processes: [PROC_QUEUED], onKill: vi.fn(), onDequeue: vi.fn() },
      });
      const badge = screen.getByText("queued");
      const dot = badge.previousElementSibling;
      expect(dot).not.toBeNull();
      expect(dot!.classList.contains("bg-orange-500")).toBe(true);
      expect(dot!.classList.contains("animate-pulse")).toBe(true);
    });
  });
});
