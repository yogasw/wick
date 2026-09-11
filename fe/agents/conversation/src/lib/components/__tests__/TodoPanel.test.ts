import { describe, test, expect } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import TodoPanel from "../TodoPanel.svelte";
import type { TodoList } from "../../api/todos.js";

function list(over: Partial<TodoList> = {}): TodoList {
  return {
    items: [
      { label: "read the logs", status: "completed" },
      { label: "write the fix", status: "in_progress" },
      { label: "ship it", status: "pending" },
    ],
    total: 3,
    completed: 1,
    done: false,
    started_at: new Date(Date.now() - 120_000).toISOString(),
    updated_at: new Date(Date.now() - 5_000).toISOString(),
    ...over,
  };
}

const noop = () => {};

describe("TodoPanel", () => {
  test("shows the active checklist and its progress", () => {
    render(TodoPanel, { active: list(), history: [], loading: false, error: null, onRefresh: noop });
    expect(screen.getByText("read the logs")).toBeTruthy();
    expect(screen.getByText("ship it")).toBeTruthy();
    expect(screen.getByText("1/3 done")).toBeTruthy();
  });

  test("says nothing is there rather than showing an empty box", () => {
    // A panel that appears blank reads as broken; it has to say why it is empty.
    render(TodoPanel, { active: null, history: [], loading: false, error: null, onRefresh: noop });
    expect(screen.getByText(/No checklist yet/i)).toBeTruthy();
  });

  test("only shows a spinner on the FIRST load", () => {
    // Replacing a list already on screen with "Loading…" hides the very
    // thing the panel exists to show, every time the agent ticks an item.
    const { queryByText } = render(TodoPanel, {
      active: list(),
      history: [],
      loading: true,
      error: null,
      onRefresh: noop,
    });
    expect(queryByText(/Loading the checklist/i)).toBeNull();
    expect(screen.getByText("write the fix")).toBeTruthy();
  });

  test("shows the loading line when there is nothing yet", () => {
    render(TodoPanel, { active: null, history: [], loading: true, error: null, onRefresh: noop });
    expect(screen.getByText(/Loading the checklist/i)).toBeTruthy();
  });

  test("earlier lists are collapsed to one line", () => {
    // The header names the list by its first item, so THAT stays visible —
    // what must not be there is the rest of it, which would turn the
    // history into a second thing to read top to bottom.
    const past = list({
      items: [
        { label: "an older task", status: "completed" },
        { label: "its second step", status: "completed" },
      ],
      total: 2,
      completed: 2,
      done: true,
    });
    render(TodoPanel, { active: list(), history: [past], loading: false, error: null, onRefresh: noop });
    expect(screen.getByText("Earlier (1)")).toBeTruthy();
    expect(screen.getByText("an older task")).toBeTruthy();
    expect(screen.queryByText("its second step")).toBeNull();
  });

  test("an earlier list expands on click", async () => {
    const past = list({
      items: [
        { label: "an older task", status: "completed" },
        { label: "its second step", status: "completed" },
      ],
      total: 2,
      completed: 2,
      done: true,
    });
    render(TodoPanel, { active: null, history: [past], loading: false, error: null, onRefresh: noop });
    await fireEvent.click(screen.getByText("an older task"));
    expect(screen.getByText("its second step")).toBeTruthy();
  });

  test("refresh is wired to the caller", async () => {
    let hits = 0;
    render(TodoPanel, {
      active: list(),
      history: [],
      loading: false,
      error: null,
      onRefresh: () => { hits++; },
    });
    await fireEvent.click(screen.getByTitle("Reload from the server"));
    expect(hits).toBe(1);
  });

  test("a finished list says so", () => {
    render(TodoPanel, {
      active: list({ completed: 3, done: true, items: [{ label: "done thing", status: "completed" }], total: 1 }),
      history: [],
      loading: false,
      error: null,
      onRefresh: noop,
    });
    expect(screen.getByText("Finished")).toBeTruthy();
  });
});
