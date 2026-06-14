import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import SessionList from "../SessionList.svelte";
import type { SessionListItem } from "../../types/agents.js";

function makeSession(overrides: Partial<SessionListItem> = {}): SessionListItem {
  return {
    id: "sess-001",
    label: "My chat",
    status: "idle",
    project_id: "",
    active_agent: "claude-sonnet",
    created_at: "2024-01-01T00:00:00Z",
    last_active: "2024-01-01T12:00:00Z",
    lifecycle: "idle",
    ...overrides,
  };
}

const SESSION_A = makeSession({ id: "sess-a", label: "Alpha chat" });
const SESSION_B = makeSession({ id: "sess-b", label: "Beta chat" });
const SESSION_C = makeSession({ id: "sess-c", label: "Gamma chat" });

describe("SessionList", () => {
  test("shows empty state when sessions is empty", () => {
    render(SessionList, {
      props: {
        sessions: [],
        search: "",
        onSearch: vi.fn(),
        onSelect: vi.fn(),
      },
    });
    expect(screen.getByText("No sessions yet.")).toBeDefined();
  });

  test("renders rows with label and a lifecycle badge", () => {
    render(SessionList, {
      props: {
        sessions: [SESSION_A],
        search: "",
        onSearch: vi.fn(),
        onSelect: vi.fn(),
      },
    });
    expect(screen.getByText("Alpha chat")).toBeDefined();
    expect(screen.getByText("idle")).toBeDefined();
  });

  test("typing in search input calls onSearch", async () => {
    const onSearch = vi.fn();
    render(SessionList, {
      props: {
        sessions: [SESSION_A],
        search: "",
        onSearch,
        onSelect: vi.fn(),
      },
    });
    const input = screen.getByPlaceholderText(/search/i);
    await fireEvent.input(input, { target: { value: "alpha" } });
    expect(onSearch).toHaveBeenCalledWith("alpha");
  });

  test("shows no-match state when search matches nothing", () => {
    render(SessionList, {
      props: {
        sessions: [SESSION_A, SESSION_B],
        search: "zzznomatch",
        onSearch: vi.fn(),
        onSelect: vi.fn(),
      },
    });
    expect(screen.getByText("No chats match your search.")).toBeDefined();
  });

  test("shows only matching rows when search matches some", () => {
    render(SessionList, {
      props: {
        sessions: [SESSION_A, SESSION_B],
        search: "alpha",
        onSearch: vi.fn(),
        onSelect: vi.fn(),
      },
    });
    expect(screen.getByText("Alpha chat")).toBeDefined();
    expect(screen.queryByText("Beta chat")).toBeNull();
  });

  test("clicking a row calls onSelect with the session id", async () => {
    const onSelect = vi.fn();
    render(SessionList, {
      props: {
        sessions: [SESSION_A],
        search: "",
        onSearch: vi.fn(),
        onSelect,
      },
    });
    const row = screen.getByText("Alpha chat").closest("[data-testid]") as HTMLElement;
    await fireEvent.click(row!);
    expect(onSelect).toHaveBeenCalledWith("sess-a");
  });

  test("selected row has aria-current=true and selected class", () => {
    render(SessionList, {
      props: {
        sessions: [SESSION_A, SESSION_B],
        search: "",
        selectedId: "sess-a",
        onSearch: vi.fn(),
        onSelect: vi.fn(),
      },
    });
    const selected = screen.getByTestId("session-row-sess-a");
    expect(selected.getAttribute("aria-current")).toBe("true");
  });

  test("delete button calls onDelete with the session id", async () => {
    const onDelete = vi.fn();
    render(SessionList, {
      props: {
        sessions: [SESSION_A],
        search: "",
        onSearch: vi.fn(),
        onSelect: vi.fn(),
        onDelete,
      },
    });
    const btn = screen.getByRole("button", { name: "Delete" });
    await fireEvent.click(btn);
    expect(onDelete).toHaveBeenCalledWith("sess-a");
  });

  test("pager is hidden when sessions <= pageSize", () => {
    render(SessionList, {
      props: {
        sessions: [SESSION_A, SESSION_B],
        search: "",
        pageSize: 10,
        onSearch: vi.fn(),
        onSelect: vi.fn(),
      },
    });
    expect(screen.queryByText(/page 1/i)).toBeNull();
  });

  test("shows only pageSize rows when sessions exceed it", () => {
    const sessions = [SESSION_A, SESSION_B, SESSION_C];
    render(SessionList, {
      props: {
        sessions,
        search: "",
        pageSize: 2,
        onSearch: vi.fn(),
        onSelect: vi.fn(),
      },
    });
    expect(screen.getByText("Alpha chat")).toBeDefined();
    expect(screen.getByText("Beta chat")).toBeDefined();
    expect(screen.queryByText("Gamma chat")).toBeNull();
  });

  test("pager appears when sessions exceed pageSize", () => {
    const sessions = [SESSION_A, SESSION_B, SESSION_C];
    render(SessionList, {
      props: {
        sessions,
        search: "",
        pageSize: 2,
        onSearch: vi.fn(),
        onSelect: vi.fn(),
      },
    });
    expect(screen.getByText(/page 1 \/ 2/i)).toBeDefined();
  });

  test("clicking next pager shows next page", async () => {
    const sessions = [SESSION_A, SESSION_B, SESSION_C];
    render(SessionList, {
      props: {
        sessions,
        search: "",
        pageSize: 2,
        onSearch: vi.fn(),
        onSelect: vi.fn(),
      },
    });
    const nextBtn = screen.getByRole("button", { name: /next/i });
    await fireEvent.click(nextBtn);
    expect(screen.getByText("Gamma chat")).toBeDefined();
    expect(screen.queryByText("Alpha chat")).toBeNull();
  });
});
