import { describe, test, expect } from "vitest";
import { render, screen, fireEvent, within } from "@testing-library/svelte";
import { tick } from "svelte";
import ConnectorChecklist from "../ConnectorChecklist.svelte";
import type { AgentConnector } from "../../api/team.js";

const c = (id: string, tier: AgentConnector["tier"], extra: Partial<AgentConnector> = {}): AgentConnector => ({
  id, key: id, label: id, description: "", accounts: [], ops: [], tier, ...extra,
});
const catalog: AgentConnector[] = [
  c("slack", "", { label: "Slack", accounts: [{ id: "", display_name: "Bot / instance" }, { id: "acc-me", display_name: "me" }] }),
  c("http", "", { label: "HTTP" }),
  c("notes", "platform", { label: "Notes" }),
  c("tool:todo", "platform", { label: "Todo list", tool: true }),
  c("wm", "system", { label: "Wick Manager" }),
];

describe("ConnectorChecklist", () => {
  test("type chips split the lists; System hidden without items", async () => {
    const { unmount } = render(ConnectorChecklist, { props: { catalog, grants: [] } });
    expect(screen.getByRole("tab", { name: /Connectors/ })).toBeTruthy();
    expect(screen.getByRole("tab", { name: /System/ })).toBeTruthy();
    // Connectors: one list, granted or not, every row at Off.
    expect(screen.getByText("HTTP")).toBeTruthy();
    expect(screen.getAllByRole("radio", { name: "Off", checked: true }).length).toBe(2);
    expect(screen.queryByText(/\+ Read|Done adding|Set level for/)).toBeNull();
    await fireEvent.click(screen.getByRole("tab", { name: /Platform/ }));
    expect(screen.getByText("Notes")).toBeTruthy();
    expect(screen.queryByText("Slack")).toBeNull();
    expect(screen.getAllByRole("radio", { name: "Default (Write)" }).length).toBe(1);
    // wick's own tools only know on and off.
    expect(screen.getAllByRole("radio", { name: "Default (On)" }).length).toBe(1);
    unmount();
    render(ConnectorChecklist, { props: { catalog: catalog.filter((x) => x.tier !== "system"), grants: [] } });
    expect(screen.queryByRole("tab", { name: /System/ })).toBeNull();
  });

  test("System defaults to Write only for the Captain", async () => {
    render(ConnectorChecklist, { props: { catalog, grants: [], isCaptain: true } });
    await fireEvent.click(screen.getByRole("tab", { name: /System/ }));
    expect(screen.getByRole("radio", { name: "Default (Write)" })).toBeTruthy();
  });

  test("override shows Custom; the Default choice resets it", async () => {
    render(ConnectorChecklist, { props: { catalog, grants: [{ connector_id: "notes", accounts: [], level: "off", ops: [] }] } });
    await fireEvent.click(screen.getByRole("tab", { name: /Platform/ }));
    expect(screen.getByText("Custom")).toBeTruthy();
    await fireEvent.click(screen.getByRole("radio", { name: "Default (Write)" }));
    await tick();
    expect(screen.queryByText("Custom")).toBeNull();
  });

  test("account sub-rows untick independently", async () => {
    render(ConnectorChecklist, { props: { catalog, grants: [{ connector_id: "slack", accounts: [], level: "read", ops: [] }] } });
    const bot = screen.getByLabelText(/Bot \/ instance/) as HTMLInputElement;
    const me = screen.getByLabelText(/@me \(yours\)/) as HTMLInputElement;
    expect(bot.checked && me.checked).toBe(true);
    await fireEvent.click(bot);
    await tick();
    expect((screen.getByLabelText(/Bot \/ instance/) as HTMLInputElement).checked).toBe(false);
    expect((screen.getByLabelText(/@me \(yours\)/) as HTMLInputElement).checked).toBe(true);
  });

  test("include-new and write ops live on the Connectors chip only", async () => {
    const withWrite = catalog.map((x) => (x.id === "http" ? { ...x, ops: [{ key: "post_req", name: "Post", destructive: true }] } : x));
    render(ConnectorChecklist, { props: { catalog: withWrite, grants: [{ connector_id: "http", accounts: [], level: "all", ops: [] }] } });
    expect(screen.getAllByText("Open other connectors read-only").length).toBeGreaterThan(0);
    const card = screen.getByText(/Write operations allowed \(1\)/).closest("details") as HTMLDetailsElement;
    expect(card.open).toBe(false);
    expect(card.textContent).toContain("post_req");
    await fireEvent.click(screen.getByRole("tab", { name: /Platform/ }));
    expect(screen.queryByText("Open other connectors read-only")).toBeNull();
    expect(screen.queryByText(/Write operations allowed/)).toBeNull();
  });

  const levelOf = (label: string) =>
    (within(screen.getByRole("radiogroup", { name: `Access level for ${label}` })).getByRole("radio", { checked: true }) as HTMLElement).textContent;

  test("checkboxes only select; the bulk bar appears with a selection and acts on it", async () => {
    render(ConnectorChecklist, { props: { catalog, grants: [] } });
    expect(screen.queryByRole("toolbar", { name: "Bulk actions" })).toBeNull();
    await fireEvent.click(screen.getByLabelText("Select HTTP"));
    await tick();
    expect(levelOf("HTTP")).toBe("Off");
    const bar = screen.getByRole("toolbar", { name: "Bulk actions" });
    expect(bar.textContent).toContain("1 selected");
    await fireEvent.click(within(bar).getByRole("button", { name: "Set Write" }));
    await tick();
    expect(levelOf("HTTP")).toBe("Write");
    expect(levelOf("Slack")).toBe("Off");
    await fireEvent.click(within(bar).getByRole("button", { name: "Remove access" }));
    await tick();
    expect(levelOf("HTTP")).toBe("Off");
    await fireEvent.click(within(bar).getByRole("button", { name: "Clear" }));
    await tick();
    expect(screen.queryByRole("toolbar", { name: "Bulk actions" })).toBeNull();
  });

  test("select all picks the shown rows, indeterminate when partial", async () => {
    render(ConnectorChecklist, { props: { catalog, grants: [] } });
    const all = screen.getByLabelText("Select all shown") as HTMLInputElement;
    await fireEvent.click(screen.getByLabelText("Select Slack"));
    await tick();
    expect(all.indeterminate).toBe(true);
    await fireEvent.input(screen.getByRole("searchbox"), { target: { value: "htt" } });
    await tick();
    await fireEvent.click(screen.getByLabelText("Select all shown"));
    await tick();
    // HTTP shown and selected; Slack, hidden by the search, stays selected.
    expect(screen.getByRole("toolbar", { name: "Bulk actions" }).textContent).toContain("2 selected");
  });

  test("Granted filter and a row's own level control", async () => {
    render(ConnectorChecklist, { props: { catalog, grants: [{ connector_id: "slack", accounts: [], level: "read", ops: [] }] } });
    expect(screen.getByRole("button", { name: /Granted · 1/ })).toBeTruthy();
    await fireEvent.click(screen.getByRole("button", { name: /^Granted/ }));
    await tick();
    expect(screen.queryByText("HTTP")).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: /Not granted · 1/ }));
    await tick();
    within(screen.getByRole("radiogroup", { name: "Access level for HTTP" })).getByRole("radio", { name: "Read" }).click();
    await tick();
    expect(screen.getByRole("button", { name: /Granted · 2/ })).toBeTruthy();
  });

  test("Same as me replaces the Connectors list; tier chips stay editable", async () => {
    render(ConnectorChecklist, { props: { catalog, grants: [], accessMode: "owner" } });
    expect(screen.getByRole("radio", { name: "Same as me", checked: true })).toBeTruthy();
    expect(screen.getByTestId("same-as-me")).toBeTruthy();
    expect(screen.queryByText("HTTP")).toBeNull();
    expect(screen.queryByText("Open other connectors read-only")).toBeNull();
    await fireEvent.click(screen.getByRole("tab", { name: /Platform/ }));
    expect(screen.getByText("Notes")).toBeTruthy();
    await fireEvent.click(screen.getByRole("tab", { name: /Connectors/ }));
    await fireEvent.click(screen.getByRole("radio", { name: "Choose connectors" }));
    await tick();
    expect(screen.getByText("HTTP")).toBeTruthy();
  });
});
