import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import ThreadMessage from "../ThreadMessage.svelte";
import ConversationThread from "../ConversationThread.svelte";
import type { ConversationTurn } from "../../types/agents.js";
import type { RemoteRecheck } from "../../api/team.js";
import { canRecheck, recheckNote, recheckToast, foldReplaced, lateLabel } from "../../remoteRecheck.js";

function turn(over: Partial<ConversationTurn>): ConversationTurn {
  return {
    turn_id: "t-1", role: "assistant", agent: "main", provider: "slack-remote/slack-remote", text: "",
    timestamp: 0, truncated: false, interrupted: false, has_trace: false, events: [], attachments: [], ...over,
  };
}

const TIMEOUT = turn({ role: "system", is_error: true, text: "No reply from the remote agent after 3m0s." });

describe("Check again (remote recheck)", () => {
  test("offered on a remote timeout and on a turn closed without marker only", () => {
    expect(canRecheck(TIMEOUT)).toBe(true);
    expect(canRecheck(turn({ text: "partial", remote_note: "ended without marker" }))).toBe(true);
    expect(canRecheck(turn({ text: "done" }))).toBe(false);
    expect(canRecheck(turn({ role: "system", is_error: true, text: "Some other error" }))).toBe(false);
  });

  test("no button without a recheck handler (a remote that cannot check again)", () => {
    render(ThreadMessage, { props: { turn: TIMEOUT } });
    expect(screen.queryByText("Check again")).toBeNull();
  });

  test("timeout: click reads the reply again, shows loading, then the reply", async () => {
    let resolve!: (r: RemoteRecheck) => void;
    const onRemoteRecheck = vi.fn(() => new Promise<RemoteRecheck>((r) => (resolve = r)));
    render(ThreadMessage, { props: { turn: TIMEOUT, onRemoteRecheck } });
    await fireEvent.click(screen.getByText("Check again"));
    expect(onRemoteRecheck).toHaveBeenCalledTimes(1);
    expect(screen.getByText("Checking…")).toBeTruthy();
    resolve({ text: "Finding A.\n\nFinal answer.", busy: false, done: true });
    await waitFor(() => expect(screen.getByTestId("remote-recheck-reply").textContent).toContain("Final answer."));
    expect(screen.getByTestId("remote-recheck-note").textContent).toBe("Latest reply from the remote.");
    expect(screen.getByText("Check again")).toBeTruthy();
  });

  test("a kept reply says where it went instead of showing a second bubble", async () => {
    const onRemoteRecheck = vi.fn().mockResolvedValue({ text: "Final answer.", busy: false, done: true, replaced: true, forwarded_to: "captain" });
    render(ThreadMessage, { props: { turn: TIMEOUT, onRemoteRecheck } });
    await fireEvent.click(screen.getByText("Check again"));
    await waitFor(() => expect(screen.getByTestId("remote-recheck-note").textContent).toBe("Reply saved · forwarded to @captain"));
    expect(screen.queryByTestId("remote-recheck-reply")).toBeNull();
  });

  test("closed without marker: same text again = nothing new; still working shows the label", async () => {
    const t = turn({ text: "Finding A.", remote_note: "ended without marker" });
    const onRemoteRecheck = vi.fn().mockResolvedValueOnce({ text: "Finding A.", busy: false, done: false })
      .mockResolvedValueOnce({ text: "Finding A.", busy: true, done: false, label: "reading code…" });
    render(ThreadMessage, { props: { turn: t, onRemoteRecheck } });
    await fireEvent.click(screen.getByText("Check again"));
    await waitFor(() => expect(screen.getByTestId("remote-recheck-note").textContent).toBe("No new reply yet."));
    expect(screen.queryByTestId("remote-recheck-reply")).toBeNull();
    await fireEvent.click(screen.getByText("Check again"));
    await waitFor(() => expect(screen.getByTestId("remote-recheck-note").textContent).toBe("The remote is still working — reading code…"));
  });

  test("an error is shown, not thrown", async () => {
    const onRemoteRecheck = vi.fn().mockRejectedValue(new Error("Slack: invalid_auth"));
    render(ThreadMessage, { props: { turn: TIMEOUT, onRemoteRecheck } });
    await fireEvent.click(screen.getByText("Check again"));
    await waitFor(() => expect(screen.getByTestId("remote-recheck-error").textContent).toContain("invalid_auth"));
  });

  test("a late reply carries the 'Late reply' label", () => {
    render(ThreadMessage, { props: { turn: turn({ text: "Final answer.", remote_note: "late reply" }) } });
    expect(screen.getByTestId("late-reply-label").textContent).toBe("Late reply");
    expect(recheckNote({ text: "", busy: false, done: false }, "x")).toBe("No new reply yet.");
  });

  test("toast wording", () => {
    expect(recheckToast({ replaced: true, forwarded_to: "captain" })).toBe("Reply saved · forwarded to @captain");
    expect(recheckToast({ replaced: true })).toBe("Reply saved");
    expect(recheckToast({ replaced: false })).toBe("");
  });
});

describe("a reply that replaced the timeout, after reload", () => {
  const history = [
    turn({ turn_id: "u1", role: "user", text: "Please check", ts: "2026-01-02T03:00:00Z" }),
    turn({ ...TIMEOUT, turn_id: "e1", ts: "2026-01-02T03:03:00Z" }),
    turn({ turn_id: "h1", role: "system", kind: "note", text: "something after", ts: "2026-01-02T03:03:01Z" }),
    turn({ turn_id: "a1", text: "Final answer.", remote_note: "rechecked", replaces: "e1", ts: "2026-01-02T03:07:00Z" }),
  ];

  test("foldReplaced puts the reply where the timeout was and hides the timeout", () => {
    const out = foldReplaced(history);
    expect(out.map((t) => t.turn_id)).toEqual(["u1", "a1", "h1"]);
    expect(out[1].late_ms).toBe(4 * 60000);
    expect(lateLabel(out[1])).toBe("Rechecked · arrived 4 min late");
  });

  test("the thread renders the reply and its label, not the timeout", () => {
    render(ConversationThread, { props: { turns: history, live: null, typing: { active: false }, onRemoteRecheck: vi.fn() } });
    expect(screen.queryByText(/No reply from the remote agent/)).toBeNull();
    expect(screen.queryByText("Check again")).toBeNull();
    expect(screen.getByText("Final answer.")).toBeTruthy();
    expect(screen.getByTestId("late-reply-label").textContent).toBe("Rechecked · arrived 4 min late");
  });
});
