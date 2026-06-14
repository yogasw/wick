import { describe, test, expect } from "vitest";
import { render, screen } from "@testing-library/svelte";
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
});
