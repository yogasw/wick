import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/svelte";
import ThreadMessage from "../ThreadMessage.svelte";
import type { ConversationTurn } from "../../types/agents.js";
import type { SlackRecheck } from "../../api/team.js";
import { canRecheck, recheckNote } from "../../remoteRecheck.js";

function turn(over: Partial<ConversationTurn>): ConversationTurn {
  return {
    turn_id: "t-1", role: "assistant", agent: "main", provider: "slack-remote/slack-remote", text: "",
    timestamp: 0, truncated: false, interrupted: false, has_trace: false, events: [], attachments: [], ...over,
  };
}

const TIMEOUT = turn({ role: "system", is_error: true, text: "No reply from the remote agent after 3m0s." });

describe("Cek ulang (Slack remote recheck)", () => {
  test("offered on a remote timeout and on a turn closed without marker only", () => {
    expect(canRecheck(TIMEOUT)).toBe(true);
    expect(canRecheck(turn({ text: "partial", remote_note: "ended without marker" }))).toBe(true);
    expect(canRecheck(turn({ text: "done" }))).toBe(false);
    expect(canRecheck(turn({ role: "system", is_error: true, text: "Some other error" }))).toBe(false);
  });

  test("no button without a recheck handler (not a Slack remote agent)", () => {
    render(ThreadMessage, { props: { turn: TIMEOUT } });
    expect(screen.queryByText("Cek ulang")).toBeNull();
  });

  test("timeout: click reads the thread again, shows loading, then the reply", async () => {
    let resolve!: (r: SlackRecheck) => void;
    const onRemoteRecheck = vi.fn(() => new Promise<SlackRecheck>((r) => (resolve = r)));
    render(ThreadMessage, { props: { turn: TIMEOUT, onRemoteRecheck } });
    await fireEvent.click(screen.getByText("Cek ulang"));
    expect(onRemoteRecheck).toHaveBeenCalledTimes(1);
    expect(screen.getByText("Mengecek…")).toBeTruthy();
    resolve({ text: "Temuan A.\n\nJawaban akhir.", busy: false, done: true });
    await waitFor(() => expect(screen.getByTestId("remote-recheck-reply").textContent).toContain("Jawaban akhir."));
    expect(screen.getByTestId("remote-recheck-note").textContent).toBe("Balasan terbaru dari Slack.");
    expect(screen.getByText("Cek ulang")).toBeTruthy();
  });

  test("closed without marker: same text again = nothing new; still working shows the label", async () => {
    const t = turn({ text: "Temuan A.", remote_note: "ended without marker" });
    const onRemoteRecheck = vi.fn().mockResolvedValueOnce({ text: "Temuan A.", busy: false, done: false })
      .mockResolvedValueOnce({ text: "Temuan A.", busy: true, done: false, label: "lagi pakai code read…" });
    render(ThreadMessage, { props: { turn: t, onRemoteRecheck } });
    await fireEvent.click(screen.getByText("Cek ulang"));
    await waitFor(() => expect(screen.getByTestId("remote-recheck-note").textContent).toBe("Belum ada balasan baru."));
    expect(screen.queryByTestId("remote-recheck-reply")).toBeNull();
    await fireEvent.click(screen.getByText("Cek ulang"));
    await waitFor(() => expect(screen.getByTestId("remote-recheck-note").textContent).toBe("Remote masih mengerjakan — lagi pakai code read…"));
  });

  test("an error is shown, not thrown", async () => {
    const onRemoteRecheck = vi.fn().mockRejectedValue(new Error("Slack: invalid_auth"));
    render(ThreadMessage, { props: { turn: TIMEOUT, onRemoteRecheck } });
    await fireEvent.click(screen.getByText("Cek ulang"));
    await waitFor(() => expect(screen.getByTestId("remote-recheck-error").textContent).toContain("invalid_auth"));
  });

  test("a late reply carries the 'balasan telat' label", () => {
    render(ThreadMessage, { props: { turn: turn({ text: "Jawaban akhir.", remote_note: "late reply" }) } });
    expect(screen.getByTestId("late-reply-label").textContent).toBe("balasan telat");
    expect(recheckNote({ text: "", busy: false, done: false }, "x")).toBe("Belum ada balasan baru.");
  });
});
