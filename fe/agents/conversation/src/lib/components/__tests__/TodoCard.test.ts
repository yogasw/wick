import { describe, test, expect } from "vitest";
import { render, screen, fireEvent } from "@testing-library/svelte";
import TodoCard from "../TodoCard.svelte";
import type { TodoItemWithSteps } from "../../todoGroups.js";
import type { ThreadBlock } from "../../types/agents.js";

function withSteps(
  items: { step: string; status: string }[],
  relatedBlocks: ThreadBlock[][] = [],
): TodoItemWithSteps[] {
  return items.map((item, i) => ({ item, relatedBlocks: relatedBlocks[i] ?? [] }));
}

describe("TodoCard", () => {
  test("renders a checklist with each step's text", () => {
    render(TodoCard, {
      props: {
        items: withSteps([
          { step: "Delay 1m via shell sleep 60", status: "completed" },
          { step: "Write detailed notes", status: "in_progress" },
          { step: "Bash sleep 900", status: "pending" },
        ]),
      },
    });
    expect(screen.getByText("Delay 1m via shell sleep 60")).toBeDefined();
    expect(screen.getByText("Write detailed notes")).toBeDefined();
    expect(screen.getByText("Bash sleep 900")).toBeDefined();
  });

  test("shows a done-count summary and progress percentage", () => {
    render(TodoCard, {
      props: {
        items: withSteps([
          { step: "a", status: "completed" },
          { step: "b", status: "completed" },
          { step: "c", status: "pending" },
        ]),
      },
    });
    expect(screen.getByText("2/3 done")).toBeDefined();
    expect(screen.getByText("67%")).toBeDefined();
  });

  test("handles an empty item list with a fallback message", () => {
    render(TodoCard, { props: { items: [] } });
    expect(screen.getByText("no items")).toBeDefined();
  });

  test("an item with no related blocks is not clickable and shows no step count", () => {
    render(TodoCard, {
      props: { items: withSteps([{ step: "a", status: "pending" }]) },
    });
    expect(screen.queryByText(/step/)).toBeNull();
  });

  test("an item with related blocks shows a step count and expands on click", async () => {
    const shellBlock: ThreadBlock = { kind: "tool", toolUseId: "s1", toolName: "shell", toolInput: '{"command":"ls"}' };
    render(TodoCard, {
      props: {
        items: withSteps([{ step: "Run tests", status: "completed" }], [[shellBlock]]),
      },
    });
    expect(screen.getByText("1 step")).toBeDefined();
    expect(screen.queryByText("shell")).toBeNull();

    await fireEvent.click(screen.getByText("Run tests"));
    expect(screen.getByText("shell")).toBeDefined();
  });

  test("clicking an expanded item again collapses it", async () => {
    const shellBlock: ThreadBlock = { kind: "tool", toolUseId: "s1", toolName: "shell", toolInput: "{}" };
    render(TodoCard, {
      props: { items: withSteps([{ step: "Run tests", status: "completed" }], [[shellBlock]]) },
    });
    const stepButton = screen.getByText("Run tests");
    await fireEvent.click(stepButton);
    expect(screen.getByText("shell")).toBeDefined();
    await fireEvent.click(stepButton);
    expect(screen.queryByText("shell")).toBeNull();
  });

  test("clicking the header collapses the whole card", async () => {
    render(TodoCard, {
      props: { items: withSteps([{ step: "a", status: "pending" }]) },
    });
    expect(screen.getByText("a")).toBeDefined();
    await fireEvent.click(screen.getByText("todo"));
    expect(screen.queryByText("a")).toBeNull();
  });
});
