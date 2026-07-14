import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import RecentSpawns from "../RecentSpawns.svelte";
import * as api from "$lib/api.js";
import type { SessionSummary, SessionsList } from "$lib/types.js";

vi.mock("$lib/api.js");

function session(over: Partial<SessionSummary> = {}): SessionSummary {
  return {
    SessionID: "sess-12345678",
    ProviderType: "claude",
    ProviderName: "claude",
    SpawnCount: 3,
    LastStatus: "",
    LastStarted: "2024-01-01T00:00:00Z",
    FirstMessage: "hello",
    Origin: "web",
    ...over,
  };
}

function list(over: Partial<SessionsList> = {}): SessionsList {
  return { Sessions: [session()], Page: 1, HasNext: false, Total: 1, ...over };
}

beforeEach(() => {
  vi.mocked(api.apiGetSessions).mockResolvedValue(list());
});

describe("RecentSpawns (per-session)", () => {
  it("loads sessions on mount (all providers when unscoped, Provider column shown)", async () => {
    render(RecentSpawns, { props: { base: "/wick", onOpenSession: vi.fn() } });
    await screen.findByText("Recent Sessions");
    expect(api.apiGetSessions).toHaveBeenCalledWith("/wick", { type: undefined, name: undefined, q: "", page: 1 });
    expect(screen.getByText("Provider")).toBeTruthy();
    expect(screen.getByText("sess-123")).toBeTruthy();
    expect(screen.getByText("3")).toBeTruthy(); // spawn count
  });

  it("scopes to a provider and hides the Provider column", async () => {
    render(RecentSpawns, { props: { base: "/wick", type: "codex", name: "gemini_flash", onOpenSession: vi.fn() } });
    await screen.findByText("Recent Sessions");
    expect(api.apiGetSessions).toHaveBeenCalledWith("/wick", { type: "codex", name: "gemini_flash", q: "", page: 1 });
    expect(screen.queryByText("Provider")).toBeNull();
  });

  it("clicking a session row calls onOpenSession with its id", async () => {
    const onOpenSession = vi.fn();
    render(RecentSpawns, { props: { base: "/wick", onOpenSession } });
    await screen.findByText("Recent Sessions");
    const row = screen.getByText("sess-123").closest("tr")!;
    await fireEvent.click(row);
    expect(onOpenSession).toHaveBeenCalledWith("sess-12345678");
  });

  it("paginates via Next, requesting page 2", async () => {
    vi.mocked(api.apiGetSessions).mockResolvedValueOnce(list({ HasNext: true, Total: 15 }));
    render(RecentSpawns, { props: { base: "/wick", onOpenSession: vi.fn() } });
    await screen.findByText("Recent Sessions");
    const next = await screen.findByText("Next →");
    await fireEvent.click(next);
    expect(vi.mocked(api.apiGetSessions).mock.calls.at(-1)![1]).toMatchObject({ page: 2 });
  });

  it("shows empty state when no sessions", async () => {
    vi.mocked(api.apiGetSessions).mockResolvedValue(list({ Sessions: [], Total: 0 }));
    render(RecentSpawns, { props: { base: "/wick", onOpenSession: vi.fn() } });
    expect(await screen.findByText("No sessions recorded yet.")).toBeTruthy();
  });
});
