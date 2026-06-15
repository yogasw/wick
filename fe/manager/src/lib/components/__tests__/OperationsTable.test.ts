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

  it("paginates with page size 10 and shows the legacy 'Showing X–Y of N' footer", () => {
    renderTable(manyOps(25));
    expect(screen.getByText("Showing 1–10 of 25")).toBeTruthy();
    expect(screen.getByText("Operation 1")).toBeTruthy();
    expect(screen.getByText("Operation 10")).toBeTruthy();
    expect(screen.queryByText("Operation 11")).toBeNull();
  });

  it("advances to the next page and back, updating the footer and rows", async () => {
    renderTable(manyOps(25));
    await fireEvent.click(screen.getByRole("button", { name: "Next →" }));
    expect(screen.getByText("Showing 11–20 of 25")).toBeTruthy();
    expect(screen.getByText("Operation 11")).toBeTruthy();
    expect(screen.queryByText("Operation 10")).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: "← Prev" }));
    expect(screen.getByText("Showing 1–10 of 25")).toBeTruthy();
    expect(screen.getByText("Operation 1")).toBeTruthy();
  });

  it("disables Prev on the first page and Next on the last page", async () => {
    renderTable(manyOps(25));
    const prev = screen.getByRole("button", { name: "← Prev" }) as HTMLButtonElement;
    const next = screen.getByRole("button", { name: "Next →" }) as HTMLButtonElement;
    expect(prev.disabled).toBe(true);
    expect(next.disabled).toBe(false);
    await fireEvent.click(next);
    await fireEvent.click(next);
    expect(screen.getByText("Showing 21–25 of 25")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Next →" }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: "← Prev" }) as HTMLButtonElement).disabled).toBe(false);
  });

  it("filters rows by name, key, and description and resets to page 1", async () => {
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
    expect(screen.getByText("Showing 1–1 of 1")).toBeTruthy();
    await fireEvent.input(search, { target: { value: "list_users" } });
    expect(screen.getByText("List Users")).toBeTruthy();
    expect(screen.queryByText("Send Message")).toBeNull();
    await fireEvent.input(search, { target: { value: "Delete" } });
    expect(screen.getByText("Delete Message")).toBeTruthy();
  });

  it("paginates over the filtered list and resets to page 1 on a new query", async () => {
    const ops = [...manyOps(15), op({ key: "zzz", name: "Zebra", description: "stripes" })];
    renderTable(ops);
    await fireEvent.click(screen.getByRole("button", { name: "Next →" }));
    expect(screen.getByText("Showing 11–16 of 16")).toBeTruthy();
    const search = screen.getByLabelText("Search operations");
    await fireEvent.input(search, { target: { value: "Operation" } });
    expect(screen.getByText("Showing 1–10 of 15")).toBeTruthy();
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

  it("select-all on the current page selects every visible row", async () => {
    vi.mocked(api.bulkToggleOperations).mockResolvedValue(undefined);
    renderTable(manyOps(12));
    await fireEvent.click(screen.getByLabelText("Select all on this page"));
    expect(screen.getByText("10 selected")).toBeTruthy();
    await fireEvent.click(screen.getByRole("button", { name: "Disable selected" }));
    await Promise.resolve();
    const call = vi.mocked(api.bulkToggleOperations).mock.calls[0];
    expect(call[0]).toBe("slack");
    expect(call[1]).toBe("row-a");
    expect(call[2]).toBe(false);
    expect(call[3]).toHaveLength(10);
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
    expect(screen.queryByLabelText("Select all on this page")).toBeNull();
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

  it("uses an en-dash and shows 0 when filtered to nothing", async () => {
    renderTable(manyOps(3));
    const search = screen.getByLabelText("Search operations");
    await fireEvent.input(search, { target: { value: "nomatch-xyz" } });
    expect(screen.getByText("Showing 0–0 of 0")).toBeTruthy();
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
});
