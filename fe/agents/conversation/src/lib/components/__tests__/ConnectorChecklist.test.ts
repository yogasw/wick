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
  test("one list for connectors; built-in tools fold away, System rows only for the Captain", async () => {
    render(ConnectorChecklist, { props: { catalog, grants: [] } });
    expect(screen.queryByRole("tab")).toBeNull();
    // Selected with nothing added → the empty state, not the available rows.
    expect(screen.getByTestId("access-empty").textContent).toContain("No connectors yet");
    const built = screen.getByTestId("built-in") as HTMLDetailsElement;
    expect(built.open).toBe(false);
    expect(built.textContent).toContain("2 on by default");
    expect(within(built).getByText("Notes")).toBeTruthy();
    expect(within(built).queryByText("Wick Manager")).toBeNull();
    // wick's own tools only know on and off.
    expect(within(built).getAllByRole("radio", { name: "Default (On)" }).length).toBe(1);
  });

  test("System defaults to Write only for the Captain", () => {
    render(ConnectorChecklist, { props: { catalog, grants: [], isCaptain: true } });
    const built = screen.getByTestId("built-in");
    expect(within(built).getByText("Wick Manager")).toBeTruthy();
    expect(within(built).getAllByRole("radio", { name: "Default (Write)" }).length).toBe(2);
  });

  test("override shows Changed; the Default choice resets it", async () => {
    render(ConnectorChecklist, { props: { catalog, grants: [{ connector_id: "notes", accounts: [], level: "off", ops: [] }] } });
    const built = screen.getByTestId("built-in");
    expect(within(built).getByText("Changed")).toBeTruthy();
    expect(built.textContent).toContain("1 changed");
    await fireEvent.click(within(built).getByRole("radio", { name: "Default (Write)" }));
    await tick();
    expect(within(built).queryByText("Changed")).toBeNull();
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

  test("include-new and write ops belong to Selected only", async () => {
    const withWrite = catalog.map((x) => (x.id === "http" ? { ...x, ops: [{ key: "post_req", name: "Post", destructive: true }] } : x));
    render(ConnectorChecklist, { props: { catalog: withWrite, grants: [{ connector_id: "http", accounts: [], level: "all", ops: [] }] } });
    expect(screen.getAllByText("Open other connectors read-only").length).toBeGreaterThan(0);
    const card = screen.getByText(/Write operations allowed \(1\)/).closest("details") as HTMLDetailsElement;
    expect(card.open).toBe(false);
    expect(card.textContent).toContain("post_req");
    await fireEvent.click(screen.getByRole("radio", { name: "All" }));
    await tick();
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
    expect(picker.textContent).toContain("Available · 1");
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
    expect(screen.getByText(/New connectors stay off until you add them/)).toBeTruthy();
    expect(screen.getByText(/^Picked/).textContent).toContain("· 1");
  });

  test("All replaces the picked list with Turned off; Selected brings it back", async () => {
    render(ConnectorChecklist, { props: { catalog, grants: [], accessMode: "owner" } });
    expect(screen.getByRole("radio", { name: "All", checked: true })).toBeTruthy();
    expect(screen.getByTestId("turned-off")).toBeTruthy();
    expect(screen.queryByTestId("access-empty")).toBeNull();
    expect(screen.queryByText("Open other connectors read-only")).toBeNull();
    await fireEvent.click(screen.getByRole("radio", { name: "Selected" }));
    await tick();
    expect(screen.getByTestId("access-empty")).toBeTruthy();
    expect(screen.queryByTestId("turned-off")).toBeNull();
  });

  test("All: turn connectors off and back on", async () => {
    render(ConnectorChecklist, { props: { catalog, grants: [], accessMode: "owner" } });
    const off = screen.getByTestId("turned-off");
    expect(off.textContent).toContain("Nothing turned off.");
    await fireEvent.click(within(off).getByRole("button", { name: "+ Turn off" }));
    // The picker offers connectors only, never Platform / System rows.
    expect(screen.queryByLabelText("Turn off Notes")).toBeNull();
    await fireEvent.click(screen.getByLabelText("Turn off HTTP"));
    await fireEvent.click(screen.getByRole("button", { name: "Turn off 1" }));
    await tick();
    expect(within(off).getByText("HTTP")).toBeTruthy();
    expect(within(off).getByText(/Turned off/).textContent).toContain("· 1");
    await fireEvent.click(screen.getByRole("button", { name: "Turn HTTP back on" }));
    await tick();
    expect(off.textContent).toContain("Nothing turned off.");
  });

  test("Lend from me: lend, set level, stop; the instance credential is the default account", async () => {
    render(ConnectorChecklist, { props: { catalog, grants: [], accessMode: "owner" } });
    const lend = screen.getByTestId("lend");
    expect(lend.textContent).toContain("Nothing lent.");
    await fireEvent.click(within(lend).getByRole("button", { name: "+ Lend" }));
    // Type chips filter the picker: two types (slack, http) here.
    await fireEvent.click(within(lend).getByRole("button", { name: /^slack/ }));
    expect(screen.queryByLabelText("Lend HTTP")).toBeNull();
    await fireEvent.click(screen.getByLabelText("Lend Slack"));
    await fireEvent.click(screen.getByRole("button", { name: "Lend 1 as Read" }));
    await tick();
    expect(within(lend).getByText("Slack")).toBeTruthy();
    const inst = screen.getByLabelText(/Instance credential/) as HTMLInputElement;
    const mine = screen.getByLabelText(/@me \(yours\)/) as HTMLInputElement;
    expect(inst.checked).toBe(true);
    expect(mine.checked).toBe(false);
    expect(screen.getByTestId("who-gets-what").textContent).toContain("Lent, as you");
    await fireEvent.click(screen.getByRole("button", { name: "Stop lending Slack" }));
    await tick();
    expect(lend.textContent).toContain("Nothing lent.");
  });

  test("a lent connector stays out of the Selected list", () => {
    render(ConnectorChecklist, { props: { catalog, grants: [{ connector_id: "slack", accounts: [""], level: "read", ops: [], lend: true }] } });
    expect(screen.getByTestId("access-empty")).toBeTruthy();
    expect(within(screen.getByTestId("lend")).getByText("Slack")).toBeTruthy();
  });

  test("Who gets what previews each kind of person", async () => {
    render(ConnectorChecklist, { props: { catalog, grants: [{ connector_id: "slack", accounts: [""], level: "read", ops: [], lend: true }], accessMode: "owner", runAs: "caller" } });
    const who = screen.getByTestId("who-gets-what");
    expect(who.textContent).toContain("every connector they have themselves");
    expect(who.textContent).toContain("which you can read");
    expect(within(who).getByText("Slack")).toBeTruthy();
    await fireEvent.click(within(who).getByRole("button", { name: "You" }));
    await tick();
    expect(who.textContent).toContain("every connector you have");
    expect(within(who).queryByText("Slack")).toBeNull();
    await fireEvent.click(within(who).getByRole("button", { name: "Someone with no connectors" }));
    await tick();
    expect(who.textContent).toContain("none of their own");
  });

  test("an agent that runs as its owner says so and offers the way back", async () => {
    render(ConnectorChecklist, { props: { catalog, grants: [], runAs: "owner" } });
    const note = screen.getByTestId("runs-as-owner");
    await fireEvent.click(within(note).getByRole("button", { name: "Use each person's own access" }));
    await tick();
    expect(screen.queryByTestId("runs-as-owner")).toBeNull();
  });
});
