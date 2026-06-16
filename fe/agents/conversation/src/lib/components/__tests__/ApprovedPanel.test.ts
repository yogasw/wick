import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import ApprovedPanel from "../ApprovedPanel.svelte";
import type { ApprovedItem } from "../../types/agents.js";

const SESSION_ITEM: ApprovedItem = { match_key: "sha256:abc123def456", scope: "session" };
const ALWAYS_ITEM: ApprovedItem = { match_key: "sha256:xyz789uvw012", scope: "always" };

describe("ApprovedPanel", () => {
  test("shows empty state when both lists are empty", () => {
    render(ApprovedPanel, {
      props: { sessionApproved: [], alwaysApproved: [], onRevoke: vi.fn() },
    });
    expect(screen.getByText(/No commands have been approved/)).toBeDefined();
  });

  test("renders a row for each session-approved item", () => {
    render(ApprovedPanel, {
      props: {
        sessionApproved: [SESSION_ITEM],
        alwaysApproved: [],
        onRevoke: vi.fn(),
      },
    });
    expect(screen.getByText("session")).toBeDefined();
    expect(screen.getByText("sha256:abc12…")).toBeDefined();
  });

  test("renders a row for each always-approved item", () => {
    render(ApprovedPanel, {
      props: {
        sessionApproved: [],
        alwaysApproved: [ALWAYS_ITEM],
        onRevoke: vi.fn(),
      },
    });
    expect(screen.getByText("always")).toBeDefined();
    expect(screen.getByText("sha256:xyz78…")).toBeDefined();
  });

  test("shows total count in summary", () => {
    render(ApprovedPanel, {
      props: {
        sessionApproved: [SESSION_ITEM],
        alwaysApproved: [ALWAYS_ITEM],
        onRevoke: vi.fn(),
      },
    });
    expect(screen.getByText("2")).toBeDefined();
  });

  test("revoke button calls onRevoke with match_key and scope for session item", async () => {
    const onRevoke = vi.fn();
    render(ApprovedPanel, {
      props: {
        sessionApproved: [SESSION_ITEM],
        alwaysApproved: [],
        onRevoke,
      },
    });
    await fireEvent.click(screen.getByText("Revoke"));
    expect(onRevoke).toHaveBeenCalledOnce();
    expect(onRevoke).toHaveBeenCalledWith("sha256:abc123def456", "session");
  });

  test("revoke button calls onRevoke with match_key and scope for always item", async () => {
    const onRevoke = vi.fn();
    render(ApprovedPanel, {
      props: {
        sessionApproved: [],
        alwaysApproved: [ALWAYS_ITEM],
        onRevoke,
      },
    });
    await fireEvent.click(screen.getByText("Revoke"));
    expect(onRevoke).toHaveBeenCalledOnce();
    expect(onRevoke).toHaveBeenCalledWith("sha256:xyz789uvw012", "always");
  });

  test("does not show empty state when there are items", () => {
    render(ApprovedPanel, {
      props: {
        sessionApproved: [SESSION_ITEM],
        alwaysApproved: [],
        onRevoke: vi.fn(),
      },
    });
    expect(screen.queryByText(/No commands have been approved/)).toBeNull();
  });
});
