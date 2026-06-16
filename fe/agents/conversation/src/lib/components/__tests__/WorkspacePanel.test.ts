import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import WorkspacePanel from "../WorkspacePanel.svelte";
import type { WsInstance, WsBase } from "../../types/agents.js";

const INST_A: WsInstance = {
  id: "cid-a",
  label: "Alpha Connector",
  status: "ready",
  fields: [],
};

const INST_B: WsInstance = {
  id: "cid-b",
  label: "Beta Connector",
  status: "needs_setup",
  fields: [],
};

const BASE_X: WsBase = { base_key: "slack", label: "Slack" };
const BASE_Y: WsBase = { base_key: "github", label: "GitHub" };

function defaultCallbacks() {
  return {
    onAdd: vi.fn(),
    onSave: vi.fn(),
    onTest: vi.fn().mockResolvedValue({ ok: true }),
    onRename: vi.fn(),
    onDuplicate: vi.fn(),
    onDelete: vi.fn(),
  };
}

describe("WorkspacePanel", () => {
  test("shows empty-state message when instances is empty", () => {
    render(WorkspacePanel, {
      props: {
        instances: [],
        bases: [],
        ...defaultCallbacks(),
      },
    });
    expect(screen.getByText(/no session connectors yet/i)).toBeDefined();
  });

  test("shows no-bases-enabled message when both instances and bases are empty", () => {
    render(WorkspacePanel, {
      props: {
        instances: [],
        bases: [],
        ...defaultCallbacks(),
      },
    });
    expect(screen.getByText(/no connector here is enabled/i)).toBeDefined();
  });

  test("no-bases message is hidden when bases are present", () => {
    render(WorkspacePanel, {
      props: {
        instances: [],
        bases: [BASE_X],
        ...defaultCallbacks(),
      },
    });
    expect(screen.queryByText(/no connector here is enabled/i)).toBeNull();
  });

  test("renders N instance cards", () => {
    render(WorkspacePanel, {
      props: {
        instances: [INST_A, INST_B],
        bases: [],
        ...defaultCallbacks(),
      },
    });
    expect(screen.getByText("Alpha Connector")).toBeDefined();
    expect(screen.getByText("Beta Connector")).toBeDefined();
  });

  test("add-from-base picker is absent when bases is empty", () => {
    render(WorkspacePanel, {
      props: {
        instances: [],
        bases: [],
        ...defaultCallbacks(),
      },
    });
    expect(screen.queryByTestId("base-picker")).toBeNull();
  });

  test("add-from-base picker is present when bases are provided", () => {
    render(WorkspacePanel, {
      props: {
        instances: [],
        bases: [BASE_X, BASE_Y],
        ...defaultCallbacks(),
      },
    });
    expect(screen.getByTestId("base-picker")).toBeDefined();
  });

  test("picker renders base labels as options", () => {
    render(WorkspacePanel, {
      props: {
        instances: [],
        bases: [BASE_X, BASE_Y],
        ...defaultCallbacks(),
      },
    });
    expect(screen.getByText("Slack")).toBeDefined();
    expect(screen.getByText("GitHub")).toBeDefined();
  });

  test("selecting a base from picker calls onAdd with base_key and resets picker", async () => {
    const onAdd = vi.fn();
    render(WorkspacePanel, {
      props: {
        instances: [],
        bases: [BASE_X, BASE_Y],
        ...defaultCallbacks(),
        onAdd,
      },
    });
    const sel = screen.getByTestId("base-picker") as HTMLSelectElement;
    await fireEvent.change(sel, { target: { value: "slack" } });
    expect(onAdd).toHaveBeenCalledOnce();
    expect(onAdd).toHaveBeenCalledWith("slack");
  });

  test("picker still shows when instances list is populated", () => {
    render(WorkspacePanel, {
      props: {
        instances: [INST_A],
        bases: [BASE_X],
        ...defaultCallbacks(),
      },
    });
    expect(screen.getByTestId("base-picker")).toBeDefined();
    expect(screen.getByText("Alpha Connector")).toBeDefined();
  });
});
