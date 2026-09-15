import { describe, test, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/svelte";
import BoardActionResult from "../BoardActionResult.svelte";
import type { ActionResult } from "../../api/tickets.js";

/* The panel drives the poll through the shared Effect client, so stubbing
   fetch is enough to drive the whole follow. */
const replies: unknown[] = [];
let calls: string[] = [];

beforeEach(() => {
  calls = [];
  replies.length = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      calls.push(String(typeof input === "string" ? input : ((input as Request).url ?? input)));
      const body = replies.shift() ?? { ok: true, result: { status: "done", message: "finished" } };
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });
    }),
  );
});

afterEach(() => vi.unstubAllGlobals());

const base = (result: ActionResult, onFinished = vi.fn()) => {
  const utils = render(BoardActionResult, {
    props: {
      base: "/tools/agents",
      projectId: "p1",
      buttonId: "btn_list",
      label: "Sync from Notion",
      result,
      onFinished,
    },
  });
  return { ...utils, onFinished };
};

describe("BoardActionResult", () => {
  test("renders the receiver's message, progress and counters", () => {
    base({
      ok: true,
      result: {
        status: "running",
        message: "syncing Dana from Notion",
        progress: { done: 3, total: 12 },
        counts: { created: 1, updated: 2 },
      },
    });
    expect(screen.getByText("Running")).toBeTruthy();
    expect(screen.getByText("syncing Dana from Notion")).toBeTruthy();
    expect(screen.getByText("3/12")).toBeTruthy();
    expect(screen.getByText(/created/)).toBeTruthy();
  });

  /* A receiver that only answered "started" tells the clicker nothing about
     whether the work landed. The panel follows the run instead. */
  test("follows a running job and refreshes the board when it ends", async () => {
    replies.push({ ok: true, result: { status: "running", progress: { done: 6, total: 12 } } });
    replies.push({ ok: true, result: { status: "done", message: "12 synced", progress: { done: 12, total: 12 } } });

    const onFinished = vi.fn();
    base(
      {
        ok: true,
        result: {
          status: "running",
          message: "started",
          progress: { done: 0, total: 12 },
          poll_url: "https://abc.com/hooks/sync/status",
        },
      },
      onFinished,
    );

    await waitFor(() => expect(screen.getByText("Done")).toBeTruthy(), { timeout: 10000 });
    expect(screen.getByText("12 synced")).toBeTruthy();
    // The poll goes through wick, never straight at the receiver.
    expect(calls.every((u) => u.includes("/board-actions/btn_list/poll"))).toBe(true);
    // Refreshed as it moved AND at the end — a card must not sit in the
    // wrong column until somebody reloads.
    expect(onFinished.mock.calls.length).toBeGreaterThanOrEqual(2);
  }, 15000);

  /* Nothing to poll: the work was done inside the request, so the board is
     already stale by the time the panel mounts. */
  test("a finished click refreshes once and polls nothing", async () => {
    const onFinished = vi.fn();
    base({ ok: true, result: { status: "done", message: "nothing to do" } }, onFinished);
    await waitFor(() => expect(onFinished).toHaveBeenCalled());
    expect(calls.length).toBe(0);
  });

  test("a transport failure renders as failed, not as success", () => {
    base({ ok: false, status: 502, error: "connection refused" });
    expect(screen.getByText("Failed")).toBeTruthy();
    expect(screen.getByText("connection refused")).toBeTruthy();
  });
});
