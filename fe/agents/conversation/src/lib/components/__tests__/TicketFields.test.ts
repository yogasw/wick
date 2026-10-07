import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import TicketFields from "../TicketFields.svelte";
import type { TicketField } from "../../types/agents.js";

const defs: TicketField[] = [
  { key: "app_code", label: "App Code", type: "text" },
  { key: "slack", label: "Slack discussion", type: "text" },
  { key: "helpdesk", label: "Helpdesk ticket", type: "text" },
  { key: "priority", label: "Priority", type: "select", options: ["Low", "High"] },
  { key: "changelog", label: "Change Log", type: "text" },
  { key: "owner", label: "Owner", type: "text", required: true },
];
const values = {
  app_code: "app-abc123",
  slack: "https://abc.slack.com/archives/C0123/p1700000000000001",
  helpdesk: "Helpdesk ticket : https://helpdesk.example.com/tickets/1001",
  owner: "Yoga",
  notion_page_id: "page-0001-internal",
};

function renderFields(over: Record<string, unknown> = {}) {
  const onSave = vi.fn(async () => {});
  const r = render(TicketFields, { props: { fields: defs, values, onSave, ...over } });
  return { ...r, onSave };
}
const tick = () => new Promise((r) => setTimeout(r, 0));

describe("TicketFields — reading", () => {
  test("only defined fields show; a key nobody defined stays hidden", () => {
    renderFields({ initialVisible: 10 });
    expect(screen.getByText("app-abc123")).toBeTruthy();
    expect(screen.queryByText("page-0001-internal")).toBeNull();
  });

  test("a URL is a link, with an open-in-new-tab button that is safe", () => {
    renderFields();
    const open = screen.getByTestId("ticket-field-open-slack") as HTMLAnchorElement;
    expect(open.getAttribute("href")).toBe(values.slack);
    expect(open.getAttribute("target")).toBe("_blank");
    expect(open.getAttribute("rel")).toContain("noopener");
    const inline = screen.getByText(values.slack) as HTMLAnchorElement;
    expect(inline.tagName).toBe("A");
    expect(inline.getAttribute("target")).toBe("_blank");
  });

  test("a URL inside text is linked and the text around it kept", () => {
    renderFields({ initialVisible: 10 });
    const row = screen.getByTestId("ticket-field-helpdesk");
    expect(row.textContent).toContain("Helpdesk ticket :");
    expect((screen.getByTestId("ticket-field-open-helpdesk") as HTMLAnchorElement).getAttribute("href")).toBe(
      "https://helpdesk.example.com/tickets/1001",
    );
  });

  test("a value without a URL gets no open button", () => {
    renderFields();
    expect(screen.queryByTestId("ticket-field-open-app_code")).toBeNull();
  });

  // An empty field says nothing about the ticket; a column of "Add …" slots
  // only pushed the values that do off the panel.
  test("empty fields are not listed at all", () => {
    renderFields({ initialVisible: 10 });
    expect(screen.queryByTestId("ticket-field-priority")).toBeNull();
    expect(screen.queryByTestId("ticket-field-changelog")).toBeNull();
    expect(screen.queryByText(/^\s*Add /)).toBeNull();
  });

  test("two filled rows at rest; Show more counts every other field, empty ones too", async () => {
    renderFields();
    // filled, in project order: app_code, slack, helpdesk, owner; empty: priority, changelog
    expect(screen.getByTestId("ticket-field-app_code")).toBeTruthy();
    expect(screen.getByTestId("ticket-field-slack")).toBeTruthy();
    expect(screen.queryByTestId("ticket-field-helpdesk")).toBeNull();
    const toggle = screen.getByTestId("ticket-fields-toggle");
    expect(toggle.textContent).toContain("Show more (4)");
    await fireEvent.click(toggle);
    for (const d of defs) expect(screen.getByTestId(`ticket-field-${d.key}`)).toBeTruthy();
    expect(screen.getByTestId("ticket-field-add-priority").textContent).toContain("Add");
    expect(screen.getByTestId("ticket-fields-toggle").textContent).toContain("Show less");
  });

  test("nothing filled: no rows, and the toggle reads Add custom fields", async () => {
    renderFields({ values: {} });
    expect(screen.queryByTestId("ticket-fields-list")).toBeNull();
    expect(screen.getByTestId("ticket-fields-toggle").textContent).toContain("Add custom fields (6)");
    await fireEvent.click(screen.getByTestId("ticket-fields-toggle"));
    expect(screen.getByTestId("ticket-field-add-app_code")).toBeTruthy();
  });

  test("the toggle sits below the rows it folds", () => {
    const { container } = renderFields();
    const list = container.querySelector("[data-testid=ticket-fields-list]")!;
    const toggle = screen.getByTestId("ticket-fields-toggle");
    expect(list.compareDocumentPosition(toggle) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  test("no toggle when every field is filled and fits", () => {
    renderFields({ fields: defs.slice(0, 2), values: { app_code: "a", slack: "b" } });
    expect(screen.queryByTestId("ticket-fields-toggle")).toBeNull();
  });
});

describe("TicketFields — editing in place", () => {
  test("Enter saves the trimmed value for that one key", async () => {
    const { onSave } = renderFields();
    await fireEvent.click(screen.getByTestId("ticket-field-edit-app_code"));
    const input = screen.getByTestId("ticket-field-input-app_code") as HTMLInputElement;
    await fireEvent.input(input, { target: { value: "  app-new  " } });
    await fireEvent.keyDown(input, { key: "Enter" });
    await tick();
    expect(onSave).toHaveBeenCalledWith("app_code", "app-new");
    expect(screen.getByText("app-new")).toBeTruthy();
  });

  // Escape removes the input, which fires blur. That blur must not save the
  // cleared draft — it would erase the field.
  test("Escape discards and never saves, even with the blur that follows", async () => {
    const { onSave } = renderFields();
    await fireEvent.click(screen.getByTestId("ticket-field-edit-app_code"));
    const input = screen.getByTestId("ticket-field-input-app_code") as HTMLInputElement;
    await fireEvent.input(input, { target: { value: "typed then abandoned" } });
    await fireEvent.keyDown(input, { key: "Escape" });
    await fireEvent.blur(input);
    await tick();
    expect(onSave).not.toHaveBeenCalled();
    expect(screen.getByText("app-abc123")).toBeTruthy();
  });

  test("an unchanged value does not call the server", async () => {
    const { onSave } = renderFields();
    await fireEvent.click(screen.getByTestId("ticket-field-edit-app_code"));
    await fireEvent.keyDown(screen.getByTestId("ticket-field-input-app_code"), { key: "Enter" });
    await tick();
    expect(onSave).not.toHaveBeenCalled();
  });

  test("blur saves too", async () => {
    const { onSave } = renderFields();
    await fireEvent.click(screen.getByTestId("ticket-field-edit-app_code"));
    const input = screen.getByTestId("ticket-field-input-app_code") as HTMLInputElement;
    await fireEvent.input(input, { target: { value: "v1.2 ships the fix" } });
    await fireEvent.blur(input);
    await tick();
    expect(onSave).toHaveBeenCalledWith("app_code", "v1.2 ships the fix");
  });

  test("a field stays listed while its draft is cleared, and leaves once saved empty", async () => {
    const { onSave } = renderFields();
    await fireEvent.click(screen.getByTestId("ticket-field-edit-slack"));
    const input = screen.getByTestId("ticket-field-input-slack") as HTMLInputElement;
    await fireEvent.input(input, { target: { value: "" } });
    expect(screen.getByTestId("ticket-field-input-slack")).toBeTruthy();
    await fireEvent.keyDown(input, { key: "Enter" });
    await tick();
    expect(onSave).toHaveBeenCalledWith("slack", "");
    expect(screen.queryByTestId("ticket-field-slack")).toBeNull();
  });

  test("a select field saves the picked option", async () => {
    const { onSave } = renderFields({ values: { ...values, priority: "Low" }, initialVisible: 10 });
    await fireEvent.click(screen.getByTestId("ticket-field-edit-priority"));
    const sel = screen.getByTestId("ticket-field-input-priority") as HTMLSelectElement;
    await fireEvent.change(sel, { target: { value: "High" } });
    await tick();
    expect(onSave).toHaveBeenCalledWith("priority", "High");
  });

  test("a required field cannot be cleared", async () => {
    const { onSave } = renderFields({ initialVisible: 10 });
    await fireEvent.click(screen.getByTestId("ticket-field-edit-owner"));
    const input = screen.getByTestId("ticket-field-input-owner") as HTMLInputElement;
    await fireEvent.input(input, { target: { value: "   " } });
    await fireEvent.keyDown(input, { key: "Enter" });
    await tick();
    expect(onSave).not.toHaveBeenCalled();
  });

  test("a failed save keeps the editor open with what was typed", async () => {
    const onSave = vi.fn(async () => { throw new Error("nope"); });
    render(TicketFields, { props: { fields: defs, values, onSave } });
    await fireEvent.click(screen.getByTestId("ticket-field-edit-app_code"));
    const input = screen.getByTestId("ticket-field-input-app_code") as HTMLInputElement;
    await fireEvent.input(input, { target: { value: "will fail" } });
    await fireEvent.keyDown(input, { key: "Enter" });
    await tick();
    expect((screen.getByTestId("ticket-field-input-app_code") as HTMLInputElement).value).toBe("will fail");
  });
});

describe("TicketFields — filling an empty field from the open list", () => {
  test("+ Add on the label row opens the editor and saves", async () => {
    const { onSave } = renderFields();
    await fireEvent.click(screen.getByTestId("ticket-fields-toggle"));
    await fireEvent.click(screen.getByTestId("ticket-field-add-changelog"));
    const input = screen.getByTestId("ticket-field-input-changelog") as HTMLInputElement;
    await fireEvent.input(input, { target: { value: "v1.2 ships the fix" } });
    await fireEvent.keyDown(input, { key: "Enter" });
    await tick();
    expect(onSave).toHaveBeenCalledWith("changelog", "v1.2 ships the fix");
  });
});
