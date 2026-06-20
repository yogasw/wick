import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, within } from "@testing-library/svelte";
import OperationsTable from "../OperationsTable.svelte";
import * as api from "$lib/api.js";
import * as router from "$lib/router.js";
import type { ConnectorOp } from "$lib/types.js";

vi.mock("$lib/api.js");
vi.mock("$lib/router.js", () => ({ push: vi.fn() }));

function op(over: Partial<ConnectorOp> & { key: string }): ConnectorOp {
  return {
    name: over.key,
    description: "",
    destructive: false,
    enabled: true,
    system_disabled: false,
    system_disabled_reason: "",
    admin_only: false,
    category: "",
    ...over,
  };
}

function manyOps(n: number): ConnectorOp[] {
  return Array.from({ length: n }, (_, i) =>
    op({ key: `op${i + 1}`, name: `Operation ${i + 1}`, description: `does thing ${i + 1}` }),
  );
}

function renderTable(operations: ConnectorOp[], canConfigure = true) {
  return render(OperationsTable, {
    operations,
    connectorKey: "slack",
    connectorId: "row-a",
    canConfigure,
  });
}

beforeEach(() => {
  vi.clearAllMocks();
});

describe("OperationsTable", () => {
  it("renders the count badge with the total op count", () => {
    renderTable(manyOps(20));
    expect(screen.getByText("20")).toBeTruthy();
  });

  it("paginates each card at 5 ops with a Prev/Next pager", async () => {
    renderTable(manyOps(12));
    expect(screen.getByText("Operation 1")).toBeTruthy();
    expect(screen.getByText("Operation 5")).toBeTruthy();
    expect(screen.queryByText("Operation 6")).toBeNull();
    expect(screen.getByText("Showing 1–5 of 12")).toBeTruthy();
    await fireEvent.click(screen.getByRole("button", { name: "Next →" }));
    expect(screen.getByText("Operation 6")).toBeTruthy();
    expect(screen.queryByText("Operation 5")).toBeNull();
    expect(screen.getByText("Showing 6–10 of 12")).toBeTruthy();
  });

  it("hides the pager when a card has 5 or fewer ops", () => {
    renderTable(manyOps(4));
    expect(screen.queryByRole("button", { name: "Next →" })).toBeNull();
    expect(screen.queryByText(/Showing/)).toBeNull();
  });

  it("filters within a card via the per-card search box", async () => {
    const ops = [
      op({ key: "alpha", name: "Alpha", category: "Drive" }),
      op({ key: "beta", name: "Beta", category: "Drive" }),
    ];
    render(OperationsTable, {
      operations: ops,
      categories: [{ key: "Drive", title: "Drive", description: "" }],
      connectorKey: "gws",
      connectorId: "row-a",
      canConfigure: true,
    });
    await fireEvent.input(screen.getByLabelText("Search Drive"), { target: { value: "alpha" } });
    expect(screen.getByText("Alpha")).toBeTruthy();
    expect(screen.queryByText("Beta")).toBeNull();
  });

  it("filters rows by name, key, and description across categories", async () => {
    const ops = [
      op({ key: "send_message", name: "Send Message", description: "post to a channel" }),
      op({ key: "delete_message", name: "Delete Message", description: "remove a post" }),
      op({ key: "list_users", name: "List Users", description: "enumerate members" }),
    ];
    renderTable(ops);
    const search = screen.getByLabelText("Search operations");
    await fireEvent.input(search, { target: { value: "channel" } });
    expect(screen.getByText("Send Message")).toBeTruthy();
    expect(screen.queryByText("Delete Message")).toBeNull();
    await fireEvent.input(search, { target: { value: "list_users" } });
    expect(screen.getByText("List Users")).toBeTruthy();
    expect(screen.queryByText("Send Message")).toBeNull();
    await fireEvent.input(search, { target: { value: "Delete" } });
    expect(screen.getByText("Delete Message")).toBeTruthy();
  });

  it("renders one card per category with its title + description, plus a sections sidebar", () => {
    const ops = [
      op({ key: "list_files", name: "List Files", category: "Drive" }),
      op({ key: "read_range", name: "Read Range", category: "Sheets" }),
    ];
    render(OperationsTable, {
      operations: ops,
      categories: [
        { key: "Drive", title: "Drive", description: "Drive files." },
        { key: "Sheets", title: "Sheets", description: "Spreadsheet ranges." },
      ],
      connectorKey: "gws",
      connectorId: "row-a",
      canConfigure: true,
    });
    expect(screen.getByRole("heading", { name: "Drive" })).toBeTruthy();
    expect(screen.getByText("Drive files.")).toBeTruthy();
    expect(screen.getByRole("heading", { name: "Sheets" })).toBeTruthy();
    /* Sidebar jump buttons (one per category). */
    expect(screen.getByRole("button", { name: /Drive\s*1/ })).toBeTruthy();
  });

  it("selects a row and bulk-enables only selected ops", async () => {
    vi.mocked(api.bulkToggleOperations).mockResolvedValue(undefined);
    const ops = [
      op({ key: "a", name: "Alpha", enabled: false }),
      op({ key: "b", name: "Bravo", enabled: false }),
    ];
    renderTable(ops);
    await fireEvent.click(screen.getByLabelText("Select Alpha"));
    expect(screen.getByText("1 selected")).toBeTruthy();
    await fireEvent.click(screen.getByRole("button", { name: "Enable selected" }));
    await Promise.resolve();
    expect(api.bulkToggleOperations).toHaveBeenCalledWith("slack", "row-a", true, ["a"]);
  });

  it("select-all in a card selects the visible page (max 5)", async () => {
    vi.mocked(api.bulkToggleOperations).mockResolvedValue(undefined);
    renderTable(manyOps(12));
    await fireEvent.click(screen.getByLabelText("Select all in operations"));
    expect(screen.getByText("5 selected")).toBeTruthy();
    await fireEvent.click(screen.getByRole("button", { name: "Disable selected" }));
    await Promise.resolve();
    const call = vi.mocked(api.bulkToggleOperations).mock.calls[0];
    expect(call[0]).toBe("slack");
    expect(call[1]).toBe("row-a");
    expect(call[2]).toBe(false);
    expect(call[3]).toHaveLength(5);
  });

  it("falls back to Enable/Disable all (empty ops array) when nothing is selected", async () => {
    vi.mocked(api.bulkToggleOperations).mockResolvedValue(undefined);
    renderTable(manyOps(5));
    await fireEvent.click(screen.getByRole("button", { name: "Enable all" }));
    await Promise.resolve();
    expect(api.bulkToggleOperations).toHaveBeenCalledWith("slack", "row-a", true, []);
  });

  it("hides selection + bulk controls when can_configure is false", () => {
    renderTable(manyOps(3), false);
    expect(screen.queryByLabelText("Select all in operations")).toBeNull();
    expect(screen.queryByRole("button", { name: "Enable all" })).toBeNull();
    expect(screen.getByLabelText("Search operations")).toBeTruthy();
  });

  it("toggles a single operation via the per-op endpoint", async () => {
    vi.mocked(api.toggleConnectorOperation).mockResolvedValue(false);
    renderTable([op({ key: "send", name: "Send", enabled: true })]);
    await fireEvent.click(screen.getByLabelText("Enable Send"));
    await Promise.resolve();
    expect(api.toggleConnectorOperation).toHaveBeenCalledWith("slack", "row-a", "send", false);
  });

  it("deep-links Test and History per operation", async () => {
    renderTable([op({ key: "send", name: "Send" })]);
    await fireEvent.click(screen.getByRole("button", { name: "Test" }));
    expect(router.push).toHaveBeenCalledWith("/connectors/slack/row-a/test?op=send");
    await fireEvent.click(screen.getByRole("button", { name: "History" }));
    expect(router.push).toHaveBeenCalledWith("/connectors/slack/row-a/history?op=send");
  });

  it("shows the system-disabled warning indicator", () => {
    renderTable([op({ key: "send", name: "Send", system_disabled: true, system_disabled_reason: "missing scope" })]);
    expect(screen.getByText(/missing scope/)).toBeTruthy();
  });

  it("renders the empty state when there are no operations", () => {
    renderTable([]);
    expect(screen.getByText("This connector exposes no operations.")).toBeTruthy();
  });

  it("shows an empty-search message when nothing matches", async () => {
    renderTable(manyOps(3));
    const search = screen.getByLabelText("Search operations");
    await fireEvent.input(search, { target: { value: "nomatch-xyz" } });
    expect(screen.getByText(/No operations match/)).toBeTruthy();
  });

  it("keeps row checkboxes independent across rows", async () => {
    renderTable([op({ key: "a", name: "Alpha" }), op({ key: "b", name: "Bravo" })]);
    const alpha = screen.getByLabelText("Select Alpha") as HTMLInputElement;
    const bravo = screen.getByLabelText("Select Bravo") as HTMLInputElement;
    await fireEvent.click(alpha);
    expect(alpha.checked).toBe(true);
    expect(bravo.checked).toBe(false);
  });

  it("scopes within helper imports correctly", () => {
    renderTable([op({ key: "a", name: "Alpha" })]);
    const region = screen.getByText("Operations").closest("section") as HTMLElement;
    expect(within(region).getByText("Alpha")).toBeTruthy();
  });

  it("applies the responsive resp-table wrapper for mobile", () => {
    const { container } = renderTable(manyOps(3));
    expect(container.querySelector(".resp-table-wrap")).toBeTruthy();
    expect(container.querySelector("table.resp-table")).toBeTruthy();
  });
});
