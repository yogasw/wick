import { describe, test, expect, vi, beforeEach, afterEach } from "vitest";
import { render } from "@testing-library/svelte";

vi.mock("../../router.js", () => ({ push: vi.fn() }));

/* The board's API, reduced to what the refetch logic touches: the filter
   resolves at once, every board fetch is counted. */
vi.mock("../../api/tickets.js", async (orig) => {
  const { Effect } = await import("effect");
  const real = (await orig()) as Record<string, unknown>;
  return {
    ...real,
    getTicketFilter: vi.fn(() => Effect.succeed({})),
    getTicketPrefs: vi.fn(() => Effect.succeed({})),
    getProjectTickets: vi.fn(() => Effect.succeed({ config: { enabled: true }, columns: [], tickets: [], sessions: [] })),
  };
});

/* The shared /stream/sessions connection: the test drives its status and
   pushes ticket signals. */
const { stream } = vi.hoisted(() => ({
  stream: {
    h: null as null | { onTicket?: (t: { project_id: string; ticket_id: string }) => void; onStatus?: (s: string) => void },
    left: 0,
  },
}));
vi.mock("../../stores/sessionsStream.js", () => ({
  connectSessionsStream: vi.fn((_base: string, h: typeof stream.h) => {
    stream.h = h;
    return () => { stream.left += 1; };
  }),
}));

import ProjectLanding from "../ProjectLanding.svelte";
import { getProjectTickets } from "../../api/tickets.js";

const props = {
  base: "/tools/agents",
  project: { id: "proj-42", name: "Acme", path: "/p", managed: true, pinned: false },
  providers: [],
  sessions: [],
  ownerTab: "me" as const,
  onOwnerTab: vi.fn(),
  onPin: vi.fn(),
  onSelectSession: vi.fn(),
};
const fetches = () => vi.mocked(getProjectTickets).mock.calls.length;

describe("ProjectLanding — board follows ticket signals instead of polling", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    stream.h = null;
    stream.left = 0;
    vi.mocked(getProjectTickets).mockClear();
  });
  afterEach(() => vi.useRealTimers());

  test("no timer fetch while the stream is up; a signal for this project refetches once", async () => {
    render(ProjectLanding, { props: props as never });
    await vi.advanceTimersByTimeAsync(50);
    expect(stream.h).not.toBeNull();
    stream.h!.onStatus?.("connected");
    const base = fetches();
    expect(base).toBeGreaterThan(0);

    // The old board refetched every 30 s.
    await vi.advanceTimersByTimeAsync(5 * 60_000);
    expect(fetches()).toBe(base);

    // Another project's ticket is not this board's business.
    stream.h!.onTicket?.({ project_id: "other", ticket_id: "T-9" });
    await vi.advanceTimersByTimeAsync(1000);
    expect(fetches()).toBe(base);

    // A burst for this project costs one fetch.
    stream.h!.onTicket?.({ project_id: "proj-42", ticket_id: "T-1" });
    stream.h!.onTicket?.({ project_id: "proj-42", ticket_id: "T-2" });
    await vi.advanceTimersByTimeAsync(1000);
    expect(fetches()).toBe(base + 1);
  });

  test("stream down: slow fallback poll; reconnect refetches once", async () => {
    render(ProjectLanding, { props: props as never });
    await vi.advanceTimersByTimeAsync(50);
    stream.h!.onStatus?.("connected");
    const base = fetches();
    stream.h!.onStatus?.("error");
    await vi.advanceTimersByTimeAsync(59_000);
    expect(fetches()).toBe(base);
    await vi.advanceTimersByTimeAsync(2_000);
    expect(fetches()).toBe(base + 1);
    stream.h!.onStatus?.("connected");
    await vi.advanceTimersByTimeAsync(1000);
    expect(fetches()).toBe(base + 2);
  });
});
