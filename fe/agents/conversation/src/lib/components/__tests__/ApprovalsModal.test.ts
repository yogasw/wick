import { describe, test, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import ApprovalsModal from "../ApprovalsModal.svelte";
import type { ApprovalRequest, ApprovalDecision } from "../../types/agents.js";

const REQ: ApprovalRequest = {
  id: "appr-1",
  agent_name: "claude",
  tool: "bash",
  work_dir: "/home/user/project",
  cmd: "git status",
  match_key: "sha256:abc123",
};

describe("ApprovalsModal", () => {
  test("renders nothing when request is null", () => {
    const { container } = render(ApprovalsModal, {
      props: { request: null, onDecide: vi.fn() },
    });
    expect(container.querySelector("div")).toBeNull();
  });

  test("renders agent_name when request is provided", () => {
    render(ApprovalsModal, { props: { request: REQ, onDecide: vi.fn() } });
    expect(screen.getByText("claude")).toBeDefined();
  });

  test("renders tool when request is provided", () => {
    render(ApprovalsModal, { props: { request: REQ, onDecide: vi.fn() } });
    expect(screen.getByText("bash")).toBeDefined();
  });

  test("renders work_dir when request is provided", () => {
    render(ApprovalsModal, { props: { request: REQ, onDecide: vi.fn() } });
    expect(screen.getByText("/home/user/project")).toBeDefined();
  });

  test("renders cmd when request is provided", () => {
    render(ApprovalsModal, { props: { request: REQ, onDecide: vi.fn() } });
    expect(screen.getByText("git status")).toBeDefined();
  });

  test("clicking 'Approve once' calls onDecide with approve_once", async () => {
    const onDecide = vi.fn();
    render(ApprovalsModal, { props: { request: REQ, onDecide } });
    await fireEvent.click(screen.getByText("Approve once"));
    expect(onDecide).toHaveBeenCalledOnce();
    expect(onDecide).toHaveBeenCalledWith("approve_once" satisfies ApprovalDecision);
  });

  test("clicking 'Allow this session' calls onDecide with approve_session", async () => {
    const onDecide = vi.fn();
    render(ApprovalsModal, { props: { request: REQ, onDecide } });
    await fireEvent.click(screen.getByText("Allow this session"));
    expect(onDecide).toHaveBeenCalledOnce();
    expect(onDecide).toHaveBeenCalledWith("approve_session" satisfies ApprovalDecision);
  });

  test("clicking 'Always allow' calls onDecide with approve_always", async () => {
    const onDecide = vi.fn();
    render(ApprovalsModal, { props: { request: REQ, onDecide } });
    await fireEvent.click(screen.getByText("Always allow"));
    expect(onDecide).toHaveBeenCalledOnce();
    expect(onDecide).toHaveBeenCalledWith("approve_always" satisfies ApprovalDecision);
  });

  test("clicking 'Block' calls onDecide with block", async () => {
    const onDecide = vi.fn();
    render(ApprovalsModal, { props: { request: REQ, onDecide } });
    await fireEvent.click(screen.getByText("Block"));
    expect(onDecide).toHaveBeenCalledOnce();
    expect(onDecide).toHaveBeenCalledWith("block" satisfies ApprovalDecision);
  });

  test("renders a starting countdown value when request is provided", () => {
    render(ApprovalsModal, { props: { request: REQ, onDecide: vi.fn() } });
    expect(screen.getByText("25s")).toBeDefined();
  });

  test("countdown auto-block after 25s via fake timers", async () => {
    vi.useFakeTimers();
    const onDecide = vi.fn();
    render(ApprovalsModal, { props: { request: REQ, onDecide } });
    vi.advanceTimersByTime(25000);
    expect(onDecide).toHaveBeenCalledWith("block" satisfies ApprovalDecision);
    vi.useRealTimers();
  });
});
