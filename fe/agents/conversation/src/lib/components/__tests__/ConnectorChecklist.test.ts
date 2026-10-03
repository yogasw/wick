import { describe, test, expect } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
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
    // Connectors: nothing granted, unticked ones not rendered until searched.
    expect(screen.getByText(/No connectors granted yet/)).toBeTruthy();
    expect(screen.queryByText("HTTP")).toBeNull();
    await fireEvent.click(screen.getByRole("tab", { name: /Platform/ }));
    expect(screen.getByText("Notes")).toBeTruthy();
    expect(screen.queryByText("Slack")).toBeNull();
    expect(screen.getAllByRole("radio", { name: "Default (Write)" }).length).toBe(2);
    unmount();
    render(ConnectorChecklist, { props: { catalog: catalog.filter((x) => x.tier !== "system"), grants: [] } });
    expect(screen.queryByRole("tab", { name: /System/ })).toBeNull();
  });

  test("System defaults to Write only for the Captain", async () => {
    render(ConnectorChecklist, { props: { catalog, grants: [], isCaptain: true } });
    await fireEvent.click(screen.getByRole("tab", { name: /System/ }));
    expect(screen.getByRole("radio", { name: "Default (Write)" })).toBeTruthy();
  });

  test("override shows Custom and Reset", async () => {
    render(ConnectorChecklist, { props: { catalog, grants: [{ connector_id: "notes", accounts: [], level: "off", ops: [] }] } });
    await fireEvent.click(screen.getByRole("tab", { name: /Platform/ }));
    expect(screen.getByText("Custom")).toBeTruthy();
    await fireEvent.click(screen.getByRole("button", { name: /Reset Notes/ }));
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
});
