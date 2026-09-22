import { describe, test, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/svelte";
import AssigneePicker from "../AssigneePicker.svelte";

const people = [
  { id: "u-a", name: "Ana" },
  { id: "u-b", name: "Budi" },
  { id: "u-c", name: "Citra" },
];

function stubList(body: unknown = { assignees: people }, status = 200) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () =>
      new Response(JSON.stringify(body), {
        status,
        headers: { "Content-Type": "application/json" },
      }),
    ),
  );
}

beforeEach(() => stubList());

function renderPicker(over: Record<string, unknown> = {}) {
  const onChange = vi.fn();
  render(AssigneePicker, {
    props: {
      base: "/tools/agents",
      projectId: "p1",
      assignees: ["u-a"],
      users: { "u-a": "Ana" },
      me: "u-c",
      onChange,
      ...over,
    },
  });
  return onChange;
}

describe("AssigneePicker", () => {
  test("names the people on the ticket, not their ids", () => {
    renderPicker();
    expect(screen.getByTestId("assignee-picker").textContent).toContain("Ana");
    expect(screen.getByTestId("assignee-picker").textContent).not.toContain("u-a");
  });

  test("the dropdown offers everyone not already on it", async () => {
    renderPicker();
    await waitFor(() => {
      const opts = [...screen.getByTestId("assignee-add").querySelectorAll("option")]
        .map((o) => o.textContent);
      expect(opts).toEqual(["Add someone…", "Budi", "Citra"]);
    });
  });

  // The whole point: a second person is ADDED, not swapped in.
  test("picking someone adds them to the list", async () => {
    const onChange = renderPicker();
    await waitFor(() => expect(screen.getByTestId("assignee-add")).toBeTruthy());
    await fireEvent.change(screen.getByTestId("assignee-add"), { target: { value: "u-b" } });
    expect(onChange).toHaveBeenCalledWith(["u-a", "u-b"]);
  });

  test("a chip's × takes that person off", async () => {
    const onChange = renderPicker({ assignees: ["u-a", "u-b"], users: { "u-a": "Ana", "u-b": "Budi" } });
    (screen.getByLabelText("Remove Ana") as HTMLButtonElement).click();
    expect(onChange).toHaveBeenCalledWith(["u-b"]);
  });

  // "take it" used to replace whoever was there. On a shared ticket that
  // silently removed a colleague.
  test("take it joins, it does not evict", () => {
    const onChange = renderPicker();
    (screen.getByTestId("assignee-take-it") as HTMLButtonElement).click();
    expect(onChange).toHaveBeenCalledWith(["u-a", "u-c"]);
  });

  test("no take-it when the caller is already on it", () => {
    renderPicker({ me: "u-a" });
    expect(screen.queryByTestId("assignee-take-it")).toBeNull();
  });

  // An older server has no assignees endpoint. The chips are the ticket's
  // real state and must survive that.
  test("a failed lookup keeps the people already on the ticket", async () => {
    stubList({ error: "not found" }, 404);
    renderPicker();
    await waitFor(() =>
      expect(screen.getByTestId("assignee-picker").textContent).toContain("Ana"),
    );
    expect(screen.queryByTestId("assignee-add")).toBeNull();
  });

  test("says so when nobody is on it", () => {
    renderPicker({ assignees: [] });
    expect(screen.getByTestId("assignee-picker").textContent).toContain("Unassigned");
  });
});
