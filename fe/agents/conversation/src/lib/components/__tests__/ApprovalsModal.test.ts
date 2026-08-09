import { describe, test, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import { tick } from "svelte";
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

  test("renders the countdown from expires_in_sec", () => {
    render(ApprovalsModal, { props: { request: { ...REQ, expires_in_sec: 25 }, onDecide: vi.fn() } });
    expect(screen.getByText("25s")).toBeDefined();
  });

  test("shows Waiting instead of a countdown when no deadline is set", () => {
    render(ApprovalsModal, { props: { request: REQ, onDecide: vi.fn() } });
    expect(screen.getByText(/waiting/i)).toBeDefined();
    expect(screen.queryByText(/^\d+s$/)).toBeNull();
  });

  // The daemon decides expiry, not the browser. A modal that blocks on
  // its own races the server: it POSTs a decision for a request the
  // daemon has already resolved, gets 410, and used to reopen itself
  // with an error no button could clear.
  test("never decides on its own when the countdown reaches zero", () => {
    vi.useFakeTimers();
    const onDecide = vi.fn();
    render(ApprovalsModal, { props: { request: { ...REQ, expires_in_sec: 25 }, onDecide } });
    vi.advanceTimersByTime(30000);
    expect(onDecide).not.toHaveBeenCalled();
    vi.useRealTimers();
  });

  test("countdown does not run without a deadline", () => {
    vi.useFakeTimers();
    const onDecide = vi.fn();
    render(ApprovalsModal, { props: { request: REQ, onDecide } });
    vi.advanceTimersByTime(120000);
    expect(onDecide).not.toHaveBeenCalled();
    vi.useRealTimers();
  });

  test("renders inline error region when error prop is set", () => {
    const request = { id: "a1", agent_name: "main", tool: "bash", work_dir: "/w", cmd: "rm -rf /", match_key: "k" };
    render(ApprovalsModal, { props: { request, onDecide: vi.fn(), error: "Decision expired (410) — request a new approval." } });
    expect(screen.getByText(/decision expired/i)).toBeDefined();
  });

  test("no error region when error prop is empty", () => {
    const request = { id: "a1", agent_name: "main", tool: "bash", work_dir: "/w", cmd: "x", match_key: "k" };
    const { container } = render(ApprovalsModal, { props: { request, onDecide: vi.fn() } });
    expect(container.querySelector("[data-approval-error]")).toBeNull();
  });

  test("Escape key dismisses (block + close)", async () => {
    const request = { id: "a1", agent_name: "m", tool: "bash", work_dir: "/", cmd: "x", match_key: "k" };
    const onDecide = vi.fn();
    const onClose = vi.fn();
    render(ApprovalsModal, { props: { request, onDecide, onClose } });
    await fireEvent.keyDown(window, { key: "Escape" });
    expect(onDecide).toHaveBeenCalledWith("block");
    expect(onClose).toHaveBeenCalled();
  });

  test("backdrop click dismisses", async () => {
    const request = { id: "a1", agent_name: "m", tool: "bash", work_dir: "/", cmd: "x", match_key: "k" };
    const onClose = vi.fn();
    const { container } = render(ApprovalsModal, { props: { request, onDecide: vi.fn(), onClose } });
    await fireEvent.click(container.querySelector("[data-approval-backdrop]")!);
    expect(onClose).toHaveBeenCalled();
  });
  // "Block" ends the agent's turn. A guided refusal also refuses the
  // command, but hands back a correction so the agent can try another
  // way — useless without a reason, hence the note field.
  describe("guided refusal", () => {
    test("Block with note reveals a note field instead of deciding immediately", async () => {
      const onDecide = vi.fn();
      render(ApprovalsModal, { props: { request: REQ, onDecide } });
      await fireEvent.click(screen.getByText("Block with note"));
      expect(onDecide).not.toHaveBeenCalled();
      expect(screen.getByPlaceholderText(/what should it do instead/i)).toBeDefined();
    });

    test("sends guide with the typed note", async () => {
      const onDecide = vi.fn();
      render(ApprovalsModal, { props: { request: REQ, onDecide } });
      await fireEvent.click(screen.getByText("Block with note"));
      const note = screen.getByPlaceholderText(/what should it do instead/i);
      await fireEvent.input(note, { target: { value: "use git clean -n first" } });
      await fireEvent.click(screen.getByText("Send"));
      expect(onDecide).toHaveBeenCalledWith("guide", "use git clean -n first");
    });

    test("will not send an empty note", async () => {
      const onDecide = vi.fn();
      render(ApprovalsModal, { props: { request: REQ, onDecide } });
      await fireEvent.click(screen.getByText("Block with note"));
      await fireEvent.click(screen.getByText("Send"));
      expect(onDecide).not.toHaveBeenCalled();
    });

    test("plain Block still decides immediately with no reason", async () => {
      const onDecide = vi.fn();
      render(ApprovalsModal, { props: { request: REQ, onDecide } });
      await fireEvent.click(screen.getByText("Block"));
      expect(onDecide).toHaveBeenCalledWith("block");
    });
  });
  // The modal interrupts whatever the user was doing, so it has to be
  // answerable from the keyboard alone — reaching for the mouse is the
  // slow path this exists to avoid.
  describe("keyboard shortcuts", () => {
    test("A approves once", async () => {
      const onDecide = vi.fn();
      render(ApprovalsModal, { props: { request: REQ, onDecide } });
      await fireEvent.keyDown(window, { key: "a" });
      expect(onDecide).toHaveBeenCalledWith("approve_once");
    });

    test("S allows the session", async () => {
      const onDecide = vi.fn();
      render(ApprovalsModal, { props: { request: REQ, onDecide } });
      await fireEvent.keyDown(window, { key: "s" });
      expect(onDecide).toHaveBeenCalledWith("approve_session");
    });

    test("W allows always", async () => {
      const onDecide = vi.fn();
      render(ApprovalsModal, { props: { request: REQ, onDecide } });
      await fireEvent.keyDown(window, { key: "w" });
      expect(onDecide).toHaveBeenCalledWith("approve_always");
    });

    test("B blocks", async () => {
      const onDecide = vi.fn();
      render(ApprovalsModal, { props: { request: REQ, onDecide } });
      await fireEvent.keyDown(window, { key: "b" });
      expect(onDecide).toHaveBeenCalledWith("block");
    });

    test("N opens the note field without deciding", async () => {
      const onDecide = vi.fn();
      render(ApprovalsModal, { props: { request: REQ, onDecide } });
      await fireEvent.keyDown(window, { key: "n" });
      expect(onDecide).not.toHaveBeenCalled();
      expect(screen.getByPlaceholderText(/what should it do instead/i)).toBeDefined();
    });

    test("Enter approves once as the default action", async () => {
      const onDecide = vi.fn();
      render(ApprovalsModal, { props: { request: REQ, onDecide } });
      await fireEvent.keyDown(window, { key: "Enter" });
      expect(onDecide).toHaveBeenCalledWith("approve_once");
    });

    // Once the note is open the user is typing prose — a bare letter must
    // reach the textarea, not fire a decision behind their back.
    test("letters type into the note instead of deciding", async () => {
      const onDecide = vi.fn();
      render(ApprovalsModal, { props: { request: REQ, onDecide } });
      await fireEvent.keyDown(window, { key: "n" });
      await fireEvent.keyDown(window, { key: "b" });
      await fireEvent.keyDown(window, { key: "a" });
      expect(onDecide).not.toHaveBeenCalled();
    });

    test("Enter sends the note, Shift+Enter does not", async () => {
      const onDecide = vi.fn();
      render(ApprovalsModal, { props: { request: REQ, onDecide } });
      await fireEvent.click(screen.getByText("Block with note"));
      const note = screen.getByPlaceholderText(/what should it do instead/i);
      await fireEvent.input(note, { target: { value: "use git clean -n" } });

      await fireEvent.keyDown(note, { key: "Enter", shiftKey: true });
      expect(onDecide).not.toHaveBeenCalled();

      await fireEvent.keyDown(note, { key: "Enter" });
      expect(onDecide).toHaveBeenCalledWith("guide", "use git clean -n");
    });

    test("Escape closes the note field before closing the modal", async () => {
      const onDecide = vi.fn();
      const onClose = vi.fn();
      render(ApprovalsModal, { props: { request: REQ, onDecide, onClose } });
      await fireEvent.keyDown(window, { key: "n" });
      await fireEvent.keyDown(window, { key: "Escape" });
      // First Escape backs out of the note, leaving the modal open.
      expect(onDecide).not.toHaveBeenCalled();
      expect(onClose).not.toHaveBeenCalled();
      // Second Escape dismisses as before.
      await fireEvent.keyDown(window, { key: "Escape" });
      expect(onDecide).toHaveBeenCalledWith("block");
    });
  });
  // The prompt arrives while the user is mid-sentence in the composer.
  // If focus stays there, the shortcuts type into the message box instead
  // of answering — so the modal has to take focus itself.
  describe("focus handling", () => {
    test("takes focus away from whatever had it when the request arrives", async () => {
      const composer = document.createElement("textarea");
      document.body.appendChild(composer);
      composer.focus();
      expect(document.activeElement).toBe(composer);

      render(ApprovalsModal, { props: { request: REQ, onDecide: vi.fn() } });
      await tick();

      expect(document.activeElement).not.toBe(composer);
      expect(document.querySelector("[data-approval-dialog]")).toBe(document.activeElement);
      composer.remove();
    });

    test("moves focus into the note field when it opens", async () => {
      render(ApprovalsModal, { props: { request: REQ, onDecide: vi.fn() } });
      await fireEvent.keyDown(window, { key: "n" });
      await tick();
      expect(document.activeElement).toBe(screen.getByPlaceholderText(/what should it do instead/i));
    });

    test("Escape from inside the note cancels it and returns focus to the dialog", async () => {
      const onDecide = vi.fn();
      render(ApprovalsModal, { props: { request: REQ, onDecide } });
      await fireEvent.keyDown(window, { key: "n" });
      await tick();
      const note = screen.getByPlaceholderText(/what should it do instead/i);
      await fireEvent.keyDown(note, { key: "Escape" });
      await tick();
      expect(onDecide).not.toHaveBeenCalled();
      expect(document.querySelector("[data-approval-dialog]")).toBe(document.activeElement);
    });
  });
  // A phone has no room for hint chips next to already-cramped buttons,
  // and no soft keyboard shows them anyway. The shortcuts themselves stay
  // wired — an Android tablet with a bluetooth keyboard still gets them;
  // only the visual affordance is desktop-only.
  describe("keyboard hints are desktop-only", () => {
    test("every hint chip is hidden below the sm breakpoint", () => {
      const { container } = render(ApprovalsModal, {
        props: { request: REQ, onDecide: vi.fn() },
      });
      const chips = container.querySelectorAll("kbd");
      expect(chips.length).toBeGreaterThan(0);
      for (const chip of chips) {
        expect(chip.className).toContain("hidden");
        expect(chip.className).toContain("sm:inline");
      }
    });

    test("the note's shortcut line is hidden below the sm breakpoint", async () => {
      render(ApprovalsModal, { props: { request: REQ, onDecide: vi.fn() } });
      await fireEvent.click(screen.getByText("Block with note"));
      const hint = screen.getByText(/Enter to send/);
      expect(hint.className).toContain("hidden");
      expect(hint.className).toContain("sm:block");
    });

    test("shortcuts still work regardless — a hardware keyboard on mobile is real", async () => {
      const onDecide = vi.fn();
      render(ApprovalsModal, { props: { request: REQ, onDecide } });
      await fireEvent.keyDown(window, { key: "a" });
      expect(onDecide).toHaveBeenCalledWith("approve_once");
    });
  });
});
