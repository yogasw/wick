import { describe, test, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import AssigneeFilter from "../AssigneeFilter.svelte";

const people = [
  { id: "u-w", name: "Wildhan Ibrahim Amin" },
  { id: "u-i", name: "Iqbal Khairil Putra" },
];

function renderFilter(selected: string[] = [], me: string | null = "u-me") {
  const onChange = vi.fn();
  render(AssigneeFilter, { props: { selected, people, me: me ?? undefined, onChange } });
  return { onChange };
}

const openIt = () => fireEvent.click(screen.getByTestId("assignee-filter-button"));
const box = (name: string | RegExp) => screen.getByRole("checkbox", { name }) as HTMLInputElement;

describe("AssigneeFilter", () => {
  test("the button sums up the pick", () => {
    renderFilter([]);
    expect(screen.getByTestId("assignee-filter-button").textContent).toContain("Assignee: Everyone");
  });

  test("several picks read as two first names and a count", () => {
    renderFilter(["me", "u-w", "u-i"]);
    expect(screen.getByTestId("assignee-filter-button").textContent).toContain("Assignee: Me, Wildhan +1");
  });

  test("opens on Everyone, Unassigned, Me, then the people", async () => {
    renderFilter([]);
    await openIt();
    const names = screen.getAllByRole("checkbox").map((b) => b.closest("label")!.textContent!.trim());
    expect(names[0]).toMatch(/^Everyone/);
    expect(names.slice(1)).toEqual(["Unassigned", "Me", "Wildhan Ibrahim Amin", "Iqbal Khairil Putra"]);
    expect(box(/Everyone/).checked).toBe(true);
    expect(screen.getByTestId("assignee-filter-button").getAttribute("aria-expanded")).toBe("true");
  });

  test("ticking adds to the list, unticking removes", async () => {
    const { onChange } = renderFilter(["me"]);
    await openIt();
    await fireEvent.click(box("Wildhan Ibrahim Amin"));
    expect(onChange).toHaveBeenLastCalledWith(["me", "u-w"]);
    await fireEvent.click(box("Me"));
    expect(onChange).toHaveBeenLastCalledWith([]);
  });

  test("Unassigned is the none value", async () => {
    const { onChange } = renderFilter([]);
    await openIt();
    await fireEvent.click(box("Unassigned"));
    expect(onChange).toHaveBeenLastCalledWith(["none"]);
  });

  test("Everyone clears the rest", async () => {
    const { onChange } = renderFilter(["u-w", "none"]);
    await openIt();
    expect(box(/Everyone/).checked).toBe(false);
    await fireEvent.click(box(/Everyone/));
    expect(onChange).toHaveBeenLastCalledWith([]);
  });

  test("search narrows the people, not the fixed rows", async () => {
    renderFilter([]);
    await openIt();
    await fireEvent.input(screen.getByTestId("assignee-filter-search"), { target: { value: "iqb" } });
    expect(screen.queryByRole("checkbox", { name: "Wildhan Ibrahim Amin" })).toBeNull();
    expect(box("Iqbal Khairil Putra")).toBeTruthy();
    expect(box("Me")).toBeTruthy();
  });

  test("no viewer, no Me row", async () => {
    renderFilter([], null);
    await openIt();
    expect(screen.queryByRole("checkbox", { name: "Me" })).toBeNull();
  });

  test("a ticked id the board no longer names keeps a row", async () => {
    renderFilter(["u-gone"]);
    await openIt();
    expect(box("u-gone").checked).toBe(true);
  });

  test("Escape closes and hands focus back to the button", async () => {
    renderFilter([]);
    await openIt();
    const search = screen.getByTestId("assignee-filter-search");
    expect(document.activeElement).toBe(search);
    await fireEvent.keyDown(search, { key: "Escape" });
    expect(screen.queryByTestId("assignee-filter-search")).toBeNull();
    expect(document.activeElement).toBe(screen.getByTestId("assignee-filter-button"));
  });

  test("arrow keys walk the search box and the boxes", async () => {
    renderFilter([]);
    await openIt();
    const search = screen.getByTestId("assignee-filter-search");
    await fireEvent.keyDown(search, { key: "ArrowDown" });
    expect(document.activeElement).toBe(box(/Everyone/));
    await fireEvent.keyDown(document.activeElement!, { key: "ArrowDown" });
    expect(document.activeElement).toBe(box("Unassigned"));
    await fireEvent.keyDown(document.activeElement!, { key: "ArrowUp" });
    await fireEvent.keyDown(document.activeElement!, { key: "ArrowUp" });
    expect(document.activeElement).toBe(search);
  });

  test("ArrowDown on the button opens it", async () => {
    renderFilter([]);
    await fireEvent.keyDown(screen.getByTestId("assignee-filter-button"), { key: "ArrowDown" });
    expect(screen.getByTestId("assignee-filter-search")).toBeTruthy();
  });

  test("a click outside closes it", async () => {
    renderFilter([]);
    await openIt();
    await fireEvent.pointerDown(document.body);
    expect(screen.queryByTestId("assignee-filter-search")).toBeNull();
  });

  test("a click inside keeps it open", async () => {
    renderFilter([]);
    await openIt();
    await fireEvent.pointerDown(box("Unassigned"));
    expect(screen.getByTestId("assignee-filter-search")).toBeTruthy();
  });
});
