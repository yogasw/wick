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
    // Connectors: nothing added yet → the empty state, not the available rows.
    expect(screen.getByTestId("access-empty").textContent).toContain("No connectors yet");
    expect(screen.queryByText("HTTP")).toBeNull();
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

  test("override shows Changed; the Default choice resets it", async () => {
    render(ConnectorChecklist, { props: { catalog, grants: [{ connector_id: "notes", accounts: [], level: "off", ops: [] }] } });
    await fireEvent.click(screen.getByRole("tab", { name: /Platform/ }));
    expect(screen.getByText("Changed")).toBeTruthy();
    await fireEvent.click(screen.getByRole("radio", { name: "Default (Write)" }));
    await tick();
    expect(screen.queryByText("Changed")).toBeNull();
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
  const slackRead = [{ connector_id: "slack", accounts: [], level: "read" as const, ops: [] }];

  test("the Add picker lists only what is not added; ticking only picks", async () => {
    render(ConnectorChecklist, { props: { catalog, grants: slackRead } });
    expect(screen.getByText("Slack")).toBeTruthy();
    expect(screen.queryByText("HTTP")).toBeNull();
    await fireEvent.click(screen.getByRole("button", { name: "+ Add connectors" }));
    const picker = screen.getByRole("region", { name: "Add connectors" });
    expect(within(picker).queryByText("Slack")).toBeNull();
    expect(picker.textContent).toContain("1 available");
    const addRead = within(picker).getByRole("button", { name: "Add 0 as Read" }) as HTMLButtonElement;
    expect(addRead.disabled).toBe(true);
    await fireEvent.click(within(picker).getByLabelText("Select all available shown"));
    await tick();
    await fireEvent.click(within(picker).getByRole("button", { name: "Add 1 as Write" }));
    await tick();
    // Added rows move to the main list; the picker closes.
    expect(screen.queryByRole("region", { name: "Add connectors" })).toBeNull();
    expect(levelOf("HTTP")).toBe("Write");
    expect(levelOf("Slack")).toBe("Read");
    await fireEvent.click(screen.getByRole("button", { name: "+ Add connectors" }));
    expect(screen.getByText("Every connector is already added.")).toBeTruthy();
    await fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  });

  test("picker select-all is indeterminate when partial and follows the search", async () => {
    const more = [...catalog, c("loki", "", { label: "Loki" })];
    render(ConnectorChecklist, { props: { catalog: more, grants: [] } });
    await fireEvent.click(screen.getByRole("button", { name: "+ Add connectors" }));
    const all = screen.getByLabelText("Select all available shown") as HTMLInputElement;
    await fireEvent.click(screen.getByLabelText("Pick Loki"));
    await tick();
    expect(all.indeterminate).toBe(true);
    await fireEvent.input(screen.getByRole("searchbox", { name: "Search available connectors" }), { target: { value: "htt" } });
    await tick();
    await fireEvent.click(screen.getByLabelText("Select all available shown"));
    await tick();
    expect((screen.getByRole("button", { name: "Add 2 as Read" }) as HTMLButtonElement).disabled).toBe(false);
  });

  test("bulk bar appears only with a selection on the added list and acts on it", async () => {
    const two = [...slackRead, { connector_id: "http", accounts: [], level: "read" as const, ops: [] }];
    render(ConnectorChecklist, { props: { catalog, grants: two } });
    expect(screen.queryByRole("toolbar", { name: "Bulk actions" })).toBeNull();
    await fireEvent.click(screen.getByLabelText("Select HTTP"));
    await tick();
    expect((screen.getByLabelText("Select all shown") as HTMLInputElement).indeterminate).toBe(true);
    const bar = screen.getByRole("toolbar", { name: "Bulk actions" });
    expect(bar.textContent).toContain("1 selected");
    await fireEvent.click(within(bar).getByRole("button", { name: "Set Write" }));
    await tick();
    expect(levelOf("HTTP")).toBe("Write");
    expect(levelOf("Slack")).toBe("Read");
    await fireEvent.click(within(bar).getByRole("button", { name: "Remove" }));
    await tick();
    expect(screen.queryByText("HTTP")).toBeNull();
    expect(screen.queryByRole("toolbar", { name: "Bulk actions" })).toBeNull();
  });

  test("levels are explained in plain words", () => {
    render(ConnectorChecklist, { props: { catalog, grants: slackRead } });
    expect(screen.getByText(/Read: can look things up · Write: can also change things/)).toBeTruthy();
    expect(screen.getByText("Connectors this agent can use.")).toBeTruthy();
  });

  test("Same as me replaces the Connectors list; tier tabs stay editable", async () => {
    render(ConnectorChecklist, { props: { catalog, grants: [], accessMode: "owner" } });
    expect(screen.getByRole("radio", { name: "Same as me", checked: true })).toBeTruthy();
    expect(screen.getByTestId("same-as-me")).toBeTruthy();
    expect(screen.queryByTestId("access-empty")).toBeNull();
    expect(screen.queryByText("Open other connectors read-only")).toBeNull();
    await fireEvent.click(screen.getByRole("tab", { name: /Platform/ }));
    expect(screen.getByText("Notes")).toBeTruthy();
    await fireEvent.click(screen.getByRole("tab", { name: /Connectors/ }));
    await fireEvent.click(screen.getByRole("radio", { name: "Choose connectors" }));
    await tick();
    expect(screen.getByTestId("access-empty")).toBeTruthy();
  });
});
